// Package x11 owns everything that talks to the X server: picking the
// target window, the double-buffered drawing surface, and event handling.
package x11

import (
	"fmt"
	"image/color"
	"math/bits"
	"sync"
	"sync/atomic"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Mode selects which window a Surface draws on.
type Mode int

const (
	ModePreview Mode = iota // create our own resizable top-level window
	ModeRoot                // the root window, or the one a screensaver daemon exports
	ModeWindow              // an explicit window id
)

// Config describes the surface to open.
type Config struct {
	Mode   Mode
	Window uint32 // target for ModeWindow

	Title         string // preview window title
	Width, Height int    // initial preview window size

	Background color.RGBA
	Ramp       []color.RGBA // colour table; Line and Dot index into it
	LineWidth  int          // pixels; 0 picks one to suit the resolution
}

// maxBatch is how many segments or rectangles go into one request. At 8
// bytes each this stays under the smallest maximum request size the X
// protocol allows a server to have (16 KiB), so it is always safe.
const maxBatch = 2000

// Surface is a flicker-free drawing target. A frame is built up off-screen
// in a Pixmap by Line and Dot calls and then shown all at once by Present.
//
// Drawing methods must be called from a single goroutine.
type Surface struct {
	conn *xgb.Conn
	win  xproto.Window

	// One graphics context per ramp colour, plus one for the background.
	// Switching colour is then free: it costs no request at draw time,
	// which matters because xgb handles every request synchronously.
	clearGC xproto.Gcontext
	gcs     []xproto.Gcontext

	owned                 bool // we created win and must destroy it
	wmProtocols, wmDelete xproto.Atom

	pixmap    xproto.Pixmap // back buffer; the id is reused across resizes
	hasPixmap bool
	depth     byte
	width     uint16
	height    uint16

	format                 pixelFormat
	colormap               xproto.Colormap
	blackPixel, whitePixel uint32

	bg        uint32
	lineWidth int // configured width, 0 = automatic
	gcWidth   int // width currently set in the graphics contexts

	// One bucket of pending primitives per ramp entry, reused every frame.
	segments [][]xproto.Segment
	rects    [][]xproto.Rectangle
	full     [1]xproto.Rectangle

	events    chan Event
	done      chan struct{}
	dead      atomic.Bool // the X connection is gone
	closeOnce sync.Once
}

// Open connects to the X server named by $DISPLAY and prepares the target
// window for drawing.
func Open(cfg Config) (*Surface, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to the X server (is DISPLAY set?): %w", err)
	}
	s := &Surface{
		conn:      conn,
		lineWidth: cfg.LineWidth,
		gcWidth:   1, // a new GC draws thin lines
		events:    make(chan Event, 16),
		done:      make(chan struct{}),
	}
	if err := s.init(cfg); err != nil {
		conn.Close()
		return nil, err
	}
	go s.pump()
	return s, nil
}

func (s *Surface) init(cfg Config) error {
	setup := xproto.Setup(s.conn)
	screen := setup.DefaultScreen(s.conn)

	switch cfg.Mode {
	case ModePreview:
		s.depth = screen.RootDepth
		s.width, s.height = clampDim(cfg.Width), clampDim(cfg.Height)
		s.format = lookupFormat(screen, screen.RootVisual)
		s.colormap = screen.DefaultColormap
		s.blackPixel, s.whitePixel = screen.BlackPixel, screen.WhitePixel
		s.bg = s.pixel(cfg.Background)
		if err := s.createWindow(screen, cfg.Title); err != nil {
			return err
		}
	case ModeRoot:
		if err := s.adoptWindow(setup, resolveRoot(s.conn, screen.Root)); err != nil {
			return err
		}
	default:
		if err := s.adoptWindow(setup, xproto.Window(cfg.Window)); err != nil {
			return err
		}
	}

	s.bg = s.pixel(cfg.Background)
	s.segments = make([][]xproto.Segment, len(cfg.Ramp))
	s.rects = make([][]xproto.Rectangle, len(cfg.Ramp))

	var err error
	if s.clearGC, err = s.createGC(s.bg); err != nil {
		return err
	}
	s.gcs = make([]xproto.Gcontext, len(cfg.Ramp))
	for i, c := range cfg.Ramp {
		if s.gcs[i], err = s.createGC(s.pixel(c)); err != nil {
			return err
		}
	}

	pixmap, err := xproto.NewPixmapId(s.conn)
	if err != nil {
		return err
	}
	s.pixmap = pixmap
	return s.allocPixmap()
}

func (s *Surface) createGC(foreground uint32) (xproto.Gcontext, error) {
	gc, err := xproto.NewGcontextId(s.conn)
	if err != nil {
		return 0, err
	}
	// GraphicsExposures must be off, or every CopyArea in Present would
	// make the server send us a NoExpose event.
	err = xproto.CreateGCChecked(s.conn, gc, xproto.Drawable(s.win),
		xproto.GcForeground|xproto.GcGraphicsExposures,
		[]uint32{foreground, 0}).Check()
	if err != nil {
		return 0, fmt.Errorf("cannot create graphics context: %w", err)
	}
	return gc, nil
}

// Size returns the current back buffer size in pixels.
func (s *Surface) Size() (w, h int) { return int(s.width), int(s.height) }

// Resize makes the back buffer match a new window size. It is a no-op when
// the size has not changed.
func (s *Surface) Resize(w, h int) error {
	nw, nh := clampDim(w), clampDim(h)
	if nw == s.width && nh == s.height {
		return nil
	}
	s.width, s.height = nw, nh
	return s.allocPixmap()
}

func (s *Surface) allocPixmap() error {
	if s.hasPixmap {
		xproto.FreePixmap(s.conn, s.pixmap)
		s.hasPixmap = false
	}
	if s.width == 0 || s.height == 0 {
		return nil
	}
	err := xproto.CreatePixmapChecked(s.conn, s.depth, s.pixmap,
		xproto.Drawable(s.win), s.width, s.height).Check()
	if err != nil {
		return fmt.Errorf("cannot allocate %dx%d back buffer: %w", s.width, s.height, err)
	}
	s.hasPixmap = true
	s.full[0] = xproto.Rectangle{Width: s.width, Height: s.height}

	// Thin lines disappear on 4K panels, so thicken them there. Width 0 is
	// X11's fast path for one-pixel lines.
	width := s.lineWidth
	if width <= 0 {
		width = 1
		if s.width >= 2000 && s.height >= 2000 {
			width = 2
		}
	}
	if width != s.gcWidth {
		value := uint32(width)
		if width == 1 {
			value = 0
		}
		for _, gc := range s.gcs {
			xproto.ChangeGC(s.conn, gc, xproto.GcLineWidth, []uint32{value})
		}
		s.gcWidth = width
	}
	return nil
}

// Line queues a line in ramp colour `shade` for the next Present.
func (s *Surface) Line(shade, x0, y0, x1, y1 int) {
	if uint(shade) >= uint(len(s.segments)) {
		return
	}
	s.segments[shade] = append(s.segments[shade], xproto.Segment{
		X1: int16(x0), Y1: int16(y0), X2: int16(x1), Y2: int16(y1),
	})
}

// Dot queues a size×size square in ramp colour `shade` for the next Present.
func (s *Surface) Dot(shade, x, y, size int) {
	if uint(shade) >= uint(len(s.rects)) {
		return
	}
	s.rects[shade] = append(s.rects[shade], xproto.Rectangle{
		X: int16(x), Y: int16(y), Width: uint16(size), Height: uint16(size),
	})
}

// Present renders everything queued since the last call into the back
// buffer and copies the finished frame to the window in one step, so a
// half-drawn frame is never visible.
func (s *Surface) Present() error {
	if !s.hasPixmap {
		s.discard()
		return nil
	}
	buf := xproto.Drawable(s.pixmap)

	xproto.PolyFillRectangle(s.conn, buf, s.clearGC, s.full[:])

	// Buckets are ordered dimmest first, so bright lines land on top.
	for i, gc := range s.gcs {
		for rects := s.rects[i]; len(rects) > 0; {
			n := minInt(len(rects), maxBatch)
			xproto.PolyFillRectangle(s.conn, buf, gc, rects[:n])
			rects = rects[n:]
		}
		for segs := s.segments[i]; len(segs) > 0; {
			n := minInt(len(segs), maxBatch)
			xproto.PolySegment(s.conn, buf, gc, segs[:n])
			segs = segs[n:]
		}
	}
	s.discard()
	s.Blit()

	// Wait for the server to catch up. This throttles us to what the
	// server can actually display instead of flooding its input queue.
	_, err := xproto.GetInputFocus(s.conn).Reply()
	return err
}

// Blit copies the last finished frame to the window again, e.g. after the
// window was uncovered.
func (s *Surface) Blit() {
	if s.hasPixmap {
		xproto.CopyArea(s.conn, xproto.Drawable(s.pixmap), xproto.Drawable(s.win), s.clearGC,
			0, 0, 0, 0, s.width, s.height)
	}
}

func (s *Surface) discard() {
	for i := range s.segments {
		s.segments[i] = s.segments[i][:0]
		s.rects[i] = s.rects[i][:0]
	}
}

// Close releases the pixmap and graphics contexts, removes what we drew and
// disconnects. It is safe to call more than once.
func (s *Surface) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.dead.Load() {
			return // nothing left to talk to
		}
		if s.hasPixmap {
			xproto.FreePixmap(s.conn, s.pixmap)
		}
		xproto.FreeGC(s.conn, s.clearGC)
		for _, gc := range s.gcs {
			xproto.FreeGC(s.conn, gc)
		}
		if s.owned {
			xproto.DestroyWindow(s.conn, s.win)
		} else {
			// Let a borrowed window repaint its own background.
			xproto.ClearArea(s.conn, false, s.win, 0, 0, 0, 0)
		}
		// Round trip so the requests above are processed before we hang up.
		xproto.GetInputFocus(s.conn).Reply()
		s.conn.Close()
	})
}

// pixelFormat describes how to build pixel values for the target visual.
type pixelFormat struct {
	trueColor              bool
	red, green, blue, fill uint32 // channel masks; fill = bits left over (alpha)
}

func lookupFormat(screen *xproto.ScreenInfo, visual xproto.Visualid) pixelFormat {
	for _, depth := range screen.AllowedDepths {
		for _, v := range depth.Visuals {
			if v.VisualId != visual {
				continue
			}
			if v.Class != xproto.VisualClassTrueColor {
				return pixelFormat{}
			}
			f := pixelFormat{trueColor: true, red: v.RedMask, green: v.GreenMask, blue: v.BlueMask}
			if depth.Depth == 32 {
				// ARGB visual: the spare bits are alpha. Leaving them
				// zero would make us transparent under a compositor.
				f.fill = ^(f.red | f.green | f.blue)
			}
			return f
		}
	}
	return pixelFormat{}
}

// pixel converts a colour to a server pixel value. On TrueColor visuals
// (practically every display today) that is pure arithmetic; on anything
// else the server allocates the closest colour it has.
func (s *Surface) pixel(c color.RGBA) uint32 {
	if s.format.trueColor {
		return channel(c.R, s.format.red) | channel(c.G, s.format.green) |
			channel(c.B, s.format.blue) | s.format.fill
	}
	reply, err := xproto.AllocColor(s.conn, s.colormap,
		uint16(c.R)*257, uint16(c.G)*257, uint16(c.B)*257).Reply()
	if err != nil {
		if int(c.R)+int(c.G)+int(c.B) > 3*127 {
			return s.whitePixel
		}
		return s.blackPixel
	}
	return reply.Pixel
}

// channel scales an 8-bit intensity to fill the bits of mask.
func channel(v uint8, mask uint32) uint32 {
	if mask == 0 {
		return 0
	}
	max := uint32(1)<<uint(bits.OnesCount32(mask)) - 1
	return (uint32(v) * max / 255) << uint(bits.TrailingZeros32(mask))
}

// clampDim limits a window dimension to what X11's 16-bit geometry allows.
func clampDim(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > 32767 {
		return 32767
	}
	return uint16(v)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
