package weather

import (
	"slices"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Lightning rods drawing a strike, from BDS 1.26.60's chunk tick (sub_CF951A0): before it
// looks for mobs, lightning looks for a rod with sub_CF99310. That collects every rod block
// within rodReach across (a circle) and rodReach up or down of the strike (sub_CE69F30 and
// the sub chunk scan 0xCF6CB80, palettes first), sorts them by square distance and takes the
// first one open to the sky where it rains just above it.
const (
	rodReach = 64
	// rodSuffix ends the name of every lightning rod: plain, exposed, weathered, oxidized and
	// their waxed forms, the list BDS's filter sub_CFBD8A0 holds.
	rodSuffix = "lightning_rod"
)

// rodHit is a rod found near a strike and its square distance from it.
type rodHit struct {
	pos  cube.Pos
	dist int
}

// rodTable marks the lightning rod states, indexed by state ID.
func rodTable(names func(id uint32) string, count int) []bool {
	t := make([]bool, count)
	for id := range t {
		t[id] = strings.HasSuffix(names(uint32(id)), rodSuffix) //nolint:gosec // block counts fit
	}
	return t
}

// isRod reports whether b is a lightning rod state.
func (t *Ticker) isRod(b uint32) bool { return int(b) < len(t.rods) && t.rods[b] }

// lightningRod is where a strike aimed at at goes instead: the nearest rod that is open to
// the sky and rained on, as BDS's sub_CF99310. Runs: owner of the region.
func (t *Ticker) lightningRod(r *world.Region, s *regionState, at cube.Pos) (cube.Pos, bool) {
	s.rods = t.rodsNear(r, at, s.rods[:0])
	slices.SortFunc(s.rods, func(a, b rodHit) int { return a.dist - b.dist })
	for _, h := range s.rods {
		if t.rodOpen(r, h.pos) && t.RainingAt(r, h.pos.Side(cube.FaceUp)) {
			return h.pos, true
		}
	}
	return cube.Pos{}, false
}

// rodsNear appends the rods in reach of at to dst, in chunk order.
func (t *Ticker) rodsNear(r *world.Region, at cube.Pos, dst []rodHit) []rodHit {
	for cx := (at[0] - rodReach) >> 4; cx <= (at[0]+rodReach)>>4; cx++ {
		for cz := (at[2] - rodReach) >> 4; cz <= (at[2]+rodReach)>>4; cz++ {
			r.FindBlocks(mcdb.ChunkPos{int32(cx), int32(cz)}, t.isRod, func(p cube.Pos, _ uint32) { //nolint:gosec // chunk coordinates fit int32
				dx, dy, dz := p[0]-at[0], p[1]-at[1], p[2]-at[2]
				if dy < -rodReach || dy > rodReach || dx*dx+dz*dz > rodReach*rodReach {
					return
				}
				dst = append(dst, rodHit{pos: p, dist: dx*dx + dy*dy + dz*dz})
			})
		}
	}
	return dst
}

// rodOpen is BDS's sky test on the rod: above the height map of its column, or above the
// dimension, or in a column with no chunk.
func (t *Ticker) rodOpen(r *world.Region, pos cube.Pos) bool {
	rg := r.Dimension().Range()
	if pos[1] > rg[1] {
		return true
	}
	if pos[1] < rg[0] {
		return false
	}
	h, ok := r.Height(pos[0], pos[2])
	return !ok || pos[1] >= h
}

// rodStrike is the strike's position on a rod: its block corner, as BDS strikes there.
func rodStrike(pos cube.Pos) mgl32.Vec3 {
	return mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])}
}
