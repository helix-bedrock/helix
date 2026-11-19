package player

import (
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// The shapes of the blocks whose collision BDS 1.26.60 works out from more than their
// state: who asks (powder snow, end gateways) or a block entity (piston arms).

// Powder snow, as BDS has it.
const (
	// powderSnowFallDistance is how far an entity must have fallen to land on powder snow.
	powderSnowFallDistance = 2.5
	// powderSnowEpsilon is how far under the block's top an entity's feet may be and still
	// stand on it.
	powderSnowEpsilon = 0.00000011920929
)

var (
	// powderSnowFull is powder snow to an entity that walks on it.
	powderSnowFull = []registry.Box{{0, 0, 0, 1, 1, 1}}
	// powderSnowLanding is powder snow to an entity falling onto it: a little lower.
	powderSnowLanding = []registry.Box{{0, 0, 0, 1, 0.9, 1}}
	// endGatewayFull is an end gateway that stops an entity: the whole block.
	endGatewayFull = []registry.Box{{0, 0, 0, 1, 1, 1}}
)

// powderSnowBoxes is BDS's powder snow: nothing without an entity, nor for one whose feet
// are under its top or that is sneaking down through it; the landing box for one that has
// fallen far enough; the whole block for a falling block or an entity that walks on it;
// else nothing, so the entity sinks in.
func powderSnowBoxes(pos cube.Pos, a *shapeAsker) []registry.Box {
	if a == nil {
		return nil
	}
	top := float32(pos[1]) + 1
	if top-powderSnowEpsilon > a.lo[1] || a.descending {
		return nil
	}
	if a.fallDistance > powderSnowFallDistance {
		return powderSnowLanding
	}
	if a.fallingBlock || a.standsOnPowderSnow {
		return powderSnowFull
	}
	return nil
}

// endGatewayBoxes is BDS's end gateway: a whole block to an entity, except in the End,
// where only an entity riding or carrying another stops on it (the rest go through it).
// BDS also needs the gateway's block entity, which Helix does not make yet, so here every
// gateway counts as having one.
func (s *Host) endGatewayBoxes(r *world.Region, a *shapeAsker) []registry.Box {
	if a == nil {
		return nil
	}
	if mcdb.Dimension(s.dimensionID(r.Dimension())) == mcdb.End && !a.riding && !a.carrying {
		return nil
	}
	return endGatewayFull
}

// pistonArmDirs is the way each arm's facing_direction points, from the piston to the
// arm: BDS's piston table, sides the other way round from other blocks.
var pistonArmDirs = [6][3]float32{{0, -1, 0}, {0, 1, 0}, {0, 0, 1}, {0, 0, -1}, {1, 0, 0}, {-1, 0, 0}}

// pistonArmShapes are an arm's three boxes when fully out, by facing_direction: the rod
// reaching back into the piston, the rod in the arm's own block, and the head.
var pistonArmShapes = [6][]registry.Box{
	{{0.3125, 0.75, 0.3125, 0.6875, 1.25, 0.6875}, {0.375, 0.25, 0.375, 0.625, 0.75, 0.625}, {0, 0, 0, 1, 0.25, 1}},
	{{0.3125, -0.25, 0.3125, 0.6875, 0.25, 0.6875}, {0.375, 0.25, 0.375, 0.625, 0.75, 0.625}, {0, 0.75, 0, 1, 1, 1}},
	{{0.3125, 0.3125, -0.25, 0.6875, 0.6875, 0.25}, {0.375, 0.375, 0.25, 0.625, 0.625, 0.75}, {0, 0, 0.75, 1, 1, 1}},
	{{0.3125, 0.3125, 0.75, 0.6875, 0.6875, 1.25}, {0.375, 0.375, 0.25, 0.625, 0.625, 0.75}, {0, 0, 0, 1, 1, 0.25}},
	{{-0.25, 0.3125, 0.3125, 0.25, 0.6875, 0.6875}, {0.25, 0.375, 0.375, 0.75, 0.625, 0.625}, {0.75, 0, 0, 1, 1, 1}},
	{{0.75, 0.3125, 0.3125, 1.25, 0.6875, 0.6875}, {0.25, 0.375, 0.375, 0.75, 0.625, 0.625}, {0, 0, 0, 0.25, 1, 1}},
}

// pistonArmBoxes is BDS's piston arm: its three boxes, moved back toward the piston by how
// far the arm is still from fully out. The piston sits one block behind the arm; without
// its block entity the arm has no boxes. A fully out arm, the usual case, returns the
// shared boxes; a moving one makes new ones.
func pistonArmBoxes(r *world.Region, pos cube.Pos, states map[string]any) []registry.Box {
	f, _ := states[stateFacingDirection].(int32)
	if f < 0 || int(f) >= len(pistonArmDirs) {
		f = 0
	}
	dir := pistonArmDirs[f]
	base := cube.Pos{pos[0] - int(dir[0]), pos[1] - int(dir[1]), pos[2] - int(dir[2])}
	be, ok := r.BlockEntity(base)
	if !ok {
		return nil
	}
	progress, ok := circuit.PistonProgress(be)
	if !ok {
		return nil
	}
	shapes := pistonArmShapes[f]
	if progress == 1 {
		return shapes
	}
	back := progress - 1
	dx, dy, dz := dir[0]*back, dir[1]*back, dir[2]*back
	out := make([]registry.Box, 0, len(shapes))
	for _, b := range shapes {
		out = append(out, registry.Box{b[0] + dx, b[1] + dy, b[2] + dz, b[3] + dx, b[4] + dy, b[5] + dz})
	}
	return out
}
