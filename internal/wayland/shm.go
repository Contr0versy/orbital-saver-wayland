package wayland

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// sharedMemory is an anonymous file mapped into our address space, the
// backing store of a wl_shm_pool.
type sharedMemory struct {
	fd   int
	data []byte
}

func newSharedMemory(size int) (*sharedMemory, error) {
	fd, err := anonymousFile()
	if err != nil {
		return nil, fmt.Errorf("cannot create shared memory: %w", err)
	}
	if err := syscall.Ftruncate(fd, int64(size)); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("cannot size shared memory to %d bytes: %w", size, err)
	}
	data, err := syscall.Mmap(fd, 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("cannot map shared memory: %w", err)
	}
	return &sharedMemory{fd: fd, data: data}, nil
}

// pixels views bytes [off, off+4n) as n 32-bit pixels.
func (m *sharedMemory) pixels(off, n int) []uint32 {
	return unsafe.Slice((*uint32)(unsafe.Pointer(&m.data[off])), n)
}

// closeFD drops our descriptor once the compositor has its own copy. The
// mapping stays valid.
func (m *sharedMemory) closeFD() {
	if m.fd >= 0 {
		syscall.Close(m.fd)
		m.fd = -1
	}
}

func (m *sharedMemory) release() {
	m.closeFD()
	if m.data != nil {
		syscall.Munmap(m.data)
		m.data = nil
	}
}

// anonymousFile returns a file descriptor with no name in any file system:
// a memfd where the kernel number is known, else a file in
// $XDG_RUNTIME_DIR (a tmpfs) that is unlinked straight away.
func anonymousFile() (int, error) {
	if sysMemfdCreate != 0 {
		name := []byte("orbital-saver\x00")
		const mfdCloexec = 1
		fd, _, errno := syscall.Syscall(sysMemfdCreate,
			uintptr(unsafe.Pointer(&name[0])), mfdCloexec, 0)
		if errno == 0 {
			return int(fd), nil
		}
	}
	f, err := os.CreateTemp(os.Getenv("XDG_RUNTIME_DIR"), "orbital-saver-*")
	if err != nil {
		return -1, err
	}
	os.Remove(f.Name())
	fd, err := syscall.Dup(int(f.Fd()))
	f.Close()
	if err != nil {
		return -1, err
	}
	syscall.CloseOnExec(fd)
	return fd, nil
}
