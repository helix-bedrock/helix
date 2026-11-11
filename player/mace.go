package player

import (
	"github.com/go-gl/mathgl/mgl32"
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
	p.SetVelocity(mgl32.Vec3{v[0], 0, v[2]})
	at := p.entity.Position()
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
	playSound(r, p.blockPos(), soundEvent{name: sound, pos: at, extra: -1, entity: "minecraft:player"}, nil)
	if grounded || p.s.liquidAtFeet(r, feet) {
		levelEvent(r, feetBlock(feet), feet, packet.LevelEventParticleSmashAttackGroundDust, 0)
	}
	b, ok := target.Owner().(Blastable)
	if !ok {
		return
	}
	lo, hi, ok := b.BlastBox()
	if !ok {
		return
	}
	lo, hi = lo.Sub(mgl32.Vec3{maceReach, maceReach, maceReach}), hi.Add(mgl32.Vec3{maceReach, maceReach, maceReach})
	for _, o := range r.AppendEntitiesIn(nil, lo[0], lo[2], hi[0], hi[2]) {
		if o == target || o == p.entity {
			continue
		}
		m, ok := o.Owner().(Moving)
		if !ok {
			continue
		}
		switch e := o.Owner().(type) {
		case *Player:
			if e.mode() == GameModeSpectator {
				continue
			}
		case *Mob:
			if e.m.Type() == nameArmorStand {
				continue
			}
		default:
			continue
		}
		pos := o.Position()
		if pos[0] < lo[0] || pos[0] > hi[0] || pos[1] < lo[1] || pos[1] > hi[1] || pos[2] < lo[2] || pos[2] > hi[2] {
			continue
		}
		d := pos.Sub(target.Position())
		dist := d.Len()
		push := (maceReach - dist) * macePush
		var dx, dz float32
		if dist >= minPushDist {
			dx, dz = d[0]/dist, d[2]/dist
		}
		m.SetVelocity(m.Velocity().Add(mgl32.Vec3{dx * push, maceLift, dz * push}))
	}
}

// minPushDist is the distance under which a smash pushes only upward.
const minPushDist = 0.0001

// liquidAtFeet reports water or lava in the block at a position.
func (s *Host) liquidAtFeet(r *world.Region, at mgl32.Vec3) bool {
	liq, ok := s.liquidIn(r, feetBlock(at))
	if !ok {
		return false
	}
	m := s.reg.Blocks.Properties(liq).Material
	return m == registry.MaterialWater || m == registry.MaterialLava
}
