package world

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/world/cube"
)

// Line of sight for the anti X-ray (hide.go): a disguised block is sent as it is only once a ray
// from the player's eye reaches one of its open faces (a face whose neighbour is not an opaque
// full block, and that faces the eye) without passing through an opaque full block. Each face
// is tried at its centre and near its four corners, so a block seen around an edge counts.

const (
	// sightInset is how far inside a face the rays aim, so they end in the block itself.
	sightInset = 0.01
	// sightCorner is how far from a face's centre, along each of its sides, the corner rays aim.
	sightCorner = 0.4
	// maxSightSteps bounds the blocks a ray crosses.
	maxSightSteps = 400
)

// sightCorners are the five spots on a face the rays aim at, as offsets along its two sides.
var sightCorners = [5][2]float32{{0, 0}, {-sightCorner, -sightCorner}, {-sightCorner, sightCorner}, {sightCorner, -sightCorner}, {sightCorner, sightCorner}}

// seesBlock reports whether eye has a clear line to an open face of the block at p.
func (r *Region) seesBlock(eye mgl32.Vec3, p cube.Pos) bool {
	t := r.d.hiding()
	centre := blockCentre(p)
	for f := cube.FaceDown; f <= cube.FaceEast; f++ {
		axis, sign := faceAxis(f)
		// Only a face turned towards the eye can be seen.
		if (eye[axis]-centre[axis])*sign <= 0.5 {
			continue
		}
		if b, ok := r.Block(p.Side(f), 0); !ok || t.occluding(b) {
			continue
		}
		u, w := (axis+1)%3, (axis+2)%3
		for _, c := range sightCorners {
			at := centre
			at[axis] += sign * (0.5 - sightInset)
			at[u] += c[0]
			at[w] += c[1]
			if r.clearRay(eye, at, p) {
				return true
			}
		}
	}
	return false
}

// faceAxis returns the axis a face is across (0 x, 1 y, 2 z) and which way it points.
func faceAxis(f cube.Face) (int, float32) {
	switch f {
	case cube.FaceDown:
		return 1, -1
	case cube.FaceUp:
		return 1, 1
	case cube.FaceNorth:
		return 2, -1
	case cube.FaceSouth:
		return 2, 1
	case cube.FaceWest:
		return 0, -1
	}
	return 0, 1
}

// clearRay walks the blocks from from to to, which lies in target, and reports whether it gets
// to target without crossing an opaque full block or one not loaded.
func (r *Region) clearRay(from, to mgl32.Vec3, target cube.Pos) bool {
	t := r.d.hiding()
	dir := to.Sub(from)
	at := blockAt(from)
	var step [3]int
	var tMax, tDelta [3]float64
	for a := range 3 {
		d := float64(dir[a])
		switch {
		case d > 0:
			step[a] = 1
			tMax[a] = (float64(at[a]+1) - float64(from[a])) / d
			tDelta[a] = 1 / d
		case d < 0:
			step[a] = -1
			tMax[a] = (float64(from[a]) - float64(at[a])) / -d
			tDelta[a] = 1 / -d
		default:
			tMax[a], tDelta[a] = math.Inf(1), math.Inf(1)
		}
	}
	for range maxSightSteps {
		if at == target {
			return true
		}
		a := 0
		if tMax[1] < tMax[a] {
			a = 1
		}
		if tMax[2] < tMax[a] {
			a = 2
		}
		if tMax[a] > 1 {
			return at == target
		}
		at[a] += step[a]
		tMax[a] += tDelta[a]
		if at == target {
			return true
		}
		b, ok := r.Block(at, 0)
		if !ok || t.occluding(b) {
			return false
		}
	}
	return false
}
