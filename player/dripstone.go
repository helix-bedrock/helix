package player

import (
	"maps"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Pointed dripstone hurting and breaking, as BDS 1.26.60 does it. When it is time to fall the
// drip package calls FallStalactite. A falling tip hurts the mobs in its box when it lands, by
// FallingBlockActor's causeFallDamage (0xBD5DD80): floor(ceil(fallen - 1) * the count the tip
// fell with), at most 40, cause Stalactite. A stalactite that lands breaks (PointedDripstone's
// onLand 0xD4FEDA0): its item pops out, LevelEvent 2021, the block is gone, LevelEvent 1064.
// Landing on a stalagmite's tip (its fall-on 0xD4FCE10) adds 2 to the fall and doubles its
// damage, cause Stalagmite (the player's causeFallDamage 0xBD008B0: ceil(scale * (fallen - 3 -
// jump boost))). A thrown trident faster than 0.6 breaks pointed dripstone it hits when
// projectilescanbreakblocks is on (its onProjectileHit 0xD4FEFD0).
const (
	nameDripstone = "minecraft:pointed_dripstone"

	causeStalactite DamageCause = 28
	causeStalagmite DamageCause = 29

	// fallingHurtMost is the most a falling block's landing hurts (FallingBlockActor's 40).
	fallingHurtMost = 40
	// stalagmiteExtraFall and stalagmiteScale are what a stalagmite's tip adds to a fall and
	// multiplies its damage by.
	stalagmiteExtraFall = 2
	stalagmiteScale     = 2
	// tridentBreakSpeed is how fast a trident must fly to break pointed dripstone.
	tridentBreakSpeed = 0.6
)

// FallStalactite turns a stalactite piece into a falling block that breaks where it lands. A
// tip passes how much each block it falls hurts the mobs it lands in; 0 hurts nothing. False
// if a BlockFallEvent listener stopped it. Runs: owner of the region.
func (s *Host) FallStalactite(r *world.Region, pos cube.Pos, block uint32, hurtPerBlock float32) bool {
	f, ok := s.startFallingBlock(r, pos, block)
	if !ok {
		return false
	}
	f.hurtPerBlock = hurtPerBlock
	f.hurtCause = causeStalactite
	f.shatters = true
	return true
}

// hurtLanding is causeFallDamage of a falling block that hurts: the players and mobs in its box.
func (f *FallingBlock) hurtLanding(r *world.Region, e *world.Entity) {
	if f.hurtPerBlock <= 0 {
		return
	}
	blocks := float32(math.Ceil(float64(f.fallen - 1)))
	if blocks <= 0 {
		return
	}
	damage := min(float32(math.Floor(float64(blocks*f.hurtPerBlock))), fallingHurtMost)
	if damage <= 0 {
		return
	}
	pos := e.Position()
	half := mgl32.Vec3{fallingHalf, fallingHalf, fallingHalf}
	lo, hi := pos.Sub(half), pos.Add(half)
	f.found = r.AppendEntitiesIn(f.found[:0], lo[0], lo[2], hi[0], hi[2])
	src := DamageSource{Cause: f.hurtCause}
	for _, o := range f.found {
		switch target := o.Owner().(type) {
		case *Player:
			if boxLo, boxHi, ok := target.BlastBox(); ok && !target.dead() && overlaps(lo, hi, boxLo, boxHi) {
				f.s.game.sys.Hurt(target, damage, src)
			}
		case *Mob:
			if boxLo, boxHi, ok := target.BlastBox(); ok && !target.Dead() && overlaps(lo, hi, boxLo, boxHi) {
				target.TakeDamage(damage, src)
			}
		}
	}
	clear(f.found)
}

// shatter is PointedDripstoneBlock's onLand: the stalactite just put down at pos breaks into
// its item.
func (f *FallingBlock) shatter(r *world.Region, pos cube.Pos) {
	s := f.s
	hash := int32(s.reg.Blocks.Hash(f.block)) //nolint:gosec // hashes are sent as int32
	if it, ok := s.pickedItem(f.block); ok {
		d := NewBlockDrop(pos, it)
		s.spawnItem(r.Dimension(), d.Stack, d.Pos, d.Velocity, world.Rotation{}, d.PickupDelay, 0)
	}
	levelEvent(r, pos, blockVec(pos), packet.LevelEventParticlesDestroyBlockNoSound, hash)
	if r.SetBlock(pos, 0, s.reg.Blocks.Air()) {
		r.UpdateNeighbours(pos)
	}
	levelEvent(r, pos, blockVec(pos), packet.LevelEventSoundPointedDripstoneLand, hash)
}

// isStalagmiteTip reports whether block is the tip of a standing pointed dripstone.
func (s *Host) isStalagmiteTip(block uint32) bool {
	if s.reg.Blocks.Name(block) != nameDripstone {
		return false
	}
	states := s.reg.Blocks.States(block)
	hanging, _ := states["hanging"].(uint8)
	thickness, _ := states["dripstone_thickness"].(string)
	return hanging == 0 && thickness == "tip"
}

// dripstoneState is the state a placed pointed dripstone takes: hanging when put against the
// bottom of a block, standing when put on top of one, and from the side the way the player
// looks (looking up hangs it); when that way has nothing to hold it, the other way. The
// thickness stays the item's (tip), as Helix does not work out neighbour states yet. The
// side rule is assumed, not read from BDS.
func dripstoneState(pl *Placement) (uint32, bool) {
	p := pl.Player
	r := p.entity.Region()
	if r == nil {
		return pl.Block, true
	}
	hang := cube.Face(pl.Face) == cube.FaceDown
	if f := cube.Face(pl.Face); f != cube.FaceDown && f != cube.FaceUp {
		hang = p.entity.Rotation().Pitch < 0
	}
	if !p.s.dripstoneHeld(r, pl.Pos, hang) && p.s.dripstoneHeld(r, pl.Pos, !hang) {
		hang = !hang
	}
	blocks := p.s.reg.Blocks
	states := maps.Clone(blocks.States(pl.Block))
	states["hanging"] = uint8(0)
	if hang {
		states["hanging"] = uint8(1)
	}
	if id, ok := blocks.StateID(nameDripstone, states); ok {
		return id, true
	}
	return pl.Block, true
}

// dripstoneHeld reports whether a dripstone at pos would be held, hanging or standing: by a
// dripstone of the same way, or a block holding it on that face.
func (s *Host) dripstoneHeld(r *world.Region, pos cube.Pos, hang bool) bool {
	at, face := pos.Side(cube.FaceDown), cube.FaceUp
	if hang {
		at, face = pos.Side(cube.FaceUp), cube.FaceDown
	}
	b, ok := r.Block(at, 0)
	if !ok {
		return false
	}
	if s.reg.Blocks.Name(b) == nameDripstone {
		h, _ := s.reg.Blocks.States(b)["hanging"].(uint8)
		return (h == 1) == hang
	}
	return s.attach != nil && s.attach.Supports(b, face)
}

// onStalagmiteTip reports whether feet in block pos stand on a stalagmite's tip: the tip is
// lower than a block, so it may be the block the feet are in or the one under it.
func (s *Host) onStalagmiteTip(r *world.Region, pos cube.Pos) bool {
	if b, ok := r.Block(pos, 0); ok && s.isStalagmiteTip(b) {
		return true
	}
	b, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && s.isStalagmiteTip(b)
}

// tridentBreaksDripstone is pointed dripstone's onProjectileHit: a thrown trident faster than
// 0.6 breaks it, with its drops, while projectilescanbreakblocks is on.
func (t *Projectile) tridentBreaksDripstone(r *world.Region, pos cube.Pos) {
	if t.trident == nil || !t.s.level.ProjectilesCanBreakBlocks || t.vel.Len() <= tridentBreakSpeed {
		return
	}
	b, ok := r.Block(pos, 0)
	if !ok || t.s.reg.Blocks.Name(b) != nameDripstone {
		return
	}
	t.s.FallBlock(r, pos, b)
}
