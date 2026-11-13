package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
)

// Pushing: BDS's Mob::pushActors (entity's state.PushActors, 1.26.60 0xBFEE7F0) after each
// mob's movement, and players pushing the mobs they walk into. It changes other mobs'
// velocity, so it runs in Apply, which runs one mob at a time in runtime-ID order. Only
// mobs are pushed so far: items, boats and minecarts keep their own push code, and a mob
// never pushes a player (the client moves its own player, as BDS's skips players too).

// pushActors pushes the mobs around this one, then lets each player that touches it push it.
func (m *Mob) pushActors() {
	if m.v.r == nil || m.Dead() || m.states == nil || m.stateEnv.Self == nil {
		return
	}
	w := pushWorld{m}
	m.pushNear = m.states.AfterMove(&m.stateEnv, w, m.pushNear)
	p := m.states.PushableByEntity
	if p == nil {
		return
	}
	reach := p.Config().Reach
	box := m.body.Box
	m.pushNear = m.rt.Env.Players(m.v.r, m.pushNear[:0])
	for _, a := range m.pushNear {
		// Pickable leaves out dead players and spectators.
		if !a.Pickable() || !touches(a.Box(), box, reach) {
			continue
		}
		p.PushedBy(&m.stateEnv, w, m, a)
	}
	m.pushNear = m.pushNear[:0]
}

// touches reports a, grown by reach on x and z as Mob::pushActors grows the pusher's box,
// overlapping b.
func touches(a, b behavior.Box, reach float64) bool {
	return a.Min.X-reach < b.Max.X && a.Max.X+reach > b.Min.X &&
		a.Min.Y < b.Max.Y && a.Max.Y > b.Min.Y &&
		a.Min.Z-reach < b.Max.Z && a.Max.Z+reach > b.Min.Z
}

// pushWorld is state.PushWorld for one mob's push. Riding is not hooked up, so nothing
// shares a vehicle or pulls anyone in.
type pushWorld struct{ m *Mob }

// mobOf is the mob behind an actor: itself, or the mob a snapshot was taken of.
func mobOf(a behavior.Actor) *Mob {
	switch v := a.(type) {
	case *Mob:
		return v
	case *Snapshot:
		return v.mob
	}
	return nil
}

func (pushWorld) PushableByEntity(a behavior.Actor) *state.PushableByEntity {
	if o := mobOf(a); o != nil && o.states != nil && !o.Dead() {
		return o.states.PushableByEntity
	}
	return nil
}

func (pushWorld) PushThrough(behavior.Actor) float64     { return 0 }
func (pushWorld) SameVehicle(_, _ behavior.Actor) bool   { return false }
func (pushWorld) PrevVehicle(behavior.Actor) behavior.ID { return behavior.NoID }
func (pushWorld) ClearPrevVehicle(behavior.Actor)        {}
func (pushWorld) Controlled(behavior.Actor) bool         { return false }
func (pushWorld) PullIn(_, _ behavior.Actor)             {}
func (pushWorld) IsMinecart(behavior.Actor) bool         { return false }

func (pushWorld) OnGround(a behavior.Actor) bool {
	if o := mobOf(a); o != nil {
		return o.body.OnGround
	}
	return false
}

func (pushWorld) Velocity(a behavior.Actor) behavior.Vec3 {
	if o := mobOf(a); o != nil {
		return o.body.Vel
	}
	return behavior.Vec3{}
}

func (pushWorld) SetVelocity(a behavior.Actor, v behavior.Vec3) {
	if o := mobOf(a); o != nil {
		o.body.Vel = v
	}
}

// Push adds the push to the mob's velocity; it moves by it on its next tick.
func (pushWorld) Push(a, _ behavior.Actor, v behavior.Vec3) {
	if o := mobOf(a); o != nil {
		o.body.Vel.X += v.X
		o.body.Vel.Y += v.Y
		o.body.Vel.Z += v.Z
	}
}

func (w pushWorld) PlaySound(a behavior.Actor, event int, p behavior.Vec3) {
	if o := mobOf(a); o != nil && o.hooks != nil {
		o.hooks.Sound(o, event, p)
	}
}

var _ state.PushWorld = pushWorld{}
