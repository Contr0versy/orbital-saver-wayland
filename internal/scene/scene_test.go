package scene

import (
	"math"
	"testing"
)

func TestSolveKepler(t *testing.T) {
	for _, e := range []float64{0, 0.05, 0.3, 0.74, 0.9} {
		for m := -7.0; m <= 7.0; m += 0.137 {
			E := SolveKepler(m, e)
			residual := math.Remainder(E-e*math.Sin(E)-m, 2*math.Pi)
			if math.Abs(residual) > 1e-9 {
				t.Fatalf("e=%.2f M=%.3f: residual %g", e, m, residual)
			}
		}
	}
}

func TestOrbitStaysOnEllipse(t *testing.T) {
	o := Orbit{
		SemiMajor:     7.6,
		Eccentricity:  0.74,
		Inclination:   0.3,
		AscendingNode: 1.1,
		ArgPeriapsis:  2.2,
		Period:        PeriodFor(7.6),
	}
	if d := o.PositionAt(0).Len(); math.Abs(d-o.Periapsis()) > 1e-9 {
		t.Errorf("at M=0 distance is %g, want periapsis %g", d, o.Periapsis())
	}
	if d := o.PositionAt(o.Period / 2).Len(); math.Abs(d-o.Apoapsis()) > 1e-9 {
		t.Errorf("at half period distance is %g, want apoapsis %g", d, o.Apoapsis())
	}
	for i := 0; i < 500; i++ {
		d := o.PositionAt(float64(i) * 0.37).Len()
		if d < o.Periapsis()-1e-9 || d > o.Apoapsis()+1e-9 {
			t.Fatalf("t=%d: distance %g outside [%g, %g]", i, d, o.Periapsis(), o.Apoapsis())
		}
	}
}

func TestKeplersSecondLaw(t *testing.T) {
	// Equal areas in equal times means the body moves fastest at periapsis.
	o := Orbit{SemiMajor: 5, Eccentricity: 0.6, Period: 100}
	const dt = 0.01
	atPeri := o.PositionAt(dt).Sub(o.PositionAt(0)).Len()
	atApo := o.PositionAt(50 + dt).Sub(o.PositionAt(50)).Len()
	want := (1 + o.Eccentricity) / (1 - o.Eccentricity) // v_peri / v_apo
	if got := atPeri / atApo; math.Abs(got-want) > 0.01 {
		t.Errorf("periapsis/apoapsis speed ratio = %g, want %g", got, want)
	}
}

func TestPalettes(t *testing.T) {
	for _, name := range PaletteNames() {
		p, err := PaletteByName(name)
		if err != nil {
			t.Fatal(err)
		}
		ramp := p.Ramp()
		if len(ramp) != Levels*int(numRoles) {
			t.Fatalf("%s: ramp has %d entries", name, len(ramp))
		}
		for role := Role(0); role < numRoles; role++ {
			if got := ramp[Shade(role, 1)]; got != p.Colors[role] {
				t.Errorf("%s role %d: full brightness = %v, want %v", name, role, got, p.Colors[role])
			}
		}
	}
	if _, err := PaletteByName("nope"); err == nil {
		t.Error("unknown palette should be an error")
	}
	if _, err := PaletteByName(" AMBER "); err != nil {
		t.Errorf("palette lookup should ignore case and spaces: %v", err)
	}
}

// recorder is a Canvas that checks everything drawn lands near the window.
type recorder struct {
	t           *testing.T
	w, h        int
	lines, dots int
}

func (r *recorder) Line(shade, x0, y0, x1, y1 int) {
	r.lines++
	r.check(shade, x0, y0)
	r.check(shade, x1, y1)
}

func (r *recorder) Dot(shade, x, y, size int) {
	r.dots++
	r.check(shade, x, y)
}

func (r *recorder) check(shade, x, y int) {
	const slack = 16
	if shade < 0 || shade >= Levels*int(numRoles) {
		r.t.Fatalf("shade %d out of range", shade)
	}
	if x < -slack || y < -slack || x > r.w+slack || y > r.h+slack {
		r.t.Fatalf("point (%d,%d) far outside %dx%d window", x, y, r.w, r.h)
	}
}

func TestDrawStaysInWindow(t *testing.T) {
	s := New(1, 500)
	for _, size := range [][2]int{{1920, 1080}, {600, 900}, {320, 200}, {3, 3}} {
		r := &recorder{t: t, w: size[0], h: size[1]}
		for i := 0; i < 200; i++ {
			s.Advance(1.7)
			s.Draw(r, r.w, r.h)
		}
		if r.lines == 0 {
			t.Errorf("%dx%d: nothing was drawn", r.w, r.h)
		}
	}
	// Degenerate sizes must be a no-op, not a panic.
	s.Draw(&recorder{t: t}, 0, 0)
}

type nullCanvas struct{}

func (nullCanvas) Line(shade, x0, y0, x1, y1 int) {}
func (nullCanvas) Dot(shade, x, y, size int)      {}

// BenchmarkFrame measures the CPU side of one 1080p frame: simulation,
// transform, clipping, projection and shading, without any X11 work.
func BenchmarkFrame(b *testing.B) {
	s := New(7, 1000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Advance(1.0 / 60)
		s.Draw(nullCanvas{}, 1920, 1080)
	}
}
