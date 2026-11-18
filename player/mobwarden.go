package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// The warden's sonic boom on the engine side (mob.WardenHooks, mob.WardenEnv): the hit,
// BDS's code and the particle level events.

// DamageSonicBoom is BDS's ActorDamageCause SonicBoom.
const DamageSonicBoom DamageCause = 31

// knockbackLiftScale matches BDS's scale on the height.
const knockbackLiftScale = 1.2

// LevelEventXYZ broadcasts LevelEventGeneric id with x, y and z float tags at p, as BDS's
// broadcastLevelEvent with a position compound does.
func (s *Host) LevelEventXYZ(r *world.Region, id int32, p behavior.Vec3) {
	if r == nil {
		return
	}
	if pk := levelEventGeneric(id, map[string]any{"x": p.X, "y": p.Y, "z": p.Z}); pk != nil {
		r.Broadcast(vecPos(mglVec(p)), pk, nil)
	}
}

// SonicHit hurts the entity with the sonic boom cause, the warden as attacker for a mob.
func (m *Mob) SonicHit(_ *mob.Mob, target behavior.ID, damage float32) {
	r := m.entity.Region()
	if r == nil {
		return
	}
	switch e := m.s.entityByID(r, target).(type) {
	case *Mob:
		if e != m && e.entity != nil && !e.Dead() {
			e.hurtBy(damage, behavior.ID(m.entity.UniqueID()), DamageSonicBoom)
		}
	case *Player:
		if e.entity != nil && !e.dead() {
			e.s.game.sys.TakeHit(NewHit(e, damage, DamageSource{Cause: DamageSonicBoom}))
		}
	}
}

// Knockback matches BDS with the parameters the goal fills: the
// velocity kept by Extra, pushed along the reversed direction by Power and up by Height
// times 1.2 (capped at HeightCap), both scaled by the target's knockback resistance.
func (m *Mob) Knockback(_ *mob.Mob, target behavior.ID, k behavior.Knockback) {
	r := m.entity.Region()
	if r == nil {
		return
	}
	switch e := m.s.entityByID(r, target).(type) {
	case *Mob:
		if e.entity == nil || e.Dead() {
			return
		}
		resist := max(1-e.m.KnockbackResistance(), 0)
		v := knockbackParams(mglVec(e.m.Body().Vel), k, resist)
		e.m.Knock(vec(v))
		e.entity.Send(&packet.SetActorMotion{EntityRuntimeID: e.entity.RuntimeID(), Velocity: v, Tick: e.entity.Tick()})
	case *Player:
		if e.entity == nil || e.dead() {
			return
		}
		resist := max(1-e.attributes.get(attrKnockbackResistance), 0)
		e.move.velocity = knockbackParams(e.move.velocity, k, resist)
		e.send(&packet.SetActorMotion{EntityRuntimeID: e.runtimeID, Velocity: e.move.velocity, Tick: e.entity.Tick()})
		e.flush()
	}
}

// knockbackParams matches BDS's velocity for the goal's parameters.
func knockbackParams(vel mgl32.Vec3, k behavior.Knockback, resist float32) mgl32.Vec3 {
	retain := min(max(k.Extra, 0), 1)
	return knockbackOf(vel, -k.DirX, -k.DirZ, k.Power*resist, k.Height*resist, retain, knockbackLiftScale, k.HeightCap)
}
