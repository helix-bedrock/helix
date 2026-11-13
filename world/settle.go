package world

import (
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/mcdb"
)

// Settler queues the ticks a chunk needs to settle: generation places blocks without
// ticking them, so liquid left beside open space would stand still until a neighbour
// changed. It runs on every chunk that loads, generated or saved, so chunks saved before
// it existed (or saved before their edges were decided) settle too. This is Helix's own
// step, not BDS's.
type Settler interface {
	// Settle runs on a loading worker, many at once, on the column just generated or
	// loaded at pos, before its saved queue is taken. It adds to col.ScheduledBlocks (due after col.Tick) the ticks the chunk
	// alone shows are needed, and returns, appended to edge, the blocks on the chunk's
	// side that need one only if the next chunk lets them move, with Tick holding the
	// delay.
	Settle(pos mcdb.ChunkPos, col *chunk.Column, edge []chunk.ScheduledBlockUpdate) []chunk.ScheduledBlockUpdate
	// SettleEdges runs on the chunk's ticks with the edge blocks Settle returned. It
	// returns, in edge's array, those it cannot decide yet because the next chunk is not
	// loaded; they are passed again on the chunk's next tick, for up to settleTicks ticks.
	// Runs: owner of the region.
	SettleEdges(r *Region, edge []chunk.ScheduledBlockUpdate) []chunk.ScheduledBlockUpdate
}

// settleChunk runs the Settler on a generated or loaded column, recovering a panic in it as
// generate does. It returns the edge blocks for the chunk's first tick.
func (d *Dimension) settleChunk(pos mcdb.ChunkPos, col *chunk.Column) (edge []chunk.ScheduledBlockUpdate) {
	if d.conf.Settler == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			d.log.Error("settle chunk", "pos", pos, "panic", r)
			edge = nil
		}
	}()
	return d.conf.Settler.Settle(pos, col, nil)
}

// settleTicks is how many of its ticks a chunk keeps asking about edge blocks whose next
// chunk is not loaded; after that they are dropped (the next chunk is most likely in
// another region, and a block change there still wakes the liquid).
const settleTicks = 200

// settleEdges passes a chunk's waiting edge blocks to the Settler.
func (r *Region) settleEdges(c *Chunk) {
	st := r.d.conf.Settler
	if st == nil || c.settleTries >= settleTicks {
		c.settle = nil
		return
	}
	c.settleTries++
	c.settle = st.SettleEdges(r, c.settle)
	if len(c.settle) == 0 {
		c.settle = nil
	}
}
