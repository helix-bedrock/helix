package player

import (
	"maps"

	"github.com/helix-bedrock/world/cube"
)

// A shulker box placed from an item whose contents are stored (helditems.go) does not read
// them: it goes cold at once, keeping each slot's count from the contents as shown, and
// reads them only when something uses them (opened, a hopper, a comparator needs no read).
// Broken before that, it gives its item the same stored contents, so a box carried, placed
// and picked up again never reads them at all. Helix's own rule, beyond BDS.

// holdFrom makes the box cold on stored contents h.
func (c *Chest) holdFrom(h *heldItems) {
	c.items = [chestSize]ItemStack{}
	c.coldSlots = [chestSize]coldSlot{}
	if shown, ok := shownItemsOf(h); ok {
		for _, item := range compounds(shown) {
			if slot, isSlot := item["Slot"].(uint8); isSlot && int(slot) < chestSize {
				c.coldSlots[slot] = c.coldSlotOf(itemFromNBT(item))
			}
		}
	}
	c.boxHeld, c.cold, c.idle = h, true, 0
	c.coldGen++
}

// warmFromHeld reads the items of a box cold on stored contents, reporting whether it was
// one. On a read error the box stays cold, so its saves keep the reference.
func (c *Chest) warmFromHeld() bool {
	h := c.boxHeld
	if h == nil {
		return false
	}
	list := c.s.heldList(h)
	if list == nil {
		return true
	}
	for _, item := range compounds(list) {
		if slot, ok := item["Slot"].(uint8); ok && int(slot) < chestSize {
			c.items[slot] = c.s.loadedItem(item)
		}
	}
	c.boxHeld, c.cold, c.idle, c.coldSlots = nil, false, 0, [chestSize]coldSlot{}
	return true
}

// heldSaveData is SaveData of a box cold on stored contents: the reference in place of the
// items.
func (c *Chest) heldSaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(c.saved)
	if m == nil {
		m = map[string]any{}
	}
	maps.Copy(m, c.NetworkData(pos))
	if key, v := c.boxHeld.saved(); key != "" {
		m[key] = v
	}
	m["Findable"] = boolByte(c.findable)
	return m
}

// dropsHeld gives the box items dropped what a box cold on stored contents holds, unread,
// and its name.
func (c *Chest) dropsHeld(items []ItemStack) []ItemStack {
	name, _ := c.saved["CustomName"].(string)
	for i := range items {
		if !isShulkerBox(items[i].Name) {
			continue
		}
		items[i] = withTag(items[i], shulkerItemsTag, c.boxHeld)
		if name != "" {
			items[i] = withTag(items[i], "display", map[string]any{"Name": name})
		}
	}
	return items
}
