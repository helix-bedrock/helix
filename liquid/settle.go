package liquid

import (
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Settling is Helix's own rule, not BDS's: generation leaves water and lava
// beside open space (a lake cut by a cliff, a cave wall, an aquifer edge),
// and nothing ticks it until a player changes a block next to it. Liquids, a
// world.Settler, finds that liquid in every chunk that loads (new or saved,
// so chunks saved before this rule settle too) and queues its tick, so it
// flows as soon as the chunk ticks. Liquid that is already settled but
// looks open (the thin end of a flow) gets one tick that changes nothing.

// chunkEdge is the last local X or Z in a chunk.
const chunkEdge = 15

// settleSides are the sides liquid can move to: down and the four
// horizontal ones. Liquid never moves up.
func settleSides() [5]cube.Pos {
	return [5]cube.Pos{{0, -1, 0}, {-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}}
}

// Settle queues a tick for every liquid in col that the chunk alone shows can
// move, and returns the liquid on the chunk's edge whose way out is only into
// the neighbouring chunk, with Tick holding its delay. Runs: a loading
// worker, many at once.
func (l *Liquids) Settle(pos mcdb.ChunkPos, col *chunk.Column, edge []chunk.ScheduledBlockUpdate) []chunk.ScheduledBlockUpdate {
	c := col.Chunk
	queued := queuedTicks(col)
	for i, sub := range c.Sub() {
		if !l.subHasLiquid(sub) {
			continue
		}
		baseY := c.SubY(int16(i)) //nolint:gosec // sub chunk counts fit int16
		edge = l.settleSub(pos, col, sub, baseY, queued, edge)
	}
	return edge
}

// queuedTicks is the positions col's saved queue already ticks, so a loaded
// chunk's liquid is not queued twice; nil when the queue is empty.
func queuedTicks(col *chunk.Column) map[cube.Pos]struct{} {
	if len(col.ScheduledBlocks) == 0 {
		return nil
	}
	queued := make(map[cube.Pos]struct{}, len(col.ScheduledBlocks))
	for _, u := range col.ScheduledBlocks {
		queued[u.Pos] = struct{}{}
	}
	return queued
}

// subHasLiquid reports whether either layer of sub holds a liquid, from the
// palettes alone, so dry sub chunks are skipped.
func (l *Liquids) subHasLiquid(sub *chunk.SubChunk) bool {
	if sub.Empty() {
		return false
	}
	for _, layer := range sub.Layers() {
		p := layer.Palette()
		for i := range p.Len() {
			if _, ok := l.at(p.Value(uint16(i))); ok { //nolint:gosec // palette sizes fit uint16
				return true
			}
		}
	}
	return false
}

// settleSub runs Settle's check on every block of one sub chunk.
func (l *Liquids) settleSub(pos mcdb.ChunkPos, col *chunk.Column, sub *chunk.SubChunk, baseY int16, queued map[cube.Pos]struct{}, edge []chunk.ScheduledBlockUpdate) []chunk.ScheduledBlockUpdate {
	for y := range uint8(16) {
		for x := range uint8(16) {
			for z := range uint8(16) {
				edge = l.settleBlock(pos, col, sub, x, baseY+int16(y), z, queued, edge)
			}
		}
	}
	return edge
}

// settleBlock queues the liquid at local x, y, z if it can move inside the
// chunk, or keeps it in edge if it touches the chunk's side.
func (l *Liquids) settleBlock(pos mcdb.ChunkPos, col *chunk.Column, sub *chunk.SubChunk, x uint8, y int16, z uint8, queued map[cube.Pos]struct{}, edge []chunk.ScheduledBlockUpdate) []chunk.ScheduledBlockUpdate {
	lb := l.chunkLiquidAt(sub, x, uint8(y&15), z) //nolint:gosec // y&15 fits uint8
	i, ok := l.at(lb)
	if !ok {
		return edge
	}
	world := cube.Pos{int(pos[0])<<4 | int(x), int(y), int(pos[1])<<4 | int(z)}
	if _, ok := queued[world]; ok {
		return edge
	}
	delay := l.TickDelay(i.kind)
	if l.opensInChunk(col.Chunk, i.kind, x, y, z) {
		col.ScheduledBlocks = append(col.ScheduledBlocks, chunk.ScheduledBlockUpdate{Pos: world, Block: lb, Tick: col.Tick + int64(delay)})
		return edge
	}
	if x == 0 || x == chunkEdge || z == 0 || z == chunkEdge {
		edge = append(edge, chunk.ScheduledBlockUpdate{Pos: world, Block: lb, Tick: int64(delay)})
	}
	return edge
}

// chunkLiquidAt is liquidAt inside one sub chunk: the second layer if it
// holds anything, else layer 0.
func (l *Liquids) chunkLiquidAt(sub *chunk.SubChunk, x, y, z uint8) uint32 {
	if b := sub.Block(x, y, z, 1); b != l.blocks.Air() {
		return b
	}
	return sub.Block(x, y, z, 0)
}

// opensInChunk reports whether liquid k at local x, y, z has a side inside
// the chunk it can move into.
func (l *Liquids) opensInChunk(c *chunk.Chunk, k Kind, x uint8, y int16, z uint8) bool {
	r := c.Range()
	for _, s := range settleSides() {
		nx, ny, nz := int(x)+s[0], int(y)+s[1], int(z)+s[2]
		if nx < 0 || nx > chunkEdge || nz < 0 || nz > chunkEdge || ny < r[0] {
			continue
		}
		b0 := c.Block(uint8(nx), int16(ny), uint8(nz), 0) //nolint:gosec // checked in range above
		b1 := c.Block(uint8(nx), int16(ny), uint8(nz), 1) //nolint:gosec // as above
		if l.opensTo(k, b0, b1) {
			return true
		}
	}
	return false
}

// SettleEdges queues the edge liquid Settle returned whose neighbour in the
// next chunk lets it move, and returns, in edge's array, the liquid whose
// neighbour is not loaded yet. Runs: owner of the region.
func (l *Liquids) SettleEdges(r *world.Region, edge []chunk.ScheduledBlockUpdate) []chunk.ScheduledBlockUpdate {
	waiting := edge[:0]
	for _, u := range edge {
		i, ok := l.at(u.Block)
		if !ok {
			continue
		}
		switch l.opensOutward(r, u.Pos, i.kind) {
		case outwardOpen:
			if r.HasScheduledTick(u.Pos, u.Block) {
				continue
			}
			r.ScheduleTick(u.Pos, u.Block, int32(u.Tick), 0) //nolint:gosec // delays are small
		case outwardUnknown:
			waiting = append(waiting, u)
		}
	}
	return waiting
}

// outward is what a liquid's sides into the next chunk allow.
type outward uint8

const (
	outwardClosed outward = iota
	outwardOpen
	// outwardUnknown means a side's chunk is not loaded.
	outwardUnknown
)

// opensOutward reports whether liquid k at pos can move through a side that
// leaves its chunk.
func (l *Liquids) opensOutward(r *world.Region, pos cube.Pos, k Kind) outward {
	res := outwardClosed
	sides := settleSides()
	for _, s := range sides[1:] {
		n := pos.Add(s)
		if n[0]>>4 == pos[0]>>4 && n[2]>>4 == pos[2]>>4 {
			continue
		}
		b0, ok := r.Block(n, 0)
		if !ok {
			res = outwardUnknown
			continue
		}
		b1, _ := r.Block(n, 1)
		if l.opensTo(k, b0, b1) {
			return outwardOpen
		}
	}
	return res
}

// opensTo reports whether liquid k could spread into a block holding b0 and
// b1 on its two layers: spreadTo's own checks, without changing anything.
func (l *Liquids) opensTo(k Kind, b0, b1 uint32) bool {
	lb := b1
	if lb == l.blocks.Air() {
		lb = b0
	}
	m := l.material(lb)
	if m == registry.MaterialLava || m == l.liq[k].material {
		return false
	}
	p := l.blocks.Properties(b0)
	if k == Lava {
		return !p.LavaBlocking
	}
	return p.LiquidReaction != registry.LiquidBlocking
}
