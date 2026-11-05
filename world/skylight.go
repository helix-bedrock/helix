package world

import (
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// skyChunk is the chunk holding pos.
func skyChunk(pos cube.Pos) mcdb.ChunkPos {
	return mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)} //nolint:gosec // block X and Z fit int32
}

// How far sky light can reach before it has faded out.
const (
	skyFull   = 15
	skyReach  = skyFull - 1
	skyWidth  = 2*skyReach + 1
	skyCells  = skyWidth * skyWidth * skyWidth
	skyColumn = skyWidth * skyWidth
)

// skySearch is what SkyLight works in, kept so searching allocates nothing once it has run.
type skySearch struct {
	generation uint16
	seen       [skyCells]uint16
	columnSeen [skyColumn]uint16
	columnTop  [skyColumn]int16
	byLoss     [skyFull][]cube.Pos
}

// skyDampening is what the blocks at pos take off light, with both layers counted. What is not
// loaded lets nothing through.
func (r *Region) skyDampening(pos cube.Pos) int {
	b, ok := r.Block(pos, 0)
	if !ok {
		return skyFull
	}
	blocks := r.d.conf.Blocks
	d := int(blocks.FilteringBlock(b))
	if second, ok := r.Block(pos, 1); ok {
		d = max(d, int(blocks.FilteringBlock(second)))
	}
	return d
}

// skyDirect is the light falling straight down to pos, with pos's own block counted if self.
func (r *Region) skyDirect(s *skySearch, from, pos cube.Pos, self bool) int {
	column := (pos[0]-from[0]+skyReach)*skyWidth + pos[2] - from[2] + skyReach
	if s.columnSeen[column] != s.generation {
		s.columnSeen[column] = s.generation
		top := int16(pos[1]) //nolint:gosec // world heights fit
		if c := r.chunks[skyChunk(pos)]; c != nil {
			top = c.col.Chunk.HighestBlock(uint8(pos[0]&15), uint8(pos[2]&15)) //nolint:gosec // masked
		}
		s.columnTop[column] = top
	}
	light := skyFull
	lowest := pos[1]
	if !self {
		lowest++
	}
	for y := int(s.columnTop[column]); y >= lowest && light > 0; y-- {
		light -= r.skyDampening(cube.Pos{pos[0], y, pos[2]})
	}
	return max(light, 0)
}

// SkyLight returns the sky light at pos, 0 to 15, before the time of day takes anything off,
// working it out from the blocks around: straight down from the sky, then spreading and losing
// at least one a block. The block at pos itself takes nothing off, as it is the thin blocks that
// ask. It searches each time, so keep the answer until NearbyVersion changes. Runs: owner.
func (r *Region) SkyLight(pos cube.Pos) uint8 {
	if _, ok := r.Block(pos, 0); !ok {
		return 0
	}
	if r.sky == nil {
		r.sky = &skySearch{}
	}
	s := r.sky
	s.generation++
	if s.generation == 0 {
		s.seen, s.columnSeen = [skyCells]uint16{}, [skyColumn]uint16{}
		s.generation = 1
	}
	best := r.skyDirect(s, pos, pos, false)
	for i := range s.byLoss {
		s.byLoss[i] = s.byLoss[i][:0]
	}
	s.byLoss[0] = append(s.byLoss[0], pos)
	s.seen[skyIndex(pos, pos)] = s.generation
	for loss := 0; loss < skyFull && skyFull-loss > best; loss++ {
		for i := 0; i < len(s.byLoss[loss]); i++ {
			at := s.byLoss[loss][i]
			next := loss + 1
			if at != pos {
				next = loss + max(1, r.skyDampening(at))
			}
			if next >= skyFull {
				continue
			}
			for f := cube.Face(0); f < 6; f++ {
				n := at.Side(f)
				if !skyInReach(pos, n) {
					continue
				}
				index := skyIndex(pos, n)
				if s.seen[index] == s.generation {
					continue
				}
				s.seen[index] = s.generation
				if r.skyDampening(n) >= skyFull {
					continue
				}
				best = max(best, r.skyDirect(s, pos, n, true)-next)
				s.byLoss[next] = append(s.byLoss[next], n)
			}
		}
	}
	return uint8(best) //nolint:gosec // 0 to 15
}

// skyInReach says n is inside the search around from.
func skyInReach(from, n cube.Pos) bool {
	for i := range n {
		if d := n[i] - from[i]; d < -skyReach || d > skyReach {
			return false
		}
	}
	return true
}

// skyIndex is n's place in the search around from.
func skyIndex(from, n cube.Pos) int {
	return ((n[0]-from[0]+skyReach)*skyWidth+n[1]-from[1]+skyReach)*skyWidth + n[2] - from[2] + skyReach
}

// NearbyVersion changes when a block changes close enough to change SkyLight at pos. Runs: owner.
func (r *Region) NearbyVersion(pos cube.Pos) uint64 {
	centre := skyChunk(pos)
	var v uint64
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			p := centre
			p[0] += x
			p[1] += z
			// Multiplied so a chunk unloading cannot cancel out another changing.
			v *= 31
			if c := r.chunks[p]; c != nil {
				v += c.version + 1
			}
		}
	}
	return v
}
