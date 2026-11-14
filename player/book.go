package player

import (
	"github.com/helix-bedrock/protocol/packet"
)

// Books and quills as BDS 1.26.60 (Linux, IDA): the BookEdit handler (ServerNetworkHandler
// slot 180, 0x8CDD7C0 with text filtering off going to 0x8CDE040) takes the item in the
// packet's inventory slot, edits a copy and puts it back in that slot. Each edit only works on
// a book and quill with user data: replace a page (0xC6D8E10), add one (0xC6D93E0, inserting),
// delete one (0xC6D9C20) and swap two (0xC6DA920), each page a compound of "text" and
// "photoname" (0xC909470) in the "pages" list, at most 50 pages; signing (0xC6D7D90 ->
// 0xC6D8430) writes author, xuid, title and generation 0 and turns it into one written book.
// Anything else in the slot becomes nothing, which BDS then does not put back.
//
// BDS takes text of any length, so one client can grow a book by megabytes a page. Helix's own
// rule: an edit whose page text is over bookMaxPageBytes, or whose photo name, title, author or
// XUID is over bookMaxFieldBytes, is ignored. Both are far above what the client's book screen
// lets a player type.
const (
	bookMaxPages      = 50
	bookMaxPageBytes  = 4096
	bookMaxFieldBytes = 256
	bookPageTextTag   = "text"
	bookPagePhotoTag  = "photoname"
	bookAuthorTag     = "author"
	bookTitleTag      = "title"
	bookXUIDTag       = "xuid"
	bookGenerationTag = "generation"
)

// handleBookEdit is the client editing or signing the book in one of its inventory slots.
func handleBookEdit(p *Player, pk *packet.BookEdit) {
	inv := &p.inventory
	slot := int(pk.InventorySlot)
	if slot < 0 || slot >= len(inv.main) {
		return
	}
	book := inv.main[slot]
	if book.Name != nameWritableBook {
		return
	}
	if !bookEditFits(pk) {
		return
	}
	if _, ok := pagesOf(book.Tag); !ok {
		return
	}
	book.Tag = cloneNBT(withPages(book.Tag))
	switch pk.ActionType {
	case packet.BookActionReplacePage:
		replacePage(book.Tag, int(pk.PageNumber), pk.Text, pk.PhotoName)
	case packet.BookActionAddPage:
		addPage(book.Tag, int(pk.PageNumber), pk.Text, pk.PhotoName)
	case packet.BookActionDeletePage:
		deletePage(book.Tag, int(pk.PageNumber))
	case packet.BookActionSwapPages:
		swapPages(book.Tag, int(pk.PageNumber), int(pk.SecondaryPageNumber))
	case packet.BookActionSign:
		book = p.s.signBook(book, pk)
	default:
		return
	}
	inv.main[slot] = book
	p.sendSlot(slot)
	p.sendEquipment()
	p.flush()
}

// bookEditFits reports whether every string of the edit is within Helix's book limits.
func bookEditFits(pk *packet.BookEdit) bool {
	return len(pk.Text) <= bookMaxPageBytes && len(pk.PhotoName) <= bookMaxFieldBytes &&
		len(pk.Title) <= bookMaxFieldBytes && len(pk.Author) <= bookMaxFieldBytes && len(pk.XUID) <= bookMaxFieldBytes
}

// signBook turns the book and quill into one written book by the packet's author, as BDS:
// the author and title are what the client sent, and it is the original (generation 0).
func (s *Host) signBook(book ItemStack, pk *packet.BookEdit) ItemStack {
	tag := book.Tag
	tag[bookAuthorTag] = pk.Author
	tag[bookXUIDTag] = pk.XUID
	tag[bookTitleTag] = pk.Title
	tag[bookGenerationTag] = int32(0)
	return s.freshStack(ItemStack{Name: nameWrittenBook, Count: 1, Tag: tag})
}

// bookPage is one page as BDS saves it.
func bookPage(text, photo string) map[string]any {
	return map[string]any{bookPageTextTag: text, bookPagePhotoTag: photo}
}

// pageList is the book's pages, made empty if there were none.
func pageList(tag map[string]any) []any {
	list, _ := tag[bookPagesTag].([]any)
	return list
}

// padPages fills the book with empty pages up to page, then adds the page: what BDS does
// when an edit names a page past the end.
func padPages(list []any, page int, text, photo string) []any {
	for len(list) < page {
		list = append(list, bookPage("", ""))
	}
	return append(list, bookPage(text, photo))
}

// replacePage sets the text of page, adding empty pages up to it when it is past the end.
func replacePage(tag map[string]any, page int, text, photo string) {
	if page < 0 || page >= bookMaxPages {
		return
	}
	list := pageList(tag)
	if page >= len(list) {
		tag[bookPagesTag] = padPages(list, page, text, photo)
		return
	}
	list[page] = bookPage(text, photo)
	tag[bookPagesTag] = list
}

// addPage inserts a page at page, moving the later ones on; a book of 50 pages takes none.
func addPage(tag map[string]any, page int, text, photo string) {
	if page < 0 || page >= bookMaxPages {
		return
	}
	list := pageList(tag)
	if len(list) >= bookMaxPages {
		return
	}
	if page >= len(list) {
		tag[bookPagesTag] = padPages(list, page, text, photo)
		return
	}
	list = append(list, nil)
	copy(list[page+1:], list[page:])
	list[page] = bookPage(text, photo)
	tag[bookPagesTag] = list
}

// deletePage takes page out, the later ones moving back.
func deletePage(tag map[string]any, page int) {
	list := pageList(tag)
	if page < 0 || page >= len(list) {
		return
	}
	tag[bookPagesTag] = append(list[:page:page], list[page+1:]...)
}

// swapPages swaps two pages that are both in the book.
func swapPages(tag map[string]any, a, b int) {
	list := pageList(tag)
	if a < 0 || b < 0 || a >= len(list) || b >= len(list) {
		return
	}
	list[a], list[b] = list[b], list[a]
}
