package mob

import (
	"encoding/json"
	"math"
	"slices"
	"strings"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/entity/path"
	"github.com/helix-bedrock/helix/server/item"
	"github.com/helix-bedrock/helix/server/world"
)

// buildGoals turns a mob's behaviour components into goals. Behaviours not
// ported yet are left out; the mob still does everything else.
func buildGoals(s *spec) (goals, targets selector) {
	for _, b := range s.behaviours {
		g, target := newGoal(b)
		if g == nil {
			continue
		}
		if target {
			targets.add(b.priority, g)
		} else {
			goals.add(b.priority, g)
		}
	}
	return goals, targets
}

func newGoal(b behaviourSpec) (g Goal, target bool) {
	r := b.raw
	speed := numberField(r, "speed_multiplier", 1)
	switch b.name {
	case "float":
		return &floatGoal{}, false
	case "random_stroll":
		return &strollGoal{speed: speed, interval: int(numberField(r, "interval", 120)),
			xz: int(numberField(r, "xz_dist", 10)), y: int(numberField(r, "y_dist", 7))}, false
	case "random_look_around":
		return &lookAroundGoal{}, false
	case "look_at_player":
		lo, hi := 2.0, 4.0
		if f := field(r, "look_time"); f != nil {
			lo, hi, _ = number(f)
		}
		return &lookAtPlayerGoal{dist: numberField(r, "look_distance", 8),
			chance: numberField(r, "probability", 0.02), minTicks: int(lo * 20), maxTicks: int(hi * 20)}, false
	case "panic":
		return &panicGoal{speed: numberField(r, "speed_multiplier", 1.25)}, false
	case "hurt_by_target":
		return &hurtByTargetGoal{alert: boolField(r, "alert_same_type", false)}, true
	case "nearest_attackable_target", "nearest_prioritized_attackable_target":
		return newNearestTarget(r), true
	case "melee_attack", "melee_box_attack":
		return &meleeGoal{speed: speed}, false
	case "tempt":
		var t struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(r, &t)
		var items []string
		for _, it := range t.Items {
			var name string
			if json.Unmarshal(it, &name) == nil {
				items = append(items, qualify(name))
				continue
			}
			var obj struct {
				Item string `json:"item"`
			}
			if json.Unmarshal(it, &obj) == nil && obj.Item != "" {
				items = append(items, qualify(obj.Item))
			}
		}
		return &temptGoal{items: items, speed: speed, radius: numberField(r, "within_radius", 10)}, false
	case "avoid_mob_type":
		return newAvoidGoal(r), false
	}
	return nil, false
}

func qualify(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return "minecraft:" + name
}

// floatGoal keeps a mob's head above water. Pumpkin's SwimGoal.
type floatGoal struct{}

func (*floatGoal) Controls() Control         { return Jump }
func (*floatGoal) CanStart(m *Mob) bool      { return m.s.inWater || m.s.inLava }
func (g *floatGoal) CanContinue(m *Mob) bool { return g.CanStart(m) }
func (*floatGoal) Start(*Mob)                {}
func (*floatGoal) Stop(*Mob)                 {}
func (*floatGoal) Tick(m *Mob) {
	if m.s.rand.Float64() < 0.8 {
		m.s.jumping = true
	}
}

// strollGoal walks a mob to a random place now and then. Pumpkin's
// WanderAroundGoal.
type strollGoal struct {
	speed    float64
	interval int
	xz, y    int
	target   mgl64.Vec3
}

func (*strollGoal) Controls() Control { return Move }
func (g *strollGoal) CanStart(m *Mob) bool {
	if g.interval > 0 && m.s.rand.IntN(g.interval) != 0 {
		return false
	}
	t, ok := randomPos(m, g.xz, g.y, nil)
	g.target = t
	return ok
}
func (*strollGoal) CanContinue(m *Mob) bool { return !m.s.nav.Idle() }
func (g *strollGoal) Start(m *Mob)          { m.s.nav.MoveTo(g.target, g.speed) }
func (*strollGoal) Stop(m *Mob)             { m.s.nav.Stop() }
func (*strollGoal) Tick(*Mob)               {}

// randomPos picks a place to walk to within xz blocks sideways and y up or
// down, the best of ten tries, as vanilla's DefaultRandomPos. If away is
// set, only places further from it count.
func randomPos(m *Mob, xz, y int, away *mgl64.Vec3) (mgl64.Vec3, bool) {
	r := m.s.rand
	pos := m.data.Pos
	rng := m.tx.Range()
	best, bestW, found := mgl64.Vec3{}, math.Inf(-1), false
	for range 10 {
		d := cube.Pos{r.IntN(2*xz+1) - xz, r.IntN(2*y+1) - y, r.IntN(2*xz+1) - xz}
		if away != nil {
			dir := pos.Sub(*away)
			if float64(d[0])*dir[0]+float64(d[2])*dir[2] < 0 {
				d[0], d[2] = -d[0], -d[2]
			}
		}
		p := cube.PosFromVec3(pos).Add(d)
		for p[1] < rng[1] && path.Solid(m.tx, p) {
			p[1]++
		}
		if p[1] <= rng[0] || p[1] >= rng[1] {
			continue
		}
		if !path.Solid(m.tx, p.Side(cube.FaceDown)) {
			continue
		}
		if path.TypeAt(m.tx, &m.s.nav.Mob, p).Malus() != 0 {
			continue
		}
		w := walkValue(m, p)
		if w > bestW {
			best, bestW, found = mgl64.Vec3{float64(p[0]) + 0.5, float64(p[1]), float64(p[2]) + 0.5}, w, true
		}
	}
	return best, found
}

// walkValue is how much a mob likes a place: animals prefer grass, as
// vanilla's Animal.getWalkTargetValue.
func walkValue(m *Mob, p cube.Pos) float64 {
	if m.s.spec.hasFamily("monster") {
		return 0
	}
	if _, ok := m.tx.Block(p.Side(cube.FaceDown)).(block.Grass); ok {
		return 10
	}
	return 0
}

// lookAroundGoal turns a mob's head to a random side for a while. Pumpkin's
// RandomLookAroundGoal.
type lookAroundGoal struct {
	dx, dz float64
	ticks  int
}

func (*lookAroundGoal) Controls() Control       { return Move | Look }
func (*lookAroundGoal) CanStart(m *Mob) bool    { return m.s.rand.Float64() < 0.02 }
func (g *lookAroundGoal) CanContinue(*Mob) bool { return g.ticks >= 0 }
func (*lookAroundGoal) Stop(*Mob)               {}
func (g *lookAroundGoal) Start(m *Mob) {
	a := 2 * math.Pi * m.s.rand.Float64()
	g.dx, g.dz, g.ticks = math.Cos(a), math.Sin(a), 20+m.s.rand.IntN(20)
}
func (g *lookAroundGoal) Tick(m *Mob) {
	g.ticks--
	eye := m.eyePos()
	m.s.look.at(mgl64.Vec3{eye[0] + g.dx, eye[1], eye[2] + g.dz}, 10, 40)
}

// lookAtPlayerGoal looks at a nearby player for a few seconds. Pumpkin's
// LookAtEntityGoal.
type lookAtPlayerGoal struct {
	dist, chance       float64
	minTicks, maxTicks int
	target             *world.EntityHandle
	ticks              int
}

func (*lookAtPlayerGoal) Controls() Control { return Look }
func (g *lookAtPlayerGoal) CanStart(m *Mob) bool {
	if m.s.rand.Float64() >= g.chance {
		return false
	}
	p := nearest(m, g.dist, func(e world.Entity) bool { return isPlayer(e) })
	if p == nil {
		return false
	}
	g.target = p.H()
	return true
}
func (g *lookAtPlayerGoal) CanContinue(m *Mob) bool {
	e, ok := g.target.Entity(m.tx)
	return ok && g.ticks > 0 && e.Position().Sub(m.data.Pos).Len() <= g.dist
}
func (g *lookAtPlayerGoal) Start(m *Mob) {
	g.ticks = g.minTicks
	if g.maxTicks > g.minTicks {
		g.ticks += m.s.rand.IntN(g.maxTicks - g.minTicks)
	}
}
func (g *lookAtPlayerGoal) Stop(*Mob) { g.target = nil }
func (g *lookAtPlayerGoal) Tick(m *Mob) {
	if e, ok := g.target.Entity(m.tx); ok {
		m.s.look.at(eyeOf(e), 10, 40)
	}
	g.ticks--
}

// panicGoal runs a hurt or burning mob to a random place. Pumpkin's
// EscapeDangerGoal.
type panicGoal struct {
	speed  float64
	target mgl64.Vec3
}

func (*panicGoal) Controls() Control { return Move }
func (g *panicGoal) CanStart(m *Mob) bool {
	hurt := m.s.hurtTick > 0 && m.s.ticks-m.s.hurtTick < 100
	if !hurt && m.data.FireDuration <= 0 {
		return false
	}
	t, ok := randomPos(m, 5, 4, nil)
	g.target = t
	return ok
}
func (*panicGoal) CanContinue(m *Mob) bool { return !m.s.nav.Idle() }
func (g *panicGoal) Start(m *Mob)          { m.s.nav.MoveTo(g.target, g.speed) }
func (*panicGoal) Stop(m *Mob)             { m.s.nav.Stop() }
func (*panicGoal) Tick(*Mob)               {}

// trackTarget is what target goals share to keep a target: Pumpkin's
// TrackTargetGoal.
type trackTarget struct {
	mustSee     bool
	unseen      int
	maxUnseen   int
	followRange float64
}

func (t *trackTarget) keep(m *Mob) bool {
	e, ok := m.Target()
	if !ok || !attackable(e) {
		return false
	}
	r := math.Max(t.followRange, m.s.spec.followRange)
	if e.Position().Sub(m.data.Pos).LenSqr() > r*r {
		return false
	}
	if t.mustSee {
		if m.canSee(e) {
			t.unseen = 0
		} else if t.unseen++; t.unseen > t.maxUnseen {
			return false
		}
	}
	return true
}

// hurtByTargetGoal targets whoever last hurt the mob. Pumpkin's RevengeGoal.
type hurtByTargetGoal struct {
	alert        bool
	lastAttacked int64
	track        trackTarget
}

func (*hurtByTargetGoal) Controls() Control { return Target }
func (g *hurtByTargetGoal) CanStart(m *Mob) bool {
	if m.s.attackedTick == g.lastAttacked || m.s.attacker == nil {
		return false
	}
	e, ok := m.s.attacker.Entity(m.tx)
	return ok && attackable(e)
}
func (g *hurtByTargetGoal) CanContinue(m *Mob) bool { return g.track.keep(m) }
func (g *hurtByTargetGoal) Start(m *Mob) {
	g.lastAttacked = m.s.attackedTick
	g.track = trackTarget{maxUnseen: 300}
	e, ok := m.s.attacker.Entity(m.tx)
	if !ok {
		return
	}
	m.SetTarget(e)
	if g.alert {
		for o := range m.tx.EntitiesWithin(cube.Box(-10, -10, -10, 10, 10, 10).Translate(m.data.Pos)) {
			if om, ok := o.(*Mob); ok && om != m && om.s.t == m.s.t && om.s.target == nil {
				om.SetTarget(e)
			}
		}
	}
}
func (*hurtByTargetGoal) Stop(m *Mob) { m.SetTarget(nil) }
func (*hurtByTargetGoal) Tick(*Mob)   {}

type targetType struct {
	filter  json.RawMessage
	maxDist float64
	mustSee bool
}

// nearestTargetGoal targets the nearest entity of the kinds the definition
// lists. Pumpkin's ActiveTargetGoal.
type nearestTargetGoal struct {
	types   []targetType
	mustSee bool
	forget  int
	track   trackTarget
	found   *world.EntityHandle
}

func newNearestTarget(r json.RawMessage) *nearestTargetGoal {
	g := &nearestTargetGoal{
		mustSee: boolField(r, "must_see", false),
		forget:  int(numberField(r, "must_see_forget_duration", 3) * 20),
	}
	radius := numberField(r, "within_radius", 0)
	var t struct {
		Types []struct {
			Filters json.RawMessage `json:"filters"`
			MaxDist *float64        `json:"max_dist"`
			MustSee *bool           `json:"must_see"`
		} `json:"entity_types"`
	}
	_ = json.Unmarshal(r, &t)
	for _, et := range t.Types {
		tt := targetType{filter: et.Filters, maxDist: radius, mustSee: g.mustSee}
		if et.MaxDist != nil {
			tt.maxDist = *et.MaxDist
		}
		if tt.maxDist == 0 {
			tt.maxDist = 16
		}
		if et.MustSee != nil {
			tt.mustSee = *et.MustSee
		}
		g.types = append(g.types, tt)
	}
	return g
}

func (*nearestTargetGoal) Controls() Control { return Target }
func (g *nearestTargetGoal) CanStart(m *Mob) bool {
	// Vanilla searches once every ten goal ticks on average.
	if m.s.rand.IntN(10) != 0 {
		return false
	}
	var best world.Entity
	bestDist := math.Inf(1)
	for _, t := range g.types {
		e := nearest(m, t.maxDist, func(e world.Entity) bool {
			return attackable(e) && evalFilter(t.filter, filterEnv{tx: m.tx, self: m, other: e}) && (!t.mustSee || m.canSee(e))
		})
		if e == nil {
			continue
		}
		if d := e.Position().Sub(m.data.Pos).LenSqr(); d < bestDist {
			best, bestDist = e, d
		}
	}
	if best == nil {
		return false
	}
	g.found = best.H()
	return true
}
func (g *nearestTargetGoal) CanContinue(m *Mob) bool { return g.track.keep(m) }
func (g *nearestTargetGoal) Start(m *Mob) {
	var r float64
	for _, t := range g.types {
		r = math.Max(r, t.maxDist)
	}
	g.track = trackTarget{mustSee: g.mustSee, maxUnseen: g.forget, followRange: r}
	if e, ok := g.found.Entity(m.tx); ok {
		m.SetTarget(e)
	}
}
func (*nearestTargetGoal) Stop(m *Mob) { m.SetTarget(nil) }
func (*nearestTargetGoal) Tick(*Mob)   {}

// meleeGoal chases the target and hits it. Pumpkin's MeleeAttackGoal.
type meleeGoal struct {
	speed    float64
	repath   int
	cooldown int
	last     mgl64.Vec3
}

func (*meleeGoal) Controls() Control { return Move | Look }
func (*meleeGoal) CanStart(m *Mob) bool {
	e, ok := m.Target()
	return ok && attackable(e)
}
func (g *meleeGoal) CanContinue(m *Mob) bool { return g.CanStart(m) }
func (g *meleeGoal) Start(m *Mob) {
	if e, ok := m.Target(); ok {
		g.last = e.Position()
		m.s.nav.MoveTo(g.last, g.speed)
	}
	g.repath, g.cooldown = 0, 0
}
func (*meleeGoal) Stop(m *Mob) { m.s.nav.Stop() }
func (g *meleeGoal) Tick(m *Mob) {
	e, ok := m.Target()
	if !ok {
		return
	}
	m.s.look.at(eyeOf(e), 30, 30)
	pos := e.Position()
	if g.repath = max(g.repath-1, 0); g.repath == 0 && (pos.Sub(g.last).LenSqr() >= 1 || m.s.rand.Float64() < 0.05) {
		m.s.nav.MoveTo(pos, g.speed)
		g.last = pos
		g.repath = 4 + m.s.rand.IntN(7)
		if d := pos.Sub(m.data.Pos).LenSqr(); d > 1024 {
			g.repath += 10
		} else if d > 256 {
			g.repath += 5
		}
	}
	if g.cooldown = max(g.cooldown-1, 0); g.cooldown == 0 && m.inReach(e) && m.canSee(e) {
		g.cooldown = 20
		m.attack(e)
	}
}

// inReach reports whether e is close enough to hit: vanilla grows the
// mob's box by about 0.8 blocks sideways.
func (m *Mob) inReach(e world.Entity) bool {
	own := m.handle.Type().BBox(m).Translate(m.data.Pos)
	reach := own.GrowVec3(mgl64.Vec3{0.8, 0, 0.8})
	other := e.H().Type().BBox(e).Translate(e.Position())
	return reach.IntersectsWith(other)
}

// temptGoal follows a player holding food the mob likes. Pumpkin's
// TemptGoal.
type temptGoal struct {
	items  []string
	speed  float64
	radius float64
	player *world.EntityHandle
}

func (*temptGoal) Controls() Control { return Move | Look }
func (g *temptGoal) CanStart(m *Mob) bool {
	p := nearest(m, g.radius, func(e world.Entity) bool { return isPlayer(e) && g.holds(e) })
	if p == nil {
		return false
	}
	g.player = p.H()
	return true
}
func (g *temptGoal) holds(e world.Entity) bool {
	h, ok := e.(interface {
		HeldItems() (item.Stack, item.Stack)
	})
	if !ok {
		return false
	}
	main, off := h.HeldItems()
	for _, s := range []item.Stack{main, off} {
		if s.Empty() {
			continue
		}
		name, _ := s.Item().EncodeItem()
		if slices.Contains(g.items, name) {
			return true
		}
	}
	return false
}
func (g *temptGoal) CanContinue(m *Mob) bool {
	e, ok := g.player.Entity(m.tx)
	return ok && g.holds(e) && e.Position().Sub(m.data.Pos).Len() <= g.radius
}
func (g *temptGoal) Start(*Mob) {}
func (g *temptGoal) Stop(m *Mob) {
	g.player = nil
	m.s.nav.Stop()
}
func (g *temptGoal) Tick(m *Mob) {
	e, ok := g.player.Entity(m.tx)
	if !ok {
		return
	}
	m.s.look.at(eyeOf(e), 30, 40)
	if e.Position().Sub(m.data.Pos).LenSqr() < 6.25 {
		m.s.nav.Stop()
	} else {
		m.s.nav.MoveTo(e.Position(), g.speed)
	}
}

type avoidType struct {
	filter            json.RawMessage
	maxDist           float64
	walkSpeed, sprint float64
}

// avoidGoal flees from mobs the definition lists. Pumpkin's AvoidEntityGoal.
type avoidGoal struct {
	types  []avoidType
	from   *world.EntityHandle
	chosen avoidType
	target mgl64.Vec3
}

func newAvoidGoal(r json.RawMessage) *avoidGoal {
	g := &avoidGoal{}
	var t struct {
		Types []struct {
			Filters json.RawMessage `json:"filters"`
			MaxDist *float64        `json:"max_dist"`
			Walk    *float64        `json:"walk_speed_multiplier"`
			Sprint  *float64        `json:"sprint_speed_multiplier"`
		} `json:"entity_types"`
	}
	_ = json.Unmarshal(r, &t)
	for _, et := range t.Types {
		a := avoidType{filter: et.Filters, maxDist: 3, walkSpeed: 1, sprint: 1}
		if et.MaxDist != nil {
			a.maxDist = *et.MaxDist
		}
		if et.Walk != nil {
			a.walkSpeed = *et.Walk
		}
		if et.Sprint != nil {
			a.sprint = *et.Sprint
		}
		g.types = append(g.types, a)
	}
	return g
}

func (*avoidGoal) Controls() Control { return Move }
func (g *avoidGoal) CanStart(m *Mob) bool {
	for _, t := range g.types {
		e := nearest(m, t.maxDist, func(e world.Entity) bool {
			return evalFilter(t.filter, filterEnv{tx: m.tx, self: m, other: e})
		})
		if e == nil {
			continue
		}
		from := e.Position()
		pos, ok := randomPos(m, 16, 7, &from)
		if !ok || pos.Sub(from).LenSqr() < m.data.Pos.Sub(from).LenSqr() {
			continue
		}
		g.from, g.chosen, g.target = e.H(), t, pos
		return true
	}
	return false
}
func (*avoidGoal) CanContinue(m *Mob) bool { return !m.s.nav.Idle() }
func (g *avoidGoal) Start(m *Mob)          { m.s.nav.MoveTo(g.target, g.chosen.walkSpeed) }
func (g *avoidGoal) Stop(m *Mob) {
	g.from = nil
	m.s.nav.Stop()
}
func (g *avoidGoal) Tick(m *Mob) {
	if e, ok := g.from.Entity(m.tx); ok && e.Position().Sub(m.data.Pos).LenSqr() < 49 {
		m.s.nav.SetSpeed(g.chosen.sprint)
	} else {
		m.s.nav.SetSpeed(g.chosen.walkSpeed)
	}
}

// nearest returns the nearest entity within r of the mob that ok accepts.
func nearest(m *Mob, r float64, ok func(world.Entity) bool) world.Entity {
	var best world.Entity
	bestDist := r * r
	box := cube.Box(-r, -r, -r, r, r, r).Translate(m.data.Pos)
	for e := range m.tx.EntitiesWithin(box) {
		if e.H() == m.handle {
			continue
		}
		d := e.Position().Sub(m.data.Pos).LenSqr()
		if d <= bestDist && ok(e) {
			best, bestDist = e, d
		}
	}
	return best
}

func isPlayer(e world.Entity) bool { return e.H().Type().EncodeEntity() == "minecraft:player" }

// attackable reports whether e is alive and may be hurt: players in
// creative or spectator mode are not.
func attackable(e world.Entity) bool {
	l, ok := e.(entity.Living)
	if !ok || l.Dead() {
		return false
	}
	if g, ok := e.(interface{ GameMode() world.GameMode }); ok && !g.GameMode().AllowsTakingDamage() {
		return false
	}
	return true
}
