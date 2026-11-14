package player

import (
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Cauldrons filling by themselves, as BDS 1.26.60: rain and snow (CauldronBlock's
// handlePrecipitation 0xCB4F560, called by the chunk tick's weather on the block under the
// rain's top) and stalactites dripping (the dripstone part of its queued tick 0xCB4A1D0, its
// accept check 0xCB502D0 and fill 0xCB505D0; the search is the drip package's).
const (
	// cauldronSnowBelow is the temperature under which precipitation fills with powder snow.
	cauldronSnowBelow = 0.15
	// cauldronDripWater is how many levels a water drip adds; one is never taken past full.
	cauldronDripWater = 2
	// cauldronDripWaterMost is the level up to which a water drip adds all of cauldronDripWater.
	cauldronDripWaterMost = 4
)

// CauldronPrecipitation is BDS's handlePrecipitation: by chance against the downfall, rain
// adds a level of water and snow (below 0.15) a level of powder snow, to an empty cauldron or
// one of the same liquid with no potion and no dye that is not full. Runs: owner of the
// region.
func (s *Host) CauldronPrecipitation(r *world.Region, pos cube.Pos, downfall, temperature float32) {
	if rand.Float32()*0.5 > downfall { //nolint:gosec // gameplay chance, not a secret
		return
	}
	b, ok := r.Block(pos, 0)
	if !ok || s.reg.Blocks.Name(b) != nameCauldron {
		return
	}
	c, ok := cauldronAt(r, pos)
	if !ok {
		return
	}
	snow := temperature < cauldronSnowBelow
	level, liquid := s.cauldronState(b)
	if level >= cauldronFull {
		return
	}
	if level > 0 && (c.potion >= 0 || liquid == cauldronLava || c.dyed || (liquid == cauldronPowderSnow) != snow) {
		return
	}
	liquid = cauldronWater
	if snow {
		liquid = cauldronPowderSnow
	}
	s.setCauldronLevel(r, pos, c, level+1, liquid)
	s.postBlockGameEvent(r, gameEventBlockChange, pos, nil, 0, false)
}

// DripAccepts is BDS's 0xCB502D0: a cauldron with its block entity, no potion, and empty or
// holding water for a water drip.
func (s *Host) DripAccepts(r *world.Region, pos cube.Pos, lava bool) bool {
	c, ok := cauldronAt(r, pos)
	if !ok {
		return false
	}
	b, _ := r.Block(pos, 0)
	level, liquid := s.cauldronState(b)
	if level == 0 {
		return true
	}
	return c.potion < 0 && !lava && liquid == cauldronWater
}

// Drip fills a cauldron from a stalactite (BDS's 0xCB505D0 from the queued tick): lava fills
// it, water adds two levels up to full; the drip sound at the block, the level event and the
// block_change game event. A full water cauldron is left as it is, with no event.
func (s *Host) Drip(r *world.Region, pos cube.Pos, lava bool) {
	c, ok := cauldronAt(r, pos)
	if !ok {
		return
	}
	b, _ := r.Block(pos, 0)
	level, _ := s.cauldronState(b)
	sound, event, liquid := packet.SoundEventPointedDripstoneCauldronDripLava, int32(packet.LevelEventCauldronFillLava), cauldronLava
	to := int32(cauldronFull)
	if !lava {
		if level >= cauldronFull {
			return
		}
		sound, event, liquid = packet.SoundEventPointedDripstoneCauldronDripWater, packet.LevelEventCauldronFillWater, cauldronWater
		to = min(level, cauldronDripWaterMost) + cauldronDripWater
	}
	playSound(r, pos, soundEvent{name: sound, pos: mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])}, extra: -1}, nil)
	s.setCauldronLevel(r, pos, c, to, liquid)
	s.cauldronEvent(r, pos, c, event)
	s.postBlockGameEvent(r, gameEventBlockChange, pos, nil, 0, false)
}
