package player

import (
	"slices"
	"strings"
)

// Adventure mode's item lists, as BDS 1.26.60 keeps them on an item (ItemStackBase's
// can_place_on and can_destroy block lists, set by /give's components and saved as the item's
// CanPlaceOn and CanDestroy, lists of block names). An adventure player may use the item on a
// block it lists and break a block it lists, unless an allow or deny block says otherwise.
const (
	canPlaceOnKey = "CanPlaceOn"
	canDestroyKey = "CanDestroy"
)

// CanPlaceOn is the blocks an adventure player may use the item on.
func (s ItemStack) CanPlaceOn() []string { return s.blockList(canPlaceOnKey) }

// CanDestroy is the blocks an adventure player may break holding the item.
func (s ItemStack) CanDestroy() []string { return s.blockList(canDestroyKey) }

// WithCanPlaceOn returns the stack with its can_place_on list set to blocks; none removes it.
func (s ItemStack) WithCanPlaceOn(blocks []string) ItemStack {
	return s.withBlockList(canPlaceOnKey, blocks)
}

// WithCanDestroy returns the stack with its can_destroy list set to blocks; none removes it.
func (s ItemStack) WithCanDestroy(blocks []string) ItemStack {
	return s.withBlockList(canDestroyKey, blocks)
}

// blockList reads a saved list of block names.
func (s ItemStack) blockList(key string) []string {
	switch v := s.saved[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if name, ok := e.(string); ok {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

// withBlockList sets a saved list of block names. saved is replaced, never changed in place:
// copies of the stack share it.
func (s ItemStack) withBlockList(key string, blocks []string) ItemStack {
	saved := make(map[string]any, len(s.saved)+1)
	for k, v := range s.saved {
		saved[k] = v
	}
	if len(blocks) == 0 {
		delete(saved, key)
	} else {
		list := make([]any, len(blocks))
		for i, b := range blocks {
			list[i] = b
		}
		saved[key] = list
	}
	s.saved = saved
	return s
}

// listsBlock reports whether list names the block called name; names without a namespace
// are minecraft's.
func listsBlock(list []string, name string) bool {
	return slices.ContainsFunc(list, func(b string) bool {
		if !strings.Contains(b, ":") {
			b = "minecraft:" + b
		}
		return b == name
	})
}
