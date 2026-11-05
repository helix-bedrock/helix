package player

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Container window rules from BDS 1.26.60.
const (
	// maxWindowID is where BDS's window ID counter wraps back to 1.
	maxWindowID = 100
	// containerTypeNone is the type BDS puts on the ContainerClose it
	// answers with (-9).
	containerTypeNone byte = 0xF7
	// containerTypeInventory is the player's own inventory (-1).
	containerTypeInventory byte = 0xFF
)

// windowState is the player's container window: BDS's per-player window ID
// counter and what is open. Owned by the player's region.
type windowState struct {
	id   byte
	open bool
	kind ContainerKind
	// inv is the open container's slots: a block's, a cart's or the player's ender
	// chest. Nil for the player's own inventory and for screens.
	inv *Inventory
	// pos is the block the window is at; dim its dimension.
	pos cube.Pos
	dim *world.Dimension
	// cart is the minecart whose slots are open, if a cart's are.
	cart *Minecart
}

// nextID moves to the next window ID.
func (w *windowState) nextID() {
	w.id++
	if w.id >= maxWindowID {
		w.id = 1
	}
}

// ContainerOpenEvent is a player about to open a container's window: its own inventory,
// a chest, ender chest, shulker box, hopper, dispenser, dropper or crafter, a cart, or a
// screen such as a crafting table's or an anvil's. Cancel it to keep the window shut: no
// window, no lid, no sound. Blocked says something stops a lid, as BDS checks; clear it
// to open anyway, or set it to keep it shut. Runs: the player's region.
type ContainerOpenEvent struct {
	Cancellable
	Player *Player
	Region *world.Region
	Kind   ContainerKind
	// Pos is the block the window is at; for a cart or the player's own inventory, the
	// block it is in.
	Pos cube.Pos
	// Inventory is the slots the window shows, nil for a screen. For the player's own
	// inventory it is that inventory.
	Inventory *Inventory
	// Chest is the block holding the slots, nil if none does. Cart is the cart's.
	Chest   *Chest
	Cart    *Minecart
	Blocked bool
}

// ContainerCloseEvent is a player's window closing, for whatever reason: closed by the
// player, moved too far, the container gone or paired. Region is nil when the player is
// already in no region; Inventory may be nil, as in ContainerOpenEvent.
type ContainerCloseEvent struct {
	Player    *Player
	Region    *world.Region
	Kind      ContainerKind
	Pos       cube.Pos
	Inventory *Inventory
}

// OnContainerOpen listens to players opening containers.
func OnContainerOpen(s *Setup, fn func(e *ContainerOpenEvent), p Priority) error {
	return addListener(s, &s.h.game.containerOpenEv, fn, p)
}

// OnContainerClose listens to players' windows closing.
func OnContainerClose(s *Setup, fn func(e *ContainerCloseEvent), p Priority) error {
	return addListener(s, &s.h.game.containerCloseEv, fn, p)
}

// windowOpening is a window about to open.
type windowOpening struct {
	kind ContainerKind
	// window is the screen the client is told to open.
	window byte
	pos    cube.Pos
	// entity is the cart's unique ID, -1 for none.
	entity  int64
	inv     *Inventory
	chest   *Chest
	cart    *Minecart
	blocked bool
}

// mayOpen runs ContainerOpenEvent on a window about to open; false keeps it shut.
func (p *Player) mayOpen(r *world.Region, o *windowOpening) bool {
	if g := p.s.game; g.containerOpenEv.Has() {
		e := ContainerOpenEvent{Player: p, Region: r, Kind: o.kind, Pos: o.pos, Inventory: o.inv, Chest: o.chest, Cart: o.cart, Blocked: o.blocked}
		g.containerOpenEv.fire(p.s.log, &e)
		o.blocked = e.Blocked || e.Cancelled()
	}
	return !o.blocked
}

// showWindow opens the window as BDS's openContainer does: the next window ID,
// ContainerOpen, then the slots. It fires no event.
func (p *Player) showWindow(r *world.Region, o *windowOpening) {
	w := &p.window
	w.nextID()
	w.open, w.kind, w.inv, w.pos, w.cart = true, o.kind, o.inv, o.pos, o.cart
	if r != nil {
		w.dim = r.Dimension()
	}
	p.send(&packet.ContainerOpen{
		WindowID: w.id, ContainerType: o.window,
		ContainerPosition: blockPos(o.pos), ContainerEntityUniqueID: o.entity,
	})
	if o.inv != nil {
		o.inv.sendAll(p, false)
	}
	p.flush()
}

// closeWindow forgets the player's window and fires ContainerCloseEvent. serverSide
// tells the client, which otherwise closed it itself.
func (p *Player) closeWindow(r *world.Region, serverSide bool) {
	w := &p.window
	was := *w
	w.open, w.kind, w.inv, w.cart = false, ContainerNone, nil, nil
	p.inventory.open = nil
	if serverSide {
		p.sendNow(&packet.ContainerClose{WindowID: w.id, ContainerType: containerTypeNone, ServerSide: true})
	}
	if g := p.s.game; was.open && g.containerCloseEv.Has() {
		e := ContainerCloseEvent{Player: p, Region: r, Kind: was.kind, Pos: was.pos, Inventory: was.inv}
		g.containerCloseEv.fire(p.s.log, &e)
	}
}

// Window returns what the player has open and where, false with nothing open. inv is
// nil for screens. Runs: the player's region.
func (p *Player) Window() (kind ContainerKind, pos cube.Pos, inv *Inventory, ok bool) {
	w := &p.window
	return w.kind, w.pos, w.inv, w.open
}

// CloseWindow closes whatever window the player has open. Runs: the player's region.
func (p *Player) CloseWindow() {
	if p.entity == nil || !p.window.open {
		return
	}
	p.leaveWindow(p.entity.Region(), true)
	p.flush()
}

// leaveWindow closes the window, taking the player off a container's viewers.
func (p *Player) leaveWindow(r *world.Region, serverSide bool) {
	if p.window.inv != nil {
		p.leaveChest(r, serverSide)
		return
	}
	p.closeWindow(r, serverSide)
}

// handleInteract is vanilla's Interact handler. Only opening the inventory
// is built: BDS opens it when the target is within 12 blocks and no
// container is open.
func handleInteract(p *Player, pk *packet.Interact) {
	if pk.ActionType == packet.InteractActionLeaveVehicle && p.vehicle != nil {
		letOff(p.vehicle, p)
		return
	}
	if pk.ActionType != packet.InteractActionOpenInventory || p.entity == nil {
		return
	}
	if p.window.open {
		return
	}
	if pk.TargetEntityRuntimeID == p.runtimeID {
		p.openInventory(p.entity.Region())
		return
	}
	// The client asks this way to open a chest or hopper minecart.
	if m := p.cartInReach(pk.TargetEntityRuntimeID); m != nil {
		p.openCart(m)
	}
}

// openInventory opens the player's own inventory as BDS does: the next
// window ID, the position floored, no entity.
func (p *Player) openInventory(r *world.Region) {
	o := windowOpening{kind: ContainerInventory, window: containerTypeInventory, pos: floorPos(p.entity.Position()), entity: -1, inv: p.Inventory()}
	if !p.mayOpen(r, &o) {
		return
	}
	// The player's own slots are always shown, in their own windows.
	o.inv = nil
	p.showWindow(r, &o)
}

// handleContainerClose is vanilla's ContainerClose handler: BDS closes
// whatever is open, whatever window the client names, and answers with
// its own window ID and no type.
func handleContainerClose(p *Player, _ *packet.ContainerClose) {
	var r *world.Region
	if p.entity != nil {
		r = p.entity.Region()
	}
	p.leaveWindow(r, false)
	p.send(&packet.ContainerClose{WindowID: p.window.id, ContainerType: containerTypeNone})
	p.flush()
}

// ItemChange says what a player's request does to a container slot.
type ItemChange uint8

// What a request does to a slot.
const (
	// ItemAdded puts an item in an empty slot or more on its stack.
	ItemAdded ItemChange = iota
	// ItemRemoved takes all or some of the slot's items.
	ItemRemoved
	// ItemReplaced swaps the slot's items for another kind.
	ItemReplaced
)

// ContainerItemEvent is a player's request about to change one slot of the container
// in its window (a chest, ender chest, shulker box, hopper, dispenser, dropper, crafter
// or cart), once for each slot it changes. The request has passed BDS's checks.
//
// Cancel refuses the whole request, not only this slot: the client puts everything
// back, so no item is lost or doubled. Changing New or Cursor is taken as it is, with
// no checks, and the player is shown the result. Taking an item moves it to the
// cursor or to an inventory slot, which is why Cursor is here. The stacks' Tag maps
// are shared: replace them rather than changing them in place.
type ContainerItemEvent struct {
	Cancellable
	Player *Player
	Region *world.Region
	Kind   ContainerKind
	// Inventory is the container's slots: for an ender chest, the player's own.
	Inventory *Inventory
	// Chest is the block holding them, Cart the cart; one of them is nil.
	Chest *Chest
	Cart  *Minecart
	// Slot is the window's slot: on a double chest, 0 to 53, the lead's half first.
	Slot   int
	Change ItemChange
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
func (e *ContainerItemEvent) InventoryItem(slot int) ItemStack {
	if slot < 0 || slot >= inventorySize {
		return ItemStack{}
	}
	return e.Player.requests.work.main[slot]
}

// SetInventoryItem changes what a slot of the player's inventory will hold after the
// request. It is taken as it is, with no checks. False if there is no such slot.
func (e *ContainerItemEvent) SetInventoryItem(slot int, s ItemStack) bool {
	if slot < 0 || slot >= inventorySize {
		return false
	}
	st := &e.Player.requests
	st.work.main[slot] = e.Player.s.freshStack(s)
	st.refresh = true
	return true
}

// OnContainerItem listens to players' requests before they change a container's slots.
func OnContainerItem(s *Setup, fn func(e *ContainerItemEvent), p Priority) error {
	return addListener(s, &s.h.game.containerItemEv, fn, p)
}

// OpenContainer opens the window of a chest, ender chest, shulker box, hopper,
// dispenser, dropper or crafter for the player, whatever the lid or the player's
// abilities say, and fires no ContainerOpenEvent. False if a window is already open.
// Runs: the player's region, which must own the container.
func (p *Player) OpenContainer(r *world.Region, c *Chest) bool {
	if p.window.open || c == nil {
		return false
	}
	p.openChest(r, c, c.pos)
	return true
}

// fireChestItems runs ContainerItemEvent for each chest slot the running request changes,
// false if a listener refused the request. A listener's changes go in the request's
// copy, and the player is sent the result once the request is answered.
func (p *Player) fireChestItems() bool {
	st := &p.requests
	work := st.work.open
	if len(work) == 0 {
		return true
	}
	g := p.s.game
	if !g.containerItemEv.Has() {
		return true
	}
	r := p.entity.Region()
	if r == nil {
		return true
	}
	c := p.windowChest(r)
	inv := p.window.inv
	if c == nil || inv == nil {
		return true
	}
	var changed []int
	for i := range st.work.main {
		if s, o := &st.work.main[i], &p.inventory.main[i]; s.netID != o.netID || s.Count != o.Count {
			changed = append(changed, i)
		}
	}
	for i := range work {
		old := inv.Item(i)
		if old.netID == work[i].netID && old.Count == work[i].Count {
			continue
		}
		e := ContainerItemEvent{
			Player: p, Region: r, Kind: inv.kind, Inventory: inv, Slot: i, Change: chestChange(old, work[i]),
			Old: old, New: work[i], Cursor: st.work.ui[cursorSlot], InventorySlots: changed,
		}
		if c.cart != nil {
			e.Cart = c.cart
		} else {
			e.Chest = c
		}
		g.containerItemEv.fire(p.s.log, &e)
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
func chestChange(old, next ItemStack) ItemChange {
	switch {
	case next.Empty(), old.stacksWith(next) && next.Count < old.Count:
		return ItemRemoved
	case old.Empty(), old.stacksWith(next):
		return ItemAdded
	}
	return ItemReplaced
}

// refreshItems sends the player its own slots and the open window's as the server has
// them, through the views: after a listener changed what a request did, or on request.
func (p *Player) refreshItems() {
	p.sendOwnItems()
	if p.window.inv == nil || p.entity == nil {
		return
	}
	if r := p.entity.Region(); r != nil && p.windowChest(r) != nil {
		p.window.inv.sendAll(p, false)
	}
}
