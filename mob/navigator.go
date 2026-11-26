package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/nav"
	"github.com/helix-bedrock/entity/path"
)

// navigator is the goals' Navigator over the mob's navigation component.
type navigator struct{ m *Mob }

// pathfinder searches paths for one mob with its settings.
type pathfinder struct{ m *Mob }

// settingsTo are the pathfinder's settings for a search toward target, as BDS gathers them.
func (p pathfinder) settingsTo(target path.Vec3) *path.Settings {
	s := p.settings()
	p.m.homeRestriction(s, target)
	return s
}

// settings are the pathfinder's settings without a target, which BDS gives its node checks:
// no home restriction then.
func (p pathfinder) settings() *path.Settings {
	m := p.m
	return &path.Settings{
		Box: m.body.Box, Width: m.body.Width, Height: m.body.Height, Pos: m.body.Pos,
		MinY: float32(pathWorld{&m.v}.MinY()), StepHeight: max(1.2, m.body.MaxAutoStep),
		Swim: m.flags.Has(nav.ActorCanSwim), Fly: m.flags.Has(nav.ActorCanFly), Walk: m.flags.Has(nav.ActorCanWalk),
		InWater: m.body.WasInWater, MobGriefing: m.rt.Env.MobGriefing(),
		DoorOpener: m.flags.Has(behavior.FlagDoorOpener), DoorBreaker: m.flags.Has(behavior.FlagDoorBreaker),
		// BDS's lava walker test is the actor's fire immunity.
		LavaWalker:             m.flags.Has(behavior.FlagFireImmune),
		SpecialStartNeighbours: m.typ == typeBee || m.typ == typeAllay,
		Path:                   m.prefPath,
		BreakBlocks:            m.breakable != nil, Breakable: m.breakable,
		Avoid: m.avoid,
		// BDS binds the climb check to either powder snow component; the equipment one is
		// what leather boots give.
		ClimbPowderSnow: m.states != nil && m.states.CanStandOnPowderSnow || m.Equipment(EquipFeet).Name == leatherBoots,
	}
}

const leatherBoots = "minecraft:leather_boots"

// homeRestriction matches BDS: a mob whose home restricts all movement keeps its search
// within the radius around the home when the target is farther from the home's centre
// than the mob is (the mob by its box's centre and bottom).
func (m *Mob) homeRestriction(s *path.Settings, target path.Vec3) {
	h := m.homeState()
	if h == nil {
		return
	}
	home, dim, ok := h.Get()
	cfg := h.Config()
	if !ok || dim != m.Dimension() || cfg.RestrictionType != homeAllMovement {
		return
	}
	cx, cy, cz := float32(home.X)+0.5, float32(home.Y), float32(home.Z)+0.5
	b := m.body.Box
	mx, mz := (b.Max.X-b.Min.X)*0.5+b.Min.X, (b.Max.Z-b.Min.Z)*0.5+b.Min.Z
	dist2 := func(x, y, z float32) float32 { return (cx-x)*(cx-x) + (cy-y)*(cy-y) + (cz-z)*(cz-z) }
	if dist2(target.X, target.Y, target.Z) <= dist2(mx, b.Min.Y, mz) {
		return
	}
	s.Restricted, s.Home, s.Radius2 = true, home, float32(cfg.RestrictionRadius*cfg.RestrictionRadius)
}

// The two actor types BDS's pathfinder starts with six neighbours for.
const (
	typeBee   = "minecraft:bee"
	typeAllay = "minecraft:allay"
)

// readPathSettings builds the pathfinder's preferred_path and break_blocks from the mob's
// state components, once per rebuild so searches allocate nothing for them.
func (m *Mob) readPathSettings() {
	m.prefPath, m.breakable = nil, nil
	if m.states == nil {
		return
	}
	if c := m.states.PreferredPath; c != nil {
		m.prefPath = c.Path(m.rt.Blocks.nameMatcher)
	}
	if c := m.states.BreakBlocks; c != nil {
		m.breakable = m.rt.Blocks.nameMatcher(c.Blocks)
	}
}

func (p pathfinder) flags() path.Flags {
	if p.m.nv == nil {
		return 0
	}
	return p.m.nv.Flags.PathFlags()
}

func (p pathfinder) PathToBlock(at path.Pos) *path.Path {
	// BDS's target for a block is its centre, just above its floor.
	target := path.Vec3{X: float32(at.X) + 0.5, Y: float32(at.Y) + 0.05, Z: float32(at.Z) + 0.5}
	return p.m.finder.FindPathToBlock(pathWorld{&p.m.v}, p.settingsTo(target), p.flags(), at, p.m.followRange)
}

func (p pathfinder) PathToMob(pos path.Vec3) *path.Path {
	return p.m.finder.FindPath(pathWorld{&p.m.v}, p.settingsTo(pos), p.flags(), pos, p.m.followRange)
}

func (n *navigator) MoveTo(p behavior.Vec3, speed float32) bool {
	m := n.m
	nm := m.navMob()
	return m.nv.MoveTo(pathWorld{&m.v}, &nm, pathfinder{m}, p, speed)
}

func (n *navigator) MoveToActor(a behavior.Actor, speed float32) bool {
	m := n.m
	nm := m.navMob()
	return m.nv.MoveToMob(pathWorld{&m.v}, &nm, pathfinder{m}, a.Pos(), speed)
}

func (n *navigator) PathTo(a behavior.Actor) (behavior.Pos, bool) {
	m := n.m
	nm := m.navMob()
	return lastNode(m.nv.CreatePathToMob(&nm, pathfinder{m}, a.Pos()))
}

// PathToStatus matches BDS's path test (1.26.60):
// any path made, and whether it reaches the entity.
func (n *navigator) PathToStatus(a behavior.Actor) (ok, complete bool) {
	m := n.m
	nm := m.navMob()
	p := m.nv.CreatePathToMob(&nm, pathfinder{m}, a.Pos())
	return p != nil, p != nil && p.Status == path.StatusComplete
}

func (n *navigator) CanPathTo(p behavior.Vec3) bool {
	_, ok := n.PathToPoint(p)
	return ok
}

func (n *navigator) PathToPoint(p behavior.Vec3) (behavior.Pos, bool) {
	m := n.m
	nm := m.navMob()
	return lastNode(m.nv.CreatePath(&nm, pathfinder{m}, path.Pos{X: floor32(p.X), Y: floor32(p.Y), Z: floor32(p.Z)}))
}

func lastNode(p *path.Path) (behavior.Pos, bool) {
	if p == nil || len(p.Points) == 0 {
		return behavior.Pos{}, false
	}
	return p.Points[len(p.Points)-1].Pos, true
}

func (n *navigator) PathEnd() (behavior.Pos, bool) {
	p, _ := n.m.nv.Path()
	return lastNode(p)
}

func (n *navigator) PathStart() (behavior.Pos, bool) {
	p, _ := n.m.nv.Path()
	if p == nil || len(p.Points) == 0 {
		return behavior.Pos{}, false
	}
	return p.Points[0].Pos, true
}

func (n *navigator) Held() bool                   { p, _ := n.m.nv.Path(); return p != nil }
func (n *navigator) SetEndOffset(o behavior.Vec3) { n.m.nv.EndOffset = o }
func (n *navigator) SetLastNodeReach(d2 float32)  { n.m.nv.SetLastNodeReach(d2) }
func (n *navigator) Done() bool                   { return n.m.nv.Done() }

func (n *navigator) Stop() {
	m := n.m
	nm := m.navMob()
	m.nv.Stop(&nm)
}

func (n *navigator) Flags() uint32     { return uint32(n.m.nv.Flags) }
func (n *navigator) SetFlags(f uint32) { n.m.nv.Flags = nav.Flags(f) }

// SetSpeed changes the speed of the path being followed: the next follow takes it.
func (n *navigator) SetSpeed(s float32) {
	m := n.m
	if p, _ := m.nv.Path(); p != nil {
		nm := m.navMob()
		m.nv.Follow(pathWorld{&m.v}, &nm, p, s)
	}
}
