package path

import (
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block/cube"
)

// Mob describes the mob a path is searched for.
type Mob struct {
	// Position is where the mob stands, at its feet.
	Position mgl64.Vec3
	// Width and Height are the mob's size.
	Width, Height float64
	// OnGround and InWater describe where the mob is now.
	OnGround, InWater bool
	// MaxFall is how far down the mob will step without a way back up.
	MaxFall int
	// MinY is the world's lowest block.
	MinY int

	// CanFloat lets the mob swim across water. CanOpenDoors and
	// CanPassDoors let it through wooden doors. CanWalkOverFences lets it
	// jump fences.
	CanFloat, CanOpenDoors, CanPassDoors, CanWalkOverFences bool

	malus [typeCount]float64
	set   [typeCount]bool
}

// SetMalus overrides the cost of a path type for this mob. A negative
// malus forbids it.
func (m *Mob) SetMalus(t Type, malus float64) {
	m.malus[t], m.set[t] = malus, true
}

// Malus returns the cost of a path type for this mob.
func (m *Mob) Malus(t Type) float64 {
	if m.set[t] {
		return m.malus[t]
	}
	return t.Malus()
}

// walker finds the positions a walking mob can step to from a node.
type walker struct {
	ctx   *context
	mob   *Mob
	nodes map[cube.Pos]*Node
	types map[cube.Pos]Type

	// width, height and depth are the mob's size in whole blocks.
	width, height int
}

func newWalker(ctx *context, mob *Mob) *walker {
	return &walker{
		ctx: ctx, mob: mob,
		nodes:  make(map[cube.Pos]*Node, 256),
		types:  make(map[cube.Pos]Type, 256),
		width:  int(math.Floor(mob.Width + 1)),
		height: int(math.Floor(mob.Height + 1)),
	}
}

func (w *walker) node(pos cube.Pos) *Node {
	if n, ok := w.nodes[pos]; ok {
		return n
	}
	n := &Node{Pos: pos, Type: Blocked, heapIdx: -1}
	w.nodes[pos] = n
	return n
}

func (w *walker) floorLevel(pos cube.Pos) float64 {
	if w.mob.CanFloat && w.ctx.isWater(pos) {
		return float64(pos[1]) + 0.5
	}
	return w.ctx.floorLevel(pos)
}

// jumpHeight is how high the mob can get up in one step: vanilla takes the
// larger of its step height (1 for pathfinding) and 1.125.
func (w *walker) jumpHeight() float64 { return 1.125 }

func (w *walker) start() *Node {
	m := w.mob
	bx, by, bz := int(math.Floor(m.Position[0])), int(math.Floor(m.Position[1])), int(math.Floor(m.Position[2]))
	startY := by
	switch {
	case m.CanFloat && m.InWater:
		for w.ctx.isWater(cube.Pos{bx, startY + 1, bz}) {
			startY++
		}
	case m.OnGround:
		startY = int(math.Floor(m.Position[1] + 0.5))
	default:
		// Falling: start from the ground below.
		y := int(math.Floor(m.Position[1] + 1))
		startY = y
		for y > m.MinY {
			startY = y
			y--
			below := cube.Pos{bx, y, bz}
			if Solid(w.ctx.src, below) {
				break
			}
		}
	}
	pos := cube.Pos{bx, startY, bz}
	if !w.canStartAt(pos) {
		half := m.Width / 2
		x0, x1 := int(math.Floor(m.Position[0]-half)), int(math.Floor(m.Position[0]+half))
		z0, z1 := int(math.Floor(m.Position[2]-half)), int(math.Floor(m.Position[2]+half))
		for _, c := range []cube.Pos{{x0, startY, z0}, {x0, startY, z1}, {x1, startY, z0}, {x1, startY, z1}} {
			if w.canStartAt(c) {
				return w.startNode(c)
			}
		}
	}
	return w.startNode(pos)
}

func (w *walker) canStartAt(pos cube.Pos) bool {
	t := w.cachedType(pos)
	return t != Open && w.mob.Malus(t) >= 0
}

func (w *walker) startNode(pos cube.Pos) *Node {
	n := w.node(pos)
	n.Type = w.cachedType(pos)
	n.Malus = w.mob.Malus(n.Type)
	return n
}

// directions in vanilla's order, with the index of the neighbour clockwise.
var (
	straight = [4]struct {
		dx, dz int
	}{{0, 1}, {-1, 0}, {0, -1}, {1, 0}} // south, west, north, east
	diagonal = [4]struct {
		a, b   int
		dx, dz int
	}{{2, 3, 1, -1}, {0, 1, -1, 1}, {1, 2, -1, -1}, {3, 0, 1, 1}}
)

func (w *walker) neighbours(cur *Node, out []*Node) []*Node {
	above := w.cachedType(cur.Pos.Side(cube.FaceUp))
	here := w.cachedType(cur.Pos)
	jump := 0
	if w.mob.Malus(above) >= 0 && here != StickyHoney {
		jump = int(math.Floor(w.jumpHeight()))
	}
	height := w.floorLevel(cur.Pos)

	var side [4]*Node
	for i, d := range straight {
		n := w.accepted(cur.Pos[0]+d.dx, cur.Pos[1], cur.Pos[2]+d.dz, jump, height, d.dx, d.dz, here)
		side[i] = n
		if n != nil && !n.closed && (n.Malus >= 0 || cur.Malus < 0) {
			out = append(out, n)
		}
	}
	for _, d := range diagonal {
		if !w.diagonalValid(cur, side[d.a], side[d.b]) {
			continue
		}
		n := w.accepted(cur.Pos[0]+d.dx, cur.Pos[1], cur.Pos[2]+d.dz, jump, height, d.dx, d.dz, here)
		if n != nil && !n.closed && n.Type != WalkableDoor && n.Malus >= 0 {
			out = append(out, n)
		}
	}
	return out
}

func (w *walker) diagonalValid(cur, ew, ns *Node) bool {
	if ew == nil || ns == nil || ns.Pos[1] > cur.Pos[1] || ew.Pos[1] > cur.Pos[1] {
		return false
	}
	if ew.Type == WalkableDoor || ns.Type == WalkableDoor {
		return false
	}
	if w.mob.Width > 1 && (ew.Malus > 0 || ns.Malus > 0) {
		return false
	}
	betweenPosts := ns.Type == Fence && ew.Type == Fence && w.mob.Width < 0.5
	return (ns.Pos[1] < cur.Pos[1] || ns.Malus >= 0 || betweenPosts) &&
		(ew.Pos[1] < cur.Pos[1] || ew.Malus >= 0 || betweenPosts)
}

// accepted returns the node a mob moving in direction (dx, dz) ends up on
// when it tries the column at x, z: stepping up, jumping or dropping down.
func (w *walker) accepted(x, y, z, jump int, nodeHeight float64, dx, dz int, current Type) *Node {
	pos := cube.Pos{x, y, z}
	if w.floorLevel(pos)-nodeHeight > w.jumpHeight() {
		return nil
	}
	t := w.cachedType(pos)
	cost := w.mob.Malus(t)
	var best *Node
	if cost >= 0 {
		best = w.costed(pos, t, cost)
	}
	if partialCollision(current) && best != nil && best.Malus >= 0 && !w.reachableWithoutCollision(pos) {
		best = nil
	}
	if t == Walkable {
		return best
	}
	switch {
	case (best == nil || best.Malus < 0) && jump > 0 && (t != Fence || w.mob.CanWalkOverFences) &&
		t != UnpassableRail && t != Trapdoor && t != PowderSnow:
		best = w.jumpOn(x, y, z, jump, nodeHeight, dx, dz, current)
	case t == Water && !w.mob.CanFloat:
		best = w.firstNonWaterBelow(x, y, z, best)
	case t == Open:
		best = w.firstGroundBelow(x, y, z)
	case partialCollision(t) && best == nil:
		best = w.node(pos)
		best.closed, best.Type, best.Malus = true, t, t.Malus()
	}
	return best
}

func (w *walker) jumpOn(x, y, z, jump int, nodeHeight float64, dx, dz int, current Type) *Node {
	up := w.accepted(x, y+1, z, jump-1, nodeHeight, dx, dz, current)
	if up == nil || w.mob.Width >= 1 || (up.Type != Open && up.Type != Walkable) {
		return up
	}
	// Check the mob fits through the gap while it jumps.
	cx, cz := float64(x-dx)+0.5, float64(z-dz)+0.5
	half := w.mob.Width / 2
	below := w.floorLevel(cube.Pos{int(math.Floor(cx)), y + 1, int(math.Floor(cz))})
	top := w.floorLevel(up.Pos)
	box := cube.Box(cx-half, below+0.001, cz-half, cx+half, w.mob.Height+top-0.002, cz+half)
	if w.ctx.collides(box) {
		return nil
	}
	return up
}

func (w *walker) firstNonWaterBelow(x, y, z int, best *Node) *Node {
	for y--; y > w.mob.MinY; y-- {
		t := w.cachedType(cube.Pos{x, y, z})
		if t != Water {
			return best
		}
		best = w.costed(cube.Pos{x, y, z}, t, w.mob.Malus(t))
	}
	return best
}

func (w *walker) firstGroundBelow(x, y, z int) *Node {
	for cy := y - 1; cy >= w.mob.MinY; cy-- {
		if y-cy > w.mob.MaxFall {
			return w.blocked(x, cy, z)
		}
		t := w.cachedType(cube.Pos{x, cy, z})
		if t != Open {
			if cost := w.mob.Malus(t); cost >= 0 {
				return w.costed(cube.Pos{x, cy, z}, t, cost)
			}
			return w.blocked(x, cy, z)
		}
	}
	return w.blocked(x, y, z)
}

func (w *walker) costed(pos cube.Pos, t Type, cost float64) *Node {
	n := w.node(pos)
	n.Type = t
	n.Malus = math.Max(n.Malus, cost)
	return n
}

func (w *walker) blocked(x, y, z int) *Node {
	n := w.node(cube.Pos{x, y, z})
	n.Type, n.Malus = Blocked, -1
	return n
}

func (w *walker) reachableWithoutCollision(to cube.Pos) bool {
	m := w.mob
	half := m.Width / 2
	bb := cube.Box(m.Position[0]-half, m.Position[1], m.Position[2]-half, m.Position[0]+half, m.Position[1]+m.Height, m.Position[2]+half)
	delta := mgl64.Vec3{
		float64(to[0]) - m.Position[0] + m.Width/2,
		float64(to[1]) - m.Position[1] + m.Height/2,
		float64(to[2]) - m.Position[2] + m.Width/2,
	}
	avg := (m.Width*2 + m.Height) / 3
	steps := int(math.Ceil(delta.Len() / avg))
	if steps <= 0 {
		return true
	}
	delta = delta.Mul(1 / float64(steps))
	for range steps {
		bb = bb.Translate(delta)
		if w.ctx.collides(bb) {
			return false
		}
	}
	return true
}

// cachedType is the path type of the whole space the mob takes up when
// standing at pos.
func (w *walker) cachedType(pos cube.Pos) Type {
	if t, ok := w.types[pos]; ok {
		return t
	}
	t := w.mobType(pos)
	w.types[pos] = t
	return t
}

func (w *walker) mobType(pos cube.Pos) Type {
	var types []Type
	for dx := range w.width {
		for dy := range w.height {
			for dz := range w.width {
				t := w.ctx.landType(pos.Add(cube.Pos{dx, dy, dz}))
				if t == DoorWoodClosed && w.mob.CanOpenDoors && w.mob.CanPassDoors {
					t = WalkableDoor
				}
				if t == DoorOpen && !w.mob.CanPassDoors {
					t = Blocked
				}
				if !slices.Contains(types, t) {
					types = append(types, t)
				}
			}
		}
	}
	if len(types) == 1 {
		return types[0]
	}
	// Ties go to the later type, as in vanilla's sorted set.
	slices.Sort(types)
	if slices.Contains(types, Fence) {
		return Fence
	}
	if slices.Contains(types, UnpassableRail) {
		return UnpassableRail
	}
	worst, worstMalus := Blocked, w.mob.Malus(Blocked)
	for _, t := range types {
		m := w.mob.Malus(t)
		if m < 0 {
			return t
		}
		if m >= worstMalus {
			worst, worstMalus = t, m
		}
	}
	here := w.ctx.landType(pos)
	if w.width > 1 {
		if w.mob.Malus(here) < worstMalus && w.mob.Malus(BigMobsCloseToDanger) < worstMalus {
			return BigMobsCloseToDanger
		}
		return worst
	}
	if here == Open && worst != Open && worstMalus == 0 {
		return Open
	}
	return worst
}

// TypeAt returns the path type of pos for mob: what it would cost the mob
// to stand there.
func TypeAt(src Source, mob *Mob, pos cube.Pos) Type {
	return newWalker(newContext(src, mob.MinY), mob).cachedType(pos)
}
