package player

import (
	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// The mace, from BDS 1.26.60's MaceItem: its attack bonus (vtable slot 39, sub_C8FC620),
// part of the base damage that Strength, Weakness and a critical hit then scale (the
// attack's damage, sub_BFDB910), and its hurtEnemy (slot 90, sub_C8FBB10): wear 1, and
// after a fall of more than 1.5 blocks the smash. Density and Breach are enchant.go's.
//
// Not done: Wind Burst, and the smash for mobs holding a mace. BDS skips pushing the
// attacker's own tamed pets; nothing is tamed yet.

// The mace's numbers.
const (
	nameMace = "minecraft:mace"
	// maceSmashFall is the fall a smash needs; maceHeavyFall the fall above which the
	// landing sound is the heavy one.
	maceSmashFall = 1.5
	maceHeavyFall = 5
	// The bonus: 4 a block up to 3, 2 a block up to 8, 1 a block beyond.
	maceFirstBlocks = 3
	maceFirstRate   = 4
	maceMidBlocks   = 8
	maceMidRate     = 2
	// maceReach is how far around the target the smash pushes; macePush scales the push
	// by how much nearer than maceReach the entity is; maceLift is the push upward.
	maceReach = 2.5
	macePush  = 0.6
	maceLift  = 0.6
	// nameArmorStand is never pushed by a smash.
	nameArmorStand = "minecraft:armor_stand"
)

// MaceBonus is the mace's extra damage for an attacker that has fallen fall blocks.
func MaceBonus(fall float32) float32 {
	switch {
	case fall <= maceSmashFall:
		return 0
	case fall <= maceFirstBlocks:
		return fall * maceFirstRate
	case fall <= maceMidBlocks:
		return maceFirstBlocks*maceFirstRate + (fall-maceFirstBlocks)*maceMidRate
	}
	return maceFirstBlocks*maceFirstRate + (maceMidBlocks-maceFirstBlocks)*maceMidRate + fall - maceMidBlocks
}

// maceSmash is the mace's hurtEnemy after p hit target: with the attacker not gliding and
// fallen more than 1.5 blocks, its fall stops (speed down cleared, fall distance gone, so the
// landing hurts from here), the smash sound plays where it is, dust rises at the target's
// feet when the target is on the ground or in a liquid, and every other player and mob
// within 2.5 blocks of the target's box is pushed away from it. Runs: the region.
func (p *Player) maceSmash(r *world.Region, target *world.Entity) {
	fall := p.FallDistance()
	if p.inventory.Held().Name != nameMace || fall <= maceSmashFall || p.Moving(MoveGliding) || target == nil {
		return
	}
	v := p.Velocity()
	p.move.fall = 0
	p.SetVelocity(mgl64.Vec3{v[0], 0, v[2]})
	p.showSmash(r, target, fall)
	p.smashPush(r, target)
}

// showSmash plays the smash sound where the attacker is, heavier for a longer fall onto a
// grounded target, and raises dust at the target's feet when it is on the ground or in a
// liquid.
func (p *Player) showSmash(r *world.Region, target *world.Entity, fall float32) {
	feet := target.Position()
	if t, ok := target.Owner().(*Player); ok {
		feet = t.AttachPos(AttachFeet)
	}
	grounded := target.OnGround()
	sound := packet.SoundEventMaceSmashAir
	if grounded {
		sound = packet.SoundEventMaceSmashGround
		if fall > maceHeavyFall {
			sound = packet.SoundEventMaceHeavySmashGround
		}
	}
	playSound(r, p.blockPos(), soundEvent{name: sound, pos: p.entity.Position(), extra: -1, entity: "minecraft:player"}, nil)
	if grounded || p.s.liquidAtFeet(r, feet) {
		levelEvent(r, feetBlock(feet), feet, packet.LevelEventParticleSmashAttackGroundDust, 0)
	}
}

// smashPush pushes every other player and mob within the mace's reach of the target's box
// away from the target, more the nearer it is.
func (p *Player) smashPush(r *world.Region, target *world.Entity) {
	b, ok := target.Owner().(Blastable)
	if !ok {
		return
	}
	lo, hi, ok := b.BlastBox()
	if !ok {
		return
	}
	reach := mgl64.Vec3{maceReach, maceReach, maceReach}
	lo, hi = lo.Sub(reach), hi.Add(reach)
	for _, o := range r.AppendEntitiesIn(nil, lo[0], lo[2], hi[0], hi[2]) {
		if o == target || o == p.entity {
			continue
		}
		m, ok := o.Owner().(Moving)
		if !ok || !smashPushes(o) {
			continue
		}
		pos := o.Position()
		if pos[0] < lo[0] || pos[0] > hi[0] || pos[1] < lo[1] || pos[1] > hi[1] || pos[2] < lo[2] || pos[2] > hi[2] {
			continue
		}
		d := pos.Sub(target.Position())
		dist := d.Len()
		push := (maceReach - dist) * macePush
		var dx, dz float64
		if dist >= minPushDist {
			dx, dz = d[0]/dist, d[2]/dist
		}
		m.SetVelocity(m.Velocity().Add(mgl64.Vec3{dx * push, maceLift, dz * push}))
	}
}

// smashPushes reports whether a smash pushes o: players not spectating and mobs other
// than armour stands.
func smashPushes(o *world.Entity) bool {
	switch e := o.Owner().(type) {
	case *Player:
		return e.mode() != GameModeSpectator
	case *Mob:
		return e.m.Type() != nameArmorStand
	}
	return false
}

// minPushDist is the distance under which a smash pushes only upward.
const minPushDist = 0.0001

// liquidAtFeet reports water or lava in the block at a position.
func (s *Host) liquidAtFeet(r *world.Region, at mgl64.Vec3) bool {
	liq, ok := s.liquidIn(r, feetBlock(at))
	if !ok {
		return false
	}
	m := s.reg.Blocks.Properties(liq).Material
	return m == registry.MaterialWater || m == registry.MaterialLava
}
