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

func (p pathfinder) settings() *path.Settings {
	m := p.m
	return &path.Settings{
		Box: m.body.Box, Width: m.body.Width, Height: m.body.Height, Pos: m.body.Pos,
		MinY: float32(pathWorld{&m.v}.MinY()), StepHeight: max(1.2, m.body.MaxAutoStep),
		Swim: m.flags.Has(nav.ActorCanSwim), Fly: m.flags.Has(nav.ActorCanFly), Walk: m.flags.Has(nav.ActorCanWalk),
		InWater: m.body.WasInWater, MobGriefing: m.rt.Env.MobGriefing(),
	}
}

func (p pathfinder) flags() path.Flags {
	if p.m.nv == nil {
		return 0
	}
	return p.m.nv.Flags.PathFlags()
}

func (p pathfinder) PathToBlock(at path.Pos) *path.Path {
	return p.m.finder.FindPathToBlock(pathWorld{&p.m.v}, p.settings(), p.flags(), at, p.m.followRange)
}

func (p pathfinder) PathToMob(pos path.Vec3) *path.Path {
	return p.m.finder.FindPath(pathWorld{&p.m.v}, p.settings(), p.flags(), pos, p.m.followRange)
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
