package fire

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Campfires going out and lighting, from BDS 1.26.60's CampfireBlock (tryLightFire,
// tryDouseFire, onPlace and neighborChanged; vtable 0xFB9CF40). The block entity, its food
// and the damage it deals are the player package's.

// CampfireWetFunc is a campfire at pos that has water in its own block: BDS's neighborChanged
// throws out the food on it. Runs: owner of the region.
type CampfireWetFunc func(r *world.Region, pos cube.Pos)

// SetCampfireWet sets what happens to the food on a campfire that water reached. Call it
// before any player joins.
func (t *Ticker) SetCampfireWet(fn CampfireWetFunc) { t.campfireWet = fn }

// IsCampfire reports whether block is a campfire or soul campfire.
func (t *Ticker) IsCampfire(block uint32) bool { return t.is(block, isCampfire) }

// CampfireLit reports whether block is a campfire or soul campfire that burns.
func (t *Ticker) CampfireLit(block uint32) bool {
	if !t.is(block, isCampfire) {
		return false
	}
	v, _ := t.blocks.States(block)[extinguishedStat].(uint8)
	return v == 0
}

// LightCampfire is BDS's tryLightFire: an unlit campfire with no water in its block lights,
// with the ignite sound. It reports whether it lit. Runs: owner of the region.
func (t *Ticker) LightCampfire(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	if !ok || !t.is(b, isCampfire) || t.CampfireLit(b) || t.WaterIn(r, pos) {
		return false
	}
	return t.setCampfire(r, pos, b, false, packet.SoundEventIgnite)
}

// DouseCampfire is BDS's tryDouseFire: a lit campfire goes out, with the extinguish sound.
// With onlyWet it does only when there is water in its block or the block above, as when
// water reaches it. It reports whether it went out. Runs: owner of the region.
func (t *Ticker) DouseCampfire(r *world.Region, pos cube.Pos, onlyWet bool) bool {
	b, ok := r.Block(pos, 0)
	if !ok || !t.CampfireLit(b) {
		return false
	}
	if onlyWet && !t.WaterIn(r, pos) && !t.WaterIn(r, pos.Side(cube.FaceUp)) {
		return false
	}
	return t.setCampfire(r, pos, b, true, packet.SoundEventExtinguishFire)
}

// setCampfire sets the campfire's extinguished state, playing sound at the block's corner as
// BDS does.
func (t *Ticker) setCampfire(r *world.Region, pos cube.Pos, b uint32, out bool, sound string) bool {
	states := t.blocks.States(b)
	next := make(map[string]any, len(states))
	for k, v := range states {
		next[k] = v
	}
	next[extinguishedStat] = uint8(0)
	if out {
		next[extinguishedStat] = uint8(1)
	}
	id, ok := t.blocks.StateID(t.blocks.Name(b), next)
	if !ok {
		return false
	}
	r.Broadcast(pos, &packet.LevelSoundEvent{
		SoundType: sound, Position: mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])}, ExtraData: -1,
	}, nil)
	t.set(r, pos, id)
	return true
}

// WaterIn reports water in the block at pos: its liquid layer, or the block itself. Runs:
// owner of the region.
func (t *Ticker) WaterIn(r *world.Region, pos cube.Pos) bool {
	return t.blocks.Properties(t.liquidAt(r, pos)).Material == materialWater
}

// campfireChanged is a campfire's onPlace and neighborChanged: water puts it out, and water in
// its own block throws out its food. Water flowing into its liquid layer counts as a change
// next to it.
func (t *Ticker) campfireChanged(r *world.Region, pos cube.Pos) {
	t.DouseCampfire(r, pos, true)
	if t.campfireWet != nil {
		if b, ok := r.Block(pos, 1); ok && t.blocks.Properties(b).Material == materialWater {
			t.campfireWet(r, pos)
		}
	}
}

// campfireAt reports whether the block at pos, layer 0, is a campfire.
func (t *Ticker) campfireAt(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && t.is(b, isCampfire)
}
