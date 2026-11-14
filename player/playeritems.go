package player

import (
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// The player's containers, sized as BDS 1.26.60 sends them at join.
const (
	inventorySize = 36
	hotbarSize    = 9
	armorSize     = 5
	uiSize        = 57
	// cursorSlot and createdOutputSlot are the UI container's slots for
	// the item under the cursor and the item a recipe or the creative
	// inventory made.
	cursorSlot        = 0
	createdOutputSlot = 50
)

// playerItems is a player's items: the inventory (the hotbar is its first
// nine slots), armour, offhand and the UI container (cursor, crafting).
// The server's copy is the only one: the client asks for changes and is
// told the result. A request works on a copy and puts it back in place, so the
// [Inventory] wrappers pointing in stay right. Runs: owner of the player's entity.
type playerItems struct {
	main    [inventorySize]ItemStack
	armor   [armorSize]ItemStack
	offhand [1]ItemStack
	ui      [uiSize]ItemStack
	// held is the selected hotbar slot.
	held uint8
	// ender is the player's ender chest. BDS keeps it on the player, whichever
	// ender chest opens it, and a death does not throw it.
	ender [chestSize]ItemStack
	// open is what a request works on of the chest the player has open, nil with
	// none. The chest holds the real items.
	open []ItemStack
}

// Held returns the item in the selected hotbar slot.
func (inv *playerItems) Held() ItemStack { return inv.main[inv.held] }

// Slot returns the item in an inventory slot (0 to 35, the hotbar first).
func (inv *playerItems) Slot(i int) ItemStack {
	if i < 0 || i >= inventorySize {
		return ItemStack{}
	}
	return inv.main[i]
}

// load reads the items BDS saved: Inventory and PlayerUIItems by their
// Slot, Armor and Offhand in order. Each stack gets a network ID.
func (inv *playerItems) load(data map[string]any, ids func() int32) {
	for _, m := range compounds(data["Inventory"]) {
		if slot, ok := m["Slot"].(uint8); ok && int(slot) < inventorySize {
			inv.main[slot] = withID(itemFromNBT(m), ids)
		}
	}
	for i, m := range compounds(data["Armor"]) {
		if i < armorSize {
			inv.armor[i] = withID(itemFromNBT(m), ids)
		}
	}
	if l := compounds(data["Offhand"]); len(l) > 0 {
		inv.offhand[0] = withID(itemFromNBT(l[0]), ids)
	}
	for _, m := range compounds(data["PlayerUIItems"]) {
		if slot, ok := m["Slot"].(uint8); ok && int(slot) < uiSize {
			inv.ui[slot] = withID(itemFromNBT(m), ids)
		}
	}
	for _, m := range compounds(data["EnderChestInventory"]) {
		if slot, ok := m["Slot"].(uint8); ok && int(slot) < chestSize {
			inv.ender[slot] = withID(itemFromNBT(m), ids)
		}
	}
	if held, ok := data["SelectedInventorySlot"].(int32); ok && held >= 0 && held < hotbarSize {
		inv.held = uint8(held)
	}
}

// takeUIItems is what BDS does to the UI slots after loading a player: a live player
// gets their items in the inventory. It reports whether any are left, to be dropped once
// the player is in the world.
func (inv *playerItems) takeUIItems(reg *registry.Registry, ids func() int32, alive bool) bool {
	left := false
	for i := range inv.ui {
		s := inv.ui[i]
		if s.Empty() {
			continue
		}
		if alive {
			s = inv.add(reg, s, ids, func(int) {})
		}
		inv.ui[i] = s
		left = left || !s.Empty()
	}
	return left
}

// tickUILeftovers drops what the UI slots still held after loading. A drop that was
// refused stays in its slot, so no item is lost. Region tick.
func (p *Player) tickUILeftovers() {
	if !p.uiLeftovers {
		return
	}
	p.uiLeftovers = false
	for i := range p.inventory.ui {
		s := p.inventory.ui[i]
		if !s.Empty() && p.s.game.sys.DropItem(p, s, true) {
			p.inventory.ui[i] = ItemStack{}
		}
	}
	p.refreshItems()
	p.flush()
}

// compounds reads an NBT list of compounds, decoding one kept as a Raw (packItems).
func compounds(v any) []map[string]any {
	l, _ := unpacked(v).([]any)
	out := make([]map[string]any, 0, len(l))
	for _, x := range l {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func withID(s ItemStack, ids func() int32) ItemStack {
	if !s.Empty() {
		s.netID = ids()
	}
	return s
}

// save writes the items as BDS saves them.
func (inv *playerItems) save(data map[string]any) {
	main := make([]any, inventorySize)
	for i, s := range inv.main {
		main[i] = s.nbt(i, true)
	}
	armor := make([]any, armorSize)
	for i, s := range inv.armor {
		armor[i] = s.nbt(i, false)
	}
	var ui []any
	for i, s := range inv.ui {
		if !s.Empty() {
			ui = append(ui, s.nbt(i, true))
		}
	}
	if ui == nil {
		ui = []any{}
	}
	ender := []any{}
	for i, s := range inv.ender {
		if !s.Empty() {
			ender = append(ender, s.nbt(i, true))
		}
	}
	data["EnderChestInventory"] = ender
	data["Inventory"], data["Armor"], data["PlayerUIItems"] = main, armor, ui
	data["Offhand"] = []any{inv.offhand[0].nbt(0, false)}
	data["Mainhand"] = []any{inv.Held().nbt(0, false)}
	data["SelectedInventorySlot"] = int32(inv.held)
}

// handleMobEquipment selects a hotbar slot. Only the choice of slot is
// taken from the client; the item is the server's.
func handleMobEquipment(p *Player, pk *packet.MobEquipment) {
	if pk.WindowID == protocol.WindowIDInventory && pk.HotBarSlot < hotbarSize {
		p.inventory.held = pk.HotBarSlot
		p.sendEquipment()
	}
}

// freeSlot returns the first empty inventory slot, -1 if none.
func (inv *playerItems) freeSlot() int {
	for i := range inv.main {
		if inv.main[i].Empty() {
			return i
		}
	}
	return -1
}

// slotWithRoom returns the first slot holding s's item with room for more, -1
// if none.
func (inv *playerItems) slotWithRoom(reg *registry.Registry, s ItemStack) int {
	max := s.maxStack(reg)
	for i := range inv.main {
		if o := &inv.main[i]; !o.Empty() && o.stacksWith(s) && o.Count < max {
			return i
		}
	}
	return -1
}

// canAdd reports whether any of s fits, as BDS's inventory canAdd.
func (inv *playerItems) canAdd(reg *registry.Registry, s ItemStack) bool {
	return inv.freeSlot() >= 0 || inv.slotWithRoom(reg, s) >= 0
}

// add puts s in the inventory as BDS's add: a damaged or unstackable item in
// the first free slot; others on the first stack with room, then the first
// free slot, until none is left. It returns what did not fit; changed is told
// each slot it filled, ids numbers new stacks.
func (inv *playerItems) add(reg *registry.Registry, s ItemStack, ids func() int32, changed func(int)) ItemStack {
	maxStack := s.maxStack(reg)
	if maxStack == 0 {
		return s
	}
	if maxStack == 1 || itemDamage(s) > 0 {
		i := inv.freeSlot()
		if i < 0 {
			return s
		}
		s.netID = ids()
		inv.main[i] = s
		changed(i)
		return ItemStack{}
	}
	for s.Count > 0 {
		i := inv.slotWithRoom(reg, s)
		if i >= 0 {
			moved := min(maxStack-inv.main[i].Count, s.Count)
			inv.main[i].Count += moved
			s.Count -= moved
			changed(i)
			continue
		}
		if i = inv.freeSlot(); i < 0 {
			return s
		}
		moved := min(maxStack, s.Count)
		placed := s
		placed.Count, placed.netID = moved, ids()
		inv.main[i] = placed
		s.Count -= moved
		changed(i)
	}
	return ItemStack{}
}
