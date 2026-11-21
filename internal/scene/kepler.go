package scene

import (
	"math"

	"github.com/Contr0versy/orbital-background/internal/math3d"
)

// periodScale is the constant in Kepler's third law, T = periodScale·a^1.5,
// in seconds per (scene unit)^1.5. It only sets the overall tempo.
const periodScale = 3.2

// Orbit holds the classical Keplerian elements of an elliptical orbit whose
// focus is at the origin. Angles are in radians, Period in seconds. The
// reference plane is XZ (Y is "north").
type Orbit struct {
	SemiMajor     float64 // a
	Eccentricity  float64 // e, 0 <= e < 1
	Inclination   float64 // i, tilt of the orbital plane
	AscendingNode float64 // Ω, where the orbit crosses the reference plane
	ArgPeriapsis  float64 // ω, angle from the node to the closest approach
	MeanAnomaly0  float64 // M at t = 0
	Period        float64 // T
}

// PeriodFor returns the period Kepler's third law gives a body orbiting the
// central star with semi-major axis a.
func PeriodFor(a float64) float64 {
	return periodScale * math.Pow(a, 1.5)
}

// SolveKepler solves Kepler's equation M = E − e·sin E for the eccentric
// anomaly E by Newton–Raphson. It converges to ~1e-12 in a handful of
// iterations for every eccentricity used here.
func SolveKepler(m, e float64) float64 {
	m = math.Remainder(m, 2*math.Pi) // wrap into [−π, π]
	E := m + e*math.Sin(m)
	for i := 0; i < 16; i++ {
		d := (E - e*math.Sin(E) - m) / (1 - e*math.Cos(E))
		E -= d
		if math.Abs(d) < 1e-12 {
			break
		}
	}
	return E
}

// PositionAt returns the body's position relative to the focus at time t.
// Because the mean anomaly advances uniformly while the true position comes
// from the eccentric anomaly, bodies sweep equal areas in equal times: they
// speed up at periapsis and slow down at apoapsis.
func (o Orbit) PositionAt(t float64) math3d.Vec3 {
	if o.SemiMajor == 0 || o.Period == 0 {
		return math3d.Vec3{}
	}
	m := o.MeanAnomaly0 + 2*math.Pi*t/o.Period
	return o.frame().Apply(o.inPlane(SolveKepler(m, o.Eccentricity)))
}

// Path samples the full ellipse as n points, for drawing the orbit outline.
func (o Orbit) Path(n int) []math3d.Vec3 {
	frame := o.frame()
	pts := make([]math3d.Vec3, n)
	for i := range pts {
		pts[i] = frame.Apply(o.inPlane(2 * math.Pi * float64(i) / float64(n)))
	}
	return pts
}

// Apoapsis is the greatest distance from the focus.
func (o Orbit) Apoapsis() float64 { return o.SemiMajor * (1 + o.Eccentricity) }

// Periapsis is the smallest distance from the focus.
func (o Orbit) Periapsis() float64 { return o.SemiMajor * (1 - o.Eccentricity) }

// inPlane gives the position in the orbital plane for eccentric anomaly E,
// with the focus at the origin and periapsis on +X.
func (o Orbit) inPlane(E float64) math3d.Vec3 {
	b := o.SemiMajor * math.Sqrt(1-o.Eccentricity*o.Eccentricity)
	return math3d.Vec3{
		X: o.SemiMajor * (math.Cos(E) - o.Eccentricity),
		Z: b * math.Sin(E),
	}
}

// frame rotates the orbital plane into world space: ω within the plane,
// then inclination, then the node about the pole.
func (o Orbit) frame() math3d.Mat3 {
	return math3d.RotY(o.AscendingNode).
		Mul(math3d.RotX(o.Inclination)).
		Mul(math3d.RotY(o.ArgPeriapsis))
}
