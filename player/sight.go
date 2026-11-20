package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Line of sight: Helix's own rule, beyond BDS (BDS lets a client use blocks and hit entities
// through walls, which cheat clients do). A block or entity counts as seen when a straight line
// from the eyes reaches one of a few points on it without crossing a block whose collision is
// the whole cube. Only full cubes block, so fences, slabs, panes, plants and open doors never
// refuse a click a vanilla client can make.

const (
	// sightInset keeps the points looked at inside the target, off its edges.
	sightInset = 0.05
	// sightMaxSteps bounds the blocks one line walks; reach is far below it.
	sightMaxSteps = 64
)

var fullCube = registry.Box{0, 0, 0, 1, 1, 1}

// seesBlock reports whether the player can see the block at pos: the clicked point, the
// centre or one of its corners.
func (p *Player) seesBlock(r *world.Region, pos cube.Pos, clicked mgl32.Vec3) bool {
	base := mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])}
	eyes := p.entity.Position()
	if p.lineClear(r, eyes, base.Add(insetPoint(clicked)), pos) {
		return true
	}
	return p.seesBox(r, eyes, base, base.Add(mgl32.Vec3{1, 1, 1}), pos)
}

// seesEntity reports whether the player can see an entity's box.
func (p *Player) seesEntity(r *world.Region, lo, hi mgl32.Vec3) bool {
	return p.seesBox(r, p.entity.Position(), lo, hi, cube.Pos{math.MinInt32, 0, 0})
}

// seesEntityID reports whether the player can see the entity with runtime ID id, and for an
// attack whether it looked at it (aim.go). An entity not found nearby, or without a box, is left
// to the handlers that look it up.
func (p *Player) seesEntityID(id uint64, attack bool) bool {
	r := p.entity.Region()
	if r == nil {
		return false
	}
	pos := p.entity.Position()
	p.touching = r.AppendEntitiesIn(p.touching[:0], pos[0]-attackScan, pos[2]-attackScan, pos[0]+attackScan, pos[2]+attackScan)
	for _, e := range p.touching {
		if e.RuntimeID() != id {
			continue
		}
		b, ok := e.Owner().(interface {
			BlastBox() (lo, hi mgl32.Vec3, ok bool)
		})
		if !ok {
			return true
		}
		lo, hi, ok := b.BlastBox()
		if !ok {
			return true
		}
		if attack {
			return p.attackFits(r, e, lo, hi)
		}
		return p.seesEntity(r, lo, hi)
	}
	return true
}

// seesBox tries the box's centre and its eight corners, pulled in from the edges.
func (p *Player) seesBox(r *world.Region, eyes, lo, hi mgl32.Vec3, target cube.Pos) bool {
	if p.lineClear(r, eyes, lo.Add(hi).Mul(0.5), target) {
		return true
	}
	in := mgl32.Vec3{sightInset, sightInset, sightInset}
	lo, hi = lo.Add(in), hi.Sub(in)
	for i := range 8 {
		corner := lo
		if i&1 != 0 {
			corner[0] = hi[0]
		}
		if i&2 != 0 {
			corner[1] = hi[1]
		}
		if i&4 != 0 {
			corner[2] = hi[2]
		}
		if p.lineClear(r, eyes, corner, target) {
			return true
		}
	}
	return false
}

// insetPoint keeps a clicked point inside its block.
func insetPoint(v mgl32.Vec3) mgl32.Vec3 {
	for i := range v {
		v[i] = min(max(v[i], sightInset), 1-sightInset)
	}
	return v
}

// lineClear walks the blocks a line from from to to crosses (a grid walk), skipping the block
// the eyes are in, and stops at the block holding to or at target. False if a full cube is in
// the way.
func (p *Player) lineClear(r *world.Region, from, to mgl32.Vec3, target cube.Pos) bool {
	cell := floorPos(from)
	end := floorPos(to)
	var step [3]int
	var tMax, tDelta [3]float64
	for i := range 3 {
		d := float64(to[i] - from[i])
		switch {
		case d > 0:
			step[i] = 1
			tDelta[i] = 1 / d
			tMax[i] = (float64(cell[i]+1) - float64(from[i])) / d
		case d < 0:
			step[i] = -1
			tDelta[i] = -1 / d
			tMax[i] = (float64(cell[i]) - float64(from[i])) / d
		default:
			tDelta[i], tMax[i] = math.Inf(1), math.Inf(1)
		}
	}
	for range sightMaxSteps {
		if cell == end {
			return true
		}
		axis := 0
		if tMax[1] < tMax[axis] {
			axis = 1
		}
		if tMax[2] < tMax[axis] {
			axis = 2
		}
		if tMax[axis] > 1 {
			return true
		}
		cell[axis] += step[axis]
		tMax[axis] += tDelta[axis]
		if cell == end || cell == target {
			return true
		}
		if p.fullCubeAt(r, cell) {
			return false
		}
	}
	return true
}

// fullCubeAt reports a block whose collision is the whole cube. Unloaded blocks do not block.
func (p *Player) fullCubeAt(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	c := p.s.reg.Blocks.Properties(b).Collision
	return len(c) == 1 && c[0] == fullCube
}
