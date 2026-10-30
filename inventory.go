package helix

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

// Inventory is a player's items: the inventory (the hotbar is its first
// nine slots), armour, offhand and the UI container (cursor, crafting).
// The server's copy is the only one: the client asks for changes and is
// told the result. Runs: owner of the player's entity.
type Inventory struct {
	main    [inventorySize]ItemStack
	armor   [armorSize]ItemStack
	offhand ItemStack
	ui      [uiSize]ItemStack
	// held is the selected hotbar slot.
	held uint8
}

// Held returns the item in the selected hotbar slot.
func (inv *Inventory) Held() ItemStack { return inv.main[inv.held] }

// Slot returns the item in an inventory slot (0 to 35, the hotbar first).
func (inv *Inventory) Slot(i int) ItemStack {
	if i < 0 || i >= inventorySize {
		return ItemStack{}
	}
	return inv.main[i]
}

// load reads the items BDS saved: Inventory and PlayerUIItems by their
// Slot, Armor and Offhand in order. Each stack gets a network ID.
func (inv *Inventory) load(data map[string]any, ids func() int32) {
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
		inv.offhand = withID(itemFromNBT(l[0]), ids)
	}
	for _, m := range compounds(data["PlayerUIItems"]) {
		if slot, ok := m["Slot"].(uint8); ok && int(slot) < uiSize {
			inv.ui[slot] = withID(itemFromNBT(m), ids)
		}
	}
	if held, ok := data["SelectedInventorySlot"].(int32); ok && held >= 0 && held < hotbarSize {
		inv.held = uint8(held)
	}
}

// compounds reads an NBT list of compounds.
func compounds(v any) []map[string]any {
	l, _ := v.([]any)
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
func (inv *Inventory) save(data map[string]any) {
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
	data["Inventory"], data["Armor"], data["PlayerUIItems"] = main, armor, ui
	data["Offhand"] = []any{inv.offhand.nbt(0, false)}
	data["Mainhand"] = []any{inv.Held().nbt(0, false)}
	data["SelectedInventorySlot"] = int32(inv.held)
}

// contentPackets are the inventory packets of the join, in BDS 1.26.60's
// order: inventory, armour, UI, offhand, then the selected hotbar slot.
func (inv *Inventory) contentPackets(reg *registry.Registry) []packet.Packet {
	list := func(window uint32, stacks []ItemStack) packet.Packet {
		content := make([]protocol.ItemInstance, len(stacks))
		for i, s := range stacks {
			content[i] = s.network(reg)
		}
		return &packet.InventoryContent{WindowID: window, Content: content}
	}
	return []packet.Packet{
		list(protocol.WindowIDInventory, inv.main[:]),
		list(protocol.WindowIDArmour, inv.armor[:]),
		list(protocol.WindowIDUI, inv.ui[:]),
		list(protocol.WindowIDOffHand, []ItemStack{inv.offhand}),
		&packet.PlayerHotBar{SelectedHotBarSlot: uint32(inv.held), WindowID: protocol.WindowIDInventory, SelectHotBarSlot: true},
	}
}

// handleMobEquipment selects a hotbar slot. Only the choice of slot is
// taken from the client; the item is the server's.
func handleMobEquipment(p *Player, pk *packet.MobEquipment) {
	if pk.WindowID == protocol.WindowIDInventory && pk.HotBarSlot < hotbarSize {
		p.inventory.held = pk.HotBarSlot
	}
}
