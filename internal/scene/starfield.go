package scene

import (
	"math"
	"math/rand"

	"github.com/Contr0versy/orbital-background/internal/math3d"
)

// Stars live in a thick shell around the system rather than on a flat
// backdrop. As the camera drifts, nearer stars slide across the view faster
// than distant ones, which is where the parallax comes from.
const (
	starShellNear = 45.0
	starShellFar  = 170.0
	starShades    = 6
)

type star struct {
	pos math3d.Vec3
	mag float64 // intrinsic brightness, 0..1
}

func newStarfield(rng *rand.Rand, n int) []star {
	stars := make([]star, n)
	for i := range stars {
		// Uniform direction on the sphere.
		y := 2*rng.Float64() - 1
		r := math.Sqrt(1 - y*y)
		theta := 2 * math.Pi * rng.Float64()
		dist := starShellNear + (starShellFar-starShellNear)*rng.Float64()
		stars[i] = star{
			pos: math3d.Vec3{X: r * math.Cos(theta), Y: y, Z: r * math.Sin(theta)}.Scale(dist),
			// Skewed so that most stars are faint and a few stand out.
			mag: 0.4 + 0.6*math.Pow(rng.Float64(), 2),
		}
	}
	return stars
}

func (s *Scene) drawStars(c Canvas, cam *math3d.Camera, vp *math3d.Viewport) {
	// Keep stars visible on high-DPI screens.
	size := 1 + int(math.Min(vp.W, vp.H))/1400
	nearest := starShellNear - camDistance
	span := starShellFar + camDistance - nearest

	for i := range s.stars {
		st := &s.stars[i]
		v := cam.ToView(st.pos)
		if v.Z < vp.Near {
			continue
		}
		x, y := vp.Project(v)
		if x < 0 || y < 0 || x >= vp.W || y >= vp.H {
			continue
		}
		brightness := st.mag * (1 - 0.45*clamp((v.Z-nearest)/span, 0, 1))
		// Stars do not need a fine fade; a few shades keep the number of
		// draw requests down.
		brightness = math.Round(brightness*starShades) / starShades
		dot := size
		if st.mag > 0.8 {
			dot++
		}
		c.Dot(Shade(RoleStarfield, brightness), int(x), int(y), dot)
	}
}
