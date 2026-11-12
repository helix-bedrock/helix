package player

import (
	"slices"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// ContainerKind is what a container or window is: a player's own slots, a block's or a
// cart's, or a block's screen with no slots of its own.
type ContainerKind uint8

// The kinds of container.
const (
	ContainerNone ContainerKind = iota
	// A player's own: inventory (the hotbar is its first nine slots), armour, offhand,
	// the UI slots (cursor, crafting grid, screens' inputs) and ender chest.
	ContainerInventory
	ContainerArmour
	ContainerOffhand
	ContainerUI
	ContainerEnderChest
	// Blocks with slots. ContainerChest is also a trapped or copper chest.
	ContainerChest
	ContainerShulkerBox
	ContainerHopper
	ContainerDispenser
	ContainerDropper
	ContainerCrafter
	// ContainerFurnace, ContainerBlastFurnace and ContainerSmoker have three slots: the
	// input, the fuel and the result.
	ContainerFurnace
	ContainerBlastFurnace
	ContainerSmoker
	// Carts with slots.
	ContainerChestMinecart
	ContainerHopperMinecart
	// Screens of blocks with no slots of their own; what is put in them is held in the
	// player's UI slots.
	ContainerCraftingTable
	ContainerSmithingTable
	ContainerAnvil
	ContainerGrindstone
	ContainerEnchantingTable
	ContainerLoom
	// ContainerChestBoat is a chest boat's slots.
	ContainerChestBoat
	// ContainerBrewingStand has five slots: the ingredient, three bottles and the fuel.
	ContainerBrewingStand
)

// own reports whether the kind is a player's own slots, shown in fixed windows.
func (k ContainerKind) own() bool { return k >= ContainerInventory && k <= ContainerUI }

// Inventory is a set of item slots and everything that shows them: a player's inventory,
// armour, offhand, UI slots or ender chest, a chest (a double chest is one Inventory of 54
// slots, the lead's first), a shulker box, a hopper, dispenser, dropper or crafter, or a
// cart. Changing a slot shows it at once to every player looking at it, through the
// [InventoryViewEvent] listeners and the inventory's own view. Methods on a nil Inventory
// do nothing and hold nothing. Runs: owner of whatever holds it (the player's region, or
// the chunk's or cart's).
type Inventory struct {
	s    *Host
	kind ContainerKind
	// parts are the slots, held by their owner: two for a double chest.
	parts [2][]ItemStack
	// holder says who looks at the slots and is told when they change.
	holder inventoryHolder
	// owner and window are a player's own slots and the window they are always shown in.
	owner  *Player
	window uint32
	view   func(e *InventoryViewEvent)
}

// inventoryHolder is what holds an inventory's slots.
type inventoryHolder interface {
	// inventoryViewers are the players who have the slots in a window.
	inventoryViewers() []*Player
	// inventoryChanged is told a slot changed, to save it.
	inventoryChanged(slot int)
}

// InventoryViewEvent is items about to be shown to one player, before they are sent: one
// slot when a slot changes, many when a window opens or is refreshed. Items are copies
// made for this player only: change them freely, for example to add lore with a price
// for this player. What the player sees is never read back or kept, so the server's
// items stay as they are and are what moves, drops and saves. Change only what the
// player sees, such as lore and names: a different item or count misleads the client's
// guesses until the next refresh. Replace a stack's Tag rather than changing it in place
// ([ItemStack.WithLore] does). Runs: owner of the player's region.
type InventoryViewEvent struct {
	Player    *Player
	Inventory *Inventory
	// Slots are the slots sent and Items what the player will see in them, in order.
	Slots []int
	Items []ItemStack
}

// OnInventoryView listens to items being shown to players, every inventory's.
func OnInventoryView(s *Setup, fn func(e *InventoryViewEvent), p Priority) error {
	return addListener(s, &s.h.game.inventoryViewEv, fn, p)
}

// Kind returns what the inventory belongs to.
func (inv *Inventory) Kind() ContainerKind {
	if inv == nil {
		return ContainerNone
	}
	return inv.kind
}

// Size returns how many slots it has.
func (inv *Inventory) Size() int {
	if inv == nil {
		return 0
	}
	return len(inv.parts[0]) + len(inv.parts[1])
}

// at returns the stack in a slot, nil if there is none.
func (inv *Inventory) at(slot int) *ItemStack {
	if inv == nil || slot < 0 {
		return nil
	}
	if slot < len(inv.parts[0]) {
		return &inv.parts[0][slot]
	}
	if slot -= len(inv.parts[0]); slot < len(inv.parts[1]) {
		return &inv.parts[1][slot]
	}
	return nil
}

// Item returns what a slot holds.
func (inv *Inventory) Item(slot int) ItemStack {
	if s := inv.at(slot); s != nil {
		return *s
	}
	return ItemStack{}
}

// Items returns a copy of every slot.
func (inv *Inventory) Items() []ItemStack {
	if inv == nil {
		return nil
	}
	return append(slices.Clone(inv.parts[0]), inv.parts[1]...)
}

// SetItem puts s in a slot, saves it and shows it to everyone looking. It fires no
// player event: those are about what players do. False if there is no such slot.
func (inv *Inventory) SetItem(slot int, s ItemStack) bool {
	cur := inv.at(slot)
	if cur == nil {
		return false
	}
	*cur = inv.s.freshStack(s)
	inv.changed(slot)
	return true
}

// changed saves a slot that changed and shows it to everyone looking.
func (inv *Inventory) changed(slot int) {
	inv.holder.inventoryChanged(slot)
	if inv.owner != nil && inv.kind.own() {
		// A storage item put in gets its ID before the slot is sent.
		inv.owner.syncStorageIDs()
	}
	for _, v := range inv.Viewers() {
		inv.sendSlot(v, slot, true)
	}
	if inv.owner != nil && inv.kind.own() {
		inv.owner.syncStorage()
		inv.owner.flushNow()
	}
}

// AddItem puts s in the inventory, on matching stacks first and then in empty slots, and
// returns what did not fit.
func (inv *Inventory) AddItem(s ItemStack) ItemStack {
	if inv == nil || s.Empty() {
		return s
	}
	limit := s.maxStack(inv.s.reg)
	size := inv.Size()
	for i := 0; i < size && !s.Empty(); i++ {
		cur := inv.Item(i)
		if cur.Empty() || !cur.stacksWith(s) || cur.Count >= limit {
			continue
		}
		n := min(limit-cur.Count, s.Count)
		cur.Count += n
		s.Count -= n
		inv.SetItem(i, cur)
	}
	for i := 0; i < size && !s.Empty(); i++ {
		if !inv.Item(i).Empty() {
			continue
		}
		put := s
		put.Count = min(limit, s.Count)
		s.Count -= put.Count
		inv.SetItem(i, put)
	}
	if s.Count == 0 {
		return ItemStack{}
	}
	return s
}

// RemoveItem takes up to count of the named item out and returns how many it took.
func (inv *Inventory) RemoveItem(name string, count int) int {
	taken := 0
	for i, size := 0, inv.Size(); i < size && taken < count; i++ {
		cur := inv.Item(i)
		if cur.Empty() || cur.Name != name {
			continue
		}
		n := min(int(cur.Count), count-taken)
		cur.Count -= uint8(n) //nolint:gosec // at most the stack's count
		taken += n
		inv.SetItem(i, cur)
	}
	return taken
}

// Clear empties every slot.
func (inv *Inventory) Clear() {
	for i, size := 0, inv.Size(); i < size; i++ {
		if !inv.Item(i).Empty() {
			inv.SetItem(i, ItemStack{})
		}
	}
}

// Viewers returns the players looking at the slots: the owner of a player's own, and
// those with the container's window open.
func (inv *Inventory) Viewers() []*Player {
	if inv == nil {
		return nil
	}
	return inv.holder.inventoryViewers()
}

// SetView sets how this inventory's items look to each player, on top of the
// [InventoryViewEvent] listeners and after them; nil removes it. It is kept only while
// the inventory is in memory, and shown from the next send: call Refresh to show it now.
func (inv *Inventory) SetView(view func(e *InventoryViewEvent)) {
	if inv != nil {
		inv.view = view
	}
}

// Refresh sends every slot again to everyone looking, for when what they should see
// changed (a price, say) while the items did not.
func (inv *Inventory) Refresh() {
	for _, v := range inv.Viewers() {
		inv.sendAll(v, true)
	}
}

// RefreshSlots sends the given slots again to everyone looking, as one batch per player.
func (inv *Inventory) RefreshSlots(slots ...int) {
	for _, v := range inv.Viewers() {
		inv.sendSlots(v, slots, true)
	}
}

// RefreshFor sends every slot again to one player, if it is looking.
func (inv *Inventory) RefreshFor(p *Player) {
	if inv != nil && slices.Contains(inv.Viewers(), p) {
		inv.sendAll(p, true)
	}
}

// windowFor returns the window p sees the inventory in, false if it does not.
func (inv *Inventory) windowFor(p *Player) (uint32, bool) {
	if inv == nil || p == nil {
		return 0, false
	}
	if inv.kind.own() {
		return inv.window, inv.owner == p
	}
	return uint32(p.window.id), p.window.open && p.window.inv == inv
}

// viewed reports whether anything changes how the items look to players.
func (inv *Inventory) viewed() bool {
	return inv != nil && (inv.view != nil || inv.s.game != nil && inv.s.game.inventoryViewEv.Has())
}

// sendSlot sends one slot to p; now flushes it at once, for a player other than the one
// whose packets this tick flushes.
func (inv *Inventory) sendSlot(p *Player, slot int, now bool) {
	inv.sendSlots(p, []int{slot}, now)
}

// sendSlots sends some slots to p, each as BDS's InventorySlot.
func (inv *Inventory) sendSlots(p *Player, slots []int, now bool) {
	window, ok := inv.windowFor(p)
	if !ok || len(slots) == 0 {
		return
	}
	items := make([]ItemStack, len(slots))
	for i, slot := range slots {
		items[i] = inv.Item(slot)
	}
	items = inv.viewFor(p, slots, items)
	for i, slot := range slots {
		p.send(slotPacket(inv.s.reg, window, slot, items[i]))
	}
	if now {
		p.flushNow()
	}
}

// sendAll sends every slot to p as BDS's InventoryContent.
func (inv *Inventory) sendAll(p *Player, now bool) {
	window, ok := inv.windowFor(p)
	if !ok {
		return
	}
	items := inv.Items()
	if inv.viewed() {
		slots := make([]int, len(items))
		for i := range slots {
			slots[i] = i
		}
		items = inv.viewFor(p, slots, items)
	}
	p.send(contentPacket(inv.s.reg, window, items))
	if now {
		p.flushNow()
	}
}

// viewFor is items as p sees them: the listeners' view, then the inventory's own.
func (inv *Inventory) viewFor(p *Player, slots []int, items []ItemStack) []ItemStack {
	if !inv.viewed() {
		return items
	}
	e := InventoryViewEvent{Player: p, Inventory: inv, Slots: slots, Items: items}
	if g := inv.s.game; g != nil {
		g.inventoryViewEv.fire(inv.s.log, &e)
	}
	if inv.view != nil {
		inv.s.callView(inv.view, &e)
	}
	if len(e.Items) != len(items) {
		// A listener that dropped or added entries sees its change refused.
		return items
	}
	return e.Items
}

// callView runs an inventory's own view, which a plugin set, keeping the server up if it
// panics.
func (s *Host) callView(view func(e *InventoryViewEvent), e *InventoryViewEvent) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("inventory view panicked", "panic", r)
		}
	}()
	view(e)
}

// slotPacket is one slot as the client gets it.
func slotPacket(reg *registry.Registry, window uint32, slot int, s ItemStack) packet.Packet {
	return &packet.InventorySlot{WindowID: window, Slot: uint32(slot), NewItem: s.network(reg)} //nolint:gosec // slots are small
}

// contentPacket is a whole window's slots as the client gets them.
func contentPacket(reg *registry.Registry, window uint32, items []ItemStack) packet.Packet {
	content := make([]protocol.ItemInstance, len(items))
	for i, s := range items {
		content[i] = s.network(reg)
	}
	return &packet.InventoryContent{WindowID: window, Content: content}
}

// WithLore returns the stack with its lore set to lines, the rest of its data kept. It
// copies the data, so the stack it was made from is unchanged.
func (s ItemStack) WithLore(lines ...string) ItemStack {
	return s.withDisplay("Lore", lines)
}

// CustomName returns the stack's custom name (display Name), "" for none.
func (s ItemStack) CustomName() string {
	display, _ := s.Tag["display"].(map[string]any)
	name, _ := display["Name"].(string)
	return name
}

// WithName returns the stack with a custom name, or none for "". It copies the data.
func (s ItemStack) WithName(name string) ItemStack {
	if name == "" {
		return s.withDisplay("Name", nil)
	}
	return s.withDisplay("Name", name)
}

// withDisplay sets one field of the item's display compound, nil to remove it.
func (s ItemStack) withDisplay(key string, value any) ItemStack {
	display, _ := s.Tag["display"].(map[string]any)
	next := make(map[string]any, len(display)+1)
	for k, v := range display {
		next[k] = v
	}
	if lines, ok := value.([]string); ok {
		l := make([]any, len(lines))
		for i, line := range lines {
			l[i] = line
		}
		value = l
	}
	if value == nil {
		delete(next, key)
	} else {
		next[key] = value
	}
	if len(next) == 0 {
		return withTag(s, "display", nil)
	}
	return withTag(s, "display", next)
}

// Lore returns the stack's lore lines.
func (s ItemStack) Lore() []string {
	display, _ := s.Tag["display"].(map[string]any)
	l, _ := display["Lore"].([]any)
	out := make([]string, 0, len(l))
	for _, v := range l {
		if line, ok := v.(string); ok {
			out = append(out, line)
		}
	}
	return out
}

// ownHolder holds a player's own slots: the player is the only one looking.
type ownHolder struct{ p *Player }

func (h ownHolder) inventoryViewers() []*Player { return []*Player{h.p} }
func (ownHolder) inventoryChanged(int)          {}

// enderHolder holds a player's ender chest: the player looks only while one is open.
type enderHolder struct{ p *Player }

func (h enderHolder) inventoryViewers() []*Player {
	if h.p.window.open && h.p.window.kind == ContainerEnderChest {
		return []*Player{h.p}
	}
	return nil
}
func (enderHolder) inventoryChanged(int) {}

// playerInventories are the wrappers of a player's own slots, made on first use. They
// point into the player's items, which a request replaces as a whole but in place.
type playerInventories struct {
	main, armour, offhand, ui, ender *Inventory
}

// inventories returns the player's wrappers, making them if needed.
func (p *Player) inventories() *playerInventories {
	w := &p.invs
	if w.main != nil {
		return w
	}
	own := ownHolder{p: p}
	items := &p.inventory
	w.main = &Inventory{s: p.s, kind: ContainerInventory, parts: [2][]ItemStack{items.main[:]}, holder: own, owner: p, window: protocol.WindowIDInventory}
	w.armour = &Inventory{s: p.s, kind: ContainerArmour, parts: [2][]ItemStack{items.armor[:]}, holder: own, owner: p, window: protocol.WindowIDArmour}
	w.offhand = &Inventory{s: p.s, kind: ContainerOffhand, parts: [2][]ItemStack{items.offhand[:]}, holder: own, owner: p, window: protocol.WindowIDOffHand}
	w.ui = &Inventory{s: p.s, kind: ContainerUI, parts: [2][]ItemStack{items.ui[:]}, holder: own, owner: p, window: protocol.WindowIDUI}
	w.ender = &Inventory{s: p.s, kind: ContainerEnderChest, parts: [2][]ItemStack{items.ender[:]}, holder: enderHolder{p: p}, owner: p}
	return w
}

// Inventory returns the player's inventory: 36 slots, the hotbar first. Runs: the
// player's region, as for the methods below and every method of what they return.
func (p *Player) Inventory() *Inventory { return p.inventories().main }

// Held is the item in the player's hand. Runs: the player's region.
func (p *Player) Held() ItemStack { return p.inventory.main[p.inventory.held] }

// Armour returns the player's armour: helmet, chestplate, leggings, boots and body.
func (p *Player) Armour() *Inventory { return p.inventories().armour }

// Offhand returns the player's offhand, one slot.
func (p *Player) Offhand() *Inventory { return p.inventories().offhand }

// UI returns the player's UI slots: the cursor (slot 0), crafting grids and the inputs of
// screens such as an anvil's.
func (p *Player) UI() *Inventory { return p.inventories().ui }

// EnderChest returns the player's ender chest, whichever ender chest opens it.
func (p *Player) EnderChest() *Inventory { return p.inventories().ender }

// RefreshItems sends the player every item it can see again: its own slots and the open
// window's, through the views.
func (p *Player) RefreshItems() {
	p.refreshItems()
	p.flushNow()
}

// flushNow flushes what was queued for the player.
func (p *Player) flushNow() { p.sendNow() }

// sameStack reports whether two stacks are the same items, whatever their IDs.
func sameStack(a, b ItemStack) bool {
	if a.Empty() || b.Empty() {
		return a.Empty() && b.Empty()
	}
	return a.Count == b.Count && a.stacksWith(b)
}

// freshStack gives a stack a new network ID, or clears it if it is empty.
func (s *Host) freshStack(it ItemStack) ItemStack {
	if it.Empty() {
		return ItemStack{}
	}
	it.netID = s.nextStackID()
	return it
}
