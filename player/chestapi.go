package player

import (
	"slices"

	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// ChestOpenEvent is a player opening a chest or ender chest, before the window
// opens. Cancel it to keep the chest shut. Blocked says the block above stops the
// lid, as BDS checks; clear it to open the chest anyway, or set it to keep it shut.
type ChestOpenEvent struct {
	Cancellable
	Player  *Player
	Region  *world.Region
	Chest   *Chest
	Pos     cube.Pos
	Blocked bool
}

// ChestCloseEvent is a player leaving a chest's window, for whatever reason: closed
// by the player, moved too far, the chest broke or paired. The player may already
// have left this region, so use only its name and ID from here if so.
type ChestCloseEvent struct {
	Player *Player
	Region *world.Region
	Chest  *Chest
}

// ChestItemChange says what a player's request does to a chest slot.
type ChestItemChange uint8

// What a request does to a slot.
const (
	// ChestItemAdded puts an item in an empty slot or more on its stack.
	ChestItemAdded ChestItemChange = iota
	// ChestItemRemoved takes all or some of the slot's items.
	ChestItemRemoved
	// ChestItemReplaced swaps the slot's items for another kind.
	ChestItemReplaced
)

// ChestItemEvent is a player's request about to change one slot of a chest or ender
// chest, once for each slot it changes. The request has passed BDS's checks.
//
// Cancel refuses the whole request, not only this slot: the client puts everything
// back, so no item is lost or doubled. Changing New or Cursor is taken as it is, with
// no checks, and the player is shown the result. Taking an item moves it to the
// cursor or to an inventory slot, which is why Cursor is here. The stacks' Tag maps
// are shared: replace them rather than changing them in place.
type ChestItemEvent struct {
	Cancellable
	Player *Player
	Region *world.Region
	// Chest is the chest in the player's window. Chest.Ender tells an ender chest,
	// whose slots are the player's.
	Chest *Chest
	// Slot is the window's slot: on a double chest, 0 to 53, the lead's half first.
	Slot   int
	Change ChestItemChange
	// Old is what the slot holds, New what it will hold.
	Old, New ItemStack
	// Cursor is what the player's cursor will hold after the request.
	Cursor ItemStack
	// InventorySlots are the player's inventory slots the request changes: where a
	// shift-click puts what it takes. Read and change them with InventoryItem and
	// SetInventoryItem.
	InventorySlots []int
}

// InventoryItem returns what a slot of the player's inventory will hold after the
// request.
func (e *ChestItemEvent) InventoryItem(slot int) ItemStack {
	if slot < 0 || slot >= inventorySize {
		return ItemStack{}
	}
	return e.Player.requests.work.main[slot]
}

// SetInventoryItem changes what a slot of the player's inventory will hold after the
// request. It is taken as it is, with no checks. False if there is no such slot.
func (e *ChestItemEvent) SetInventoryItem(slot int, s ItemStack) bool {
	if slot < 0 || slot >= inventorySize {
		return false
	}
	st := &e.Player.requests
	st.work.main[slot] = e.Player.s.freshStack(s)
	st.refresh = true
	return true
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

// OnChestOpen listens to players opening chests and ender chests.
func OnChestOpen(s *Setup, fn func(e *ChestOpenEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.chestOpenEv.add(s.owner, p, fn)
	return nil
}

// OnChestClose listens to players leaving chest windows.
func OnChestClose(s *Setup, fn func(e *ChestCloseEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.chestCloseEv.add(s.owner, p, fn)
	return nil
}

// OnChestItem listens to players' requests before they change a chest's slots.
func OnChestItem(s *Setup, fn func(e *ChestItemEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.chestItemEv.add(s.owner, p, fn)
	return nil
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

// sameStack reports whether two stacks are the same items, whatever their IDs.
func sameStack(a, b ItemStack) bool {
	if a.Empty() || b.Empty() {
		return a.Empty() && b.Empty()
	}
	return a.Count == b.Count && a.stacksWith(b)
}

// ChestAt returns the chest or ender chest at pos, nil if none is there. Runs: owner
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
// Player.EnderItem.
func (c *Chest) Ender() bool { return c.ender }

// Partner returns the other half's position, false if the chest is single.
func (c *Chest) Partner() (cube.Pos, bool) { return c.partner, c.paired }

// Lead reports whether this half holds the first slots of a double chest; a single
// chest is its own lead.
func (c *Chest) Lead() bool { return c.lead }

// Size returns the chest's slots: 27, 54 when double, 0 for an ender chest. All the
// slot methods below work on the whole chest from either half, the lead's slots
// first. Runs: owner of the chunk, as for all the methods below.
func (c *Chest) Size(r *world.Region) int {
	if c.ender {
		return 0
	}
	if _, follower := c.halves(r); follower != nil {
		return 2 * chestSize
	}
	return c.halfSize()
}

// slotAt returns the half holding a slot of the whole chest and the slot in it.
func (c *Chest) slotAt(r *world.Region, slot int) (*Chest, int) {
	if c.ender || slot < 0 {
		return nil, 0
	}
	lead, follower := c.halves(r)
	switch {
	case slot < c.halfSize():
		return lead, slot
	case slot < 2*chestSize && follower != nil:
		return follower, slot - chestSize
	}
	return nil, 0
}

// Item returns what a slot holds.
func (c *Chest) Item(r *world.Region, slot int) ItemStack {
	half, at := c.slotAt(r, slot)
	if half == nil {
		return ItemStack{}
	}
	return half.items[at]
}

// Items returns a copy of every slot.
func (c *Chest) Items(r *world.Region) []ItemStack {
	if c.ender {
		return nil
	}
	lead, follower := c.halves(r)
	items := slices.Clone(lead.items[:c.halfSize()])
	if follower != nil {
		items = append(items, follower.items[:]...)
	}
	return items
}

// SetItem puts s in a slot, shows it to whoever has the chest open and saves it. It
// does not fire ChestItemEvent, which is about players. False if there is no such slot.
func (c *Chest) SetItem(r *world.Region, slot int, s ItemStack) bool {
	half, at := c.slotAt(r, slot)
	if half == nil {
		return false
	}
	s = c.s.freshStack(s)
	half.items[at] = s
	if half.cart == nil {
		r.SaveBlockEntity(half.pos)
	}
	lead, _ := c.halves(r)
	for _, v := range lead.viewers {
		v.sendNow(&packet.InventorySlot{WindowID: uint32(v.window.id), Slot: uint32(slot), NewItem: s.network(c.s.reg)}) //nolint:gosec // slots are small
	}
	return true
}

// freshStack gives a stack a new network ID, or clears it if it is empty.
func (s *Host) freshStack(it ItemStack) ItemStack {
	if it.Empty() {
		return ItemStack{}
	}
	it.netID = s.nextStackID()
	return it
}

// AddItem puts s in the chest, on matching stacks first and then in empty slots, and
// returns what did not fit.
func (c *Chest) AddItem(r *world.Region, s ItemStack) ItemStack {
	size := c.Size(r)
	limit := s.maxStack(c.s.reg)
	for i := 0; i < size && !s.Empty(); i++ {
		cur := c.Item(r, i)
		if cur.Empty() || !cur.stacksWith(s) || cur.Count >= limit {
			continue
		}
		n := min(limit-cur.Count, s.Count)
		cur.Count += n
		s.Count -= n
		c.SetItem(r, i, cur)
	}
	for i := 0; i < size && !s.Empty(); i++ {
		if !c.Item(r, i).Empty() {
			continue
		}
		put := s
		put.Count = min(limit, s.Count)
		s.Count -= put.Count
		c.SetItem(r, i, put)
	}
	if s.Count == 0 {
		return ItemStack{}
	}
	return s
}

// RemoveItem takes up to count of the named item out and returns how many it took.
func (c *Chest) RemoveItem(r *world.Region, name string, count int) int {
	taken := 0
	for i, size := 0, c.Size(r); i < size && taken < count; i++ {
		cur := c.Item(r, i)
		if cur.Empty() || cur.Name != name {
			continue
		}
		n := min(int(cur.Count), count-taken)
		cur.Count -= uint8(n) //nolint:gosec // at most the stack's count
		taken += n
		c.SetItem(r, i, cur)
	}
	return taken
}

// Clear empties every slot.
func (c *Chest) Clear(r *world.Region) {
	for i, size := 0, c.Size(r); i < size; i++ {
		c.SetItem(r, i, ItemStack{})
	}
}

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
	p.openChest(r, c, p.window.chestPos)
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

// OpenChest opens a chest's window for the player whatever the lid or the player's
// abilities say, and fires no ChestOpenEvent. False if a window is already open. Runs:
// the player's region, which must own the chest.
func (p *Player) OpenChest(r *world.Region, c *Chest) bool {
	if p.window.open {
		return false
	}
	p.openChest(r, c, c.pos)
	return true
}

// CloseChest closes the player's chest window if one is open. Runs: the player's
// region.
func (p *Player) CloseChest() {
	if p.entity == nil || !p.window.chest {
		return
	}
	p.leaveChest(p.entity.Region(), true)
	p.window.open = false
	p.flush()
}

// EnderItem returns a slot of the player's ender chest. Runs: the player's region, as
// for the methods below.
func (p *Player) EnderItem(slot int) ItemStack {
	if slot < 0 || slot >= chestSize {
		return ItemStack{}
	}
	return p.inventory.ender[slot]
}

// EnderItems returns a copy of the player's ender chest.
func (p *Player) EnderItems() []ItemStack { return slices.Clone(p.inventory.ender[:]) }

// SetEnderItem sets a slot of the player's ender chest, shown at once if it is open.
// False if there is no such slot.
func (p *Player) SetEnderItem(slot int, s ItemStack) bool {
	if slot < 0 || slot >= chestSize {
		return false
	}
	s = p.s.freshStack(s)
	p.inventory.ender[slot] = s
	if p.window.chest && p.entity != nil {
		if r := p.entity.Region(); r != nil {
			if c := p.s.chestAt(r, p.window.chestPos); c != nil && c.ender {
				p.sendNow(&packet.InventorySlot{WindowID: uint32(p.window.id), Slot: uint32(slot), NewItem: s.network(p.s.reg)}) //nolint:gosec // slots are small
			}
		}
	}
	return true
}

// chestContent is what a player's window on c shows.
func (p *Player) chestContent(r *world.Region, c *Chest) []protocol.ItemInstance {
	shown := p.inventory.ender[:]
	if !c.ender {
		shown = c.Items(r)
	}
	content := make([]protocol.ItemInstance, 0, len(shown))
	for _, s := range shown {
		content = append(content, s.network(p.s.reg))
	}
	return content
}

// fireChestItems runs ChestItemEvent for each chest slot the running request changes,
// false if a listener refused the request. A listener's changes go in the request's
// copy, and the player is sent the result once the request is answered.
func (p *Player) fireChestItems() bool {
	st := &p.requests
	work := st.work.open
	if len(work) == 0 {
		return true
	}
	g := p.s.game
	if !g.chestItemEv.Has() {
		return true
	}
	r := p.entity.Region()
	if r == nil {
		return true
	}
	c := p.s.chestAt(r, p.window.chestPos)
	if c == nil {
		return true
	}
	var changed []int
	for i := range st.work.main {
		if s, o := &st.work.main[i], &p.inventory.main[i]; s.netID != o.netID || s.Count != o.Count {
			changed = append(changed, i)
		}
	}
	for i := range work {
		var old ItemStack
		if c.ender {
			old = p.inventory.ender[i]
		} else {
			old = c.Item(r, i)
		}
		if old.netID == work[i].netID && old.Count == work[i].Count {
			continue
		}
		e := ChestItemEvent{
			Player: p, Region: r, Chest: c, Slot: i, Change: chestChange(old, work[i]),
			Old: old, New: work[i], Cursor: st.work.ui[cursorSlot], InventorySlots: changed,
		}
		g.chestItemEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
		if !sameStack(e.New, work[i]) {
			work[i] = p.s.freshStack(e.New)
			st.refresh = true
		}
		if !sameStack(e.Cursor, st.work.ui[cursorSlot]) {
			st.work.ui[cursorSlot] = p.s.freshStack(e.Cursor)
			st.refresh = true
		}
	}
	return true
}

// chestChange says what going from old to next does to a slot.
func chestChange(old, next ItemStack) ChestItemChange {
	switch {
	case next.Empty(), old.stacksWith(next) && next.Count < old.Count:
		return ChestItemRemoved
	case old.Empty(), old.stacksWith(next):
		return ChestItemAdded
	}
	return ChestItemReplaced
}

// refreshItems sends the player its inventory and open chest as the server has them,
// after a listener changed what a request did.
func (p *Player) refreshItems() {
	for _, pk := range p.inventory.contentPackets(p.s.reg) {
		p.send(pk)
	}
	if !p.window.chest || p.entity == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	if c := p.windowChest(r); c != nil {
		p.send(&packet.InventoryContent{WindowID: uint32(p.window.id), Content: p.chestContent(r, c)})
	}
}

// ComparatorSignal is what a comparator reads from the chest: how full it and its
// other half are. An ender chest's items belong to players, so it reads as empty.
func (c *Chest) ComparatorSignal(r *world.Region, _ cube.Pos) int32 {
	if c.machine != nil && c.machine.crafter {
		return c.crafterSignal()
	}
	var sum circuit.ContainerSum
	for _, it := range c.Items(r) {
		maxStack := 0
		if t, ok := c.s.reg.Items.ByName(it.Name); ok {
			maxStack = int(t.MaxStack)
		}
		sum.Add(int(it.Count), maxStack)
	}
	return sum.Signal(c.Size(r))
}
