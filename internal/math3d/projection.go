package math3d

// Camera is a pinhole camera. View space has X to the right, Y up and Z
// pointing forward, so a view-space Z is the depth of the point.
type Camera struct {
	Pos            Vec3
	Right, Up, Fwd Vec3
}

// LookAt builds a camera at pos aimed at target.
func LookAt(pos, target, worldUp Vec3) Camera {
	fwd := target.Sub(pos).Normalize()
	right := fwd.Cross(worldUp).Normalize()
	return Camera{Pos: pos, Right: right, Up: right.Cross(fwd), Fwd: fwd}
}

// ToView transforms a world-space point into view space.
func (c *Camera) ToView(p Vec3) Vec3 {
	d := p.Sub(c.Pos)
	return Vec3{d.Dot(c.Right), d.Dot(c.Up), d.Dot(c.Fwd)}
}

// clipMargin lets lines run slightly past the window edge so that wide
// lines are not visibly cut off at the border.
const clipMargin = 8

// Viewport maps view space onto window pixels with a perspective divide.
type Viewport struct {
	W, H   float64 // window size in pixels
	CX, CY float64 // pixel the optical axis passes through
	Scale  float64 // focal length: pixels per unit at depth 1
	Near   float64 // near clipping depth, must be > 0
}

// Project maps a view-space point with v.Z > 0 to pixel coordinates.
func (vp *Viewport) Project(v Vec3) (x, y float64) {
	k := vp.Scale / v.Z
	return vp.CX + v.X*k, vp.CY - v.Y*k
}

// Segment turns a view-space line into a drawable pixel line. It clips
// against the near plane in 3D, projects, then clips against the window
// rectangle, so the result always fits comfortably in 16-bit
// coordinates. ok is false when nothing of the line is visible.
func (vp *Viewport) Segment(a, b Vec3) (x0, y0, x1, y1 int, ok bool) {
	if a.Z < vp.Near && b.Z < vp.Near {
		return 0, 0, 0, 0, false
	}
	if a.Z < vp.Near {
		a = a.Lerp(b, (vp.Near-a.Z)/(b.Z-a.Z))
	} else if b.Z < vp.Near {
		b = b.Lerp(a, (vp.Near-b.Z)/(a.Z-b.Z))
	}
	ax, ay := vp.Project(a)
	bx, by := vp.Project(b)
	ax, ay, bx, by, ok = clipRect(ax, ay, bx, by,
		-clipMargin, -clipMargin, vp.W+clipMargin, vp.H+clipMargin)
	if !ok {
		return 0, 0, 0, 0, false
	}
	return roundInt(ax), roundInt(ay), roundInt(bx), roundInt(by), true
}

// clipRect is Liang–Barsky line clipping against an axis-aligned rectangle.
func clipRect(x0, y0, x1, y1, xmin, ymin, xmax, ymax float64) (float64, float64, float64, float64, bool) {
	t0, t1 := 0.0, 1.0
	dx, dy := x1-x0, y1-y0
	edge := func(p, q float64) bool {
		if p == 0 {
			return q >= 0 // parallel to this edge: inside or entirely out
		}
		r := q / p
		if p < 0 {
			if r > t1 {
				return false
			}
			if r > t0 {
				t0 = r
			}
		} else {
			if r < t0 {
				return false
			}
			if r < t1 {
				t1 = r
			}
		}
		return true
	}
	if !edge(-dx, x0-xmin) || !edge(dx, xmax-x0) || !edge(-dy, y0-ymin) || !edge(dy, ymax-y0) {
		return 0, 0, 0, 0, false
	}
	return x0 + t0*dx, y0 + t0*dy, x0 + t1*dx, y0 + t1*dy, true
}

func roundInt(v float64) int {
	if v < 0 {
		return int(v - 0.5)
	}
	return int(v + 0.5)
}
