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

// rehideSlack is how much farther than the reveal radius a player must go from a disguised block
// it was sent as it is to be sent its cover again: a player at the edge does not see it flicker.
const rehideSlack = 8

// revealEvery is how many ticks a player standing in one block waits between reveal scans: the
// chunks around it may have arrived meanwhile.
const revealEvery = 10

// maxSightChecks and maxHideChecks cap the line of sight checks (hidesight.go) a player costs a
// tick: for ores not shown yet and for ores shown that may have gone out of sight. Ores past the
// cap are checked on the next tick, so a crowd in a big cave cannot make a tick long.
const (
	maxSightChecks = 48
	maxHideChecks  = 32
)

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

// OreHiding is the default: every ore and ancient debris, shown as the stone it sits in, and every
// block that gives a base away (baseBlocks), shown as stone or air.
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
	if baseBlock(name) {
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

// baseBlock reports whether the block named name gives a base away.
func baseBlock(name string) bool {
	return baseBlocks[name] || strings.HasSuffix(name, "shulker_box")
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
	// fake is the solid sub chunk sent for each sub chunk index while it is hidden, nil when
	// sub chunks are never hidden (subhide.go).
	fake [][]byte
}

func newHideTable(conf Config, r cube.Range) *hideTable {
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
	if _, off := rules.(NoHiding); !off {
		t.fake = newFakeSubs(conf, r)
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
func (t *hideTable) apply(c *chunk.Chunk, pos mcdb.ChunkPos, sky *columnHeights) (*chunk.Chunk, []cube.Pos) {
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
	near []cube.Pos
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
	cover := s.t.coverOf(s.src.Block(x, y, z, 0))
	if cover == noCover || s.underSky(x, y, z) {
		// An ore on a ravine wall or a hillside: anyone can see it from afar, so it goes
		// out as it is.
		return
	}
	if !s.t.sealed(s.src, x, y, z) {
		s.near = append(s.near, cube.Pos{int(s.pos[0])<<4 | int(x), int(y), int(s.pos[1])<<4 | int(z)})
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
	t := r.d.hide
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
	t := r.d.hide
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
	t := r.d.hide
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
		// A piston going back and forth next to an ore opens it again and again: list it once.
		if c, ok := r.chunks[chunkAt(n)]; ok && !slices.Contains(c.opened, n) {
			c.opened = append(c.opened, n)
		}
	}
}

// revealState is what a player was sent of one chunk's disguised blocks: net is the network
// chunk the client has, done the blocks it was sent as they are since.
type revealState struct {
	net  *NetChunk
	done map[cube.Pos]struct{}
	// fake and real have a bit for each sub chunk index answered solid and as it is
	// (subhide.go).
	fake, real uint64
}

// RevealNear sends the player what it is near enough to see: the real blocks of the disguised
// blocks with an open face within the reveal radius of eye, each once while its client keeps
// that chunk, and the real sub chunks of the underground ones within the sub chunk reveal radius
// (subhide.go). Runs: the viewer's region, with eye the player's position.
func (v *Viewer) RevealNear(r *Region, eye mgl32.Vec3) {
	t := r.d.hide
	if t == nil || (!t.any && t.fake == nil) {
		return
	}
	at := blockAt(eye)
	now := r.CurrentTick()
	if at == v.revealAt && now-v.revealTick < revealEvery {
		return
	}
	v.revealAt, v.revealTick = at, now
	v.sightLeft, v.hideLeft = maxSightChecks, maxHideChecks
	v.netMu.Lock()
	v.eye, v.eyeSet = eye, true
	for pos, st := range v.reveal {
		if v.nets[pos] != st.net {
			delete(v.reveal, pos)
		}
	}
	out := v.revealOut[:0]
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
	if v.sightLeft <= 0 || v.hideLeft <= 0 {
		// Out of sight checks: the rest are done on the next tick, moved or not.
		v.revealTick = now - revealEvery
	}
}

// revealOres adds to out the real blocks of the disguised blocks with an open face within the
// reveal radius of eye that the client was not sent yet. Under netMu.
func (v *Viewer) revealOres(r *Region, eye mgl32.Vec3, out []packet.Packet) []packet.Packet {
	radius := r.d.revealRadius()
	reach := int32(radius+15) >> 4 //nolint:gosec // small
	centre := chunkAt(blockAt(eye))
	out = v.rehideOres(r, eye, radius+rehideSlack, out)
	for dx := -reach; dx <= reach; dx++ {
		for dz := -reach; dz <= reach; dz++ {
			pos := mcdb.ChunkPos{centre[0] + dx, centre[1] + dz}
			nc, ok := v.nets[pos]
			if !ok {
				continue
			}
			st := v.revealOf(pos, nc)
			out = v.revealList(r, st, nc.near, eye, radius, out)
			if c, ok := r.chunks[pos]; ok {
				out = v.revealList(r, st, c.opened, eye, radius, out)
			}
		}
	}
	return out
}

// rehideOres adds to out the covers of the disguised blocks the client was sent as they are that
// are now farther than radius from eye or out of its sight (a wall in between, a block placed
// over the open face), so an X-ray sees stone there again; the player is sent the real ones again
// when it can see them. Under netMu.
func (v *Viewer) rehideOres(r *Region, eye mgl32.Vec3, radius int, out []packet.Packet) []packet.Packet {
	r2 := float32(radius * radius)
	t := r.d.hide
	for _, st := range v.reveal {
		for p := range st.done {
			if blockCentre(p).Sub(eye).LenSqr() <= r2 && (v.hideLeft <= 0 || v.stillSees(r, eye, p)) {
				continue
			}
			delete(st.done, p)
			b, ok := r.Block(p, 0)
			if !ok {
				continue
			}
			if cover := t.coverOf(b); cover != noCover {
				out = append(out, &packet.UpdateBlock{
					Position:          protocol.BlockPos{int32(p[0]), int32(p[1]), int32(p[2])}, //nolint:gosec // block coordinates fit int32
					NewBlockRuntimeID: r.d.conf.Blocks.Hash(cover),
					Flags:             UpdateBlockFlags,
				})
			}
		}
	}
	return out
}

// blockCentre is the centre of the block at p.
func blockCentre(p cube.Pos) mgl32.Vec3 {
	return mgl32.Vec3{float32(p[0]) + 0.5, float32(p[1]) + 0.5, float32(p[2]) + 0.5}
}

// revealList adds to out the real blocks of the disguised blocks in list within radius of eye
// that were not sent yet.
func (v *Viewer) revealList(r *Region, st *revealState, list []cube.Pos, eye mgl32.Vec3, radius int, out []packet.Packet) []packet.Packet {
	r2 := float32(radius * radius)
	t := r.d.hide
	for _, p := range list {
		if blockCentre(p).Sub(eye).LenSqr() > r2 {
			continue
		}
		if _, ok := st.done[p]; ok {
			continue
		}
		b, ok := r.Block(p, 0)
		if !ok {
			continue
		}
		if t.coverOf(b) != noCover && (v.sightLeft <= 0 || !v.nowSees(r, eye, p)) {
			continue // out of sight: tried again on the next scan
		}
		if st.done == nil {
			st.done = map[cube.Pos]struct{}{}
		}
		st.done[p] = struct{}{}
		if t.coverOf(b) == noCover {
			continue
		}
		out = append(out, &packet.UpdateBlock{
			Position:          protocol.BlockPos{int32(p[0]), int32(p[1]), int32(p[2])}, //nolint:gosec // block coordinates fit int32
			NewBlockRuntimeID: r.d.conf.Blocks.Hash(b),
			Flags:             UpdateBlockFlags,
		})
		out = r.appendBlockEntity(out, p)
	}
	return out
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
	t := r.d.hide
	if t == nil || !t.any {
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

// nowSees is seesBlock for an ore not shown yet, paid from the tick's sight checks.
func (v *Viewer) nowSees(r *Region, eye mgl32.Vec3, p cube.Pos) bool {
	v.sightLeft--
	return r.seesBlock(eye, p)
}

// stillSees is seesBlock for an ore already shown, paid from the tick's checks for hiding: past
// them, a shown ore is kept as it is until the next tick.
func (v *Viewer) stillSees(r *Region, eye mgl32.Vec3, p cube.Pos) bool {
	v.hideLeft--
	return r.seesBlock(eye, p)
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
