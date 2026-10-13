package mob

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/item/potion"
	"github.com/helix-bedrock/helix/server/world"
)

func newGoal2(b behaviourSpec) Goal {
	r := b.raw
	switch b.name {
	case "swell":
		return &swellGoal{start: numberField(r, "start_distance", 2.5), stop: numberField(r, "stop_distance", 6)}
	case "ranged_attack":
		lo, hi := 1.0, 3.0
		if f := field(r, "attack_interval"); f != nil {
			lo, hi, _ = number(f)
		} else if f := field(r, "attack_interval_min"); f != nil {
			lo = numberField(r, "attack_interval_min", 1)
			hi = numberField(r, "attack_interval_max", 3)
		}
		rng := 15.0
		if f := field(r, "attack_range"); f != nil {
			if _, h, ok := number(f); ok {
				rng = h
			} else {
				rng = numberField(r, "attack_radius", 15)
			}
		}
		return &rangedGoal{speed: numberField(r, "speed_multiplier", 1), minTicks: int(lo * 20), maxTicks: int(hi * 20), radius: rng}
	case "breed":
		return &breedGoal{speed: numberField(r, "speed_multiplier", 1)}
	case "follow_parent":
		return &followParentGoal{speed: numberField(r, "speed_multiplier", 1)}
	}
	return newDirectGoal(b)
}

// swellGoal holds a creeper still while it is about to explode, as
// vanilla's SwellGoal.
type swellGoal struct{ start, stop float64 }

func (*swellGoal) Controls() Control { return Move }
func (g *swellGoal) CanStart(m *Mob) bool {
	if m.s.fuse >= 0 {
		return true
	}
	t, ok := m.Target()
	return ok && t.Position().Sub(m.data.Pos).Len() < g.start
}
func (g *swellGoal) CanContinue(m *Mob) bool {
	if m.s.fuse >= 0 {
		return true
	}
	t, ok := m.Target()
	return ok && t.Position().Sub(m.data.Pos).Len() < g.stop
}
func (*swellGoal) Start(m *Mob) { m.s.nav.Stop() }
func (*swellGoal) Stop(*Mob)    {}
func (*swellGoal) Tick(m *Mob) {
	if t, ok := m.Target(); ok {
		m.s.look.at(eyeOf(t), 30, 30)
	}
}

// rangedGoal shoots at the target from a distance: Pumpkin's
// RangedAttackGoal (vanilla's RangedBowAttackGoal without strafing).
type rangedGoal struct {
	speed              float64
	minTicks, maxTicks int
	radius             float64
	cooldown, seen     int
}

func (*rangedGoal) Controls() Control { return Move | Look }
func (g *rangedGoal) CanStart(m *Mob) bool {
	t, ok := m.Target()
	return ok && attackable(t) && m.s.spec.shooter != ""
}
func (g *rangedGoal) CanContinue(m *Mob) bool { return g.CanStart(m) }
func (g *rangedGoal) Start(*Mob)              { g.cooldown, g.seen = -1, 0 }
func (*rangedGoal) Stop(m *Mob)               { m.s.nav.Stop() }
func (g *rangedGoal) Tick(m *Mob) {
	t, ok := m.Target()
	if !ok {
		return
	}
	dist := t.Position().Sub(m.data.Pos).Len()
	sees := m.canSee(t)
	if sees {
		g.seen++
	} else {
		g.seen = 0
	}
	// Close enough with a clear shot: stand and shoot, else walk closer.
	if dist <= g.radius*0.75 && g.seen >= 5 {
		m.s.nav.Stop()
	} else {
		m.s.nav.MoveTo(t.Position(), g.speed)
	}
	m.s.look.at(eyeOf(t), 30, 30)
	if g.cooldown--; g.cooldown <= 0 && sees && dist <= g.radius {
		m.shoot(t)
		g.cooldown = g.minTicks
		if g.maxTicks > g.minTicks {
			g.cooldown += m.s.rand.IntN(g.maxTicks - g.minTicks + 1)
		}
	} else if g.cooldown < 0 && !sees {
		g.cooldown = g.minTicks
	}
}

// shoot fires the mob's projectile at t, as vanilla's skeleton: aimed at a
// third of t's height, raised to make up for gravity.
func (m *Mob) shoot(t world.Entity) {
	if m.s.spec.shooter != "minecraft:arrow" {
		// Other projectiles (fireballs, tridents) are not ported yet.
		return
	}
	from := m.eyePos().Sub(mgl64.Vec3{0, 0.1})
	to := t.Position()
	bb := t.H().Type().BBox(t)
	to[1] += bb.Height() / 3
	d := to.Sub(from)
	h := math.Hypot(d[0], d[2])
	dir := mgl64.Vec3{d[0], d[1] + h*0.2, d[2]}
	if dir.Len() < 1e-4 {
		return
	}
	// Vanilla's spread shrinks with difficulty: 14 - difficulty*4.
	diff, _ := world.DifficultyID(m.tx.World().Difficulty())
	spread := float64(14-diff*4) * 0.0075
	r := m.s.rand
	dir = dir.Normalize().Add(mgl64.Vec3{r.NormFloat64() * spread, r.NormFloat64() * spread, r.NormFloat64() * spread})
	create := m.tx.World().EntityRegistry().Config().Arrow
	m.tx.AddEntity(create(world.EntitySpawnOpts{Position: from, Velocity: dir.Mul(1.6)}, world.ArrowSpawnConfig{
		Damage: 1, Owner: m, DisablePickup: true, Tip: potion.Potion{},
	}))
	m.viewAction(entity.SwingArmAction{})
}

// breedGoal walks to a partner in love and makes a baby: Pumpkin's
// BreedGoal.
type breedGoal struct {
	speed   float64
	partner *world.EntityHandle
	ticks   int
}

func (*breedGoal) Controls() Control { return Move | Look }
func (g *breedGoal) CanStart(m *Mob) bool {
	b := m.s.spec.breed
	if b == nil || !m.InLove() {
		return false
	}
	p := nearest(m, 8, func(e world.Entity) bool {
		o, ok := e.(*Mob)
		_, mate := b.mates[e.H().Type().EncodeEntity()]
		return ok && mate && o.InLove()
	})
	if p == nil {
		return false
	}
	g.partner = p.H()
	return true
}
func (g *breedGoal) CanContinue(m *Mob) bool {
	p, ok := g.partner.Entity(m.tx)
	return ok && m.InLove() && p.(*Mob).InLove() && g.ticks < 60
}
func (g *breedGoal) Start(*Mob) { g.ticks = 0 }
func (g *breedGoal) Stop(m *Mob) {
	g.partner = nil
	m.s.nav.Stop()
}
func (g *breedGoal) Tick(m *Mob) {
	pe, ok := g.partner.Entity(m.tx)
	if !ok {
		return
	}
	p := pe.(*Mob)
	m.s.look.at(p.eyePos(), 10, 40)
	m.s.nav.MoveTo(p.Position(), g.speed)
	g.ticks++
	if g.ticks >= 60 && p.Position().Sub(m.data.Pos).LenSqr() < 9 {
		m.breed(p)
	}
}

// breed makes a baby with partner and ends both mobs' love.
func (m *Mob) breed(partner *Mob) {
	babyType := m.s.spec.breed.mates[partner.Def().ID]
	t, ok := m.tx.World().EntityRegistry().Lookup(babyType)
	if bt, isMob := t.(*Type); ok && isMob {
		m.tx.AddEntity(bt.spawnWith(m.data.Pos, m.data.Rot.Yaw(), "minecraft:entity_born", natural{}))
	}
	for _, p := range []*Mob{m, partner} {
		p.s.love, p.s.breedCooldown = 0, 6000
	}
	// Vanilla gives 1 to 7 experience for breeding; experience orbs are not
	// dropped yet.
}

// followParentGoal keeps a baby near an adult of its kind: Pumpkin's
// FollowParentGoal.
type followParentGoal struct {
	speed  float64
	parent *world.EntityHandle
	delay  int
}

func (*followParentGoal) Controls() Control { return Move }
func (g *followParentGoal) CanStart(m *Mob) bool {
	if !m.Baby() {
		return false
	}
	p := nearest(m, 8, func(e world.Entity) bool {
		o, ok := e.(*Mob)
		return ok && o.s.t == m.s.t && !o.Baby()
	})
	if p == nil || p.Position().Sub(m.data.Pos).LenSqr() < 9 {
		return false
	}
	g.parent = p.H()
	return true
}
func (g *followParentGoal) CanContinue(m *Mob) bool {
	p, ok := g.parent.Entity(m.tx)
	if !ok || !m.Baby() {
		return false
	}
	d := p.Position().Sub(m.data.Pos).LenSqr()
	return d >= 9 && d <= 256
}
func (g *followParentGoal) Start(*Mob) { g.delay = 0 }
func (g *followParentGoal) Stop(m *Mob) {
	g.parent = nil
	m.s.nav.Stop()
}
func (g *followParentGoal) Tick(m *Mob) {
	if g.delay--; g.delay > 0 {
		return
	}
	g.delay = 10
	if p, ok := g.parent.Entity(m.tx); ok {
		m.s.nav.MoveTo(p.Position(), g.speed)
	}
}
