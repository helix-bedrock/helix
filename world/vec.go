package world

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/go-gl/mathgl/mgl64"
)

// Positions in the world are 64-bit: a 32-bit float far from 0, 0 cannot hold a position
// to a fraction of a block (1/8 of a block at 1,000,000), which makes movement stutter
// and collisions miss. BDS keeps 32-bit positions; the difference near spawn is below a
// ten-millionth of a block. Velocities, rotations and other small numbers stay 32-bit, as
// BDS's, and packets carry 32-bit positions: convert with Vec32 when writing one and
// Vec64 when reading one.

// Vec64 widens a 32-bit vector: a position read from a packet, or a velocity or offset to
// add to a position.
func Vec64(v mgl32.Vec3) mgl64.Vec3 {
	return mgl64.Vec3{float64(v[0]), float64(v[1]), float64(v[2])}
}

// Vec32 narrows a 64-bit vector for a packet, or a difference between two positions that
// is small enough to keep its precision.
func Vec32(v mgl64.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{float32(v[0]), float32(v[1]), float32(v[2])}
}
