package world

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// UpdateBlockFlags are BDS's flags on the UpdateBlock it sends for a block
// set by gameplay: neighbours and network.
const UpdateBlockFlags = 3

// chunkPacket is a packet for every player whose client has a chunk.
type chunkPacket struct {
	chunk  mcdb.ChunkPos
	pk     packet.Packet
	except *Viewer
}

// Broadcast sends pk at the end of the tick to every player whose client
// was sent the chunk of pos, except the player of except (nil for none),
// in the order the packets were queued; block changes go the same way.
// Runs: owner.
func (r *Region) Broadcast(pos cube.Pos, pk packet.Packet, except *Viewer) {
	r.chunkPackets = append(r.chunkPackets, chunkPacket{chunk: chunkAt(pos), pk: pk, except: except})
}

// updateBlock queues the UpdateBlock of a block set on layer.
func (r *Region) updateBlock(pos cube.Pos, layer uint8, block uint32) {
	r.Broadcast(pos, &packet.UpdateBlock{
		Position:          protocol.BlockPos{int32(pos[0]), int32(pos[1]), int32(pos[2])}, //nolint:gosec // block coordinates fit int32
		NewBlockRuntimeID: r.d.conf.Blocks.Hash(block),
		Flags:             UpdateBlockFlags,
		Layer:             uint32(layer),
	}, nil)
}

func chunkAt(pos cube.Pos) mcdb.ChunkPos {
	return mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)} //nolint:gosec // block X and Z fit int32 chunks
}

// sendChunkPackets sends the packets queued this tick: to the players of
// this region now, to those of other regions on their next tick.
func (r *Region) sendChunkPackets() {
	if len(r.chunkPackets) == 0 {
		return
	}
	r.d.mu.Lock()
	for v := range r.d.viewers {
		list := r.packetsFor(v)
		if len(list) == 0 {
			continue
		}
		if v.region == r {
			sendAll(v, list)
			continue
		}
		if o := v.region; o != nil && o.state != stateDead {
			o.post(entry{v: v, fn: func(*Region) { sendAll(v, list) }})
		}
	}
	r.d.mu.Unlock()
	clear(r.chunkPackets)
	r.chunkPackets = r.chunkPackets[:0]
}

// packetsFor returns the queued packets for chunks v's client was sent.
func (r *Region) packetsFor(v *Viewer) []packet.Packet {
	var list []packet.Packet
	v.netMu.Lock()
	defer v.netMu.Unlock()
	for _, c := range r.chunkPackets {
		if c.except == v {
			continue
		}
		if _, ok := v.nets[c.chunk]; ok {
			list = append(list, c.pk)
		}
	}
	return list
}

func sendAll(v *Viewer, pks []packet.Packet) {
	for _, pk := range pks {
		v.send(pk)
	}
	v.unflushed = true
}
