package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/effect"
)

// A mob's tick has two halves. The thinking half is TickPhysics (physics, world.EntityConfig
// Physics), Publish and Tick (goals, navigation and the controls, Think); the region runs
// each over every mob before the next, as BDS runs its systems. In a crowded region many
// mobs think at once on different cores, so while thinking a mob changes only itself, sees
// every other entity as its Snapshot (taken after its TickPhysics, and again at the end of its
// Apply), and queues whatever it does to
// anything else. Apply is the other half: it runs on the region's goroutine, mobs in runtime
// ID order, and does the queued work in the order it was asked for. One core or many, the
// results are the same.

// opKind is what a queued op does.
type opKind uint8

const (
	opSound opKind = iota
	opSwing
	opAttack
	opLevelEvent
	opBreakBlock
	opSetBlock
	opParticle
	opSetMobTarget
	opMobEvent
	opSetSlot
	opDropItem
	opPickUp
	opActorEvent
	opHurt
	opShoot
	opBreed
	opDropLeash
	opDwellerTick
	opDoor
	opDoorEvent
	opCarried
	opExplode
	opTransform
	opLovePartner
	opLoveLeader
	opWilling
	opUseBreedFood
	opSpawnBaby
	opSonicHit
	opKnockback
	opLevelEventXYZ
	opJoinCaravan
	opLeaveCaravan
	opHurtBy
	opRaidBlock
	opSpawnProjectile
	opDropLoot
	opSetVelocityOf
	opRemoveActor
	opMakeJump
	opLevelEventData
	opSetMingle
	opDropHorn
	opGiveEffect
	opRemoveEffect
	opClearEffects
	opDrinkPotion
	opStartRiding
	opStopRiding
	opEjectRiders
	opAddRiders
	opTeleport
	opSpawnEntry
	opGameEvent
	opPlaceBlock
)

// ShowHooks is what an engine that shows a mob's hand item for trade_interest implements on
// its Hooks: the item in the mob's main hand changed for show only.
type ShowHooks interface {
	Carried(m *Mob)
}

// op is one queued change. Only the fields its kind uses are set.
type op struct {
	kind   opKind
	n      int32
	id     behavior.ID
	other  behavior.ID
	at     behavior.Vec3
	pos    behavior.Pos
	name   string
	placed behavior.PlacedBlock
	then   behavior.AttackResult
	amount float32
	// item is the stack of the item ops (items.go).
	item behavior.Item
	// knock is the push of opKnockback.
	knock behavior.Knockback
	// dir is the aim of opSpawnProjectile.
	dir behavior.Vec3
	// mingle is the MingleComponent of opSetMingle.
	mingle behavior.MingleState
	// effect is the effect of opGiveEffect, its ID in n or by its name.
	effect effect.Instance
	// spawn is the minecraft:spawn_entity entry of opSpawnEntry.
	spawn *state.SpawnEntry
}

// do runs o now, or queues it for Apply while the mob thinks.
func (m *Mob) do(o op) {
	if m.thinking {
		m.ops = append(m.ops, o)
		return
	}
	m.run(o)
}

// flagsChanged tells those who see the mob its flags changed: once, at Apply, while thinking.
func (m *Mob) flagsChanged() {
	if m.thinking {
		m.flagsDirty = true
		return
	}
	if m.hooks != nil && m.v.r != nil {
		m.hooks.FlagsChanged(m)
	}
}

// Apply does what the mob queued while it thought, in the order it asked, then takes the
// snapshot other mobs see next tick. Runs: the region's goroutine, after the think phase.
func (m *Mob) Apply() {
	for i := range m.ops {
		m.run(m.ops[i])
		m.ops[i] = op{}
	}
	m.ops = m.ops[:0]
	m.tickRiders()
	m.pushActors()
	if m.flagsDirty {
		m.flagsDirty = false
		m.flagsChanged()
	}
	m.snapshot()
}

// run does one op.
func (m *Mob) run(o op) {
	r, env := m.v.r, m.rt.Env
	if r == nil {
		if o.kind == opAttack && o.then != nil {
			o.then.AttackResult(o.id, false, false)
		}
		return
	}
	switch o.kind {
	case opSound:
		m.traceSound(int(o.n), o.at)
		if m.hooks != nil {
			m.hooks.Sound(m, int(o.n), o.at)
		}
	case opSwing:
		m.traceOp(&o, "swing", true)
		if m.hooks != nil {
			m.hooks.Swing(m)
		}
	case opAttack:
		hit, killed := false, false
		if m.hooks != nil && !m.Dead() {
			hit, killed = m.hooks.Attack(m, o.id)
		}
		if hit {
			m.lastHurtMob = o.id
			m.lastHurtStamp++
			m.noActionTime = 0
		}
		m.traceOp(&o, "attack", hit)
		if killed {
			m.traceOp(&o, "killed target", true)
		}
		if _, melee := o.then.(*behavior.Melee); melee && !m.Dead() {
			m.meleeEffect(o.id)
		}
		if o.then != nil {
			o.then.AttackResult(o.id, hit, killed)
		}
	case opLevelEvent:
		env.LevelEvent(r, o.n, o.at, o.name)
	case opBreakBlock:
		env.BreakBlock(r, o.pos, o.id)
	case opSetBlock:
		env.SetBlock(r, o.pos, o.placed, o.id)
	case opParticle:
		env.Particle(r, o.name, o.at)
	case opSetMobTarget:
		if t := env.Mob(r, o.id); t != nil {
			t.SetTarget(o.other)
			t.snapshot()
		}
	case opShoot:
		m.traceOp(&o, "shoot", true)
		if m.hooks != nil && !m.Dead() {
			m.hooks.Shoot(m, o.id, o.name, o.n, o.amount, int(o.other))
		}
	case opActorEvent:
		if m.hooks != nil {
			m.hooks.ActorEvent(m, int(o.n))
		}
	case opHurt:
		if m.hooks != nil {
			m.hooks.Hurt(m, o.amount, int(o.n))
		}
	case opBreed:
		m.mate(o.id)
	case opLovePartner, opLoveLeader, opWilling, opUseBreedFood, opSpawnBaby:
		m.runLoveOp(&o)
	case opDropLeash:
		if m.hooks != nil {
			m.hooks.LeashDropped(m, o.n != 0)
		}
	case opCarried:
		if h, ok := m.hooks.(ShowHooks); ok {
			h.Carried(m)
		}
	case opTransform:
		m.traceOp(&o, "transform", true)
		m.transforming = false
		if h, ok := m.hooks.(TransformHooks); ok && m.states != nil && m.states.Transformation != nil && !m.Dead() {
			h.Transform(m, m.states.Transformation.Config())
		}
	case opExplode:
		m.traceOp(&o, "explode", true)
		if h, ok := m.hooks.(ExplodeHooks); ok {
			h.Explode(m, o.at, m.blast)
		}
	case opDwellerTick:
		m.runDwellerTick(r)
	case opDoor:
		env.SetDoorOpen(r, o.pos, o.n != 0)
	case opDoorEvent:
		env.LevelEventData(r, o.n, o.at, int32(o.amount))
	case opSetSlot, opDropItem, opPickUp:
		m.runItemOp(r, &o)
	case opSonicHit, opKnockback, opLevelEventXYZ:
		m.runWardenOp(&o)
	case opJoinCaravan, opLeaveCaravan:
		m.runCaravanOp(&o)
	case opHurtBy:
		if h, ok := m.hooks.(GuardianHooks); ok && !m.Dead() {
			h.HurtBy(m, o.id, int(o.n), o.amount, o.other != 0)
		}
	case opRaidBlock:
		if e, ok := env.(GardenEnv); ok {
			e.RaidBlock(r, o.pos, m.id)
		}
	case opSpawnProjectile, opDropLoot, opSetVelocityOf, opRemoveActor, opMakeJump, opLevelEventData,
		opSetMingle, opDropHorn:
		m.runOtherOp(&o)
	case opGiveEffect, opRemoveEffect, opClearEffects, opDrinkPotion:
		m.runEffectOp(&o)
	case opStartRiding, opStopRiding, opEjectRiders, opAddRiders:
		m.runRideOp(&o)
	case opTeleport, opSpawnEntry, opGameEvent, opPlaceBlock:
		m.runActionOp(&o)
	case opMobEvent:
		if t := env.Mob(r, o.id); t != nil {
			t.v.r = r
			t.Event(o.name, o.other)
			t.snapshot()
		}
	}
}

// Snapshot is a mob as the other mobs see it while they think: a copy taken when its last
// Apply ended, and again by Publish once it has moved this tick. Valid until its next Apply.
type Snapshot struct {
	// mob is the mob it was taken of, for Apply-time work such as pushing; never read
	// while mobs think.
	mob           *Mob
	id            behavior.ID
	typ           string
	pos, eyes     behavior.Vec3
	box           behavior.Box
	alive         bool
	flags         path.ActorFlags
	headPitch     float32
	headYaw       float32
	bodyYaw       float32
	yaw           float32
	monster       bool
	target        behavior.ID
	lastHurtBy    behavior.ID
	lastHurtMob   behavior.ID
	lastHurtStamp int32
	dimension     int
	families      []string
	comps         definition.Group
	health        float32
	inWater       bool
	underWater    bool
	inLava        bool
	onGround      bool
	// breedReady is minecraft:breedable present and not cooling down, for other mobs'
	// CanMate.
	breedReady bool
	// trusted and hasTrust are the trust component's set (trust.go).
	trusted  []behavior.ID
	hasTrust bool
	// chasing and the caravan fields are pack.go's.
	chasing                  bool
	inCaravan                bool
	caravanHead, caravanTail behavior.ID
	// equip is the equipment, for other mobs' has_equipment tests.
	equip [EquipSlots]behavior.Item
	// lovePartner, loveLeader, willing and breedFood are the make_love state (love.go).
	lovePartner                    behavior.ID
	loveLeader, willing, breedFood bool
	// vel and prevPos are StateVectorComponent's posDelta and posPrev (mingle.go).
	vel, prevPos behavior.Vec3
	// mingle is MingleComponent, set when hasMingle is.
	mingle    behavior.MingleState
	hasMingle bool
	// riders and vehicle are the mob's riding; rideable, seatCount, rideMaxWidth and
	// rideFamilies its rideable rules, for find_mount.
	riders       []behavior.ID
	vehicle      behavior.ID
	rideable     bool
	seatCount    int
	rideMaxWidth float32
	rideFamilies []string
}

// SetBoxListener has fn told the mob's box each time its snapshot is taken, and once now, so
// the engine can keep a copy for its area searches. Nil stops it. Runs: owner, not while the
// mob thinks.
func (m *Mob) SetBoxListener(fn func(behavior.Box)) {
	m.onBox = fn
	if fn != nil {
		fn(m.snap.box)
	}
}

// Snapshot returns the mob as others see it this tick.
func (m *Mob) Snapshot() *Snapshot { return &m.snap }

// snapshot copies the parts of the mob others read. It reuses the families slice.
func (m *Mob) snapshot() {
	s := &m.snap
	s.mob = m
	s.id, s.typ = m.id, m.typ
	s.pos, s.eyes, s.box = m.body.Pos, m.Eyes(), m.body.Box
	if m.onBox != nil {
		m.onBox(s.box)
	}
	s.vel, s.prevPos = m.body.Vel, m.body.PrevPos
	s.mingle, s.hasMingle = m.mingle, m.hasMingle
	s.alive = !m.Dead()
	s.flags = m.flags
	s.headPitch, s.headYaw, s.bodyYaw, s.yaw = m.pitch, m.headYaw, m.bodyYaw, m.yaw
	s.monster = m.Monster()
	s.target, s.lastHurtBy, s.lastHurtMob, s.lastHurtStamp = m.target, m.lastHurtBy, m.lastHurtMob, m.lastHurtStamp
	s.dimension = m.Dimension()
	s.families = append(s.families[:0], m.families...)
	s.comps = m.inst.Components()
	s.health = m.health
	s.inWater, s.underWater, s.inLava, s.onGround = m.InWater(), m.UnderWater(), m.InLava(), m.OnGround()
	s.equip = m.equip
	s.lovePartner, s.loveLeader, s.willing = m.love.partner, m.love.leader, m.love.willing
	s.breedFood = m.breedFoodSlot() >= 0
	s.breedReady = m.states != nil && m.states.Breedable != nil && m.states.Breedable.Cooldown() <= 0
	s.trusted, s.hasTrust = m.trusted, m.hasTrust()
	s.chasing, s.inCaravan, s.caravanHead, s.caravanTail = m.chasing, m.InCaravan(), m.caravanHead, m.caravanTail
	s.riders, s.vehicle = append(s.riders[:0], m.ride.riders...), m.ride.vehicle
	s.rideable = m.ride.seats != nil
	s.seatCount, s.rideMaxWidth = m.ride.rules.SeatCount, m.ride.rules.MaxWidth
	s.rideFamilies = append(s.rideFamilies[:0], m.ride.rules.Families...)
}

func (s *Snapshot) ID() behavior.ID                    { return s.id }
func (s *Snapshot) Pos() behavior.Vec3                 { return s.pos }
func (s *Snapshot) Box() behavior.Box                  { return s.box }
func (s *Snapshot) Eyes() behavior.Vec3                { return s.eyes }
func (s *Snapshot) Alive() bool                        { return s.alive }
func (s *Snapshot) Removed() bool                      { return false }
func (s *Snapshot) Pickable() bool                     { return s.alive }
func (s *Snapshot) Interacts() bool                    { return true }
func (s *Snapshot) IsMob() bool                        { return true }
func (s *Snapshot) IsPlayer() bool                     { return false }
func (s *Snapshot) Flag(n int) bool                    { return s.flags.Has(n) }
func (s *Snapshot) Type() string                       { return s.typ }
func (s *Snapshot) GameMode() int                      { return -1 }
func (s *Snapshot) Riders() []behavior.ID              { return s.riders }
func (s *Snapshot) Vehicle() behavior.ID               { return s.vehicle }
func (s *Snapshot) Owner() behavior.ID                 { return behavior.NoID }
func (s *Snapshot) Team() int                          { return 0 }
func (s *Snapshot) Dimension() int                     { return s.dimension }
func (s *Snapshot) HeadRotation() (pitch, yaw float32) { return s.headPitch, s.headYaw }
func (s *Snapshot) BodyYaw() float32                   { return s.bodyYaw }

// Yaw is the mob's ActorRotationComponent yaw, as entity's goals read it
// from other actors.
func (s *Snapshot) Yaw() float32                      { return s.yaw }
func (s *Snapshot) Invulnerable() bool                { return false }
func (s *Snapshot) HasHealth() bool                   { return true }
func (s *Snapshot) Targetable() bool                  { return !s.flags.Has(behavior.FlagCannotTarget) }
func (s *Snapshot) Monster() bool                     { return s.monster }
func (s *Snapshot) ArmourCoverage() float32           { return 0 }
func (s *Snapshot) WearsHeadOf(string) bool           { return false }
func (s *Snapshot) Target() behavior.ID               { return s.target }
func (s *Snapshot) HijacksMount() bool                { return false }
func (s *Snapshot) LastHurtByMob() behavior.ID        { return s.lastHurtBy }
func (s *Snapshot) LastHurtMob() (behavior.ID, int32) { return s.lastHurtMob, s.lastHurtStamp }
func (s *Snapshot) BlocksSpace() bool                 { return false }
func (s *Snapshot) AttachPos(int) behavior.Vec3       { return s.eyes }

// Families is the mob's type_family list.
func (s *Snapshot) Families() []string { return s.families }

var _ behavior.Actor = (*Snapshot)(nil)
