// Package mob runs mobs from their entity definitions with the entity library: the
// definition's components become the mob's goals, navigation, move control and physics,
// ticked in BDS's order. Nothing here knows any mob by name: a zombie and a cow differ only
// in their definition, as in BDS.
//
// Sensors and event components run (sensors.go), and the mob state components (state.go);
// loot and saving are not hooked up yet. The player package's Mob actor owns the entity, packets and damage.
package mob

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/component"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/goal"
	"github.com/helix-bedrock/entity/move"
	"github.com/helix-bedrock/entity/nav"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/physics"
	"github.com/helix-bedrock/entity/sensor"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/world"
)

// BDS's actor flag numbers the runtime sets itself.
const (
	FlagNoAI       = 16
	FlagCanClimb   = 19
	FlagCanWalk    = 22
	FlagBreathing  = 35
	FlagHasGravity = 49
	FlagCollision  = 48
)

// BDS's defaults for a mob without the components that set them.
const (
	defaultMaxAutoStep = 0.5625
	defaultAirSpeed    = 0.02
	defaultFollowRange = 16
	defaultHealth      = 10
	// hurtTicks is how long a mob only takes damage above its last hurt.
	hurtTicks = 10
	// deathTicks is how long a dead mob stays before it is removed.
	deathTicks = 20
	// lookYaw is the body control's turn limit when the head looks away.
	bodyMaxTurn = 75
)

// Runtime is what every mob of a server shares: the block tables, the engine's answers and
// the goal registry. Runs: read from any region once built.
type Runtime struct {
	Blocks *Blocks
	Env    Env
	// Goals maps goal components to factories; vanilla's by default.
	Goals *component.Registry
	Log   *slog.Logger
	// parser reads definitions; parsed definitions are cached by type.
	parser *definition.Parser
	defs   map[string]*definition.Definition
	// sounds are the level sound events' numbers by name (soundevents_gen.go).
	sounds map[string]int
}

// NewRuntime returns a runtime with vanilla's goals.
func NewRuntime(b *Blocks, env Env, log *slog.Logger) *Runtime {
	rt := &Runtime{Blocks: b, Env: env, Goals: component.Vanilla(), Log: log, parser: definition.NewParser(), defs: map[string]*definition.Definition{}, sounds: map[string]int{}}
	for n, name := range soundEvents {
		if name != "" {
			rt.sounds[name] = n
		}
	}
	return rt
}

// SoundName is the name of a level sound event number, "" for none.
func SoundName(n int) string {
	if n < 0 || n >= len(soundEvents) || n == behavior.NoSound {
		return ""
	}
	return soundEvents[n]
}

// Parse reads and caches the definition of an entity type. Call it at boot for every type,
// so regions only read the cache.
func (rt *Runtime) Parse(typ string, raw json.RawMessage) error {
	def, warnings, err := rt.parser.Parse(raw)
	if err != nil {
		return fmt.Errorf("mob: parse %s: %w", typ, err)
	}
	for _, w := range warnings {
		rt.Log.Debug("entity definition", "type", typ, "warning", w)
	}
	rt.defs[typ] = def
	return nil
}

// Has reports a parsed definition for typ.
func (rt *Runtime) Has(typ string) bool { _, ok := rt.defs[typ]; return ok }

// Hooks are what the engine does for one mob: its packets and its effects on others. They
// run on the region's goroutine, never while mobs think.
type Hooks interface {
	// Attack is a melee hit on the entity with the ID; it reports a hit and whether the
	// entity died. It runs in the mob's Apply.
	Attack(m *Mob, target behavior.ID) (hit, killed bool)
	// Swing swings the mob's arm for those who see it.
	Swing(m *Mob)
	// Sound plays a level sound event at p.
	Sound(m *Mob, sound int, p behavior.Vec3)
	// FlagsChanged tells those who see the mob that its actor flags or air changed.
	FlagsChanged(m *Mob)
	// ActorEvent sends an actor event about the mob.
	ActorEvent(m *Mob, id int)
	// Hurt hurts the mob through the engine's hurt (its packets included) with no attacker.
	Hurt(m *Mob, amount float32, cause int)
}

// Mob is one mob: its definition state, goals, navigation and body. It is used only from its
// entity's tick: Tick in the think phase, the rest on the region's goroutine.
type Mob struct {
	rt    *Runtime
	hooks Hooks
	typ   string
	id    behavior.ID
	rid   uint64

	inst     *definition.Instance
	families []string
	sel      goal.Selector
	nv       *nav.Navigation
	navigate navigator
	finder   *path.Finder
	movement component.Movement
	hasMove  bool
	ctl      move.Control
	look     move.Look
	jump     move.Jump
	lookSet  bool
	body     physics.Body
	flags    path.ActorFlags

	pitch, yaw, headYaw, bodyYaw float32
	prevPitch, prevYaw           float32

	health, maxHealth                   float32
	speed, underwaterSpeed, followRange float32
	attackDamage                        float32

	target, lastHurtBy, lastHurtMob behavior.ID
	lastHurtTick                    uint64
	lastHurtStamp                   int32
	lastHurtAmount                  float32
	hurtTime                        int32
	lastCause                       int
	deathTime                       int32
	persistent                      bool
	// states are the definition's state components (state.go), stateEnv what they run with,
	// stateComps the components they were read from. despawned is set when the mob despawned.
	states     *state.Set
	stateEnv   state.Env
	stateComps definition.Group
	despawned  bool
	// fireTicks is how long the mob still burns, burnTicks how long it has burned; air and
	// maxAir its air supply.
	fireTicks, burnTicks int32
	air, maxAir          int16
	// sensors are the definition's sensors and event components (sensors.go), sensorEnv what
	// they run with, sensorComps the components they were read from.
	sensors      *sensor.Set
	sensorEnv    sensor.Env
	sensorComps  definition.Group
	swellDir     int32
	noActionTime int32
	// scale is minecraft:scale's value, the size the client draws the mob at (a rabbit 0.6,
	// babies their group's own).
	scale float32
	// moveSpeed is the speed the move control last chose (BDS's Mob::mSpeed), kept between
	// ticks so walking speeds up a tenth at a time.
	moveSpeed float32

	// rnd is the mob's own random, seeded from its ID, so what it draws does not depend on
	// which mobs think on which core.
	rnd *Random
	// thinking is set during Tick: ops queue instead of running, flag changes wait for Apply.
	thinking   bool
	flagsDirty bool
	ops        []op
	snap       Snapshot

	v view
}

// New makes a mob of the type at pos. method is how it came to be (spawned, born, loaded),
// which picks the event its definition runs first.
func (rt *Runtime) New(typ string, uniqueID int64, runtimeID uint64, pos behavior.Vec3, yaw float32, hooks Hooks, method definition.InitMethod) (*Mob, error) {
	return rt.NewWithEvent(typ, uniqueID, runtimeID, pos, yaw, hooks, method, "")
}

// NewWithEvent is New with the event a mob starts with when method is
// definition.InitEvent, as an identifier such as minecraft:pillager<event> asks.
func (rt *Runtime) NewWithEvent(typ string, uniqueID int64, runtimeID uint64, pos behavior.Vec3, yaw float32, hooks Hooks, method definition.InitMethod, initEvent string) (*Mob, error) {
	def, ok := rt.defs[typ]
	if !ok {
		return nil, fmt.Errorf("mob: no definition for %s", typ)
	}
	m := &Mob{
		rt: rt, hooks: hooks, typ: typ, id: behavior.ID(uniqueID), rid: runtimeID,
		target: behavior.NoID, lastHurtBy: behavior.NoID, lastHurtMob: behavior.NoID, lastCause: -1,
		yaw: yaw, headYaw: yaw, bodyYaw: yaw, prevYaw: yaw,
		finder: path.NewFinder(),
		health: defaultHealth, maxHealth: defaultHealth, followRange: defaultFollowRange,
	}
	m.rnd = NewRandom(uint64(uniqueID)) //nolint:gosec // the bits of the ID seed the random
	m.v = view{blocks: rt.Blocks, env: rt.Env, rnd: m.rnd, m: m}
	m.navigate.m = m
	m.body = physics.Body{Pos: pos, PrevPos: pos, Yaw: yaw, SwimSpeed: 1, MaxAutoStep: defaultMaxAutoStep, AirSpeed: defaultAirSpeed, HasPhysics: true, JumpBoost: -1, Levitation: -1}
	m.flags.Set(FlagHasGravity, true)
	m.flags.Set(FlagCollision, true)
	m.flags.Set(FlagBreathing, true)
	m.flags.Set(FlagCanClimb, true)
	m.inst = definition.New(def, m, method, initEvent)
	m.setSize(0.6, 1.8)
	m.snapshot()
	return m, nil
}

// Type is the mob's entity type.
func (m *Mob) Type() string { return m.typ }

// Families is the mob's type_family list.
func (m *Mob) Families() []string { return m.families }

// Body is the mob's movement state.
func (m *Mob) Body() *physics.Body { return &m.body }

// Flags are the mob's actor flags.
func (m *Mob) Flags() *path.ActorFlags { return &m.flags }

// Rotation is the mob's pitch, yaw, head yaw and body yaw.
func (m *Mob) Rotation() (pitch, yaw, headYaw, bodyYaw float32) {
	return m.pitch, m.yaw, m.headYaw, m.bodyYaw
}

// Scale is the size the client draws the mob at, from minecraft:scale (1 without it).
func (m *Mob) Scale() float32 {
	if m.scale <= 0 {
		return 1
	}
	return m.scale
}

// Size is the mob's collision width and height.
func (m *Mob) Size() (w, h float32) { return m.body.Width, m.body.Height }

// MaxHealth is the health attribute's maximum.
func (m *Mob) MaxHealth() float32 { return m.maxHealth }

// HealthValue is the mob's health.
func (m *Mob) HealthValue() float32 { return m.health }

// AttackDamage is the attack component's damage.
func (m *Mob) AttackDamage() float32 { return m.attackDamage }

// MovementSpeed is the movement attribute.
func (m *Mob) MovementSpeed() float32 { return m.speed }

// Dead reports a mob with no health left.
func (m *Mob) Dead() bool { return m.health <= 0 }

func (m *Mob) setSize(w, h float32) {
	m.body.Width, m.body.Height = w, h
	m.body.Box = boxAt(m.body.Pos, w, h)
}

func boxAt(p path.Vec3, w, h float32) path.Box {
	return path.Box{Min: path.Vec3{X: p.X - w/2, Y: p.Y, Z: p.Z - w/2}, Max: path.Vec3{X: p.X + w/2, Y: p.Y + h, Z: p.Z + w/2}}
}

// Tick runs the thinking half of one tick of the mob on region r, in BDS's system order: the
// definition's pending changes, the goal selector, navigation, the move control, the jump and
// look controls, the movement pipeline and the body control. It may run at the same time as
// other mobs' Tick (see think.go); call Apply after it. It reports whether the mob's death has
// ended and it should be removed.
func (m *Mob) Tick(r *world.Region) (remove bool) {
	m.v.r = r
	m.thinking = true
	remove = m.think()
	m.thinking = false
	return remove
}

func (m *Mob) think() (remove bool) {
	if m.inst.Changed() || m.inst.Components() == nil {
		m.refresh()
	}
	if m.hurtTime > 0 {
		m.hurtTime--
	}
	if m.Dead() {
		m.deathTime++
		return m.deathTime >= deathTicks
	}
	if m.stateTick() {
		return true
	}
	m.sensorTick()
	m.prevPitch, m.prevYaw = m.pitch, m.yaw
	m.lookSet = false
	m.noActionTime++

	noAI := m.flags.Has(FlagNoAI)
	m.sel.Tick(noAI)
	if m.nv != nil && nav.ShouldTick(false, noAI, m.health) {
		nm := m.navMob()
		m.nv.Tick(pathWorld{&m.v}, &nm)
	}
	m.moveControl()
	m.lookControl()
	m.physicsTick()
	m.bodyControl()
	return false
}

// refresh rebuilds the mob from its definition's components, as BDS's reload: goals,
// navigation, movement, attributes and size.
func (m *Mob) refresh() {
	t := m.tester(nil)
	m.inst.Refresh(t, func(comps definition.Group) {
		order := m.inst.Order()
		m.readAttributes(comps)
		if n, _, ok, err := component.Navigation(order, comps); ok {
			if err != nil {
				m.rt.Log.Debug("mob navigation", "type", m.typ, "err", err)
			}
			m.nv = n
			nm := m.navMob()
			m.nv.Init(&nm)
		} else {
			m.nv = nil
		}
		if mv, ok, err := component.ReadMovement(order, comps); ok {
			if err != nil {
				m.rt.Log.Debug("mob movement", "type", m.typ, "err", err)
			}
			m.movement, m.hasMove = mv, true
			m.ctl.MaxTurn = mv.MaxTurn
		} else {
			m.hasMove = false
		}
		ctx := component.Context{World: goalWorld{&m.v}, Mob: m, Resolver: resolver{m}}
		m.readStates(ctx, comps)
		m.scale = 1
		if m.states != nil && m.states.Scale != nil {
			m.scale = m.states.Scale.Value
		}
		m.readSensors(ctx, comps)
		goals, warnings := m.rt.Goals.Build(ctx, order, comps)
		for _, w := range warnings {
			m.rt.Log.Debug("mob goal", "type", m.typ, "warning", w)
		}
		goals.Apply(&m.sel)
	})
	m.flagsChanged()
}

// readAttributes reads the attribute components the minimal hookup uses.
func (m *Mob) readAttributes(comps definition.Group) {
	if v, mx, ok := attribute(comps["minecraft:health"]); ok {
		if m.maxHealth != mx || m.inst.Components() == nil {
			m.health = v
		}
		m.maxHealth = mx
	}
	if v, _, ok := attribute(comps["minecraft:movement"]); ok {
		m.speed = v
	}
	if v, _, ok := attribute(comps["minecraft:underwater_movement"]); ok {
		m.underwaterSpeed = v
	}
	if v, _, ok := attribute(comps["minecraft:follow_range"]); ok {
		m.followRange = v
	}
	m.attackDamage = 0
	if raw, ok := comps["minecraft:attack"]; ok {
		var a struct {
			Damage json.RawMessage `json:"damage"`
		}
		if json.Unmarshal(raw, &a) == nil {
			var rg behavior.Range[float32]
			if rg.UnmarshalJSON(a.Damage) == nil {
				m.attackDamage = rg.Max
			}
		}
	}
	if raw, ok := comps["minecraft:collision_box"]; ok {
		var b struct{ Width, Height float32 }
		if json.Unmarshal(raw, &b) == nil {
			m.setSize(b.Width, b.Height)
		}
	}
	m.families = m.families[:0]
	if raw, ok := comps["minecraft:type_family"]; ok {
		var f struct {
			Family []string `json:"family"`
		}
		if json.Unmarshal(raw, &f) == nil {
			m.families = append(m.families, f.Family...)
		}
	}
	m.flags.Set(behavior.FlagFireImmune, comps["minecraft:fire_immune"] != nil)
	if raw, ok := comps["minecraft:physics"]; ok {
		var p struct {
			HasGravity   *bool `json:"has_gravity"`
			HasCollision *bool `json:"has_collision"`
		}
		if json.Unmarshal(raw, &p) == nil {
			m.flags.Set(FlagHasGravity, p.HasGravity == nil || *p.HasGravity)
			m.flags.Set(FlagCollision, p.HasCollision == nil || *p.HasCollision)
		}
	}
	m.persistent = m.persistent || comps["minecraft:persistent"] != nil
}

// attribute reads an attribute component: value and max, value as a number or a range.
func attribute(raw json.RawMessage) (v, mx float32, ok bool) {
	if raw == nil {
		return 0, 0, false
	}
	var a struct {
		Value json.RawMessage `json:"value"`
		Max   *float32        `json:"max"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Value == nil {
		return 0, 0, false
	}
	var rg behavior.Range[float32]
	if rg.UnmarshalJSON(a.Value) != nil {
		return 0, 0, false
	}
	v = rg.Max
	mx = v
	if a.Max != nil {
		mx = *a.Max
	}
	return v, mx, true
}

// navMob is the mob as navigation sees it.
func (m *Mob) navMob() nav.Mob {
	nm := nav.Mob{
		Pos: m.body.Pos, Vel: m.body.Vel, Box: m.body.Box, Width: m.body.Width, Height: m.body.Height,
		CanFly: m.flags.Has(nav.ActorCanFly), CanSwim: m.flags.Has(nav.ActorCanSwim), CanWalk: m.flags.Has(nav.ActorCanWalk),
		OnGround: m.body.OnGround, WasInWater: m.body.WasInWater, InLava: m.body.Travel() == physics.TravelLava,
		HorizontalCollision: m.body.HorizontalCollision[0] || m.body.HorizontalCollision[1],
		Actor:               flagActor{m}, Difficulty: m.rt.Env.Difficulty(), MobGriefing: m.rt.Env.MobGriefing(),
		FireImmune: m.flags.Has(behavior.FlagFireImmune),
	}
	if m.hasMove {
		nm.MoveControl = &m.ctl
	}
	return nm
}

type flagActor struct{ m *Mob }

func (a flagActor) Flag(n int) bool        { return a.m.flags.Has(n) }
func (a flagActor) SetFlag(n int, on bool) { a.m.SetFlag(n, on) }

// moveControl runs the movement component's control on the wanted position navigation set.
func (m *Mob) moveControl() {
	if !m.hasMove {
		return
	}
	b := move.Body{
		Pos: m.body.Pos, Vel: m.body.Vel, Box: m.body.Box, Width: m.body.Width,
		Pitch: m.pitch, Yaw: m.yaw, BodyYaw: m.bodyYaw, HeadYaw: m.headYaw, Flags: m.flags,
		OnGround: m.body.OnGround, WasInWater: m.body.WasInWater, InWater: m.body.WasInWater,
		MovementSpeed: m.speed, AirSpeed: m.body.AirSpeed, MaxAutoStep: m.body.MaxAutoStep,
		Speed: m.moveSpeed, MoveSpeed: m.moveSpeed,
		LocalMove: m.body.Input, Jump: &m.jump, Look: &m.look,
		Eye: path.Vec3{X: m.body.Pos.X, Y: m.body.Pos.Y + m.eyeHeight(), Z: m.body.Pos.Z},
	}
	// The walking controls speed up towards the end of the path the mob follows (BDS's
	// Mob::mSpeed and the navigation's last node); without them the speed stays 0.
	var end path.Pos
	if m.nv != nil {
		if p, _ := m.nv.Path(); p != nil && len(p.Points) > 0 {
			end = p.Points[len(p.Points)-1].Pos
			b.PathEnd = &end
		}
	}
	var target path.Vec3
	if t := m.rt.Env.Actor(m.v.r, m.target); t != nil {
		target = t.Pos()
		b.Target = &target
	}
	switch m.movement.Name {
	case "minecraft:movement.generic", "minecraft:movement.amphibious":
		move.Generic(pathWorld{&m.v}, &b, &m.ctl)
	case "minecraft:movement.fly":
		move.Fly(&b, &m.ctl)
	case "minecraft:movement.hover":
		move.Hover(&b, &m.ctl)
	default:
		// Basic, skip, jump, sway, glide and dolphin fall back to basic walking in this
		// first hookup.
		move.Basic(&b, &m.ctl)
	}
	m.yaw, m.bodyYaw, m.pitch = b.Yaw, b.BodyYaw, b.Pitch
	// SetSpeed keeps the new speed in Speed; a control that stops clears MoveSpeed.
	if m.moveSpeed != 0 && b.MoveSpeed == 0 {
		m.moveSpeed = 0
	} else {
		m.moveSpeed = b.Speed
	}
	m.body.Input = b.LocalMove
	m.body.Vel = b.Vel
	m.flags = b.Flags
}

// lookControl turns the head toward what a goal looks at, else back to the body.
func (m *Mob) lookControl() {
	if m.look.Has {
		d := path.Vec3{X: m.look.Pos.X - m.body.Pos.X, Y: m.look.Pos.Y - (m.body.Pos.Y + m.eyeHeight()), Z: m.look.Pos.Z - m.body.Pos.Z}
		yaw := float32(math.Atan2(float64(d.Z), float64(d.X)))*180/math.Pi - 90
		h := float32(math.Sqrt(float64(d.X*d.X + d.Z*d.Z)))
		pitch := -float32(math.Atan2(float64(d.Y), float64(h))) * 180 / math.Pi
		m.headYaw = approach(m.headYaw, yaw, m.look.YawSpeed)
		m.pitch = approach(m.pitch, pitch, m.look.PitchSpeed)
		m.look.Has = false
		return
	}
	m.headYaw = approach(m.headYaw, m.bodyYaw, 10)
}

// bodyControl turns the body: with the movement while it moves, else within reach of the
// head.
func (m *Mob) bodyControl() {
	dx, dz := m.body.Pos.X-m.body.PrevPos.X, m.body.Pos.Z-m.body.PrevPos.Z
	if dx*dx+dz*dz > 2.5e-7 {
		m.bodyYaw = m.yaw
		m.headYaw = clampAround(m.headYaw, m.bodyYaw, bodyMaxTurn)
		return
	}
	m.bodyYaw = clampAround(m.bodyYaw, m.headYaw, bodyMaxTurn)
}

func (m *Mob) physicsTick() {
	m.body.PrevPos = m.body.Pos
	m.body.Yaw = m.yaw
	m.body.Flags = m.flags
	m.body.MovementSpeed = m.speed
	m.body.Jumping = m.jump.Jumping
	m.jump.Jumping = false
	m.body.Nav = physics.Nav{Present: m.nv != nil, Following: m.nv != nil && !m.nv.Done()}
	if m.nv != nil {
		m.body.Nav.CanSink = m.nv.Flags&nav.FlagCanSink != 0
		m.body.Nav.CanWalkInLava = m.nv.Flags&nav.FlagCanWalkInLava != 0
	}
	if t := m.rt.Env.Actor(m.v.r, m.target); t != nil {
		m.body.HasTarget, m.body.Target = true, t.Pos()
	} else {
		m.body.HasTarget = false
	}
	m.body.Tick(physWorld{&m.v})
	m.body.Box = boxAt(m.body.Pos, m.body.Width, m.body.Height)
}

func (m *Mob) eyeHeight() float32 { return m.body.Height * 0.85 }

func wrap(d float32) float32 {
	d = float32(math.Mod(float64(d), 360))
	if d >= 180 {
		d -= 360
	}
	if d < -180 {
		d += 360
	}
	return d
}

func approach(from, to, step float32) float32 {
	d := wrap(to - from)
	return from + max(-step, min(step, d))
}

func clampAround(v, centre, limit float32) float32 {
	d := wrap(v - centre)
	return centre + max(-limit, min(limit, d))
}

// Hurt takes damage from the attacker (NoID for none) with a cause. It applies BDS's hurt
// time: within it only damage above the last hurt counts. It reports the damage dealt.
func (m *Mob) Hurt(amount float32, by behavior.ID, cause int, tick uint64) float32 {
	if m.Dead() || amount <= 0 {
		return 0
	}
	attacker := m.actor(by)
	amount, ok := m.damageSensed(amount, cause, attacker)
	if !ok || amount <= 0 {
		return 0
	}
	dealt := amount
	if m.hurtTime > 0 {
		if amount <= m.lastHurtAmount {
			return 0
		}
		dealt = amount - m.lastHurtAmount
	} else {
		m.hurtTime = hurtTicks
	}
	m.lastHurtAmount = amount
	m.health = max(m.health-dealt, 0)
	m.lastCause = cause
	if by != behavior.NoID {
		m.lastHurtBy, m.lastHurtTick = by, tick
	}
	m.noActionTime = 0
	m.fire(onHurt, attacker)
	if attacker != nil && attacker.IsPlayer() {
		m.fire(onHurtByPlayer, attacker)
	}
	if m.Dead() {
		m.fire(onDeath, attacker)
	}
	m.snapshot()
	return dealt
}

// Knock pushes the mob as a hit does.
func (m *Mob) Knock(v behavior.Vec3) { m.body.Vel = v }

// SetPos moves the mob, as a teleport does.
func (m *Mob) SetPos(p behavior.Vec3) {
	m.body.Pos, m.body.PrevPos = p, p
	m.body.Box = boxAt(p, m.body.Width, m.body.Height)
	if !m.thinking {
		m.snapshot()
	}
}

// Ready builds the mob from its definition before it is in the world, so its size,
// attributes and flags are known: the natural spawner checks them before adding it. r is the
// region it will join. Runs: owner of r.
func (m *Mob) Ready(r *world.Region) {
	m.v.r = r
	if m.inst.Components() == nil {
		m.refresh()
	}
	m.snapshot()
}

// Event runs a definition event on the mob.
func (m *Mob) RunEvent(name string) { m.inst.Execute(name, m.tester(nil)) }
