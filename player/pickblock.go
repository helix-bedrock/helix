package player

import (
	"strings"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// How far from a block BDS lets a player pick it, measured to the block's corner.
const (
	pickReach         = 8.7
	pickReachCreative = 14.0
)

// unpickable blocks answer no to BDS's pick check.
var unpickable = map[string]bool{
	"minecraft:air": true, "minecraft:fire": true, "minecraft:soul_fire": true,
}

// pickedAs is the item of each block that looks like another's: what is lit or powered gives
// the plain item. BDS's block classes override their pick item for these; the names here are
// read from Helix's block list, not from each override.
var pickedAs = map[string]string{
	standingBanner: bannerItem, wallBanner: bannerItem,
	"minecraft:powered_repeater":           "minecraft:repeater",
	"minecraft:powered_comparator":         "minecraft:comparator",
	"minecraft:lit_furnace":                "minecraft:furnace",
	"minecraft:lit_smoker":                 "minecraft:smoker",
	"minecraft:lit_blast_furnace":          "minecraft:blast_furnace",
	"minecraft:lit_redstone_lamp":          "minecraft:redstone_lamp",
	"minecraft:lit_redstone_ore":           "minecraft:redstone_ore",
	"minecraft:lit_deepslate_redstone_ore": "minecraft:deepslate_redstone_ore",
	"minecraft:unlit_redstone_torch":       "minecraft:redstone_torch",
	"minecraft:frosted_ice":                "minecraft:ice",
}

// pickedItem is the item picking block gives: one, as BDS makes it.
func (s *Host) pickedItem(block uint32) (ItemStack, bool) {
	name := s.reg.Blocks.Name(block)
	if unpickable[name] {
		return ItemStack{}, false
	}
	item := name
	if as, ok := pickedAs[name]; ok {
		item = as
	} else if strings.Contains(name, "double_") && strings.HasSuffix(name, "_slab") {
		item = strings.Replace(name, "double_", "", 1)
	} else {
		for planted, placed := range plantedBlocks {
			if placed == name {
				item = planted
				break
			}
		}
	}
	if _, ok := s.reg.Items.ByName(item); !ok {
		return ItemStack{}, false
	}
	stack := ItemStack{Name: item, Count: 1}
	if id, ok := s.reg.Blocks.Block(item); ok {
		stack.Block = map[string]any{"name": item, "states": s.reg.Blocks.States(id), "version": s.reg.Blocks.Version(id)}
	}
	return stack, true
}

// findPicked is where the inventory holds s: the held slot first, as BDS looks, then in order.
func (inv *playerItems) findPicked(s ItemStack) int {
	matches := func(o ItemStack) bool { return !o.Empty() && o.Name == s.Name && o.Meta == s.Meta }
	if matches(inv.main[inv.held]) {
		return int(inv.held)
	}
	for i := range inv.main {
		if matches(inv.main[i]) {
			return i
		}
	}
	return -1
}

// lockedInSlot says the stack may not leave its slot.
func lockedInSlot(s ItemStack) bool {
	lock, _ := s.Tag["minecraft:item_lock"].(uint8)
	return lock == itemLockInSlot
}

// pick is BDS's pick into the inventory. What the inventory holds comes to the hotbar: selected
// there, else swapped with an empty hotbar slot, else with the held slot. Only creative makes
// the item: in an empty hotbar slot, else in the held slot, whose stack moves to an empty slot
// or is lost if there is none. It reports whether anything changed.
func (inv *playerItems) pick(s ItemStack, creative bool) bool {
	free := inv.freeSlot()
	hotbarFree := free >= 0 && free < hotbarSize
	at := inv.findPicked(s)
	switch {
	case at >= 0 && at < hotbarSize:
		inv.held = uint8(at) //nolint:gosec // below hotbarSize
		return true
	case at >= 0:
		to := int(inv.held)
		if hotbarFree {
			to = free
		} else if lockedInSlot(inv.main[to]) {
			return false
		}
		inv.main[at], inv.main[to] = inv.main[to], inv.main[at]
		inv.held = uint8(to) //nolint:gosec // below hotbarSize
		return true
	case !creative:
		return false
	}
	to := int(inv.held)
	if hotbarFree {
		to = free
	} else {
		if lockedInSlot(inv.main[to]) {
			return false
		}
		if free >= 0 {
			inv.main[free] = inv.main[to]
		}
	}
	inv.main[to] = s
	inv.held = uint8(to) //nolint:gosec // below hotbarSize
	return true
}

// handleBlockPick is BDS's handler for picking a block. The hotbar slot and the request for the
// block's data are not used: BDS reads the slot from the player, and a block's data on an item
// is not built. BDS also sends GuiDataPickItem with the item's name, which needs names Helix
// does not have; the client names the item it selects anyway.
func handleBlockPick(p *Player, pk *packet.BlockPickRequest) {
	if p.entity == nil || p.dead() {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	pos := cube.Pos{int(pk.Position[0]), int(pk.Position[1]), int(pk.Position[2])}
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	stack, ok := p.s.pickedItem(block)
	if !ok {
		return
	}
	// A banner comes with its colour and patterns, as BDS's asItemInstance gives it.
	stack = bannerDrops(r, pos, []ItemStack{stack})[0]
	creative := p.creative()
	reach := float32(pickReach)
	if creative {
		reach = pickReachCreative
	}
	at := p.entity.Position()
	dx, dy, dz := at[0]-float32(pos[0]), at[1]-float32(pos[1]), at[2]-float32(pos[2])
	if dx*dx+dy*dy+dz*dz > reach*reach {
		return
	}
	stack = p.s.freshStack(stack)
	inv := &p.inventory
	if !inv.pick(stack, creative) {
		return
	}
	p.Inventory().sendAll(p, false)
	p.send(&packet.PlayerHotBar{SelectedHotBarSlot: uint32(inv.held), WindowID: protocol.WindowIDInventory, SelectHotBarSlot: true})
	p.sendEquipment()
}
