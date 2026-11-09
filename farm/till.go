package farm

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Till is what a hoe makes of a block: the block it becomes, whether the hoe's on_tool_used
// runs (its sound and a point of wear), and an item the block drops.
type Till struct {
	Block uint32
	// ToolUsed is false for rooted dirt, whose use with a hoe (0xD53DCC0) does not call it.
	ToolUsed bool
	// Drop is the item dropped, "" for none: rooted dirt drops hanging roots.
	Drop string
}

// tillTable is BDS's dirt blocks with their tilled block (each block's use with an item
// tagged minecraft:is_hoe): dirt, grass and dirt paths become farmland, coarse dirt becomes
// dirt, and rooted dirt becomes dirt and drops hanging roots.
func (t *Ticker) tillTable() map[uint32]Till {
	m := map[uint32]Till{}
	add := func(from, to string, toolUsed bool, drop string) {
		f, ok := t.blocks.Block(from)
		if !ok {
			return
		}
		into, ok := t.blocks.Block(to)
		if !ok {
			return
		}
		m[f] = Till{Block: into, ToolUsed: toolUsed, Drop: drop}
	}
	add(dirt, Farmland, true, "")
	add(grassBlock, Farmland, true, "")
	add(grassPath, Farmland, true, "")
	add(coarseDirt, dirt, true, "")
	add(rootedDirt, dirt, false, HangingRoots)
	return m
}

// Tilled returns what a hoe makes of block, false if it is not tilled. Runs: any goroutine.
func (t *Ticker) Tilled(block uint32) (Till, bool) {
	k, ok := t.tills[t.blocks.Default(block)]
	return k, ok
}

// TillAt tills the block at pos as a hoe used on it does, with no check of what is above: BDS's
// dirt blocks make none. It returns what it did, false if the block is not tilled. The caller
// plays the hoe's sound, wears it and drops Drop. Runs: owner of the region.
func (t *Ticker) TillAt(r *world.Region, pos cube.Pos) (Till, bool) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return Till{}, false
	}
	k, ok := t.Tilled(b)
	if !ok {
		return Till{}, false
	}
	t.set(r, pos, k.Block)
	return k, true
}
