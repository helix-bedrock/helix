package player

import (
	"maps"
	"slices"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// PlayerRecipes edits which of the server's recipes one player has, during one
// Player.EditRecipes call. It only takes recipes away: use registry.EditRecipes to add
// or change recipes for everyone.
//
// The rules are kept by name, not as a list, so they keep working when the server's
// recipes change: a recipe added later that makes a removed item is removed too. They
// last until the player leaves and are not saved.
type PlayerRecipes struct {
	rules recipeRules
}

// recipeRules are what a player's recipes leave out. Once published they do not change.
type recipeRules struct {
	all                bool
	ids, items, blocks map[string]struct{}
}

// empty reports whether the rules leave nothing out.
func (r *recipeRules) empty() bool {
	return !r.all && len(r.ids) == 0 && len(r.items) == 0 && len(r.blocks) == 0
}

// hides reports whether the rules leave out a recipe.
func (r *recipeRules) hides(id, block string, output []string) bool {
	if _, ok := r.ids[id]; ok {
		return true
	}
	if _, ok := r.blocks[block]; ok {
		return true
	}
	return slices.ContainsFunc(output, func(item string) bool {
		_, ok := r.items[item]
		return ok
	})
}

// Remove takes away the recipes with an ID, of any kind that has one.
func (r *PlayerRecipes) Remove(id string) { r.rules.ids[id] = struct{}{} }

// RemoveFor takes away every shaped, shapeless and smithing recipe that makes an item.
func (r *PlayerRecipes) RemoveFor(item string) { r.rules.items[item] = struct{}{} }

// ClearBlock takes away every recipe made at a block, such as "smithing_table". For a
// crafting table that leaves the special recipes; Clear takes those too.
func (r *PlayerRecipes) ClearBlock(block string) { r.rules.blocks[block] = struct{}{} }

// Clear takes away every recipe of every kind, as registry's Clear does for everyone.
func (r *PlayerRecipes) Clear() { r.rules.all = true }

// Reset gives the player all of the server's recipes again.
func (r *PlayerRecipes) Reset() {
	r.rules = recipeRules{ids: map[string]struct{}{}, items: map[string]struct{}{}, blocks: map[string]struct{}{}}
}

// EditRecipes changes which recipes this player has. The player is sent its new list
// once edit returns, and the server refuses a recipe the player does not have, whatever
// the client asks for. Runs: anywhere.
func (p *Player) EditRecipes(edit func(r *PlayerRecipes)) {
	p.applyRecipeEdit(edit)
	p.sendRecipes()
}

// applyRecipeEdit publishes the rules an edit makes from a copy of the current ones.
func (p *Player) applyRecipeEdit(edit func(r *PlayerRecipes)) {
	p.recipeMu.Lock()
	defer p.recipeMu.Unlock()
	e := PlayerRecipes{}
	e.Reset()
	if old := p.recipeRules.Load(); old != nil {
		e.rules.all = old.all
		maps.Copy(e.rules.ids, old.ids)
		maps.Copy(e.rules.items, old.items)
		maps.Copy(e.rules.blocks, old.blocks)
	}
	edit(&e)
	if e.rules.empty() {
		p.recipeRules.Store(nil)
		return
	}
	p.recipeRules.Store(&e.rules)
}

// sendRecipes sends the player its recipes. The lock keeps two sends in order, so the
// last one a client gets is the newest list. Runs: anywhere.
func (p *Player) sendRecipes() {
	p.recipeMu.Lock()
	defer p.recipeMu.Unlock()
	p.sendNow(p.recipesPacket())
}

// recipesPacket is the recipes packet this player gets: the server's own, shared by
// everyone, unless the player has rules.
func (p *Player) recipesPacket() *packet.CraftingData {
	if v := p.recipeView(); v != nil {
		return v.pk
	}
	return p.s.reg.Crafting.Packet()
}

// SendRecipes sends every player online its recipes, after the server's changed.
// Runs: anywhere.
func (h *Host) SendRecipes() {
	for _, p := range h.global.online() {
		p.sendRecipes()
	}
}

// recipeView is one player's rules applied to one recipes packet.
type recipeView struct {
	rules  *recipeRules
	source *packet.CraftingData
	pk     *packet.CraftingData
	// hidden are the network IDs a player may not use. Clearing hides everything,
	// which rules.all says without a list.
	hidden map[uint32]struct{}
}

// recipeView returns the player's view, nil for a player with no rules. It is rebuilt
// only when the rules or the server's recipes change.
func (p *Player) recipeView() *recipeView {
	rules := p.recipeRules.Load()
	if rules == nil {
		return nil
	}
	source := p.s.reg.Crafting.Packet()
	if v := p.recipeCache.Load(); v != nil && v.rules == rules && v.source == source {
		return v
	}
	v := p.buildRecipeView(rules, source)
	p.recipeCache.Store(v)
	return v
}

// buildRecipeView filters a packet. Network IDs stay as they are, so the table of the
// server's recipes works for every player. Kinds with no ID stay unless cleared; the
// client must drop what it had, so the packet always says to clear.
func (p *Player) buildRecipeView(rules *recipeRules, source *packet.CraftingData) *recipeView {
	v := &recipeView{rules: rules, source: source, hidden: map[uint32]struct{}{}}
	if rules.all {
		v.pk = &packet.CraftingData{ClearRecipes: true}
		return v
	}
	hide := func(netID uint32, id, block string, output ...protocol.ItemStack) bool {
		names := make([]string, 0, len(output))
		for _, st := range output {
			t, _ := p.s.reg.Items.ByID(int16(st.NetworkID)) //nolint:gosec // network IDs are int16
			names = append(names, t.Name)
		}
		if !rules.hides(id, block, names) {
			return false
		}
		v.hidden[netID] = struct{}{}
		return true
	}
	pk := *source
	pk.ClearRecipes = true
	pk.ShapedRecipes = slices.DeleteFunc(slices.Clone(source.ShapedRecipes), func(r protocol.ShapedRecipe) bool {
		return hide(r.RecipeNetworkID, r.RecipeID, r.Block, r.Output...)
	})
	pk.ShapelessRecipes = slices.DeleteFunc(slices.Clone(source.ShapelessRecipes), func(r protocol.ShapelessRecipe) bool {
		return hide(r.RecipeNetworkID, r.RecipeID, r.Block, r.Output...)
	})
	pk.UserDataShapelessRecipes = slices.DeleteFunc(slices.Clone(source.UserDataShapelessRecipes), func(r protocol.UserDataShapelessRecipe) bool {
		return hide(r.RecipeNetworkID, r.RecipeID, r.Block, r.Output...)
	})
	pk.SmithingTransformRecipes = slices.DeleteFunc(slices.Clone(source.SmithingTransformRecipes), func(r protocol.SmithingTransformRecipe) bool {
		return hide(r.RecipeNetworkID, r.RecipeID, r.Block, r.Result)
	})
	pk.SmithingTrimRecipes = slices.DeleteFunc(slices.Clone(source.SmithingTrimRecipes), func(r protocol.SmithingTrimRecipe) bool {
		return hide(r.RecipeNetworkID, r.RecipeID, r.Block)
	})
	v.pk = &pk
	return v
}

// hasRecipe reports whether the player may use a recipe the client named.
func (p *Player) hasRecipe(netID uint32) bool {
	v := p.recipeView()
	if v == nil {
		return true
	}
	if v.rules.all {
		return false
	}
	_, hidden := v.hidden[netID]
	return !hidden
}
