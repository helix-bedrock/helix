package player

import (
	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

const (
	nameScaffolding = "minecraft:scaffolding"
	stateStability  = "stability"
	stateUpperBit   = "upper_block_bit"
	// scaffoldingLoose is the stability BDS gives no box at all.
	scaffoldingLoose = 7
	// scaffoldingEpsilon is how far under its top BDS still counts a box as above it.
	scaffoldingEpsilon = 0.000001
)

// scaffoldingBox is what scaffolding is to an entity above it. Shared: do not change it.
var scaffoldingBox = []registry.Box{{0, 0, 0, 1, 1, 1}}

// boxesAt returns the collision boxes of block b at pos, for anything that is not an entity's
// box moving: a ray, a stuck arrow, a lid. Most come straight from the state. The list is
// shared: do not change it.
func (s *Host) boxesAt(r *world.Region, pos cube.Pos, b uint32) []registry.Box {
	blocks := s.reg.Blocks
	props := blocks.Properties(b)
	if !props.CollisionComputed {
		return props.Collision
	}
	if !blockuse.IsDoor(blocks, b) {
		return nil
	}
	return s.doorBoxes(r, pos, b)
}

// boxesFor is boxesAt for an entity whose box is lo..hi. BDS asks scaffolding with the entity:
// it has a box only for one wholly above it (0xD5100A0). Without one it has none.
func (s *Host) boxesFor(r *world.Region, pos cube.Pos, b uint32, lo, hi mgl64.Vec3) []registry.Box {
	blocks := s.reg.Blocks
	props := blocks.Properties(b)
	if !props.CollisionComputed {
		return props.Collision
	}
	if blocks.Name(b) != nameScaffolding {
		return s.boxesAt(r, pos, b)
	}
	if stability, _ := blocks.States(b)[stateStability].(int32); stability == scaffoldingLoose {
		return nil
	}
	x, y, z := float64(pos[0]), float64(pos[1]), float64(pos[2])
	if lo[1] < y+1-scaffoldingEpsilon || hi[0] <= x || x+1 <= lo[0] || hi[2] <= z || z+1 <= lo[2] {
		return nil
	}
	return scaffoldingBox
}

// doorBoxes is DoorBlock's shape (0xCB180D0): the lower half holds the direction and open
// state, the upper the hinge. As in BDS (0xCB17EC0), a half without its other half of the same
// door reads as a door with everything at zero.
func (s *Host) doorBoxes(r *world.Region, pos cube.Pos, b uint32) []registry.Box {
	blocks := s.reg.Blocks
	states := blocks.States(b)
	otherAt := pos.Side(cube.FaceUp)
	upperHalf := false
	if v, _ := states[stateUpperBit].(uint8); v != 0 {
		otherAt, upperHalf = pos.Side(cube.FaceDown), true
	}
	other, ok := r.Block(otherAt, 0)
	if !ok || blocks.Name(other) != blocks.Name(b) {
		return registry.DoorBoxes(nil, nil)
	}
	if upperHalf {
		return registry.DoorBoxes(blocks.States(other), states)
	}
	return registry.DoorBoxes(states, blocks.States(other))
}

// boxAt is a block's collision box placed at the block at.
func boxAt(at cube.Pos, box registry.Box) (lo, hi mgl64.Vec3) {
	base := mgl64.Vec3{float64(at[0]), float64(at[1]), float64(at[2])}
	return base.Add(mgl64.Vec3{float64(box[0]), float64(box[1]), float64(box[2])}),
		base.Add(mgl64.Vec3{float64(box[3]), float64(box[4]), float64(box[5])})
}
