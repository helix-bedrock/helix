package helix

import (
	"maps"
	"reflect"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
)

// ItemStack is a stack of items the server holds. The client only ever
// sees copies; what it says it holds is never read back.
type ItemStack struct {
	// Name is the item's name, such as "minecraft:stick"; empty for no item.
	Name string
	// Meta is the item's aux value (BDS's "Damage").
	Meta int16
	// Count is how many items are in the stack.
	Count uint8
	// Block is a block item's block, as BDS saves it ({name, states,
	// version}); nil for other items.
	Block map[string]any
	// Tag is the item's user data (BDS's "tag").
	Tag map[string]any

	// saved keeps the other fields of the saved item (CanPlaceOn,
	// CanDestroy, WasPickedUp, ...), written back as they were.
	saved map[string]any
	// netID is the stack's network ID, which the client names it by.
	netID int32
}

// Empty reports whether the slot holds nothing.
func (s ItemStack) Empty() bool { return s.Count == 0 || s.Name == "" }

// stacksWith reports whether o can join s in one slot, as BDS matches
// stacks: same item, aux value, block and data.
func (s ItemStack) stacksWith(o ItemStack) bool {
	return s.Name == o.Name && s.Meta == o.Meta && reflect.DeepEqual(s.Block, o.Block) && reflect.DeepEqual(s.Tag, o.Tag)
}

// itemFromNBT reads an item as BDS saves it. An empty compound or a zero
// count is an empty slot.
func itemFromNBT(m map[string]any) ItemStack {
	name, _ := m["Name"].(string)
	count, _ := m["Count"].(uint8)
	if name == "" || count == 0 {
		return ItemStack{}
	}
	s := ItemStack{Name: name, Count: count, saved: map[string]any{}}
	s.Meta, _ = m["Damage"].(int16)
	s.Block, _ = m["Block"].(map[string]any)
	s.Tag, _ = m["tag"].(map[string]any)
	for k, v := range m {
		switch k {
		case "Name", "Count", "Damage", "Block", "tag", "Slot":
		default:
			s.saved[k] = v
		}
	}
	return s
}

// nbt returns the item as BDS saves it: an empty slot in a list with slot
// numbers is BDS's empty item, elsewhere an empty compound.
func (s ItemStack) nbt(slot int, withSlot bool) map[string]any {
	if s.Empty() {
		if !withSlot {
			return map[string]any{}
		}
		return map[string]any{"Count": uint8(0), "Damage": int16(0), "Name": "", "Slot": uint8(slot), "WasPickedUp": uint8(0)} //nolint:gosec // slots are small
	}
	m := maps.Clone(s.saved)
	if m == nil {
		m = map[string]any{"WasPickedUp": uint8(0)}
	}
	m["Name"], m["Count"], m["Damage"] = s.Name, s.Count, s.Meta
	if s.Block != nil {
		m["Block"] = s.Block
	}
	if s.Tag != nil {
		m["tag"] = s.Tag
	}
	if withSlot {
		m["Slot"] = uint8(slot) //nolint:gosec // slots are small
	}
	return m
}

// network returns the stack as the client gets it. An item the registry
// no longer has is sent as nothing.
func (s ItemStack) network(reg *registry.Registry) protocol.ItemInstance {
	if s.Empty() {
		return protocol.ItemInstance{}
	}
	t, ok := reg.Items.ByName(s.Name)
	if !ok {
		return protocol.ItemInstance{}
	}
	st := protocol.ItemStack{
		ItemType: protocol.ItemType{NetworkID: int32(t.ID), MetadataValue: uint32(uint16(s.Meta))},
		Count:    uint16(s.Count), NBTData: s.Tag,
	}
	if s.Block != nil {
		name, _ := s.Block["name"].(string)
		states, _ := s.Block["states"].(map[string]any)
		if id, ok := reg.Blocks.StateID(name, states); ok {
			st.BlockRuntimeID = int32(reg.Blocks.Hash(id)) //nolint:gosec // hashes are sent as int32
		}
	}
	return protocol.ItemInstance{StackNetworkID: s.netID, Stack: st}
}

// itemFromNetwork reads a stack the server itself sent (the creative
// inventory), never one a client sent.
func itemFromNetwork(reg *registry.Registry, st protocol.ItemStack) ItemStack {
	t, ok := reg.Items.ByID(int16(st.NetworkID)) //nolint:gosec // network IDs are int16
	if !ok || st.Count == 0 {
		return ItemStack{}
	}
	s := ItemStack{Name: t.Name, Meta: int16(st.MetadataValue), Count: uint8(min(st.Count, 255)), Tag: maps.Clone(st.NBTData)} //nolint:gosec // aux values are int16
	if st.BlockRuntimeID != 0 {
		if id, ok := reg.Blocks.HashToRuntimeID(uint32(st.BlockRuntimeID)); ok { //nolint:gosec // hashes are sent as int32
			s.Block = map[string]any{"name": reg.Blocks.Name(id), "states": reg.Blocks.States(id), "version": reg.Blocks.Version(id)}
		}
	}
	return s
}

// maxStack returns how many of the stack's item fit in one slot.
func (s ItemStack) maxStack(reg *registry.Registry) uint8 {
	if t, ok := reg.Items.ByName(s.Name); ok {
		return t.MaxStack
	}
	return 0
}
