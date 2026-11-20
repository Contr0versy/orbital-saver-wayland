//go:build !amd64

package wayland

// Writing caches back for the display engine is only implemented on amd64
// (see flush_amd64.go); elsewhere frames go in shared memory.
const canFlushCache = false

func flushCache(px []uint32) {}
