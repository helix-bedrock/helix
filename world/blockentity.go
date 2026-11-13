package world

import (
	"maps"
	"slices"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
)

// BlockEntity is what a block keeps beyond its state, such as a chest's items
// (BDS's block actor). It lives with its chunk, so only the region owning that
// chunk may touch it.
type BlockEntity interface {
	// SaveData returns the compound BDS saves for it. The result goes to a
	// save worker, so it must not change afterwards.
	SaveData(pos cube.Pos) map[string]any
	// NetworkData returns the compound the client gets. The same rule applies.
	NetworkData(pos cube.Pos) map[string]any
}

// BlockEntityTicker is a block entity that ticks with its chunk.
type BlockEntityTicker interface {
	TickBlockEntity(r *Region, pos cube.Pos)
}

// BlockEntityPlacer is a block entity told when the block making it was set.
type BlockEntityPlacer interface {
	BlockEntityPlaced(r *Region, pos cube.Pos)
}

// BlockEntityRemover is a block entity told when its block was replaced by a
// block of another type. It is already out of the chunk.
type BlockEntityRemover interface {
	BlockEntityRemoved(r *Region, pos cube.Pos)
}

// BlockEntityKeeper is a block entity that stays when its block changes to another type,
// if it says so: a furnace lighting or going out swaps between two blocks and keeps its
// slots, as BDS's furnace does. A kept block entity is neither told nor replaced.
type BlockEntityKeeper interface {
	KeepBlockEntity(block uint32) bool
}

// BlockEntityBinder is a block entity given its chunk's handle whenever it is put in a
// chunk, so it can mark itself for saving from code that has no region at hand.
type BlockEntityBinder interface {
	BindBlockEntity(h ChunkHandle)
}

// ChunkHandle is the chunk a block entity is in. Runs: owner of that chunk.
type ChunkHandle struct{ c *Chunk }

// Changed marks the chunk to be saved at the end of the tick, as
// [Region.SaveBlockEntity] does. A zero handle, or one whose chunk has gone, does
// nothing that lasts.
func (h ChunkHandle) Changed() {
	if h.c != nil {
		h.c.dirty = true
	}
}

// BlockEntities makes block entities: the engine's list of those it runs.
type BlockEntities interface {
	// Load returns the live form of a saved block entity. False keeps it saved
	// as it is, as for those the engine does not run yet.
	Load(pos cube.Pos, saved map[string]any) (BlockEntity, bool)
	// New returns the block entity block makes, or nil. It is asked whenever a
	// block changes type, so it must be cheap.
	New(block uint32) BlockEntity
}

// SetBlockEntities sets who makes block entities. Set it before any player
// joins: chunks loaded earlier keep theirs saved. Runs: anywhere.
func (d *Dimension) SetBlockEntities(b BlockEntities) {
	d.blockEntities.Store(&b)
	listens := make([]bool, d.conf.Blocks.Count())
	for id := range listens {
		_, listens[id] = b.New(uint32(id)).(GameEventListener) //nolint:gosec // state IDs fit
	}
	d.listenerStates.Store(&listens)
}

// BlockEntityMaker returns who makes block entities now, or nil, so another can
// wrap it. Runs: anywhere.
func (d *Dimension) BlockEntityMaker() BlockEntities {
	if m := d.blockEntities.Load(); m != nil {
		return *m
	}
	return nil
}

// networkHidden are the saved fields the client never needs.
var networkHidden = [...]string{"Items", "Findable", "LootTable", "LootTableSeed"}

// savedNetworkData is the client's view of a saved compound.
func savedNetworkData(saved map[string]any) map[string]any {
	out := maps.Clone(saved)
	for _, k := range networkHidden {
		delete(out, k)
	}
	return out
}

// savedBlockEntity is a block entity the engine does not run yet: its data is
// kept as it was loaded, so the world loses nothing.
type savedBlockEntity struct {
	data map[string]any
}

// SaveData returns the data as it was loaded.
func (s savedBlockEntity) SaveData(cube.Pos) map[string]any { return s.data }

// NetworkData returns the data without what the client never needs.
func (s savedBlockEntity) NetworkData(cube.Pos) map[string]any { return savedNetworkData(s.data) }

// loadBlockEntities turns the saved block entities of a chunk that just loaded
// into live ones.
func (r *Region) loadBlockEntities(c *Chunk) {
	saved := c.col.BlockEntities
	c.col.BlockEntities = nil
	defer r.addListeners(c)
	if len(saved) == 0 {
		return
	}
	var maker BlockEntities
	if m := r.d.blockEntities.Load(); m != nil {
		maker = *m
	}
	for _, s := range saved {
		var be BlockEntity = savedBlockEntity{data: s.Data}
		if maker != nil {
			if live, ok := maker.Load(s.Pos, s.Data); ok {
				be = live
			}
		}
		c.addBlockEntity(s.Pos, be)
	}
}

// addListeners gives every block of the chunk whose block entity hears game events one, if it
// has none: generation places sculk sensors and shriekers without them, where BDS's setBlock
// makes one. Only sub chunks whose palette holds such a state are walked.
func (r *Region) addListeners(c *Chunk) {
	m, l := r.d.blockEntities.Load(), r.d.listenerStates.Load()
	if m == nil || l == nil {
		return
	}
	listens := *l
	for i, sub := range c.col.Chunk.Sub() {
		if sub == nil || sub.Empty() || !paletteListens(sub.Layer(0).Palette(), listens) {
			continue
		}
		baseY := int(c.col.Chunk.SubY(int16(i))) //nolint:gosec // sub chunk indices fit
		for x := range 16 {
			for y := range 16 {
				for z := range 16 {
					b := sub.Block(byte(x), byte(y), byte(z), 0) //nolint:gosec // local coordinates fit
					if int(b) >= len(listens) || !listens[b] {
						continue
					}
					pos := cube.Pos{int(c.pos[0])<<4 + x, baseY + y, int(c.pos[1])<<4 + z}
					if _, ok := c.blockEntities[pos]; ok {
						continue
					}
					if be := (*m).New(b); be != nil {
						c.addBlockEntity(pos, be)
					}
				}
			}
		}
	}
}

// paletteListens reports a state in the palette whose block entity hears game events.
func paletteListens(p *chunk.Palette, listens []bool) bool {
	for i := range p.Len() {
		if v := p.Value(uint16(i)); int(v) < len(listens) && listens[v] { //nolint:gosec // palette indices fit
			return true
		}
	}
	return false
}

// addBlockEntity puts be in the chunk in place of any other at pos.
func (c *Chunk) addBlockEntity(pos cube.Pos, be BlockEntity) {
	c.removeBlockEntity(pos)
	if c.blockEntities == nil {
		c.blockEntities = make(map[cube.Pos]BlockEntity)
	}
	c.blockEntities[pos] = be
	if b, ok := be.(BlockEntityBinder); ok {
		b.BindBlockEntity(ChunkHandle{c: c})
	}
	if _, ok := be.(BlockEntityTicker); ok {
		c.tickingEntities = append(c.tickingEntities, pos)
	}
	if _, ok := be.(GameEventListener); ok {
		c.listeners = append(c.listeners, pos)
	}
}

// removeBlockEntity takes the block entity at pos out of the chunk.
func (c *Chunk) removeBlockEntity(pos cube.Pos) (BlockEntity, bool) {
	be, ok := c.blockEntities[pos]
	if !ok {
		return nil, false
	}
	delete(c.blockEntities, pos)
	if i := slices.Index(c.tickingEntities, pos); i >= 0 {
		c.tickingEntities = slices.Delete(c.tickingEntities, i, i+1)
	}
	if i := slices.Index(c.listeners, pos); i >= 0 {
		c.listeners = slices.Delete(c.listeners, i, i+1)
	}
	return be, true
}

// blockTypeChanged runs after the block at pos on layer 0 changed from old to
// block: a new type drops the block entity of the old one and makes its own.
func (r *Region) blockTypeChanged(c *Chunk, pos cube.Pos, old, block uint32) {
	blocks := r.d.conf.Blocks
	if blocks.Name(old) == blocks.Name(block) {
		return
	}
	if k, ok := c.blockEntities[pos].(BlockEntityKeeper); ok && k.KeepBlockEntity(block) {
		c.dirty = true
		c.netChanged()
		return
	}
	if be, ok := c.removeBlockEntity(pos); ok {
		c.netChanged()
		if rem, ok := be.(BlockEntityRemover); ok {
			rem.BlockEntityRemoved(r, pos)
		}
	}
	m := r.d.blockEntities.Load()
	if m == nil {
		return
	}
	if be := (*m).New(block); be != nil {
		c.addBlockEntity(pos, be)
		c.dirty = true
		c.netChanged()
		if pl, ok := be.(BlockEntityPlacer); ok {
			pl.BlockEntityPlaced(r, pos)
		}
	}
}

// netChanged drops the chunk's network form.
func (c *Chunk) netChanged() {
	c.net = nil
	c.version++
}

// BlockEntity returns the block entity at pos, and false if there is none or
// its chunk is not loaded in this region. Runs: owner.
func (r *Region) BlockEntity(pos cube.Pos) (BlockEntity, bool) {
	c, ok := r.chunkAt(pos)
	if !ok {
		return nil, false
	}
	be, ok := c.blockEntities[pos]
	return be, ok
}

// SetBlockEntity puts be at pos in place of any other, telling neither. False if
// the chunk is not loaded in this region. Runs: owner.
func (r *Region) SetBlockEntity(pos cube.Pos, be BlockEntity) bool {
	c, ok := r.chunkAt(pos)
	if !ok {
		return false
	}
	c.addBlockEntity(pos, be)
	c.dirty = true
	c.netChanged()
	r.d.cache.drop(c.pos)
	return true
}

// TakeBlockEntity takes the block entity at pos out of the chunk without telling it,
// for something that moves it elsewhere. Runs: owner.
func (r *Region) TakeBlockEntity(pos cube.Pos) (BlockEntity, bool) {
	c, ok := r.chunkAt(pos)
	if !ok {
		return nil, false
	}
	be, ok := c.removeBlockEntity(pos)
	if ok {
		c.dirty = true
		c.netChanged()
		r.d.cache.drop(c.pos)
	}
	return be, ok
}

// SaveBlockEntity marks a block entity changed in a way only the save sees, such
// as a chest's items. Runs: owner.
func (r *Region) SaveBlockEntity(pos cube.Pos) {
	if c, ok := r.chunkAt(pos); ok {
		c.dirty = true
	}
}

// SendBlockEntity marks a block entity changed in a way the client sees and sends
// it to every player whose client has the chunk (BDS's BlockActorData). Runs: owner.
func (r *Region) SendBlockEntity(pos cube.Pos) {
	c, ok := r.chunkAt(pos)
	if !ok {
		return
	}
	be, ok := c.blockEntities[pos]
	if !ok {
		return
	}
	c.dirty = true
	c.netChanged()
	r.d.cache.drop(c.pos)
	r.Broadcast(pos, &packet.BlockActorData{
		Position: protocol.BlockPos{int32(pos[0]), int32(pos[1]), int32(pos[2])}, //nolint:gosec // block coordinates fit int32
		NBTData:  be.NetworkData(pos),
	}, nil)
}

// tickBlockEntities ticks the chunk's block entities, in the order they came. A
// tick may remove some.
func (r *Region) tickBlockEntities(c *Chunk) {
	for i := 0; i < len(c.tickingEntities); i++ {
		pos := c.tickingEntities[i]
		if t, ok := c.blockEntities[pos].(BlockEntityTicker); ok {
			t.TickBlockEntity(r, pos)
		}
	}
}

// savedBlockEntities returns the chunk's block entities as the store wants them.
func (c *Chunk) savedBlockEntities() []chunk.BlockEntity {
	if len(c.blockEntities) == 0 {
		return nil
	}
	out := make([]chunk.BlockEntity, 0, len(c.blockEntities))
	for pos, be := range c.blockEntities {
		out = append(out, chunk.BlockEntity{Pos: pos, Data: be.SaveData(pos)})
	}
	return sortedBlockEntities(out)
}

// networkBlockEntities returns what the client gets of the chunk's block entities.
func (c *Chunk) networkBlockEntities() []chunk.BlockEntity {
	if len(c.blockEntities) == 0 {
		return nil
	}
	out := make([]chunk.BlockEntity, 0, len(c.blockEntities))
	for pos, be := range c.blockEntities {
		out = append(out, chunk.BlockEntity{Pos: pos, Data: be.NetworkData(pos)})
	}
	return sortedBlockEntities(out)
}

// sortedBlockEntities puts them in a fixed order, so one chunk always saves and
// encodes to the same bytes.
func sortedBlockEntities(l []chunk.BlockEntity) []chunk.BlockEntity {
	slices.SortFunc(l, func(a, b chunk.BlockEntity) int {
		for i := range a.Pos {
			if d := a.Pos[i] - b.Pos[i]; d != 0 {
				return d
			}
		}
		return 0
	})
	return l
}
