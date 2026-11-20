package world

import (
	"bytes"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
)

// Reading back a sub chunk as a network chunk holds it, for revealing a hidden sub chunk of a
// chunk the region does not hold (past the simulation distance): its blocks go to the client
// as block updates, which it applies without redrawing the chunk (subhide.go).

// netLayer is one block layer of an encoded sub chunk: the bits per index, the packed indices
// (cell x<<8 | z<<4 | y, 32/bits a word from the low bits up) and the palette of block hashes.
type netLayer struct {
	bits    int
	words   []uint32
	palette []uint32
}

// at returns the block hash of cell.
func (l *netLayer) at(cell int) uint32 {
	if l.bits == 0 {
		return l.palette[0]
	}
	per := 32 / l.bits
	w := l.words[cell/per]
	i := int(w>>(uint(cell%per)*uint(l.bits))) & (1<<l.bits - 1)
	if i >= len(l.palette) {
		return l.palette[0]
	}
	return l.palette[i]
}

// readNetLayers reads the block layers of payload, a sub chunk in NetworkHashEncoding: version 9,
// the layer count and the sub chunk Y, then each layer. False if it cannot be read.
func readNetLayers(payload []byte) ([]netLayer, bool) {
	buf := bytes.NewBuffer(payload)
	head := buf.Next(3)
	if len(head) != 3 || head[0] != chunk.SubChunkVersion {
		return nil, false
	}
	layers := make([]netLayer, 0, head[1])
	for range int(head[1]) {
		l, ok := readNetLayer(buf)
		if !ok {
			return nil, false
		}
		layers = append(layers, l)
	}
	return layers, true
}

// readNetLayer reads one layer: its header byte (bits per index, shifted, and the network flag),
// its words, then its palette, counted unless the layer has 0 bits per index.
func readNetLayer(buf *bytes.Buffer) (netLayer, bool) {
	h, err := buf.ReadByte()
	if err != nil {
		return netLayer{}, false
	}
	l := netLayer{bits: int(h >> 1)}
	n := chunk.WordCount(l.bits)
	if n < 0 {
		return netLayer{}, false
	}
	data := buf.Next(n * 4)
	if len(data) != n*4 {
		return netLayer{}, false
	}
	l.words = make([]uint32, n)
	for i := range l.words {
		l.words[i] = uint32(data[i*4]) | uint32(data[i*4+1])<<8 | uint32(data[i*4+2])<<16 | uint32(data[i*4+3])<<24
	}
	count := int32(1)
	if l.bits != 0 {
		if protocol.Varint32(buf, &count) != nil || count <= 0 || count > subCells {
			return netLayer{}, false
		}
	}
	l.palette = make([]uint32, count)
	for i := range l.palette {
		var v int32
		if protocol.Varint32(buf, &v) != nil {
			return netLayer{}, false
		}
		l.palette[i] = uint32(v) //nolint:gosec // a hash, kept as its bits
	}
	return l, true
}

// appendNetSubBlocks adds to out the blocks of sub chunk index i of nc, at sub chunk Y y, that
// differ from its solid stand-in, and its second layer, as block updates, in mode (appendSubBlocks). Its ores and base
// blocks are disguised in nc already; its block entity data is not sent.
func (r *Region) appendNetSubBlocks(out []packet.Packet, nc *NetChunk, i, y int32, mode subMode) []packet.Packet {
	t := r.d.hide
	if i < 0 || int(i) >= len(nc.subs) || int(i) >= len(t.fakeBlock) {
		return out
	}
	layers, ok := readNetLayers(nc.subs[i].payload)
	if !ok || len(layers) == 0 {
		return out
	}
	blocks := r.d.conf.Blocks
	fake, air := blocks.Hash(t.fakeBlock[i]), blocks.Hash(blocks.Air())
	pk := &packet.UpdateSubChunkBlocks{Position: protocol.BlockPos{nc.pos[0] << 4, y << 4, nc.pos[1] << 4}}
	for cell := range subCells {
		at := protocol.BlockPos{nc.pos[0]<<4 | int32(cell>>8), y<<4 + int32(cell&15), nc.pos[1]<<4 | int32(cell>>4&15)} //nolint:gosec // cell < 4096
		h := layers[0].at(cell)
		liquid := r.liquidHash(h)
		switch {
		case mode == subLiquids && !liquid:
		case mode == subDry && liquid:
			pk.Blocks = append(pk.Blocks, protocol.BlockChangeEntry{BlockPos: at, BlockRuntimeID: air, Flags: UpdateBlockFlags})
		case h != fake:
			pk.Blocks = append(pk.Blocks, protocol.BlockChangeEntry{BlockPos: at, BlockRuntimeID: h, Flags: UpdateBlockFlags})
		}
		if len(layers) > 1 && mode != subDry {
			if h := layers[1].at(cell); h != air {
				pk.Extra = append(pk.Extra, protocol.BlockChangeEntry{BlockPos: at, BlockRuntimeID: h, Flags: UpdateBlockFlags})
			}
		}
	}
	return append(out, pk)
}

// liquidHash reports whether the block of hash h is water or lava.
func (r *Region) liquidHash(h uint32) bool {
	b, ok := r.d.conf.Blocks.HashToRuntimeID(h)
	return ok && r.d.hide.isLiquid(b)
}
