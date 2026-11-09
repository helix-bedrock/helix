package world

import (
	"sync"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Light follows BDS 1.26.60's SubChunkRelighter and LevelChunk height map:
//
//   - Each block has a light emission and a light dampening (its Block's +164 and +165
//     bytes, helix-data's light_emission and light_dampening). A cell counts the larger of
//     its two layers.
//   - Light entering a cell loses the cell's dampening, at least 1 (the relighter's
//     absorption, sub_CF53410 and sub_CF53660), so it fades by one a block through air.
//   - Block light starts at each cell's emission.
//   - Sky light is 15 from the top of the world down to the column's height: the cell
//     above the highest block that dampens light on either layer or is a half slab
//     (LevelChunk::_recalcHeight, sub_CF8F420). Below that it only spreads, so even
//     straight down under leaves it loses at least one a block.
//
// Light is not saved: BDS works it out when a chunk loads, and so does Helix, on the
// worker that loads the chunk for what lies inside it, then across its sides once it
// joins a region. A block change relights what it touches at the end of the region's
// tick, or before light is next read. Chunks not loaded in the region let no light
// through.

// MaxLight is the brightest light level.
const MaxLight = 15

// LightRules says how blocks give and take light. Every method may be called from any
// goroutine. The registry's values (BDS's) are the default.
type LightRules interface {
	// Emission is the light the block gives off, 0 to 15.
	Emission(block uint32) uint8
	// Dampening is how much light the block takes away, 0 to 15. A cell takes away at
	// least 1 whatever its blocks say.
	Dampening(block uint32) uint8
	// StopsSky reports whether the block ends a column's full sunlight. BDS stops it at
	// any block that dampens light and at half slabs.
	StopsSky(block uint32) bool
}

// SkyMode says whether a dimension has sky light.
type SkyMode uint8

// The sky modes.
const (
	// SkyDefault gives sky light to the overworld only, as BDS's dimensions do.
	SkyDefault SkyMode = iota
	// SkyOn gives the dimension sky light.
	SkyOn
	// SkyOff leaves the dimension without sky light: it is 0 everywhere.
	SkyOff
)

// RegistryLight is the default LightRules: the registry's values, which are BDS's.
type RegistryLight struct{ Blocks *registry.Blocks }

// Emission is the state's LightEmission.
func (l RegistryLight) Emission(block uint32) uint8 { return l.Blocks.LightBlock(block) }

// Dampening is the state's LightDampening.
func (l RegistryLight) Dampening(block uint32) uint8 { return l.Blocks.FilteringBlock(block) }

// StopsSky is BDS's height map test: dampening light, or a half slab.
func (l RegistryLight) StopsSky(block uint32) bool {
	p := l.Blocks.Properties(block)
	return p.LightDampening != 0 || p.HalfSlab
}

// Packed light facts of a block: dampening, emission and whether it stops the sky.
const (
	lightDampMask  = 0x0f
	lightEmitShift = 4
	lightStopsSky  = 1 << 8
)

// lightTable is the light facts of every block, packed, read on every light step.
type lightTable struct {
	rules LightRules
	info  []uint16
	sky   bool
}

func newLightTable(conf Config) *lightTable {
	rules := conf.Light
	if rules == nil {
		rules = RegistryLight{Blocks: conf.Blocks}
	}
	t := &lightTable{rules: rules, info: make([]uint16, conf.Blocks.Count())}
	for id := range t.info {
		t.info[id] = packLight(rules, uint32(id)) //nolint:gosec // registry IDs fit
	}
	switch conf.Sky {
	case SkyOn:
		t.sky = true
	case SkyDefault:
		t.sky = conf.Dim == mcdb.Overworld
	}
	return t
}

func packLight(rules LightRules, block uint32) uint16 {
	v := uint16(min(rules.Dampening(block), MaxLight)) | uint16(min(rules.Emission(block), MaxLight))<<lightEmitShift
	if rules.StopsSky(block) {
		v |= lightStopsSky
	}
	return v
}

// of is the packed facts of block.
func (t *lightTable) of(block uint32) uint16 {
	if int(block) < len(t.info) {
		return t.info[block]
	}
	return packLight(t.rules, block)
}

// cell is the packed facts of a cell from both its layers.
func (t *lightTable) cell(first, second uint16) uint16 {
	damp := max(first&lightDampMask, second&lightDampMask)
	emit := max(first>>lightEmitShift&lightDampMask, second>>lightEmitShift&lightDampMask)
	return damp | emit<<lightEmitShift | (first|second)&lightStopsSky
}

// absorption is what light loses entering a cell with packed facts v: its dampening,
// at least 1.
func absorption(v uint16) uint8 { return max(uint8(v&lightDampMask), 1) } //nolint:gosec // masked

func emission(v uint16) uint8 { return uint8(v >> lightEmitShift & lightDampMask) } //nolint:gosec // masked

// lightSub is one sub chunk's light: a nibble per cell, or one value for every cell
// while they are all the same.
type lightSub struct {
	sky, block         *[2048]byte
	skyFill, blockFill uint8
}

// lightCell is a cell's place in its sub chunk's nibbles.
func lightCell(x, y, z int) int { return x<<8 | z<<4 | y&15 }

func (s *lightSub) get(sky bool, i int) uint8 {
	arr, fill := s.block, s.blockFill
	if sky {
		arr, fill = s.sky, s.skyFill
	}
	if arr == nil {
		return fill
	}
	return arr[i>>1] >> (uint(i&1) << 2) & 0xf
}

func (s *lightSub) set(sky bool, i int, v uint8) {
	arr, fill := &s.block, s.blockFill
	if sky {
		arr, fill = &s.sky, s.skyFill
	}
	if *arr == nil {
		if v == fill {
			return
		}
		a := new([2048]byte)
		b := fill | fill<<4
		for j := range a {
			a[j] = b
		}
		*arr = a
	}
	shift := uint(i&1) << 2
	(*arr)[i>>1] = (*arr)[i>>1]&^(0xf<<shift) | v<<shift
}

// chunkLight is a chunk's light and its height map.
type chunkLight struct {
	subs []lightSub
	// height is BDS's height map: for each column (x | z<<4), the lowest Y in full
	// sunlight.
	height [256]int16
}

// lightScratch is a chunk's light worked out on its own: one value per cell, Y-major.
type lightScratch struct {
	cells      []uint16
	sky, block []uint8
	buckets    [MaxLight + 1][]int32
}

var lightScratches = sync.Pool{New: func() any { return &lightScratch{} }}

// lightColumn works out the light inside c alone, as BDS's original lighting does for
// a new chunk: sky light down to the height map, block light from emitters, both
// spread within the chunk. Light from the chunks beside it comes in when it joins a
// region (Region.joinLight). Runs: a worker.
func (d *Dimension) lightColumn(c *chunk.Chunk) *chunkLight {
	t := d.light
	height := d.r.Height() + 1
	n := height << 8
	s := lightScratches.Get().(*lightScratch) //nolint:forcetypeassert // the pool makes only these
	defer lightScratches.Put(s)
	s.cells = resize(s.cells, n)
	s.sky, s.block = resize(s.sky, n), resize(s.block, n)
	air := t.cell(t.of(d.conf.Blocks.Air()), 0)
	for i, sub := range c.Sub() {
		base := i << 12
		if base >= n {
			break
		}
		cells := s.cells[base:min(base+4096, n)]
		layers := sub.Layers()
		if len(layers) == 0 || sub.Empty() {
			for j := range cells {
				cells[j] = air
			}
			continue
		}
		for y := range 16 {
			for z := range 16 {
				for x := range 16 {
					j := y<<8 | z<<4 | x
					if j >= len(cells) {
						continue
					}
					first := t.of(layers[0].At(byte(x), byte(y), byte(z))) //nolint:gosec // 0 to 15
					var second uint16
					if len(layers) > 1 {
						second = t.of(layers[1].At(byte(x), byte(y), byte(z))) //nolint:gosec // 0 to 15
					}
					cells[j] = t.cell(first, second)
				}
			}
		}
	}
	l := &chunkLight{subs: make([]lightSub, (height+15)>>4)}
	for col := range 256 {
		y := height
		for y > 0 && s.cells[(y-1)<<8|col]&lightStopsSky == 0 {
			y--
		}
		l.height[col] = int16(y + d.r[0]) //nolint:gosec // world heights fit
	}
	for i := range s.buckets {
		s.buckets[i] = s.buckets[i][:0]
	}
	for i, v := range s.cells {
		s.sky[i], s.block[i] = 0, emission(v)
		if s.block[i] > 1 {
			s.buckets[s.block[i]] = append(s.buckets[s.block[i]], int32(i)) //nolint:gosec // fits
		}
	}
	spreadScratch(s, s.block, height)
	if t.sky {
		for col := range 256 {
			h := int(l.height[col]) - d.r[0]
			for y := h; y < height; y++ {
				s.sky[y<<8|col] = MaxLight
			}
			// Cells in full sunlight next to cells that are not: the column's top
			// cell, and the cells beside a higher column of this chunk.
			top := h
			x, z := col&15, col>>4
			for _, nb := range [4][2]int{{x - 1, z}, {x + 1, z}, {x, z - 1}, {x, z + 1}} {
				if nb[0] >= 0 && nb[0] < 16 && nb[1] >= 0 && nb[1] < 16 {
					top = max(top, int(l.height[nb[0]|nb[1]<<4])-d.r[0])
				}
			}
			for y := h; y <= top && y < height; y++ {
				s.buckets[MaxLight] = append(s.buckets[MaxLight], int32(y<<8|col)) //nolint:gosec // fits
			}
		}
		spreadScratch(s, s.sky, height)
	}
	for i := range l.subs {
		base := i << 12
		end := min(base+4096, n)
		pack(&l.subs[i], true, s.sky[base:end])
		pack(&l.subs[i], false, s.block[base:end])
	}
	return l
}

// spreadScratch spreads the light queued in s.buckets through vals, inside one chunk.
func spreadScratch(s *lightScratch, vals []uint8, height int) {
	n := len(vals)
	for level := MaxLight; level >= 2; level-- {
		for k := 0; k < len(s.buckets[level]); k++ {
			i := int(s.buckets[level][k])
			if int(vals[i]) != level {
				continue
			}
			x, z, y := i&15, i>>4&15, i>>8
			for f := range 6 {
				j := i
				switch f {
				case 0:
					if y == 0 {
						continue
					}
					j -= 256
				case 1:
					if y+1 >= height {
						continue
					}
					j += 256
				case 2:
					if z == 0 {
						continue
					}
					j -= 16
				case 3:
					if z == 15 {
						continue
					}
					j += 16
				case 4:
					if x == 0 {
						continue
					}
					j--
				case 5:
					if x == 15 {
						continue
					}
					j++
				}
				if j < 0 || j >= n {
					continue
				}
				a := int(absorption(s.cells[j]))
				if a >= level {
					continue
				}
				if next := uint8(level - a); next > vals[j] { //nolint:gosec // 1 to 14
					vals[j] = next
					s.buckets[next] = append(s.buckets[next], int32(j)) //nolint:gosec // fits
				}
			}
		}
		s.buckets[level] = s.buckets[level][:0]
	}
	s.buckets[1], s.buckets[0] = s.buckets[1][:0], s.buckets[0][:0]
}

// pack stores vals, Y-major, into sub's nibbles, as one value if they are all the same.
func pack(sub *lightSub, sky bool, vals []uint8) {
	same := len(vals) == 4096
	for _, v := range vals {
		if v != vals[0] {
			same = false
			break
		}
	}
	if same {
		if sky {
			sub.sky, sub.skyFill = nil, vals[0]
		} else {
			sub.block, sub.blockFill = nil, vals[0]
		}
		return
	}
	for i, v := range vals {
		y, z, x := i>>8, i>>4&15, i&15
		sub.set(sky, lightCell(x, y, z), v)
	}
}

func resize[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	return s[:n]
}

// lightNode is a cell and the light it had, for taking light away.
type lightNode struct {
	pos   cube.Pos
	level uint8
}

// lightWork is a region's working space for relighting, kept between uses.
type lightWork struct {
	dirty   []cube.Pos
	columns []cube.Pos
	remove  []lightNode
	removed []cube.Pos
	buckets [MaxLight + 1][]cube.Pos
	// last is the chunk found last, so cells next to each other skip the map.
	lastPos   mcdb.ChunkPos
	lastChunk *Chunk
}

// lightChunk is the chunk holding pos in this region, for light.
func (r *Region) lightChunk(pos cube.Pos) *Chunk {
	if pos[1] < r.d.r[0] || pos[1] > r.d.r[1] {
		return nil
	}
	cp := mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)} //nolint:gosec // block X and Z fit int32 chunks
	w := r.light
	if w.lastChunk != nil && w.lastPos == cp {
		return w.lastChunk
	}
	c := r.chunks[cp]
	if c == nil || c.light == nil {
		return nil
	}
	w.lastPos, w.lastChunk = cp, c
	return c
}

// lightAt is the light of kind at pos, false where no chunk with light is loaded.
func (r *Region) lightAt(sky bool, pos cube.Pos) (uint8, bool) {
	c := r.lightChunk(pos)
	if c == nil {
		return 0, false
	}
	return c.light.subs[(pos[1]-r.d.r[0])>>4].get(sky, lightCell(pos[0]&15, pos[1], pos[2]&15)), true
}

func (r *Region) setLight(c *Chunk, sky bool, pos cube.Pos, v uint8) {
	c.light.subs[(pos[1]-r.d.r[0])>>4].set(sky, lightCell(pos[0]&15, pos[1], pos[2]&15), v)
}

// cellLight is the packed facts of the cell at pos.
func (r *Region) cellLight(c *Chunk, pos cube.Pos) uint16 {
	t := r.d.light
	x, y, z := uint8(pos[0]&15), int16(pos[1]), uint8(pos[2]&15) //nolint:gosec // masked and range-checked
	return t.cell(t.of(c.col.Chunk.Block(x, y, z, 0)), t.of(c.col.Chunk.Block(x, y, z, 1)))
}

// baseLight is the light of kind the cell at pos has of its own: full sunlight at or
// above the column's height, its emission for block light.
func (r *Region) baseLight(c *Chunk, sky bool, pos cube.Pos) uint8 {
	if sky {
		if r.d.light.sky && int16(pos[1]) >= c.light.height[pos[0]&15|(pos[2]&15)<<4] { //nolint:gosec // world heights fit
			return MaxLight
		}
		return 0
	}
	return emission(r.cellLight(c, pos))
}

// lightChanged notes that the block at pos changed in a way that changes light.
func (r *Region) lightChanged(c *Chunk, pos cube.Pos, old, block uint32) {
	if c.light == nil || r.d.light.of(old) == r.d.light.of(block) {
		return
	}
	r.light.dirty = append(r.light.dirty, pos)
}

// flushLight relights what the block changes since the last flush touched. Runs: owner.
func (r *Region) flushLight() {
	w := r.light
	if len(w.dirty) == 0 {
		return
	}
	w.lastChunk = nil
	w.columns = w.columns[:0]
	for _, pos := range w.dirty {
		c := r.lightChunk(pos)
		if c == nil {
			continue
		}
		col := pos[0]&15 | (pos[2]&15)<<4
		old, now := c.light.height[col], r.columnHeight(c, pos[0]&15, pos[2]&15)
		if old == now {
			continue
		}
		c.light.height[col] = now
		for y := min(old, now); y < max(old, now); y++ {
			w.columns = append(w.columns, cube.Pos{pos[0], int(y), pos[2]})
		}
	}
	if r.d.light.sky {
		r.relight(true, w.dirty, w.columns)
	}
	r.relight(false, w.dirty, nil)
	w.dirty = w.dirty[:0]
}

// columnHeight is BDS's height of the column x, z of c: above the highest cell that
// stops the sky.
func (r *Region) columnHeight(c *Chunk, x, z int) int16 {
	subs := c.col.Chunk.Sub()
	t := r.d.light
	for i := len(subs) - 1; i >= 0; i-- {
		if subs[i].Empty() {
			continue
		}
		layers := subs[i].Layers()
		for y := 15; y >= 0; y-- {
			v := t.of(layers[0].At(byte(x), byte(y), byte(z))) //nolint:gosec // 0 to 15
			if len(layers) > 1 {
				v |= t.of(layers[1].At(byte(x), byte(y), byte(z))) //nolint:gosec // 0 to 15
			}
			if v&lightStopsSky != 0 {
				return int16(i<<4+y+1) + int16(r.d.r[0]) //nolint:gosec // world heights fit
			}
		}
	}
	return int16(r.d.r[0]) //nolint:gosec // world heights fit
}

// relight takes the light of kind away from the changed cells and everything lit
// through them, then fills it in again from what is left and from each cell's own
// light, as BDS's subtractive and additive passes do.
func (r *Region) relight(sky bool, changed, more []cube.Pos) {
	w := r.light
	w.remove, w.removed = w.remove[:0], w.removed[:0]
	for _, list := range [2][]cube.Pos{changed, more} {
		for _, pos := range list {
			c := r.lightChunk(pos)
			if c == nil {
				continue
			}
			l, _ := r.lightAt(sky, pos)
			r.setLight(c, sky, pos, 0)
			w.remove = append(w.remove, lightNode{pos, l})
			w.removed = append(w.removed, pos)
		}
	}
	for i := 0; i < len(w.remove); i++ {
		n := w.remove[i]
		for f := cube.Face(0); f < 6; f++ {
			q := n.pos.Side(f)
			lq, ok := r.lightAt(sky, q)
			if !ok || lq == 0 {
				continue
			}
			if lq < n.level {
				r.setLight(r.lightChunk(q), sky, q, 0)
				w.remove = append(w.remove, lightNode{q, lq})
				w.removed = append(w.removed, q)
				continue
			}
			w.buckets[lq] = append(w.buckets[lq], q)
		}
	}
	for _, pos := range w.removed {
		c := r.lightChunk(pos)
		if b := r.baseLight(c, sky, pos); b > 0 {
			if cur, _ := r.lightAt(sky, pos); b > cur {
				r.setLight(c, sky, pos, b)
				w.buckets[b] = append(w.buckets[b], pos)
			}
		}
	}
	r.spreadLight(sky)
}

// spreadLight spreads the light of kind queued in the buckets through the region's
// chunks, brightest first.
func (r *Region) spreadLight(sky bool) {
	w := r.light
	for level := MaxLight; level >= 2; level-- {
		for k := 0; k < len(w.buckets[level]); k++ {
			pos := w.buckets[level][k]
			if cur, _ := r.lightAt(sky, pos); int(cur) != level {
				continue
			}
			for f := cube.Face(0); f < 6; f++ {
				q := pos.Side(f)
				c := r.lightChunk(q)
				if c == nil {
					continue
				}
				a := int(absorption(r.cellLight(c, q)))
				if a >= level {
					continue
				}
				next := uint8(level - a) //nolint:gosec // 1 to 14
				if cur, _ := r.lightAt(sky, q); next > cur {
					r.setLight(c, sky, q, next)
					w.buckets[next] = append(w.buckets[next], q)
				}
			}
		}
		w.buckets[level] = w.buckets[level][:0]
	}
	w.buckets[1], w.buckets[0] = w.buckets[1][:0], w.buckets[0][:0]
}

// joinLight lets light across the sides between c, just loaded, and the chunks beside
// it in the region. Sub chunks with one light level on both sides are skipped: nothing
// can flow between them.
func (r *Region) joinLight(c *Chunk) {
	if c.light == nil {
		return
	}
	w := r.light
	w.lastChunk = nil
	sides := [4]struct {
		dx, dz int32
	}{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	for _, sky := range [2]bool{true, false} {
		if sky && !r.d.light.sky {
			continue
		}
		queued := false
		for _, s := range sides {
			nb := r.chunks[mcdb.ChunkPos{c.pos[0] + s.dx, c.pos[1] + s.dz}]
			if nb == nil || nb.light == nil {
				continue
			}
			for i := range c.light.subs {
				if uniformPair(&c.light.subs[i], &nb.light.subs[i], sky) {
					continue
				}
				baseY := i<<4 + r.d.r[0]
				for y := baseY; y < baseY+16 && y <= r.d.r[1]; y++ {
					for k := range 16 {
						var own, other cube.Pos
						ox, oz := int(c.pos[0])<<4, int(c.pos[1])<<4
						switch {
						case s.dx < 0:
							own, other = cube.Pos{ox, y, oz + k}, cube.Pos{ox - 1, y, oz + k}
						case s.dx > 0:
							own, other = cube.Pos{ox + 15, y, oz + k}, cube.Pos{ox + 16, y, oz + k}
						case s.dz < 0:
							own, other = cube.Pos{ox + k, y, oz}, cube.Pos{ox + k, y, oz - 1}
						default:
							own, other = cube.Pos{ox + k, y, oz + 15}, cube.Pos{ox + k, y, oz + 16}
						}
						for _, p := range [2]cube.Pos{own, other} {
							if l, ok := r.lightAt(sky, p); ok && l > 1 {
								w.buckets[l] = append(w.buckets[l], p)
								queued = true
							}
						}
					}
				}
			}
		}
		if queued {
			r.spreadLight(sky)
		}
	}
}

// uniformPair reports whether both sub chunks hold one and the same light of kind.
func uniformPair(a, b *lightSub, sky bool) bool {
	if sky {
		return a.sky == nil && b.sky == nil && a.skyFill == b.skyFill
	}
	return a.block == nil && b.block == nil && a.blockFill == b.blockFill
}

// Light returns the sky light and block light at pos, 0 to 15 each, with false if
// its chunk is not loaded in this region. The sky light is before the time of day
// takes anything off (see daytime.SkyDarken). Above the world the sky is full and
// below it there is none. Runs: owner.
func (r *Region) Light(pos cube.Pos) (sky, block uint8, ok bool) {
	if !r.ticking.Load() {
		return 0, 0, false
	}
	if _, ok := r.chunks[mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)}]; !ok { //nolint:gosec // block X and Z fit int32 chunks
		return 0, 0, false
	}
	switch {
	case pos[1] > r.d.r[1]:
		if r.d.light.sky {
			return MaxLight, 0, true
		}
		return 0, 0, true
	case pos[1] < r.d.r[0]:
		return 0, 0, true
	}
	r.flushLight()
	r.light.lastChunk = nil
	sky, ok = r.lightAt(true, pos)
	if !ok {
		return 0, 0, false
	}
	block, _ = r.lightAt(false, pos)
	return sky, block, true
}

// Height is BDS's height map at column x, z: the Y above the highest block that stops
// sunlight (dampens light, or is a half slab), with false if its chunk is not loaded in this
// region. Runs: owner.
func (r *Region) Height(x, z int) (int, bool) {
	if !r.ticking.Load() {
		return 0, false
	}
	c, ok := r.chunks[mcdb.ChunkPos{int32(x >> 4), int32(z >> 4)}] //nolint:gosec // block X and Z fit int32 chunks
	if !ok || c.light == nil {
		return 0, false
	}
	r.flushLight()
	return int(c.light.height[x&15|(z&15)<<4]), true
}

// SkyLight returns the sky light at pos, 0 to 15, before the time of day takes
// anything off; 0 where its chunk is not loaded in this region. Runs: owner.
func (r *Region) SkyLight(pos cube.Pos) uint8 {
	sky, _, _ := r.Light(pos)
	return sky
}

// BlockLight returns the block light at pos, 0 to 15; 0 where its chunk is not loaded
// in this region. Runs: owner.
func (r *Region) BlockLight(pos cube.Pos) uint8 {
	_, block, _ := r.Light(pos)
	return block
}

// Brightness is BDS's raw brightness at pos: the brighter of the block light and the
// sky light less skyDarken, the darkening of night and weather (daytime.SkyDarken).
// Runs: owner.
func (r *Region) Brightness(pos cube.Pos, skyDarken uint8) uint8 {
	sky, block, _ := r.Light(pos)
	if sky > skyDarken {
		sky -= skyDarken
	} else {
		sky = 0
	}
	return max(sky, block)
}
