package player

import (
	"math"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// The engine side of guardian_attack (mob.GuardianHooks), raid_garden (mob.GardenEnv) and
// follow_caravan's caravan-updated event (mob.ActorEventDataHooks).

// ActorEventData sends the actor event with its data about the mob.
func (m *Mob) ActorEventData(_ *mob.Mob, id, data int32) {
	if m.entity == nil || m.entity.Region() == nil || id < 0 || id > math.MaxUint8 {
		return
	}
	m.entity.Send(actorEventData(m.entity.RuntimeID(), uint8(id), data))
}

// HurtBy hurts the entity with the cause from the mob, the hurt time skipped when
// ignoreCooldown is set (guardian_attack's magic hit).
func (m *Mob) HurtBy(_ *mob.Mob, target behavior.ID, cause int, damage float32, ignoreCooldown bool) {
	r := m.entity.Region()
	if r == nil {
		return
	}
	switch e := m.s.entityByID(r, target).(type) {
	case *Mob:
		if e != m && e.entity != nil && !e.Dead() {
			e.hurtWith(damage, behavior.ID(m.entity.UniqueID()), DamageCause(cause), ignoreCooldown)
		}
	case *Player:
		if e.entity != nil && !e.dead() {
			e.s.game.sys.TakeHit(NewHit(e, damage, DamageSource{Cause: DamageCause(cause), IgnoreHurtTime: ignoreCooldown}))
		}
	}
}

// RaidBlock is minecraft:on_raided on the block at p (raid_garden). BDS 1.26.60's vanilla
// blocks that answer it: a sweet berry bush is picked (0xD4DF540, the use's 0xD4E0390), cave
// vines with berries are picked (0xCB50F60, the use's 0xCB52C10), and a crop loses a growth
// stage and shows LevelEvent 2011 with its new block (0xCB3BBF0). The block-changed game
// events BDS posts are not posted: Helix's game events come from players only.
func (s *Host) RaidBlock(r *world.Region, p behavior.Pos, _ behavior.ID) {
	f := s.farm
	if f == nil || r == nil {
		return
	}
	pos := cube.Pos{int(p.X), int(p.Y), int(p.Z)}
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	if bush, _ := f.BerryBush(b); bush {
		f.PickBerries(r, pos, func(block uint32) { s.dropPicked(r, pos, block, packet.SoundEventSweetBerryBushPick) })
		return
	}
	if f.PickGlowBerries(r, pos, func(block uint32) { s.dropPicked(r, pos, block, packet.SoundEventCaveVinesPickBerries) }) {
		return
	}
	if now, ok := f.RaidCrop(r, pos); ok {
		levelEvent(r, pos, blockCenter(pos), packet.LevelEventParticlesCropEaten, int32(s.reg.Blocks.Hash(now))) //nolint:gosec // hashes are sent as int32
	}
}
