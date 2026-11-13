package player

import (
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// BDS 1.26.60's search for a place to respawn around the world spawn: the respawn state
// machine (sub_BCED800, states 4 to 7) tries columns within the spawnradius game rule in a
// random order and checks each with sub_BCF19F0. A column is walked down through leaves to
// the ground under them; one that only offers leaves to stand on, or a spot with leaves
// under the feet, is kept as a fallback and used once the nearby columns all fail. With
// nothing found the player comes back at the world spawn itself.
//
// Not ported: BDS waits for the chunks of the search to load; Helix skips columns whose
// chunk the player's region does not have. BDS seeds its order from the clock, so no two
// respawns pick in the same order there either. The "solid blocking" check at the feet
// (Block +432: material solid and block property bit 18) is left to the collision fit,
// which raises the player out of the same blocks; the second, column-climbing search BDS
// runs for a rejected column is not ported.

// Numbers of the search, BDS's.
const (
	// spawnRadiusCap is the largest spawnradius BDS uses.
	spawnRadiusCap = 256
	// spawnInnerRadius is the most the inner grid reaches around one outer cell.
	spawnInnerRadius = 8
	// spawnOuterStep is how far apart the outer grid's cells are, in blocks.
	spawnOuterStep = 32
)

// noLeaf is the leaf top of a column without leaves.
const noLeaf = math.MinInt

// Materials the search looks at (BDS's MaterialType).
const (
	materialLeaves = 7
	materialPortal = 16
)

// unsafeRespawnBlocks are the blocks sub_BD051A0 refuses besides portals and lava: magma
// (block property bit 22), sweet berry bushes (bits 22 and 27) and wither roses (by name).
var unsafeRespawnBlocks = map[string]bool{
	"minecraft:magma": true, "minecraft:sweet_berry_bush": true, "minecraft:wither_rose": true,
}

// spawnGrid is BDS's sub_BCC9820: the cells of a square of radius r visited once each in a
// random order, by a prime step from a random start.
type spawnGrid struct {
	r, n, step, start, next uint32
}

// newSpawnGrid starts a grid of radius r. Its step is the largest prime no more than 2r
// (2 for small squares).
func newSpawnGrid(r uint32) spawnGrid {
	n := (2*r + 1) * (2*r + 1)
	return spawnGrid{r: r, n: n, step: largestPrimeUpTo(2 * r), start: rand.Uint32N(n)}
}

// largestPrimeUpTo is the step rule of sub_BCC9820.
func largestPrimeUpTo(v uint32) uint32 {
	for ; v > 2; v-- {
		if isPrime(v) {
			return v
		}
	}
	return 2
}

func isPrime(v uint32) bool {
	for d := uint32(2); d*d <= v; d++ {
		if v%d == 0 {
			return false
		}
	}
	return true
}

// nextWithin returns the grid's next cell, scaled by scale and moved by base, whose distance
// from the spawn is within radius. False once every cell was given.
func (g *spawnGrid) nextWithin(base [2]int32, scale, radius int32) ([2]int32, bool) {
	side := 2*g.r + 1
	for g.next < g.n {
		idx := (g.start + g.step*g.next) % g.n
		g.next++
		x := base[0] + (int32(idx%side)-int32(g.r))*scale //nolint:gosec // small grid
		z := base[1] + (int32(idx/side)-int32(g.r))*scale //nolint:gosec // small grid
		if int64(x)*int64(x)+int64(z)*int64(z) <= int64(radius)*int64(radius) {
			return [2]int32{x, z}, true
		}
	}
	return [2]int32{}, false
}

// respawnSearch is one search's state: the radius and the leaf fallback.
type respawnSearch struct {
	s       *Host
	r       *world.Region
	spawn   cube.Pos
	radius  int32
	leaf    cube.Pos
	hasLeaf bool
}

// worldRespawn is where a player without a bed or anchor respawns: a safe place within the
// spawn radius around the world spawn, as BDS looks for one. Eye position, as worldSpawn.
func (p *Player) worldRespawn(r *world.Region) mgl32.Vec3 {
	eyes, at := p.worldSpawn()
	if r == nil {
		return eyes
	}
	q := respawnSearch{s: p.s, r: r, spawn: cube.Pos{int(at[0]), int(at[1]), int(at[2])},
		radius: min(max(p.s.level.SpawnRadius, 0), spawnRadiusCap)}
	if pos, ok := q.find(); ok {
		return mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + eyeHeight, float32(pos[2]) + 0.5}
	}
	return eyes
}

// find tries the outer cells in their order and the inner cells around each, and the leaf
// fallback once an outer cell's inner cells are used up.
func (q *respawnSearch) find() (cube.Pos, bool) {
	if q.radius == 0 {
		return q.check(0, 0)
	}
	inner := min(uint32(q.radius), spawnInnerRadius)            //nolint:gosec // radius is positive
	outer := newSpawnGrid(outerRadius(uint32(q.radius), inner)) //nolint:gosec // radius is positive
	for {
		base, ok := outer.nextWithin([2]int32{}, spawnOuterStep, q.radius)
		if !ok {
			return cube.Pos{}, false
		}
		if pos, ok := q.searchCell(base, inner); ok {
			return pos, true
		}
		if q.hasLeaf {
			return q.leaf, true
		}
	}
}

// searchCell tries the inner cells around one outer cell.
func (q *respawnSearch) searchCell(base [2]int32, inner uint32) (cube.Pos, bool) {
	cells := newSpawnGrid(inner)
	for {
		off, ok := cells.nextWithin(base, 1, q.radius)
		if !ok {
			return cube.Pos{}, false
		}
		if pos, ok := q.check(off[0], off[1]); ok {
			return pos, true
		}
	}
}

// outerRadius is the outer grid's radius: how many steps of twice the inner radius the
// spawn radius goes past the inner grid's reach.
func outerRadius(radius, inner uint32) uint32 {
	past := max(radius, spawnInnerRadius) - spawnInnerRadius
	step := 2 * inner
	if step == 0 {
		return 0
	}
	return (past + step - 1) / step
}

// check is sub_BCF19F0 for the column dx, dz from the spawn: the place to stand in it, if
// it is safe and not on leaves. A place on leaves becomes the fallback.
func (q *respawnSearch) check(dx, dz int32) (cube.Pos, bool) {
	x, z := q.spawn[0]+int(dx), q.spawn[2]+int(dz)
	top, ok := q.surface(x, z)
	if !ok {
		return cube.Pos{}, false
	}
	feet, leafTop, ok := q.ground(cube.Pos{x, top, z})
	if !ok {
		return cube.Pos{}, false
	}
	feet, ok = q.fit(feet)
	if !ok {
		return cube.Pos{}, false
	}
	if q.onLeaves(feet, leafTop) {
		if !q.hasLeaf {
			q.leaf, q.hasLeaf = feet, true
		}
		return cube.Pos{}, false
	}
	return feet, true
}

// surface is BDS's getAboveTopSolidBlock with water and leaves: the Y above the highest
// block of the column that is solid material, water or leaves. False if the column's chunk
// is not here.
func (q *respawnSearch) surface(x, z int) (int, bool) {
	rng := q.r.Dimension().Range()
	for y := rng.Max(); y >= rng.Min(); y-- {
		b, ok := q.r.Block(cube.Pos{x, y, z}, 0)
		if !ok {
			return 0, false
		}
		props := q.s.reg.Blocks.Properties(b)
		if props.SolidMaterial || props.Material == registry.MaterialWater || props.Material == materialLeaves {
			return y + 1, true
		}
	}
	return rng.Min(), true
}

// ground walks the column down from top: through leaves, remembering the top of the first,
// to the first block with a collision box. A column that reaches something unsafe or the
// bottom stands on its leaves, if it had any.
func (q *respawnSearch) ground(top cube.Pos) (feet cube.Pos, leafTop int, ok bool) {
	leafTop = noLeaf
	minY := q.r.Dimension().Range().Min()
	for at := top; at[1] >= minY; at[1]-- {
		b, _ := q.r.Block(at, 0)
		if q.unsafe(b) {
			break
		}
		props := q.s.reg.Blocks.Properties(b)
		if props.Material == materialLeaves {
			if leafTop == noLeaf {
				leafTop = at[1] + 1
			}
			continue
		}
		if len(props.Collision) > 0 {
			return cube.Pos{at[0], at[1] + 1, at[2]}, leafTop, true
		}
	}
	if leafTop != noLeaf {
		return cube.Pos{top[0], leafTop, top[2]}, leafTop, true
	}
	return cube.Pos{}, leafTop, false
}

// unsafe is sub_BD051A0: portals, lava, magma, sweet berry bushes and wither roses.
func (q *respawnSearch) unsafe(b uint32) bool {
	props := q.s.reg.Blocks.Properties(b)
	return props.Material == materialPortal || props.Material == registry.MaterialLava ||
		unsafeRespawnBlocks[q.s.reg.Blocks.Name(b)]
}

// fit raises the player's box out of leaves and block shapes, as BDS steps it up. False if
// the box holds something unsafe, cannot rise or leaves the world.
func (q *respawnSearch) fit(feet cube.Pos) (cube.Pos, bool) {
	maxY := q.r.Dimension().Range().Max()
	for feet[1] <= maxY {
		lo, hi := respawnBox(feet)
		if q.boxHas(lo, hi, q.unsafe) {
			return cube.Pos{}, false
		}
		if q.boxHas(lo, hi, q.isLeaves) {
			feet[1]++
			continue
		}
		top, hit := q.shapeTop(lo, hi)
		if !hit {
			return feet, true
		}
		if top <= feet[1] {
			return cube.Pos{}, false
		}
		feet[1] = top
	}
	return cube.Pos{}, false
}

// respawnBox is the player's box standing at the centre of the block feet.
func respawnBox(feet cube.Pos) (lo, hi mgl32.Vec3) {
	cx, cz := float32(feet[0])+0.5, float32(feet[2])+0.5
	return mgl32.Vec3{cx - playerHalfWidth, float32(feet[1]), cz - playerHalfWidth},
		mgl32.Vec3{cx + playerHalfWidth, float32(feet[1]) + playerHeight, cz + playerHalfWidth}
}

func (q *respawnSearch) isLeaves(b uint32) bool {
	return q.s.reg.Blocks.Properties(b).Material == materialLeaves
}

// boxHas reports whether any block the box touches matches.
func (q *respawnSearch) boxHas(lo, hi mgl32.Vec3, match func(uint32) bool) bool {
	for x := floorInt(lo[0]); x <= floorInt(hi[0]); x++ {
		for y := floorInt(lo[1]); y < ceilInt(hi[1]); y++ {
			for z := floorInt(lo[2]); z <= floorInt(hi[2]); z++ {
				if b, ok := q.r.Block(cube.Pos{int(x), int(y), int(z)}, 0); ok && match(b) {
					return true
				}
			}
		}
	}
	return false
}

// shapeTop is the block Y the player must stand at to clear the highest block shape its box
// overlaps, and whether it overlaps any.
func (q *respawnSearch) shapeTop(lo, hi mgl32.Vec3) (int, bool) {
	top, hit := 0, false
	for x := floorInt(lo[0]); x <= floorInt(hi[0]); x++ {
		for y := floorInt(lo[1]); y < ceilInt(hi[1]); y++ {
			for z := floorInt(lo[2]); z <= floorInt(hi[2]); z++ {
				b, ok := q.r.Block(cube.Pos{int(x), int(y), int(z)}, 0)
				if !ok {
					continue
				}
				for _, box := range q.s.reg.Blocks.Properties(b).Collision {
					if !boxOverlaps(box, x, y, z, lo, hi) {
						continue
					}
					if t := ceilInt(float32(y) + box[4]); !hit || int(t) > top {
						top, hit = int(t), true
					}
				}
			}
		}
	}
	return top, hit
}

// boxOverlaps reports whether a block shape at x, y, z overlaps the box lo..hi.
func boxOverlaps(box registry.Box, x, y, z int32, lo, hi mgl32.Vec3) bool {
	bx, by, bz := float32(x), float32(y), float32(z)
	return bx+box[0] < hi[0] && bx+box[3] > lo[0] &&
		by+box[1] < hi[1] && by+box[4] > lo[1] &&
		bz+box[2] < hi[2] && bz+box[5] > lo[2]
}

// onLeaves is BDS's leaf test after the fit: the block under the feet is leaves, or the
// column's leaves are at or under them.
func (q *respawnSearch) onLeaves(feet cube.Pos, leafTop int) bool {
	if leafTop != noLeaf && leafTop <= feet[1] {
		return true
	}
	b, ok := q.r.Block(cube.Pos{feet[0], feet[1] - 1, feet[2]}, 0)
	return ok && q.isLeaves(b)
}

func ceilInt(f float32) int32 {
	i := floorInt(f)
	if float32(i) < f {
		i++
	}
	return i
}
