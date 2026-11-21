// Package scene models the solar system (bodies, Keplerian orbits,
// starfield, colour palettes) and renders it as shaded 2D lines onto a
// Canvas. It knows nothing about the display.
package scene

import (
	"math"
	"math/rand"

	"github.com/Contr0versy/orbital-background/internal/math3d"
)

// Canvas receives the finished 2D primitives for one frame. shade is an
// index into the table returned by Palette.Ramp.
type Canvas interface {
	Line(shade, x0, y0, x1, y1 int)
	Dot(shade, x, y, size int)
}

const (
	deg = math.Pi / 180

	// The camera circles the system slowly while bobbing up and down.
	camDistance    = 34.0
	camElevMid     = 19 * deg
	camElevSwing   = 6 * deg
	camElevPeriod  = 110.0             // seconds per bob
	camAzimuthRate = 2 * math.Pi / 300 // one lap every five minutes

	orbitSegments = 96
	fitMargin     = 1.08 // breathing room around the system when fitting it to the window
	fogFloor      = 0.14 // brightness of the farthest lines
)

// part is one wireframe layer of a body, e.g. a planet's globe or its rings.
type part struct {
	mesh  *math3d.Mesh
	role  Role
	scale float64 // size relative to the body's radius
	spin  float64 // rad/s about the body's axis
	glow  float64 // > 0: self-lit at this brightness, ignoring depth fog
}

type body struct {
	radius float64
	orbit  Orbit
	parent int         // index of the body this one orbits, -1 for the star
	axis   math3d.Mat3 // axial tilt
	parts  []part
	path   []math3d.Vec3 // orbit outline in world space, nil if not drawn
	pos    math3d.Vec3   // current world position
}

// Scene is the animated solar system.
type Scene struct {
	bodies   []body
	stars    []star
	time     float64
	extent   float64 // radius of the disc that encloses every orbit
	azimuth0 float64
	scratch  []math3d.Vec3 // reused per-frame vertex buffer
}

// New builds the system. The layout is fixed; seed only varies where each
// body starts on its orbit, how the orbits are oriented, and the starfield.
func New(seed int64, starCount int) *Scene {
	rng := rand.New(rand.NewSource(seed))
	s := &Scene{azimuth0: rng.Float64() * 2 * math.Pi}
	s.build(rng)
	s.stars = newStarfield(rng, starCount)

	maxVerts := orbitSegments
	for i := range s.bodies {
		b := &s.bodies[i]
		if b.parent < 0 && b.orbit.SemiMajor > 0 {
			b.path = b.orbit.Path(orbitSegments)
			s.extent = math.Max(s.extent, b.orbit.Apoapsis()+b.radius)
		}
		for _, p := range b.parts {
			if n := len(p.mesh.Verts); n > maxVerts {
				maxVerts = n
			}
		}
	}
	s.scratch = make([]math3d.Vec3, maxVerts)
	s.Advance(0)
	return s
}

func (s *Scene) build(rng *rand.Rand) {
	var (
		octa   = math3d.Octahedron()
		icosa  = math3d.Icosahedron()
		dodeca = math3d.Dodecahedron()
		globe  = math3d.WireSphere(3, 3, 16)
		rings  = math3d.Ring(40, 1.45, 1.75, 2.05)
	)
	angle := func() float64 { return rng.Float64() * 2 * math.Pi }

	// add places a body on an orbit around the star and returns its index.
	add := func(a, e, incl, radius, tilt float64, parts ...part) int {
		s.bodies = append(s.bodies, body{
			radius: radius,
			parent: -1,
			axis:   math3d.RotY(angle()).Mul(math3d.RotZ(tilt * deg)),
			parts:  parts,
			orbit: Orbit{
				SemiMajor:     a,
				Eccentricity:  e,
				Inclination:   incl * deg,
				AscendingNode: angle(),
				ArgPeriapsis:  angle(),
				MeanAnomaly0:  angle(),
				Period:        PeriodFor(a),
			},
		})
		return len(s.bodies) - 1
	}
	// moon puts a small body in orbit around another one.
	moon := func(parent int, mesh *math3d.Mesh, a, incl, period, radius float64) {
		s.bodies = append(s.bodies, body{
			radius: radius,
			parent: parent,
			axis:   math3d.Identity(),
			parts:  []part{{mesh: mesh, role: RoleAccent, scale: 1, spin: 1.8}},
			orbit: Orbit{
				SemiMajor:     a,
				Eccentricity:  0.06,
				Inclination:   incl * deg,
				AscendingNode: angle(),
				MeanAnomaly0:  angle(),
				Period:        period,
			},
		})
	}

	// The star: a bright icosahedron inside a fainter counter-rotating
	// dodecahedron (its dual), with no orbit of its own.
	add(0, 0, 0, 1.05, 8,
		part{mesh: icosa, role: RoleStar, scale: 1, spin: 0.35, glow: 1},
		part{mesh: dodeca, role: RoleStar, scale: 1.38, spin: -0.2, glow: 0.42},
	)

	//  semi-major  ecc   incl  radius  tilt
	add(2.6, 0.20, 7.0, 0.20, 4, part{mesh: octa, role: RolePlanet, scale: 1, spin: 1.6})
	add(3.9, 0.03, 3.4, 0.32, 14, part{mesh: icosa, role: RolePlanet, scale: 1, spin: 0.9})

	blue := add(5.4, 0.07, 0.0, 0.36, 23, part{mesh: globe, role: RolePlanet, scale: 1, spin: 0.8})
	moon(blue, octa, 0.80, 18, 6.5, 0.09)

	add(7.0, 0.11, 1.9, 0.27, 25, part{mesh: octa, role: RolePlanet, scale: 1, spin: 1.1})

	ringed := add(9.2, 0.05, 2.5, 0.60, 27,
		part{mesh: globe, role: RolePlanet, scale: 1, spin: 0.5},
		part{mesh: rings, role: RoleAccent, scale: 1},
	)
	moon(ringed, icosa, 1.75, 27, 11, 0.11)

	add(11.6, 0.14, 5.5, 0.42, 98, part{mesh: dodeca, role: RolePlanet, scale: 1, spin: 0.7})

	// A comet on a long, eccentric orbit: it lingers at the edge of the
	// system and whips around the star, which makes Kepler's second law
	// easy to see.
	add(7.6, 0.74, 9.0, 0.13, 0, part{mesh: octa, role: RoleAccent, scale: 1, spin: 3.0})
}

// Advance moves the simulation forward by dt seconds.
func (s *Scene) Advance(dt float64) {
	s.time += dt
	// Parents always precede their moons in s.bodies.
	for i := range s.bodies {
		b := &s.bodies[i]
		b.pos = b.orbit.PositionAt(s.time)
		if b.parent >= 0 {
			b.pos = b.pos.Add(s.bodies[b.parent].pos)
		}
	}
}

// Draw renders the current state for a w×h pixel window.
func (s *Scene) Draw(c Canvas, w, h int) {
	if w < 2 || h < 2 {
		return
	}
	cam := s.camera(s.elevation())
	vp := s.viewport(w, h)

	s.drawStars(c, &cam, &vp)
	for i := range s.bodies {
		s.drawOrbit(c, &cam, &vp, &s.bodies[i])
	}
	for i := range s.bodies {
		s.drawBody(c, &cam, &vp, &s.bodies[i])
	}
}

// elevation is the camera's current angle above the orbital plane.
func (s *Scene) elevation() float64 {
	return camElevMid + camElevSwing*math.Sin(s.time*2*math.Pi/camElevPeriod)
}

func (s *Scene) camera(elev float64) math3d.Camera {
	azimuth := s.azimuth0 + s.time*camAzimuthRate
	pos := math3d.Vec3{
		X: math.Cos(elev) * math.Sin(azimuth),
		Y: math.Sin(elev),
		Z: math.Cos(elev) * math.Cos(azimuth),
	}.Scale(camDistance)
	return math3d.LookAt(pos, math3d.Vec3{}, math3d.Vec3{Y: 1})
}

// viewport picks the focal length so the whole system fits the window at
// any aspect ratio, with the star in the centre. Seen from above the plane,
// the near side of the orbital disc projects lower and larger than the far
// side, so it is the near edge that limits the fit vertically.
func (s *Scene) viewport(w, h int) math3d.Viewport {
	r, d := s.extent, float64(camDistance)
	lateral := r / math.Sqrt(d*d-r*r) // half-width of the disc per unit focal length

	// How far the near edge of the disc projects below the star per unit
	// focal length. The focal length is fixed by the steepest view the
	// camera reaches, so the picture does not zoom as the camera bobs.
	steep := camElevMid + camElevSwing
	below := r * math.Sin(steep) / (d - r*math.Cos(steep))
	scale := math.Min(float64(w)/(2*lateral), float64(h)/(2*below)) / fitMargin

	return math3d.Viewport{
		W:     float64(w),
		H:     float64(h),
		CX:    float64(w) / 2,
		CY:    float64(h) / 2,
		Scale: scale,
		Near:  0.5,
	}
}

// fog maps view depth to brightness: full at the near edge of the system,
// fading smoothly to fogFloor at the far edge.
func (s *Scene) fog(z float64) float64 {
	f := clamp((camDistance+s.extent-z)/(2*s.extent), 0, 1)
	return fogFloor + (1-fogFloor)*f
}

func (s *Scene) drawOrbit(c Canvas, cam *math3d.Camera, vp *math3d.Viewport, b *body) {
	if b.path == nil {
		return
	}
	view := s.scratch[:len(b.path)]
	for i, p := range b.path {
		view[i] = cam.ToView(p)
	}
	for i := range view {
		p, q := view[i], view[(i+1)%len(view)]
		line(c, vp, p, q, Shade(RoleOrbit, s.fog((p.Z+q.Z)/2)))
	}
}

func (s *Scene) drawBody(c Canvas, cam *math3d.Camera, vp *math3d.Viewport, b *body) {
	centre := cam.ToView(b.pos)
	for i := range b.parts {
		p := &b.parts[i]
		size := b.radius * p.scale
		rot := b.axis.Mul(math3d.RotY(p.spin * s.time))

		view := s.scratch[:len(p.mesh.Verts)]
		for j, v := range p.mesh.Verts {
			view[j] = cam.ToView(b.pos.Add(rot.Apply(v).Scale(size)))
		}
		for _, e := range p.mesh.Edges {
			v0, v1 := view[e[0]], view[e[1]]
			z := (v0.Z + v1.Z) / 2

			brightness := p.glow
			if brightness == 0 {
				brightness = s.fog(z)
			}
			// Dim the edges on the far side of the body itself. Without
			// this a see-through wireframe reads as flat.
			brightness *= 0.7 + 0.3*clamp((centre.Z-z)/size, -1, 1)

			line(c, vp, v0, v1, Shade(p.role, brightness))
		}
	}
}

func line(c Canvas, vp *math3d.Viewport, a, b math3d.Vec3, shade int) {
	if x0, y0, x1, y1, ok := vp.Segment(a, b); ok {
		c.Line(shade, x0, y0, x1, y1)
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
