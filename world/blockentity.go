package world

import (
	"bytes"
	"errors"
	"maps"
	"slices"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
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

// BlockEntityMover is a block entity a piston may carry as it is, as BDS's moving
// block keeps its block actor: Moving is told when it is taken out at from, Moved
// when it is set down at to, after it is back in a chunk. One that is not a mover is
// carried as its saved data and loaded again where it lands, which costs a save and
// a load per push, so a block entity with much data should be one.
type BlockEntityMover interface {
	BlockEntityMoving(r *Region, from cube.Pos)
	BlockEntityMoved(r *Region, to cube.Pos)
}

// ColdBlockEntity is a block entity that leaves its big data, such as a chest's items, on
// disk while nothing uses it, so memory does not grow with what a world stores. Cold reports
// whether its data is on disk only: a save then copies its stored compound as it is
// (SaveData is not asked), and the world calls Warm before taking it out of its chunk. The
// world asks it to Cool, dropping what it can read back, only while the chunk's stored copy
// is current: nothing changed since the last save, which has reached the store. Warm reads
// it back through its ChunkHandle's Stored. Runs: owner of the chunk.
type ColdBlockEntity interface {
	BlockEntity
	Cold() bool
	Cool(r *Region, pos cube.Pos) bool
	Warm(pos cube.Pos)
}

// RawBlockEntities is a BlockEntities that loads a block entity from its stored bytes,
// decoding only what it needs: a chest that starts cold reads its slot counts and leaves
// the rest on disk. False falls back to Load. Runs: on a chunk load worker.
type RawBlockEntities interface {
	LoadRaw(pos cube.Pos, raw []byte) (BlockEntity, bool)
}

// BlockEntityBinder is a block entity given its chunk's handle whenever it is put in a
// chunk, so it can mark itself for saving from code that has no region at hand.
type BlockEntityBinder interface {
	BindBlockEntity(h ChunkHandle)
}

// ChunkHandle is the chunk a block entity is in. Runs: owner of that chunk.
type ChunkHandle struct {
	c *Chunk
	d *Dimension
}

// errNoBlockEntityStore is a store that cannot read one block entity back.
var errNoBlockEntityStore = errors.New("world: store cannot load single block entities")

// Stored reads the block entity at pos as its chunk last stored it, for a ColdBlockEntity
// warming. It reads the disk, so it is for data not used every tick. Runs: owner of the
// chunk.
func (h ChunkHandle) Stored(pos cube.Pos) (map[string]any, error) {
	if h.c == nil || h.d == nil {
		return nil, errNoBlockEntityStore
	}
	l, ok := h.d.conf.Store.(mcdb.BlockEntityLoader)
	if !ok {
		return nil, errNoBlockEntityStore
	}
	return l.LoadBlockEntity(h.c.pos, h.d.conf.Dim, pos)
}

// StoredLater is Stored read on the chunk's load worker, so a block entity in a chunk of
// hundreds of megabytes does not stall its region's tick while it is found: done runs on the
// chunk's owner afterwards, unless the chunk unloaded meanwhile. The read waits for the
// chunk's saves already queued. Runs: owner of the chunk.
func (h ChunkHandle) StoredLater(pos cube.Pos, done func(r *Region, saved map[string]any, err error)) {
	if h.c == nil || h.d == nil {
		done(nil, nil, errNoBlockEntityStore)
		return
	}
	d, c := h.d, h.c
	d.pool.do(c.pos, prioNeeded, func() {
		defer d.sched.recovered(d.log)
		saved, err := h.Stored(pos)
		d.postAt(c.pos, func(r *Region) {
			if r.chunks[c.pos] == c {
				done(r, saved, err)
			}
		})
	})
}

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
	// as it is, as for those the engine does not run yet. Runs: on a chunk load
	// worker, many at once, so it may read only saved and data that never changes.
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

// rawNetworkData is the client's view of a stored compound, decoding none of what it leaves
// out.
func rawNetworkData(raw []byte) map[string]any {
	m, err := nbt.DecodeWithout(raw, nbt.LittleEndian, networkHiddenTag)
	if err != nil {
		return map[string]any{}
	}
	return m
}

// networkHiddenTag reports a saved field the client never needs. The lists named are skipped
// whole, so nothing inside them is asked about.
func networkHiddenTag(name string) bool { return slices.Contains(networkHidden[:], name) }

// savedBlockEntity is a block entity the engine does not run yet: its data is
// kept as it was loaded, decoded or as its stored bytes, so the world loses nothing.
type savedBlockEntity struct {
	data map[string]any
	raw  []byte
}

// SaveData returns the data as it was loaded.
func (s savedBlockEntity) SaveData(cube.Pos) map[string]any {
	if s.raw != nil {
		m, err := nbt.DecodeWithout(s.raw, nbt.LittleEndian, nil)
		if err != nil {
			return map[string]any{}
		}
		return m
	}
	return s.data
}

// NetworkData returns the data without what the client never needs.
func (s savedBlockEntity) NetworkData(cube.Pos) map[string]any {
	if s.raw != nil {
		return rawNetworkData(s.raw)
	}
	return savedNetworkData(s.data)
}

// loadedBlockEntity is a block entity a load worker made, waiting to join its chunk.
type loadedBlockEntity struct {
	pos cube.Pos
	be  BlockEntity
}

// makeBlockEntities turns the saved block entities of a chunk being loaded into live ones. It
// runs on the load worker, not the tick: a chunk of huge chests takes hundreds of
// milliseconds to read into items, which would stall its region's tick.
func (d *Dimension) makeBlockEntities(saved []chunk.BlockEntity) []loadedBlockEntity {
	if len(saved) == 0 {
		return nil
	}
	var maker BlockEntities
	if m := d.blockEntities.Load(); m != nil {
		maker = *m
	}
	out := make([]loadedBlockEntity, 0, len(saved))
	for _, s := range saved {
		out = append(out, loadedBlockEntity{pos: s.Pos, be: makeBlockEntity(maker, s)})
	}
	return out
}

// storedBlockEntities reads the block entities of a loaded chunk from a store that gives
// them one at a time (mcdb.BlockEntityLoader), adding each one's network data to network:
// the chunk's block entity value, which chests full of books make hundreds of megabytes, is
// never held whole, only its biggest block entity. Runs: on the load worker.
func (d *Dimension) storedBlockEntities(pos mcdb.ChunkPos, network *[]chunk.BlockEntity) ([]loadedBlockEntity, error) {
	l, ok := d.conf.Store.(mcdb.BlockEntityLoader)
	if !ok {
		return nil, nil
	}
	var maker BlockEntities
	if m := d.blockEntities.Load(); m != nil {
		maker = *m
	}
	var out []loadedBlockEntity
	err := l.EachBlockEntity(pos, d.conf.Dim, func(at cube.Pos, raw []byte) error {
		*network = append(*network, chunk.BlockEntity{Pos: at, Data: rawNetworkData(raw)})
		out = append(out, loadedBlockEntity{pos: at, be: makeBlockEntity(maker, chunk.BlockEntity{Pos: at, Raw: raw})})
		return nil
	})
	return out, err
}

// makeBlockEntity is the live form of one saved block entity: from its stored bytes when the
// maker reads them, else decoded, else kept as it was saved. Stored bytes are only read
// during the call: what is kept of them is copied.
func makeBlockEntity(maker BlockEntities, s chunk.BlockEntity) BlockEntity {
	if s.Raw != nil {
		if rl, ok := maker.(RawBlockEntities); ok {
			if live, ok := rl.LoadRaw(s.Pos, s.Raw); ok {
				return live
			}
		}
		if maker == nil {
			return savedBlockEntity{raw: bytes.Clone(s.Raw)}
		}
		data, err := nbt.DecodeWithout(s.Raw, nbt.LittleEndian, nil)
		if err != nil {
			return savedBlockEntity{raw: bytes.Clone(s.Raw)}
		}
		s.Data = data
	}
	if maker != nil {
		if live, ok := maker.Load(s.Pos, s.Data); ok {
			return live
		}
	}
	return savedBlockEntity{data: s.Data}
}

// loadBlockEntities adds the block entities a load worker made to a chunk that just joined.
func (r *Region) loadBlockEntities(c *Chunk, loaded []loadedBlockEntity) {
	defer r.addListeners(c)
	for _, l := range loaded {
		c.addBlockEntity(l.pos, l.be)
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
						c.dirty = true // a new block entity the store does not have yet
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
		b.BindBlockEntity(ChunkHandle{c: c, d: c.d})
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
	if cb, ok := c.blockEntities[pos].(ColdBlockEntity); ok && cb.Cold() {
		cb.Warm(pos) // its stored compound stays behind at pos
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

// ResendBlock sends the block at pos on layer 0 to the chunk's viewers again, unchanged. A
// client draws some block entity data (a cauldron's colour) into the chunk's mesh and only
// redraws on a block update, so data sent after the block's own update needs another one.
// Runs: owner.
func (r *Region) ResendBlock(pos cube.Pos) {
	if b, ok := r.Block(pos, 0); ok {
		r.updateBlock(pos, 0, b)
	}
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
func (c *Chunk) savedBlockEntities() (out []chunk.BlockEntity, keep []cube.Pos) {
	if len(c.blockEntities) == 0 {
		return nil, nil
	}
	out = make([]chunk.BlockEntity, 0, len(c.blockEntities))
	for pos, be := range c.blockEntities {
		switch be := be.(type) {
		case ColdBlockEntity:
			if be.Cold() {
				keep = append(keep, pos)
				continue
			}
		case savedBlockEntity:
			if be.raw != nil {
				out = append(out, chunk.BlockEntity{Pos: pos, Raw: be.raw})
				continue
			}
		}
		out = append(out, chunk.BlockEntity{Pos: pos, Data: be.SaveData(pos)})
	}
	return sortedBlockEntities(out), keep
}

// coolEveryTicks is how often each chunk's block entities are asked to leave their data on
// disk: 10 seconds, spread over the chunks by position.
const coolEveryTicks = 200

// coolRound asks the block entities of the chunks whose turn it is to leave their data on
// disk, in chunks whose stored copy is current.
func (r *Region) coolRound() {
	if _, ok := r.d.conf.Store.(mcdb.BlockEntityLoader); !ok {
		return
	}
	for _, c := range r.chunks {
		if (r.ticks+autoSavePhase(c.pos))%coolEveryTicks != 0 || c.dirty || c.discard || c.saving.Load() > 0 {
			continue
		}
		for pos, be := range c.blockEntities {
			if cb, ok := be.(ColdBlockEntity); ok && !cb.Cold() {
				cb.Cool(r, pos)
			}
		}
	}
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
