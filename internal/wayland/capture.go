package wayland

import (
	"github.com/codetheuri/orbital-saver/internal/raster"
)

// Finding what shows of the wallpaper. Wayland does not tell a client
// which of its pixels are covered, so we ask the compositor for a copy of
// the output (wlr-screencopy) and compare it with what we drew: where the
// two agree, the wallpaper shows. The copy is compared and dropped at
// once; nothing of it is kept.

const screencopyVersion = 3 // buffer_done, so the formats are known before copying

// wl_shm formats we can compare. The first two are XRGB8888 in memory;
// the drm fourccs are the same with red and blue swapped.
const (
	shmARGB8888 = 0
	shmXRGB8888 = 1
	shmABGR8888 = 0x34324241
	shmXBGR8888 = 0x34324258
)

// capture is one screencopy frame in flight.
type capture struct {
	frame   uint32
	gen     int // Surface.layoutGen when it was asked for
	format  uint32
	w, h    int
	stride  int // bytes
	offered bool
	yInvert bool

	mem    *sharedMemory
	pool   uint32
	buffer uint32
}

// Capture asks for a copy of the output to find which cells of the surface
// show; Pending reports Visibility when the answer is in. It returns false
// when the compositor cannot copy outputs, or before the surface has shown
// a frame. If a capture is already running, its answer is dropped and
// another is taken after it.
func (s *Surface) Capture() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.output()
	if s.screencopy == 0 || s.closed || o == nil || !s.shown {
		return false
	}
	if s.capt != nil {
		s.captureAgain = true
		return true
	}
	s.startCapture(o)
	return s.c.flush() == nil
}

func (s *Surface) startCapture(o *output) {
	s.captureAgain = false
	s.capt = &capture{frame: s.c.newID(), gen: s.layoutGen}
	s.c.request(s.screencopy, 0, s.capt.frame, 0, o.id) // capture_output(frame, overlay_cursor: no, output)
}

// ShowAll forgets which cells were covered, so every cell animates until
// the next capture. Call it as soon as windows may have moved: a cell
// wrongly taken as covered would show a stale picture. A capture running
// now describes the old layout, and its answer is dropped.
func (s *Surface) ShowAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.layoutGen++
	s.visible, s.visibleN = nil, 0
}

// Visible returns the fraction of the surface's cells that showed at the
// last capture, 1 when not known.
func (s *Surface) Visible() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.visible == nil {
		return 1
	}
	return float64(s.visibleN) / float64(len(s.visible))
}

func (s *Surface) captureEvent(m *message) {
	c := s.capt
	switch m.opcode {
	case 0: // buffer(format, width, height, stride): a wl_shm buffer will do
		switch f := m.uint(0); f {
		case shmARGB8888, shmXRGB8888, shmABGR8888, shmXBGR8888:
			c.format, c.offered = f, true
			c.w, c.h, c.stride = int(m.uint(1)), int(m.uint(2)), int(m.uint(3))
		}
		if s.screencopyVer < 3 {
			s.copyFrame()
		}
	case 1: // flags
		c.yInvert = m.uint(0)&1 != 0
	case 6: // buffer_done
		s.copyFrame()
	case 2: // ready
		s.compareFrame()
		s.endCapture()
	case 3: // failed
		s.endCapture()
	}
}

// copyFrame gives the compositor a buffer to copy the output into.
func (s *Surface) copyFrame() {
	c := s.capt
	if !c.offered || c.w <= 0 || c.h <= 0 || c.stride < 4*c.w {
		s.endCapture()
		return
	}
	mem, err := newSharedMemory(c.stride * c.h)
	if err != nil {
		s.endCapture()
		return
	}
	c.mem = mem
	c.pool, c.buffer = s.c.newID(), s.c.newID()
	s.c.putFD(mem.fd)
	s.c.request(s.shm, 0, c.pool, uint32(c.stride*c.h)) // create_pool
	s.c.request(c.pool, 0, c.buffer, 0, uint32(c.w), uint32(c.h), uint32(c.stride), c.format)
	s.c.request(c.frame, 0, c.buffer) // copy
	if s.c.flush() == nil {
		mem.closeFD()
	}
}

// compareFrame turns the copy into the visible-cell mask, unless the
// layout changed since it was asked for or it does not line up with our
// buffers (a different scale, a rotated output).
func (s *Surface) compareFrame() {
	c := s.capt
	if c.gen != s.layoutGen || s.captureAgain || c.mem == nil ||
		c.w != s.bufW || c.h != s.bufH || s.bufs[0].Buffer == nil || s.bufs[1].Buffer == nil {
		return
	}
	screen := c.mem.pixels(0, c.stride/4*c.h)
	swap := c.format == shmABGR8888 || c.format == shmXBGR8888
	s.visible, s.visibleN = raster.VisibleCells(screen, c.stride/4, c.yInvert, swap,
		s.bufs[0].Pix, s.bufs[1].Pix, c.w, c.h)
	s.pending.Visibility = true
	s.wake()
}

// endCapture frees a capture, and starts the one asked for meanwhile.
func (s *Surface) endCapture() {
	c := s.capt
	s.c.request(c.frame, 1) // destroy
	if c.buffer != 0 {
		s.c.request(c.buffer, 0) // wl_buffer.destroy
		s.c.request(c.pool, 1)   // wl_shm_pool.destroy
	}
	if c.mem != nil {
		c.mem.release()
	}
	s.capt = nil
	if s.captureAgain && !s.closed {
		if o := s.output(); o != nil {
			s.startCapture(o)
		}
	}
}
