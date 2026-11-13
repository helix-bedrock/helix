package fall

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// StartFunc turns block at pos into a falling block: it makes the entity and empties pos
// (Table.Lift). False if nothing started, for example a plugin stopped it.
type StartFunc func(r *world.Region, pos cube.Pos, block uint32) bool

// DestroyFunc removes a snow layer that has nothing to lie on and cannot fall.
type DestroyFunc func(r *world.Region, pos cube.Pos, block uint32)

// Ticker adds falling to another BlockTicker. Runs: owner of the region.
type Ticker struct {
	world.BlockTicker
	t       *Table
	start   StartFunc
	destroy DestroyFunc
	// brk breaks a settled scaffolding nothing holds up, with its drops.
	brk DestroyFunc
}

// SetBreak sets what breaks a scaffolding that lost its hold: the block goes with its drops
// and its neighbours are told. Call it before the server starts ticking.
func (k *Ticker) SetBreak(fn DestroyFunc) { k.brk = fn }

// NewTicker returns a Ticker that makes falling blocks with start, removes unheld snow
// with destroy, and hands everything else to next.
func NewTicker(t *Table, start StartFunc, destroy DestroyFunc, next world.BlockTicker) *Ticker {
	return &Ticker{BlockTicker: next, t: t, start: start, destroy: destroy}
}

// Table returns the rules the Ticker runs by.
func (k *Ticker) Table() *Table { return k.t }

// Placed is FallingBlock's onPlace listener: a look in 2 ticks. A snow layer does not look
// when placed (TopSnowBlock's listener only does for a flag the snow layer is not built with).
func (k *Ticker) Placed(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
	if layer == 0 {
		switch k.t.Kind(block) {
		case Block:
			r.ScheduleTick(pos, block, delayPlaced, 0)
		case Scaffold:
			k.scaffoldPlaced(r, pos, block)
		}
	}
	k.BlockTicker.Placed(r, pos, block, layer)
}

// NeighbourChanged is neighborChanged: a falling block looks again in 1 tick when the
// block under it changed and in 2 for any other side; a snow layer only for the block
// under it.
func (k *Ticker) NeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, layer uint8) {
	if layer == 0 {
		below := from == pos.Side(cube.FaceDown)
		switch k.t.Kind(block) {
		case Block:
			delay := int32(delayAround)
			if below {
				delay = delayBelow
			}
			r.ScheduleTick(pos, block, delay, 0)
		case Snow:
			if below {
				r.ScheduleTick(pos, block, delayBelow, 0)
			}
		case Scaffold:
			k.scaffoldNeighbour(r, pos, block)
		}
	}
	k.BlockTicker.NeighbourChanged(r, pos, from, block, layer)
}

// QueuedTick runs the falling block's and snow layer's tick.
func (k *Ticker) QueuedTick(r *world.Region, pos cube.Pos, block uint32) {
	switch k.t.Kind(block) {
	case Block:
		k.tickBlock(r, pos)
	case Snow:
		k.tickSnow(r, pos, block)
	case Scaffold:
		k.tickScaffold(r, pos, block)
	default:
		k.BlockTicker.QueuedTick(r, pos, block)
	}
}

// tickBlock is FallingBlock's tick (sub_CAF8640): the lowest falling block of the column
// pos is in falls, if it is above the bottom of the world and free to fall.
func (k *Ticker) tickBlock(r *world.Region, pos cube.Pos) {
	minY := r.Dimension().Range()[0]
	low := pos
	for low[1]-1 > minY {
		b, ok := r.Block(low.Side(cube.FaceDown), 0)
		if !ok || k.t.Kind(b) != Block {
			break
		}
		low[1]--
	}
	if low[1] <= minY || !k.t.FreeBelow(r, low, Block) {
		return
	}
	b, ok := r.Block(low, 0)
	if !ok {
		return
	}
	if k.start(r, low, b) {
		k.tickAround(r, low.Side(cube.FaceUp))
		k.tickAround(r, low.Side(cube.FaceDown))
	}
}

// tickAround is tickBlocksAround2D (sub_CAF7B90): the falling blocks beside pos, west, east,
// north and south, look again in 2 ticks.
func (k *Ticker) tickAround(r *world.Region, pos cube.Pos) {
	for _, f := range [...]cube.Face{cube.FaceWest, cube.FaceEast, cube.FaceNorth, cube.FaceSouth} {
		n := pos.Side(f)
		if b, ok := r.Block(n, 0); ok && k.t.Kind(b) == Block {
			r.ScheduleTick(n, b, delayAround, 0)
		}
	}
}

// tickSnow is TopSnowBlock's tick listener (sub_D4E9720): a snow layer free to fall falls;
// one that is neither free to fall nor held is destroyed.
func (k *Ticker) tickSnow(r *world.Region, pos cube.Pos, block uint32) {
	minY := r.Dimension().Range()[0]
	if k.t.FreeBelow(r, pos, Snow) {
		if pos[1] > minY {
			k.start(r, pos, block)
		}
		return
	}
	if !k.t.SnowSurvives(r, pos) && k.destroy != nil {
		k.destroy(r, pos, block)
	}
}
