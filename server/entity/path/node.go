// Package path finds paths for mobs through a world and walks them along
// those paths (Helix).
//
// Ported from Pumpkin (github.com/Pumpkin-MC/Pumpkin, GPL-3.0),
// crates/pumpkin-core/src/entity/ai/pathfinder, which follows vanilla's
// pathfinder. Nodes are pointers here, as in vanilla, rather than Pumpkin's
// copies.
//
// Everything in this package runs inside one transaction, on the region of
// the mob it finds a path for.
package path

import (
	"math"

	"github.com/helix-bedrock/helix/server/block/cube"
)

// Type is what a block position is like for a walking mob.
type Type uint8

// The path types, as vanilla names them.
const (
	Blocked Type = iota
	Open
	Walkable
	WalkableDoor
	Trapdoor
	PowderSnow
	DangerPowderSnow
	Fence
	Lava
	Water
	WaterBorder
	Rail
	UnpassableRail
	DangerFire
	DamageFire
	DangerOther
	DamageOther
	DoorOpen
	DoorWoodClosed
	DoorIronClosed
	Breach
	Leaves
	StickyHoney
	Cocoa
	DamageCautious
	DangerTrapdoor
	BigMobsCloseToDanger
	typeCount
)

// Malus is the default extra cost of stepping on a position of the type. A
// negative malus means mobs cannot go there.
func (t Type) Malus() float64 {
	switch t {
	case Blocked, PowderSnow, Fence, Lava, UnpassableRail, DamageOther, DoorWoodClosed, DoorIronClosed, Leaves:
		return -1
	case Breach, BigMobsCloseToDanger:
		return 4
	case Water, WaterBorder, DangerFire, DangerOther, StickyHoney:
		return 8
	case DamageFire:
		return 16
	}
	return 0
}

func partialCollision(t Type) bool {
	return t == Fence || t == DoorWoodClosed || t == DoorIronClosed
}

// Node is a position on a path.
type Node struct {
	Pos cube.Pos
	// Type is the path type of the position, Malus its cost for the mob.
	Type  Type
	Malus float64

	g, h, f  float64
	walked   float64
	heapIdx  int
	cameFrom *Node
	closed   bool
}

func (n *Node) dist(o *Node) float64 {
	dx, dy, dz := float64(o.Pos[0]-n.Pos[0]), float64(o.Pos[1]-n.Pos[1]), float64(o.Pos[2]-n.Pos[2])
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func (n *Node) manhattan(o cube.Pos) float64 {
	return math.Abs(float64(o[0]-n.Pos[0])) + math.Abs(float64(o[1]-n.Pos[1])) + math.Abs(float64(o[2]-n.Pos[2]))
}

// heap is the open set of the search, ordered by f.
type heap []*Node

func (h *heap) push(n *Node) {
	n.heapIdx = len(*h)
	*h = append(*h, n)
	h.up(n.heapIdx)
}

func (h *heap) pop() *Node {
	old := *h
	n := old[0]
	last := len(old) - 1
	old[0], old[last] = old[last], nil
	*h = old[:last]
	if last > 0 {
		(*h)[0].heapIdx = 0
		h.down(0)
	}
	n.heapIdx = -1
	return n
}

// update restores order after n's f dropped.
func (h *heap) update(n *Node) { h.up(n.heapIdx) }

func (h heap) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if h[p].f <= h[i].f {
			return
		}
		h.swap(i, p)
		i = p
	}
}

func (h heap) down(i int) {
	for {
		l, s := 2*i+1, i
		if l < len(h) && h[l].f < h[s].f {
			s = l
		}
		if r := l + 1; r < len(h) && h[r].f < h[s].f {
			s = r
		}
		if s == i {
			return
		}
		h.swap(i, s)
		i = s
	}
}

func (h heap) swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx, h[j].heapIdx = i, j
}

// Path is a list of nodes a mob walks along.
type Path struct {
	nodes   []*Node
	next    int
	target  cube.Pos
	dist    float64
	reached bool
}

func newPath(nodes []*Node, target cube.Pos, reached bool) *Path {
	p := &Path{nodes: nodes, target: target, reached: reached, dist: math.MaxFloat64}
	if len(nodes) > 0 {
		p.dist = nodes[len(nodes)-1].manhattan(target)
	}
	return p
}

// Done reports whether the mob walked the whole path.
func (p *Path) Done() bool { return p.next >= len(p.nodes) }

// Reaches reports whether the path ends at its target, not just as close
// as the search got.
func (p *Path) Reaches() bool { return p.reached }

// Target is the position the path was searched for.
func (p *Path) Target() cube.Pos { return p.target }

// DistToTarget is the manhattan distance from the end of the path to its
// target.
func (p *Path) DistToTarget() float64 { return p.dist }

// Nodes returns the nodes of the path.
func (p *Path) Nodes() []*Node { return p.nodes }

// Next returns the node the mob walks to now, or nil when done.
func (p *Path) Next() *Node {
	if p.next < len(p.nodes) {
		return p.nodes[p.next]
	}
	return nil
}

func (p *Path) advance() { p.next++ }

func (p *Path) remaining() float64 {
	var d float64
	for i := p.next + 1; i < len(p.nodes); i++ {
		d += p.nodes[i-1].dist(p.nodes[i])
	}
	return d
}

func (p *Path) truncate(i int) {
	if i < len(p.nodes) {
		p.nodes = p.nodes[:i]
		p.next = min(p.next, len(p.nodes))
	}
}
