package wayland

import (
	"syscall"
	"testing"
)

func TestDMABufMemory(t *testing.T) {
	if syscall.Access("/dev/udmabuf", 6) != nil { // read and write
		t.Skip("no access to /dev/udmabuf")
	}
	const w, h = 100, 30 // 12000 bytes: not a whole number of pages
	m, err := newDMABufMemory(2, w*h*4)
	if err != nil {
		t.Fatal(err)
	}
	defer m.release()
	if m.size%pageSize != 0 || m.size < w*h*4 || len(m.fds) != 2 {
		t.Fatalf("size %d, %d fds", m.size, len(m.fds))
	}
	// The two buffers are separate memory.
	m.beginCPU(1)
	b := m.pixels(1, w*h)
	b[0], b[w*h-1] = 0xff00ff, 0x00ff00
	flushCache(b[:1])
	flushCache(b[1 : w*h-1]) // a length that is not a whole number of cache lines
	flushCache(b[:0])
	m.endCPU(1)
	if m.pixels(0, w*h)[0] != 0 || m.data[m.size] != 0xff {
		t.Error("buffer 1 is not where it should be")
	}
	// The dma-buf maps the same pages.
	view, err := syscall.Mmap(m.fds[1], 0, m.size, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(view)
	if view[0] != 0xff || view[(w*h-1)*4+1] != 0xff {
		t.Error("the dma-buf does not show what was written to the memfd")
	}
}
