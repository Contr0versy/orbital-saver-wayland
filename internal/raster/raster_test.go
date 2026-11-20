package raster

import (
	"image/color"
	"testing"

	"github.com/codetheuri/orbital-saver/internal/scene"
)

var (
	black = color.RGBA{A: 255}
	white = color.RGBA{R: 255, G: 255, B: 255, A: 255}
)

func newTest(w, h, lineWidth int, full bool) (*Canvas, *Buffer) {
	c := NewCanvas([]color.RGBA{white}, black, full)
	c.SetSize(w, h, lineWidth)
	return c, NewBuffer(make([]uint32, w*h), w, h)
}

func count(b *Buffer, px uint32) int {
	n := 0
	for _, p := range b.Pix {
		if p == px {
			n++
		}
	}
	return n
}

func TestLinePixels(t *testing.T) {
	c, b := newTest(64, 64, 1, false)
	c.Line(0, 2, 3, 20, 3) // horizontal, endpoints included
	c.Line(0, 5, 10, 5, 30)
	c.Line(0, 10, 10, 20, 20) // diagonal
	c.Render(b)
	on := Pack(white)
	want := 19 + 21 + 11
	if got := count(b, on); got != want {
		t.Errorf("lit pixels = %d, want %d", got, want)
	}
	for _, p := range [][2]int{{2, 3}, {20, 3}, {5, 30}, {15, 15}} {
		if b.Pix[p[1]*64+p[0]] != on {
			t.Errorf("pixel %v not lit", p)
		}
	}
}

func TestWideLineAndClipping(t *testing.T) {
	c, b := newTest(32, 32, 2, false)
	c.Line(0, -10, 5, 50, 5) // crosses both edges
	c.Dot(0, 30, 30, 4)      // partly outside
	c.Render(b)
	if got := count(b, Pack(white)); got != 32*2+4 {
		t.Errorf("lit pixels = %d, want %d", got, 32*2+4)
	}
}

func TestUnchangedFrameHasNoDamage(t *testing.T) {
	c, b := newTest(256, 256, 1, false)
	draw := func(x int) {
		c.Line(0, 10, 10, 200, 40)
		c.Dot(0, x, 150, 2)
	}
	draw(100)
	if d := c.Render(b); len(d) != 1 || d[0] != (Rect{W: 256, H: 256}) {
		t.Fatalf("first frame damage = %v, want the whole buffer", d)
	}
	draw(100)
	if c.Changed() {
		t.Error("identical frame reported as changed")
	}
	if d := c.Render(b); len(d) != 0 {
		t.Errorf("identical frame damage = %v, want none", d)
	}
	draw(101) // the dot moves one pixel inside its cell
	d := c.Render(b)
	if len(d) != 1 || d[0] != (Rect{X: 96, Y: 128, W: 32, H: 32}) {
		t.Errorf("one-pixel move damage = %v, want the dot's cell", d)
	}
}

func TestHiddenCellsWaitAndCatchUp(t *testing.T) {
	c, b := newTest(64, 64, 1, false) // 2x2 cells
	draw := func(y int) {
		c.Line(0, 0, y, 63, y) // across both cells of a row
	}
	draw(5)
	c.Render(b)
	// Only the right half shows; the line moves within the top row.
	c.SetVisible([]bool{false, true, false, true})
	draw(6)
	if !c.Changed() {
		t.Fatal("a change in a visible cell went unnoticed")
	}
	d := c.Render(b)
	if len(d) != 1 || d[0] != (Rect{X: 32, Y: 0, W: 32, H: 32}) {
		t.Errorf("damage = %v, want only the visible top-right cell", d)
	}
	on := Pack(white)
	if b.Pix[5*64+10] != on || b.Pix[6*64+10] == on {
		t.Error("the hidden cell was redrawn")
	}
	if b.Pix[6*64+40] != on || b.Pix[5*64+40] == on {
		t.Error("the visible cell was not redrawn")
	}
	// Nothing visible moves: no commit needed.
	draw(6)
	if c.Changed() {
		t.Error("an unchanged frame counts as changed")
	}
	c.Render(b)
	// Everything shows again: the stale cells catch up, the rest stay.
	c.SetVisible(nil)
	draw(6)
	if !c.Changed() {
		t.Fatal("revealed stale cells went unnoticed")
	}
	d = c.Render(b)
	if len(d) != 1 || d[0] != (Rect{X: 0, Y: 0, W: 32, H: 64}) {
		t.Errorf("damage = %v, want the left column of cells", d)
	}
	if b.Pix[6*64+10] != on || b.Pix[5*64+10] == on {
		t.Error("the revealed cell still shows the old frame")
	}
}

func TestVisibleCells(t *testing.T) {
	const w, h = 64, 64 // 2x2 cells
	a := make([]uint32, w*h)
	b := make([]uint32, w*h)
	for i := range a {
		a[i] = 0xff102030
		b[i] = 0xff405060
	}
	// The screen shows a in the top-left cell, b in the bottom-right, and
	// a window everywhere else, except a sliver of a too thin to count.
	screen := func(flip, swap bool) []uint32 {
		const stride = w + 8
		s := make([]uint32, stride*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				p := uint32(0x00777777)
				switch {
				case x < 32 && y < 32:
					p = a[y*w+x]
				case x >= 32 && y >= 32:
					p = b[y*w+x]
				case x >= 32 && y < 32 && x < 36 && y < SampleStep: // one sample point
					p = a[y*w+x]
				}
				if swap {
					p = p&0xff<<16 | p&0xff00 | p>>16&0xff
				}
				sy := y
				if flip {
					sy = h - 1 - y
				}
				s[sy*stride+x] = p
			}
		}
		return s
	}
	for _, c := range []struct{ flip, swap bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		mask, n := VisibleCells(screen(c.flip, c.swap), w+8, c.flip, c.swap, a, b, w, h)
		want := []bool{true, false, false, true}
		if n != 2 || len(mask) != 4 || mask[0] != want[0] || mask[1] != want[1] || mask[2] != want[2] || mask[3] != want[3] {
			t.Errorf("flip=%v swap=%v: mask %v (%d set), want %v", c.flip, c.swap, mask, n, want)
		}
	}
}

func TestDamageMerging(t *testing.T) {
	c, b := newTest(256, 256, 1, false)
	c.Render(b)
	c.Line(0, 40, 40, 40, 120) // a vertical line through cells (1, 1..3)
	d := c.Render(b)
	if len(d) != 1 || d[0] != (Rect{X: 32, Y: 32, W: 32, H: 96}) {
		t.Errorf("damage = %v, want one 32x96 rect", d)
	}
}

// TestDirtyMatchesFullRedraw renders real scene frames into two alternating
// buffers (as a Wayland client does) using dirty cells, and checks every
// frame against a full redraw.
func TestDirtyMatchesFullRedraw(t *testing.T) {
	const w, h = 640, 400
	p, _ := scene.PaletteByName("amber")
	dirty := NewCanvas(p.Ramp(), p.Background, false)
	full := NewCanvas(p.Ramp(), p.Background, true)
	dirty.SetSize(w, h, 2)
	full.SetSize(w, h, 2)
	bufs := [2]*Buffer{
		NewBuffer(make([]uint32, w*h), w, h),
		NewBuffer(make([]uint32, w*h), w, h),
	}
	ref := NewBuffer(make([]uint32, w*h), w, h)

	s := scene.New(7, 600)
	for frame := 0; frame < 120; frame++ {
		s.Advance(1.0 / 20)
		s.Draw(dirty, w, h)
		s.Draw(full, w, h)
		b := bufs[frame%2]
		dirty.Render(b)
		full.Render(ref)
		for i := range ref.Pix {
			if b.Pix[i] != ref.Pix[i] {
				t.Fatalf("frame %d: pixel (%d,%d) = %08x, want %08x",
					frame, i%w, i/w, b.Pix[i], ref.Pix[i])
			}
		}
	}
}

// Written must name every pixel Render changed: the dma-buf path writes
// exactly those back from the CPU cache, and a pixel it missed would show
// stale on a screen that scans the buffer out directly.
func TestWrittenCoversEveryChange(t *testing.T) {
	const w, h = 640, 400
	p, _ := scene.PaletteByName("amber")
	c := NewCanvas(p.Ramp(), p.Background, false)
	c.SetSize(w, h, 2)
	bufs := [2]*Buffer{
		NewBuffer(make([]uint32, w*h), w, h),
		NewBuffer(make([]uint32, w*h), w, h),
	}
	before := make([]uint32, w*h)
	written := make([]bool, w*h)
	s := scene.New(7, 600)
	for frame := 0; frame < 120; frame++ {
		s.Advance(1.0 / 20)
		s.Draw(c, w, h)
		b := bufs[frame%2]
		copy(before, b.Pix)
		c.Render(b)
		for i := range written {
			written[i] = false
		}
		c.Written(func(y, x0, x1 int) {
			for x := x0; x < x1; x++ {
				written[y*w+x] = true
			}
		})
		for i := range before {
			if b.Pix[i] != before[i] && !written[i] {
				t.Fatalf("frame %d: pixel (%d,%d) changed outside the written runs", frame, i%w, i/w)
			}
		}
	}
}

// TestSceneDamage reports how much of a 2880x1920 frame changes at 20 fps
// with the default scene, which decides whether dirty cells pay off.
func TestSceneDamage(t *testing.T) {
	const w, h = 2880, 1920
	p, _ := scene.PaletteByName("amber")
	c := NewCanvas(p.Ramp(), p.Background, false)
	c.SetSize(w, h, 2)
	b := NewBuffer(make([]uint32, w*h), w, h)
	s := scene.New(1, 1500)
	s.Draw(c, w, h)
	c.Render(b)

	var area, frames, rects int
	for i := 0; i < 100; i++ {
		s.Advance(1.0 / 20)
		s.Draw(c, w, h)
		for _, r := range c.Render(b) {
			area += r.W * r.H
			rects++
		}
		frames++
	}
	t.Logf("20 fps at %dx%d: %.1f%% of the frame damaged on average, %.1f rects per frame",
		w, h, 100*float64(area)/float64(frames*w*h), float64(rects)/float64(frames))
}

func TestRenderDoesNotAllocate(t *testing.T) {
	const w, h = 960, 600
	p, _ := scene.PaletteByName("amber")
	c := NewCanvas(p.Ramp(), p.Background, false)
	c.SetSize(w, h, 1)
	b := NewBuffer(make([]uint32, w*h), w, h)
	s := scene.New(3, 1500)
	frame := func() {
		s.Advance(1.0 / 30)
		s.Draw(c, w, h)
		c.Render(b)
	}
	for i := 0; i < 10; i++ { // let the buckets reach their working size
		frame()
	}
	if n := testing.AllocsPerRun(50, frame); n != 0 {
		t.Errorf("%v allocations per frame, want 0", n)
	}
}

func BenchmarkRenderFull(b *testing.B)  { benchRender(b, true) }
func BenchmarkRenderDirty(b *testing.B) { benchRender(b, false) }

func benchRender(b *testing.B, full bool) {
	const w, h = 2880, 1920
	p, _ := scene.PaletteByName("amber")
	c := NewCanvas(p.Ramp(), p.Background, full)
	c.SetSize(w, h, 2)
	buf := NewBuffer(make([]uint32, w*h), w, h)
	s := scene.New(1, 1500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Advance(1.0 / 20)
		s.Draw(c, w, h)
		c.Render(buf)
	}
}

func BenchmarkVisibleCells(b *testing.B) {
	const w, h = 2880, 1920
	screen := make([]uint32, w*h)
	front := make([]uint32, w*h)
	back := make([]uint32, w*h)
	for i := range screen {
		screen[i] = uint32(i) & 0xffffff
		front[i] = 0xff000000 | uint32(i)&0xffffff
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		VisibleCells(screen, w, false, false, front, back, w, h)
	}
}
