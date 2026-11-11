package registry

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// ItemType is an item as the client learns it at join, plus the facts the
// server keeps about it.
type ItemType struct {
	// Name is the namespaced name, such as "minecraft:stick".
	Name string
	// ID is the network ID. Vanilla items keep BDS's; 0 asks Freeze to
	// pick a free one.
	ID int16
	// Version is the item entry version the client is told (BDS sends 0,
	// 1 or 2).
	Version int32
	// ComponentBased marks items the client builds from Components, as
	// it does for data-driven items.
	ComponentBased bool
	// Components is the component data sent with the item, if any.
	Components map[string]any
	// MaxStack is how many fit in one slot. 0 for the block-only entries
	// BDS lists without an item type (lit furnaces, double slabs, ...),
	// which never appear in inventories.
	MaxStack uint8
	// Tags are the item's tags, such as "minecraft:boats".
	Tags []string
	// Wearable marks items a player may wear, in ArmorSlot (0 head, 1
	// chest, 2 legs, 3 feet), as BDS's armour container checks.
	Wearable  bool
	ArmorSlot uint8
	// Offhand marks items the offhand accepts.
	Offhand bool
	// Storage is what the item holds when it holds items itself, as a bundle does
	// (BDS's minecraft:storage_item and minecraft:storage_weight_limit); nil for items
	// that hold none.
	Storage *ItemStorage
	// StorageWeight is what the item weighs inside a storage item on top of what it
	// holds (minecraft:storage_weight_modifier). A storage item with 0 cannot go in
	// another.
	StorageWeight int
}

// ItemStorage is the rules of an item that holds items, from its definition's
// components. Any item can have them; vanilla's are the bundles.
type ItemStorage struct {
	// Slots is how many stacks it holds (max_slots).
	Slots int
	// WeightLimit is the total weight it holds (max_weight_limit): an item weighs
	// 64 / its stack size each, so 64 of a 64-stack item fill it.
	WeightLimit int
	// Nested lets other storage items in (allow_nested_storage_items), if they have a
	// StorageWeight.
	Nested bool
	// Banned items never go in; when Allowed is not empty, only those go in.
	Banned, Allowed []string
}

// RegisterItem adds an item type. Registering a name that exists replaces
// the old type (keeping its ID when the new one has none), and the log
// says who replaced whom.
func (s *Setup) RegisterItem(t ItemType) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if !strings.Contains(t.Name, ":") {
		return fmt.Errorf("registry: item %q: name needs a namespace", t.Name)
	}
	if old, ok := s.s.items[t.Name]; ok {
		s.log.Info("item replaced", "item", t.Name, "by", s.owner, "was", s.s.itemOwners[t.Name])
		if t.ID == 0 {
			t.ID = old.ID
		}
	} else {
		s.s.itemOrder = append(s.s.itemOrder, t.Name)
	}
	delete(s.s.deletedItems, t.Name)
	s.s.items[t.Name] = t
	s.s.itemOwners[t.Name] = s.owner
	return nil
}

// DeleteItem removes an item type from the whole server: clients are not
// told about it. What happens to saved stacks of it is decided with item
// stacks.
func (s *Setup) DeleteItem(name string) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if _, ok := s.s.items[name]; !ok {
		return fmt.Errorf("registry: delete item %s: not registered", name)
	}
	delete(s.s.items, name)
	s.s.deletedItems[name] = true
	s.log.Info("item deleted", "item", name, "by", s.owner)
	return nil
}

// Items is the frozen item table, in registration order (vanilla first,
// in BDS's order). Its methods are safe for use by many goroutines at
// once.
type Items struct {
	types  []ItemType
	byName map[string]int
	byID   map[int16]int
	pk     *packet.ItemRegistry
}

// freezeItems builds the item table. Items without an ID get the next
// free one above every ID in use; BDS's own rule for data-driven items
// is not known yet.
func (s *Setup) freezeItems() (*Items, error) {
	it := &Items{byName: map[string]int{}, byID: map[int16]int{}}
	var next int16 = 1
	for _, name := range s.s.itemOrder {
		if t, ok := s.s.items[name]; ok && t.ID >= next {
			next = t.ID + 1
		}
	}
	for _, name := range s.s.itemOrder {
		t, ok := s.s.items[name]
		if !ok {
			continue // deleted
		}
		if t.ID == 0 {
			if next <= 0 {
				return nil, fmt.Errorf("registry: freeze: no free item ID for %s", name)
			}
			t.ID, next = next, next+1
		}
		if other, dup := it.byID[t.ID]; dup {
			return nil, fmt.Errorf("registry: freeze: items %s and %s share ID %d", it.types[other].Name, name, t.ID)
		}
		t.Components = maps.Clone(t.Components)
		t.Tags = slices.Clone(t.Tags)
		it.byID[t.ID] = len(it.types)
		it.byName[name] = len(it.types)
		it.types = append(it.types, t)
	}
	// The client learns every item from this one packet; it is built once
	// and the same value goes to every player.
	entries := make([]protocol.ItemEntry, len(it.types))
	for i, t := range it.types {
		entries[i] = protocol.ItemEntry{Name: t.Name, RuntimeID: t.ID, ComponentBased: t.ComponentBased, Version: t.Version, Data: t.Components}
	}
	it.pk = &packet.ItemRegistry{Items: entries}
	return it, nil
}

// Count returns the number of item types.
func (it *Items) Count() int { return len(it.types) }

// Each runs fn for every item type, in order.
func (it *Items) Each(fn func(ItemType)) {
	for _, t := range it.types {
		fn(t)
	}
}

// ByName returns an item type by name.
func (it *Items) ByName(name string) (ItemType, bool) {
	i, ok := it.byName[name]
	if !ok {
		return ItemType{}, false
	}
	return it.types[i], true
}

// ByID returns an item type by network ID.
func (it *Items) ByID(id int16) (ItemType, bool) {
	i, ok := it.byID[id]
	if !ok {
		return ItemType{}, false
	}
	return it.types[i], true
}

// RegistryPacket returns the ItemRegistry packet every joining player
// gets. It is shared: do not change it.
func (it *Items) RegistryPacket() *packet.ItemRegistry { return it.pk }
