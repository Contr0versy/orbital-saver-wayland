package math3d

import "math"

// Mat3 is a 3x3 rotation matrix in row-major order.
type Mat3 [9]float64

// Identity returns the matrix that leaves vectors unchanged.
func Identity() Mat3 {
	return Mat3{
		1, 0, 0,
		0, 1, 0,
		0, 0, 1,
	}
}

// RotX rotates by a radians about the X axis.
func RotX(a float64) Mat3 {
	s, c := math.Sincos(a)
	return Mat3{
		1, 0, 0,
		0, c, -s,
		0, s, c,
	}
}

// RotY rotates by a radians about the Y axis.
func RotY(a float64) Mat3 {
	s, c := math.Sincos(a)
	return Mat3{
		c, 0, s,
		0, 1, 0,
		-s, 0, c,
	}
}

// RotZ rotates by a radians about the Z axis.
func RotZ(a float64) Mat3 {
	s, c := math.Sincos(a)
	return Mat3{
		c, -s, 0,
		s, c, 0,
		0, 0, 1,
	}
}

// Mul returns m·n: the rotation that applies n first, then m.
func (m Mat3) Mul(n Mat3) Mat3 {
	var out Mat3
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			out[r*3+c] = m[r*3]*n[c] + m[r*3+1]*n[3+c] + m[r*3+2]*n[6+c]
		}
	}
	return out
}

// Apply rotates v by m.
func (m Mat3) Apply(v Vec3) Vec3 {
	return Vec3{
		m[0]*v.X + m[1]*v.Y + m[2]*v.Z,
		m[3]*v.X + m[4]*v.Y + m[5]*v.Z,
		m[6]*v.X + m[7]*v.Y + m[8]*v.Z,
	}
}
