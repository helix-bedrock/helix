package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// The engine side of guardian_attack (mob.GuardianHooks), raid_garden (mob.GardenEnv),
// follow_caravan's caravan-updated event (mob.ActorEventDataHooks), fire_at_target's shots
// (mob.ProjectileHooks), the loot eat_mob, sneeze and drop_item_for drop (mob.LootHooks) and
// drop_item_for's time of day (mob.ClockEnv).

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
// blocks that answer it: a sweet berry bush is picked (the use's), cave
// vines with berries are picked (the use's), and a crop loses a growth
// stage and shows LevelEvent 2011 with its new block. The block-changed game
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

// ShootAlong spawns the projectile definition at from flying along dir at its own power, the
// mob as its owner (fire_at_target).
func (m *Mob) ShootAlong(_ *mob.Mob, def string, from, dir behavior.Vec3) {
	r := m.entity.Region()
	if r == nil || m.gone {
		return
	}
	spec, ok := m.s.projectileSpec(def, "")
	if !ok {
		return
	}
	d := mglVec(dir)
	if d.Len() < 1e-7 {
		return
	}
	if pr := m.s.SpawnProjectile(r.Dimension(), spec, mglVec(from), d.Normalize(), spec.Power, nil); pr != nil {
		pr.shooter, pr.separated = m, false
	}
}

// DropLoot rolls the loot table for the mob, killed naming the entity_killed type, and drops
// each item at at; a pickupDelay below 0 takes a death drop's. Nothing drops without
// doMobLoot.
func (m *Mob) DropLoot(_ *mob.Mob, table string, at behavior.Vec3, killed behavior.ID, pickupDelay int32) {
	r := m.entity.Region()
	if r == nil || m.gone || table == "" || m.s.entityLoot == nil || !m.s.mobLoot() {
		return
	}
	c := m.lootContext(r)
	if a := m.s.Actor(r, killed); a != nil {
		c.Killed = a.Type()
	}
	pos := mglVec(at)
	for _, d := range m.s.entityLoot.Roll(table, c, nil) {
		for _, it := range m.s.lootStacks(d) {
			if pickupDelay < 0 {
				m.s.spawnMobDrop(r, it, pos)
				continue
			}
			m.s.spawnItem(r.Dimension(), m.s.freshStack(it), pos, mgl32.Vec3{dropDrift(), dropUp, dropDrift()}, world.Rotation{}, pickupDelay, 0)
		}
	}
}

// TimeOfDay is BDS's code: the sun angle of the overworld clock.
func (s *Host) TimeOfDay() float32 {
	if s.clock == nil {
		return 0
	}
	return daytime.SunAngle(s.clock.Time(), s.clock.Cycle())
}
