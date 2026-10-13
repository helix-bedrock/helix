package path

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block/cube"
)

const (
	// minVisited is vanilla's lowest node budget for one search.
	minVisited = 560
	// targetWeight makes the search greedy towards the target, as vanilla.
	targetWeight = 1.5
)

// Find searches a path for mob from where it stands to dest, ending within
// reach blocks (manhattan) of it. Nodes further than followRange from the
// start are not searched. If dest cannot be reached, the path leads as
// close as the search got; nil is returned only if the mob has nowhere to
// start from.
func Find(src Source, mob *Mob, dest mgl64.Vec3, reach int, followRange float64) *Path {
	ctx := newContext(src, mob.MinY)
	w := newWalker(ctx, mob)
	start := w.start()
	if start == nil {
		return nil
	}
	targetPos := cube.PosFromVec3(dest)
	target := w.node(targetPos)

	best, bestH := start, start.dist(target)
	start.g, start.h, start.f, start.walked, start.cameFrom = 0, bestH, bestH, 0, nil

	open := heap{}
	open.push(start)
	maxVisited := max(minVisited, min(2048, max(100, int(followRange*16))))
	reached := false
	buf := make([]*Node, 0, 8)

	for visited := 0; len(open) > 0; visited++ {
		if visited >= maxVisited {
			break
		}
		cur := open.pop()
		cur.closed = true
		if cur.manhattan(targetPos) <= float64(reach) {
			best, reached = cur, true
			break
		}
		if cur.Pos.Vec3().Sub(start.Pos.Vec3()).Len() >= followRange {
			continue
		}
		buf = w.neighbours(cur, buf[:0])
		for _, n := range buf {
			step := cur.dist(n)
			walked := cur.walked + step
			g := cur.g + step + n.Malus
			inOpen := n.heapIdx >= 0
			if walked >= followRange || (inOpen && g >= n.g) {
				continue
			}
			n.walked, n.cameFrom, n.g = walked, cur, g
			d := n.dist(target)
			if d < bestH {
				best, bestH = n, d
			}
			n.h = d * targetWeight
			n.f = n.g + n.h
			if inOpen {
				open.update(n)
			} else {
				open.push(n)
			}
		}
	}

	var nodes []*Node
	seen := map[*Node]bool{}
	for n := best; n != nil && !seen[n]; n = n.cameFrom {
		seen[n] = true
		nodes = append(nodes, n)
	}
	for i, j := 0, len(nodes)-1; i < j; i, j = i+1, j-1 {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	}
	return newPath(nodes, targetPos, reached)
}

// Surface returns the first position at or above pos that a mob can stand
// in, or the ground below an air position, as vanilla does before
// searching to a target.
func Surface(src Source, pos cube.Pos, minY, maxY int) cube.Pos {
	src = guard(src)
	if !Solid(src, pos) {
		p := pos
		for p[1] >= minY && !Solid(src, p) {
			p[1]--
		}
		if p[1] >= minY {
			return p.Side(cube.FaceUp)
		}
		return pos
	}
	for pos[1] <= maxY && Solid(src, pos) {
		pos[1]++
	}
	return pos
}

func centre(p cube.Pos) mgl64.Vec3 {
	return mgl64.Vec3{float64(p[0]) + 0.5, float64(p[1]), float64(p[2]) + 0.5}
}

func wrapDegrees(d float64) float64 {
	d = math.Mod(d, 360)
	if d >= 180 {
		d -= 360
	} else if d < -180 {
		d += 360
	}
	return d
}
