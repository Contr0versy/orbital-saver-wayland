// Package raster draws the scene's lines and dots into a 32-bit pixel
// buffer on the CPU. It is the Wayland counterpart of the X server's
// PolySegment: Wayland has no drawing service, so the client fills pixels
// itself.
//
// To keep the battery cost down it also works out which parts of the
// picture changed. The buffer is divided into square cells, and every cell
// gets a fingerprint of the primitives that touch it. A cell whose
// fingerprint matches what a buffer already holds is not redrawn, and only
// cells that differ from the frame on screen are reported as damage. When
// nothing moved by a whole pixel there is no damage at all, and the caller
// can skip the commit entirely.
package raster

import "image/color"

// CellShift sets the cell size: cells are 1<<CellShift pixels square.
const CellShift = 5

const cellSize = 1 << CellShift

// DefaultMaxRects is the default cap on the damage list; see SetMaxRects.
// Measured on the default scene at 2880x1920 and 20 fps, exact damage needs
// about 210 rects and covers about 18% of the frame.
const DefaultMaxRects = 256

// VisibleCells compares every SampleStep-th pixel of every SampleStep-th
// row, 64 points in a cell: a whole comparison of a 2880x1920 screen took
// 20 ms, longer than a frame. A cell shows when MinVisibleSamples of its
// points match. A 12-pixel gap still gets three columns of points.
const (
	SampleStep        = 4
	MinVisibleSamples = 2
)

// staleCell marks a cell fingerprint as unknown, so the cell is redrawn
// and damaged the next time it shows.
const staleCell = 0x5ca1ab1edeadbeef

// Rect is a damaged area in buffer pixels.
type Rect struct{ X, Y, W, H int }

type segment struct{ x0, y0, x1, y1 int32 }

type dot struct{ x, y, size int32 }

// Buffer is a pixel array the Canvas renders into, plus a record of which
// frame each of its cells currently shows.
type Buffer struct {
	Pix  []uint32 // W*H pixels, row-major, no padding
	W, H int

	cells []uint64
	valid bool // cells describes Pix; false until the first full render
}

// NewBuffer wraps pix (at least w*h pixels) as a render target.
func NewBuffer(pix []uint32, w, h int) *Buffer {
	return &Buffer{Pix: pix[:w*h], W: w, H: h}
}

// Canvas collects one frame of primitives (it satisfies scene.Canvas) and
// renders them. A Canvas is not safe for concurrent use.
type Canvas struct {
	ramp      []uint32
	bg        uint32
	full      bool // redraw and damage everything every frame
	maxRects  int
	w, h      int
	cols      int
	rows      int
	lineWidth int

	lines [][]segment // one bucket per ramp entry, dimmest first
	dots  [][]dot

	cells      []uint64 // fingerprint per cell for the frame being built
	shown      []uint64 // fingerprint per cell of the last rendered frame
	shownValid bool
	mask       []bool // cells being redrawn in the current Render
	visible    []bool // cells that show on screen; nil means all
	damage     []Rect
	open       []int // damage rects that may still grow downwards
}

// NewCanvas prepares a canvas for the given colour table and background.
// With full set, every frame is redrawn and damaged in full; that is the
// simple path, kept for comparison and measurement.
func NewCanvas(ramp []color.RGBA, background color.RGBA, full bool) *Canvas {
	c := &Canvas{
		ramp:     make([]uint32, len(ramp)),
		bg:       Pack(background),
		full:     full,
		maxRects: DefaultMaxRects,
		lines:    make([][]segment, len(ramp)),
		dots:     make([][]dot, len(ramp)),
	}
	for i, col := range ramp {
		c.ramp[i] = Pack(col)
	}
	return c
}

// SetMaxRects caps how many rects Render returns. Past the cap, each row of
// cells is reported as one span from its first to its last change, and if
// that is still too many, the whole change as one bounding box. Fewer,
// larger rects are cheaper to send and may be cheaper for the compositor,
// but cover pixels that did not change.
func (c *Canvas) SetMaxRects(n int) {
	if n < 1 {
		n = 1
	}
	c.maxRects = n
}

// Pack converts a colour to an XRGB8888 pixel.
func Pack(c color.RGBA) uint32 {
	return 0xff<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
}

// SetSize sets the pixel size of the frames to come and the line width in
// pixels. Changing either forgets what is on screen, so the next frame is
// drawn and damaged in full.
func (c *Canvas) SetSize(w, h, lineWidth int) {
	if lineWidth < 1 {
		lineWidth = 1
	}
	if w == c.w && h == c.h && lineWidth == c.lineWidth {
		return
	}
	c.w, c.h, c.lineWidth = w, h, lineWidth
	c.cols = (w + cellSize - 1) >> CellShift
	c.rows = (h + cellSize - 1) >> CellShift
	n := c.cols * c.rows
	c.cells = make([]uint64, n)
	c.shown = make([]uint64, n)
	c.mask = make([]bool, n)
	c.visible = nil
	c.shownValid = false
	c.Discard()
}

// SetVisible limits drawing and damage to the cells set in mask, row by
// row as VisibleCells returns them; nil means every cell. Hidden cells are
// left alone and are redrawn and damaged when they show again. A mask
// for another size is ignored.
func (c *Canvas) SetVisible(mask []bool) {
	if len(mask) != len(c.cells) {
		mask = nil
	}
	c.visible = mask
}

func (c *Canvas) shows(i int) bool { return c.visible == nil || c.visible[i] }

// Size returns the frame size set by SetSize.
func (c *Canvas) Size() (w, h int) { return c.w, c.h }

// Line queues a line in ramp colour shade.
func (c *Canvas) Line(shade, x0, y0, x1, y1 int) {
	if uint(shade) >= uint(len(c.lines)) {
		return
	}
	c.lines[shade] = append(c.lines[shade], segment{int32(x0), int32(y0), int32(x1), int32(y1)})
	pad := c.lineWidth/2 + 1
	h := mix(uint64(shade)<<1 | 0)
	h = mix(h ^ pack4(x0, y0, x1, y1))
	c.stamp(h, minInt(x0, x1)-pad, minInt(y0, y1)-pad, maxInt(x0, x1)+pad, maxInt(y0, y1)+pad)
}

// Dot queues a size×size square in ramp colour shade, with its top-left
// corner at (x, y).
func (c *Canvas) Dot(shade, x, y, size int) {
	if uint(shade) >= uint(len(c.dots)) || size <= 0 {
		return
	}
	c.dots[shade] = append(c.dots[shade], dot{int32(x), int32(y), int32(size)})
	h := mix(uint64(shade)<<1 | 1)
	h = mix(h ^ pack4(x, y, size, 0))
	c.stamp(h, x, y, x+size-1, y+size-1)
}

// stamp adds a primitive's fingerprint to every cell its bounding box
// touches. Addition is order-independent, which is right: within one shade
// the drawing order does not change the pixels, and shades are always
// drawn in index order.
func (c *Canvas) stamp(h uint64, x0, y0, x1, y1 int) {
	cx0, cy0, cx1, cy1, ok := c.cellRange(x0, y0, x1, y1)
	if !ok {
		return
	}
	for cy := cy0; cy <= cy1; cy++ {
		row := c.cells[cy*c.cols:]
		for cx := cx0; cx <= cx1; cx++ {
			row[cx] += h
		}
	}
}

// cellRange converts a pixel box to the range of cells it overlaps.
func (c *Canvas) cellRange(x0, y0, x1, y1 int) (cx0, cy0, cx1, cy1 int, ok bool) {
	if x1 < 0 || y1 < 0 || x0 >= c.w || y0 >= c.h {
		return 0, 0, 0, 0, false
	}
	x0, y0 = maxInt(x0, 0), maxInt(y0, 0)
	x1, y1 = minInt(x1, c.w-1), minInt(y1, c.h-1)
	return x0 >> CellShift, y0 >> CellShift, x1 >> CellShift, y1 >> CellShift, true
}

// Changed reports whether the queued frame differs from the last rendered
// one. When it does not, there is nothing to show and nothing to commit.
func (c *Canvas) Changed() bool {
	if c.full || !c.shownValid {
		return true
	}
	for i, h := range c.cells {
		if h != c.shown[i] && c.shows(i) {
			return true
		}
	}
	return false
}

// Discard drops the queued frame.
func (c *Canvas) Discard() {
	for i := range c.lines {
		c.lines[i] = c.lines[i][:0]
		c.dots[i] = c.dots[i][:0]
	}
	for i := range c.cells {
		c.cells[i] = 0
	}
}

// Render draws the queued frame into b, touching only cells whose contents
// differ from what b already holds, and returns the areas that differ from
// the previously rendered frame: the damage to report to the compositor.
// The returned slice is reused by the next call. The queue is emptied.
func (c *Canvas) Render(b *Buffer) []Rect {
	if b.W != c.w || b.H != c.h {
		panic("raster: buffer size does not match canvas")
	}
	if len(b.cells) != len(c.cells) {
		b.cells = make([]uint64, len(c.cells))
		b.valid = false
	}

	any := false
	for i, h := range c.cells {
		dirty := c.full || c.shows(i) && (!b.valid || b.cells[i] != h)
		c.mask[i] = dirty
		any = any || dirty
	}
	if any {
		c.clearMasked(b)
		for shade, col := range c.ramp {
			for _, d := range c.dots[shade] {
				c.fillDot(b, d, col)
			}
			for _, s := range c.lines[shade] {
				c.drawLine(b, s, col)
			}
		}
	}
	copy(b.cells, c.cells)
	b.valid = true

	c.collectDamage()
	copy(c.shown, c.cells)
	c.shownValid = true
	if c.visible != nil && !c.full {
		for i, show := range c.visible {
			if !show {
				b.cells[i], c.shown[i] = staleCell, staleCell
			}
		}
	}
	c.Discard()
	return c.damage
}

func (c *Canvas) clearMasked(b *Buffer) {
	c.maskedRuns(func(y, x0, x1 int) {
		row := b.Pix[y*c.w+x0 : y*c.w+x1]
		for i := range row {
			row[i] = c.bg
		}
	})
}

// Written calls fn for every run of pixels the last Render may have
// written, one pixel row at a time: Pix[y*W+x0 : y*W+x1]. Nothing outside
// these runs was touched.
func (c *Canvas) Written(fn func(y, x0, x1 int)) { c.maskedRuns(fn) }

// maskedRuns calls fn for each pixel row of each run of dirty cells.
func (c *Canvas) maskedRuns(fn func(y, x0, x1 int)) {
	for cy := 0; cy < c.rows; cy++ {
		y0 := cy << CellShift
		y1 := minInt(y0+cellSize, c.h)
		for cx := 0; cx < c.cols; {
			if !c.mask[cy*c.cols+cx] {
				cx++
				continue
			}
			start := cx
			for cx < c.cols && c.mask[cy*c.cols+cx] {
				cx++
			}
			x0, x1 := start<<CellShift, minInt(cx<<CellShift, c.w)
			for y := y0; y < y1; y++ {
				fn(y, x0, x1)
			}
		}
	}
}

// masked reports whether any cell under the pixel box is being redrawn.
func (c *Canvas) masked(x0, y0, x1, y1 int) bool {
	cx0, cy0, cx1, cy1, ok := c.cellRange(x0, y0, x1, y1)
	if !ok {
		return false
	}
	for cy := cy0; cy <= cy1; cy++ {
		row := c.mask[cy*c.cols:]
		for cx := cx0; cx <= cx1; cx++ {
			if row[cx] {
				return true
			}
		}
	}
	return false
}

func (c *Canvas) fillDot(b *Buffer, d dot, col uint32) {
	x0, y0 := int(d.x), int(d.y)
	x1, y1 := x0+int(d.size)-1, y0+int(d.size)-1
	if !c.masked(x0, y0, x1, y1) {
		return
	}
	x0, y0 = maxInt(x0, 0), maxInt(y0, 0)
	x1, y1 = minInt(x1, c.w-1), minInt(y1, c.h-1)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			c.plot(b, x, y, col)
		}
	}
}

// drawLine is Bresenham's algorithm, both endpoints included like X11's
// PolySegment. Wide lines are drawn as a span across the minor axis at
// every step.
func (c *Canvas) drawLine(b *Buffer, s segment, col uint32) {
	x0, y0, x1, y1 := int(s.x0), int(s.y0), int(s.x1), int(s.y1)
	pad := c.lineWidth / 2
	if !c.masked(minInt(x0, x1)-pad, minInt(y0, y1)-pad, maxInt(x0, x1)+pad, maxInt(y0, y1)+pad) {
		return
	}
	dx, dy := absInt(x1-x0), -absInt(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	xMajor := dx >= -dy
	lo := -(c.lineWidth - 1) / 2
	hi := lo + c.lineWidth - 1
	err := dx + dy
	for {
		if c.lineWidth == 1 {
			c.plot(b, x0, y0, col)
		} else {
			for k := lo; k <= hi; k++ {
				if xMajor {
					c.plot(b, x0, y0+k, col)
				} else {
					c.plot(b, x0+k, y0, col)
				}
			}
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func (c *Canvas) plot(b *Buffer, x, y int, col uint32) {
	if uint(x) >= uint(c.w) || uint(y) >= uint(c.h) {
		return
	}
	if !c.mask[(y>>CellShift)*c.cols+x>>CellShift] {
		return
	}
	b.Pix[y*c.w+x] = col
}

// collectDamage turns the cells that differ from the shown frame into a
// short list of rectangles: runs of cells along each row, merged with the
// run directly above when they line up.
func (c *Canvas) collectDamage() {
	c.damage = c.damage[:0]
	if c.full || !c.shownValid {
		c.damage = append(c.damage, Rect{W: c.w, H: c.h})
		return
	}
	if c.collectRuns(false) <= c.maxRects {
		return
	}
	if c.collectRuns(true) <= c.maxRects {
		return
	}
	bb := c.damage[0]
	for _, r := range c.damage[1:] {
		x1, y1 := maxInt(bb.X+bb.W, r.X+r.W), maxInt(bb.Y+bb.H, r.Y+r.H)
		bb.X, bb.Y = minInt(bb.X, r.X), minInt(bb.Y, r.Y)
		bb.W, bb.H = x1-bb.X, y1-bb.Y
	}
	c.damage = append(c.damage[:0], bb)
}

// collectRuns fills c.damage and returns its length. With span set, all
// changed cells of a row form one run, from the first to the last.
func (c *Canvas) collectRuns(span bool) int {
	c.damage = c.damage[:0]
	c.open = c.open[:0]
	changed := func(cy, cx int) bool {
		i := cy*c.cols + cx
		return c.cells[i] != c.shown[i] && c.shows(i)
	}
	for cy := 0; cy < c.rows; cy++ {
		y := cy << CellShift
		h := minInt(cellSize, c.h-y)
		stillOpen := len(c.open)
		for cx := 0; cx < c.cols; {
			if !changed(cy, cx) {
				cx++
				continue
			}
			start, end := cx, cx
			for cx < c.cols && (span || changed(cy, cx)) {
				if changed(cy, cx) {
					end = cx
				}
				cx++
			}
			x, w := start<<CellShift, minInt((end+1)<<CellShift, c.w)-start<<CellShift
			merged := false
			for _, ri := range c.open[:stillOpen] {
				r := &c.damage[ri]
				if r.X == x && r.W == w && r.Y+r.H == y {
					r.H += h
					c.open = append(c.open, ri)
					merged = true
					break
				}
			}
			if !merged {
				c.damage = append(c.damage, Rect{X: x, Y: y, W: w, H: h})
				c.open = append(c.open, len(c.damage)-1)
			}
		}
		// Rects not extended by this row are closed.
		n := copy(c.open, c.open[stillOpen:])
		c.open = c.open[:n]
	}
	return len(c.damage)
}

// VisibleCells works out which cells of a w×h surface show on screen, by
// comparing a copy of the screen with the two buffers the surface shows
// in turn: wherever the screen holds what one of them holds, the surface
// is not covered. Only a grid of sample points is compared (SampleStep). screen is XRGB8888 with stride pixels per row, upside
// down if flip, with red and blue swapped if swap. It returns one entry
// per cell, row by row (see SetVisible), and how many are set.
func VisibleCells(screen []uint32, stride int, flip, swap bool, a, b []uint32, w, h int) (mask []bool, n int) {
	cols := (w + cellSize - 1) >> CellShift
	rows := (h + cellSize - 1) >> CellShift
	counts := make([]int, cols*rows)
	const rgb = 0xffffff
	// Points sit in the middle of each SampleStep square.
	for y := SampleStep / 2; y < h; y += SampleStep {
		sy := y
		if flip {
			sy = h - 1 - y
		}
		srow := screen[sy*stride : sy*stride+w]
		arow, brow := a[y*w:(y+1)*w], b[y*w:(y+1)*w]
		cells := counts[(y>>CellShift)*cols:]
		for x := SampleStep / 2; x < w; x += SampleStep {
			p := srow[x] & rgb
			if swap {
				p = p&0xff<<16 | p&0xff00 | p>>16
			}
			if p == arow[x]&rgb || p == brow[x]&rgb {
				cells[x>>CellShift]++
			}
		}
	}
	mask = make([]bool, len(counts))
	for i, k := range counts {
		if k >= MinVisibleSamples {
			mask[i] = true
			n++
		}
	}
	return mask, n
}

// mix is the splitmix64 finaliser: a cheap, well-spread 64-bit hash step.
func mix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	return x ^ x>>31
}

func pack4(a, b, c, d int) uint64 {
	return uint64(uint16(a)) | uint64(uint16(b))<<16 | uint64(uint16(c))<<32 | uint64(uint16(d))<<48
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
