package player

import (
	"math"
	"math/rand/v2"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Falling anvils, as BDS 1.26.60 does them. An anvil's falling block hurts what it lands in
// (AnvilBlock's falling hurt flag; FallingBlockActor's causeFallDamage with
// its defaults of 2 a block, at most 40, cause Anvil). The same landing may wear the anvil, by
// chance 0.05 + 0.05 a block fallen: an anvil becomes chipped, a chipped one damaged, and a
// damaged one breaks, the falling block vanishing with nothing placed or dropped. A placed
// anvil sends LevelEvent 1022 (its onLand).
const (
	causeAnvil DamageCause = 17

	// anvilHurtPerBlock is what each block an anvil falls hurts.
	anvilHurtPerBlock = 2
	// anvilWearBase and anvilWearPerBlock are the chance a landing wears the anvil.
	anvilWearBase     = 0.05
	anvilWearPerBlock = 0.05
	// anvilWornOut is the wear at which a landing anvil breaks.
	anvilWornOut = 2
)

// setupHurt gives a falling anvil its hurt.
func (f *FallingBlock) setupHurt() {
	if anvilWear(f.s.reg.Blocks.Name(f.block)) < 0 {
		return
	}
	f.hurtPerBlock, f.hurtCause, f.anvil = anvilHurtPerBlock, causeAnvil, true
}

// anvilWear is how worn an anvil block is: 0 to 2, 3 for the deprecated anvil, -1 for any
// other block.
func anvilWear(name string) int {
	switch name {
	case anvilBlock:
		return 0
	case anvilChipped:
		return 1
	case anvilDamaged:
		return anvilWornOut
	case anvilDeprecated:
		return anvilWornOut + 1
	}
	return -1
}

// wearAnvil is causeFallDamage's anvil part, for a fall of blocks: by chance the anvil wears
// one step, or breaks when it was already damaged.
func (f *FallingBlock) wearAnvil(blocks float32) {
	if !f.anvil || blocks <= 0 || float64(blocks)*anvilWearPerBlock+anvilWearBase <= float64(rand.Float32()) { //nolint:gosec // gameplay chance, not a secret
		return
	}
	wear := anvilWear(f.s.reg.Blocks.Name(f.block))
	if wear >= anvilWornOut {
		f.broken = true
		return
	}
	next := anvilChipped
	if wear == 1 {
		next = anvilDamaged
	}
	if id, ok := f.s.reg.Blocks.StateID(next, f.s.reg.Blocks.States(f.block)); ok {
		f.block = id
	}
}

// fallenBlocks is how many blocks a fall counts for: ceil(fallen - 1).
func fallenBlocks(fallen float32) float32 {
	return float32(math.Ceil(float64(fallen - 1)))
}

// anvilLanded is AnvilBlock's onLand: the landing sound's level event.
func anvilLanded(r *world.Region, pos cube.Pos) {
	levelEvent(r, pos, blockVec(pos), packet.LevelEventSoundAnvilLand, 0)
}
