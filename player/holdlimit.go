package player

import "github.com/helix-bedrock/protocol/nbt"

// What one shulker box or one storage item (a bundle) may hold is capped by its saved size:
// Helix's own rule, beyond BDS, which takes any amount. A box of bundles of full books would
// otherwise be gigabytes in one chest: every holder of items (chests, carts, boats, ender
// chests, players, items on the ground) then holds at most its slots times this. An item that
// would take a box or bundle over it is refused; one already over it, from an older save,
// may still be emptied.

// heldMaxBytes is the most a shulker box's or a bundle's items may take saved. A box of
// /chestbomb's books (27 books of 50 pages) is about 1 MB.
const heldMaxBytes = 2 << 20

// heldBytes is about what items take saved, together.
func heldBytes(items []ItemStack) int {
	n := 0
	for _, it := range items {
		n += itemBytes(it)
	}
	return n
}

// itemFixedBytes is about what a saved item takes besides its name and tag: the Count,
// Damage, Slot and WasPickedUp tags and the compound's own bytes.
const itemFixedBytes = 48

// itemBytes is about what an item takes saved: its name, its tag and what it holds.
func itemBytes(s ItemStack) int {
	if s.Empty() {
		return 0
	}
	return itemFixedBytes + len(s.Name) + valueBytes(s.Tag) + heldBytes(s.stored)
}

// valueBytes is about what an NBT value takes encoded, its tag type and name not counted.
func valueBytes(v any) int {
	switch v := v.(type) {
	case map[string]any:
		n := 1 // the end tag
		for k, e := range v {
			n += 3 + len(k) + valueBytes(e)
		}
		return n
	case []any:
		n := 5 // element type and length
		for _, e := range v {
			n += valueBytes(e)
		}
		return n
	case []map[string]any:
		n := 5
		for _, e := range v {
			n += valueBytes(e)
		}
		return n
	case string:
		return 2 + len(v)
	case nbt.Raw:
		return v.Len()
	case *heldItems:
		return v.size
	case []byte:
		return 4 + len(v)
	case []int32:
		return 4 + 4*len(v)
	case []int64:
		return 4 + 8*len(v)
	case uint8, int8, bool:
		return 1
	case int16:
		return 2
	case int32, float32:
		return 4
	}
	return 8
}

// heldOver reports whether items now held would take a box or bundle over heldMaxBytes when
// it held before: only growth past the cap is refused.
func heldOver(now, before []ItemStack) bool {
	n := heldBytes(now)
	return n > heldMaxBytes && n > heldBytes(before)
}

// shulkerTooFull reports whether the running request would fill the open shulker box past
// heldMaxBytes.
func (p *Player) shulkerTooFull() bool {
	work := p.requests.work.open
	if len(work) == 0 {
		return false
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	c := p.windowChest(r)
	if c == nil || !c.shulker {
		return false
	}
	return heldOver(work[:min(len(work), chestSize)], c.items[:])
}

// shulkerRoomFor reports whether one of s still fits the box's cap. Other chests take any.
func (c *Chest) shulkerRoomFor(s ItemStack) bool {
	if !c.shulker {
		return true
	}
	one := s
	one.Count = 1
	return heldBytes(c.items[:])+itemBytes(one) <= heldMaxBytes
}
