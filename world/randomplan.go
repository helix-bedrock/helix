package world

import (
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Random ticks are planned on idle cores and run in order. Drawing each chunk's positions from
// BDS's generator and reading which blocks there take random ticks is most of their cost: at
// speed 1 a chunk draws about 60 positions and few hold a block that ticks. The draws follow
// one another through the region's generator, so each chunk's start is found by jumping the
// generator ahead (lcgSkip); then every chunk's positions are drawn in parallel
// (Region.Parallel). The ticks themselves still run one after another in BDS's order. A chunk
// whose blocks changed earlier in the tick reads them again, and one whose height or start
// changed draws again, so the result is exactly the one-by-one result.

// The bits of plannedTick.ticks.
const (
	plannedLayer0 uint8 = 1 << iota
	plannedLayer1
)

// plannedTick is a drawn position and the layers whose block took random ticks when drawn.
type plannedTick struct {
	pos   cube.Pos
	ticks uint8
}

// chunkPlan is one chunk's random ticks for this tick.
type chunkPlan struct {
	c *Chunk
	// top is the highest sub chunk holding a block, start and end the generator before and
	// after its draws, version the chunk's version when planned.
	top        int
	start, end uint32
	version    uint64
	from, n    int
}

// lcgSkip is the region's random tick generator advanced n draws.
func lcgSkip(x uint32, n int32) uint32 {
	fa, fc := uint32(lcgMultiplier), uint32(lcgIncrement)
	ra, rc := uint32(1), uint32(0)
	for ; n > 0; n >>= 1 {
		if n&1 == 1 {
			ra, rc = fa*ra, fa*rc+fc
		}
		fa, fc = fa*fa, fa*fc+fc
	}
	return ra*x + rc
}

// The generator BDS draws random tick positions from: x = 3x + 1013904223.
const (
	lcgMultiplier = 3
	lcgIncrement  = 1013904223
)

// topSub is the highest sub chunk of c holding a block, -1 for none.
func (r *Region) topSub(c *Chunk) int {
	subs := c.col.Chunk.Sub()
	air := r.d.conf.Blocks.Air()
	for i := len(subs) - 1; i >= 0; i-- {
		if !subEmpty(subs[i], air) {
			return i
		}
	}
	return -1
}

// randomTickCount is how many positions a chunk with top as its highest sub chunk draws.
func randomTickCount(top int, speed int32) int32 {
	return int32(float32(top+1)*randomTicksPerSubChunk) * speed
}

// planRandomTicks plans the random ticks of the chunks tickBlocks will tick, in its order.
func (r *Region) planRandomTicks(now int64, speed int32) {
	r.plans = r.plans[:0]
	if r.d.conf.Ticker == nil || speed <= 0 {
		return
	}
	add := func(pos mcdb.ChunkPos) {
		c, ok := r.chunks[pos]
		if !ok || c.tickedAt == now || c.plannedAt == now {
			return
		}
		c.plannedAt = now
		c.plan = len(r.plans)
		r.plans = append(r.plans, chunkPlan{c: c})
	}
	for _, v := range r.viewers {
		if v.lastRadius >= 0 {
			for _, pos := range v.scratch {
				add(pos)
			}
		}
	}
	for _, h := range r.tickets {
		if h.t.ticking {
			for _, pos := range h.chunks {
				add(pos)
			}
		}
	}
	lcg, total := r.lcg, 0
	for i := range r.plans {
		p := &r.plans[i]
		p.top = r.topSub(p.c)
		p.start, p.version, p.from = lcg, p.c.version, total
		if p.top >= 0 {
			n := randomTickCount(p.top, speed)
			p.n = int(n)
			lcg = lcgSkip(lcg, n)
		}
		p.end = lcg
		total += p.n
	}
	if cap(r.planned) < total {
		r.planned = make([]plannedTick, total)
	}
	r.planned = r.planned[:total]
	if r.planFn == nil {
		r.planFn = r.drawPlan
	}
	r.Parallel(len(r.plans), r.planFn)
}

// drawPlan draws the positions of plan i and reads which of their blocks take random ticks.
// It only reads.
func (r *Region) drawPlan(i int) {
	p := &r.plans[i]
	if p.n == 0 {
		return
	}
	ticker := r.d.conf.Ticker
	subs := p.c.col.Chunk.Sub()
	height := uint16(16 * (p.top + 1)) //nolint:gosec // at most a few hundred
	baseX, baseZ := int(p.c.pos[0])<<4, int(p.c.pos[1])<<4
	lcg := p.start
	out := r.planned[p.from : p.from+p.n]
	for k := range out {
		lcg = lcgMultiplier*lcg + lcgIncrement
		x, z := uint8(lcg>>2)&15, uint8(lcg>>10)&15
		y := uint16(lcg>>18) % height
		out[k] = plannedTick{pos: cube.Pos{baseX + int(x), r.d.r[0] + int(y), baseZ + int(z)}}
		sub := subs[y>>4]
		if sub == nil {
			continue
		}
		layers := sub.Layers()
		if len(layers) > 0 && ticker.RandomTicks(layers[0].At(x, uint8(y&15), z), 0) {
			out[k].ticks |= plannedLayer0
		}
		if len(layers) > 1 && ticker.RandomTicks(layers[1].At(x, uint8(y&15), z), 1) {
			out[k].ticks |= plannedLayer1
		}
	}
}

// runPlannedTicks runs c's planned random ticks and reports whether it could: false when the
// chunk has no plan this tick, or its height or the generator changed since, and the ticks
// must be drawn again one by one.
func (r *Region) runPlannedTicks(c *Chunk, now int64, speed int32) bool {
	if c.plannedAt != now || c.plan >= len(r.plans) || r.plans[c.plan].c != c {
		return false
	}
	p := &r.plans[c.plan]
	if r.lcg != p.start || r.topSub(c) != p.top {
		return false
	}
	if p.top < 0 || speed <= 0 {
		return true
	}
	r.lcg = p.end
	// As randomTicks does, the sub chunks are taken once for the whole chunk.
	subs := c.col.Chunk.Sub()
	for k := range p.n {
		t := r.planned[p.from+k]
		if c.version != p.version {
			// A block of this chunk changed since the plan: read the blocks again.
			r.randomTickAt(subs, t.pos, plannedLayer0|plannedLayer1, true)
			continue
		}
		r.randomTickAt(subs, t.pos, t.ticks, false)
	}
	return true
}

// randomTickAt is randomTicks' step for one position: layer 0's block, then layer 1's, each
// ticked if it takes random ticks. Unless check is set, the layers in ticks are known to.
// Layer 1 is checked again when layer 0's tick changed the chunk.
func (r *Region) randomTickAt(subs []*chunk.SubChunk, pos cube.Pos, ticks uint8, check bool) {
	y := pos[1] - r.d.r[0]
	sub := subs[y>>4]
	if sub == nil {
		return
	}
	ticker := r.d.conf.Ticker
	x, ly, z := uint8(pos[0]&15), uint8(y&15), uint8(pos[2]&15) //nolint:gosec // masked
	layers := sub.Layers()
	c := r.chunks[mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)}] //nolint:gosec // chunk coordinates fit int32
	var version uint64
	if c != nil {
		version = c.version
	}
	if len(layers) > 0 && (check || ticks&plannedLayer0 != 0) {
		if b := layers[0].At(x, ly, z); !check || ticker.RandomTicks(b, 0) {
			ticker.RandomTick(r, pos, b, 0)
		}
	}
	if c != nil && c.version != version {
		check = true
	}
	if len(layers) > 1 && (check || ticks&plannedLayer1 != 0) {
		if b := layers[1].At(x, ly, z); !check || ticker.RandomTicks(b, 1) {
			ticker.RandomTick(r, pos, b, 1)
		}
	}
}
