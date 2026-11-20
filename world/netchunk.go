package world

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// NetChunk is a chunk encoded for the client once and shared by every
// player who sees it. It never changes; a changed chunk gets a new one.
//
// Chunks go out in BDS's sub-chunk request mode: LevelChunk carries the
// biomes, then the client asks for each sub chunk with SubChunkRequest.
// The packet fields follow a capture of BDS 1.26.60 sending chunks.
type NetChunk struct {
	pos    mcdb.ChunkPos
	dim    int32
	bottom int32 // absolute sub chunk Y of section 0
	level  *packet.LevelChunk
	subs   []netSub
	// edges are the disguisable blocks on the chunk's sides (hide.go).
	edges []hiddenEdge
	size  int
}

// netSub is one sub chunk as SubChunk answers it.
type netSub struct {
	payload    []byte
	hmType     byte
	renderType byte
	hm         protocol.HeightMap
	// render is the render height map, only where it differs from hm: most
	// sections have no water in them.
	render *protocol.HeightMap
}

// columnHeights is a height for each column of a chunk, indexed by x | z<<4.
type columnHeights [256]int16

// encodeNetChunk encodes c, which must not change while this runs: pass a
// snapshot or a chunk no other goroutine has yet.
func encodeNetChunk(pos mcdb.ChunkPos, dim mcdb.Dimension, c *chunk.Chunk, border []byte, entities []chunk.BlockEntity, light *lightTable, hide *hideTable, show []cube.Pos) *NetChunk {
	r := c.Range()
	nc := &NetChunk{pos: pos, dim: int32(dim), bottom: int32(r[0] >> 4)} //nolint:gosec // dimension IDs and heights are small
	count := c.HighestFilledSubChunk()
	// Biomes, then the border map. Block entities go with their sub chunk.
	payload := append(chunk.EncodeBiomes(c, chunk.NetworkHashEncoding), netBorder(border)...)
	// BDS sends no sub chunks in the packet (count 0) and tells the
	// client how many to request, from the bottom, as the limit.
	nc.level = &packet.LevelChunk{
		Position: protocol.ChunkPos{pos[0], pos[1]}, Dimension: nc.dim,
		SubChunkLimit: protocol.Option(int32(count)), RawPayload: payload,
	}
	nc.size = len(payload)
	var heights, render columnHeights
	for col := range heights {
		x, z := col&15, col>>4
		heights[col] = light.columnTop(c, x, z, lightStopsSky)
		render[col] = light.columnTop(c, x, z, lightStopsRender)
	}
	// Heights come from the real blocks; the client is sent the disguised ones (hide.go).
	shown, edges := hide.apply(c, pos, show)
	nc.edges = edges
	subs := c.Sub()
	nc.subs = make([]netSub, len(subs))
	for i := range subs {
		s := &nc.subs[i]
		s.payload = chunk.EncodeSubChunk(shown, chunk.NetworkHashEncoding, i)
		for _, be := range entities {
			if int(c.SubY(int16(i))) != be.Pos[1]&^15 { //nolint:gosec // few sections
				continue
			}
			// A compound that cannot be encoded is left out; the block still shows.
			if data, err := nbt.MarshalEncoding(be.Data, nbt.NetworkLittleEndian); err == nil {
				s.payload = append(s.payload, data...)
			}
		}
		nc.size += len(s.payload)
		base := int(c.SubY(int16(i))) //nolint:gosec // few sections
		s.hmType, s.hm = subHeightMap(&heights, base)
		s.setRender(&render, base)
	}
	return nc
}

// setRender sets the render height map of the section starting at Y base, as
// BDS sends it: "all copied" when it has data equal to the height map.
func (s *netSub) setRender(render *columnHeights, base int) {
	kind, hm := subHeightMap(render, base)
	s.renderType = kind
	if kind != protocol.HeightMapDataHasData {
		return
	}
	if s.hmType == protocol.HeightMapDataHasData && hm == s.hm {
		s.renderType = protocol.HeightMapDataAllCopied
		return
	}
	s.render = &hm
}

// subHeightMap is the client's height map of the section starting at Y
// base: each column's height relative to it, -1 below and 16 above, and
// one of the "all too low/high" types when every column agrees.
func subHeightMap(heights *columnHeights, base int) (byte, protocol.HeightMap) {
	var hm protocol.HeightMap
	low, high := true, true
	for z := range 16 {
		for x := range 16 {
			rel := int(heights[x|z<<4]) - base
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
	// BDS answers Success for every sub chunk, air ones included.
	e.Result, e.RawPayload = protocol.SubChunkResultSuccess, protocol.Option(s.payload)
	e.HeightMapType, e.RenderHeightMapType = s.hmType, s.renderType
	if s.hmType == protocol.HeightMapDataHasData {
		e.HeightMapData = protocol.Option(s.hm)
	}
	if s.render != nil {
		e.RenderHeightMapData = protocol.Option(*s.render)
	}
	return e
}
