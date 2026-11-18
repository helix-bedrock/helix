package player

import (
	"maps"

	"github.com/helix-bedrock/helix/registry"
)

// Sea pickles stack as BDS 1.26.60's SeaPickleBlockItem ('s class): a pickle placed on
// a pickle of fewer than 4 adds one to its cluster_count and keeps the rest of its state. Whether
// a new one is dead is the farm's onPlace, by the water it is placed in.

const (
	nameSeaPickle        = "minecraft:sea_pickle"
	statePickleCluster   = "cluster_count"
	pickleMaxClusterSize = 3
)

// seaPickleBuiltOver reports whether placing block on the pickle b adds to it.
func seaPickleBuiltOver(blocks *registry.Blocks, b, block uint32) bool {
	if blocks.Name(b) != nameSeaPickle || blocks.Name(block) != nameSeaPickle {
		return false
	}
	n, _ := blocks.States(b)[statePickleCluster].(int32)
	return n < pickleMaxClusterSize
}

// seaPickleState is the pickle placed: one more on the pickle already there, else the item's.
func seaPickleState(pl *Placement) (uint32, bool) {
	blocks := pl.Player.s.reg.Blocks
	if r := pl.Player.entity.Region(); r != nil {
		if b, ok := r.Block(pl.Pos, 0); ok && blocks.Name(b) == nameSeaPickle {
			states := maps.Clone(blocks.States(b))
			n, _ := states[statePickleCluster].(int32)
			states[statePickleCluster] = n + 1
			return blocks.StateID(nameSeaPickle, states)
		}
	}
	return pl.Block, true
}
