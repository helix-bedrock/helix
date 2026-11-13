package player

import (
	"math/rand/v2"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// What a lightning strike does to the blocks it lands on, from BDS 1.26.60's LightningBolt
// tick (0xBD52630) on its first tick: the block at the bolt is hit (onLightningHit, vtable slot
// 16), then copper is cleaned: the block a struck rod stands on, else the block under the bolt.
// If that is copper that is not waxed, 3 to 5 times it is hit and a walk of 1 to 8 steps cleans
// a copper neighbour (one of the 27 around, up to 10 tries a step) and moves there. A copper
// block hit goes back one oxidation stage (CopperLightningRodBlock 0xD559BF0); a rod hit turns
// it on (circuit.StrikeRod) with an electric spark (LevelEvent 2033).
//
// Channeling tridents on rods are LightningRodBlock::onProjectileHit (slot 15, 0xD558820): the
// top rod of a stack takes it, and under rain with open sky it strikes there.
const (
	// rodHitsBase and rodHitsSpread: the target is hit rodHitsBase + rand(rodHitsSpread) times.
	rodHitsBase   = 3
	rodHitsSpread = 3
	// rodWalkSpread is the most steps a walk takes; rodWalkTries the tries for each step.
	rodWalkSpread = 8
	rodWalkTries  = 10
	// rodSuffix ends every lightning rod's name.
	rodSuffix = "lightning_rod"
)

// isRodBlock reports whether name is a lightning rod of any oxidation, waxed or not.
func isRodBlock(name string) bool { return strings.HasSuffix(name, rodSuffix) }

// boltHitBlocks is the bolt's first tick on the blocks at pos.
func (s *Host) boltHitBlocks(r *world.Region, pos cube.Pos) {
	s.lightningHitBlock(r, pos)
	target := pos.Side(cube.FaceDown)
	if b, ok := r.Block(pos, 0); ok && isRodBlock(s.reg.Blocks.Name(b)) {
		target = circuit.RodSupport(s.reg.Blocks.States(b), pos)
	}
	if !s.isCleanableCopper(r, target) {
		return
	}
	for range rodHitsBase + rand.IntN(rodHitsSpread) { //nolint:gosec // weather, not a secret
		s.lightningHitBlock(r, target)
		s.cleanCopperWalk(r, target)
	}
}

// cleanCopperWalk is one walk from start: each step finds a copper neighbour that is not
// waxed, hits it and moves there; a step that finds none in its tries ends the walk.
func (s *Host) cleanCopperWalk(r *world.Region, start cube.Pos) {
	at := start
	for range rand.IntN(rodWalkSpread) + 1 { //nolint:gosec // weather, not a secret
		next, ok := s.copperNeighbour(r, at)
		if !ok {
			return
		}
		s.lightningHitBlock(r, next)
		at = next
	}
}

// copperNeighbour tries up to rodWalkTries random blocks in the 3 by 3 by 3 around at for
// copper that is not waxed.
func (s *Host) copperNeighbour(r *world.Region, at cube.Pos) (cube.Pos, bool) {
	for range rodWalkTries {
		p := cube.Pos{at[0] - 1 + rand.IntN(3), at[1] - 1 + rand.IntN(3), at[2] - 1 + rand.IntN(3)} //nolint:gosec // weather, not a secret
		if s.isCleanableCopper(r, p) {
			return p, true
		}
	}
	return cube.Pos{}, false
}

// isCleanableCopper reports whether the block at pos is copper that is not waxed.
func (s *Host) isCleanableCopper(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	_, ok = s.copperStage(s.reg.Blocks.Name(b))
	return ok
}

// lightningHitBlock is BDS's onLightningHit on the block at pos: copper goes back a stage, a
// rod turns on.
func (s *Host) lightningHitBlock(r *world.Region, pos cube.Pos) {
	s.deoxidize(r, pos)
	if fn := s.circuits.StrikeRod; fn != nil && fn(r, pos) {
		levelEvent(r, pos, blockVec(pos), packet.LevelEventParticlesElectricSpark, 0)
	}
}

// tridentOnRod is a Channeling trident landing on the rod at pos: the top rod of the stack
// takes it, and if it rains just above that rod and nothing is over it, lightning strikes it.
// False if the block is no rod.
func (t *Projectile) tridentOnRod(r *world.Region, pos cube.Pos) bool {
	if t.trident == nil || t.trident.channeling <= 0 {
		return false
	}
	b, ok := r.Block(pos, 0)
	if !ok || !isRodBlock(t.s.reg.Blocks.Name(b)) {
		return false
	}
	for {
		above, ok := r.Block(pos.Side(cube.FaceUp), 0)
		if !ok || !isRodBlock(t.s.reg.Blocks.Name(above)) {
			break
		}
		pos = pos.Side(cube.FaceUp)
	}
	if t.s.chunkWeather == nil || !t.s.chunkWeather.RainingAt(r, pos.Side(cube.FaceUp)) || !t.s.skyOpen(r, blockVec(pos)) {
		return true
	}
	t.s.Strike(r, mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]), float32(pos[2]) + 0.5})
	playSound(r, pos, soundEvent{name: packet.SoundEventTridentThunder, pos: blockVec(pos), extra: -1}, nil)
	return true
}
