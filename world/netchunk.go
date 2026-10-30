package world

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/mcdb"
)

// NetChunk is a chunk encoded for the client once and shared by every
// player who sees it. It never changes; a changed chunk gets a new one.
//
// Chunks go out in BDS's sub-chunk request mode: LevelChunk carries the
// biomes, then the client asks for each sub chunk with SubChunkRequest.
type NetChunk struct {
	pos    mcdb.ChunkPos
	dim    int32
	bottom int32 // absolute sub chunk Y of section 0
	level  *packet.LevelChunk
	subs   []netSub
	size   int
}

// netSub is one sub chunk as SubChunk answers it.
type netSub struct {
	payload []byte // nil when all air
	hmType  byte
	hm      protocol.HeightMap
}

// encodeNetChunk encodes c, which must not change while this runs: pass a
// snapshot or a chunk no other goroutine has yet.
func encodeNetChunk(pos mcdb.ChunkPos, dim mcdb.Dimension, c *chunk.Chunk) *NetChunk {
	r := c.Range()
	nc := &NetChunk{pos: pos, dim: int32(dim), bottom: int32(r[0] >> 4)} //nolint:gosec // dimension IDs and heights are small
	count := c.HighestFilledSubChunk()
	// Biomes, then no border blocks. Block entities are sent with block
	// behaviour, which knows what the client may see of them.
	payload := append(chunk.EncodeBiomes(c, chunk.NetworkHashEncoding), 0)
	nc.level = &packet.LevelChunk{
		Position: protocol.ChunkPos{pos[0], pos[1]}, Dimension: nc.dim,
		SubChunkCount: uint32(count), SubChunkLimit: protocol.Option(int32(count)),
		RawPayload: payload,
	}
	nc.size = len(payload)
	heights := c.HeightMap()
	subs := c.Sub()
	nc.subs = make([]netSub, len(subs))
	for i, sub := range subs {
		s := &nc.subs[i]
		if !sub.Empty() {
			s.payload = chunk.EncodeSubChunk(c, chunk.NetworkHashEncoding, i)
			nc.size += len(s.payload)
		}
		s.hmType, s.hm = subHeightMap(heights, int(c.SubY(int16(i)))) //nolint:gosec // few sections
	}
	return nc
}

// subHeightMap is the client's height map of the section starting at Y
// base: each column's height relative to it, -1 below and 16 above, and
// one of the "all too low/high" types when every column agrees.
func subHeightMap(heights chunk.HeightMap, base int) (byte, protocol.HeightMap) {
	var hm protocol.HeightMap
	low, high := true, true
	for z := range uint8(16) {
		for x := range uint8(16) {
			rel := int(heights.At(x, z)) - base
			switch {
			case rel < 0:
				rel, high = -1, false
			case rel > 15:
				rel, low = 16, false
			default:
				low, high = false, false
			}
			hm[z][x] = int8(rel) //nolint:gosec // -1..16
		}
	}
	switch {
	case low:
		return protocol.HeightMapDataTooLow, hm
	case high:
		return protocol.HeightMapDataTooHigh, hm
	}
	return protocol.HeightMapDataHasData, hm
}

// Pos is the chunk's position.
func (nc *NetChunk) Pos() mcdb.ChunkPos { return nc.pos }

// LevelChunk is the packet that announces the chunk. It is shared: do not
// change it.
func (nc *NetChunk) LevelChunk() *packet.LevelChunk { return nc.level }

// Size is about how many bytes the encoded chunk holds.
func (nc *NetChunk) Size() int { return nc.size }

// entry answers a request for the sub chunk at absolute sub chunk Y y.
func (nc *NetChunk) entry(off protocol.SubChunkOffset, y int32) protocol.SubChunkEntry {
	e := protocol.SubChunkEntry{Offset: off}
	i := y - nc.bottom
	if i < 0 || int(i) >= len(nc.subs) {
		e.Result = protocol.SubChunkResultIndexOutOfBounds
		return e
	}
	s := &nc.subs[i]
	e.Result = protocol.SubChunkResultSuccessAllAir
	if s.payload != nil {
		e.Result = protocol.SubChunkResultSuccess
		e.RawPayload = protocol.Option(s.payload)
	}
	e.HeightMapType = s.hmType
	if s.hmType == protocol.HeightMapDataHasData {
		e.HeightMapData = protocol.Option(s.hm)
	}
	e.RenderHeightMapType = protocol.HeightMapDataAllCopied
	return e
}
