package math3d

import "math"

// Mesh is a wireframe: vertices plus the index pairs joined by an edge.
// Every generator in this file returns a mesh that fits the unit sphere.
type Mesh struct {
	Verts []Vec3
	Edges [][2]int
}

var phi = (1 + math.Sqrt(5)) / 2 // golden ratio

// Octahedron returns the regular 6-vertex, 12-edge polyhedron.
func Octahedron() *Mesh {
	return platonic([]Vec3{
		{1, 0, 0}, {-1, 0, 0},
		{0, 1, 0}, {0, -1, 0},
		{0, 0, 1}, {0, 0, -1},
	})
}

// Icosahedron returns the regular 12-vertex, 30-edge polyhedron.
func Icosahedron() *Mesh {
	var verts []Vec3
	for _, a := range []float64{-1, 1} {
		for _, b := range []float64{-phi, phi} {
			verts = append(verts, Vec3{0, a, b}, Vec3{a, b, 0}, Vec3{b, 0, a})
		}
	}
	return platonic(verts)
}

// Dodecahedron returns the regular 20-vertex, 30-edge polyhedron.
func Dodecahedron() *Mesh {
	var verts []Vec3
	signs := []float64{-1, 1}
	for _, x := range signs {
		for _, y := range signs {
			for _, z := range signs {
				verts = append(verts, Vec3{x, y, z})
			}
		}
	}
	for _, a := range []float64{-1 / phi, 1 / phi} {
		for _, b := range []float64{-phi, phi} {
			verts = append(verts, Vec3{0, a, b}, Vec3{a, b, 0}, Vec3{b, 0, a})
		}
	}
	return platonic(verts)
}

// platonic builds a regular polyhedron from its vertices. In a regular
// polyhedron the edges are exactly the vertex pairs at the shortest
// distance, so no hand-written edge table is needed.
func platonic(verts []Vec3) *Mesh {
	m := &Mesh{Verts: make([]Vec3, len(verts))}
	for i, v := range verts {
		m.Verts[i] = v.Normalize()
	}
	shortest := math.Inf(1)
	for i := range m.Verts {
		for j := i + 1; j < len(m.Verts); j++ {
			if d := m.Verts[i].Sub(m.Verts[j]).Len(); d < shortest {
				shortest = d
			}
		}
	}
	for i := range m.Verts {
		for j := i + 1; j < len(m.Verts); j++ {
			if m.Verts[i].Sub(m.Verts[j]).Len() < shortest*1.01 {
				m.Edges = append(m.Edges, [2]int{i, j})
			}
		}
	}
	return m
}

// WireSphere returns a globe: `rings` circles of latitude and `meridians`
// great circles through the poles, each drawn with `segments` lines.
func WireSphere(rings, meridians, segments int) *Mesh {
	m := &Mesh{}
	for i := 1; i <= rings; i++ {
		lat := -math.Pi/2 + math.Pi*float64(i)/float64(rings+1)
		y, r := math.Sin(lat), math.Cos(lat)
		m.addLoop(segments, func(t float64) Vec3 {
			return Vec3{r * math.Cos(t), y, r * math.Sin(t)}
		})
	}
	for i := 0; i < meridians; i++ {
		lon := math.Pi * float64(i) / float64(meridians)
		cx, cz := math.Cos(lon), math.Sin(lon)
		m.addLoop(segments, func(t float64) Vec3 {
			return Vec3{math.Cos(t) * cx, math.Sin(t), math.Cos(t) * cz}
		})
	}
	return m
}

// Ring returns concentric circles in the equatorial (XZ) plane, one per
// radius, for planetary ring systems.
func Ring(segments int, radii ...float64) *Mesh {
	m := &Mesh{}
	for _, r := range radii {
		r := r
		m.addLoop(segments, func(t float64) Vec3 {
			return Vec3{r * math.Cos(t), 0, r * math.Sin(t)}
		})
	}
	return m
}

// addLoop appends a closed polyline of n points sampled from at(angle).
func (m *Mesh) addLoop(n int, at func(t float64) Vec3) {
	base := len(m.Verts)
	for i := 0; i < n; i++ {
		m.Verts = append(m.Verts, at(2*math.Pi*float64(i)/float64(n)))
		m.Edges = append(m.Edges, [2]int{base + i, base + (i+1)%n})
	}
}
