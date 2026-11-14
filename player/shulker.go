package player

import (
	"strings"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Shulker box rules from BDS 1.26.60. Its block actor is a chest's (ctor 0xD3E5380) with these
// differences.
const (
	shulkerEntityID  = "ShulkerBox"
	shulkerFacingTag = "facing"
	shulkerItemsTag  = "Items"
	// shulkerDefaultFacing is the side a new box opens to until it is placed: up.
	shulkerDefaultFacing = uint8(cube.FaceUp)
	// shulkerLidSpeed is how far the lid moves per tick, half a chest's.
	shulkerLidSpeed = 0.05
	// shulkerLidHeight is how high in the block it opens to the lid reaches (0xD3E66E0).
	shulkerLidHeight = 0.5
)

// isShulkerBox reports whether a block or item is a shulker box of any colour. These are what
// BDS keeps out of a box (its list at 0xFF1A6C8, read by 0xD3E6C60 and 0xC9DFCF0).
func isShulkerBox(name string) bool { return strings.HasSuffix(name, "shulker_box") }

// lidSpeed is how far the lid moves per tick.
func (c *Chest) lidSpeed() float32 {
	if c.shulker {
		return shulkerLidSpeed
	}
	return chestLidSpeed
}

// shulkerCanOpen is BDS's check for a box (0xD45BDD0 with the box of 0xD3E66E0): the lower half
// of the block the lid opens into must be free of collision boxes of any block but this one.
// Whichever way it opens, that box is the same shape.
func (c *Chest) shulkerCanOpen(r *world.Region, pos cube.Pos) bool {
	at := pos
	if c.facing <= uint8(cube.FaceEast) {
		at = pos.Side(cube.Face(c.facing))
	}
	// A block below can reach up into it, as a fence does.
	for x := at[0] - 1; x <= at[0]+1; x++ {
		for y := at[1] - 1; y <= at[1]; y++ {
			for z := at[2] - 1; z <= at[2]+1; z++ {
				b := cube.Pos{x, y, z}
				if b == pos {
					continue
				}
				block, ok := r.Block(b, 0)
				if !ok {
					continue
				}
				for _, box := range c.s.boxesAt(r, b, block) {
					if shulkerLidHits(box, float32(x-at[0]), float32(y-at[1]), float32(z-at[2])) {
						return false
					}
				}
			}
		}
	}
	return true
}

// shulkerLidHits reports whether a box of a block x, y, z from the lid's block overlaps the lid.
func shulkerLidHits(box registry.Box, x, y, z float32) bool {
	return box[3]+x > 0 && box[0]+x < 1 && box[5]+z > 0 && box[2]+z < 1 && box[4]+y > 0 && box[1]+y < shulkerLidHeight
}

// placedFrom is what BDS's block item does to a placed box (0xC9D0BE0): it opens towards the
// clicked side, and takes the item's contents and name. Runs: owner of its chunk.
func (c *Chest) placedFrom(r *world.Region, pl *Placement) {
	if pl.Face >= int32(cube.FaceDown) && pl.Face <= int32(cube.FaceEast) {
		c.facing = uint8(pl.Face) //nolint:gosec // checked just here
	}
	c.changed = true
	for _, item := range compounds(pl.Item.Tag[shulkerItemsTag]) {
		if slot, ok := item["Slot"].(uint8); ok && int(slot) < chestSize {
			c.items[slot] = withID(itemFromNBT(item), c.s.nextStackID)
		}
	}
	if display, ok := pl.Item.Tag["display"].(map[string]any); ok {
		if name, ok := display["Name"].(string); ok {
			if c.saved == nil {
				c.saved = map[string]any{}
			}
			c.saved["CustomName"] = name
		}
	}
	r.SaveBlockEntity(pl.Pos)
}

// shulkerDrops gives the box dropped from pos what the box held, and its name. Call it before
// the block goes. Other drops are left as they are. Runs: owner of the chunk of pos.
func (s *Host) shulkerDrops(r *world.Region, pos cube.Pos, items []ItemStack) []ItemStack {
	c := s.chestAt(r, pos)
	if c == nil || !c.shulker {
		return items
	}
	c.Warm(pos)
	held := []any{}
	for i, it := range c.items {
		if !it.Empty() {
			held = append(held, it.nbt(i, true))
		}
	}
	name, _ := c.saved["CustomName"].(string)
	for i := range items {
		if !isShulkerBox(items[i].Name) {
			continue
		}
		if len(held) > 0 {
			items[i] = withTag(items[i], shulkerItemsTag, packedItems(held))
		}
		if name != "" {
			items[i] = withTag(items[i], "display", map[string]any{"Name": name})
		}
	}
	return items
}

// shulkerInShulker reports whether the running request would put a box in the open box. Only
// slots it changed count, so a box already inside from an old save can still be taken out.
func (p *Player) shulkerInShulker() bool {
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
	for i := range min(len(work), chestSize) {
		if isShulkerBox(work[i].Name) && work[i].netID != c.items[i].netID {
			return true
		}
	}
	return false
}
