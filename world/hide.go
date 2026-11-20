package world

import (
	"math"
	"slices"
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

// rehideSlack is how much farther than the reveal radius a disguised block's changes still reach
// a player that was not shown it (hiddenChange).
const rehideSlack = 8

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

// OreHiding is the default: every ore and ancient debris, shown as the stone it sits in; every
// block that gives a base away (baseBlocks) or a cave (caveBlocks), shown as stone when solid and
// air otherwise; and the overworld's bedrock, shown as deepslate.
type OreHiding struct {
	Blocks *registry.Blocks
	// Hide are more blocks to hide, by name ("minecraft:..."): shown as stone when solid, air
	// otherwise. Show are blocks never hidden, by name, over every other rule.
	Hide, Show map[string]bool
	// Dim is the dimension: bedrock is hidden in the overworld only, where it is the floor
	// under the caves, not the Nether's roof.
	Dim mcdb.Dimension
}

const (
	nameAncientDebris = "minecraft:ancient_debris"
	nameNetherGoldOre = "minecraft:nether_gold_ore"
	nameQuartzOre     = "minecraft:quartz_ore"
	coverStone        = "minecraft:stone"
	coverDeepslate    = "minecraft:deepslate"
	coverNetherrack   = "minecraft:netherrack"
)

// Disguise shows an ore as stone, a deepslate ore as deepslate, a Nether ore or ancient debris as
// netherrack, a base or cave block as stone or air, and bedrock as deepslate.
func (h OreHiding) Disguise(block uint32) (uint32, bool) {
	name := h.Blocks.Name(block)
	if h.Show[name] {
		return 0, false
	}
	if name == nameBedrock {
		if h.Dim != mcdb.Overworld {
			return 0, false
		}
		return h.Blocks.Block(coverDeepslate)
	}
	if baseBlock(name) || caveBlocks[name] || h.Hide[name] {
		// A solid one goes out as stone, the rest (chests, anvils, spawners) as air.
		if occluder(h.Blocks.Properties(block)) {
			return h.Blocks.Block(coverStone)
		}
		return h.Blocks.Air(), true
	}
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

// baseBlocks are the blocks that give a base away: storage, workstations and spawners. They are
// hidden like ores, so an X-ray cannot find bases.
var baseBlocks = map[string]bool{
	"minecraft:chest": true, "minecraft:trapped_chest": true, "minecraft:ender_chest": true,
	"minecraft:barrel": true, "minecraft:hopper": true, "minecraft:dropper": true,
	"minecraft:dispenser": true, "minecraft:crafter": true,
	"minecraft:furnace": true, "minecraft:lit_furnace": true, "minecraft:blast_furnace": true,
	"minecraft:lit_blast_furnace": true, "minecraft:smoker": true, "minecraft:lit_smoker": true,
	"minecraft:anvil": true, "minecraft:chipped_anvil": true, "minecraft:damaged_anvil": true,
	"minecraft:crafting_table": true, "minecraft:enchanting_table": true,
	"minecraft:brewing_stand": true, "minecraft:beacon": true, "minecraft:lectern": true,
	"minecraft:mob_spawner": true, "minecraft:trial_spawner": true,
}

// caveBlocks are the blocks that show where caves, mineshafts and geodes are: hidden like ores,
// so an X-ray sees no rails, cobwebs, torches, lichen or amethyst through the rock.
var caveBlocks = map[string]bool{
	"minecraft:rail": true, "minecraft:golden_rail": true, "minecraft:detector_rail": true,
	"minecraft:activator_rail": true, "minecraft:web": true, "minecraft:torch": true,
	"minecraft:soul_torch": true, "minecraft:redstone_torch": true, "minecraft:unlit_redstone_torch": true,
	"minecraft:glow_lichen": true, "minecraft:cave_vines": true, "minecraft:cave_vines_body_with_berries": true,
	"minecraft:cave_vines_head_with_berries": true, "minecraft:amethyst_block": true,
	"minecraft:budding_amethyst": true, "minecraft:small_amethyst_bud": true,
	"minecraft:medium_amethyst_bud": true, "minecraft:large_amethyst_bud": true,
	"minecraft:amethyst_cluster": true, "minecraft:calcite": true, "minecraft:smooth_basalt": true,
}

// nameBedrock is hidden in the overworld only (OreHiding.Disguise).
const nameBedrock = "minecraft:bedrock"

// liquidNames are the blocks a cave sub chunk seen from afar is sent without (subhide.go).
var liquidNames = map[string]bool{
	"minecraft:water": true, "minecraft:flowing_water": true, "minecraft:lava": true, "minecraft:flowing_lava": true,
}

// baseBlock reports whether the block named name gives a base away.
func baseBlock(name string) bool {
	return baseBlocks[name] || strings.HasSuffix(name, "shulker_box")
}

// isLiquid reports whether block is water or lava.
func (t *hideTable) isLiquid(block uint32) bool {
	return int(block) < len(t.liquid) && t.liquid[block]
}

// hiding returns the dimension's hiding table: the rules SetHiding last set.
func (d *Dimension) hiding() *hideTable { return d.hide.Load() }

// SetHiding replaces the dimension's anti X-ray rules (Config.Hiding): nil for OreHiding,
// NoHiding to turn it off. Chunks encoded from now on follow them; what clients were already
// sent stays until those chunks change or are sent again. Runs: anywhere.
func (d *Dimension) SetHiding(rules BlockHiding) {
	d.hide.Store(newHideTable(d.conf, rules, d.r))
	d.cache.clear()
}

// noCover marks a block shown as it is.
const noCover = ^uint32(0)

// hideTable is the hiding rules worked out for every block once.
type hideTable struct {
	// cover is what each block is shown as while buried, noCover for itself.
	cover []uint32
	// occludes marks the opaque full blocks, which hide what is behind them.
	occludes []bool
	// liquid marks water and lava, left out of cave sub chunks seen from afar (subhide.go).
	liquid []bool
	any    bool
	// fake is the solid sub chunk sent for each sub chunk index while it is hidden, nil when
	// sub chunks are never hidden (subhide.go).
	fake [][]byte
	// fakeBlock is the block each of them is made of.
	fakeBlock []uint32
}

func newHideTable(conf Config, rules BlockHiding, r cube.Range) *hideTable {
	if rules == nil {
		rules = OreHiding{Blocks: conf.Blocks, Dim: conf.Dim}
	}
	n := conf.Blocks.Count()
	t := &hideTable{cover: make([]uint32, n), occludes: make([]bool, n), liquid: make([]bool, n)}
	for id := range n {
		b := uint32(id) //nolint:gosec // registry IDs fit
		t.cover[id] = noCover
		if c, ok := rules.Disguise(b); ok && c != b {
			t.cover[id], t.any = c, true
		}
		t.occludes[id] = occluder(conf.Blocks.Properties(b))
		t.liquid[id] = liquidNames[conf.Blocks.Name(b)]
	}
	if _, off := rules.(NoHiding); !off && !conf.NoSubHiding {
		t.fake, t.fakeBlock = newFakeSubs(conf, r)
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
func (t *hideTable) apply(c *chunk.Chunk, pos mcdb.ChunkPos, sky *columnHeights) (*chunk.Chunk, []hiddenBlock) {
	if t == nil || !t.any {
		return c, nil
	}
	s := hideScan{t: t, src: c, out: c, pos: pos, sky: sky}
	for i, sub := range c.Sub() {
		if sub.Empty() {
			continue
		}
		layers := sub.Layers()
		if len(layers) == 0 || !s.markOres(layers[0].Palette()) {
			continue
		}
		s.scanSub(layers[0], c.SubY(int16(i))) //nolint:gosec // few sections
	}
	if s.out != c {
		// Setting a block leaves the old one in the palette: without this the client would
		// still be told which ores a sub chunk holds. Compact skips the sub chunks out shares.
		s.out.Compact()
	}
	return s.out, s.near
}

// hideScan is one apply: the chunk read, the copy written and the open ores found.
type hideScan struct {
	t   *hideTable
	src *chunk.Chunk
	out *chunk.Chunk
	pos mcdb.ChunkPos
	// sky is the chunk's render height in each column: air at or above it is under open sky.
	sky *columnHeights
	// ores marks the palette indices of the sub chunk being scanned that are disguised.
	ores []bool
	near []hiddenBlock
}

// hiddenBlock is a disguised block that may be seen and its real block.
type hiddenBlock struct {
	pos   cube.Pos
	block uint32
}

// markOres marks which entries of palette p are disguised, false if none is: most sub chunks
// are skipped without looking at a block.
func (s *hideScan) markOres(p *chunk.Palette) bool {
	s.ores = slices.Grow(s.ores[:0], p.Len())[:p.Len()]
	found := false
	for i := range p.Len() {
		ore := s.t.coverOf(p.Value(uint16(i))) != noCover //nolint:gosec // palettes are small
		s.ores[i] = ore
		found = found || ore
	}
	return found
}

// scanSub disguises the ores of the sub chunk starting at Y base, whose first layer is storage,
// reading its packed indices directly: each word holds 32/bits indices from its low bits up, cell
// x<<8 | z<<4 | y.
func (s *hideScan) scanSub(storage *chunk.PalettedStorage, base int16) {
	bits, words := storage.Raw()
	if bits == 0 {
		// One block fills it, an ore (markOres found one).
		for cell := range 4096 {
			s.hide(cell, base)
		}
		return
	}
	per := 32 / bits
	mask := uint32(1)<<bits - 1
	cell := 0
	for _, w := range words {
		for range per {
			if cell == 4096 {
				return
			}
			if idx := int(w & mask); idx < len(s.ores) && s.ores[idx] {
				s.hide(cell, base)
			}
			w >>= bits
			cell++
		}
	}
}

// underSky reports whether a face of the block at x, y, z of the chunk touches air under open
// sky: a block next to it, in the chunk, that is not opaque and full and has nothing above it
// that stops the view.
func (s *hideScan) underSky(x uint8, y int16, z uint8) bool {
	if s.sky == nil {
		return false
	}
	r := s.src.Range()
	ix, iz := int(x), int(z)
	for f := cube.FaceDown; f <= cube.FaceEast; f++ {
		nx, ny, nz := ix, y, iz
		switch f {
		case cube.FaceDown:
			ny--
		case cube.FaceUp:
			ny++
		case cube.FaceNorth:
			nz--
		case cube.FaceSouth:
			nz++
		case cube.FaceWest:
			nx--
		case cube.FaceEast:
			nx++
		}
		if nx < 0 || nx > 15 || nz < 0 || nz > 15 || int(ny) < r[0] || int(ny) > r[1] {
			continue
		}
		if ny < s.sky[nx|nz<<4] || s.t.occluding(s.src.Block(uint8(nx), ny, uint8(nz), 0)) { //nolint:gosec // 0..15
			continue
		}
		return true
	}
	return false
}

// hide disguises the ore at cell of the sub chunk starting at Y base, and lists it if a face of
// it may be open.
func (s *hideScan) hide(cell int, base int16) {
	x, z, y := uint8(cell>>8), uint8(cell>>4&15), base+int16(cell&15) //nolint:gosec // cell < 4096
	real := s.src.Block(x, y, z, 0)
	cover := s.t.coverOf(real)
	if cover == noCover || s.underSky(x, y, z) {
		// An ore on a ravine wall or a hillside: anyone can see it from afar, so it goes
		// out as it is.
		return
	}
	if !s.t.sealed(s.src, x, y, z) {
		s.near = append(s.near, hiddenBlock{pos: cube.Pos{int(s.pos[0])<<4 | int(x), int(y), int(s.pos[1])<<4 | int(z)}, block: real})
	}
	if s.out == s.src {
		s.out = s.src.Snapshot()
	}
	s.out.SetBlock(x, y, z, 0, cover)
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

// hideAround sends the cover of every disguised block next to pos that the block just placed there
// sealed on every side: nobody can see it any more, so no client keeps the real one.
func (r *Region) hideAround(pos cube.Pos, old, block uint32) {
	t := r.d.hiding()
	if t == nil || !t.any || t.occluding(old) || !t.occluding(block) {
		return
	}
	for f := cube.FaceDown; f <= cube.FaceEast; f++ {
		n := pos.Side(f)
		b, ok := r.Block(n, 0)
		if !ok {
			continue
		}
		if cover := t.coverOf(b); cover != noCover && r.sealedAt(n) {
			r.updateBlock(n, 0, cover)
			r.forgetShown(n)
		}
	}
}

// forgetShown takes p off what every player of the region was sent as it is, so it is revealed
// again when it opens.
func (r *Region) forgetShown(p cube.Pos) {
	at := chunkAt(p)
	for _, v := range r.viewers {
		v.netMu.Lock()
		if st := v.reveal[at]; st != nil {
			delete(st.done, p)
		}
		v.netMu.Unlock()
	}
}

// sealedAt reports whether every block touching the block at p is a loaded opaque full block.
func (r *Region) sealedAt(p cube.Pos) bool {
	t := r.d.hiding()
	for f := cube.FaceDown; f <= cube.FaceEast; f++ {
		if b, ok := r.Block(p.Side(f), 0); !ok || !t.occluding(b) {
			return false
		}
	}
	return true
}

// notePlaced lists a disguised block just set at pos on its chunk, for players who can see it to
// be sent it (RevealNear): the UpdateBlock of it reaches only those shown it already.
func (r *Region) notePlaced(pos cube.Pos, block uint32) {
	t := r.d.hiding()
	if t == nil || !t.any || t.coverOf(block) == noCover {
		return
	}
	if c, ok := r.chunks[chunkAt(pos)]; ok && !slices.Contains(c.opened, pos) {
		c.opened = append(c.opened, pos)
	}
}

// revealAround notes the disguised blocks next to pos once the block at pos stopped covering
// them: players near them are sent the real ones (RevealNear), and the chunk's next encode
// lists them itself.
func (r *Region) revealAround(pos cube.Pos, old, block uint32) {
	t := r.d.hiding()
	if t == nil || !t.any || !t.occluding(old) || t.occluding(block) {
		return
	}
	for f := cube.FaceDown; f <= cube.FaceEast; f++ {
		n := pos.Side(f)
		b, ok := r.Block(n, 0)
		if !ok || t.coverOf(b) == noCover {
			continue
		}
		// A piston going back and forth next to an ore opens it again and again: list it once.
		if c, ok := r.chunks[chunkAt(n)]; ok && !slices.Contains(c.opened, n) {
			c.opened = append(c.opened, n)
		}
	}
}

// revealState is what a player was sent of one chunk's disguised blocks: net is the network
// chunk the client has, done the blocks it was sent as they are since, with where the player was
// and the block's chunk version when it last saw each.
type revealState struct {
	net  *NetChunk
	done map[cube.Pos]struct{}
	// fake and real have a bit for each sub chunk index answered solid and as it is
	// (subhide.go).
	fake, real uint64
	// dry has a bit for each sub chunk sent without its water and lava (subDry).
	dry uint64
	// all is set once a player who sees everything was sent its hidden blocks (seeall.go).
	all bool
}

// RevealNear sends the player what it is near enough to see: the real blocks of the disguised
// blocks with an open face within the reveal radius of eye, each once while its client keeps
// that chunk, and the real sub chunks of the underground ones within the sub chunk reveal radius
// (subhide.go). Runs: the viewer's region, with eye the player's position.
func (v *Viewer) RevealNear(r *Region, eye mgl32.Vec3) {
	t := r.d.hiding()
	if t == nil || (!t.any && t.fake == nil) {
		return
	}
	if v.seeAll.Load() {
		v.revealAll(r)
		return
	}
	at := blockAt(eye)
	now := r.CurrentTick()
	v.netMu.Lock()
	if at == v.revealAt && now-v.revealTick < revealEvery && v.visNets == len(v.nets) {
		// Unmoved, and no chunk arrived for the walk of what it sees (subvis.go).
		v.netMu.Unlock()
		return
	}
	v.revealAt, v.revealTick = at, now
	v.eye, v.eyeSet = eye, true
	out := v.revealOut[:0]
	v.walkVisible(r, eye)
	if t.fake != nil {
		out = v.revealSubs(r, eye, out)
	}
	if t.any {
		out = v.revealOres(r, eye, out)
	}
	v.netMu.Unlock()
	for i, pk := range out {
		v.send(pk)
		out[i] = nil
	}
	v.revealOut = out[:0]
}

// revealOres adds to out the real blocks of the disguised blocks with a face that may be open in
// the sub chunks the player may see (subvis.go), however far, that the client was not sent yet.
// Which blocks may be seen is worked out once, when a chunk is encoded, so they show as soon as
// the chunk and its view arrive, with no check per player. Under netMu.
func (v *Viewer) revealOres(r *Region, eye mgl32.Vec3, out []packet.Packet) []packet.Packet {
	radius := r.d.revealRadius()
	reach := max(int32(radius+15)>>4, v.visReach) //nolint:gosec // small
	centre := chunkAt(blockAt(eye))
	for dx := -reach; dx <= reach; dx++ {
		for dz := -reach; dz <= reach; dz++ {
			pos := mcdb.ChunkPos{centre[0] + dx, centre[1] + dz}
			nc, ok := v.nets[pos]
			if !ok {
				continue
			}
			st := v.revealOf(pos, nc)
			c, loaded := r.chunks[pos]
			for _, h := range nc.near {
				if loaded {
					// The live block: the chunk may have changed since it was encoded.
					b, ok := r.Block(h.pos, 0)
					if !ok {
						continue
					}
					h.block = b
				}
				out = v.revealOne(r, st, h, out)
			}
			if loaded {
				for _, p := range c.opened {
					if b, ok := r.Block(p, 0); ok {
						out = v.revealOne(r, st, hiddenBlock{pos: p, block: b}, out)
					}
				}
			}
		}
	}
	return out
}

// blockCentre is the centre of the block at p.
func blockCentre(p cube.Pos) mgl32.Vec3 {
	return mgl32.Vec3{float32(p[0]) + 0.5, float32(p[1]) + 0.5, float32(p[2]) + 0.5}
}

// revealOne adds to out the real block h, a disguised block that may be seen, if its sub chunk
// is in the player's view and the client was not sent it yet. Under netMu.
func (v *Viewer) revealOne(r *Region, st *revealState, h hiddenBlock, out []packet.Packet) []packet.Packet {
	p := h.pos
	sy := int32(p[1] >> 4) //nolint:gosec // block Y fits
	if !v.visibleSub(chunkAt(p), sy) {
		return out
	}
	if _, ok := st.done[p]; ok {
		return out
	}
	if i := sy - st.net.bottom; i >= 0 && i < maxHiddenSubs && st.fake&(1<<i) != 0 {
		return out // the client has its sub chunk as solid stone: revealed with it (subhide.go)
	}
	if st.done == nil {
		st.done = map[cube.Pos]struct{}{}
	}
	st.done[p] = struct{}{}
	if r.d.hiding().coverOf(h.block) == noCover {
		return out
	}
	out = append(out, &packet.UpdateBlock{
		Position:          protocol.BlockPos{int32(p[0]), int32(p[1]), int32(p[2])}, //nolint:gosec // block coordinates fit int32
		NewBlockRuntimeID: r.d.conf.Blocks.Hash(h.block),
		Flags:             UpdateBlockFlags,
	})
	return r.appendBlockEntity(out, p)
}

// appendBlockEntity adds the block entity data of the block at p to out, if it has any: the
// network chunk left it out while the block was disguised.
func (r *Region) appendBlockEntity(out []packet.Packet, p cube.Pos) []packet.Packet {
	c, ok := r.chunks[chunkAt(p)]
	if !ok {
		return out
	}
	be, ok := c.blockEntities[p]
	if !ok {
		return out
	}
	return append(out, &packet.BlockActorData{
		Position: protocol.BlockPos{int32(p[0]), int32(p[1]), int32(p[2])}, //nolint:gosec // block coordinates fit int32
		NBTData:  be.NetworkData(p),
	})
}

// shownReal reports whether the client was sent the block at p as it is by a reveal. Under netMu.
func (v *Viewer) shownReal(p cube.Pos) bool {
	st := v.reveal[chunkAt(p)]
	if st == nil {
		return false
	}
	_, ok := st.done[p]
	return ok
}

// hiddenChange reports whether pk, a change to a block that is disguised, must not reach v's
// client: it is not near and was not shown the real block, so the change would give it away
// (a furnace lighting, a chest opening, a block entity). Under netMu.
func (r *Region) hiddenChange(v *Viewer, pk packet.Packet) bool {
	t := r.d.hiding()
	if t == nil || !t.any || v.seeAll.Load() {
		return false
	}
	var at protocol.BlockPos
	var block uint32
	switch pk := pk.(type) {
	case *packet.UpdateBlock:
		b, ok := r.d.conf.Blocks.HashToRuntimeID(pk.NewBlockRuntimeID)
		if !ok || pk.Layer != 0 {
			return false
		}
		at, block = pk.Position, b
	case *packet.BlockActorData:
		at = pk.Position
		block, _ = r.Block(cube.Pos{int(at[0]), int(at[1]), int(at[2])}, 0)
	case *packet.BlockEvent:
		at = pk.Position
		block, _ = r.Block(cube.Pos{int(at[0]), int(at[1]), int(at[2])}, 0)
	default:
		return false
	}
	p := cube.Pos{int(at[0]), int(at[1]), int(at[2])}
	if t.coverOf(block) == noCover || v.shownReal(p) {
		return false
	}
	// A player near it gets every change, as it would see it (an ore or chest open to the sky
	// went out as it is); only those far away are kept from them.
	near := float32(r.d.revealRadius() + rehideSlack)
	return !v.eyeSet || blockCentre(p).Sub(v.eye).LenSqr() > near*near
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
