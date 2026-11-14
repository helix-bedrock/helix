package player

import (
	"maps"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Lecterns as BDS 1.26.60 (Linux, IDA): LecternBlock (vtable 0xFB7F208) with its use listener
// 0xCADC910, attack (slot 82, 0xCADD570) and playerWillDestroy (slot 77, 0xCADD3A0) dropping the
// book, the comparator signal (slot 93, 0xCADD6F0); LecternBlockActor (type 37, vtable
// 0xFBF9E18, saved as "Lectern": hasBook, page, totalPages, book, only while it holds one) with
// setBook 0xD423CD0 (page 0, totalPages from the book) and dropBook 0xD423BF0; the page the
// client turns to comes in LecternUpdate (ServerNetworkHandler: any lectern the player's
// dimension has there, setPageOnServer pulses redstone when the page changes, then
// setTotalPages), and clicking a lectern with a book opens window type 25 (ServerPlayer slot
// 192, 0xA1466E0, openBook: no container manager, so nothing stays open).
const (
	lecternBlock      = "minecraft:lectern"
	lecternEntityID   = "Lectern"
	lecternHasBookTag = "hasBook"
	lecternBookTag    = "book"
	lecternPageTag    = "page"
	lecternPagesTag   = "totalPages"
	bookPagesTag      = "pages"
	nameWritableBook  = "minecraft:writable_book"
	nameWrittenBook   = "minecraft:written_book"
	// lecternSignalTop is the comparator signal's range above 1: the last page gives
	// 1 + lecternSignalTop; lecternFewPages is the page count below which any page does.
	lecternSignalTop = 14
	lecternFewPages  = 4
)

// Lectern is a lectern's block entity: the book on it and the page it is open at. Get it
// with [LecternAt]. Runs: owner of its chunk.
type Lectern struct {
	s     *Host
	saved map[string]any
	book  ItemStack
	page  int32
	pages int32
	chunk world.ChunkHandle
}

// LecternAt returns the lectern block entity at pos. Runs: owner of the chunk.
func LecternAt(r *world.Region, pos cube.Pos) (*Lectern, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	l, ok := be.(*Lectern)
	return l, ok
}

// Book returns the book on the lectern, empty for none.
func (l *Lectern) Book() ItemStack { return l.book }

// Page returns the page the book is open at, and how many pages it has.
func (l *Lectern) Page() (page, pages int32) { return l.page, l.pages }

// SetBook puts book on the lectern, open at the first page; an empty item takes it off.
// Clients are told. Nothing is dropped.
func (l *Lectern) SetBook(r *world.Region, pos cube.Pos, book ItemStack) {
	l.book, l.page, l.pages = book, 0, 0
	if !book.Empty() {
		l.pages = bookPages(book)
	}
	l.chunk.Changed()
	r.SendBlockEntity(pos)
}

// SetPage opens the book at page, as BDS's setPageOnServer: a change of page gives a redstone
// pulse. Runs: owner of the region.
func (l *Lectern) SetPage(r *world.Region, pos cube.Pos, page int32) {
	if l.page == page {
		return
	}
	if fn := l.s.circuits.PulseLectern; fn != nil {
		fn(r, pos)
	}
	l.page = page
	l.chunk.Changed()
}

// DropBook throws the book out of the top of the lectern, as BDS's dropBook: from the block's
// centre, like a container's contents. Runs: owner of the region.
func (l *Lectern) DropBook(r *world.Region, pos cube.Pos) {
	if l.book.Empty() {
		return
	}
	items := []ItemStack{l.book}
	l.SetBook(r, pos, ItemStack{})
	l.s.spillSlots(r, items, blockVec(pos), true, chestPickupDelay)
}

// bookPages is how many pages a book holds: its pages list.
func bookPages(book ItemStack) int32 {
	list, _ := book.Tag[bookPagesTag].([]any)
	return int32(len(list)) //nolint:gosec // a book's pages fit
}

// ComparatorSignal is how far through the book it is open, as BDS: 1 + 14 * page / (pages
// / 2 - 1), 15 for a book of fewer than 4 pages, 0 with no book.
func (l *Lectern) ComparatorSignal(*world.Region, cube.Pos) int32 {
	if l.book.Empty() {
		return 0
	}
	f := float32(lecternSignalTop)
	if l.pages >= lecternFewPages {
		f = lecternSignalTop * (float32(l.page) / (float32(l.pages/2) - 1))
	}
	return int32(f) + 1
}

// BindBlockEntity keeps the chunk the lectern is in.
func (l *Lectern) BindBlockEntity(h world.ChunkHandle) { l.chunk = h }

// KeepBlockEntity keeps the book when only the block's state changes (powered_bit).
func (l *Lectern) KeepBlockEntity(block uint32) bool {
	return l.s.reg.Blocks.Name(block) == lecternBlock
}

// loadLectern brings back a saved lectern.
func (s *Host) loadLectern(saved map[string]any) *Lectern {
	l := &Lectern{s: s, saved: maps.Clone(saved)}
	if has, _ := saved[lecternHasBookTag].(uint8); has != 0 {
		if m, ok := saved[lecternBookTag].(map[string]any); ok {
			l.book = s.loadedItem(m)
		}
		l.page, _ = saved[lecternPageTag].(int32)
		l.pages, _ = saved[lecternPagesTag].(int32)
	}
	for _, k := range [...]string{lecternHasBookTag, lecternBookTag, lecternPageTag, lecternPagesTag} {
		delete(l.saved, k)
	}
	return l
}

// SaveData is what BDS saves of a lectern: the book's keys only while it holds one.
func (l *Lectern) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(l.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = lecternEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	if !l.book.Empty() {
		m[lecternHasBookTag] = uint8(1)
		m[lecternPageTag] = l.page
		m[lecternPagesTag] = l.pages
		m[lecternBookTag] = l.book.nbt(0, false)
	}
	return m
}

// NetworkData is the same: the client draws the book and opens it from this.
func (l *Lectern) NetworkData(pos cube.Pos) map[string]any { return l.SaveData(pos) }

// isLecternBook reports whether a lectern takes the item: a book and quill or a written book.
func isLecternBook(item ItemStack) bool {
	return item.Name == nameWritableBook || item.Name == nameWrittenBook
}

// useLectern is the use listener: only a player who may open containers. A book held on an
// empty lectern goes on it (one, used up unless the player builds instantly) with BDS's book
// sound; a lectern with a book opens it. An empty lectern clicked with anything else does not
// take the click.
func (p *Player) useLectern(r *world.Region, u *BlockUse) bool {
	if !p.abilities.Flag(AbilityOpenContainers) {
		return false
	}
	l, ok := LecternAt(r, u.Pos)
	if !ok {
		return false
	}
	held := p.inventory.main[p.inventory.held]
	if l.book.Empty() {
		if held.Empty() || !isLecternBook(held) {
			return false
		}
		p.putLecternBook(r, u, l, held)
		return true
	}
	p.openLectern(r, u.Pos)
	return true
}

// putLecternBook puts one of held on the lectern.
func (p *Player) putLecternBook(r *world.Region, u *BlockUse, l *Lectern, held ItemStack) {
	one := p.s.freshStack(held)
	one.Count = 1
	l.SetBook(r, u.Pos, one)
	if !p.abilities.Flag(AbilityInstantBuild) {
		p.useHeld()
	}
	playSound(r, u.Pos, soundEvent{name: packet.SoundEventLecternBookPlace, pos: blockVec(u.Pos), extra: -1}, nil)
}

// openLectern opens the lectern's book screen as BDS's ServerPlayer::openBook: only when no
// window is open, the next window ID and ContainerOpen type 25, but no window is kept open.
// The client closes the book without a ContainerClose, so a window kept open here would stop
// the player opening anything again. ContainerOpenEvent still runs and may stop it.
func (p *Player) openLectern(r *world.Region, pos cube.Pos) {
	if p.window.open {
		return
	}
	o := windowOpening{kind: ContainerLectern, window: byte(protocol.ContainerTypeLectern), pos: pos, entity: -1}
	if !p.mayOpen(r, &o) {
		return
	}
	p.window.nextID()
	p.send(&packet.ContainerOpen{
		WindowID: p.window.id, ContainerType: o.window,
		ContainerPosition: blockPos(pos), ContainerEntityUniqueID: o.entity,
	})
	p.flush()
}

// hitLectern is a player starting to break a lectern (BDS's attack) or about to break it
// (playerWillDestroy): a player who may mine knocks the book off.
func (p *Player) hitLectern(r *world.Region, pos cube.Pos) {
	if p.dead() || !p.abilities.Flag(AbilityMine) {
		return
	}
	if l, ok := LecternAt(r, pos); ok {
		l.DropBook(r, pos)
	}
}

// handleLecternUpdate is the client turning a lectern's page: the lectern at the position in
// the player's dimension opens at the page and takes the page count, as BDS does with no
// other check.
func handleLecternUpdate(p *Player, pk *packet.LecternUpdate) {
	if p.entity == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	pos := toCube(pk.Position)
	l, ok := LecternAt(r, pos)
	if !ok {
		return
	}
	l.SetPage(r, pos, int32(pk.Page))
	l.pages = int32(pk.PageCount)
	l.chunk.Changed()
}
