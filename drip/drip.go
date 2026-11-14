// Package drip is pointed dripstone dripping water and lava into cauldrons, as BDS 1.26.60
// does it: a stalactite's random tick (0xD553150, 0xD553380) finds the source above its base
// and a cauldron below its tip that takes it, shows the drip and queues a tick on the
// cauldron; the cauldron's queued tick (CauldronBlock's BlockQueuedTickEvent handler
// 0xCB4A1D0, its dripstone part) looks up for a tip again and fills it. The research note is
// private. The cauldron itself (levels, block entity) is the player package's, reached
// through Cauldrons.
package drip

import (
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

const (
	nameDripstone = "minecraft:pointed_dripstone"
	nameCauldron  = "minecraft:cauldron"
	nameWater     = "minecraft:water"
	nameLava      = "minecraft:lava"

	stateHanging   = "hanging"
	stateThickness = "dripstone_thickness"
	stateDepth     = "liquid_depth"
	thicknessTip   = "tip"

	// reach is how far BDS looks: from a tip up to its base, from a tip down to a cauldron,
	// and from a cauldron up to a tip.
	reach = 10
	// waterOdds and lavaOdds are the random tick's chances a base with water or lava above it
	// drips (BDS's random float at most 0.42 or 0.07).
	waterOdds = 0.42
	lavaOdds  = 0.07
	// tickDelay is added to the drop's height for the cauldron's tick.
	tickDelay = 50
	// dripParticle is the drip LevelEvent's data: the dripstone drip particle, +1 for lava.
	dripParticle = 0x1C
)

// kind is what the dripping reads of a block state.
type kind uint8

const (
	kindOther kind = iota
	// kindHanging is a hanging pointed dripstone that is not its tip, kindTip its tip.
	kindHanging
	kindTip
	kindCauldron
	// kindWater and kindLava are sources (liquid_depth 0).
	kindWater
	kindLava
)

// Cauldrons is the cauldron side. Runs: owner of the region.
type Cauldrons interface {
	// DripAccepts reports whether the cauldron at pos takes a drip of lava, or of water.
	DripAccepts(r *world.Region, pos cube.Pos, lava bool) bool
	// Drip fills the cauldron at pos from a stalactite.
	Drip(r *world.Region, pos cube.Pos, lava bool)
}

// Ticker adds dripping into cauldrons to another BlockTicker.
type Ticker struct {
	world.BlockTicker
	air       uint32
	kinds     []kind
	cauldrons Cauldrons
}

// NewTicker returns a Ticker that hands everything else to next.
func NewTicker(blocks *registry.Blocks, cauldrons Cauldrons, next world.BlockTicker) *Ticker {
	t := &Ticker{BlockTicker: next, air: blocks.Air(), cauldrons: cauldrons, kinds: make([]kind, blocks.Count())}
	for id := range uint32(blocks.Count()) { //nolint:gosec // block counts fit
		t.kinds[id] = kindOf(blocks, id)
	}
	return t
}

// kindOf reads one state.
func kindOf(blocks *registry.Blocks, id uint32) kind {
	states := blocks.States(id)
	switch blocks.Name(id) {
	case nameDripstone:
		if hanging, _ := states[stateHanging].(uint8); hanging == 0 {
			return kindOther
		}
		if th, _ := states[stateThickness].(string); th == thicknessTip {
			return kindTip
		}
		return kindHanging
	case nameCauldron:
		return kindCauldron
	case nameWater, nameLava:
		if depth, _ := states[stateDepth].(int32); depth != 0 {
			return kindOther
		}
		if blocks.Name(id) == nameLava {
			return kindLava
		}
		return kindWater
	}
	return kindOther
}

func (t *Ticker) kind(r *world.Region, pos cube.Pos) kind {
	b, ok := r.Block(pos, 0)
	if !ok || int(b) >= len(t.kinds) {
		return kindOther
	}
	return t.kinds[b]
}

// RandomTicks adds hanging pointed dripstone.
func (t *Ticker) RandomTicks(block uint32, layer uint8) bool {
	if layer == 0 && int(block) < len(t.kinds) {
		if k := t.kinds[block]; k == kindHanging || k == kindTip {
			return true
		}
	}
	return t.BlockTicker != nil && t.BlockTicker.RandomTicks(block, layer)
}

// RandomTick is BDS's pointed dripstone random tick, its cauldron part: the base of a
// stalactite with a source above it, by chance, drips toward a cauldron that takes it.
func (t *Ticker) RandomTick(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
	if layer != 0 || int(block) >= len(t.kinds) || (t.kinds[block] != kindHanging && t.kinds[block] != kindTip) {
		if t.BlockTicker != nil {
			t.BlockTicker.RandomTick(r, pos, block, layer)
		}
		return
	}
	lava, ok := t.fluidAbove(r, pos)
	if !ok {
		return
	}
	odds := float32(waterOdds)
	if lava {
		odds = lavaOdds
	}
	if rand.Float32() > odds { //nolint:gosec // gameplay chance, not a secret
		return
	}
	t.dripFromBase(r, pos, lava)
}

// dripFromBase is BDS's 0xD553380: only the base (no dripstone above it) drips; its tip, then
// the cauldron under it, then the drip particle and the cauldron's tick.
func (t *Ticker) dripFromBase(r *world.Region, base cube.Pos, lava bool) {
	if k := t.kind(r, base.Side(cube.FaceUp)); k == kindHanging || k == kindTip {
		return
	}
	tip, ok := t.tipBelow(r, base)
	if !ok {
		return
	}
	at, ok := t.cauldronBelow(r, tip, lava)
	if !ok {
		return
	}
	data := int32(dripParticle)
	if lava {
		data++
	}
	r.Broadcast(tip, &packet.LevelEvent{
		EventType: packet.LevelEventParticlesDripstoneDrip, EventData: data,
		Position: mgl32.Vec3{float32(tip[0]), float32(tip[1]), float32(tip[2])},
	}, nil)
	if b, ok := r.Block(at, 0); ok {
		r.ScheduleTick(at, b, int32(tip[1]-at[1]+tickDelay), 0) //nolint:gosec // within reach
	}
}

// tipBelow follows a stalactite down from pos to its tip, within reach.
func (t *Ticker) tipBelow(r *world.Region, pos cube.Pos) (cube.Pos, bool) {
	for range reach {
		switch t.kind(r, pos) {
		case kindTip:
			return pos, true
		case kindHanging:
			pos = pos.Side(cube.FaceDown)
		default:
			return pos, false
		}
	}
	return pos, false
}

// fluidAbove is BDS's 0xD5531E0: from a hanging dripstone up to its base (within reach), the
// source above the base, lava or water.
func (t *Ticker) fluidAbove(r *world.Region, pos cube.Pos) (lava, ok bool) {
	for range reach {
		above := pos.Side(cube.FaceUp)
		switch t.kind(r, above) {
		case kindHanging, kindTip:
			pos = above
			continue
		case kindWater:
			return false, true
		case kindLava:
			return true, true
		}
		return false, false
	}
	return false, false
}

// cauldronBelow is BDS's 0xD553870: the first block under the tip that is a cauldron taking
// the fluid, through air only, within reach.
func (t *Ticker) cauldronBelow(r *world.Region, tip cube.Pos, lava bool) (cube.Pos, bool) {
	rg := r.Dimension().Range()
	pos := tip
	for range reach {
		pos = pos.Side(cube.FaceDown)
		if pos[1] < rg[0] {
			return pos, false
		}
		if t.kind(r, pos) == kindCauldron && t.cauldrons.DripAccepts(r, pos, lava) {
			return pos, true
		}
		if b, ok := r.Block(pos, 0); !ok || b != t.air {
			return pos, false
		}
	}
	return pos, false
}

// QueuedTick is the cauldron's queued tick, its dripstone part: a tip within reach above,
// through air, with a source above its base, fills the cauldron if it takes the fluid.
func (t *Ticker) QueuedTick(r *world.Region, pos cube.Pos, block uint32) {
	if int(block) >= len(t.kinds) || t.kinds[block] != kindCauldron {
		if t.BlockTicker != nil {
			t.BlockTicker.QueuedTick(r, pos, block)
		}
		return
	}
	tip, ok := t.tipAbove(r, pos)
	if !ok {
		return
	}
	lava, ok := t.fluidAbove(r, tip)
	if !ok || !t.cauldrons.DripAccepts(r, pos, lava) {
		return
	}
	t.cauldrons.Drip(r, pos, lava)
}

// tipAbove looks up from a cauldron through air for a stalactite's tip, within reach.
func (t *Ticker) tipAbove(r *world.Region, pos cube.Pos) (cube.Pos, bool) {
	rg := r.Dimension().Range()
	for range reach {
		pos = pos.Side(cube.FaceUp)
		if pos[1] > rg[1] {
			return pos, false
		}
		if t.kind(r, pos) == kindTip {
			return pos, true
		}
		if b, ok := r.Block(pos, 0); !ok || b != t.air {
			return pos, false
		}
	}
	return pos, false
}
