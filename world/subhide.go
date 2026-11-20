package world

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Hidden sub chunks: Helix's own anti X-ray, beyond BDS, on top of hide.go. A sub chunk under
// the ground in every column (below the render height map, so not under open sky or water) can
// only be seen from inside the caves in it, so a client far from it is answered a sub chunk of
// solid stone instead (deepslate below Y 0, netherrack in the Nether, end stone in the End): an
// X-ray sees no caves, lava, ores or chests there, and block changes in it are not sent either.
// When the player comes within the sub chunk reveal radius of one, the chunk is announced again
// and the client asks for its sub chunks, and is answered the real one, which it keeps.

// DefaultSubRevealRadius is how close, in blocks, a player must be to an underground sub chunk
// to be sent the real one.
const DefaultSubRevealRadius = 32

const (
	coverEndStone = "minecraft:end_stone"
	// maxHiddenSubs is how many sub chunks of a chunk a bit mask holds.
	maxHiddenSubs = 64
)

// newFakeSubs returns the solid sub chunk a client far away is answered for each sub chunk
// index of a dimension with range r, and the block each is made of; nil if a cover block is
// missing.
func newFakeSubs(conf Config, r cube.Range) ([][]byte, []uint32) {
	blocks := conf.Blocks
	c := chunk.New(blocks, r)
	n := len(c.Sub())
	if n > maxHiddenSubs {
		return nil, nil
	}
	covers := make([]uint32, n)
	for i := range n {
		base := c.SubY(int16(i)) //nolint:gosec // few sections
		cover, ok := blocks.Block(fakeCover(conf.Dim, base))
		if !ok {
			return nil, nil
		}
		covers[i] = cover
		for y := range int16(16) {
			for z := range uint8(16) {
				for x := range uint8(16) {
					c.SetBlock(x, base+y, z, 0, cover)
				}
			}
		}
	}
	c.Compact()
	fake := make([][]byte, n)
	for i := range n {
		fake[i] = chunk.EncodeSubChunk(c, chunk.NetworkHashEncoding, i)
	}
	return fake, covers
}

// fakeCover is the block a hidden sub chunk starting at Y base is made of.
func fakeCover(dim mcdb.Dimension, base int16) string {
	switch {
	case dim == mcdb.Nether:
		return coverNetherrack
	case dim == mcdb.End:
		return coverEndStone
	case base < 0:
		return coverDeepslate
	}
	return coverStone
}

// SetEye tells the viewer where the player is, for which sub chunks it is sent as they are.
// Runs: anywhere.
func (v *Viewer) SetEye(pos mgl32.Vec3) {
	v.netMu.Lock()
	v.eye, v.eyeSet = pos, true
	v.netMu.Unlock()
}

// subEntry answers the client's request for sub chunk y of nc, at pos, as it may be shown: the
// solid one while it is underground and far. Under netMu.
func (v *Viewer) subEntry(pos mcdb.ChunkPos, nc *NetChunk, off protocol.SubChunkOffset, y int32) protocol.SubChunkEntry {
	e := nc.entry(off, y)
	t := v.d.hide
	i := y - nc.bottom
	if e.Result != protocol.SubChunkResultSuccess || t == nil || t.fake == nil || !v.eyeSet ||
		int(i) >= len(t.fake) || nc.underground&(1<<i) == 0 {
		return e
	}
	st := v.revealOf(pos, nc)
	bit := uint64(1) << i
	if st.real&bit != 0 {
		return e
	}
	if v.d.subNear(v.eye, pos, y) || v.visibleSub(pos, y) {
		st.real |= bit
		return e
	}
	st.fake |= bit
	e.RawPayload = protocol.Option(t.fake[i])
	return e
}

// revealOf returns what the client was sent of the chunk at pos, which it has as nc. Under
// netMu.
func (v *Viewer) revealOf(pos mcdb.ChunkPos, nc *NetChunk) *revealState {
	if v.reveal == nil {
		v.reveal = map[mcdb.ChunkPos]*revealState{}
	}
	st := v.reveal[pos]
	if st == nil || st.net != nc {
		st = &revealState{net: nc}
		v.reveal[pos] = st
	}
	return st
}

// revealSubs sends the client, as block updates, the real blocks of the solid stand-in sub chunks
// it was answered that are now within the sub chunk reveal radius of eye or in its view
// (walkVisible). Block updates are
// applied by every client; a chunk announced again is redrawn whole, which flickers, and a
// SubChunk packet it did not ask for may be dropped. Runs: the viewer's region, under netMu; the
// packets go in out.
func (v *Viewer) revealSubs(r *Region, eye mgl32.Vec3, out []packet.Packet) []packet.Packet {
	v.walkVisible(r, eye)
	for pos, st := range v.reveal {
		nc := v.nets[pos]
		if st.fake == 0 || nc == nil || st.net != nc {
			continue
		}
		var want uint64
		for i := range int32(maxHiddenSubs) {
			y := nc.bottom + i
			if st.fake&(1<<i) != 0 && (r.d.subNear(eye, pos, y) || v.visibleSub(pos, y)) {
				want |= 1 << i
			}
		}
		if want == 0 {
			continue
		}
		c, loaded := r.chunks[pos]
		// Past the simulation distance only its network form is here: read back from that.
		latest := nc
		if !loaded {
			if cur := r.netChunk(pos); cur != nil {
				latest = cur
			}
		}
		st.real, st.fake = st.real|want, st.fake&^want
		st.forgetIn(want)
		for i := range int32(maxHiddenSubs) {
			if want&(1<<i) == 0 {
				continue
			}
			if loaded {
				out = r.appendSubBlocks(out, c, i, nc.bottom+i)
			} else {
				out = r.appendNetSubBlocks(out, latest, i, nc.bottom+i)
			}
		}
	}
	return out
}

// appendSubBlocks adds to out the blocks of sub chunk index i of c, at sub chunk Y y, that differ
// from its solid stand-in (ores and base blocks still disguised, for the line of sight to show),
// its second layer (water in blocks), and the block entity data of the blocks not disguised.
func (r *Region) appendSubBlocks(out []packet.Packet, c *Chunk, i, y int32) []packet.Packet {
	t := r.d.hide
	subs := c.col.Chunk.Sub()
	if int(i) >= len(subs) || int(i) >= len(t.fakeBlock) {
		return out
	}
	sub, fake := subs[i], t.fakeBlock[i]
	blocks := r.d.conf.Blocks
	air := blocks.Air()
	pk := &packet.UpdateSubChunkBlocks{Position: protocol.BlockPos{c.pos[0] << 4, y << 4, c.pos[1] << 4}}
	for by := range byte(16) {
		for bz := range byte(16) {
			for bx := range byte(16) {
				at := protocol.BlockPos{c.pos[0]<<4 | int32(bx), y<<4 + int32(by), c.pos[1]<<4 | int32(bz)}
				b := sub.Block(bx, by, bz, 0)
				if cover := t.coverOf(b); cover != noCover {
					b = cover
				}
				if b != fake {
					pk.Blocks = append(pk.Blocks, protocol.BlockChangeEntry{BlockPos: at, BlockRuntimeID: blocks.Hash(b), Flags: UpdateBlockFlags})
				}
				if w := sub.Block(bx, by, bz, 1); w != air {
					pk.Extra = append(pk.Extra, protocol.BlockChangeEntry{BlockPos: at, BlockRuntimeID: blocks.Hash(w), Flags: UpdateBlockFlags})
				}
			}
		}
	}
	out = append(out, pk)
	for p := range c.blockEntities {
		if int32(p[1]>>4) != y { //nolint:gosec // block Y fits
			continue
		}
		if b, ok := r.Block(p, 0); ok && t.coverOf(b) == noCover {
			out = r.appendBlockEntity(out, p)
		}
	}
	return out
}

// forgetIn takes off done the blocks in the sub chunks marked in subs: they were just sent
// disguised.
func (st *revealState) forgetIn(subs uint64) {
	for p := range st.done {
		if i := int32(p[1]>>4) - st.net.bottom; i >= 0 && i < maxHiddenSubs && subs&(1<<i) != 0 { //nolint:gosec // block Y fits
			delete(st.done, p)
		}
	}
}

// hidesEntityAt reports whether the client has only the solid stand-in of the sub chunk holding
// pos: an entity there is not shown, as the client could not see it (Region.visibility). Runs:
// anywhere.
func (v *Viewer) hidesEntityAt(pos mgl32.Vec3) bool {
	if v.d.hide == nil || v.d.hide.fake == nil {
		return false
	}
	b := blockAt(pos)
	at := chunkAt(b)
	v.netMu.Lock()
	defer v.netMu.Unlock()
	st, nc := v.reveal[at], v.nets[at]
	if st == nil || nc == nil || st.net != nc {
		return false
	}
	i := int32(b[1]>>4) - nc.bottom //nolint:gosec // block Y fits
	if i < 0 || i >= maxHiddenSubs {
		return false
	}
	bit := uint64(1) << i
	return st.fake&bit != 0 && st.real&bit == 0
}

// hiddenUpdate reports whether pk changes a block in a sub chunk the client was answered solid:
// it is not sent, the real sub chunk carries it when revealed. Under netMu.
func (v *Viewer) hiddenUpdate(pos mcdb.ChunkPos, pk packet.Packet) bool {
	u, ok := pk.(*packet.UpdateBlock)
	if !ok {
		return false
	}
	st := v.reveal[pos]
	nc := v.nets[pos]
	if st == nil || nc == nil || st.net != nc {
		return false
	}
	i := u.Position[1]>>4 - nc.bottom
	if i < 0 || i >= maxHiddenSubs {
		return false
	}
	bit := uint64(1) << i
	return st.fake&bit != 0 && st.real&bit == 0
}

// subNear reports whether the sub chunk at chunk pos and sub chunk Y y is within the sub chunk
// reveal radius of eye.
func (d *Dimension) subNear(eye mgl32.Vec3, pos mcdb.ChunkPos, y int32) bool {
	lo := mgl32.Vec3{float32(pos[0] << 4), float32(y << 4), float32(pos[1] << 4)}
	var d2 float32
	for a := range 3 {
		var gap float32
		switch {
		case eye[a] < lo[a]:
			gap = lo[a] - eye[a]
		case eye[a] > lo[a]+16:
			gap = eye[a] - lo[a] - 16
		}
		d2 += gap * gap
	}
	radius := float32(d.subRevealRadius())
	return d2 <= radius*radius
}

// subRevealRadius is Config.SubRevealRadius, or DefaultSubRevealRadius.
func (d *Dimension) subRevealRadius() int {
	if d.conf.SubRevealRadius > 0 {
		return int(d.conf.SubRevealRadius)
	}
	return DefaultSubRevealRadius
}
