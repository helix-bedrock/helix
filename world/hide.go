package world

import (
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Anti X-ray: Helix's own, beyond BDS. The client is sent every block of every chunk it sees,
// so an X-ray cheat can show it every ore under the ground, on cave walls as much as in solid
// rock. Every block the hiding rules disguise (ores, by default) goes out in the shared chunk
// as its cover (stone, deepslate or netherrack). Each player is then sent the real ones it
// could see: those with a face not sealed by an opaque full block, within the reveal radius
// of it (Viewer.RevealNear). The ones sealed in rock are sent when a block next to them stops
// covering them. The world itself is never changed, only what clients are told.

// DefaultRevealRadius is how close, in blocks, a player must be to a disguised block with an
// open face to be sent the real one.
const DefaultRevealRadius = 16

// revealEvery is how many ticks a player standing in one block waits between reveal scans: the
// chunks around it may have arrived meanwhile.
const revealEvery = 10

// BlockHiding says which blocks clients are shown as something else.
type BlockHiding interface {
	// Disguise returns the block a client is shown in place of block until it is near enough
	// to see it, false for a block shown as it is.
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

// apply returns c as clients are shown it: c itself when nothing is disguised, else a snapshot
// with every disguised block as its cover, and the disguised blocks that may be seen (a face
// not sealed within the chunk, or on the chunk's side), which players near them are sent as
// they are. c is only read.
func (t *hideTable) apply(c *chunk.Chunk, pos mcdb.ChunkPos) (*chunk.Chunk, []cube.Pos) {
	if t == nil || !t.any {
		return c, nil
	}
	out := c
	var near []cube.Pos
	for i, sub := range c.Sub() {
		if sub.Empty() || !t.mayHide(sub) {
			continue
		}
		base := c.SubY(int16(i)) //nolint:gosec // few sections
		for y := range int16(16) {
			for z := range uint8(16) {
				for x := range uint8(16) {
					cover := t.coverOf(sub.Block(x, uint8(y), z, 0)) //nolint:gosec // 0..15
					if cover == noCover {
						continue
					}
					if !t.sealed(c, x, base+y, z) {
						near = append(near, cube.Pos{int(pos[0])<<4 | int(x), int(base + y), int(pos[1])<<4 | int(z)})
					}
					if out == c {
						out = c.Snapshot()
					}
					out.SetBlock(x, base+y, z, 0, cover)
				}
			}
		}
	}
	if out != c {
		// Setting a block leaves the old one in the palette: without this the client would
		// still be told which ores a sub chunk holds. Compact skips the sub chunks out shares.
		out.Compact()
	}
	return out, near
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

// sealed reports whether every face of the block at x, y, z of c touches an opaque full block of
// c: a face on the chunk's side is not, as the chunk next door is not known here.
func (t *hideTable) sealed(c *chunk.Chunk, x uint8, y int16, z uint8) bool {
	if x == 0 || x == 15 || z == 0 || z == 15 {
		return false
	}
	ix, iz := int(x), int(z)
	return t.covered(c, ix-1, y, iz) && t.covered(c, ix+1, y, iz) && t.covered(c, ix, y, iz-1) &&
		t.covered(c, ix, y, iz+1) && t.covered(c, ix, y-1, iz) && t.covered(c, ix, y+1, iz)
}

// covered reports whether the cell at x, y, z of c hides a face next to it. Cells above or below
// the world count as covering.
func (t *hideTable) covered(c *chunk.Chunk, x int, y int16, z int) bool {
	r := c.Range()
	if int(y) < r[0] || int(y) > r[1] {
		return true
	}
	return t.occluding(c.Block(uint8(x), y, uint8(z), 0)) //nolint:gosec // 0..15
}

// revealAround notes the disguised blocks next to pos once the block at pos stopped covering
// them: players near them are sent the real ones (RevealNear), and the chunk's next encode
// lists them itself.
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
		if c, ok := r.chunks[chunkAt(n)]; ok {
			c.opened = append(c.opened, n)
		}
	}
}

// revealState is what a player was sent of one chunk's disguised blocks: net is the network
// chunk the client has, done the blocks it was sent as they are since.
type revealState struct {
	net  *NetChunk
	done map[cube.Pos]struct{}
}

// RevealNear sends the player the real blocks of the disguised blocks with an open face within
// the reveal radius of eye, in the chunks its client has, each once while the client keeps
// that chunk. Runs: the viewer's region, with eye the player's position.
func (v *Viewer) RevealNear(r *Region, eye mgl32.Vec3) {
	t := r.d.hide
	if t == nil || !t.any {
		return
	}
	at := blockAt(eye)
	now := r.CurrentTick()
	if at == v.revealAt && now-v.revealTick < revealEvery {
		return
	}
	v.revealAt, v.revealTick = at, now
	radius := r.d.revealRadius()
	reach := int32(radius+15) >> 4 //nolint:gosec // small
	centre := chunkAt(at)
	v.netMu.Lock()
	defer v.netMu.Unlock()
	if v.reveal == nil {
		v.reveal = map[mcdb.ChunkPos]*revealState{}
	}
	for pos, st := range v.reveal {
		if v.nets[pos] != st.net {
			delete(v.reveal, pos)
		}
	}
	for dx := -reach; dx <= reach; dx++ {
		for dz := -reach; dz <= reach; dz++ {
			pos := mcdb.ChunkPos{centre[0] + dx, centre[1] + dz}
			nc, ok := v.nets[pos]
			if !ok {
				continue
			}
			st := v.reveal[pos]
			if st == nil {
				st = &revealState{net: nc}
				v.reveal[pos] = st
			}
			v.revealList(r, st, nc.near, eye, radius)
			if c, ok := r.chunks[pos]; ok {
				v.revealList(r, st, c.opened, eye, radius)
			}
		}
	}
}

// revealList sends the real blocks of the disguised blocks in list within radius of eye that
// were not sent yet.
func (v *Viewer) revealList(r *Region, st *revealState, list []cube.Pos, eye mgl32.Vec3, radius int) {
	r2 := float32(radius * radius)
	t := r.d.hide
	for _, p := range list {
		centre := mgl32.Vec3{float32(p[0]) + 0.5, float32(p[1]) + 0.5, float32(p[2]) + 0.5}
		if centre.Sub(eye).LenSqr() > r2 {
			continue
		}
		if _, ok := st.done[p]; ok {
			continue
		}
		if st.done == nil {
			st.done = map[cube.Pos]struct{}{}
		}
		st.done[p] = struct{}{}
		b, ok := r.Block(p, 0)
		if !ok || t.coverOf(b) == noCover {
			continue
		}
		v.send(&packet.UpdateBlock{
			Position:          protocol.BlockPos{int32(p[0]), int32(p[1]), int32(p[2])}, //nolint:gosec // block coordinates fit int32
			NewBlockRuntimeID: r.d.conf.Blocks.Hash(b),
			Flags:             UpdateBlockFlags,
		})
	}
}

// revealRadius is Config.RevealRadius, or DefaultRevealRadius.
func (d *Dimension) revealRadius() int {
	if d.conf.RevealRadius > 0 {
		return int(d.conf.RevealRadius)
	}
	return DefaultRevealRadius
}

// blockAt is the block a position is in.
func blockAt(v mgl32.Vec3) cube.Pos {
	return cube.Pos{int(math.Floor(float64(v[0]))), int(math.Floor(float64(v[1]))), int(math.Floor(float64(v[2])))}
}
