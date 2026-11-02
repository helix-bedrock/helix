package player

import (
	"maps"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
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
)

// Cardinal directions in a chest's state, numbered as BDS numbers them.
const (
	cardinalSouth int8 = iota
	cardinalWest
	cardinalNorth
	cardinalEast
)

// cardinalState is the block state a chest faces by.
const cardinalState = "minecraft:cardinal_direction"

// chestSides are the two neighbours a chest placed facing each direction tries to
// pair with, in BDS's order.
var chestSides = [...][2]cube.Face{
	cardinalSouth: {cube.FaceEast, cube.FaceWest},
	cardinalWest:  {cube.FaceNorth, cube.FaceSouth},
	cardinalNorth: {cube.FaceEast, cube.FaceWest},
	cardinalEast:  {cube.FaceNorth, cube.FaceSouth},
}

// chestTable holds the chest facts of every block state. It does not change after
// it is built, so any region may read it.
type chestTable struct {
	kind []int8
	// dir is the direction a chest state faces.
	dir []int8
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
	t := &chestTable{kind: make([]int8, n), dir: make([]int8, n), clear: make([]bool, n)}
	for id := range n {
		state := uint32(id) //nolint:gosec // state IDs fit uint32
		name, states := blocks.Name(state), blocks.States(state)
		t.kind[id] = chestKindOf(name)
		if dir, ok := states[cardinalState].(string); ok {
			t.dir[id] = cardinalOf(dir)
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
	}
	return chestNone
}

// cardinalOf numbers a direction.
func cardinalOf(dir string) int8 {
	switch dir {
	case "west":
		return cardinalWest
	case "north":
		return cardinalNorth
	case "east":
		return cardinalEast
	}
	return cardinalSouth
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
	items [chestSize]ItemStack
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
}

// chestEntities is the world's list of block entities the engine runs.
type chestEntities struct {
	s *Host
}

// New returns a new chest for a chest block.
func (e chestEntities) New(block uint32) world.BlockEntity {
	t := e.s.chests
	if int(block) >= len(t.kind) || t.kind[block] == chestNone {
		return nil
	}
	return &Chest{s: e.s, lead: true, ender: t.kind[block] == chestEnder}
}

// Load brings back a chest as BDS loads one. A follower waits for its lead's tick
// to link them; only the lead reads the partner's position.
func (e chestEntities) Load(pos cube.Pos, saved map[string]any) (world.BlockEntity, bool) {
	id, _ := saved["id"].(string)
	if id != chestEntityID && id != enderChestEntityID {
		return nil, false
	}
	c := &Chest{s: e.s, pos: pos, lead: true, ender: id == enderChestEntityID, saved: maps.Clone(saved)}
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
	if c.ender {
		return
	}
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	for _, side := range chestSides[c.s.chests.dir[block]] {
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
	alongX := t.dir[a]&^2 == 0
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
	c.unpair(r)
	c.closeViewers(r)
	for i := range c.items {
		c.throwSlot(r, i)
	}
}

// throwSlot throws a slot's items out in piles, as BDS's container drop does.
func (c *Chest) throwSlot(r *world.Region, slot int) {
	s := c.items[slot]
	c.items[slot] = ItemStack{}
	for !s.Empty() {
		pile := s
		pile.Count = min(uint8(rand.IntN(chestPileExtra))+chestPileMin, s.Count) //nolint:gosec // an item's scatter, not a secret
		s.Count -= pile.Count
		at := blockVec(c.pos).Add(mgl32.Vec3{chestThrowOffset(), chestThrowOffset(), chestThrowOffset()})
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
	c.tickPair(r)
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
		c.lid = min(was+chestLidSpeed, 1)
	case !c.open && was > 0:
		c.lid = was - chestLidSpeed
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
	if c.ender {
		return packet.SoundEventEnderChestOpen
	}
	return packet.SoundEventChestOpen
}

// closeSound is the sound of the lid closing.
func (c *Chest) closeSound() string {
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
	r.Broadcast(c.pos, &packet.LevelSoundEvent{
		SoundType: sound, Position: at.Add(mgl32.Vec3{0.5, 0.5, 0.5}),
		ExtraData: int32(c.s.reg.Blocks.Hash(block)), //nolint:gosec // hashes are sent as int32
	}, nil)
}

// blockEvent tells the players near how the lid stands.
func (c *Chest) blockEvent(r *world.Region) {
	r.Broadcast(c.pos, &packet.BlockEvent{
		Position:  protocol.BlockPos{int32(c.pos[0]), int32(c.pos[1]), int32(c.pos[2])}, //nolint:gosec // block coordinates fit int32
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
			v.closeWindow(true)
		}
	}
}

// useChest is what clicking a chest does: it is taken whether or not the chest
// opens. BDS always has a block entity here; a chest placed before Helix had them
// gets one now, so old test worlds still work.
func (p *Player) useChest(r *world.Region, u *BlockUse) bool {
	c := p.s.chestAt(r, u.Pos)
	if c == nil {
		c = &Chest{s: p.s, pos: u.Pos, lead: true, ender: p.s.chests.kind[u.Block] == chestEnder}
		if !r.SetBlockEntity(u.Pos, c) {
			return false
		}
	}
	if !p.abilities.Flag(AbilityOpenContainers) {
		return true
	}
	if !c.canOpen(r, u.Pos) {
		return true
	}
	if o := c.mate(r); o != nil && !o.canOpen(r, o.pos) {
		return true
	}
	p.openChest(r, c, u.Pos)
	return true
}

// canOpen is BDS's check of the block above: listed blocks never stop the lid, any
// other does if a collision box of it reaches the lid. Cats come with mobs.
func (c *Chest) canOpen(r *world.Region, pos cube.Pos) bool {
	above, ok := r.Block(pos.Side(cube.FaceUp), 0)
	if !ok || c.s.chests.clear[above] {
		return true
	}
	for _, box := range c.s.reg.Blocks.Properties(above).Collision {
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
	w := &p.window
	if w.open {
		return
	}
	lead, follower := c.halves(r)
	w.nextID()
	w.open, w.chest, w.chestPos = true, true, pos
	p.send(&packet.ContainerOpen{
		WindowID: w.id, ContainerType: byte(protocol.ContainerTypeContainer),
		ContainerPosition:       protocol.BlockPos{int32(pos[0]), int32(pos[1]), int32(pos[2])}, //nolint:gosec // block coordinates fit int32
		ContainerEntityUniqueID: -1,
	})
	content := make([]protocol.ItemInstance, 0, 2*chestSize)
	shown := lead.items[:]
	if c.ender {
		shown = p.inventory.ender[:]
	}
	for _, s := range shown {
		content = append(content, s.network(p.s.reg))
	}
	if follower != nil {
		for _, s := range follower.items {
			content = append(content, s.network(p.s.reg))
		}
	}
	p.send(&packet.InventoryContent{WindowID: uint32(w.id), Content: content})
	p.flush()
	lead.addViewer(r, p)
}

// closeWindow forgets the player's chest window. serverSide tells the client,
// which otherwise closed it itself.
func (p *Player) closeWindow(serverSide bool) {
	w := &p.window
	w.open, w.chest = false, false
	p.inventory.open = nil
	if serverSide {
		p.sendNow(&packet.ContainerClose{WindowID: w.id, ContainerType: containerTypeNone, ServerSide: true})
	}
}

// leaveChest takes the player off its chest's viewers and closes the window. A chest
// that is no longer in this region is told on its own tick.
func (p *Player) leaveChest(r *world.Region, serverSide bool) {
	w := &p.window
	if !w.chest {
		return
	}
	pos := w.chestPos
	p.closeWindow(serverSide)
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
	p.s.overworld.RunAt(mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)}, func(o *world.Region) { //nolint:gosec // chunk coordinates fit int32
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
	if !p.window.chest {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	pos := p.window.chestPos
	d := p.entity.Position().Sub(blockVec(pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}))
	if p.s.chestAt(r, pos) == nil || d.Dot(d) > chestViewRange*chestViewRange {
		p.leaveChest(r, true)
		p.flush()
	}
}

// loadChestWork copies the open chest's items for a request to work on, lead's
// half first; the result stays nil with no chest open.
func (p *Player) loadChestWork() []ItemStack {
	st := &p.requests
	st.chest = st.chest[:0]
	if !p.window.chest {
		return nil
	}
	r := p.entity.Region()
	if r == nil {
		return nil
	}
	c := p.s.chestAt(r, p.window.chestPos)
	if c == nil {
		return nil
	}
	if c.ender {
		st.chest = append(st.chest, p.inventory.ender[:]...)
		return st.chest
	}
	lead, follower := c.halves(r)
	st.chest = append(st.chest, lead.items[:]...)
	if follower != nil {
		st.chest = append(st.chest, follower.items[:]...)
	}
	return st.chest
}

// commitChestWork writes back what a request did to the open chest and shows the
// slots it changed to the others who have it open.
func (p *Player) commitChestWork() {
	work := p.inventory.open
	if len(work) == 0 {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	c := p.s.chestAt(r, p.window.chestPos)
	if c == nil {
		return
	}
	// An ender chest's items are this player's alone, saved with the player.
	if c.ender {
		copy(p.inventory.ender[:], work)
		return
	}
	lead, follower := c.halves(r)
	for i, s := range work {
		half, at := lead, i
		if i >= chestSize {
			half, at = follower, i-chestSize
		}
		if half == nil {
			break
		}
		cur := &half.items[at]
		if cur.netID == s.netID && cur.Count == s.Count {
			continue
		}
		*cur = s
		r.SaveBlockEntity(half.pos)
		for _, v := range lead.viewers {
			if v != p {
				v.sendNow(&packet.InventorySlot{WindowID: uint32(v.window.id), Slot: uint32(i), NewItem: s.network(p.s.reg)}) //nolint:gosec // slots are small
			}
		}
	}
}
