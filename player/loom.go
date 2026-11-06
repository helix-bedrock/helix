package player

import (
	"maps"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
)

// BDS 1.26.60's loom (CraftHandlerLoom). The research note is private.
const (
	loomBlock = "minecraft:loom"
	// The loom's slots of the UI container: the banner, the dye, the pattern item.
	loomBannerSlot   = 9
	loomDyeSlot      = 10
	loomMaterialSlot = 11
)

// LoomInput is what a loom is asked to do: the pattern's ID (as banners save it), how
// many banners at once, and the three slots.
type LoomInput struct {
	Pattern               string
	Times                 uint8
	Banner, Dye, Material ItemStack
}

// LoomFunc is what a loom makes; no item refuses the request. BDS's never consumes the
// pattern item: one of the banner and one dye go per banner made.
type LoomFunc func(p *Player, in LoomInput) ItemStack

// LoomEvent fires when a loom's result has passed the Loom system. Cancel refuses it;
// changing Result changes what the player gets, and the player is sent its items after
// the response. Runs: owner of the player's entity.
type LoomEvent struct {
	Cancellable
	Player *Player
	In     LoomInput
	Result ItemStack
}

// OnLoom listens to players using looms.
func OnLoom(s *Setup, fn func(e *LoomEvent), p Priority) error {
	return addListener(s, &s.h.game.loomEv, fn, p)
}

// useLoom is what clicking a loom does.
func (p *Player) useLoom(r *world.Region, u *BlockUse) bool {
	p.openStation(r, u, ContainerLoom, byte(protocol.ContainerTypeLoom))
	return true
}

// defaultBanner is BDS's isDefaultBanner: a banner whose Type, if it is an int, is 0, so
// an ominous banner takes no pattern.
func defaultBanner(s ItemStack) bool {
	if s.Name != bannerItem {
		return false
	}
	t, ok := tagInt(s.Tag, bannerTypeTag)
	return !ok || t == BannerNormal
}

// vanillaLoom is BDS's loom: a known pattern, a normal banner with fewer than the most
// patterns, a dye, enough of both for every banner made, and the pattern's own item in
// the third slot, or nothing there for a pattern drawn with a dye alone. The banner keeps
// its data; its patterns are rebuilt from their Pattern and Color and the new one goes on
// top in the dye's colour.
func vanillaLoom(p *Player, in LoomInput) ItemStack {
	t := p.s.banners()
	pat, ok := t.Pattern(in.Pattern)
	if !ok || !defaultBanner(in.Banner) {
		return ItemStack{}
	}
	old, _ := in.Banner.Tag[bannerPatternsTag].([]any)
	if len(old) >= t.MaxPatterns {
		return ItemStack{}
	}
	color, isDye := t.Dyes[in.Dye.Name]
	if in.Dye.Empty() || !isDye || in.Dye.Count < in.Times || in.Banner.Count < in.Times {
		return ItemStack{}
	}
	if pat.ItemType < 0 {
		if !in.Material.Empty() {
			return ItemStack{}
		}
	} else if kind, isPattern := t.PatternItems[in.Material.Name]; in.Material.Empty() || !isPattern || kind != pat.ItemType {
		return ItemStack{}
	}
	layers := make([]any, 0, len(old)+1)
	for _, x := range old {
		m, isCompound := x.(map[string]any)
		if !isCompound {
			continue
		}
		id, _ := m[bannerPatternTag].(string)
		c, _ := tagInt(m, bannerColorTag)
		layers = append(layers, map[string]any{bannerPatternTag: id, bannerColorTag: c})
	}
	layers = append(layers, map[string]any{bannerPatternTag: pat.ID, bannerColorTag: int32(color)})
	result := in.Banner
	result.Count = in.Times
	result.Tag = maps.Clone(in.Banner.Tag)
	if result.Tag == nil {
		result.Tag = map[string]any{}
	}
	result.Tag[bannerPatternsTag] = layers
	return result
}

// craftLoom is BDS's loom action: the result is worked out by the server from the
// slots it holds, and the banner and the dye are consumed once per banner.
func (p *Player) craftLoom(a *protocol.CraftLoomRecipeStackRequestAction) (uint8, []slotRef) {
	st := &p.requests
	c := &st.craft
	if p.window.kind != ContainerLoom {
		return protocol.ItemStackResponseStatusMissingInputSparseContainer, nil
	}
	if c.active {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	ui := &st.work.ui
	in := LoomInput{Pattern: a.Pattern, Times: a.TimesCrafted, Banner: ui[loomBannerSlot], Dye: ui[loomDyeSlot], Material: ui[loomMaterialSlot]}
	g := p.s.game
	result := g.sys.Loom(p, in)
	if result.Empty() {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	if g.loomEv.Has() {
		e := LoomEvent{Player: p, In: in, Result: result}
		g.loomEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
		}
		if result = e.Result; result.Empty() {
			return protocol.ItemStackResponseStatusInvalidCraftResultItem, nil
		}
	}
	c.active, c.times = true, 1
	if in.Times > 0 {
		c.slots = append(c.slots,
			expectedSlot{container: protocol.ContainerLoomInput, slot: loomBannerSlot, item: in.Banner, left: in.Times},
			expectedSlot{container: protocol.ContainerLoomDye, slot: loomDyeSlot, item: in.Dye, left: in.Times},
		)
	}
	c.results = append(c.results, craftResult{stack: result, left: int32(result.Count)})
	// As at a table: the client is sent the item the server made.
	st.refresh = true
	return protocol.ItemStackResponseStatusOK, nil
}
