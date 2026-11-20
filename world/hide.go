package world

import (
	"strings"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
)

// Anti X-ray: Helix's own, beyond BDS. The client is sent every block of every chunk it sees,
// so an X-ray cheat can show it the ores deep in the ground. A block the hiding rules disguise
// (ores, by default) goes out as its cover (stone, deepslate or netherrack) while every face
// of it touches an opaque full block: nobody could see it anyway. When a block next to one
// changes, the real block is sent, so mining into a vein or opening a cave shows it. The
// world itself is never changed, only what the client is told.

// BlockHiding says what clients are shown of buried blocks.
type BlockHiding interface {
	// Disguise returns the block a client is shown in place of block while no face of it
	// is open, false for a block shown as it is.
	Disguise(block uint32) (uint32, bool)
}

// NoHiding shows every block as it is: X-ray protection off.
type NoHiding struct{}

// Disguise shows block as it is.
func (NoHiding) Disguise(uint32) (uint32, bool) { return 0, false }

// OreHiding is the default: every ore and ancient debris, shown as the stone it sits in.
type OreHiding struct{ Blocks *registry.Blocks }

const (
	nameAncientDebris = "minecraft:ancient_debris"
	nameNetherGoldOre = "minecraft:nether_gold_ore"
	nameQuartzOre     = "minecraft:quartz_ore"
	coverStone        = "minecraft:stone"
	coverDeepslate    = "minecraft:deepslate"
	coverNetherrack   = "minecraft:netherrack"
)

// Disguise shows an ore as stone, a deepslate ore as deepslate and a Nether ore or ancient
// debris as netherrack.
func (h OreHiding) Disguise(block uint32) (uint32, bool) {
	name := h.Blocks.Name(block)
	if name != nameAncientDebris && !strings.HasSuffix(name, "_ore") {
		return 0, false
	}
	cover := coverStone
	switch {
	case strings.Contains(name, "deepslate_"):
		cover = coverDeepslate
	case name == nameNetherGoldOre || name == nameQuartzOre || name == nameAncientDebris:
		cover = coverNetherrack
	}
	return h.Blocks.Block(cover)
}

// noCover marks a block shown as it is.
const noCover = ^uint32(0)

// hideTable is the hiding rules worked out for every block once.
type hideTable struct {
	// cover is what each block is shown as while buried, noCover for itself.
	cover []uint32
	// occludes marks the opaque full blocks, which hide what is behind them.
	occludes []bool
	any      bool
}

func newHideTable(conf Config) *hideTable {
	rules := conf.Hiding
	if rules == nil {
		rules = OreHiding{Blocks: conf.Blocks}
	}
	n := conf.Blocks.Count()
	t := &hideTable{cover: make([]uint32, n), occludes: make([]bool, n)}
	for id := range n {
		b := uint32(id) //nolint:gosec // registry IDs fit
		t.cover[id] = noCover
		if c, ok := rules.Disguise(b); ok && c != b {
			t.cover[id], t.any = c, true
		}
		t.occludes[id] = occluder(conf.Blocks.Properties(b))
	}
	return t
}

// fullBox is a whole block.
var fullBox = registry.Box{0, 0, 0, 1, 1, 1}

// occluder reports whether a block with props hides the faces of the blocks next to it.
func occluder(props registry.Properties) bool {
	return !props.CollisionComputed && len(props.Collision) == 1 && props.Collision[0] == fullBox &&
		props.LightDampening >= MaxLight
}

func (t *hideTable) coverOf(b uint32) uint32 {
	if int(b) < len(t.cover) {
		return t.cover[b]
	}
	return noCover
}

func (t *hideTable) occluding(b uint32) bool {
	return int(b) < len(t.occludes) && t.occludes[b]
}

// apply returns c as the client is shown it: c itself when nothing is hidden, else a snapshot
// with the buried blocks disguised. c is only read.
func (t *hideTable) apply(c *chunk.Chunk) *chunk.Chunk {
	if t == nil || !t.any {
		return c
	}
	out := c
	for i, sub := range c.Sub() {
		if sub.Empty() || !t.mayHide(sub) {
			continue
		}
		base := c.SubY(int16(i)) //nolint:gosec // few sections
		for y := range int16(16) {
			for z := range uint8(16) {
				for x := range uint8(16) {
					cover := t.coverOf(sub.Block(x, uint8(y), z, 0)) //nolint:gosec // 0..15
					if cover == noCover || !t.buried(c, x, base+y, z) {
						continue
					}
					if out == c {
						out = c.Snapshot()
					}
					out.SetBlock(x, base+y, z, 0, cover)
				}
			}
		}
	}
	return out
}

// mayHide reports whether the sub chunk's first layer holds any block that may be disguised.
func (t *hideTable) mayHide(sub *chunk.SubChunk) bool {
	layers := sub.Layers()
	if len(layers) == 0 {
		return false
	}
	p := layers[0].Palette()
	for i := range p.Len() {
		if t.coverOf(p.Value(uint16(i))) != noCover { //nolint:gosec // palettes are small
			return true
		}
	}
	return false
}

// buried reports whether every face of the block at x, y, z of c touches an opaque full block.
// A face on the chunk's side counts as covered: the chunk next to it is not known here, and a
// block revealed by a change is sent anyway.
func (t *hideTable) buried(c *chunk.Chunk, x uint8, y int16, z uint8) bool {
	ix, iz := int(x), int(z)
	return t.covered(c, ix-1, y, iz) && t.covered(c, ix+1, y, iz) && t.covered(c, ix, y, iz-1) &&
		t.covered(c, ix, y, iz+1) && t.covered(c, ix, y-1, iz) && t.covered(c, ix, y+1, iz)
}

// covered reports whether the cell at x, y, z of c, which may be just outside it, hides a face
// next to it. Cells outside the chunk count as covering.
func (t *hideTable) covered(c *chunk.Chunk, x int, y int16, z int) bool {
	r := c.Range()
	if x < 0 || x > 15 || z < 0 || z > 15 || int(y) < r[0] || int(y) > r[1] {
		return true
	}
	return t.occluding(c.Block(uint8(x), y, uint8(z), 0)) //nolint:gosec // 0..15
}

// revealAround sends the real blocks next to pos that clients may have been shown disguised,
// once the block at pos stopped covering them.
func (r *Region) revealAround(pos cube.Pos, old, block uint32) {
	t := r.d.hide
	if t == nil || !t.any || !t.occluding(old) || t.occluding(block) {
		return
	}
	for f := cube.FaceDown; f <= cube.FaceEast; f++ {
		n := pos.Side(f)
		b, ok := r.Block(n, 0)
		if !ok || t.coverOf(b) == noCover {
			continue
		}
		r.updateBlock(n, 0, b)
	}
}
