package helix

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// The container IDs a stack request names a player's slots by.
const (
	containerArmor         = protocol.ContainerArmor
	containerCombined      = protocol.ContainerCombinedHotBarAndInventory
	containerHotbar        = protocol.ContainerHotBar
	containerInventory     = protocol.ContainerInventory
	containerOffhand       = protocol.ContainerOffhand
	containerCursor        = protocol.ContainerCursor
	containerCreatedOutput = protocol.ContainerCreatedOutput
)

// Item locks, as BDS keeps them in an item's "minecraft:item_lock" tag.
const itemLockInSlot = 1

// bindingCurse is the enchantment that keeps armour on a player who is not
// in creative.
const bindingCurse = 27

// assignmentsKept is how many past requests' slots a request may name by
// request ID (the client does so for requests not yet answered).
const assignmentsKept = 32

// slotRef is a slot a request names, resolved to the server's slot.
type slotRef struct {
	container byte
	slot      byte
	stack     *ItemStack
	// kind tells the slot's rules apart.
	kind byte
}

// requestState is a player's stack request state: the copy a request
// works on, and the network IDs earlier requests left in each slot.
type requestState struct {
	work        Inventory
	assignments [assignmentsKept]struct {
		id    int32
		slots map[[2]byte]int32
	}
	next int
}

// handleItemStackRequest answers the client's inventory requests as BDS's
// server-authoritative inventory does: each request is checked action by
// action against the server's items on a copy, and only a request that
// passes as a whole changes them. The response tells the client what its
// slots hold now; a refused request changes nothing and the client puts
// its items back.
func handleItemStackRequest(p *Player, pk *packet.ItemStackRequest) {
	resp := &packet.ItemStackResponse{Responses: make([]protocol.ItemStackResponse, 0, len(pk.Requests))}
	for _, req := range pk.Requests {
		resp.Responses = append(resp.Responses, p.stackRequest(req))
	}
	_ = p.conn.WritePacket(resp)
	p.flush()
}

// stackRequest runs one request.
func (p *Player) stackRequest(req protocol.ItemStackRequest) protocol.ItemStackResponse {
	st := &p.requests
	st.work = p.inventory
	var touched []slotRef
	for _, a := range req.Actions {
		status, refs := p.stackAction(a)
		if status != protocol.ItemStackResponseStatusOK {
			return protocol.ItemStackResponse{Status: status, RequestID: req.RequestID}
		}
		touched = append(touched, refs...)
	}
	// The created output only holds an item for the request that made it.
	st.work.ui[createdOutputSlot] = ItemStack{}
	p.inventory = st.work

	resp := protocol.ItemStackResponse{Status: protocol.ItemStackResponseStatusOK, RequestID: req.RequestID}
	slots := map[[2]byte]int32{}
	for _, r := range touched {
		if r.container == containerCreatedOutput {
			continue
		}
		key := [2]byte{r.container, r.slot}
		if _, seen := slots[key]; seen {
			continue
		}
		s := p.slotStack(r)
		slots[key] = s.netID
		info := protocol.StackResponseSlotInfo{Slot: r.slot, HotbarSlot: r.slot, Count: s.Count, StackNetworkID: s.netID, CustomName: customName(s)}
		i := containerIndex(resp.ContainerInfo, r.container)
		if i < 0 {
			resp.ContainerInfo = append(resp.ContainerInfo, protocol.StackResponseContainerInfo{Container: protocol.FullContainerName{ContainerID: r.container}})
			i = len(resp.ContainerInfo) - 1
		}
		resp.ContainerInfo[i].SlotInfo = append(resp.ContainerInfo[i].SlotInfo, info)
	}
	a := &st.assignments[st.next%assignmentsKept]
	a.id, a.slots = req.RequestID, slots
	st.next++
	return resp
}

func containerIndex(l []protocol.StackResponseContainerInfo, id byte) int {
	for i, c := range l {
		if c.Container.ContainerID == id {
			return i
		}
	}
	return -1
}

// customName is the name an anvil gave the item, which the response
// carries.
func customName(s ItemStack) string {
	display, _ := s.Tag["display"].(map[string]any)
	name, _ := display["Name"].(string)
	return name
}

// slotStack returns the final stack of a touched slot.
func (p *Player) slotStack(r slotRef) ItemStack {
	ref, ok := p.slot(&p.inventory, r.container, r.slot)
	if !ok {
		return ItemStack{}
	}
	return *ref.stack
}

// stackAction checks and applies one action on the request's copy.
func (p *Player) stackAction(a protocol.StackRequestAction) (uint8, []slotRef) {
	switch a := a.(type) {
	case *protocol.TakeStackRequestAction:
		return p.transfer(a.Count, a.Source, a.Destination)
	case *protocol.PlaceStackRequestAction:
		return p.transfer(a.Count, a.Source, a.Destination)
	case *protocol.SwapStackRequestAction:
		return p.swap(a.Source, a.Destination)
	case *protocol.DestroyStackRequestAction:
		return p.destroy(a.Count, a.Source)
	case *protocol.DropStackRequestAction:
		// Dropped items become item entities, which are not built yet.
		return protocol.ItemStackResponseStatusCannotDropItem, nil
	case *protocol.CraftCreativeStackRequestAction:
		return p.craftCreative(a)
	}
	// Crafting and the other screens come later.
	return protocol.ItemStackResponseStatusError, nil
}

// slot resolves a container and slot of the player's to the stack in inv.
// The hotbar, the inventory and both together are views of one container,
// as in BDS. Offhand accepts slot 0 and 1 (lenient: which one the client
// uses is not checked against BDS yet).
func (p *Player) slot(inv *Inventory, container, slot byte) (slotRef, bool) {
	r := slotRef{container: container, slot: slot, kind: container}
	switch container {
	case containerHotbar:
		if slot >= hotbarSize {
			return r, false
		}
		r.stack, r.kind = &inv.main[slot], containerCombined
	case containerInventory:
		if slot < hotbarSize || slot >= inventorySize {
			return r, false
		}
		r.stack, r.kind = &inv.main[slot], containerCombined
	case containerCombined:
		if slot >= inventorySize {
			return r, false
		}
		r.stack = &inv.main[slot]
	case containerArmor:
		if slot >= armorSize {
			return r, false
		}
		r.stack = &inv.armor[slot]
	case containerOffhand:
		if slot > 1 {
			return r, false
		}
		r.stack = &inv.offhand
	case containerCursor:
		if slot != cursorSlot {
			return r, false
		}
		r.stack = &inv.ui[cursorSlot]
	case containerCreatedOutput:
		if slot != createdOutputSlot {
			return r, false
		}
		r.stack = &inv.ui[createdOutputSlot]
	default:
		return r, false
	}
	return r, true
}

// validate resolves a slot the client named and checks, as BDS does, that
// the stack it names by network ID is the one the server has there; a
// negative ID names the stack an earlier request left in the slot. The
// created output holds the item this request made, whatever ID the
// client gives it.
func (p *Player) validate(info protocol.StackRequestSlotInfo, needItem bool) (slotRef, bool) {
	r, ok := p.slot(&p.requests.work, info.Container.ContainerID, info.Slot)
	if !ok {
		return r, false
	}
	if needItem && r.stack.Empty() {
		return r, false
	}
	if r.container == containerCreatedOutput {
		return r, true
	}
	want := info.StackNetworkID
	if want < 0 {
		var found bool
		want, found = p.requests.assigned(want, info.Container.ContainerID, info.Slot)
		if !found {
			return r, false
		}
	}
	return r, r.stack.netID == want
}

// assigned returns the network ID request id left in a slot.
func (st *requestState) assigned(id int32, container, slot byte) (int32, bool) {
	for _, a := range st.assignments {
		if a.id == id && a.slots != nil {
			netID, ok := a.slots[[2]byte{container, slot}]
			return netID, ok
		}
	}
	return 0, false
}

// sameSlot reports whether two slots are one, also through the hotbar and
// inventory views of the combined container.
func sameSlot(a, b slotRef) bool { return a.stack == b.stack }

// transfer moves count items from src to dst (take and place), as BDS's
// transfer: never to the created output or the same slot, only as many
// as the source has, into an empty slot or onto the same item, never past
// the item's stack size.
func (p *Player) transfer(count byte, srcInfo, dstInfo protocol.StackRequestSlotInfo) (uint8, []slotRef) {
	if dstInfo.Container.ContainerID == containerCreatedOutput {
		return protocol.ItemStackResponseStatusDstContainerEqualToCreatedOutputContainer, nil
	}
	src, ok := p.validate(srcInfo, true)
	if !ok {
		return protocol.ItemStackResponseStatusFailedToValidateSrcSlot, nil
	}
	dst, ok := p.validate(dstInfo, false)
	if !ok {
		return protocol.ItemStackResponseStatusFailedToValidateDstSlot, nil
	}
	if sameSlot(src, dst) {
		return protocol.ItemStackResponseStatusDstContainerAndSlotEqualToSrcContainerAndSlot, nil
	}
	if count == 0 || count > src.stack.Count {
		return protocol.ItemStackResponseStatusError, nil
	}
	moved := *src.stack
	moved.Count = count
	if !p.mayRemove(src, *src.stack) || !p.mayPlace(dst, moved) {
		return protocol.ItemStackResponseStatusCannotPlaceItem, nil
	}
	max := moved.maxStack(p.s.reg)
	switch {
	case dst.stack.Empty():
		if count > max {
			return protocol.ItemStackResponseStatusCannotPlaceItem, nil
		}
		if count == src.stack.Count {
			// The whole stack moves and keeps its ID.
			*dst.stack, *src.stack = *src.stack, ItemStack{}
			return protocol.ItemStackResponseStatusOK, []slotRef{src, dst}
		}
		moved.netID = p.s.stackIDs.Add(1)
		*dst.stack = moved
	case dst.stack.stacksWith(moved) && int(dst.stack.Count)+int(count) <= int(max):
		dst.stack.Count += count
	default:
		return protocol.ItemStackResponseStatusCannotPlaceItem, nil
	}
	src.stack.Count -= count
	if src.stack.Count == 0 {
		*src.stack = ItemStack{}
	}
	return protocol.ItemStackResponseStatusOK, []slotRef{src, dst}
}

// swap exchanges two slots' stacks, as BDS's swap: both may be taken out
// and put in the other's slot.
func (p *Player) swap(srcInfo, dstInfo protocol.StackRequestSlotInfo) (uint8, []slotRef) {
	if dstInfo.Container.ContainerID == containerCreatedOutput {
		return protocol.ItemStackResponseStatusDstContainerEqualToCreatedOutputContainer, nil
	}
	src, ok := p.validate(srcInfo, true)
	if !ok {
		return protocol.ItemStackResponseStatusFailedToValidateSrcSlot, nil
	}
	dst, ok := p.validate(dstInfo, true)
	if !ok {
		return protocol.ItemStackResponseStatusFailedToValidateDstSlot, nil
	}
	if sameSlot(src, dst) {
		return protocol.ItemStackResponseStatusDstContainerAndSlotEqualToSrcContainerAndSlot, nil
	}
	a, b := *src.stack, *dst.stack
	if !p.mayRemove(src, a) || !p.mayRemove(dst, b) || !p.mayPlace(dst, a) || !p.mayPlace(src, b) {
		return protocol.ItemStackResponseStatusCannotSwapItem, nil
	}
	*src.stack, *dst.stack = b, a
	return protocol.ItemStackResponseStatusOK, []slotRef{src, dst}
}

// destroy deletes items, which BDS allows only in creative.
func (p *Player) destroy(count byte, srcInfo protocol.StackRequestSlotInfo) (uint8, []slotRef) {
	src, ok := p.validate(srcInfo, true)
	if !ok {
		return protocol.ItemStackResponseStatusFailedToValidateSrcSlot, nil
	}
	if !p.creative() || !p.mayRemove(src, *src.stack) {
		return protocol.ItemStackResponseStatusCannotDestroyItem, nil
	}
	if count == 0 || count > src.stack.Count {
		return protocol.ItemStackResponseStatusInvalidRemovedAmount, nil
	}
	src.stack.Count -= count
	if src.stack.Count == 0 {
		*src.stack = ItemStack{}
	}
	return protocol.ItemStackResponseStatusOK, []slotRef{src}
}

// craftCreative puts an item from the creative inventory in the created
// output, for creative players only; the next actions take it from there.
func (p *Player) craftCreative(a *protocol.CraftCreativeStackRequestAction) (uint8, []slotRef) {
	if !p.creative() {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	for _, it := range p.s.reg.Creative.Packet().Items {
		if it.CreativeItemNetworkID != a.CreativeItemNetworkID {
			continue
		}
		s := itemFromNetwork(p.s.reg, it.Item)
		if s.Empty() {
			break
		}
		s.Count = s.maxStack(p.s.reg)
		s.netID = p.s.stackIDs.Add(1)
		p.requests.work.ui[createdOutputSlot] = s
		return protocol.ItemStackResponseStatusOK, nil
	}
	return protocol.ItemStackResponseStatusInvalidCraftResult, nil
}

// creative reports whether the player is in creative, its own or the
// world's.
func (p *Player) creative() bool {
	mode := p.gameMode
	if mode == GameModeDefault {
		mode = p.s.level.gameMode
	}
	return mode == GameModeCreative
}

// mayRemove applies BDS's rules for taking an item out of a slot: an item
// locked in its slot stays, and armour with the curse of binding stays on
// a player not in creative.
func (p *Player) mayRemove(r slotRef, s ItemStack) bool {
	if lock, _ := s.Tag["minecraft:item_lock"].(uint8); lock == itemLockInSlot {
		return false
	}
	return r.kind != containerArmor || p.creative() || !hasEnchant(s, bindingCurse)
}

// mayPlace applies BDS's rules for putting an item in a slot. Armour and
// offhand need to know which items are wearable where and which go in the
// offhand, which the registry does not have yet: until it does, only
// emptying those slots is allowed.
func (p *Player) mayPlace(r slotRef, s ItemStack) bool {
	if s.Empty() {
		return true
	}
	if lock, _ := s.Tag["minecraft:item_lock"].(uint8); lock == itemLockInSlot {
		return false
	}
	return r.kind != containerArmor && r.kind != containerOffhand
}

// hasEnchant reports whether the item has an enchantment, read from the
// "ench" list BDS saves.
func hasEnchant(s ItemStack, id int16) bool {
	for _, e := range compounds(s.Tag["ench"]) {
		if v, _ := e["id"].(int16); v == id {
			return true
		}
	}
	return false
}
