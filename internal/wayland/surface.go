// Package wayland draws the scene on a wlr-layer-shell surface: behind
// every window as a live wallpaper, or above everything as a screensaver.
// It speaks the Wayland wire protocol directly, in pure Go, and rasterises
// on the CPU into dma-bufs the GPU reads in place (see dmabuf.go), or into
// shared-memory buffers where dma-bufs are not available.
//
// Only the handful of interfaces the program needs are implemented:
// wl_compositor, wl_shm, wl_output, wl_seat (screensaver mode),
// zwlr_layer_shell_v1 and, with Config.DMABuf, zwp_linux_dmabuf_v1.
package wayland

import (
	"errors"
	"fmt"
	"image/color"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codetheuri/orbital-saver/internal/raster"
)

// Layer is the layer-shell layer the surface lives on.
type Layer uint32

const (
	LayerBackground Layer = 0 // under every window: a live wallpaper
	LayerOverlay    Layer = 3 // over everything: a screensaver
)

// Config describes the surface to open.
type Config struct {
	Layer     Layer
	Namespace string // layer-shell namespace, for compositor layer rules
	Output    string // output name such as "eDP-1"; "" lets the compositor pick
	Scale     int    // buffer scale; 0 follows the output

	Background color.RGBA
	Ramp       []color.RGBA // colour table; Line and Dot index into it
	LineWidth  int          // logical pixels; 0 means 1

	FullRedraw bool // redraw and damage the whole surface every frame
	MaxRects   int  // cap on damage rects per frame; 0 = raster default
	DMABuf     bool // hand the compositor dma-bufs where it can take them, else shared memory
}

// Pending is what happened since the last call to Surface.Pending.
type Pending struct {
	Frame     bool  // the compositor wants the next frame
	Configure bool  // size or scale changed: draw again even if paused
	Quit      bool  // the surface is gone or the user dismissed it
	Err       error // set with Quit when something went wrong
}

// Protocol versions we speak. We bind the lower of these and what the
// compositor offers.
const (
	compositorVersion = 6 // 4 for damage_buffer, 6 for preferred_buffer_scale
	shmVersion        = 1
	outputVersion     = 4 // names
	seatVersion       = 5
	layerShellVersion = 4
	dmabufVersion     = 3 // the last version that lists modifiers as events
)

const pixelFormatXRGB8888 = 1

type output struct {
	id, name   uint32 // object id, registry name
	version    uint32
	outputName string // e.g. "eDP-1"
	scale      int
	refresh    int // mHz
	width      int // current mode, physical pixels
	height     int
}

type buffer struct {
	id   uint32
	busy bool // the compositor has not released it yet
	*raster.Buffer
}

// Surface is a layer-shell surface showing the scene. Line and Dot queue a
// frame, Present shows it. Begin, Line, Dot and Present must be called from
// one goroutine; events are read on another.
type Surface struct {
	cfg    Config
	canvas *raster.Canvas

	mu sync.Mutex // everything below, and all writes to conn
	c  *conn

	registry, compositor, shm, layerShell, seat, dmabuf uint32
	compositorVer                                       uint32
	outputs                                             []*output
	target                                              *output // the output we asked for, or nil
	entered                                             *output // the output the surface is on

	surface, layerSurface uint32
	keyboard, pointer     uint32
	pointerX, pointerY    int32 // where the pointer entered, 24.8 fixed point

	syncCB       uint32
	syncFinished bool
	frameCB      uint32

	dmabufLinear bool  // the compositor takes linear XRGB8888 dma-bufs
	dmabufErr    error // why Config.DMABuf fell back to shared memory

	pool      uint32
	memory    *sharedMemory
	gpuMemory *dmabufMemory // instead of pool and memory with Config.DMABuf
	flushPix  []uint32      // the buffer flushRun writes back
	flushRun  func(y, x0, x1 int)
	bufs      [2]buffer
	bufW      int
	bufH      int
	poolScale int

	configured     bool
	pendingSerial  uint32
	pendingW       int
	pendingH       int
	logicalW       int
	logicalH       int
	preferredScale int
	scale          int

	pending Pending
	notify  chan struct{}
	closed  bool
}

// Open connects to the compositor and creates the layer surface. The first
// frame can be drawn once Pending reports Configure.
func Open(cfg Config) (*Surface, error) {
	c, err := dial()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to the Wayland compositor: %w", err)
	}
	s := &Surface{
		cfg:    cfg,
		c:      c,
		canvas: raster.NewCanvas(cfg.Ramp, cfg.Background, cfg.FullRedraw),
		notify: make(chan struct{}, 1),
	}
	if cfg.MaxRects > 0 {
		s.canvas.SetMaxRects(cfg.MaxRects)
	}
	if err := s.init(); err != nil {
		c.close()
		return nil, err
	}
	go s.pump()
	return s, nil
}

func (s *Surface) init() error {
	s.registry = s.c.newID()
	s.c.request(1, 1, s.registry) // wl_display.get_registry
	if err := s.roundtrip(); err != nil {
		return err
	}
	switch {
	case s.compositor == 0:
		return errors.New("the compositor offers no wl_compositor")
	case s.compositorVer < 4:
		return fmt.Errorf("wl_compositor version %d is too old (need 4)", s.compositorVer)
	case s.shm == 0:
		return errors.New("the compositor offers no wl_shm")
	case s.layerShell == 0:
		return errors.New("the compositor does not support wlr-layer-shell " +
			"(niri, sway, Hyprland, river and KDE do; GNOME does not)")
	}
	// A second round trip collects every output's name, scale and mode,
	// and the dma-buf formats.
	if err := s.roundtrip(); err != nil {
		return err
	}
	if s.cfg.DMABuf && !s.dmabufLinear {
		s.cfg.DMABuf = false
		s.dmabufErr = errors.New("the compositor does not take linear XRGB8888 dma-bufs")
	}

	if s.cfg.Output != "" {
		for _, o := range s.outputs {
			if o.outputName == s.cfg.Output {
				s.target = o
			}
		}
		if s.target == nil {
			names := make([]string, 0, len(s.outputs))
			for _, o := range s.outputs {
				names = append(names, o.outputName)
			}
			sort.Strings(names)
			return fmt.Errorf("no output named %q (available: %s)", s.cfg.Output, strings.Join(names, ", "))
		}
	}

	s.surface = s.c.newID()
	s.c.request(s.compositor, 0, s.surface) // create_surface

	s.layerSurface = s.c.newID()
	start := s.c.begin(s.layerShell, 0) // get_layer_surface
	s.c.putUint(s.layerSurface)
	s.c.putUint(s.surface)
	if s.target != nil {
		s.c.putUint(s.target.id)
	} else {
		s.c.putUint(0)
	}
	s.c.putUint(uint32(s.cfg.Layer))
	s.c.putString(s.cfg.Namespace)
	s.c.end(start)

	s.c.request(s.layerSurface, 0, 0, 0)            // set_size: let the compositor decide
	s.c.request(s.layerSurface, 1, 1|2|4|8)         // set_anchor: all four edges
	s.c.request(s.layerSurface, 2, uint32(1<<32-1)) // set_exclusive_zone -1: ignore panels, cover the whole output

	if s.cfg.Layer == LayerOverlay {
		s.c.request(s.layerSurface, 4, 1) // keyboard_interactivity: exclusive
	} else {
		// A wallpaper takes no input: an empty input region lets clicks
		// through to whatever is behind it (the compositor's backdrop).
		region := s.c.newID()
		s.c.request(s.compositor, 1, region) // create_region
		s.c.request(s.surface, 5, region)    // set_input_region
		s.c.request(region, 0)               // destroy
	}
	s.c.request(s.surface, 6) // commit: no buffer yet, asks for a configure
	return s.c.flush()
}

// roundtrip sends wl_display.sync and dispatches events until it is done.
// Only used before the event goroutine starts.
func (s *Surface) roundtrip() error {
	s.syncCB = s.c.newID()
	s.syncFinished = false
	s.c.request(1, 0, s.syncCB) // wl_display.sync
	if err := s.c.flush(); err != nil {
		return err
	}
	for !s.syncFinished {
		if err := s.c.read(); err != nil {
			return err
		}
		s.dispatchAll()
		if s.pending.Quit {
			if s.pending.Err != nil {
				return s.pending.Err
			}
			return errors.New("compositor closed the surface")
		}
	}
	return nil
}

// pump reads and dispatches events until the connection ends.
func (s *Surface) pump() {
	for {
		err := s.c.read()
		s.mu.Lock()
		if err != nil {
			if !s.closed {
				s.quit(fmt.Errorf("lost the Wayland connection: %w", err))
			}
			s.mu.Unlock()
			return
		}
		s.dispatchAll()
		s.c.flush() // replies such as set_cursor
		s.mu.Unlock()
	}
}

func (s *Surface) dispatchAll() {
	for {
		m, ok := s.c.next()
		if !ok {
			break
		}
		s.dispatch(m)
	}
	if s.c.readErr != nil {
		s.quit(s.c.readErr)
	}
}

// Notify is signalled whenever there is something to collect with Pending.
func (s *Surface) Notify() <-chan struct{} { return s.notify }

// Pending returns and clears what happened since the last call.
func (s *Surface) Pending() Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending
	s.pending = Pending{Quit: p.Quit, Err: p.Err}
	return p
}

func (s *Surface) wake() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Surface) quit(err error) {
	if !s.pending.Quit {
		s.pending.Quit, s.pending.Err = true, err
	}
	s.wake()
}

// Explain swaps a write error for the protocol error that most likely
// caused it. A compositor that rejects a request sends wl_display.error and
// hangs up, so the next write fails with a bare "broken pipe" while the
// reason is still on its way to the event goroutine.
func (s *Surface) Explain(err error) error {
	deadline := time.After(500 * time.Millisecond)
	for {
		s.mu.Lock()
		quit, perr := s.pending.Quit, s.pending.Err
		s.mu.Unlock()
		if quit && perr != nil {
			return perr
		}
		select {
		case <-s.notify:
		case <-deadline:
			return err
		}
	}
}

// Refresh returns the refresh rate of the output the surface is on, in
// mHz, or 0 if unknown.
func (s *Surface) Refresh() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o := s.output(); o != nil {
		return o.refresh
	}
	return 0
}

// Buffers says what kind of buffers the frames go to the compositor in,
// and why not dma-bufs if they were asked for and are not used.
func (s *Surface) Buffers() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.gpuMemory != nil:
		return "dma-bufs"
	case s.dmabufErr != nil:
		return "shared memory (no dma-bufs: " + s.dmabufErr.Error() + ")"
	}
	return "shared memory"
}

// Output returns the name of the output the surface is on ("" if not
// known yet) and the surface's size in logical pixels.
func (s *Surface) Output() (name string, w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o := s.output(); o != nil {
		name = o.outputName
	}
	return name, s.logicalW, s.logicalH
}

// output is the output that decides our scale and refresh rate.
func (s *Surface) output() *output {
	switch {
	case s.entered != nil:
		return s.entered
	case s.target != nil:
		return s.target
	case len(s.outputs) == 1:
		return s.outputs[0]
	}
	return nil
}

func (s *Surface) currentScale() int {
	if s.cfg.Scale > 0 {
		return s.cfg.Scale
	}
	if s.preferredScale > 0 {
		return s.preferredScale
	}
	if o := s.output(); o != nil && o.scale > 0 {
		return o.scale
	}
	// Not on any output yet: guess the largest scale, so a HiDPI screen
	// does not start blurry.
	scale := 1
	for _, o := range s.outputs {
		if o.scale > scale {
			scale = o.scale
		}
	}
	return scale
}

// Begin applies the latest configure and returns the size in buffer pixels
// to draw the next frame at. ok is false before the first configure, and
// then nothing should be drawn.
func (s *Surface) Begin() (w, h int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pending.Quit {
		return 0, 0, false
	}
	if s.pendingSerial != 0 {
		s.c.request(s.layerSurface, 6, s.pendingSerial) // ack_configure
		s.pendingSerial = 0
		s.configured = true
		if s.pendingW > 0 && s.pendingH > 0 {
			s.logicalW, s.logicalH = s.pendingW, s.pendingH
		} else if o := s.output(); o != nil && o.width > 0 {
			sc := maxInt(o.scale, 1)
			s.logicalW, s.logicalH = o.width/sc, o.height/sc
		} else {
			s.logicalW, s.logicalH = 1280, 720
		}
	}
	if !s.configured {
		return 0, 0, false
	}
	if scale := s.currentScale(); scale != s.scale {
		s.scale = scale
		s.c.request(s.surface, 8, uint32(scale)) // set_buffer_scale
	}
	w, h = s.logicalW*s.scale, s.logicalH*s.scale
	if cw, ch := s.canvas.Size(); cw != w || ch != h {
		// The surface is opaque: the compositor need not draw anything
		// behind it.
		region := s.c.newID()
		s.c.request(s.compositor, 1, region)                                 // create_region
		s.c.request(region, 1, 0, 0, uint32(s.logicalW), uint32(s.logicalH)) // add
		s.c.request(s.surface, 4, region)                                    // set_opaque_region
		s.c.request(region, 0)                                               // destroy
	}
	lw := s.cfg.LineWidth
	if lw <= 0 {
		lw = 1
	}
	s.canvas.SetSize(w, h, lw*s.scale)
	return w, h, true
}

// Line queues a line in ramp colour shade.
func (s *Surface) Line(shade, x0, y0, x1, y1 int) { s.canvas.Line(shade, x0, y0, x1, y1) }

// Dot queues a size×size square in ramp colour shade.
func (s *Surface) Dot(shade, x, y, size int) { s.canvas.Dot(shade, x, y, size) }

// Present renders the queued frame and commits it. It reports whether a
// commit was made; when one was, Pending reports Frame once the compositor
// wants the next one. Nothing is committed when no pixel changed, or when
// both buffers are still held by the compositor.
func (s *Surface) Present() (committed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.configured || s.pending.Quit {
		s.canvas.Discard()
		return false, nil
	}
	if !s.canvas.Changed() {
		s.canvas.Discard()
		return false, s.c.flush() // a configure may still need acking
	}
	w, h := s.canvas.Size()
	if w != s.bufW || h != s.bufH {
		if err := s.allocBuffers(w, h); err != nil {
			return false, err
		}
	}
	i := 0
	for i < len(s.bufs) && s.bufs[i].busy {
		i++
	}
	if i == len(s.bufs) {
		s.canvas.Discard()
		return false, s.c.flush()
	}
	b := &s.bufs[i]

	var damage []raster.Rect
	if s.gpuMemory != nil {
		s.gpuMemory.beginCPU(i)
		damage = s.canvas.Render(b.Buffer)
		// Write back what the frame changed, in case the display engine
		// scans this buffer out directly (see flush_amd64.go).
		s.flushPix = b.Pix
		s.canvas.Written(s.flushRun)
		s.gpuMemory.endCPU(i)
	} else {
		damage = s.canvas.Render(b.Buffer)
	}
	s.c.request(s.surface, 1, b.id, 0, 0) // attach
	for _, r := range damage {
		s.c.request(s.surface, 9, uint32(r.X), uint32(r.Y), uint32(r.W), uint32(r.H)) // damage_buffer
	}
	if s.frameCB == 0 {
		s.frameCB = s.c.newID()
		s.c.request(s.surface, 3, s.frameCB) // frame
	}
	s.c.request(s.surface, 6) // commit
	b.busy = true
	return true, s.c.flush()
}

// allocBuffers replaces the buffers with two w×h ones: dma-bufs with
// Config.DMABuf where they work, else shared memory in one memfd.
func (s *Surface) allocBuffers(w, h int) error {
	s.freeBuffers()
	if s.cfg.DMABuf {
		mem, err := newDMABufMemory(len(s.bufs), w*4*h)
		if err == nil {
			return s.useDMABufs(mem, w, h)
		}
		// Most likely no access to /dev/udmabuf. Shared memory always works.
		s.cfg.DMABuf = false
		s.dmabufErr = err
	}
	stride := w * 4
	size := stride * h
	mem, err := newSharedMemory(2 * size)
	if err != nil {
		return err
	}
	s.pool = s.c.newID()
	s.c.putFD(mem.fd)
	s.c.request(s.shm, 0, s.pool, uint32(2*size)) // create_pool; the fd travels out of band, with no slot here
	for i := range s.bufs {
		id := s.c.newID()
		s.c.request(s.pool, 0, id, uint32(i*size), uint32(w), uint32(h), uint32(stride), pixelFormatXRGB8888) // create_buffer
		s.bufs[i] = buffer{id: id, Buffer: raster.NewBuffer(mem.pixels(i*size, w*h), w, h)}
	}
	err = s.c.flush()
	mem.closeFD()
	if err != nil {
		mem.release()
		return err
	}
	s.memory, s.bufW, s.bufH = mem, w, h
	return nil
}

// useDMABufs makes two w×h linear buffers out of mem's dma-bufs.
func (s *Surface) useDMABufs(mem *dmabufMemory, w, h int) error {
	stride := w * 4
	for i := range s.bufs {
		params := s.c.newID()
		s.c.request(s.dmabuf, 1, params) // create_params
		s.c.putFD(mem.fds[i])
		s.c.request(params, 1, 0, 0, uint32(stride), 0, drmModLinear) // add(fd out of band, plane, offset, stride, modifier hi, lo)
		id := s.c.newID()
		s.c.request(params, 3, id, uint32(w), uint32(h), drmFormatXRGB8888, 0) // create_immed
		s.c.request(params, 0)                                                 // destroy
		s.bufs[i] = buffer{id: id, Buffer: raster.NewBuffer(mem.pixels(i, w*h), w, h)}
	}
	if err := s.c.flush(); err != nil {
		mem.release()
		return err
	}
	s.gpuMemory, s.bufW, s.bufH = mem, w, h
	s.flushRun = func(y, x0, x1 int) { flushCache(s.flushPix[y*w+x0 : y*w+x1]) }
	return nil
}

func (s *Surface) freeBuffers() {
	for i := range s.bufs {
		if s.bufs[i].id != 0 {
			s.c.request(s.bufs[i].id, 0) // wl_buffer.destroy
		}
		s.bufs[i] = buffer{}
	}
	if s.pool != 0 {
		s.c.request(s.pool, 1) // wl_shm_pool.destroy
		s.pool = 0
	}
	if s.memory != nil {
		// The compositor keeps its own mapping; ours can go.
		s.memory.release()
		s.memory = nil
	}
	if s.gpuMemory != nil {
		s.gpuMemory.release()
		s.gpuMemory = nil
	}
	s.bufW, s.bufH = 0, 0
}

// Close destroys the surface and disconnects. Safe to call more than once.
func (s *Surface) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.layerSurface != 0 {
		s.c.request(s.layerSurface, 7) // destroy
	}
	if s.surface != 0 {
		s.c.request(s.surface, 0) // destroy
	}
	s.freeBuffers()
	s.c.flush()
	s.c.close()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
