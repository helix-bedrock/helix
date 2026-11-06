package player

import (
	"maps"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Chest rules from BDS 1.26.60.
const (
	// chestSize is one chest's slots; a double chest is two, the lead's first.
	chestSize = 27
	// chestEventOpen is the BlockEvent saying how a chest's lid stands.
	chestEventOpen = 1
	// chestEventResend is how often, in ticks, an open chest says so again.
	chestEventResend = 80
	// chestLidSpeed is how far the lid moves per tick, 0 to 1.
	chestLidSpeed = 0.1
	// chestLidCloseSound is where a closing lid makes its sound.
	chestLidCloseSound = 0.5
	// chestLidHeight is how far above the block below it the lid needs room,
	// chestLidInset how far in from the sides.
	chestLidHeight = 0.5
	chestLidInset  = 0.0625
	// chestViewRange is how far a player may be from an open chest.
	chestViewRange = 12
	// Thrown items land inside the block's middle, in piles of chestPileMin plus
	// up to chestPileExtra-1 items, moving a little and upward.
	chestThrowInset  = 0.1
	chestThrowSpan   = 0.8
	chestThrowSpeed  = 0.05
	chestThrowUp     = 0.2
	chestPileMin     = 10
	chestPileExtra   = 21
	chestPickupDelay = 10
	// chestEntityID and enderChestEntityID are the saved IDs of their block
	// entities.
	chestEntityID      = "Chest"
	enderChestEntityID = "EnderChest"
)

// Kinds of chest. Only chests of one kind pair.
const (
	chestNone int8 = iota - 1
	chestPlain
	chestTrapped
	chestCopper
	chestEnder
	chestMachine
	chestShulker
)

// cardinalState is the block state a chest faces by.
const cardinalState = direction.CardinalState

// chestTable holds the chest facts of every block state. It does not change after
// it is built, so any region may read it.
type chestTable struct {
	kind []int8
	// dir is the direction a chest state faces.
	dir []direction.Cardinal
	// clear marks the states that never stop a chest under them opening.
	clear []bool
}

// newChestTable builds the table from BDS's chest obstruction data.
func newChestTable(blocks *registry.Blocks) (*chestTable, error) {
	obstruction, err := data.ChestObstructionData()
	if err != nil {
		return nil, err
	}
	whole := make(map[string]bool, len(obstruction.Blocks))
	for _, name := range obstruction.Blocks {
		whole[name] = true
	}
	n := blocks.BlockCount()
	t := &chestTable{kind: make([]int8, n), dir: make([]direction.Cardinal, n), clear: make([]bool, n)}
	for id := range n {
		state := uint32(id) //nolint:gosec // state IDs fit uint32
		name, states := blocks.Name(state), blocks.States(state)
		t.kind[id] = chestKindOf(name)
		if dir, ok := states[cardinalState].(string); ok {
			// A value BDS does not have counts as south, its first.
			t.dir[id], _ = direction.Parse(dir)
		}
		t.clear[id] = whole[name] || slices.ContainsFunc(obstruction.States[name], func(listed map[string]any) bool {
			return sameStates(listed, states)
		})
	}
	return t, nil
}

// chestKindOf is the kind of chest a block is.
func chestKindOf(name string) int8 {
	switch {
	case name == "minecraft:chest":
		return chestPlain
	case name == "minecraft:trapped_chest":
		return chestTrapped
	case strings.HasSuffix(name, "copper_chest"):
		return chestCopper
	case name == "minecraft:ender_chest":
		return chestEnder
	case machineSpecs[name] != nil:
		return chestMachine
	case isShulkerBox(name):
		return chestShulker
	}
	return chestNone
}

// sameStates reports whether states read from JSON, where numbers are floats, are
// the block's.
func sameStates(listed, states map[string]any) bool {
	if len(listed) != len(states) {
		return false
	}
	for k, want := range listed {
		got, ok := states[k]
		if !ok {
			return false
		}
		f, isNum := want.(float64)
		switch v := got.(type) {
		case uint8:
			if !isNum || float64(v) != f {
				return false
			}
		case int32:
			if !isNum || float64(v) != f {
				return false
			}
		default:
			if got != want {
				return false
			}
		}
	}
	return true
}

// Chest is a chest's block entity: its items, who has it open and which chest it
// is paired with. A double chest is two of these, the lead holding the first half.
// An ender chest is one too, with the same lid and rules, but it never pairs and its
// viewers each see their own items, which are kept on the player. Runs: owner of its
// chunk.
type Chest struct {
	s     *Host
	pos   cube.Pos
	ender bool
	// cart is the minecart holding these slots, if no block does.
	cart *Minecart
	// shulker says this is a shulker box: a chest that never pairs, whose lid opens
	// towards facing and whose items leave with the dropped box.
	shulker bool
	facing  uint8
	// machine says this is a hopper, dispenser or dropper rather than a chest: the
	// same slots and window, without a lid or a partner.
	machine *machineSpec
	// furnace is a furnace's, blast furnace's or smoker's own part.
	furnace *Furnace
	// cooldown and lastTick are a hopper's, in ticks.
	cooldown int32
	lastTick int64
	// disabled is a crafter's turned off slots, by bit, and crafting how long it still
	// shows that it crafted.
	disabled uint16
	crafting int32
	items    [chestSize]ItemStack
	// saved keeps the other fields of the saved chest (CustomName, LootTable...),
	// written back as they were.
	saved map[string]any
	// lead says this half comes first; a chest with no partner is its own lead.
	lead bool
	// paired means partner is the other half. pending means partner was loaded
	// from the save and is not linked yet, unpaired that it was paired once.
	paired, pending, unpaired bool
	partner                   cube.Pos
	findable                  bool
	// changed means the client must be told at the next tick.
	changed bool
	// viewers have this chest open, on the lead only.
	viewers []*Player
	// open is the lid's goal, lid how far it stands, resend the ticks since the
	// last BlockEvent.
	open   bool
	lid    float32
	resend int32
	// inv wraps this chest's slots; whole wraps both halves of a double chest, on the
	// lead, made for wholeOf.
	inv, whole *Inventory
	wholeOf    *Chest
	// chunk is the chunk the chest is in, to save it when a slot changes.
	chunk world.ChunkHandle
}

// BindBlockEntity keeps the chunk the chest is in.
func (c *Chest) BindBlockEntity(h world.ChunkHandle) { c.chunk = h }

// inventoryViewers are the players with the chest open; the lead keeps them.
func (c *Chest) inventoryViewers() []*Player { return c.viewers }

// inventoryChanged saves the chest. A cart's slots are saved with the cart.
func (c *Chest) inventoryChanged(int) { c.chunk.Changed() }

// doubleHolder holds both halves of a double chest's slots.
type doubleHolder struct{ lead, follower *Chest }

func (h doubleHolder) inventoryViewers() []*Player { return h.lead.viewers }

func (h doubleHolder) inventoryChanged(slot int) {
	if slot < chestSize {
		h.lead.chunk.Changed()
		return
	}
	h.follower.chunk.Changed()
}

// Inventory returns the chest's slots: both halves of a double chest from either half,
// the lead's first. Nil for an ender chest, whose slots are each player's
// ([Player.EnderChest]). Runs: owner of the chunk.
func (c *Chest) Inventory(r *world.Region) *Inventory {
	if c.ender {
		return nil
	}
	lead, follower := c.halves(r)
	if follower == nil {
		return lead.ownInventory()
	}
	if lead.whole == nil || lead.wholeOf != follower {
		lead.whole = &Inventory{
			s: c.s, kind: ContainerChest, parts: [2][]ItemStack{lead.items[:], follower.items[:]},
			holder: doubleHolder{lead: lead, follower: follower},
		}
		lead.wholeOf = follower
	}
	return lead.whole
}

// ownInventory is this half's slots alone.
func (c *Chest) ownInventory() *Inventory {
	if c.inv == nil {
		c.inv = &Inventory{s: c.s, kind: c.Kind(), parts: [2][]ItemStack{c.items[:c.halfSize()]}, holder: c}
	}
	return c.inv
}

// Kind returns what the chest is: a chest, ender chest, shulker box, hopper, dispenser,
// dropper or crafter, or a cart's slots.
func (c *Chest) Kind() ContainerKind {
	switch {
	case c.ender:
		return ContainerEnderChest
	case c.shulker:
		return ContainerShulkerBox
	case c.cart != nil && c.machine != nil && c.machine.hopper:
		return ContainerHopperMinecart
	case c.cart != nil:
		return ContainerChestMinecart
	case c.machine == nil:
		return ContainerChest
	case c.machine.hopper:
		return ContainerHopper
	case c.machine.dropper:
		return ContainerDropper
	case c.machine.crafter:
		return ContainerCrafter
	case c.machine.furnace != nil:
		return c.machine.furnace.kind
	}
	return ContainerDispenser
}

// chestEntities is the world's list of block entities the engine runs.
type chestEntities struct {
	s *Host
}

// New returns a new chest for a chest block.
func (e chestEntities) New(block uint32) world.BlockEntity {
	if e.s.reg.Blocks.Name(block) == jukeboxBlock {
		return &jukeboxEntity{s: e.s}
	}
	if e.s.reg.Blocks.Name(block) == enchantingTable {
		return &enchantTableEntity{}
	}
	if e.s.reg.Blocks.Name(block) == blockuse.Bed {
		return &bedEntity{}
	}
	t := e.s.chests
	if int(block) >= len(t.kind) || t.kind[block] == chestNone {
		return nil
	}
	if spec := machineSpecs[e.s.reg.Blocks.Name(block)]; spec != nil {
		return e.s.newMachine(cube.Pos{}, spec)
	}
	return &Chest{s: e.s, lead: true, ender: t.kind[block] == chestEnder, shulker: t.kind[block] == chestShulker, facing: shulkerDefaultFacing}
}

// Load brings back a chest as BDS loads one. A follower waits for its lead's tick
// to link them; only the lead reads the partner's position.
func (e chestEntities) Load(pos cube.Pos, saved map[string]any) (world.BlockEntity, bool) {
	id, _ := saved["id"].(string)
	if id == jukeboxEntityID {
		return e.s.loadJukebox(saved), true
	}
	if id == bedEntityID {
		return loadBed(saved), true
	}
	if id == enchantTableEntityID {
		return &enchantTableEntity{saved: maps.Clone(saved)}, true
	}
	if spec := machineByID(id); spec != nil {
		return e.s.loadMachine(pos, saved, spec), true
	}
	if id != chestEntityID && id != enderChestEntityID && id != shulkerEntityID {
		return nil, false
	}
	c := &Chest{s: e.s, pos: pos, lead: true, ender: id == enderChestEntityID, shulker: id == shulkerEntityID, saved: maps.Clone(saved)}
	c.facing, _ = saved[shulkerFacingTag].(uint8)
	for _, item := range compounds(saved["Items"]) {
		slot, _ := item["Slot"].(uint8)
		if int(slot) < chestSize {
			c.items[slot] = withID(itemFromNBT(item), e.s.nextStackID)
		}
	}
	if v, ok := saved["pairlead"].(uint8); ok {
		c.lead = v != 0
	}
	x, okX := saved["pairx"].(int32)
	z, okZ := saved["pairz"].(int32)
	if okX && okZ && c.lead {
		c.pending, c.partner = true, cube.Pos{int(x), pos[1], int(z)}
	}
	if _, ok := saved["forceunpair"]; ok {
		c.lead, c.paired = true, false
	}
	findable, _ := saved["Findable"].(uint8)
	c.findable = findable != 0
	for _, k := range [...]string{"Items", "Findable", "pairlead", "pairx", "pairz", "forceunpair"} {
		delete(c.saved, k)
	}
	return c, true
}

// nextStackID numbers an item stack for the network.
func (s *Host) nextStackID() int32 { return s.stackIDs.Add(1) }

// NetworkData is what the client gets of a chest: where it is and its partner.
func (c *Chest) NetworkData(pos cube.Pos) map[string]any {
	m := map[string]any{"isMovable": uint8(1)}
	for _, k := range [...]string{"isMovable", "CustomName"} {
		if v, ok := c.saved[k]; ok {
			m[k] = v
		}
	}
	m["id"] = chestEntityID
	if c.ender {
		m["id"] = enderChestEntityID
	}
	if c.shulker {
		m["id"], m[shulkerFacingTag] = shulkerEntityID, c.facing
	}
	if c.machine != nil {
		m["id"] = c.machine.id
		if c.machine.crafter {
			m[disabledSlotsTag] = int16(c.disabled) //nolint:gosec // nine bits
		}
	}
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	switch {
	case c.paired || c.pending:
		m["pairlead"] = boolByte(c.lead)
		m["pairx"], m["pairz"] = int32(c.partner[0]), int32(c.partner[2]) //nolint:gosec // block coordinates fit int32
	case c.unpaired:
		m["forceunpair"] = uint8(1)
	}
	return m
}

// SaveData is what BDS saves of a chest: the same plus the items.
func (c *Chest) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(c.saved)
	if m == nil {
		m = map[string]any{}
	}
	maps.Copy(m, c.NetworkData(pos))
	items := []any{}
	for i, s := range c.items {
		if !s.Empty() {
			items = append(items, s.nbt(i, true))
		}
	}
	m["Items"], m["Findable"] = items, boolByte(c.findable)
	if c.machine != nil {
		delete(m, "Findable")
		if c.machine.hopper {
			m[transferCooldownTag] = c.cooldown
		}
		if c.furnace != nil {
			c.furnace.save(m)
		}
	}
	return m
}

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// BlockEntityPlaced tries the two neighbours a chest facing this way can pair with.
func (c *Chest) BlockEntityPlaced(r *world.Region, pos cube.Pos) {
	c.pos = pos
	if c.ender || c.shulker || c.machine != nil {
		return
	}
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	// BDS tries the two neighbours beside the way the chest faces, in this order.
	for _, side := range c.s.chests.dir[block].Beside() {
		c.tryPair(r, pos.Side(side))
	}
}

// tryPair pairs with the chest at pos if it can: this chest follows, the one that
// was there leads. Anyone who has that chest open is closed out.
func (c *Chest) tryPair(r *world.Region, pos cube.Pos) {
	if c.paired {
		return
	}
	o := c.s.chestAt(r, pos)
	if o == nil || !c.canPair(r, o) {
		return
	}
	if g := c.s.game; g.chestPairEv.Has() {
		e := ChestPairEvent{Region: r, Chest: c, Other: o}
		g.chestPairEv.fire(c.s.log, &e)
		if e.Cancelled() {
			return
		}
	}
	o.closeViewers(r)
	c.link(o.pos, false)
	o.link(c.pos, true)
}

// canPair is BDS's rule for two chests: the other has no partner, is on the same
// level, of the same kind, faces the same way and stands beside this one rather
// than in front of it.
func (c *Chest) canPair(r *world.Region, o *Chest) bool {
	if o.paired || o.pos[1] != c.pos[1] {
		return false
	}
	a, okA := r.Block(c.pos, 0)
	b, okB := r.Block(o.pos, 0)
	if !okA || !okB {
		return false
	}
	t := c.s.chests
	if t.kind[a] == chestNone || t.kind[a] == chestEnder || t.kind[a] != t.kind[b] || t.dir[a] != t.dir[b] {
		return false
	}
	alongX := t.dir[a].FacesZ()
	if o.pos[0] == c.pos[0] {
		return !alongX
	}
	return alongX
}

// link makes pos this chest's partner.
func (c *Chest) link(pos cube.Pos, lead bool) {
	c.paired, c.pending, c.unpaired, c.partner, c.lead = true, false, false, pos, lead
	c.changed = true
}

// forget drops the partner, as BDS keeps a chest that once had one.
func (c *Chest) forget() {
	c.paired, c.pending, c.lead, c.unpaired, c.changed = false, false, true, true, true
}

// unpair takes the two halves apart, closing out anyone with them open.
func (c *Chest) unpair(r *world.Region) {
	lead, _ := c.halves(r)
	lead.closeViewers(r)
	if o := c.mate(r); o != nil {
		o.forget()
	}
	c.forget()
}

// mate is the other half, if its chunk is here.
func (c *Chest) mate(r *world.Region) *Chest {
	if !c.paired {
		return nil
	}
	return c.s.chestAt(r, c.partner)
}

// halves returns the lead and the follower, nil if the chest is single.
func (c *Chest) halves(r *world.Region) (lead, follower *Chest) {
	o := c.mate(r)
	switch {
	case o == nil:
		return c, nil
	case c.lead:
		return c, o
	}
	return o, c
}

// chestAt returns the chest at pos if there is one in this region.
func (s *Host) chestAt(r *world.Region, pos cube.Pos) *Chest {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil
	}
	c, _ := be.(*Chest)
	return c
}

// BlockEntityRemoved takes a broken chest apart from its partner and throws its
// half of the items.
func (c *Chest) BlockEntityRemoved(r *world.Region, pos cube.Pos) {
	c.pos = pos
	if c.shulker {
		c.closeViewers(r)
		return
	}
	c.unpair(r)
	c.closeViewers(r)
	if c.furnace != nil {
		c.furnaceBroken(r, pos)
	}
	items := c.items[:]
	if g := c.s.game; !c.ender && g.chestSpillEv.Has() {
		e := ChestSpillEvent{Region: r, Chest: c, Pos: pos, Items: slices.Clone(items)}
		g.chestSpillEv.fire(c.s.log, &e)
		items = e.Items
		if e.Cancelled() {
			items = nil
		}
	}
	for _, s := range items {
		c.throwStack(r, s)
	}
	c.items = [chestSize]ItemStack{}
}

// throwStack throws a slot's items out in piles, as BDS's container drop does
// (0xB53D210): one place per slot, drawn before the first pile, so the piles
// start together and merge on the ground; each pile gets its own velocity.
func (c *Chest) throwStack(r *world.Region, s ItemStack) {
	if s.Empty() {
		return
	}
	at := blockVec(c.pos).Add(mgl32.Vec3{chestThrowOffset(), chestThrowOffset(), chestThrowOffset()})
	for !s.Empty() {
		pile := s
		pile.Count = min(uint8(rand.IntN(chestPileExtra))+chestPileMin, s.Count) //nolint:gosec // an item's scatter, not a secret
		s.Count -= pile.Count
		vel := mgl32.Vec3{chestThrowDrift(), chestThrowDrift() + chestThrowUp, chestThrowDrift()}
		c.s.spawnItem(r.Dimension(), pile, at, vel, world.Rotation{}, chestPickupDelay, 0)
	}
}

func chestThrowOffset() float32 {
	return rand.Float32()*chestThrowSpan + chestThrowInset //nolint:gosec // an item's scatter, not a secret
}

func chestThrowDrift() float32 {
	return float32(rand.NormFloat64()) * chestThrowSpeed //nolint:gosec // an item's scatter, not a secret
}

// TickBlockEntity is BDS's chest tick: tell clients of changes, keep the pair
// right, and move the lid with its sounds.
func (c *Chest) TickBlockEntity(r *world.Region, pos cube.Pos) {
	c.pos = pos
	if c.changed {
		c.changed = false
		r.SendBlockEntity(pos)
	}
	if c.machine != nil {
		if c.machine.hopper {
			c.tickHopper(r, pos, int(c.s.redstone.Get().Hopper.ChainDepth))
		}
		if c.machine.crafter {
			c.tickCrafter(r, pos)
		}
		if c.furnace != nil {
			c.tickFurnace(r, pos)
		}
		return
	}
	if !c.shulker {
		c.tickPair(r)
	}
	if c.open {
		c.resend++
		if c.resend >= chestEventResend {
			c.resend = 0
			c.blockEvent(r)
		}
		if c.lid == 0 {
			c.lidSound(r, c.openSound())
		}
	}
	was := c.lid
	switch {
	case c.open && was < 1:
		c.lid = min(was+c.lidSpeed(), 1)
	case !c.open && was > 0:
		c.lid = was - c.lidSpeed()
		if was >= chestLidCloseSound && c.lid < chestLidCloseSound {
			c.lidSound(r, c.closeSound())
		}
		if c.lid < 0 {
			c.lid = 0
		}
	}
}

// tickPair links a pair loaded from the save once the partner's chunk is here, and
// drops a partner that is gone.
func (c *Chest) tickPair(r *world.Region) {
	switch {
	case c.pending:
		if _, loaded := r.Block(c.partner, 0); !loaded {
			return
		}
		c.pending = false
		if o := c.s.chestAt(r, c.partner); o != nil && c.canPair(r, o) {
			c.link(o.pos, true)
			o.link(c.pos, false)
		}
	case c.paired:
		if _, loaded := r.Block(c.partner, 0); !loaded {
			return
		}
		o := c.s.chestAt(r, c.partner)
		if o == nil {
			c.forget()
			return
		}
		if !o.paired || o.partner != c.pos {
			if c.canPair(r, o) {
				o.link(c.pos, !c.lead)
			} else {
				c.unpair(r)
			}
		}
	default:
		c.lead = true
	}
}

// openSound is the sound of the lid starting to open.
func (c *Chest) openSound() string {
	if c.shulker {
		return packet.SoundEventShulkerBoxOpen
	}
	if c.ender {
		return packet.SoundEventEnderChestOpen
	}
	return packet.SoundEventChestOpen
}

// closeSound is the sound of the lid closing.
func (c *Chest) closeSound() string {
	if c.shulker {
		return packet.SoundEventShulkerBoxClosed
	}
	if c.ender {
		return packet.SoundEventEnderChestClosed
	}
	return packet.SoundEventChestClosed
}

// lidSound plays a lid's sound at the middle of the whole chest.
func (c *Chest) lidSound(r *world.Region, sound string) {
	at := blockVec(c.pos)
	if c.paired {
		at[0] = (at[0] + float32(c.partner[0])) / 2
		at[2] = (at[2] + float32(c.partner[2])) / 2
	}
	block, ok := r.Block(c.pos, 0)
	if !ok {
		return
	}
	playSound(r, c.pos, c.s.blockSound(sound, at.Add(mgl32.Vec3{0.5, 0.5, 0.5}), block), nil)
}

// blockEvent tells the players near how the lid stands.
func (c *Chest) blockEvent(r *world.Region) {
	if c.cart != nil {
		return
	}
	if c.machine != nil {
		return
	}
	r.Broadcast(c.pos, &packet.BlockEvent{
		Position:  blockPos(c.pos),
		EventType: chestEventOpen, EventData: int32(boolByte(c.open)),
	}, nil)
}

// addViewer is BDS's start open: the first viewer opens the lid.
func (c *Chest) addViewer(r *world.Region, p *Player) {
	if slices.Contains(c.viewers, p) {
		return
	}
	c.viewers = append(c.viewers, p)
	c.open = true
	c.viewersChanged(r)
	if len(c.viewers) == 1 {
		c.blockEvent(r)
	}
}

// removeViewer is BDS's stop open: the last viewer closes the lid.
func (c *Chest) removeViewer(r *world.Region, p *Player) {
	i := slices.Index(c.viewers, p)
	if i < 0 {
		return
	}
	c.viewers = slices.Delete(c.viewers, i, i+1)
	c.viewersChanged(r)
	if len(c.viewers) == 0 {
		c.open = false
		c.blockEvent(r)
	}
}

// closeViewers closes the window of everyone with the chest open. BDS only clears
// its list and leaves their windows up showing the chest as it was; Helix closes
// them, because a window with no chest behind it must not take items.
func (c *Chest) closeViewers(r *world.Region) {
	if len(c.viewers) == 0 {
		return
	}
	for _, v := range slices.Clone(c.viewers) {
		c.removeViewer(r, v)
		// One who just moved to another region closes itself on its next tick.
		if v.entity != nil && v.entity.Region() == r {
			v.closeWindow(r, true)
		}
	}
}

// useChest is what clicking a chest does: it is taken whether or not the chest
// opens. BDS always has a block entity here; a chest placed before Helix had them
// gets one now, so old test worlds still work.
func (p *Player) useChest(r *world.Region, u *BlockUse) bool {
	c := p.s.chestAt(r, u.Pos)
	if c == nil {
		if spec := machineSpecs[p.s.reg.Blocks.Name(u.Block)]; spec != nil {
			c = p.s.newMachine(u.Pos, spec)
		} else {
			c = &Chest{s: p.s, pos: u.Pos, lead: true, ender: p.s.chests.kind[u.Block] == chestEnder, shulker: p.s.chests.kind[u.Block] == chestShulker, facing: shulkerDefaultFacing}
		}
		if !r.SetBlockEntity(u.Pos, c) {
			return false
		}
	}
	if !p.abilities.Flag(AbilityOpenContainers) {
		return true
	}
	if p.window.open {
		return true
	}
	blocked := !c.canOpen(r, u.Pos)
	if o := c.mate(r); o != nil && !o.canOpen(r, o.pos) {
		blocked = true
	}
	o := p.chestOpening(r, c, u.Pos)
	o.blocked = blocked
	if p.mayOpen(r, &o) {
		p.openChest(r, c, u.Pos)
	}
	return true
}

// chestOpening is the window a chest opens.
func (p *Player) chestOpening(r *world.Region, c *Chest, pos cube.Pos) windowOpening {
	inv := c.Inventory(r)
	if c.ender {
		inv = p.EnderChest()
	}
	return windowOpening{kind: c.Kind(), window: c.windowType(), pos: pos, entity: -1, inv: inv, chest: c}
}

// canOpen is BDS's check of the block above: listed blocks never stop the lid, any
// other does if a collision box of it reaches the lid. Cats come with mobs.
func (c *Chest) canOpen(r *world.Region, pos cube.Pos) bool {
	if c.machine != nil {
		return true
	}
	if c.shulker {
		return c.shulkerCanOpen(r, pos)
	}
	above, ok := r.Block(pos.Side(cube.FaceUp), 0)
	if !ok || c.s.chests.clear[above] {
		return true
	}
	for _, box := range c.s.boxesAt(r, pos.Side(cube.FaceUp), above) {
		if chestLidHits(box) {
			return false
		}
	}
	return true
}

// chestLidHits reports whether a box of the block above overlaps the open lid.
func chestLidHits(box registry.Box) bool {
	const far = 1 - chestLidInset
	// A box runs from its first three numbers (x, y, z) to its last three.
	return box[3] > chestLidInset && box[0] < far && box[5] > chestLidInset && box[2] < far &&
		box[4] > 0 && box[1] < chestLidHeight
}

// openChest opens the window as BDS's openContainer does: refused while one is
// open, the next window ID, ContainerOpen at the clicked block, then the contents.
func (p *Player) openChest(r *world.Region, c *Chest, pos cube.Pos) {
	if p.window.open {
		return
	}
	lead, _ := c.halves(r)
	o := p.chestOpening(r, c, pos)
	p.showWindow(r, &o)
	lead.addViewer(r, p)
	if c.furnace != nil {
		c.furnace.showAll(p)
	}
}

// leaveChest takes the player off its chest's viewers and closes the window. A chest
// that is no longer in this region is told on its own tick.
func (p *Player) leaveChest(r *world.Region, serverSide bool) {
	w := &p.window
	if w.inv == nil {
		return
	}
	pos, dim := w.pos, w.dim
	cart := w.cart
	p.closeWindow(r, serverSide)
	if cart != nil {
		if cart.chest != nil {
			if cr := cart.entity.Region(); cr != nil && (r == nil || cr == r) {
				cart.chest.removeViewer(cr, p)
			}
		}
		return
	}
	if r != nil {
		if c := p.s.chestAt(r, pos); c != nil {
			lead, _ := c.halves(r)
			lead.removeViewer(r, p)
			c.removeViewer(r, p)
			return
		}
		if _, loaded := r.Block(pos, 0); loaded {
			return
		}
	}
	if dim == nil {
		return
	}
	dim.RunAt(mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)}, func(o *world.Region) { //nolint:gosec // chunk coordinates fit int32
		if c := p.s.chestAt(o, pos); c != nil {
			lead, _ := c.halves(o)
			lead.removeViewer(o, p)
			c.removeViewer(o, p)
		}
	})
}

// tickChest closes the player's chest window when the chest is gone or too far, as
// BDS checks its open container each tick.
func (p *Player) tickChest() {
	if p.window.inv == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	// Checked first, since only then may the cart be read: BDS measures to the middle of the
	// block a cart is in (0xBC30280).
	gone := p.windowChest(r) == nil
	if !gone {
		at := p.window.pos
		if m := p.window.cart; m != nil {
			at = floorPos(m.entity.Position())
		}
		d := p.entity.Position().Sub(blockVec(at).Add(mgl32.Vec3{0.5, 0.5, 0.5}))
		gone = d.Dot(d) > chestViewRange*chestViewRange
	}
	if gone {
		p.leaveChest(r, true)
		p.flush()
	}
}

// loadChestWork copies the open container's items for a request to work on; the
// result stays nil with none open.
func (p *Player) loadChestWork() []ItemStack {
	st := &p.requests
	st.chest = st.chest[:0]
	if p.window.inv == nil {
		return nil
	}
	r := p.entity.Region()
	if r == nil || p.windowChest(r) == nil {
		return nil
	}
	inv := p.window.inv
	st.chest = append(st.chest, inv.parts[0]...)
	st.chest = append(st.chest, inv.parts[1]...)
	return st.chest
}

// commitChestWork writes back what a request did to the open container and shows the
// slots it changed to the others who have it open.
func (p *Player) commitChestWork() {
	work := p.inventory.open
	if len(work) == 0 {
		return
	}
	r := p.entity.Region()
	if r == nil || p.windowChest(r) == nil {
		return
	}
	inv := p.window.inv
	for i, s := range work {
		cur := inv.at(i)
		if cur == nil {
			break
		}
		if cur.netID == s.netID && cur.Count == s.Count {
			continue
		}
		if c := p.windowChest(r); c != nil && c.furnace != nil && i == furnaceResult && !cur.Empty() && (s.Empty() || cur.stacksWith(s) && s.Count < cur.Count) {
			c.resultTaken(r, p, *cur, int(cur.Count)-int(s.Count))
		}
		*cur = s
		inv.holder.inventoryChanged(i)
		for _, v := range inv.Viewers() {
			if v != p {
				inv.sendSlot(v, i, true)
			}
		}
	}
}

// ChestPairEvent is a placed chest about to pair with the one beside it. Cancel it to
// leave both single.
type ChestPairEvent struct {
	Cancellable
	Region *world.Region
	// Chest is the chest just placed, Other the one it would pair with.
	Chest, Other *Chest
}

// ChestSpillEvent is a chest's items about to be thrown out because its block went.
// Change Items to decide what comes out; cancelling means nothing does. Either way the
// chest's items are gone. An ender chest has none, so it never fires for one.
type ChestSpillEvent struct {
	Cancellable
	Region *world.Region
	Chest  *Chest
	Pos    cube.Pos
	Items  []ItemStack
}

// OnChestPair listens to placed chests about to pair.
func OnChestPair(s *Setup, fn func(e *ChestPairEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.chestPairEv.add(s.owner, p, fn)
	return nil
}

// OnChestSpill listens to chests about to throw out their items.
func OnChestSpill(s *Setup, fn func(e *ChestSpillEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.chestSpillEv.add(s.owner, p, fn)
	return nil
}

// ChestAt returns the chest, ender chest, shulker box, hopper, dispenser, dropper or
// crafter at pos, nil if none is there. Runs: owner
// of the chunk.
func (s *Host) ChestAt(r *world.Region, pos cube.Pos) *Chest { return s.chestAt(r, pos) }

// WithChest runs fn on the owner of the chest at pos in the dimension, loading its
// chunk; c is nil if no chest is there. Runs: anywhere.
func (s *Host) WithChest(d *world.Dimension, pos cube.Pos, fn func(r *world.Region, c *Chest)) {
	d.RunAt(mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)}, func(r *world.Region) { //nolint:gosec // chunk coordinates fit int32
		fn(r, s.chestAt(r, pos))
	})
}

// Overworld returns the overworld.
func (s *Host) Overworld() *world.Dimension { return s.overworld }

// Pos returns where the chest is.
func (c *Chest) Pos() cube.Pos { return c.pos }

// Ender reports whether this is an ender chest. Its items are each player's: see
// Player.EnderChest.
func (c *Chest) Ender() bool { return c.ender }

// Partner returns the other half's position, false if the chest is single.
func (c *Chest) Partner() (cube.Pos, bool) { return c.partner, c.paired }

// Lead reports whether this half holds the first slots of a double chest; a single
// chest is its own lead.
func (c *Chest) Lead() bool { return c.lead }

// Viewers returns the players with this chest open.
func (c *Chest) Viewers(r *world.Region) []*Player {
	lead, _ := c.halves(r)
	return slices.Clone(lead.viewers)
}

// CloseViewers closes the window of everyone with this chest open.
func (c *Chest) CloseViewers(r *world.Region) {
	lead, _ := c.halves(r)
	lead.closeViewers(r)
}

// Name returns the chest's custom name, empty if it has none.
func (c *Chest) Name() string {
	name, _ := c.saved["CustomName"].(string)
	return name
}

// SetName sets the custom name of the whole chest; empty removes it. Anyone with it
// open has the window opened again, because a client reads the name only on opening.
func (c *Chest) SetName(r *world.Region, name string) {
	lead, follower := c.halves(r)
	for _, half := range [...]*Chest{lead, follower} {
		if half == nil {
			continue
		}
		if half.saved == nil {
			half.saved = map[string]any{}
		}
		if name == "" {
			delete(half.saved, "CustomName")
		} else {
			half.saved["CustomName"] = name
		}
		half.changed = false
		r.SendBlockEntity(half.pos)
		r.SaveBlockEntity(half.pos)
	}
	for _, v := range lead.viewers {
		if v.entity != nil && v.entity.Region() == r {
			v.reopenChest(r, c)
		}
	}
}

// reopenChest shows the window again, the player staying among the chest's viewers
// so the lid stays up and no close event fires.
func (p *Player) reopenChest(r *world.Region, c *Chest) {
	p.sendNow(&packet.ContainerClose{WindowID: p.window.id, ContainerType: containerTypeNone, ServerSide: true})
	p.window.open = false
	p.openChest(r, c, p.window.pos)
}

// PairWith pairs this chest with other as its lead, closing their windows. BDS's rule
// must allow it: beside each other, same kind, same direction, other unpaired. False
// if it does not or this chest already has a partner.
func (c *Chest) PairWith(r *world.Region, other *Chest) bool {
	if c.paired || c == other || !c.canPair(r, other) {
		return false
	}
	c.closeViewers(r)
	other.closeViewers(r)
	c.link(other.pos, true)
	other.link(c.pos, false)
	return true
}

// Unpair splits a double chest into two single ones, closing their windows.
func (c *Chest) Unpair(r *world.Region) {
	if c.paired {
		c.unpair(r)
	}
}

// ComparatorSignal is what a comparator reads from the chest: how full it and its
// other half are. An ender chest's items belong to players, so it reads as empty.
func (c *Chest) ComparatorSignal(r *world.Region, _ cube.Pos) int32 {
	if c.machine != nil && c.machine.crafter {
		return c.crafterSignal()
	}
	var sum circuit.ContainerSum
	for _, it := range c.Inventory(r).Items() {
		maxStack := 0
		if t, ok := c.s.reg.Items.ByName(it.Name); ok {
			maxStack = int(t.MaxStack)
		}
		sum.Add(int(it.Count), maxStack)
	}
	return sum.Signal(c.Inventory(r).Size())
}
