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

func TestSetColorsRedrawsEverything(t *testing.T) {
	c := NewCanvas([]color.RGBA{white}, black, false)
	c.SetSize(64, 64, 1)
	bufs := []*Buffer{NewBuffer(make([]uint32, 64*64), 64, 64), NewBuffer(make([]uint32, 64*64), 64, 64)}
	for _, b := range bufs {
		c.Line(0, 0, 0, 63, 63)
		c.Render(b)
	}
	red := color.RGBA{R: 255, A: 255}
	c.SetColors([]color.RGBA{red}, white)
	for i, b := range bufs {
		c.Line(0, 0, 0, 63, 63)
		// Once the first recoloured frame is shown, the same picture again
		// is no change; the other buffer still catches up when used.
		if c.Changed() != (i == 0) {
			t.Fatalf("frame %d: Changed = %v", i, c.Changed())
		}
		d := c.Render(b)
		if i == 0 && (len(d) != 1 || d[0] != (Rect{W: 64, H: 64})) {
			t.Errorf("damage after recolour = %v, want the whole buffer", d)
		}
		if b.Pix[0] != Pack(red) || b.Pix[63] != Pack(white) {
			t.Errorf("buffer %d not recoloured: %08x %08x", i, b.Pix[0], b.Pix[63])
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
