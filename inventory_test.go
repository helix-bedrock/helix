package helix

import (
	"testing"

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
	s := &Server{reg: reg, level: level{gameMode: GameModeCreative}}
	p := &Player{s: s, gameMode: GameModeSurvival}
	p.inventory.load(map[string]any{"Inventory": []any{
		map[string]any{"Name": "minecraft:netherite_sword", "Count": uint8(1), "Damage": int16(0), "Slot": uint8(0), "WasPickedUp": uint8(0)},
		map[string]any{"Name": "minecraft:emerald", "Count": uint8(64), "Damage": int16(0), "Slot": uint8(2), "WasPickedUp": uint8(0)},
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
		take(1, slotInfo(containerCreatedOutput, createdOutputSlot, -15), slotInfo(containerHotbar, 0, 0)),
	}})
	if r.Status != protocol.ItemStackResponseStatusOK || p.inventory.main[0].Count != 1 || !p.inventory.ui[createdOutputSlot].Empty() {
		t.Fatalf("creative: %+v", r)
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
