package registry

import (
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/helix-bedrock/protocol/packet"
)

// Live holds a packet the client gets at join that may change while the
// server runs: the creative inventory or the recipes. Each change builds
// a new packet and swaps it in at once; readers keep the one they got.
// Listeners hear about every change, so the server can send the new
// packet to players already online. Safe for use by many goroutines.
type Live[P any] struct {
	cur   atomic.Pointer[P]
	clone func(*P) *P

	mu        sync.Mutex
	listeners map[int]func(*P)
	next      int
}

func newLive[P any](p *P, clone func(*P) *P) *Live[P] {
	l := &Live[P]{clone: clone, listeners: map[int]func(*P){}}
	l.cur.Store(p)
	return l
}

// Packet returns the current packet. It is shared: do not change it.
func (l *Live[P]) Packet() *P { return l.cur.Load() }

// Update changes the packet. edit gets a copy whose lists may be replaced,
// reordered, grown or shrunk; replace list elements instead of changing
// the values nested inside them, which are shared with the old packet.
// Listeners run after the swap, on the calling goroutine.
func (l *Live[P]) Update(edit func(p *P)) {
	l.mu.Lock()
	p := l.clone(l.cur.Load())
	edit(p)
	l.cur.Store(p)
	fns := make([]func(*P), 0, len(l.listeners))
	for _, k := range slices.Sorted(maps.Keys(l.listeners)) {
		fns = append(fns, l.listeners[k])
	}
	l.mu.Unlock()
	for _, fn := range fns {
		fn(p)
	}
}

// OnChange registers fn for every later change. It returns a function
// that removes it.
func (l *Live[P]) OnChange(fn func(p *P)) (remove func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := l.next
	l.next++
	l.listeners[k] = fn
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.listeners, k)
	}
}

func cloneCreative(p *packet.CreativeContent) *packet.CreativeContent {
	return &packet.CreativeContent{Groups: slices.Clone(p.Groups), Items: slices.Clone(p.Items)}
}

func cloneCrafting(p *packet.CraftingData) *packet.CraftingData {
	return &packet.CraftingData{
		ShapedRecipes:                slices.Clone(p.ShapedRecipes),
		ShapelessRecipes:             slices.Clone(p.ShapelessRecipes),
		MultiRecipes:                 slices.Clone(p.MultiRecipes),
		UserDataShapelessRecipes:     slices.Clone(p.UserDataShapelessRecipes),
		ShapelessChemistryRecipes:    slices.Clone(p.ShapelessChemistryRecipes),
		ShapedChemistryRecipes:       slices.Clone(p.ShapedChemistryRecipes),
		SmithingTransformRecipes:     slices.Clone(p.SmithingTransformRecipes),
		SmithingTrimRecipes:          slices.Clone(p.SmithingTrimRecipes),
		PotionRecipes:                slices.Clone(p.PotionRecipes),
		PotionContainerChangeRecipes: slices.Clone(p.PotionContainerChangeRecipes),
		MaterialReducers:             slices.Clone(p.MaterialReducers),
		ClearRecipes:                 p.ClearRecipes,
	}
}

// EditCreative changes the creative inventory at boot; Registry.Creative
// changes it later. Vanilla's is BDS's own.
func (s *Setup) EditCreative(edit func(p *packet.CreativeContent)) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if s.s.creative == nil {
		s.s.creative = &packet.CreativeContent{}
	}
	p := cloneCreative(s.s.creative)
	edit(p)
	s.s.creative = p
	if s.owner != s.s.vanillaOwner {
		s.log.Info("creative inventory edited", "by", s.owner)
	}
	return nil
}

// EditCrafting changes the recipes at boot; Registry.Crafting changes them
// later. Vanilla's are BDS's own.
func (s *Setup) EditCrafting(edit func(p *packet.CraftingData)) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if s.s.crafting == nil {
		s.s.crafting = &packet.CraftingData{ClearRecipes: true}
	}
	p := cloneCrafting(s.s.crafting)
	edit(p)
	s.s.crafting = p
	if s.owner != s.s.vanillaOwner {
		s.log.Info("recipes edited", "by", s.owner)
	}
	return nil
}
