package registry

import (
	"errors"
	"fmt"
	"slices"

	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// smithingBlock is the block tag of a smithing recipe.
const smithingBlock = "smithing_table"

// SmithingRecipe is a smithing recipe as a plugin reads and writes it.
type SmithingRecipe struct {
	// ID names the recipe, such as "minecraft:smithing_netherite_sword".
	ID string
	// Template is optional. Base and Addition are required. Use crafting.AnyVariant
	// or crafting.Tagged to build them.
	Template, Base, Addition crafting.Ingredient
	// Result is the item made. The base's data (enchantments, damage, name) carries
	// over to it.
	Result     string
	ResultMeta int16
	// ResultCount is how many are made; 0 means one.
	ResultCount uint8
	// Trim marks vanilla's armour trim recipe. It has no Result and can only be
	// listed or removed.
	Trim bool
}

// Smithing edits the smithing recipes during one EditSmithing call. By default every
// vanilla recipe is there; a plugin may take any of them away or change them.
type Smithing struct {
	pk     *packet.CraftingData
	items  *Items
	blocks *Blocks
}

// Recipes lists every smithing recipe.
func (s *Smithing) Recipes() []SmithingRecipe {
	out := make([]SmithingRecipe, 0, len(s.pk.SmithingTransformRecipes)+len(s.pk.SmithingTrimRecipes))
	for _, r := range s.pk.SmithingTransformRecipes {
		out = append(out, s.read(r))
	}
	for _, r := range s.pk.SmithingTrimRecipes {
		out = append(out, SmithingRecipe{
			ID: r.RecipeID, Trim: true, Template: crafting.ReadIngredient(r.Template),
			Base: crafting.ReadIngredient(r.Base), Addition: crafting.ReadIngredient(r.Addition),
		})
	}
	return out
}

func (s *Smithing) read(r protocol.SmithingTransformRecipe) SmithingRecipe {
	out := SmithingRecipe{
		ID: r.RecipeID, Template: crafting.ReadIngredient(r.Template), Base: crafting.ReadIngredient(r.Base),
		Addition: crafting.ReadIngredient(r.Addition), ResultMeta: int16(r.Result.MetadataValue), //nolint:gosec // aux values are int16
		ResultCount: uint8(min(r.Result.Count, 255)), //nolint:gosec // bounded
	}
	if t, ok := s.items.ByID(int16(r.Result.NetworkID)); ok { //nolint:gosec // network IDs are int16
		out.Result = t.Name
	}
	return out
}

// Recipe returns the recipe with an ID.
func (s *Smithing) Recipe(id string) (SmithingRecipe, bool) {
	i := slices.IndexFunc(s.Recipes(), func(r SmithingRecipe) bool { return r.ID == id })
	if i < 0 {
		return SmithingRecipe{}, false
	}
	return s.Recipes()[i], true
}

// For lists the recipes that make an item.
func (s *Smithing) For(item string) []SmithingRecipe {
	return slices.DeleteFunc(s.Recipes(), func(r SmithingRecipe) bool { return r.Result != item })
}

// Clear removes every smithing recipe, trims too.
func (s *Smithing) Clear() {
	s.pk.SmithingTransformRecipes, s.pk.SmithingTrimRecipes = nil, nil
}

// Remove removes one recipe by ID. It reports whether the recipe was there.
func (s *Smithing) Remove(id string) bool {
	before := len(s.pk.SmithingTransformRecipes) + len(s.pk.SmithingTrimRecipes)
	s.pk.SmithingTransformRecipes = slices.DeleteFunc(s.pk.SmithingTransformRecipes, func(r protocol.SmithingTransformRecipe) bool { return r.RecipeID == id })
	s.pk.SmithingTrimRecipes = slices.DeleteFunc(s.pk.SmithingTrimRecipes, func(r protocol.SmithingTrimRecipe) bool { return r.RecipeID == id })
	return len(s.pk.SmithingTransformRecipes)+len(s.pk.SmithingTrimRecipes) != before
}

// RemoveFor removes every recipe that makes an item and returns how many there were.
func (s *Smithing) RemoveFor(item string) int {
	before := len(s.pk.SmithingTransformRecipes)
	s.pk.SmithingTransformRecipes = slices.DeleteFunc(s.pk.SmithingTransformRecipes, func(r protocol.SmithingTransformRecipe) bool {
		return s.read(r).Result == item
	})
	return before - len(s.pk.SmithingTransformRecipes)
}

// Set changes the recipe with r's ID, or adds it if there is none.
func (s *Smithing) Set(r SmithingRecipe) error {
	if r.Trim {
		return errors.New("registry: smithing: the trim recipe can only be removed")
	}
	if r.ID == "" {
		return errors.New("registry: smithing: recipe has no ID")
	}
	if r.Base.Empty() || r.Addition.Empty() {
		return fmt.Errorf("registry: smithing: %s: base and addition are required", r.ID)
	}
	result, err := s.result(r)
	if err != nil {
		return err
	}
	if s.isTrim(r.ID) {
		return fmt.Errorf("registry: smithing: %s is the trim recipe", r.ID)
	}
	out := protocol.SmithingTransformRecipe{
		RecipeID: r.ID, Block: smithingBlock, Template: r.Template.Descriptor(), Base: r.Base.Descriptor(),
		Addition: r.Addition.Descriptor(), Result: result,
	}
	if i := slices.IndexFunc(s.pk.SmithingTransformRecipes, func(o protocol.SmithingTransformRecipe) bool { return o.RecipeID == r.ID }); i >= 0 {
		out.RecipeNetworkID = s.pk.SmithingTransformRecipes[i].RecipeNetworkID
		s.pk.SmithingTransformRecipes[i] = out
		return nil
	}
	out.RecipeNetworkID = nextRecipeID(s.pk)
	s.pk.SmithingTransformRecipes = append(s.pk.SmithingTransformRecipes, out)
	return nil
}

// isTrim reports whether an ID belongs to the trim recipe.
func (s *Smithing) isTrim(id string) bool {
	return slices.ContainsFunc(s.pk.SmithingTrimRecipes, func(o protocol.SmithingTrimRecipe) bool { return o.RecipeID == id })
}

// result is the packet's form of a recipe's result. A block item names its block's
// default state, as BDS's recipes do.
func (s *Smithing) result(r SmithingRecipe) (protocol.ItemStack, error) {
	t, ok := s.items.ByName(r.Result)
	if !ok {
		return protocol.ItemStack{}, fmt.Errorf("registry: smithing: %s: unknown result %q", r.ID, r.Result)
	}
	count := r.ResultCount
	if count == 0 {
		count = 1
	}
	if count > t.MaxStack {
		return protocol.ItemStack{}, fmt.Errorf("registry: smithing: %s: %d of %s is over a stack", r.ID, count, r.Result)
	}
	st := protocol.ItemStack{
		ItemType: protocol.ItemType{NetworkID: int32(t.ID), MetadataValue: uint32(uint16(r.ResultMeta))},
		Count:    uint16(count),
	}
	if id, ok := s.blocks.Block(r.Result); ok {
		st.BlockRuntimeID = int32(s.blocks.Hash(id)) //nolint:gosec // hashes are sent as int32
	}
	return st, nil
}

// nextRecipeID is a network ID no recipe of the packet uses.
func nextRecipeID(p *packet.CraftingData) uint32 {
	var top uint32
	note := func(id uint32) { top = max(top, id) }
	for _, r := range p.ShapedRecipes {
		note(r.RecipeNetworkID)
	}
	for _, r := range p.ShapelessRecipes {
		note(r.RecipeNetworkID)
	}
	for _, r := range p.MultiRecipes {
		note(r.RecipeNetworkID)
	}
	for _, r := range p.UserDataShapelessRecipes {
		note(r.RecipeNetworkID)
	}
	for _, r := range p.ShapelessChemistryRecipes {
		note(r.RecipeNetworkID)
	}
	for _, r := range p.ShapedChemistryRecipes {
		note(r.RecipeNetworkID)
	}
	for _, r := range p.SmithingTransformRecipes {
		note(r.RecipeNetworkID)
	}
	for _, r := range p.SmithingTrimRecipes {
		note(r.RecipeNetworkID)
	}
	return top + 1
}

// EditSmithing changes the smithing recipes while the server runs, and everyone online
// gets the new list once edit returns. If edit returns an error nothing changes.
// Runs: any goroutine.
func (r *Registry) EditSmithing(edit func(s *Smithing) error) error {
	var err error
	r.Crafting.Update(func(p *packet.CraftingData) {
		err = applySmithing(p, r.Items, r.Blocks, edit)
	})
	return err
}

// applySmithing runs one edit, putting the lists back if it fails.
func applySmithing(p *packet.CraftingData, items *Items, blocks *Blocks, edit func(s *Smithing) error) error {
	transforms, trims := slices.Clone(p.SmithingTransformRecipes), slices.Clone(p.SmithingTrimRecipes)
	if err := edit(&Smithing{pk: p, items: items, blocks: blocks}); err != nil {
		p.SmithingTransformRecipes, p.SmithingTrimRecipes = transforms, trims
		return err
	}
	return nil
}

// EditSmithing changes the smithing recipes at boot, after every block and item is
// registered. An edit's error stops the server starting.
func (s *Setup) EditSmithing(edit func(s *Smithing) error) error {
	if s.s.frozen {
		return ErrFrozen
	}
	s.s.smithingEdits = append(s.s.smithingEdits, edit)
	return nil
}
