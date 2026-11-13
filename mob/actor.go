package mob

import (
	"slices"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/physics"
)

// The mob as the goals see it: behavior.Actor, Mob, Targeter, Attacker and Creeper. Parts the
// minimal hookup does not have yet (riding, leashes, homes, effects, items) answer as a mob
// without them.

func (m *Mob) ID() behavior.ID     { return m.id }
func (m *Mob) RuntimeID() uint64   { return m.rid }
func (m *Mob) Pos() behavior.Vec3  { return m.body.Pos }
func (m *Mob) Box() behavior.Box   { return m.body.Box }
func (m *Mob) Eyes() behavior.Vec3 { p := m.body.Pos; p.Y += m.eyeHeight(); return p }
func (m *Mob) Alive() bool         { return !m.Dead() }
func (m *Mob) Removed() bool       { return false }
func (m *Mob) Pickable() bool      { return !m.Dead() }
func (m *Mob) IsMob() bool         { return true }
func (m *Mob) IsPlayer() bool      { return false }
func (m *Mob) Flag(n int) bool     { return m.flags.Has(n) }
func (m *Mob) GameMode() int       { return -1 }

func (m *Mob) Riders() []behavior.ID              { return nil }
func (m *Mob) Vehicle() behavior.ID               { return behavior.NoID }
func (m *Mob) Owner() behavior.ID                 { return behavior.NoID }
func (m *Mob) Team() int                          { return 0 }
func (m *Mob) HeadRotation() (pitch, yaw float64) { return m.pitch, m.headYaw }
func (m *Mob) BodyYaw() float64                   { return m.bodyYaw }
func (m *Mob) Invulnerable() bool                 { return false }
func (m *Mob) HasHealth() bool                    { return true }
func (m *Mob) Targetable() bool                   { return !m.flags.Has(behavior.FlagCannotTarget) }
func (m *Mob) Monster() bool                      { return slices.Contains(m.families, "monster") }
func (m *Mob) ArmourCoverage() float64            { return 0 }
func (m *Mob) WearsHeadOf(string) bool            { return false }
func (m *Mob) Target() behavior.ID                { return m.target }
func (m *Mob) HijacksMount() bool                 { return false }
func (m *Mob) LastHurtByMob() behavior.ID         { return m.lastHurtBy }
func (m *Mob) LastHurtMob() (behavior.ID, int32)  { return m.lastHurtMob, m.lastHurtStamp }
func (m *Mob) BlocksSpace() bool                  { return false }
func (m *Mob) AttachPos(int) behavior.Vec3        { return m.Eyes() }

// Dimension is the ID of the mob's dimension.
func (m *Mob) Dimension() int {
	if m.v.r == nil {
		return 0
	}
	return int(m.rt.Env.DimensionID(m.v.r.Dimension()))
}

// Nav is the mob's navigation, nil without a navigation component.
func (m *Mob) Nav() behavior.Navigator {
	if m.nv == nil {
		return nil
	}
	return &m.navigate
}

// SetFlag sets an actor flag and tells those who see the mob when it changed.
func (m *Mob) SetFlag(n int, on bool) {
	if m.flags.Has(n) == on {
		return
	}
	m.flags.Set(n, on)
	m.flagsChanged()
}

// WalkTargetValue is PathfinderMob's: 0 everywhere in the minimal hookup (animals preferring
// grass and monsters darkness is not ported yet).
func (m *Mob) WalkTargetValue(behavior.Pos) float64 { return 0 }

// LookAt points the look control at p unless a goal already did this tick.
func (m *Mob) LookAt(p behavior.Vec3, yawMax, pitchMax float64) {
	if m.lookSet {
		return
	}
	m.lookSet = true
	m.look.Has, m.look.Pos, m.look.YawSpeed, m.look.PitchSpeed = true, p, yawMax, pitchMax
}

func (m *Mob) LookBusy() bool                    { return m.lookSet }
func (m *Mob) MaxHeadXRot() float64              { return 40 }
func (m *Mob) LastHurtBy() (behavior.ID, uint64) { return m.lastHurtBy, m.lastHurtTick }
func (m *Mob) ClearLastHurtBy()                  { m.lastHurtBy = behavior.NoID }
func (m *Mob) LastDamageCause() int              { return m.lastCause }

// PlaySound plays a sound at the mob's eyes.
func (m *Mob) PlaySound(sound int) {
	if sound != behavior.NoSound {
		m.do(op{kind: opSound, n: int32(sound), at: m.Eyes()}) //nolint:gosec // sound IDs fit
	}
}

// PlaySoundAt plays a sound at p.
func (m *Mob) PlaySoundAt(sound int, p behavior.Vec3) {
	if sound != behavior.NoSound {
		m.do(op{kind: opSound, n: int32(sound), at: p}) //nolint:gosec // sound IDs fit
	}
}

// Event fires a definition event on the mob.
func (m *Mob) Event(name string, other behavior.ID) {
	var o behavior.Actor
	if other != behavior.NoID && m.v.r != nil {
		o = m.rt.Env.Actor(m.v.r, other)
	}
	m.inst.Execute(name, m.tester(o))
}

func (m *Mob) Teleport(p behavior.Vec3)             { m.SetPos(p) }
func (m *Mob) Dwelling() (behavior.Pos, bool, bool) { return behavior.Pos{}, false, false }
func (m *Mob) Home() (behavior.Pos, int32, bool)    { return behavior.Pos{}, 0, false }
func (m *Mob) InLava() bool                         { return m.body.Travel() == physics.TravelLava }
func (m *Mob) InWater() bool                        { return m.body.WasInWater }
func (m *Mob) UnderWater() bool                     { return m.body.HeadInWater }
func (m *Mob) FloatJump()                           { m.jump.Jumping = true }
func (m *Mob) DismountPassengers()                  {}
func (m *Mob) HeardVibration() (uint64, bool)       { return 0, false }
func (m *Mob) SetPersistent()                       { m.persistent = true }
func (m *Mob) CanSit() bool                         { return false }
func (m *Mob) OnGround() bool                       { return m.body.OnGround }
func (m *Mob) OnFire() bool                         { return m.fireTicks > 0 }
func (m *Mob) Velocity() behavior.Vec3              { return m.body.Vel }
func (m *Mob) SetVelocity(v behavior.Vec3)          { m.body.Vel = v }
func (m *Mob) HasEffect(int) bool                   { return false }
func (m *Mob) SetHijackable(bool)                   {}
func (m *Mob) NoActionTime() int32                  { return m.noActionTime }
func (m *Mob) ResetFlock()                          {}
func (m *Mob) SetPersistSit(bool)                   {}
func (m *Mob) HelmetEmpty() bool                    { return true }
func (m *Mob) Speed() float64                       { return m.speed }
func (m *Mob) UnderwaterSpeed() float64             { return m.underwaterSpeed }
func (m *Mob) FollowRange() float64                 { return m.followRange }
func (m *Mob) PrevRotation() (pitch, yaw float64)   { return m.prevPitch, m.prevYaw }
func (m *Mob) SetYaw(yaw float64)                   { m.yaw = yaw }
func (m *Mob) Breathable() bool                     { return true }
func (m *Mob) AirSupply() int16                     { return 300 }
func (m *Mob) VerticalCollision() bool              { return m.body.VerticalCollision }
func (m *Mob) Persistent() bool                     { return m.persistent }

// OverWater is in water, or water is the first non-air block below.
func (m *Mob) OverWater() bool {
	if m.body.WasInWater {
		return true
	}
	if m.v.r == nil {
		return false
	}
	p := behavior.Pos{X: floor32(m.body.Pos.X), Y: floor32(m.body.Pos.Y) - 1, Z: floor32(m.body.Pos.Z)}
	w := goalWorld{&m.v}
	for ; p.Y > w.MinHeight(); p.Y-- {
		b := w.LiquidBlock(p)
		if b.Material != materialAir {
			return b.Material == materialWater
		}
	}
	return false
}

// Move adds to the mob's move request.
func (m *Mob) Move(v behavior.Vec3) {
	m.body.Vel.X += v.X
	m.body.Vel.Y += v.Y
	m.body.Vel.Z += v.Z
}

// MoveWanted reports the move control's wanted position.
func (m *Mob) MoveWanted() (wanted, ok bool) { return m.ctl.HasWanted, m.hasMove }

func (m *Mob) SetMoveWanted(p behavior.Vec3, speed float64) {
	if m.hasMove {
		m.ctl.SetWantedPosition(p, speed)
	}
}

func (m *Mob) ClearMoveWanted() {
	if m.hasMove {
		m.ctl.ClearWanted()
	}
}

// --- Targeter ---

// CanSee is a clear line from the mob's eyes to the other's eyes.
func (m *Mob) CanSee(a behavior.Actor) bool {
	if m.v.r == nil {
		return false
	}
	return goalWorld{&m.v}.ClearLine(m.Eyes(), a.Eyes())
}

// CanAttack is Actor::canAttack as far as the hookup goes: alive, not itself, and not a player
// in creative or spectator.
func (m *Mob) CanAttack(a behavior.Actor) bool {
	if a == nil || !a.Alive() || a.ID() == m.id {
		return false
	}
	if a.IsPlayer() {
		g := a.GameMode()
		if g == behavior.GameModeDefault {
			g = m.rt.Env.DefaultGameMode()
		}
		return g != behavior.GameModeCreative && g != behavior.GameModeSpectator && !a.Invulnerable()
	}
	return true
}

func (m *Mob) Admiring() bool          { return false }
func (m *Mob) PlayersTargetable() bool { return true }

// --- Attacker ---

// Swing swings the mob's arm.
func (m *Mob) Swing() { m.do(op{kind: opSwing}) }

// Attack hits a with the mob's attack, in Apply when the mob is thinking; then hears the
// result there.
func (m *Mob) Attack(a behavior.Actor, then behavior.AttackResult) {
	m.do(op{kind: opAttack, id: a.ID(), then: then})
}

// --- BlockEater (behavior.eat_block, a sheep growing its wool back) ---

// ActorEvent sends an actor event about the mob; the eat goal's head-down animation.
func (m *Mob) ActorEvent(id, _ int32) { m.do(op{kind: opActorEvent, n: id}) }

// EatGameEvent would post the eat game event; game events (vibrations) are not built.
func (m *Mob) EatGameEvent(behavior.Vec3) {}

// standProbe is how far under its box BDS looks for what a mob stands on.
const standProbe = 0.2

// StandingOn is Actor::getBlockPosCurrentlyStandingOn (BDS 1.26.60 0xB394740): the block
// whose collision meets a flat box 0.2 under the mob's box, else the block the mob is in.
// When several blocks meet it, BDS's choice was not read; the one nearest the mob's middle
// is taken.
func (m *Mob) StandingOn() behavior.Pos {
	b := m.body.Box
	y := b.Min.Y - standProbe
	probe := path.Box{Min: path.Vec3{X: b.Min.X, Y: y, Z: b.Min.Z}, Max: path.Vec3{X: b.Max.X, Y: y, Z: b.Max.Z}}
	m.standBoxes = physWorld{&m.v}.CollisionBoxes(m.standBoxes[:0], probe)
	cx, cz := (b.Min.X+b.Max.X)/2, (b.Min.Z+b.Max.Z)/2
	best, found := float64(0), false
	var at path.Vec3
	for _, bx := range m.standBoxes {
		dx, dz := (bx.Min.X+bx.Max.X)/2-cx, (bx.Min.Z+bx.Max.Z)/2-cz
		if d := dx*dx + dz*dz; !found || d < best {
			best, found, at = d, true, bx.Min
		}
	}
	if !found {
		at = m.body.Pos
	}
	return behavior.Pos{X: floor32(at.X), Y: floor32(at.Y), Z: floor32(at.Z)}
}

// --- Creeper ---

func (m *Mob) SwellDir() int32     { return m.swellDir }
func (m *Mob) SetSwellDir(d int32) { m.swellDir = d }

// The parts of a mob the goals ask for at runtime; a missing method would silently drop
// every goal that needs it.
var (
	_ behavior.Attacker = (*Mob)(nil)
	_ behavior.Creeper  = (*Mob)(nil)
	_ definition.Host   = (*Mob)(nil)
	_ definition.Tester = tester{}
)
