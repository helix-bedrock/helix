package player

import (
	"maps"
	"slices"

	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Chiseled bookshelves as BDS 1.26.60 (Linux, IDA): ChiseledBookshelfBlock (vtable 0xFBA07A0)
// with its use listener 0xCB57B50, the comparator signal (slot 93, 0xCB58620: the last slot used)
// and the drop on remove (slot 116, 0xCB586C0); ChiseledBookshelfBlockActor (type 51, vtable
// 0xFC00860, saved as "ChiseledBookshelf") with its load 0xD45DFC0, setItem 0xD45CDD0 (only items
// tagged minecraft:bookshelf_books, LastInteractedSlot = slot + 1) and the books_stored update
// 0xD45DD80 (bit i for a book in slot i).
const (
	nameChiseledBookshelf = "minecraft:chiseled_bookshelf"
	bookshelfEntityID     = "ChiseledBookshelf"
	bookshelfSlots        = 6
	bookshelfLastTag      = "LastInteractedSlot"
	bookshelfItemsTag     = "Items"
	stateBooksStored      = "books_stored"
	stateBookshelfFacing  = "direction"
	bookshelfBookTag      = "minecraft:bookshelf_books"
	nameEnchantedBook     = "minecraft:enchanted_book"
	// The front's columns end at these distances across; the top row starts half way up.
	bookshelfFirst, bookshelfMiddle = 0.3125, 0.6875
	bookshelfTop                    = 0.5
	// bookshelfColumns is how many books a row holds.
	bookshelfColumns = 3
)

// Bookshelf is a chiseled bookshelf's block entity: six books, top row left to right then the
// bottom row, as seen from the front. Get it with [BookshelfAt]. Runs: owner of its chunk.
type Bookshelf struct {
	s     *Host
	saved map[string]any
	items [bookshelfSlots]ItemStack
	// last is BDS's LastInteractedSlot: the slot last put in or taken from plus one, 0 for
	// none yet. It is the comparator signal.
	last  int32
	chunk world.ChunkHandle
}

// BookshelfAt returns the chiseled bookshelf block entity at pos. Runs: owner of the chunk.
func BookshelfAt(r *world.Region, pos cube.Pos) (*Bookshelf, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	b, ok := be.(*Bookshelf)
	return b, ok
}

// Item returns what is in slot 0 to 5.
func (b *Bookshelf) Item(slot int) ItemStack {
	if slot < 0 || slot >= bookshelfSlots {
		return ItemStack{}
	}
	return b.items[slot]
}

// LastSlot is the slot last used plus one, 0 if none was: what a comparator reads.
func (b *Bookshelf) LastSlot() int32 { return b.last }

// SetItem puts item in slot 0 to 5 as BDS's setItem does: only a book (an item tagged
// minecraft:bookshelf_books) or nothing, which also makes it the last slot used. The block
// shows its books and clients are told. False if the item may not go there.
func (b *Bookshelf) SetItem(r *world.Region, pos cube.Pos, slot int, item ItemStack) bool {
	if slot < 0 || slot >= bookshelfSlots || (!item.Empty() && !b.s.isShelfBook(item)) {
		return false
	}
	b.items[slot] = item
	b.last = int32(slot) + 1 //nolint:gosec // slot is below 6
	b.chunk.Changed()
	b.showBooks(r, pos)
	r.SendBlockEntity(pos)
	return true
}

// showBooks sets books_stored to the slots that hold a book.
func (b *Bookshelf) showBooks(r *world.Region, pos cube.Pos) {
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	blocks := b.s.reg.Blocks
	states := maps.Clone(blocks.States(block))
	var stored int32
	for i, it := range b.items {
		if !it.Empty() {
			stored |= 1 << i
		}
	}
	states[stateBooksStored] = stored
	if id, ok := blocks.StateID(blocks.Name(block), states); ok && id != block && r.SetBlock(pos, 0, id) {
		r.UpdateNeighbours(pos)
	}
}

// isShelfBook reports whether a chiseled bookshelf takes the item: its tags hold
// minecraft:bookshelf_books (books, enchanted books, books and quills in vanilla).
func (s *Host) isShelfBook(item ItemStack) bool {
	t, ok := s.reg.Items.ByName(item.Name)
	return ok && slices.Contains(t.Tags, bookshelfBookTag)
}

// ComparatorSignal is the last slot used.
func (b *Bookshelf) ComparatorSignal(*world.Region, cube.Pos) int32 { return b.last }

// BindBlockEntity keeps the chunk the bookshelf is in.
func (b *Bookshelf) BindBlockEntity(h world.ChunkHandle) { b.chunk = h }

// KeepBlockEntity keeps the books when only the block's state changes (books_stored).
func (b *Bookshelf) KeepBlockEntity(block uint32) bool {
	return b.s.reg.Blocks.Name(block) == nameChiseledBookshelf
}

// loadBookshelf brings back a saved chiseled bookshelf: Items in slot order, empty ones
// included, and LastInteractedSlot.
func (s *Host) loadBookshelf(saved map[string]any) *Bookshelf {
	b := &Bookshelf{s: s, saved: maps.Clone(saved)}
	list, _ := saved[bookshelfItemsTag].([]any)
	for i, v := range list {
		if i >= bookshelfSlots {
			break
		}
		if m, ok := v.(map[string]any); ok {
			if it := s.loadedItem(m); !it.Empty() && s.isShelfBook(it) {
				b.items[i] = it
			}
		}
	}
	b.last, _ = saved[bookshelfLastTag].(int32)
	delete(b.saved, bookshelfItemsTag)
	delete(b.saved, bookshelfLastTag)
	return b
}

// SaveData is what BDS saves of a chiseled bookshelf.
func (b *Bookshelf) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(b.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = bookshelfEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	items := make([]any, bookshelfSlots)
	for i, it := range b.items {
		items[i] = it.nbt(0, false)
	}
	m[bookshelfItemsTag] = items
	m[bookshelfLastTag] = b.last
	return m
}

// NetworkData is the same, with the books as shown (shownNBT): their pages stay on the server.
func (b *Bookshelf) NetworkData(pos cube.Pos) map[string]any {
	m := b.SaveData(pos)
	m[bookshelfItemsTag] = shownList(b.items[:])
	return m
}

// BlockEntityRemoved drops the books of a bookshelf that went, as a container's contents.
func (b *Bookshelf) BlockEntityRemoved(r *world.Region, pos cube.Pos) {
	b.s.spillSlots(r, b.items[:], blockVec(pos), true, chestPickupDelay)
	b.items = [bookshelfSlots]ItemStack{}
}

// useBookshelf is the use listener: only a player who may open containers, only the front.
// A slot with a book gives it to the player (dropped if it does not fit); an empty slot
// takes one of the held item if it is a book. Clicks on the front off any slot are not taken.
func (p *Player) useBookshelf(r *world.Region, u *BlockUse) bool {
	if !p.abilities.Flag(AbilityOpenContainers) {
		return false
	}
	b, ok := BookshelfAt(r, u.Pos)
	if !ok {
		return false
	}
	facing, _ := p.s.reg.Blocks.States(u.Block)[stateBookshelfFacing].(int32)
	front := direction.Cardinal(facing).Face() //nolint:gosec // the state is 0 to 3
	if cube.Face(u.Face) != front {
		return false
	}
	slot, ok := bookshelfSlot(front, shelfClick(u))
	if !ok {
		return false
	}
	if book := b.items[slot]; !book.Empty() {
		p.takeShelfBook(r, u, b, slot, book)
		return true
	}
	p.putShelfBook(r, u, b, slot)
	return true
}

// takeShelfBook takes the book from slot into the player's inventory, with BDS's pickup sound.
func (p *Player) takeShelfBook(r *world.Region, u *BlockUse, b *Bookshelf, slot int, book ItemStack) {
	b.SetItem(r, u.Pos, slot, ItemStack{})
	if rest := p.Give(book); !rest.Empty() {
		p.Drop(rest)
	}
	sound := packet.SoundEventPickup
	if book.Name == nameEnchantedBook {
		sound = packet.SoundEventPickupEnchanted
	}
	playSound(r, u.Pos, p.s.blockSound(sound, blockCenter(u.Pos), u.Block), nil)
}

// putShelfBook puts one of the held item in slot if it is a book, used up unless the player
// builds instantly, with BDS's insert sound.
func (p *Player) putShelfBook(r *world.Region, u *BlockUse, b *Bookshelf, slot int) {
	inv := &p.inventory
	held := inv.main[inv.held]
	if held.Empty() || !p.s.isShelfBook(held) {
		return
	}
	one := p.s.freshStack(held)
	one.Count = 1
	if !b.SetItem(r, u.Pos, slot, one) {
		return
	}
	if !p.abilities.Flag(AbilityInstantBuild) {
		p.useHeld()
	}
	sound := packet.SoundEventInsert
	if held.Name == nameEnchantedBook {
		sound = packet.SoundEventInsertEnchanted
	}
	playSound(r, u.Pos, p.s.blockSound(sound, blockCenter(u.Pos), u.Block), nil)
}

// bookshelfSlot is the slot a click on the front picks, measured across the front as a
// shelf's: thirds at 0.3125 and 0.6875, the top row from half way up. A click on an edge goes
// to the first slot that holds it, in BDS's order 0 to 5.
func bookshelfSlot(front cube.Face, c [3]float32) (int, bool) {
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
	up := c[1]
	columns := [bookshelfColumns][2]float32{{0, bookshelfFirst}, {bookshelfFirst, bookshelfMiddle}, {bookshelfMiddle, 1}}
	rows := [2][2]float32{{bookshelfTop, 1}, {0, 1}}
	for row, rr := range rows {
		for col, cc := range columns {
			if across >= cc[0] && across <= cc[1] && up >= rr[0] && up <= rr[1] {
				return row*bookshelfColumns + col, true
			}
		}
	}
	return 0, false
}
