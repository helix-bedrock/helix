package portal

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Ticker is a world.BlockTicker that lights nether portals from fire placed in a frame
// (the fire block's onPlace) and breaks portals whose frame broke (the portal block's
// neighborChanged), and hands every other call to next.
type Ticker struct {
	p    *Portals
	next world.BlockTicker
	dim  func(*world.Dimension) mcdb.Dimension
}

var _ world.BlockTicker = (*Ticker)(nil)

// NewTicker wraps next; dim says which dimension a region's dimension is.
func NewTicker(p *Portals, dim func(*world.Dimension) mcdb.Dimension, next world.BlockTicker) *Ticker {
	return &Ticker{p: p, next: next, dim: dim}
}

// RandomTicks hands on to next.
func (t *Ticker) RandomTicks(block uint32, layer uint8) bool { return t.next.RandomTicks(block, layer) }

// RandomTick hands on to next.
func (t *Ticker) RandomTick(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
	t.next.RandomTick(r, pos, block, layer)
}

// QueuedTick hands on to next.
func (t *Ticker) QueuedTick(r *world.Region, pos cube.Pos, block uint32) {
	t.next.QueuedTick(r, pos, block)
}

// Placed is the fire block's onPlace (sub_CAFE9C0): fire placed in the overworld or the
// Nether, by a player or by spreading, first tries to light a nether portal there; when
// it does, the fire is gone and next is not told. Everything else goes to next.
func (t *Ticker) Placed(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
	if layer == 0 && t.p.IsFire(block) {
		if d := t.dim(r.Dimension()); (d == mcdb.Overworld || d == mcdb.Nether) && t.p.Light(r, d, pos) {
			return
		}
	}
	t.next.Placed(r, pos, block, layer)
}

// NeighbourChanged checks a portal block's frame, else hands on to next.
func (t *Ticker) NeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, layer uint8) {
	if layer == 0 && t.p.IsPortal(block) {
		t.p.Check(r, pos, block)
		return
	}
	t.next.NeighbourChanged(r, pos, from, block, layer)
}
