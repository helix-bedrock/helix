package player

import (
	"maps"

	"github.com/helix-bedrock/protocol/nbt"
)

// Items inside an item (a shulker box item's "Items") are kept as one nbt.Raw, in the disk
// encoding, from the moment the item is read: Helix's own rule, for memory. A box of full books
// is about a megabyte of text; as maps and strings it takes half as much again and gives the
// collector millions of pointers to scan, and every chunk save encoded it again. As a Raw it is
// one pointer-free byte slice about its size on disk, a save copies its bytes, and copies of
// the tag share it (it is never changed). What reads the items decodes it (compounds); the
// client gets it decoded without book pages (shownItemsOf).

// packItems is the tag with its item list turned into a Raw, or the tag as it was when it has
// no list. The tag passed is not changed.
func packItems(tag map[string]any) map[string]any {
	list, ok := tag[shulkerItemsTag].([]any)
	if !ok {
		return tag
	}
	raw, err := nbt.MarshalRaw(list, nbt.LittleEndian)
	if err != nil {
		return tag
	}
	tag = maps.Clone(tag)
	tag[shulkerItemsTag] = raw
	return tag
}

// packedItems is a list of saved items as an item's tag keeps it, or the list itself if it
// cannot be encoded.
func packedItems(list []any) any {
	raw, err := nbt.MarshalRaw(list, nbt.LittleEndian)
	if err != nil {
		return list
	}
	return raw
}

// unpacked is v decoded when it is a Raw, else v.
func unpacked(v any) any {
	if r, ok := v.(nbt.Raw); ok {
		out, err := r.Value()
		if err != nil {
			return nil
		}
		return out
	}
	return v
}

// shownItemsOf is an item list of a tag as the client gets it: without book pages at any depth.
func shownItemsOf(v any) (any, bool) {
	switch l := v.(type) {
	case []any:
		return shownItems(l), true
	case nbt.Raw:
		out, err := l.ValueWithout(isPagesTag)
		if err != nil {
			return []any{}, true
		}
		return out, true
	}
	return nil, false
}

// isPagesTag reports whether a tag name is a book's pages.
func isPagesTag(name string) bool { return name == bookPagesTag }
