package world

import (
	"maps"

	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Border blocks wall off their whole column, as BDS 1.26.60 keeps it: each chunk has a map
// of its 256 columns (LevelChunk mBorderBlockMap), set when a border block is placed
// (BorderBlock onPlace 0xCB7FCE0, server side only) and cleared when one is removed and no
// other is left in the column (onRemove 0xCB7FD60, LevelChunk setBorder 0xCF91C40). The map
// is stored as chunk tag 56 and sent after the biomes of LevelChunk, both as a count byte
// and then the column indices, z*16+x, rising (0xCF996D0); the client walls the columns off
// from that alone. BDS sends no chunk again when the map changes, its server-side movement
// checks the players; Helix leaves player movement to the client, so it sends the chunk
// again to everyone who has it.
const (
	// borderTag is the chunk tag of the border map.
	borderTag byte = 0x38
	// borderBlockName is the block that walls its column off.
	borderBlockName = "minecraft:border_block"
	// columns is how many columns a chunk has.
	columns = 256
)

// borderChanged keeps c's border map when the block at pos went from old to block, and marks
// the chunk to go to its viewers again when the map changed.
func (r *Region) borderChanged(c *Chunk, pos cube.Pos, old, block uint32) {
	blocks := r.d.conf.Blocks
	placed := blocks.Name(block) == borderBlockName
	if !placed && blocks.Name(old) != borderBlockName {
		return
	}
	col := byte(pos[2]&15)<<4 | byte(pos[0]&15) //nolint:gosec // masked
	on := placed || r.columnHasBorder(pos)
	set := decodeBorder(c.col.Extra[borderTag])
	if set[col] == on {
		return
	}
	set[col] = on
	// The map is shared with saves on workers: it is replaced, never changed in place.
	extra := maps.Clone(c.col.Extra)
	if extra == nil {
		extra = map[byte][]byte{}
	}
	if b := encodeBorder(&set); b != nil {
		extra[borderTag] = b
	} else {
		delete(extra, borderTag)
	}
	c.col.Extra = extra
	c.border = borderBits(extra[borderTag])
	c.dirty, c.resendNet = true, true
	if !c.encoding {
		r.encode(c)
	}
}

// columnHasBorder reports whether a border block is left in the column of pos.
func (r *Region) columnHasBorder(pos cube.Pos) bool {
	blocks := r.d.conf.Blocks
	rng := r.d.Range()
	for y := rng[0]; y <= rng[1]; y++ {
		if b, ok := r.Block(cube.Pos{pos[0], y, pos[2]}, 0); ok && blocks.Name(b) == borderBlockName {
			return true
		}
	}
	return false
}

// decodeBorder reads a stored border map; what does not parse counts as no border.
func decodeBorder(b []byte) (set [columns]bool) {
	if len(b) == 0 || len(b) != 1+int(b[0]) {
		return set
	}
	for _, i := range b[1:] {
		set[i] = true
	}
	return set
}

// encodeBorder writes a border map as BDS stores it, or nil when no column has a border.
func encodeBorder(set *[columns]bool) []byte {
	n := 0
	for _, on := range set {
		if on {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	b := make([]byte, 1, 1+n)
	b[0] = byte(n) //nolint:gosec // at most 255: a full map of 256 would wrap, as in BDS
	for i, on := range set {
		if on {
			b = append(b, byte(i))
		}
	}
	return b
}

// netBorder is the border map as LevelChunk carries it: a count byte of 0 for none.
func netBorder(b []byte) []byte {
	if len(b) == 0 || len(b) != 1+int(b[0]) {
		return []byte{0}
	}
	return b
}

// borderBits is a stored border map as a bit per column.
func borderBits(b []byte) (bits [columns / 64]uint64) {
	set := decodeBorder(b)
	for i, on := range set {
		if on {
			bits[i/64] |= 1 << (i % 64)
		}
	}
	return bits
}

// BorderColumn reports whether the column x, z holds a border block, which walls the whole
// column off while border blocks are on, and whether its chunk is loaded in this region.
// Runs: owner, also from Parallel.
func (r *Region) BorderColumn(x, z int) (border, loaded bool) {
	if !r.ticking.Load() {
		return false, false
	}
	c, ok := r.chunks[mcdb.ChunkPos{int32(x >> 4), int32(z >> 4)}] //nolint:gosec // block X and Z fit int32 chunks
	if !ok {
		return false, false
	}
	if r.d.bordersOff.Load() {
		return false, true
	}
	i := (z&15)<<4 | x&15
	return c.border[i/64]&(1<<(i%64)) != 0, true
}

// SetBorderBlocks turns the walls of border blocks on or off. BDS 1.26.60 has no such
// switch: border blocks always wall their column off. Off, the blocks stay and their map is
// still kept and saved, but no column is walled off: clients are sent no border columns and
// the server's own checks (mobs, building) see none. Every loaded chunk with border columns
// goes to its players again. Chunks players are only shown, outside every region, keep what
// they were sent until they are sent again. Not saved: a restart turns them on. Safe from
// any goroutine.
func (d *Dimension) SetBorderBlocks(on bool) {
	if d.bordersOff.Swap(!on) == !on {
		return
	}
	d.cache.clear()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.regions {
		r.post(entry{fn: (*Region).resendBorders, every: true})
	}
}

// BorderBlocks reports whether border blocks wall their columns off (see SetBorderBlocks).
// Safe from any goroutine.
func (d *Dimension) BorderBlocks() bool { return !d.bordersOff.Load() }

// netBorderOf is the border map a chunk is sent with: none while border blocks are off.
func (d *Dimension) netBorderOf(b []byte) []byte {
	if d.bordersOff.Load() {
		return nil
	}
	return b
}

// resendBorders encodes every chunk of the region with border columns again and sends it to
// the players that have it, after border blocks were turned on or off.
func (r *Region) resendBorders() {
	for _, c := range r.chunks {
		if c.border == [columns / 64]uint64{} {
			continue
		}
		c.resendNet = true
		if c.encoding {
			c.staleNet = true
			continue
		}
		r.encode(c)
	}
}
