package session

import "github.com/helix-bedrock/helix/server/world"

// hashRegistry gives the client block network hashes instead of runtime IDs.
// Helix turns on StartGame.UseBlockNetworkIDHashes, so every block ID sent
// to or read from the client (block updates, items, particles, sounds) is a
// hash of the block state, and the order of the server's block state list
// does not have to match the client's.
type hashRegistry struct{ world.BlockRegistry }

// BlockRuntimeID returns the network hash of b.
func (r hashRegistry) BlockRuntimeID(b world.Block) uint32 {
	rid := r.BlockRegistry.BlockRuntimeID(b)
	if h, ok := r.RuntimeIDToHash(rid); ok {
		return h
	}
	return rid
}

// BlockByRuntimeID returns the block with network hash h.
func (r hashRegistry) BlockByRuntimeID(h uint32) (world.Block, bool) {
	rid, ok := r.HashToRuntimeID(h)
	if !ok {
		return nil, false
	}
	return r.BlockRegistry.BlockByRuntimeID(rid)
}

// BlockByRuntimeIDOrAir returns the block with network hash h, or air.
func (r hashRegistry) BlockByRuntimeIDOrAir(h uint32) world.Block {
	if b, ok := r.BlockByRuntimeID(h); ok {
		return b
	}
	return r.Air()
}
