package fall

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Scaffolding as BDS 1.26.60's ScaffoldingBlock (Linux, IDA): stability sub_D510EC0, its
// update sub_D50FE50, onPlace listener sub_D50F860, neighborChanged (slot 75) sub_D510D40,
// tick listener sub_D50FAB0, mayPlace (slot 68) sub_D511640, getPlacementBlock sub_D5114D0.
//
// Each scaffolding keeps how far it is from something holding it up: 0 on a block that
// holds things on top (or on the bottom of the world), the same as the scaffolding under it,
// else one more than the steadiest scaffolding beside it. At 7 nothing holds it: a
// scaffolding that never settled (stability_check 0) falls as a falling block, one that did
// breaks and drops its item. Each break changes its neighbours, which look again a tick
// later, so a tower whose base goes comes down block by block.

// Scaffolding's names and numbers.
const (
	nameScaffolding     = "minecraft:scaffolding"
	stateStability      = "stability"
	stateStabilityCheck = "stability_check"
	// scaffoldLoose is the stability of a scaffolding nothing holds up.
	scaffoldLoose = 7
	// BDS's tick delays: placed 2, a neighbour changed 1.
	delayScaffoldPlaced    = 2
	delayScaffoldNeighbour = 1
)

// Names a placed scaffolding may not go into, and the ones a loose one may go over.
const (
	nameLava         = "minecraft:lava"
	nameFlowingLava  = "minecraft:flowing_lava"
	nameWater        = "minecraft:water"
	nameFlowingWater = "minecraft:flowing_water"
)

// scaffoldStates reads a scaffolding state's stability and stability_check.
func scaffoldStates(states map[string]any) (int8, bool) {
	s, _ := states[stateStability].(int32)
	c, _ := states[stateStabilityCheck].(uint8)
	return int8(min(max(s, 0), scaffoldLoose)), c != 0 //nolint:gosec // clamped to 0..7
}

// findScaffolds fills the state of every stability and stability_check.
func (t *Table) findScaffolds() {
	for s := range int32(scaffoldLoose + 1) {
		for c := range uint8(2) {
			if id, ok := t.blocks.StateID(nameScaffolding, map[string]any{stateStability: s, stateStabilityCheck: c}); ok {
				t.scaffold[s][c] = id
			}
		}
	}
}

// Stability is the stability a scaffolding at pos has where it is, 0 to 7 (BDS's
// getStability). Runs: owner of the region.
func (t *Table) Stability(r *world.Region, pos cube.Pos) int {
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return scaffoldLoose
	}
	if s := t.state(below); s.kind == Scaffold {
		return int(s.stability)
	}
	if pos[1] <= r.Dimension().Range()[0] || t.holdsScaffold(below) {
		return 0
	}
	best := scaffoldLoose
	for _, f := range [...]cube.Face{cube.FaceNorth, cube.FaceEast, cube.FaceSouth, cube.FaceWest} {
		if b, ok := r.Block(pos.Side(f), 0); ok {
			if s := t.state(b); s.kind == Scaffold {
				best = min(best, int(s.stability)+1)
			}
		}
	}
	return best
}

// holdsScaffold reports whether below holds a scaffolding on its top, BDS's
// canProvideSupport up.
func (t *Table) holdsScaffold(below uint32) bool {
	return t.support != nil && t.support(below, cube.FaceUp)
}

// ScaffoldState is the scaffolding state a scaffolding placed at pos gets: its stability
// there, not yet settled (BDS's getPlacementBlock).
func (t *Table) ScaffoldState(r *world.Region, pos cube.Pos) uint32 {
	return t.scaffold[t.Stability(r, pos)][0]
}

// ScaffoldMayPlace is BDS's mayPlace for scaffolding at pos: never into lava, and a
// scaffolding nothing would hold up only over air or water, where it falls at once.
func (t *Table) ScaffoldMayPlace(r *world.Region, pos cube.Pos) bool {
	if at, ok := r.Block(pos, 0); ok {
		if n := t.blocks.Name(at); n == nameLava || n == nameFlowingLava {
			return false
		}
	}
	if t.Stability(r, pos) < scaffoldLoose {
		return true
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return false
	}
	if t.holdsScaffold(below) {
		return true
	}
	n := t.blocks.Name(below)
	return below == t.air || n == nameWater || n == nameFlowingWater
}

// updateScaffold writes the stability a scaffolding at pos has now into its state, keeping
// stability_check, and reports whether something holds it up (BDS's sub_D50FE50).
func (t *Table) updateScaffold(r *world.Region, pos cube.Pos, block uint32) bool {
	s := t.Stability(r, pos)
	c := 0
	if t.state(block).checked {
		c = 1
	}
	if next := t.scaffold[s][c]; next != block && r.SetBlock(pos, 0, next) {
		r.UpdateNeighbours(pos)
	}
	return s < scaffoldLoose
}

// scaffoldPlaced is the onPlace listener: the stability is set at once and the scaffolding
// looks again in 2 ticks, unless a look is already waiting.
func (k *Ticker) scaffoldPlaced(r *world.Region, pos cube.Pos, block uint32) {
	k.t.updateScaffold(r, pos, block)
	if b, ok := r.Block(pos, 0); ok && k.t.Kind(b) == Scaffold && !r.HasScheduledTick(pos, b) {
		r.ScheduleTick(pos, b, delayScaffoldPlaced, 0)
	}
}

// scaffoldNeighbour is neighborChanged: a scaffolding whose stability would change looks
// again in a tick.
func (k *Ticker) scaffoldNeighbour(r *world.Region, pos cube.Pos, block uint32) {
	if k.t.Stability(r, pos) != int(k.t.state(block).stability) {
		r.ScheduleTick(pos, block, delayScaffoldNeighbour, 0)
	}
}

// tickScaffold is the tick listener: a held scaffolding is marked settled; a loose one
// breaks with its drops if it had settled, else falls.
func (k *Ticker) tickScaffold(r *world.Region, pos cube.Pos, block uint32) {
	if !k.t.updateScaffold(r, pos, block) {
		b, ok := r.Block(pos, 0)
		if !ok || k.t.Kind(b) != Scaffold {
			return
		}
		if k.t.state(b).checked {
			if k.brk != nil {
				k.brk(r, pos, b)
			}
			return
		}
		k.tickBlock(r, pos)
		return
	}
	b, ok := r.Block(pos, 0)
	if !ok || k.t.Kind(b) != Scaffold {
		return
	}
	if s := k.t.state(b); !s.checked {
		if r.SetBlock(pos, 0, k.t.scaffold[s.stability][1]) {
			r.UpdateNeighbours(pos)
		}
	}
}
