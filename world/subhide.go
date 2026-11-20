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
// index of a dimension with range r, nil if a cover block is missing.
func newFakeSubs(conf Config, r cube.Range) [][]byte {
	blocks := conf.Blocks
	c := chunk.New(blocks, r)
	n := len(c.Sub())
	if n > maxHiddenSubs {
		return nil
	}
	for i := range n {
		base := c.SubY(int16(i)) //nolint:gosec // few sections
		cover, ok := blocks.Block(fakeCover(conf.Dim, base))
		if !ok {
			return nil
		}
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
	return fake
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
	if v.d.subNear(v.eye, pos, y) {
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

// revealSubs announces again the chunks around eye with a solid sub chunk now within the sub
// chunk reveal radius, from their latest network form, so the client asks for them and gets the
// real ones. Runs: the viewer's region, under netMu; the announcements go in out.
func (v *Viewer) revealSubs(r *Region, eye mgl32.Vec3, out []packet.Packet) []packet.Packet {
	radius := r.d.subRevealRadius()
	reach := int32(radius+15) >> 4 //nolint:gosec // small
	centre := chunkAt(blockAt(eye))
	encodedNow := false
	for dx := -reach; dx <= reach; dx++ {
		for dz := -reach; dz <= reach; dz++ {
			pos := mcdb.ChunkPos{centre[0] + dx, centre[1] + dz}
			nc, ok := v.nets[pos]
			st := v.reveal[pos]
			if !ok || st == nil || st.net != nc || st.fake == 0 {
				continue
			}
			var want uint64
			for i := range int32(maxHiddenSubs) {
				if st.fake&(1<<i) != 0 && r.d.subNear(eye, pos, nc.bottom+i) {
					want |= 1 << i
				}
			}
			if want == 0 {
				continue
			}
			cur := r.netChunk(pos)
			if cur == nil {
				c, ok := r.chunks[pos]
				if !ok || encodedNow {
					continue // next scan
				}
				// A chunk that changes every tick never keeps a worker's encode (it is stale by
				// the time it comes back), and the player would walk into stone that is not
				// there: encode it here, one a scan.
				cur, encodedNow = r.encodeNow(c), true
			}
			v.nets[pos] = cur
			v.reveal[pos] = &revealState{net: cur, real: st.real | want}
			out = append(out, cur.level)
		}
	}
	return out
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

// encodeNow encodes c on the tick and keeps the result as its network form, which is current
// until the chunk changes again. An encode still on a worker is checked as usual when it is
// back.
func (r *Region) encodeNow(c *Chunk) *NetChunk {
	d := r.d
	nc := encodeNetChunk(c.pos, d.conf.Dim, c.col.Chunk.Snapshot(), d.netBorderOf(c.col.Extra[borderTag]),
		c.networkBlockEntities(), d.light, d.hide)
	c.net = nc
	d.publish(c.pos, nc)
	return nc
}
