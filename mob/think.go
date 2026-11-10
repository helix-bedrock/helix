package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/path"
)

// A mob's tick has two halves (world.EntityConfig Think and Input). Tick is the thinking
// half: goals, navigation, the controls and physics. In a crowded region many mobs think at
// once on different cores, so while thinking a mob changes only itself, sees every other
// entity as its Snapshot from the end of their last apply, and queues whatever it does to
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
)

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
		if m.hooks != nil {
			m.hooks.Sound(m, int(o.n), o.at)
		}
	case opSwing:
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
	case opMobEvent:
		if t := env.Mob(r, o.id); t != nil {
			t.v.r = r
			t.Event(o.name, o.other)
			t.snapshot()
		}
	}
}

// Snapshot is a mob as the other mobs see it while they think: a copy taken when its last
// Apply ended. Valid until its next Apply.
type Snapshot struct {
	id            behavior.ID
	typ           string
	pos, eyes     behavior.Vec3
	box           behavior.Box
	alive         bool
	flags         path.ActorFlags
	headPitch     float32
	headYaw       float32
	bodyYaw       float32
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
}

// Snapshot returns the mob as others see it this tick.
func (m *Mob) Snapshot() *Snapshot { return &m.snap }

// snapshot copies the parts of the mob others read. It reuses the families slice.
func (m *Mob) snapshot() {
	s := &m.snap
	s.id, s.typ = m.id, m.typ
	s.pos, s.eyes, s.box = m.body.Pos, m.Eyes(), m.body.Box
	s.alive = !m.Dead()
	s.flags = m.flags
	s.headPitch, s.headYaw, s.bodyYaw = m.pitch, m.headYaw, m.bodyYaw
	s.monster = m.Monster()
	s.target, s.lastHurtBy, s.lastHurtMob, s.lastHurtStamp = m.target, m.lastHurtBy, m.lastHurtMob, m.lastHurtStamp
	s.dimension = m.Dimension()
	s.families = append(s.families[:0], m.families...)
	s.comps = m.inst.Components()
	s.health = m.health
	s.inWater, s.underWater, s.inLava, s.onGround = m.InWater(), m.UnderWater(), m.InLava(), m.OnGround()
}

func (s *Snapshot) ID() behavior.ID                    { return s.id }
func (s *Snapshot) Pos() behavior.Vec3                 { return s.pos }
func (s *Snapshot) Box() behavior.Box                  { return s.box }
func (s *Snapshot) Eyes() behavior.Vec3                { return s.eyes }
func (s *Snapshot) Alive() bool                        { return s.alive }
func (s *Snapshot) Removed() bool                      { return false }
func (s *Snapshot) Pickable() bool                     { return s.alive }
func (s *Snapshot) IsMob() bool                        { return true }
func (s *Snapshot) IsPlayer() bool                     { return false }
func (s *Snapshot) Flag(n int) bool                    { return s.flags.Has(n) }
func (s *Snapshot) Type() string                       { return s.typ }
func (s *Snapshot) GameMode() int                      { return -1 }
func (s *Snapshot) Riders() []behavior.ID              { return nil }
func (s *Snapshot) Vehicle() behavior.ID               { return behavior.NoID }
func (s *Snapshot) Owner() behavior.ID                 { return behavior.NoID }
func (s *Snapshot) Team() int                          { return 0 }
func (s *Snapshot) Dimension() int                     { return s.dimension }
func (s *Snapshot) HeadRotation() (pitch, yaw float32) { return s.headPitch, s.headYaw }
func (s *Snapshot) BodyYaw() float32                   { return s.bodyYaw }
func (s *Snapshot) Invulnerable() bool                 { return false }
func (s *Snapshot) HasHealth() bool                    { return true }
func (s *Snapshot) Targetable() bool                   { return !s.flags.Has(behavior.FlagCannotTarget) }
func (s *Snapshot) Monster() bool                      { return s.monster }
func (s *Snapshot) ArmourCoverage() float32            { return 0 }
func (s *Snapshot) WearsHeadOf(string) bool            { return false }
func (s *Snapshot) Target() behavior.ID                { return s.target }
func (s *Snapshot) HijacksMount() bool                 { return false }
func (s *Snapshot) LastHurtByMob() behavior.ID         { return s.lastHurtBy }
func (s *Snapshot) LastHurtMob() (behavior.ID, int32)  { return s.lastHurtMob, s.lastHurtStamp }
func (s *Snapshot) BlocksSpace() bool                  { return false }
func (s *Snapshot) AttachPos(int) behavior.Vec3        { return s.eyes }

// Families is the mob's type_family list.
func (s *Snapshot) Families() []string { return s.families }

var _ behavior.Actor = (*Snapshot)(nil)
