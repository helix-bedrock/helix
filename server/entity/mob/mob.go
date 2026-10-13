package mob

import (
	"encoding/json"
	"github.com/helix-bedrock/helix/server/item"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/entity/effect"
	"github.com/helix-bedrock/helix/server/entity/path"
	"github.com/helix-bedrock/helix/server/world"
)

// Mob is a vanilla mob in a transaction. It is valid only in the
// transaction it was opened in.
type Mob struct {
	tx     *world.Tx
	handle *world.EntityHandle
	data   *world.EntityData
	s      *state
}

// state is what a mob keeps between ticks. It lives in
// world.EntityData.Data.
type state struct {
	t      *Type
	groups []string
	spec   *spec
	rand   *rand.Rand

	health  *entity.HealthManager
	effects *entity.EffectManager
	speed   float64

	nav            path.Navigator
	goals, targets selector
	look           look
	headYaw, pitch float64

	forward      float64
	jumping      bool
	jumpCooldown int
	onGround     bool
	inWater      bool
	inLava       bool
	fallDistance float64

	target       *world.EntityHandle
	attacker     *world.EntityHandle
	hurtTick     int64 // tick of the last damage from anything
	attackedTick int64 // tick of the last damage from an entity
	immuneTicks  int
	lastDamage   float64
	killer       world.Entity

	ticks      int64
	deathTicks int
	persistent bool

	// natural is set for mobs spawned by the Spawner, with their population
	// category and whether they spawned in a cave, for the caps.
	natural     bool
	category    string
	underground bool
	// inactive counts ticks spent far from players, for despawning.
	inactive int

	// fuse counts down to an explosion, -1 when no fuse burns.
	fuse          int
	sensorInside  bool
	age           int
	love          int
	breedCooldown int
	// held is the item in the mob's hand, from its equipment table.
	held     item.Stack
	equipped bool

	// dest is where a swimming or flying mob steers to.
	dest        *mgl64.Vec3
	directSpeed float64
}

func (t *Type) newState(seed [2]uint64, groups []string) *state {
	s := &state{t: t, rand: rand.New(rand.NewPCG(seed[0], seed[1]))}
	s.setGroups(groups)
	s.health = entity.NewHealthManager(s.spec.health, s.spec.maxHealth)
	s.effects = entity.NewEffectManager()
	s.speed = s.spec.speed
	s.fuse = -1
	return s
}

// setGroups changes the active component groups and rebuilds the goals.
func (s *state) setGroups(groups []string) {
	s.groups = groups
	s.spec = s.t.def.spec(groups)
	s.goals, s.targets = buildGoals(s.spec)
	s.nav = path.Navigator{
		FollowRange: s.spec.followRange,
		Reach:       1,
		AvoidSun:    s.spec.avoidSun,
		Mob: path.Mob{
			Width: s.spec.width * s.spec.scale, Height: s.spec.height * s.spec.scale,
			MaxFall: 3, CanFloat: s.spec.canFloat, CanOpenDoors: s.spec.canOpenDoors,
			CanPassDoors: s.spec.canPassDoors, CanWalkOverFences: s.spec.canWalkOverFences,
		},
	}
	// Vanilla mob costs (Mob.java): fire is avoided, lava and burning blocks
	// forbidden, water costly unless the mob swims.
	s.nav.Mob.SetMalus(path.DangerFire, 16)
	s.nav.Mob.SetMalus(path.DamageFire, -1)
	s.nav.Mob.SetMalus(path.Lava, -1)
	s.nav.Mob.SetMalus(path.DangerOther, 8)
	if s.spec.canFloat {
		s.nav.Mob.SetMalus(path.Water, 0)
	} else {
		s.nav.Mob.SetMalus(path.Water, 8)
	}
	if s.spec.avoidWater {
		s.nav.Mob.SetMalus(path.Water, -1)
	}
}

// H returns the mob's handle.
func (m *Mob) H() *world.EntityHandle { return m.handle }

// Tx returns the transaction the mob is open in.
func (m *Mob) Tx() *world.Tx { return m.tx }

// Def returns the mob's definition.
func (m *Mob) Def() *Def { return m.s.t.def }

// Position returns where the mob's feet are.
func (m *Mob) Position() mgl64.Vec3 { return m.data.Pos }

// Rotation returns the mob's body yaw and its pitch.
func (m *Mob) Rotation() cube.Rotation { return m.data.Rot }

// HeadYaw returns where the mob's head faces, which may differ from its
// body.
func (m *Mob) HeadYaw() float64 { return m.s.headYaw }

// Velocity returns the mob's velocity in blocks per tick.
func (m *Mob) Velocity() mgl64.Vec3 { return m.data.Vel }

// SetVelocity sets the mob's velocity.
func (m *Mob) SetVelocity(v mgl64.Vec3) { m.data.Vel = v }

// OnGround reports whether the mob stands on something.
func (m *Mob) OnGround() bool { return m.s.onGround }

// Families returns the mob's type families, such as "zombie" and "monster".
func (m *Mob) Families() []string { return m.s.spec.families }

// Baby reports whether the mob is a baby.
func (m *Mob) Baby() bool { return m.s.spec.baby }

// Scale returns the mob's size relative to normal.
func (m *Mob) Scale() float64 { return m.s.spec.scale }

// Groups returns the mob's active component groups.
func (m *Mob) Groups() []string { return slices.Clone(m.s.groups) }

// TriggerEvent runs one of the mob's definition events, such as
// "minecraft:become_angry", changing its component groups.
func (m *Mob) TriggerEvent(name string) {
	r := &eventRun{def: m.s.t.def, groups: slices.Clone(m.s.groups), rand: m.s.rand.Float64,
		filter: func(f json.RawMessage) bool { return evalFilter(f, filterEnv{tx: m.tx, self: m}) }}
	r.fire(name)
	if !slices.Equal(r.groups, m.s.groups) {
		m.s.setGroups(r.groups)
		m.SetMaxHealth(m.s.spec.maxHealth)
		m.s.speed = m.s.spec.speed
		m.updateState()
	}
}

// Target returns the entity the mob is after, if it is still in the world.
func (m *Mob) Target() (world.Entity, bool) {
	if m.s.target == nil {
		return nil, false
	}
	e, ok := m.s.target.Entity(m.tx)
	if !ok {
		m.s.target = nil
	}
	return e, ok
}

// SetTarget sets the entity the mob is after. Nil clears it.
func (m *Mob) SetTarget(e world.Entity) {
	if e == nil {
		m.s.target = nil
		return
	}
	m.s.target = e.H()
}

// Navigator returns the mob's navigator, to send it somewhere.
func (m *Mob) Navigator() *path.Navigator { return &m.s.nav }

// LookAt turns the mob's head towards pos over the next ticks.
func (m *Mob) LookAt(pos mgl64.Vec3) { m.s.look.at(pos, 10, 40) }

// Health returns the mob's health.
func (m *Mob) Health() float64 { return m.s.health.Health() }

// MaxHealth returns the mob's maximum health.
func (m *Mob) MaxHealth() float64 { return m.s.health.MaxHealth() }

// SetMaxHealth sets the mob's maximum health.
func (m *Mob) SetMaxHealth(v float64) { m.s.health.SetMaxHealth(v) }

// Dead reports whether the mob has died.
func (m *Mob) Dead() bool { return m.s.health.Health() <= 0 }

// Speed returns the mob's movement speed in blocks per tick.
func (m *Mob) Speed() float64 { return m.s.speed }

// SetSpeed sets the mob's movement speed.
func (m *Mob) SetSpeed(v float64) { m.s.speed = v }

// AddEffect gives the mob an effect.
func (m *Mob) AddEffect(e effect.Effect) { m.s.effects.Add(e, m) }

// RemoveEffect removes an effect from the mob.
func (m *Mob) RemoveEffect(e effect.Type) { m.s.effects.Remove(e, m) }

// Effects returns the mob's effects.
func (m *Mob) Effects() []effect.Effect { return m.s.effects.Effects() }

// Effect returns one of the mob's effects.
func (m *Mob) Effect(e effect.Type) (effect.Effect, bool) { return m.s.effects.Effect(e) }

// OnFireDuration returns how long the mob keeps burning.
func (m *Mob) OnFireDuration() time.Duration { return m.data.FireDuration }

// SetOnFire sets the mob on fire for d.
func (m *Mob) SetOnFire(d time.Duration) {
	d = max(d, 0)
	changed := (m.data.FireDuration > 0) != (d > 0)
	m.data.FireDuration = d
	if changed {
		m.updateState()
	}
}

// Extinguish puts the fire on the mob out.
func (m *Mob) Extinguish() { m.SetOnFire(0) }

// NameTag returns the mob's name tag.
func (m *Mob) NameTag() string { return m.data.Name }

// SetNameTag names the mob. A named mob never despawns.
func (m *Mob) SetNameTag(s string) {
	m.data.Name = s
	m.s.persistent = m.s.persistent || s != ""
	m.updateState()
}

// Heal heals the mob.
func (m *Mob) Heal(h float64, _ world.HealingSource) float64 {
	if m.Dead() || h <= 0 {
		return 0
	}
	before := m.Health()
	m.s.health.AddHealth(h)
	return m.Health() - before
}

// immuneTicks is how long a mob ignores weaker hits after being hurt, as
// vanilla: half a second.
const immuneTicks = 10

// Hurt damages the mob. Within half a second of the last hit, only damage
// above that hit's counts, as in vanilla.
func (m *Mob) Hurt(dmg float64, src world.DamageSource) (float64, bool) {
	if m.Dead() || dmg < 0 {
		return 0, false
	}
	if _, ok := m.Effect(effect.FireResistance); ok && src.Fire() {
		return 0, false
	}
	if res, ok := m.Effect(effect.Resistance); ok && src.ReducedByResistance() {
		dmg *= 1 - effect.Resistance.Multiplier(src, res.Level())
	}
	n := dmg
	if m.s.immuneTicks > 0 {
		if n = dmg - m.s.lastDamage; n <= 0 {
			return 0, false
		}
	} else {
		m.s.immuneTicks = immuneTicks
		m.viewAction(entity.HurtAction{})
	}
	m.s.lastDamage = dmg
	m.s.health.AddHealth(-n)
	m.s.hurtTick = m.s.ticks
	if attacker := attackerOf(src); attacker != nil {
		m.s.attacker = attacker.H()
		m.s.attackedTick = m.s.ticks
		m.s.killer = attacker
	}
	if m.Dead() {
		m.die()
	}
	return n, true
}

// attackerOf returns the entity responsible for damage, if any.
func attackerOf(src world.DamageSource) world.Entity {
	switch s := src.(type) {
	case entity.AttackDamageSource:
		return s.Attacker
	case entity.ProjectileDamageSource:
		return s.Owner
	}
	return nil
}

// KnockBack pushes the mob away from src.
func (m *Mob) KnockBack(src mgl64.Vec3, force, height float64) {
	force *= 1 - m.s.spec.knockbackResist
	if force <= 0 {
		return
	}
	d := m.Position().Sub(src)
	d[1] = 0
	if d.Len() < 1e-4 {
		d = mgl64.Vec3{m.s.rand.Float64() - 0.5, 0, m.s.rand.Float64() - 0.5}
	}
	v := d.Normalize().Mul(force)
	v[1] = height
	m.data.Vel = v
}

func (m *Mob) die() {
	m.s.nav.Stop()
	m.SetTarget(nil)
	m.Extinguish()
	m.viewAction(entity.DeathAction{})
}

// Close removes the mob from its world.
func (m *Mob) Close() error {
	m.tx.RemoveEntity(m)
	return m.handle.Close()
}

func (m *Mob) viewAction(a world.EntityAction) {
	for _, v := range m.tx.Viewers(m.data.Pos) {
		v.ViewEntityAction(m, a)
	}
}

func (m *Mob) updateState() {
	for _, v := range m.tx.Viewers(m.data.Pos) {
		v.ViewEntityState(m)
	}
}

// eyePos is where the mob sees from.
func (m *Mob) eyePos() mgl64.Vec3 {
	return m.data.Pos.Add(mgl64.Vec3{0, m.s.spec.height * m.s.spec.scale * 0.85})
}

// Tick runs the mob for one tick: its AI, movement and surroundings.
func (m *Mob) Tick(tx *world.Tx, current int64) {
	s := m.s
	s.ticks++
	if m.Dead() {
		// The death animation takes a second; then the body goes.
		if s.deathTicks++; s.deathTicks >= 20 {
			m.dropLoot()
			_ = m.Close()
		}
		return
	}
	if m.data.Pos[1] < float64(tx.Range()[0])-64 {
		m.Hurt(4, entity.VoidDamageSource{})
		return
	}
	if s.immuneTicks > 0 {
		s.immuneTicks--
	}
	if m.despawn(tx) {
		return
	}
	if !s.equipped {
		s.equipped = true
		m.equip()
	}
	s.effects.Tick(m, tx)
	m.sense()
	m.burn(current)
	if m.Dead() {
		return
	}

	// The pathfinder must know where the world ends; worlds reach below 0.
	s.nav.Mob.MinY = tx.Range()[0]
	s.targets.tick(m)
	s.goals.tick(m)
	m.tickComponents(tx)
	if m.handle.Closed() {
		return
	}
	if s.spec.walks {
		st := s.nav.Tick(tx, m.data.Pos, s.onGround, s.inWater, s.speed, m.data.Rot.Yaw())
		if !s.nav.Idle() {
			m.data.Rot = cube.Rotation{st.Yaw, m.data.Rot.Pitch()}
			s.forward = st.Forward
			s.jumping = s.jumping || st.Jump
		} else {
			s.forward = 0
		}
	}
	if s.spec.swims || s.spec.flies {
		m.steer()
	}
	s.headYaw, s.pitch = s.look.tick(m.eyePos(), s.headYaw, s.pitch, m.data.Rot.Yaw(), !s.nav.Idle())
	m.data.Rot = cube.Rotation{m.data.Rot.Yaw(), s.pitch}

	mv := m.travel(tx)
	s.jumping = false
	m.checkInsiders(tx)
	if mv != nil {
		mv.Send()
	}
	m.data.Age += time.Second / 20
}

// sense updates what the mob knows about where it is.
func (m *Mob) sense() {
	s := m.s
	feet := cube.PosFromVec3(m.data.Pos)
	s.inWater, s.inLava = false, false
	if l, ok := m.tx.Liquid(feet); ok {
		switch l.(type) {
		case block.Water:
			s.inWater = true
		case block.Lava:
			s.inLava = true
		}
	}
	if s.inWater && m.data.FireDuration > 0 {
		m.Extinguish()
	}
}

// burn handles fire on the mob and burning in daylight.
func (m *Mob) burn(current int64) {
	s := m.s
	if m.data.FireDuration > 0 {
		m.data.FireDuration -= time.Second / 20
		if m.data.FireDuration <= 0 || m.tx.RainingAt(cube.PosFromVec3(m.data.Pos)) {
			m.Extinguish()
		} else if m.data.FireDuration%time.Second == 0 {
			m.Hurt(1, block.FireDamageSource{})
		}
	}
	if s.spec.burnsInDaylight && current%20 == 0 && !s.inWater && m.daylight() {
		m.SetOnFire(8 * time.Second)
	}
}

// daylight reports whether the mob stands in open sunlight.
func (m *Mob) daylight() bool {
	t := m.tx.World().Time() % 24000
	if t < 0 || t >= 12000 {
		return false
	}
	head := cube.PosFromVec3(m.eyePos())
	return m.tx.SkyLight(head) >= 15 && !m.tx.RainingAt(head)
}

// checkInsiders lets the blocks the mob is in act on it: lava, fire, cactus
// and so on.
func (m *Mob) checkInsiders(tx *world.Tx) {
	box := m.handle.Type().BBox(m).Translate(m.data.Pos).Grow(-0.0001)
	lo, hi := cube.PosFromVec3(box.Min()), cube.PosFromVec3(box.Max())
	for y := lo[1]; y <= hi[1]; y++ {
		for x := lo[0]; x <= hi[0]; x++ {
			for z := lo[2]; z <= hi[2]; z++ {
				pos := cube.Pos{x, y, z}
				b := tx.Block(pos)
				if in, ok := b.(block.EntityInsider); ok {
					in.EntityInside(pos, tx, m)
					if _, liquid := b.(world.Liquid); liquid {
						continue
					}
				}
				if l, ok := tx.Liquid(pos); ok {
					if in, ok := l.(block.EntityInsider); ok {
						in.EntityInside(pos, tx, m)
					}
				}
			}
		}
	}
}

// canSee reports whether nothing solid is between the mob's eyes and e's.
func (m *Mob) canSee(e world.Entity) bool {
	from, to := m.eyePos(), eyeOf(e)
	d := to.Sub(from)
	steps := int(math.Ceil(d.Len() * 4))
	for i := 1; i < steps; i++ {
		if path.Solid(m.tx, cube.PosFromVec3(from.Add(d.Mul(float64(i)/float64(steps))))) {
			return false
		}
	}
	return true
}

func eyeOf(e world.Entity) mgl64.Vec3 {
	if m, ok := e.(*Mob); ok {
		return m.eyePos()
	}
	return entity.EyePosition(e)
}

// attack hits e with the mob's attack damage.
func (m *Mob) attack(e world.Entity) {
	m.viewAction(entity.SwingArmAction{})
	lo, hi := m.s.spec.attack[0], m.s.spec.attack[1]
	dmg := lo
	if hi > lo {
		dmg = lo + float64(m.s.rand.IntN(int(hi-lo)+1))
	}
	if str, ok := m.Effect(effect.Strength); ok {
		dmg += dmg * effect.Strength.Multiplier(str.Level())
	}
	if l, ok := e.(entity.Living); ok {
		if _, vulnerable := l.Hurt(dmg, entity.AttackDamageSource{Attacker: m}); vulnerable {
			l.KnockBack(m.data.Pos, 0.4, 0.4)
		}
	}
}
