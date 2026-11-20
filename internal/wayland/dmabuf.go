package wayland

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// GPU buffers without a GPU API. udmabuf turns pages of a memfd into a
// dma-buf, which the compositor imports as a texture in place: the GPU
// samples our pixels where they are instead of the compositor copying
// them into a texture on every commit, as it must with wl_shm. We still
// rasterise on the CPU into the same mapping.

const (
	drmFormatXRGB8888 = 0x34325258 // fourcc "XR24"
	drmModLinear      = 0

	mfdCloexec       = 1
	mfdAllowSealing  = 2
	fAddSeals        = 1033
	fSealShrink      = 2
	udmabufFlagCloex = 1
	pageSize         = 4096

	// _IOW('u', 0x42, struct udmabuf_create) and _IOW('b', 0, struct dma_buf_sync).
	udmabufCreate = 0x40187542
	dmaBufSync    = 0x40086200

	dmaBufSyncRW    = 3 // read | write
	dmaBufSyncStart = 0
	dmaBufSyncEnd   = 4
)

// dmabufMemory is one memfd mapped into our address space, cut into n
// dma-bufs of size bytes each.
type dmabufMemory struct {
	memfd int
	data  []byte
	size  int   // bytes per buffer, page aligned
	fds   []int // one dma-buf per buffer
}

// newDMABufMemory creates n page-aligned dma-bufs of at least size bytes.
func newDMABufMemory(n, size int) (*dmabufMemory, error) {
	if sysMemfdCreate == 0 || !canFlushCache {
		return nil, errors.New("dma-bufs are only supported on amd64 for now")
	}
	dev, err := syscall.Open("/dev/udmabuf", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open /dev/udmabuf: %w", err)
	}
	defer syscall.Close(dev)

	size = (size + pageSize - 1) &^ (pageSize - 1)
	name := []byte("orbital-saver-dmabuf\x00")
	fd, _, errno := syscall.Syscall(sysMemfdCreate, uintptr(unsafe.Pointer(&name[0])), mfdCloexec|mfdAllowSealing, 0)
	if errno != 0 {
		return nil, fmt.Errorf("cannot create a memfd: %w", errno)
	}
	m := &dmabufMemory{memfd: int(fd), size: size}
	fail := func(what string, err error) (*dmabufMemory, error) {
		m.release()
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if err := syscall.Ftruncate(m.memfd, int64(n*size)); err != nil {
		return fail("cannot size the memfd", err)
	}
	// udmabuf insists the memory cannot shrink under the GPU.
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(m.memfd), fAddSeals, fSealShrink); errno != 0 {
		return fail("cannot seal the memfd", errno)
	}
	if m.data, err = syscall.Mmap(m.memfd, 0, n*size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED); err != nil {
		return fail("cannot map the memfd", err)
	}
	for i := 0; i < n; i++ {
		req := struct {
			memfd, flags uint32
			offset, size uint64
		}{uint32(m.memfd), udmabufFlagCloex, uint64(i * size), uint64(size)}
		buf, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(dev), udmabufCreate, uintptr(unsafe.Pointer(&req)))
		if errno != 0 {
			return fail("cannot create a udmabuf", errno)
		}
		m.fds = append(m.fds, int(buf))
	}
	return m, nil
}

// pixels views buffer i as n 32-bit pixels.
func (m *dmabufMemory) pixels(i, n int) []uint32 {
	return unsafe.Slice((*uint32)(unsafe.Pointer(&m.data[i*m.size])), n)
}

// beginCPU and endCPU bracket our writes to buffer i, so caches are made
// coherent with the GPU where the hardware needs it. On most x86 machines
// they cost a system call each and do nothing more.
func (m *dmabufMemory) beginCPU(i int) { m.sync(i, dmaBufSyncStart) }
func (m *dmabufMemory) endCPU(i int)   { m.sync(i, dmaBufSyncEnd) }

func (m *dmabufMemory) sync(i int, flags uint64) {
	arg := flags | dmaBufSyncRW
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(m.fds[i]), dmaBufSync, uintptr(unsafe.Pointer(&arg)))
}

func (m *dmabufMemory) release() {
	for _, fd := range m.fds {
		syscall.Close(fd)
	}
	m.fds = nil
	if m.data != nil {
		syscall.Munmap(m.data)
		m.data = nil
	}
	if m.memfd >= 0 {
		syscall.Close(m.memfd)
		m.memfd = -1
	}
}
