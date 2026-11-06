package player

import (
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/helix-bedrock/protocol"
)

// BDS 1.26.60's shield + banner recipe (ShieldRecipe: matches 0xC497C50, assemble 0xC496C90,
// recipe ID shield_banner_recipeId). The research note is private.
const shieldItem = "minecraft:shield"

// shieldBannerRecipe is the UUID BDS sends the recipe under in CraftingData.
var shieldBannerRecipe = uuid.UUID{15: 0xc8}

// The pattern and colour BDS draws on a shield made with an ominous banner.
const (
	ominousPattern = "ill"
	ominousColor   = 15
)

// ShieldBannerFunc is what a shield and a banner crafted together make; no item refuses
// the craft. The grid's items are read from the server's slots.
type ShieldBannerFunc func(p *Player, grid []ItemStack) ItemStack

// ShieldBannerEvent fires when a shield and banner craft has passed the ShieldBanner
// system. Cancel refuses it; changing Result changes what the player gets. Runs: owner of
// the player's entity.
type ShieldBannerEvent struct {
	Cancellable
	Player         *Player
	Shield, Banner ItemStack
	Result         ItemStack
}

// OnShieldBanner listens to players putting banners on shields.
func OnShieldBanner(s *Setup, fn func(e *ShieldBannerEvent), p Priority) error {
	return addListener(s, &s.h.game.shieldBannerEv, fn, p)
}

// shieldAndBanner finds the one shield and one banner of a grid as BDS's matches does:
// other items are not looked at, a shield already carrying a Base is refused, and so is a
// banner with more than BDS's 6 patterns.
func shieldAndBanner(grid []ItemStack) (shield, banner ItemStack, ok bool) {
	shields, banners := 0, 0
	for _, s := range grid {
		switch {
		case s.Empty():
		case s.Name == shieldItem:
			shields++
			if _, has := s.Tag[bannerBaseTag]; has {
				return ItemStack{}, ItemStack{}, false
			}
			shield = s
		case s.Name == bannerItem:
			if l, _ := s.Tag[bannerPatternsTag].([]any); len(l) > shieldBannerPatterns {
				return ItemStack{}, ItemStack{}, false
			}
			banners++
			banner = s
		}
	}
	return shield, banner, shields == 1 && banners == 1
}

// shieldBannerPatterns is the most patterns BDS's recipe takes from a banner.
const shieldBannerPatterns = 6

// vanillaShieldBanner is BDS's assemble: one shield keeping its data, with the banner's
// data added and its colour as Base; an ominous banner gives only the illager pattern in
// white.
func vanillaShieldBanner(_ *Player, grid []ItemStack) ItemStack {
	shield, banner, ok := shieldAndBanner(grid)
	if !ok {
		return ItemStack{}
	}
	result := shield
	result.Count = 1
	result.Tag = maps.Clone(shield.Tag)
	if result.Tag == nil {
		result.Tag = map[string]any{}
	}
	if t, _ := tagInt(banner.Tag, bannerTypeTag); t == BannerOminous {
		result.Tag[bannerPatternsTag] = []any{map[string]any{bannerPatternTag: ominousPattern, bannerColorTag: int32(ominousColor)}}
	} else {
		maps.Copy(result.Tag, banner.Tag)
	}
	result.Tag[bannerBaseTag] = int32(banner.Meta)
	return result
}

// craftSpecial is a craft of one of BDS's special recipes, worked out in code. Only the
// shield and banner one is built; the rest are refused as unknown.
func (p *Player) craftSpecial(a *protocol.CraftRecipeStackRequestAction, recipe uuid.UUID) (uint8, []slotRef) {
	if recipe != shieldBannerRecipe {
		return protocol.ItemStackResponseStatusFailedToFindRecipeByNetId, nil
	}
	st := &p.requests
	c := &st.craft
	start, size, _, ok := p.craftGrid()
	if !ok {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	if c.active || len(c.results) != 0 {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	if a.NumberOfCrafts == 0 {
		return protocol.ItemStackResponseStatusInvalidNumCrafts, nil
	}
	grid := slices.Clone(st.work.ui[start : start+size])
	g := p.s.game
	result := g.sys.ShieldBanner(p, grid)
	if result.Empty() {
		return protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems, nil
	}
	if g.shieldBannerEv.Has() {
		shield, banner, _ := shieldAndBanner(grid)
		e := ShieldBannerEvent{Player: p, Shield: shield, Banner: banner, Result: result}
		g.shieldBannerEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
		}
		if result = e.Result; result.Empty() {
			return protocol.ItemStackResponseStatusInvalidCraftResultItem, nil
		}
	}
	c.active, c.times = true, a.NumberOfCrafts
	for i := range size {
		if s := grid[i]; !s.Empty() {
			c.slots = append(c.slots, expectedSlot{container: protocol.ContainerCraftingInput, slot: byte(start + i), item: s, left: a.NumberOfCrafts}) //nolint:gosec // slots are small
		}
	}
	c.results = append(c.results, craftResult{stack: result, left: int32(result.Count) * int32(a.NumberOfCrafts)})
	// As at a table: the client is sent the item the server made.
	st.refresh = true
	return protocol.ItemStackResponseStatusOK, nil
}
