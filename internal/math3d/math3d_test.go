package math3d

import (
	"math"
	"testing"
)

func TestPlatonicSolids(t *testing.T) {
	cases := []struct {
		name         string
		mesh         *Mesh
		verts, edges int
	}{
		{"octahedron", Octahedron(), 6, 12},
		{"icosahedron", Icosahedron(), 12, 30},
		{"dodecahedron", Dodecahedron(), 20, 30},
	}
	for _, c := range cases {
		if len(c.mesh.Verts) != c.verts || len(c.mesh.Edges) != c.edges {
			t.Errorf("%s: got %d verts / %d edges, want %d / %d",
				c.name, len(c.mesh.Verts), len(c.mesh.Edges), c.verts, c.edges)
		}
		for _, v := range c.mesh.Verts {
			if math.Abs(v.Len()-1) > 1e-12 {
				t.Errorf("%s: vertex %v is not on the unit sphere", c.name, v)
			}
		}
	}
}

func TestGeneratedMeshesAreWellFormed(t *testing.T) {
	for name, m := range map[string]*Mesh{
		"sphere": WireSphere(3, 3, 16),
		"ring":   Ring(32, 1.4, 2.0),
	} {
		for _, e := range m.Edges {
			if e[0] < 0 || e[0] >= len(m.Verts) || e[1] < 0 || e[1] >= len(m.Verts) {
				t.Fatalf("%s: edge %v out of range", name, e)
			}
		}
	}
	if got := len(WireSphere(3, 3, 16).Edges); got != 6*16 {
		t.Errorf("sphere: got %d edges, want %d", got, 6*16)
	}
}

func TestRotationsCompose(t *testing.T) {
	// A quarter turn about Y takes +X to -Z; a further quarter turn about X
	// takes -Z to +Y.
	got := RotX(math.Pi / 2).Mul(RotY(math.Pi / 2)).Apply(Vec3{1, 0, 0})
	want := Vec3{0, 1, 0}
	if got.Sub(want).Len() > 1e-12 {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSegmentClipping(t *testing.T) {
	vp := Viewport{W: 800, H: 600, CX: 400, CY: 300, Scale: 500, Near: 0.5}

	if _, _, _, _, ok := vp.Segment(Vec3{0, 0, -1}, Vec3{1, 0, -2}); ok {
		t.Error("line behind the camera should be rejected")
	}
	if _, _, _, _, ok := vp.Segment(Vec3{50, 0, 1}, Vec3{60, 0, 1}); ok {
		t.Error("line outside the window should be rejected")
	}

	// A line crossing the near plane and running far off-screen must come
	// back clamped to the (padded) window rectangle.
	x0, y0, x1, y1, ok := vp.Segment(Vec3{0, 0, 10}, Vec3{900, 0, -5})
	if !ok {
		t.Fatal("partially visible line was rejected")
	}
	for _, v := range []int{x0, x1} {
		if v < -clipMargin || v > 800+clipMargin {
			t.Errorf("x coordinate %d escaped the window", v)
		}
	}
	if y0 != 300 || y1 != 300 {
		t.Errorf("horizontal line moved vertically: y0=%d y1=%d", y0, y1)
	}
}
