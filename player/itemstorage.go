package player

import (
	"maps"
	"slices"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Storage items are items that hold items, as a bundle does. Any item whose definition
// has minecraft:storage_item is one ([registry.ItemType.Storage]); the rules below are
// BDS 1.26.60's, read from its DynamicContainerValidation, and the packets are what BDS
// sends a real client (runtime dump, 2026-11-11).
//
// What one holds lives in its stack (ItemStack.stored), so it moves, drops, saves and is
// worked on by requests with the stack. The client names one by its dynamic container ID
// (BDS's "bundle_id" tag), which the server gives it once it is in a player's slots.
const (
	// storedContentTag and storedWeightTag are how BDS saves what a storage item holds
	// and its weight in the item's tag; storageIDTag is the ID tag the client gets
	// instead.
	storedContentTag = "storage_item_component_content"
	storedWeightTag  = "bundle_weight"
	storageIDTag     = "bundle_id"
	// storageWindow is the window BDS sends storage items' slots in (the registry
	// window).
	storageWindow = 125
	// storageFullWeight is what a full stack of any item weighs: one item weighs this
	// divided by its stack size.
	storageFullWeight = 64
)

// readStored takes a loaded item's saved contents out of its tag.
func (s *ItemStack) readStored() {
	list, hasList := s.Tag[storedContentTag].([]any)
	_, hasWeight := s.Tag[storedWeightTag]
	_, hasID := s.Tag[storageIDTag]
	if !hasList && !hasWeight && !hasID {
		return
	}
	tag := maps.Clone(s.Tag)
	delete(tag, storedContentTag)
	delete(tag, storedWeightTag)
	delete(tag, storageIDTag)
	if len(tag) == 0 {
		tag = nil
	}
	s.Tag = tag
	for i, e := range list {
		m, _ := e.(map[string]any)
		it := itemFromNBT(m)
		slot := i
		if v, ok := m["Slot"].(uint8); ok {
			slot = int(v)
		}
		for len(s.stored) <= slot {
			s.stored = append(s.stored, ItemStack{})
		}
		s.stored[slot] = it
	}
}

// savedTag is the tag as BDS saves it: a storage item's contents and weight added, its
// runtime ID left out.
func (s ItemStack) savedTag() map[string]any {
	if s.storedEmpty() {
		return s.Tag
	}
	tag := maps.Clone(s.Tag)
	if tag == nil {
		tag = map[string]any{}
	}
	list := make([]any, 0, len(s.stored))
	for i, it := range s.stored {
		if !it.Empty() {
			list = append(list, it.nbt(i, true))
		}
	}
	tag[storedContentTag] = list
	tag[storedWeightTag] = s.storedWeight
	return tag
}

// networkTag is the tag as the client gets it: a storage item's ID added, what it holds
// sent apart, in its own window. Book pages go only with a book in the player's own
// slots (own), never with items inside another item (a shulker box's): the client
// reads a book only from its own slots, and its tooltips show titles, not pages. A box
// of full books is about a megabyte with them, so a chest of such boxes would be one
// packet of 28 MB. Helix's own rule, beyond BDS: the server's items keep everything.
func (s ItemStack) networkTag(own bool) map[string]any {
	if s.storageID == 0 && (own || !hasPages(s.Tag)) && !holdsItems(s.Tag) {
		return s.Tag
	}
	tag := maps.Clone(s.Tag)
	if tag == nil {
		tag = map[string]any{}
	}
	if s.storageID != 0 {
		tag[storageIDTag] = s.storageID - 1
	}
	if !own {
		delete(tag, bookPagesTag)
	}
	if list, ok := tag[shulkerItemsTag].([]any); ok {
		tag[shulkerItemsTag] = shownItems(list)
	}
	return tag
}

// hasPages reports whether a tag holds book pages.
func hasPages(tag map[string]any) bool {
	_, ok := tag[bookPagesTag]
	return ok
}

// holdsItems reports whether a tag holds items, as a shulker box item's does.
func holdsItems(tag map[string]any) bool {
	_, ok := tag[shulkerItemsTag].([]any)
	return ok
}

// shownItems is a list of saved items as the client gets it inside another item: each
// without book pages, and so on down.
func shownItems(list []any) []any {
	out := make([]any, len(list))
	for i, v := range list {
		if m, ok := v.(map[string]any); ok {
			out[i] = shownCompound(m)
			continue
		}
		out[i] = v
	}
	return out
}

// shownCompound is one saved item without its book pages, the rest shared.
func shownCompound(m map[string]any) map[string]any {
	t, ok := m["tag"].(map[string]any)
	if !ok || !hasPages(t) && !holdsItems(t) {
		return m
	}
	tag := maps.Clone(t)
	delete(tag, bookPagesTag)
	if list, ok := tag[shulkerItemsTag].([]any); ok {
		tag[shulkerItemsTag] = shownItems(list)
	}
	out := maps.Clone(m)
	out["tag"] = tag
	return out
}

// storedEmpty reports whether the stack holds no items.
func (s ItemStack) storedEmpty() bool {
	for _, it := range s.stored {
		if !it.Empty() {
			return false
		}
	}
	return true
}

// Stored returns a copy of what a storage item holds, one entry a slot, empty slots
// included. Nil for an item that holds nothing.
func (s ItemStack) Stored() []ItemStack { return slices.Clone(s.stored) }

// withStored returns the stack holding items, its saved weight worked out.
func (s ItemStack) withStored(reg *registry.Registry, items []ItemStack) ItemStack {
	s.stored = items
	s.storedWeight = int32(storedWeight(reg, items)) //nolint:gosec // at most a few hundred
	return s
}

// itemWeight is what a stack weighs in a storage item, as BDS's sub_C4AFF60: a storage
// item weighs its StorageWeight plus what it holds, any other item its count times
// 64 / its stack size (whole numbers).
func itemWeight(reg *registry.Registry, s ItemStack) int {
	if s.Empty() {
		return 0
	}
	t, ok := reg.Items.ByName(s.Name)
	if !ok {
		return 0
	}
	if t.Storage != nil {
		return t.StorageWeight + storedWeight(reg, s.stored)
	}
	if t.MaxStack == 0 {
		return 0
	}
	return int(s.Count) * (storageFullWeight / int(t.MaxStack))
}

// storedWeight is what a storage item's slots weigh together.
func storedWeight(reg *registry.Registry, items []ItemStack) int {
	w := 0
	for _, it := range items {
		w += itemWeight(reg, it)
	}
	return w
}

// storeLimit is how many of s may go in a storage slot holding inSlot when the storage
// item already holds current weight, as BDS's DynamicContainerValidation: none of an
// empty, banned or not allowed item, nor of a storage item unless nesting is allowed and
// it has a weight, nor more than the weight left (the weight of one each); at most the
// slot's room.
func storeLimit(reg *registry.Registry, rules *registry.ItemStorage, inSlot, s ItemStack, current int) int {
	if rules == nil || s.Empty() || slices.Contains(rules.Banned, s.Name) {
		return 0
	}
	if len(rules.Allowed) > 0 && !slices.Contains(rules.Allowed, s.Name) {
		return 0
	}
	t, ok := reg.Items.ByName(s.Name)
	if !ok {
		return 0
	}
	if t.Storage != nil && (!rules.Nested || t.StorageWeight == 0) {
		return 0
	}
	one := s
	one.Count = 1
	w := itemWeight(reg, one)
	left := rules.WeightLimit - current
	if w > left {
		return 0
	}
	room := int(t.MaxStack)
	if !inSlot.Empty() {
		if !inSlot.stacksWith(s) {
			return 0
		}
		room -= int(inSlot.Count)
	}
	if w == 0 {
		return max(room, 0)
	}
	return max(min(room, left/w), 0)
}

// storageWork is a request's copy of what one storage item holds.
type storageWork struct {
	id    int32
	rules *registry.ItemStorage
	items []ItemStack
}

// storageOwner finds the stack with dynamic container id in the player's items: its
// inventory, offhand, UI slots (the cursor) and the open container's slots.
func storageOwner(inv *playerItems, id int32) *ItemStack {
	for _, l := range [][]ItemStack{inv.main[:], inv.offhand[:], inv.ui[:], inv.armor[:], inv.open} {
		for i := range l {
			if l[i].storageID == id+1 && !l[i].Empty() {
				return &l[i]
			}
		}
	}
	return nil
}

// storageSlot resolves a slot of the storage item the client names by its dynamic
// container ID, in the request's copy.
func (p *Player) storageSlot(info protocol.StackRequestSlotInfo) (slotRef, bool) {
	r := slotRef{container: info.Container.ContainerID, slot: info.Slot, kind: protocol.ContainerDynamic}
	id, ok := info.Container.DynamicContainerID.Value()
	if !ok {
		return r, false
	}
	w := p.requests.storageWork(p.s.reg, int32(id)) //nolint:gosec // IDs are small
	if w == nil || int(info.Slot) >= len(w.items) {
		return r, false
	}
	r.dynamic = w.id + 1
	r.stack = &w.items[info.Slot]
	return r, true
}

// storageWork returns the request's copy of storage item id's slots, making it from the
// work items on first use. Nil if the player has no such item.
func (st *requestState) storageWork(reg *registry.Registry, id int32) *storageWork {
	for i := range st.storage {
		if st.storage[i].id == id {
			return &st.storage[i]
		}
	}
	owner := storageOwner(&st.work, id)
	if owner == nil {
		return nil
	}
	t, ok := reg.Items.ByName(owner.Name)
	if !ok || t.Storage == nil {
		return nil
	}
	items := make([]ItemStack, max(t.Storage.Slots, len(owner.stored)))
	copy(items, owner.stored)
	st.storage = append(st.storage, storageWork{id: id, rules: t.Storage, items: items})
	return &st.storage[len(st.storage)-1]
}

// storageAccepts applies the storage rules to putting s in a storage slot of the
// request: the slot's own stack is replaced unless s joins it.
func (p *Player) storageAccepts(r slotRef, s ItemStack) bool {
	var w *storageWork
	for i := range p.requests.storage {
		if p.requests.storage[i].id+1 == r.dynamic {
			w = &p.requests.storage[i]
		}
	}
	if w == nil {
		return false
	}
	reg := p.s.reg
	current, inSlot := storedWeight(reg, w.items), *r.stack
	if !inSlot.Empty() && !inSlot.stacksWith(s) {
		current -= itemWeight(reg, inSlot)
		inSlot = ItemStack{}
	}
	return int(s.Count) <= storeLimit(reg, w.rules, inSlot, s, current)
}

// commitStorage writes the request's storage copies into their items in the work copy.
// False if a storage item ended up inside itself, which refuses the request.
func (p *Player) commitStorage() bool {
	st := &p.requests
	for _, w := range st.storage {
		for _, it := range w.items {
			if it.storageID == w.id+1 {
				return false
			}
		}
		owner := storageOwner(&st.work, w.id)
		if owner == nil {
			// The item left the player's slots in the same request.
			return false
		}
		*owner = owner.withStored(p.s.reg, w.items)
	}
	return true
}

// storageStack is what a storage slot a request touched holds once it is done.
func (p *Player) storageStack(r slotRef) ItemStack {
	owner := storageOwner(&p.inventory, r.dynamic-1)
	if owner == nil || int(r.slot) >= len(owner.stored) {
		return ItemStack{}
	}
	return owner.stored[r.slot]
}

// syncStorage gives storage items in the player's own slots their dynamic container IDs
// and sends the client what they hold, as BDS's per-player DynamicContainerTracker: all
// slots once (InventoryContent in window 125), then each slot that changes
// (InventorySlot), each naming the container (63 and the ID) and the storage item.
func (p *Player) syncStorage() {
	reg := p.s.reg
	invs := p.inventories()
	var seen []int32
	for _, inv := range []*Inventory{invs.main, invs.armour, invs.offhand, invs.ui} {
		for i := range inv.Size() {
			s := inv.at(i)
			if s == nil || s.Empty() {
				continue
			}
			t, ok := reg.Items.ByName(s.Name)
			if !ok || t.Storage == nil {
				continue
			}
			if s.storageID == 0 {
				s.storageID = p.s.storageIDs.Add(1)
				inv.sendSlot(p, i, false)
			}
			id := s.storageID - 1
			seen = append(seen, id)
			p.sendStorage(*s, id, t.Storage.Slots)
		}
	}
	for id := range p.storageSent {
		if !slices.Contains(seen, id) {
			delete(p.storageSent, id)
		}
	}
}

// syncStorageIDs gives storage items in the player's own slots their dynamic container
// IDs, without sending anything.
func (p *Player) syncStorageIDs() {
	reg := p.s.reg
	inv := &p.inventory
	for _, l := range [][]ItemStack{inv.main[:], inv.armor[:], inv.offhand[:], inv.ui[:]} {
		for i := range l {
			if l[i].Empty() || l[i].storageID != 0 {
				continue
			}
			if t, ok := reg.Items.ByName(l[i].Name); ok && t.Storage != nil {
				l[i].storageID = p.s.storageIDs.Add(1)
			}
		}
	}
}

// sendStorage sends what changed in one storage item since the client last got it.
func (p *Player) sendStorage(owner ItemStack, id int32, slots int) {
	reg := p.s.reg
	items := make([]ItemStack, max(slots, len(owner.stored)))
	copy(items, owner.stored)
	name := protocol.FullContainerName{ContainerID: protocol.ContainerDynamic, DynamicContainerID: protocol.Option(uint32(id))} //nolint:gosec // IDs are small
	holder := owner
	holder.netID, holder.stored = 0, nil
	holder.Count = 1
	storage := holder.network(reg)
	if p.storageSent == nil {
		p.storageSent = map[int32][]ItemStack{}
	}
	last, ok := p.storageSent[id]
	if !ok {
		content := make([]protocol.ItemInstance, len(items))
		for i, it := range items {
			content[i] = it.networkShown(reg)
		}
		p.send(&packet.InventoryContent{WindowID: storageWindow, Content: content, Container: name, StorageItem: storage})
	} else {
		for i, it := range items {
			if i < len(last) && last[i].netID == it.netID && last[i].Count == it.Count && last[i].Name == it.Name {
				continue
			}
			p.send(&packet.InventorySlot{
				WindowID: storageWindow, Slot: uint32(i), //nolint:gosec // slots are small
				Container: protocol.Option(name), StorageItem: protocol.Option(storage), NewItem: it.networkShown(reg),
			})
		}
	}
	p.storageSent[id] = items
}

// spillStored drops what a storage item on the ground holds where it is, as BDS's item
// actor does when one is destroyed (sub_BD628A0). How BDS throws them is not read yet:
// they land where the item was, still.
func (i *ItemActor) spillStored(e *world.Entity) {
	if e == nil || i.stack.storedEmpty() {
		return
	}
	r := e.Region()
	if r == nil {
		return
	}
	i.s.spillSlots(r, i.stack.stored, e.Position(), false, 0)
}
