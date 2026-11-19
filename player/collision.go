package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

const (
	nameScaffolding     = "minecraft:scaffolding"
	nameEndGateway      = "minecraft:end_gateway"
	namePistonArm       = "minecraft:piston_arm_collision"
	nameStickyPistonArm = "minecraft:sticky_piston_arm_collision"
	stateStability      = "stability"
	stateUpperBit       = "upper_block_bit"
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
	return s.boxesAsked(r, pos, b, nil)
}

// boxesFor is boxesAt for an entity whose box is lo..hi and of which nothing else is known.
func (s *Host) boxesFor(r *world.Region, pos cube.Pos, b uint32, lo, hi mgl32.Vec3) []registry.Box {
	return s.boxesAsked(r, pos, b, &shapeAsker{lo: lo, hi: hi})
}

// shapeAsker is the entity whose box is moving, as BDS's shape code sees it: the blocks
// whose shape depends on who asks (scaffolding, powder snow, end gateways) read it.
type shapeAsker struct {
	// lo and hi are the entity's box; lo's y is its feet.
	lo, hi mgl32.Vec3
	// fallDistance is how far it has fallen.
	fallDistance float32
	// descending is the actor flag a sneaking player gets over powder snow and scaffolding.
	descending bool
	// fallingBlock is set for a falling block, which lands on powder snow.
	fallingBlock bool
	// standsOnPowderSnow is set for an entity that walks on powder snow: one with the
	// entity component for it, or wearing leather boots.
	standsOnPowderSnow bool
	// riding and carrying are set for an entity on a mount or with passengers.
	riding, carrying bool
}

// boxesAsked returns the collision boxes of block b at pos for the entity a, nil for none
// (a ray, a lid). Most come straight from the state. The list may be shared: do not change
// it.
func (s *Host) boxesAsked(r *world.Region, pos cube.Pos, b uint32, a *shapeAsker) []registry.Box {
	blocks := s.reg.Blocks
	props := blocks.Properties(b)
	if !props.CollisionComputed {
		return props.Collision
	}
	switch name := blocks.Name(b); {
	case blockuse.IsDoor(blocks, b):
		return s.doorBoxes(r, pos, b)
	case name == nameScaffolding:
		return scaffoldingBoxes(blocks, pos, b, a)
	case name == namePowderSnow:
		return powderSnowBoxes(pos, a)
	case name == nameEndGateway:
		return s.endGatewayBoxes(r, a)
	case name == namePistonArm || name == nameStickyPistonArm:
		return pistonArmBoxes(r, pos, blocks.States(b))
	}
	return nil
}

// scaffoldingBoxes: BDS asks scaffolding with the entity: it has a box only for one wholly
// above it, and none for loose scaffolding or without an entity.
func scaffoldingBoxes(blocks *registry.Blocks, pos cube.Pos, b uint32, a *shapeAsker) []registry.Box {
	if a == nil {
		return nil
	}
	if stability, _ := blocks.States(b)[stateStability].(int32); stability == scaffoldingLoose {
		return nil
	}
	x, y, z := float32(pos[0]), float32(pos[1]), float32(pos[2])
	lo, hi := a.lo, a.hi
	if lo[1] < y+1-scaffoldingEpsilon || hi[0] <= x || x+1 <= lo[0] || hi[2] <= z || z+1 <= lo[2] {
		return nil
	}
	return scaffoldingBox
}

// doorBoxes is DoorBlock's shape: the lower half holds the direction and open
// state, the upper the hinge. As in BDS, a half without its other half of the same
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
