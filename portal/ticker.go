package portal

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Ticker is a world.BlockTicker that breaks nether portals whose frame broke (the
// portal block's neighborChanged) and hands every other call to next.
type Ticker struct {
	p    *Portals
	next world.BlockTicker
}

var _ world.BlockTicker = (*Ticker)(nil)

// NewTicker wraps next.
func NewTicker(p *Portals, next world.BlockTicker) *Ticker { return &Ticker{p: p, next: next} }

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

// Placed hands on to next.
func (t *Ticker) Placed(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
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
