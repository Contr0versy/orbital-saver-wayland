package wayland

import "unsafe"

// The display engine can scan a buffer out directly, without the GPU
// compositing it, and on Intel it does not look into the CPU's caches. So
// pixels still sitting in a cache when the frame is committed are not what
// the screen shows: the picture flickers between old and new. The dma-buf
// sync ioctl does not write caches back on x86, so we do it ourselves for
// every byte a frame wrote. 64 bytes is the x86 cache line.

const canFlushCache = true

func cpuid(leaf, sub uint32) (a, b, c, d uint32)

//go:noescape
func flushOpt(p unsafe.Pointer, n uintptr)

//go:noescape
func flushLegacy(p unsafe.Pointer, n uintptr)

// hasCLFLUSHOPT: CPUID leaf 7, EBX bit 23.
var hasCLFLUSHOPT = func() bool {
	if max, _, _, _ := cpuid(0, 0); max < 7 {
		return false
	}
	_, b, _, _ := cpuid(7, 0)
	return b&(1<<23) != 0
}()

// flushCache writes the cache lines covering px back to memory and evicts
// them, then fences so the writes land before the frame is committed.
func flushCache(px []uint32) {
	if len(px) == 0 {
		return
	}
	if hasCLFLUSHOPT {
		flushOpt(unsafe.Pointer(&px[0]), uintptr(len(px)*4))
	} else {
		flushLegacy(unsafe.Pointer(&px[0]), uintptr(len(px)*4))
	}
}
