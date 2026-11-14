package player

import (
	"maps"
	"strings"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Shelves as BDS 1.26.60 (Linux, IDA): ShelfBlock's use listener 0xD51F060 and its single
// slot swap 0xD5224A0, ShelfBlockActor (type 59, saved as "Shelf") with its save 0xD3E4D70
// and load 0xD3E49A0, and the drop on destroy 0xD522160.
//
// Built: a shelf that is not powered swaps the held item with the slot clicked on its front;
// a powered one swaps its whole row with the hotbar (shelfrow.go). Not built: the
// comparator signal.
const (
	shelfEntityID    = "Shelf"
	shelfSlots       = 3
	shelfItemsTag    = "Items"
	stateShelfPower  = "powered_bit"
	stateShelfFacing = "minecraft:cardinal_direction"
	// The band of the front a click must land in, and the edges between the slots.
	shelfLow, shelfHigh   = 0.25, 0.6875
	shelfFirst, shelfLast = 0.3125, 0.6875
)

// isShelfBlock reports whether name is one of the wooden shelves.
func isShelfBlock(name string) bool { return strings.HasSuffix(name, "_shelf") }

// Shelf is a shelf's block entity: the three items on it, left to right as seen from the
// front. Get it with [ShelfAt]. Runs: owner of its chunk.
type Shelf struct {
	s     *Host
	saved map[string]any
	items [shelfSlots]ItemStack
	chunk world.ChunkHandle
}

// ShelfAt returns the shelf block entity at pos. Runs: owner of the chunk.
func ShelfAt(r *world.Region, pos cube.Pos) (*Shelf, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	sh, ok := be.(*Shelf)
	return sh, ok
}

// Item returns what is in slot 0 to 2.
func (sh *Shelf) Item(slot int) ItemStack {
	if slot < 0 || slot >= shelfSlots {
		return ItemStack{}
	}
	return sh.items[slot]
}

// SetItem puts item in slot 0 to 2; an empty item clears it. Clients are told.
func (sh *Shelf) SetItem(r *world.Region, pos cube.Pos, slot int, item ItemStack) {
	if slot < 0 || slot >= shelfSlots {
		return
	}
	sh.items[slot] = item
	sh.chunk.Changed()
	r.SendBlockEntity(pos)
}

// BindBlockEntity keeps the chunk the shelf is in.
func (sh *Shelf) BindBlockEntity(h world.ChunkHandle) { sh.chunk = h }

// KeepBlockEntity keeps the items when the shelf only changes state (power, its row).
func (sh *Shelf) KeepBlockEntity(block uint32) bool {
	return isShelfBlock(sh.s.reg.Blocks.Name(block))
}

// loadShelf brings back a saved shelf: Items holds the slots in order.
func (s *Host) loadShelf(saved map[string]any) *Shelf {
	sh := &Shelf{s: s, saved: maps.Clone(saved)}
	list, _ := saved[shelfItemsTag].([]any)
	for i, v := range list {
		if i >= shelfSlots {
			break
		}
		if m, ok := v.(map[string]any); ok {
			sh.items[i] = s.loadedItem(m)
		}
	}
	delete(sh.saved, shelfItemsTag)
	return sh
}

// SaveData is what BDS saves of a shelf: every slot in order, empty ones too, without slot
// numbers.
func (sh *Shelf) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(sh.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = shelfEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	items := make([]any, shelfSlots)
	for i, it := range sh.items {
		items[i] = it.nbt(0, false)
	}
	m[shelfItemsTag] = items
	return m
}

// NetworkData is the same, with the items as shown (shownNBT): the client draws the items
// from it.
func (sh *Shelf) NetworkData(pos cube.Pos) map[string]any {
	m := sh.SaveData(pos)
	m[shelfItemsTag] = shownList(sh.items[:])
	return m
}

// BlockEntityRemoved drops what was on a destroyed shelf, as a container's contents.
func (sh *Shelf) BlockEntityRemoved(r *world.Region, pos cube.Pos) {
	sh.s.spillSlots(r, sh.items[:], blockVec(pos), true, chestPickupDelay)
	sh.items = [shelfSlots]ItemStack{}
}

// useShelf is the use listener: only a player who may open containers, only the front, and
// for now only an unpowered shelf. The slot clicked swaps with the held item; nothing
// happens if both are empty or the held item is locked in its slot. In creative the held
// item stays and a copy goes on the shelf (read as BDS's instant build check, not confirmed).
func (p *Player) useShelf(r *world.Region, u *BlockUse) bool {
	if !p.abilities.Flag(AbilityOpenContainers) {
		return false
	}
	sh, ok := ShelfAt(r, u.Pos)
	if !ok {
		return false
	}
	states := p.s.reg.Blocks.States(u.Block)
	front, ok := cardinalFace(states[stateShelfFacing])
	if !ok || cube.Face(u.Face) != front {
		return false
	}
	if powered, _ := states[stateShelfPower].(uint8); powered != 0 {
		return p.swapShelfRow(r, u.Pos, states)
	}
	slot, ok := shelfSlot(front, shelfClick(u))
	if !ok {
		return false
	}
	return p.swapShelf(r, u.Pos, sh, slot)
}

// swapShelf swaps the held item with slot, with BDS's sounds: place_item when one side was
// empty, single_swap when both had an item.
func (p *Player) swapShelf(r *world.Region, pos cube.Pos, sh *Shelf, slot int) bool {
	inv := &p.inventory
	held, on := inv.main[inv.held], sh.items[slot]
	if (held.Empty() && on.Empty()) || itemLocked(held) {
		return false
	}
	sound := packet.SoundEventSingleItemSwap
	if held.Empty() || on.Empty() {
		sound = packet.SoundEventPlaceItem
	}
	if p.creative() {
		sh.SetItem(r, pos, slot, withID(held, p.s.nextStackID))
		sound = packet.SoundEventPlaceItem
	} else {
		sh.SetItem(r, pos, slot, held)
		inv.main[inv.held] = on
		p.sendHeld()
	}
	playSound(r, pos, soundEvent{name: sound, pos: blockVec(pos), extra: -1}, nil)
	p.flush()
	return true
}

// cardinalFace is the face a minecraft:cardinal_direction value points to.
func cardinalFace(v any) (cube.Face, bool) {
	switch v {
	case "north":
		return cube.FaceNorth, true
	case "south":
		return cube.FaceSouth, true
	case "west":
		return cube.FaceWest, true
	case "east":
		return cube.FaceEast, true
	}
	return 0, false
}

// shelfClick is where on its block the click landed, 0 to 1 on each axis; a click sent in
// world coordinates is moved into the block.
func shelfClick(u *BlockUse) [3]float32 {
	var c [3]float32
	for i := range c {
		c[i] = u.ClickPos[i]
		if c[i] < 0 || c[i] > 1 {
			c[i] -= float32(u.Pos[i])
		}
	}
	return c
}

// shelfSlot is the slot a click on the front picks, as BDS measures across the front (1-x
// facing north, x south, z west, 1-z east): the first 0.3125, the middle up to 0.6875, the
// last the rest, between 0.25 and 0.6875 high.
func shelfSlot(front cube.Face, c [3]float32) (int, bool) {
	var across float32
	switch front {
	case cube.FaceNorth:
		across = 1 - c[0]
	case cube.FaceSouth:
		across = c[0]
	case cube.FaceWest:
		across = c[2]
	case cube.FaceEast:
		across = 1 - c[2]
	default:
		return 0, false
	}
	if c[1] < shelfLow || c[1] > shelfHigh || across < 0 || across > 1 {
		return 0, false
	}
	switch {
	case across <= shelfFirst:
		return 0, true
	case across <= shelfLast:
		return 1, true
	}
	return 2, true
}
