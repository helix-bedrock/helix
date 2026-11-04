package player

import (
	"log/slog"
	"testing"

	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
)

// testPlayer is a creative player holding 64 emeralds in slot 2 and a
// sword in slot 0, with vanilla's registry.
func testPlayer(t *testing.T) *Player {
	t.Helper()
	setup := registry.NewSetup(nil, "minecraft")
	if err := registry.Vanilla(setup); err != nil {
		t.Fatal(err)
	}
	reg, err := setup.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	s := &Host{redstone: circuit.NewSettings(circuit.DefaultConfig()), reg: reg, log: slog.New(slog.DiscardHandler), level: Level{GameMode: GameModeCreative}}
	p := &Player{s: s, gameMode: GameModeSurvival}
	p.inventory.load(map[string]any{"Inventory": []any{
		map[string]any{"Name": "minecraft:netherite_sword", "Count": uint8(1), "Damage": int16(0), "Slot": uint8(0), "WasPickedUp": uint8(0)},
		map[string]any{"Name": "minecraft:emerald", "Count": uint8(64), "Damage": int16(0), "Slot": uint8(2), "WasPickedUp": uint8(0)},
		map[string]any{"Name": "minecraft:diamond_helmet", "Count": uint8(1), "Damage": int16(0), "Slot": uint8(5), "WasPickedUp": uint8(0)},
	}}, func() int32 { return s.stackIDs.Add(1) })
	return p
}

func slotInfo(container, slot byte, id int32) protocol.StackRequestSlotInfo {
	return protocol.StackRequestSlotInfo{Container: protocol.FullContainerName{ContainerID: container}, Slot: slot, StackNetworkID: id}
}

func take(count byte, src, dst protocol.StackRequestSlotInfo) *protocol.TakeStackRequestAction {
	a := &protocol.TakeStackRequestAction{}
	a.Count, a.Source, a.Destination = count, src, dst
	return a
}

// TestStackRequests checks BDS's checks on the player's own inventory.
func TestStackRequests(t *testing.T) {
	p := testPlayer(t)
	sword, emeralds := p.inventory.main[0].netID, p.inventory.main[2].netID

	// Half the emeralds to the cursor: the cursor gets a new stack.
	r := p.stackRequest(protocol.ItemStackRequest{RequestID: -1, Actions: []protocol.StackRequestAction{
		take(32, slotInfo(containerHotbar, 2, emeralds), slotInfo(containerCursor, 0, 0)),
	}})
	if r.Status != protocol.ItemStackResponseStatusOK || len(r.ContainerInfo) != 2 ||
		p.inventory.main[2].Count != 32 || p.inventory.ui[cursorSlot].Count != 32 {
		t.Fatalf("take half: %+v", r)
	}
	cursor := p.inventory.ui[cursorSlot].netID

	// The client names a stack the server does not have there.
	r = p.stackRequest(protocol.ItemStackRequest{RequestID: -3, Actions: []protocol.StackRequestAction{
		take(1, slotInfo(containerHotbar, 2, 999), slotInfo(containerInventory, 9, 0)),
	}})
	if r.Status != protocol.ItemStackResponseStatusFailedToValidateSrcSlot || p.inventory.main[9].Count != 0 {
		t.Fatalf("wrong net ID accepted: %+v", r)
	}

	// Emeralds onto the sword, and more than the source has: refused, and
	// a refused request changes nothing even after a good action.
	r = p.stackRequest(protocol.ItemStackRequest{RequestID: -5, Actions: []protocol.StackRequestAction{
		take(1, slotInfo(containerCursor, 0, cursor), slotInfo(containerInventory, 10, 0)),
		take(1, slotInfo(containerCursor, 0, cursor), slotInfo(containerHotbar, 0, sword)),
	}})
	if r.Status != protocol.ItemStackResponseStatusCannotPlaceItem || p.inventory.main[10].Count != 0 || p.inventory.ui[cursorSlot].Count != 32 {
		t.Fatalf("onto another item: %+v", r)
	}
	r = p.stackRequest(protocol.ItemStackRequest{RequestID: -7, Actions: []protocol.StackRequestAction{
		take(33, slotInfo(containerCursor, 0, cursor), slotInfo(containerInventory, 10, 0)),
	}})
	if r.Status == protocol.ItemStackResponseStatusOK {
		t.Fatal("moved more than the source has")
	}

	// Back onto the emeralds, naming the slot by the first request's ID.
	r = p.stackRequest(protocol.ItemStackRequest{RequestID: -9, Actions: []protocol.StackRequestAction{
		take(32, slotInfo(containerCursor, 0, -1), slotInfo(containerHotbar, 2, -1)),
	}})
	if r.Status != protocol.ItemStackResponseStatusOK || p.inventory.main[2].Count != 64 || !p.inventory.ui[cursorSlot].Empty() {
		t.Fatalf("merge back: %+v", r)
	}

	// Destroying needs creative; the world's game mode is followed.
	destroy := &protocol.DestroyStackRequestAction{Count: 1, Source: slotInfo(containerHotbar, 0, sword)}
	if r = p.stackRequest(protocol.ItemStackRequest{RequestID: -11, Actions: []protocol.StackRequestAction{destroy}}); r.Status != protocol.ItemStackResponseStatusCannotDestroyItem {
		t.Fatalf("destroy in survival: %+v", r)
	}
	p.gameMode = GameModeDefault
	if r = p.stackRequest(protocol.ItemStackRequest{RequestID: -13, Actions: []protocol.StackRequestAction{destroy}}); r.Status != protocol.ItemStackResponseStatusOK || !p.inventory.main[0].Empty() {
		t.Fatalf("destroy in creative: %+v", r)
	}

	// A creative item into the hotbar through the created output.
	r = p.stackRequest(protocol.ItemStackRequest{RequestID: -15, Actions: []protocol.StackRequestAction{
		&protocol.CraftCreativeStackRequestAction{CreativeItemNetworkID: p.s.reg.Creative.Packet().Items[0].CreativeItemNetworkID, NumberOfCrafts: 1},
		&protocol.CraftResultsDeprecatedStackRequestAction{TimesCrafted: 1},
		take(1, slotInfo(containerCreatedOutput, createdOutputSlot, -15), slotInfo(containerHotbar, 0, 0)),
	}})
	if r.Status != protocol.ItemStackResponseStatusOK || p.inventory.main[0].Count != 1 || !p.inventory.ui[createdOutputSlot].Empty() {
		t.Fatalf("creative: %+v", r)
	}
}

// TestEquipment checks BDS's armour and offhand rules.
func TestEquipment(t *testing.T) {
	p := testPlayer(t)
	helmet, emeralds := p.inventory.main[5].netID, p.inventory.main[2].netID
	try := func(count byte, src, dst protocol.StackRequestSlotInfo) uint8 {
		return p.stackRequest(protocol.ItemStackRequest{RequestID: -1, Actions: []protocol.StackRequestAction{take(count, src, dst)}}).Status
	}
	if try(1, slotInfo(containerHotbar, 5, helmet), slotInfo(containerArmor, 1, 0)) == protocol.ItemStackResponseStatusOK {
		t.Fatal("helmet worn on the chest")
	}
	if try(1, slotInfo(containerHotbar, 2, emeralds), slotInfo(containerOffhand, 1, 0)) == protocol.ItemStackResponseStatusOK {
		t.Fatal("emerald in the offhand")
	}
	if try(1, slotInfo(containerHotbar, 2, emeralds), slotInfo(containerArmor, 0, 0)) == protocol.ItemStackResponseStatusOK {
		t.Fatal("emerald worn")
	}
	if s := try(1, slotInfo(containerHotbar, 5, helmet), slotInfo(containerArmor, 0, 0)); s != protocol.ItemStackResponseStatusOK || p.inventory.armor[0].Name != "minecraft:diamond_helmet" {
		t.Fatalf("helmet not worn: %d", s)
	}
}

func TestInventorySave(t *testing.T) {
	p := testPlayer(t)
	data := map[string]any{}
	p.inventory.save(data)
	var back Inventory
	back.load(data, func() int32 { return 0 })
	for i := range inventorySize {
		a, b := p.inventory.main[i], back.main[i]
		if a.Name != b.Name || a.Count != b.Count {
			t.Fatalf("slot %d: %+v != %+v", i, a, b)
		}
	}
	if l := data["Armor"].([]any); len(l) != armorSize || len(l[0].(map[string]any)) != 0 {
		t.Fatalf("armour %v", l)
	}
}

// TestDropRequest checks a drop takes only what the server holds, and only
// once the whole request passed.
func TestDropRequest(t *testing.T) {
	p := testPlayer(t)
	p.abilities = DefaultAbilities()
	var dropped []ItemStack
	p.s.game = &gameplay{}
	p.s.game.sys.DropItem = func(_ *Player, s ItemStack, _ bool) bool {
		dropped = append(dropped, s)
		return true
	}
	emeralds := p.inventory.main[2].netID
	drop := func(count byte, id int32) *protocol.DropStackRequestAction {
		return &protocol.DropStackRequestAction{Count: count, Source: slotInfo(containerHotbar, 2, id)}
	}
	cases := []struct {
		name   string
		action protocol.StackRequestAction
		status uint8
	}{
		{"more than held", drop(65, emeralds), protocol.ItemStackResponseStatusInvalidRemovedAmount},
		{"none", drop(0, emeralds), protocol.ItemStackResponseStatusInvalidRemovedAmount},
		{"forged stack", drop(1, emeralds+999), protocol.ItemStackResponseStatusFailedToValidateSrcSlot},
		{"empty slot", &protocol.DropStackRequestAction{Count: 1, Source: slotInfo(containerHotbar, 7, 0)}, protocol.ItemStackResponseStatusFailedToValidateSrcSlot},
	}
	for _, c := range cases {
		r := p.stackRequest(protocol.ItemStackRequest{RequestID: -1, Actions: []protocol.StackRequestAction{c.action}})
		if r.Status != c.status || p.inventory.main[2].Count != 64 || len(dropped) != 0 {
			t.Fatalf("%s: status %d, count %d, dropped %d", c.name, r.Status, p.inventory.main[2].Count, len(dropped))
		}
	}
	// A drop followed by a refused action drops nothing.
	r := p.stackRequest(protocol.ItemStackRequest{RequestID: -3, Actions: []protocol.StackRequestAction{drop(4, emeralds), drop(1, emeralds+999)}})
	if r.Status == protocol.ItemStackResponseStatusOK || p.inventory.main[2].Count != 64 || len(dropped) != 0 {
		t.Fatalf("refused request: status %d, count %d, dropped %d", r.Status, p.inventory.main[2].Count, len(dropped))
	}
	r = p.stackRequest(protocol.ItemStackRequest{RequestID: -5, Actions: []protocol.StackRequestAction{drop(4, emeralds)}})
	if r.Status != protocol.ItemStackResponseStatusOK || p.inventory.main[2].Count != 60 || len(dropped) != 1 || dropped[0].Count != 4 || dropped[0].Name != "minecraft:emerald" {
		t.Fatalf("drop: status %d, count %d, dropped %v", r.Status, p.inventory.main[2].Count, dropped)
	}
}

// TestLegacyDrop checks the client's old items must match the server's and
// only one slot and a world drop are accepted.
func TestLegacyDrop(t *testing.T) {
	p := testPlayer(t)
	it, ok := p.s.reg.Items.ByName("minecraft:emerald")
	if !ok {
		t.Fatal("no emerald")
	}
	stack := func(id int32, count uint16) protocol.ItemInstance {
		return protocol.ItemInstance{Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: count}}
	}
	id := int32(it.ID)
	from := func(slot uint32, old, left uint16) protocol.InventoryAction {
		return protocol.InventoryAction{SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.Option(int8(protocol.WindowIDInventory)), InventorySlot: slot, OldItem: stack(id, old), NewItem: stack(id, left)}
	}
	out := func(itemID int32, n uint16) protocol.InventoryAction {
		return protocol.InventoryAction{SourceType: protocol.InventoryActionSourceWorld, NewItem: stack(itemID, n)}
	}
	diamond, _ := p.s.reg.Items.ByName("minecraft:diamond")
	bad := map[string][]protocol.InventoryAction{
		"item not held":  {from(7, 1, 0), out(id, 1)},
		"count not held": {from(2, 65, 0), out(id, 65)},
		"makes items":    {from(2, 64, 63), out(id, 64)},
		"other item out": {from(2, 64, 63), out(int32(diamond.ID), 1)},
		"no slot":        {out(id, 1)},
		"three actions":  {from(2, 64, 63), out(id, 1), out(id, 1)},
	}
	for name, a := range bad {
		if _, _, ok := p.legacyDrop(a); ok {
			t.Fatalf("%s: accepted", name)
		}
	}
	slot, count, ok := p.legacyDrop([]protocol.InventoryAction{from(2, 64, 63), out(id, 1)})
	if !ok || slot != 2 || count != 1 {
		t.Fatalf("honest drop: slot %d, count %d, ok %v", slot, count, ok)
	}
}

// TestInventoryAdd checks BDS's order: stacks with room, then free slots; the
// rest comes back.
func TestInventoryAdd(t *testing.T) {
	p := testPlayer(t)
	ids := func() int32 { return p.s.stackIDs.Add(1) }
	var filled []int
	changed := func(i int) { filled = append(filled, i) }
	inv := &p.inventory
	inv.main[2].Count = 60
	rest := inv.add(p.s.reg, ItemStack{Name: "minecraft:emerald", Count: 10}, ids, changed)
	if !rest.Empty() || inv.main[2].Count != 64 || inv.main[1].Count != 6 || inv.main[1].netID == 0 || len(filled) != 2 {
		t.Fatalf("rest %v, slot 2 %d, slot 1 %d, filled %v", rest, inv.main[2].Count, inv.main[1].Count, filled)
	}
	for i := range inv.main {
		if inv.main[i].Empty() {
			inv.main[i] = ItemStack{Name: "minecraft:diamond", Count: 64}
		}
	}
	if inv.canAdd(p.s.reg, ItemStack{Name: "minecraft:stick", Count: 1}) {
		t.Fatal("full inventory takes a new item")
	}
	rest = inv.add(p.s.reg, ItemStack{Name: "minecraft:emerald", Count: 60}, ids, changed)
	if rest.Count != 2 || inv.main[1].Count != 64 {
		t.Fatalf("rest %d, slot 1 %d", rest.Count, inv.main[1].Count)
	}
}
