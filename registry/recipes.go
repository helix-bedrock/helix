package registry

import (
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// The largest a crafting recipe can be.
const (
	maxRecipeSide  = 3
	maxRecipeInput = maxRecipeSide * maxRecipeSide
)

// defaultRecipeBlock is where a recipe with no Block is made.
const defaultRecipeBlock = "crafting_table"

// RecipeItem is an item a recipe makes.
type RecipeItem struct {
	Name string
	Meta int16
	// Count is how many; 0 means one.
	Count uint8
}

// Recipe is a shaped or shapeless recipe as a plugin reads and writes it. Crafting
// tables, furnaces, stonecutters and campfires all keep theirs this way, told apart
// by Block. Screens not built yet list their recipes but cannot use them.
type Recipe struct {
	// ID names the recipe, such as "minecraft:stick". A few vanilla IDs are used by
	// two recipes, one of them with the block "deprecated".
	ID string
	// Block says where it is made: "crafting_table" (the default), "furnace",
	// "blast_furnace", "smoker", "campfire", "soul_campfire", "stonecutter",
	// "cartography_table".
	Block string
	// Shaped recipes have their Input row by row, Width by Height, with a zero
	// crafting.Ingredient for an empty cell. Mirrored lets one fit flipped.
	Shaped        bool
	Width, Height int
	Mirrored      bool
	// Input is what it takes. Use crafting.AnyVariant or crafting.Tagged to build it.
	Input []crafting.Ingredient
	// Output is what it makes. A recipe can make several things, as a cake gives
	// back its buckets.
	Output []RecipeItem
	// Priority picks between recipes that fit the same items; lower comes first.
	Priority int32
}

// Recipes edits the recipes during one EditRecipes call. By default every vanilla
// recipe is there; a plugin may take any of them away or change them.
type Recipes struct {
	pk     *packet.CraftingData
	items  *Items
	blocks *Blocks
}

// List returns every shaped and shapeless recipe.
func (s *Recipes) List() []Recipe {
	out := make([]Recipe, 0, len(s.pk.ShapedRecipes)+len(s.pk.ShapelessRecipes))
	for _, r := range s.pk.ShapedRecipes {
		out = append(out, Recipe{
			ID: r.RecipeID, Block: r.Block, Shaped: true, Width: int(r.Width), Height: int(r.Height), Mirrored: r.AssumeSymmetry,
			Input: readIngredients(r.Input), Output: s.readOutput(r.Output), Priority: r.Priority,
		})
	}
	for _, r := range s.pk.ShapelessRecipes {
		out = append(out, Recipe{ID: r.RecipeID, Block: r.Block, Input: readIngredients(r.Input), Output: s.readOutput(r.Output), Priority: r.Priority})
	}
	return out
}

func readIngredients(list []protocol.ItemDescriptorCount) []crafting.Ingredient {
	out := make([]crafting.Ingredient, len(list))
	for i, d := range list {
		out[i] = crafting.ReadIngredient(d)
	}
	return out
}

func (s *Recipes) readOutput(list []protocol.ItemStack) []RecipeItem {
	out := make([]RecipeItem, 0, len(list))
	for _, st := range list {
		out = append(out, RecipeItem{Name: itemName(s.items, st), Meta: int16(st.MetadataValue), Count: uint8(min(st.Count, 255))}) //nolint:gosec // aux values are int16, count is bounded
	}
	return out
}

// Get returns the recipe with an ID. Where two share it, the one not marked
// "deprecated" comes back.
func (s *Recipes) Get(id string) (Recipe, bool) {
	found := Recipe{}
	ok := false
	for _, r := range s.List() {
		if r.ID == id && (!ok || found.Block == deprecatedRecipeBlock) {
			found, ok = r, true
		}
	}
	return found, ok
}

// For lists the recipes that make an item.
func (s *Recipes) For(item string) []Recipe {
	return slices.DeleteFunc(s.List(), func(r Recipe) bool {
		return !slices.ContainsFunc(r.Output, func(o RecipeItem) bool { return o.Name == item })
	})
}

// ForBlock lists the recipes made at a block, such as "stonecutter".
func (s *Recipes) ForBlock(block string) []Recipe {
	return slices.DeleteFunc(s.List(), func(r Recipe) bool { return r.Block != block })
}

// Clear removes every recipe of every kind: crafting, furnaces, stonecutters,
// special ones, smithing and brewing.
func (s *Recipes) Clear() {
	p := s.pk
	p.ShapedRecipes, p.ShapelessRecipes, p.UserDataShapelessRecipes, p.MultiRecipes = nil, nil, nil, nil
	p.ShapedChemistryRecipes, p.ShapelessChemistryRecipes = nil, nil
	p.SmithingTransformRecipes, p.SmithingTrimRecipes = nil, nil
	p.PotionRecipes, p.PotionContainerChangeRecipes, p.MaterialReducers = nil, nil, nil
}

// ClearBlock removes every recipe made at a block and returns how many there were.
// For a crafting table that leaves the special recipes; see ClearSpecial.
func (s *Recipes) ClearBlock(block string) int {
	return s.remove(func(id, b string, _ []protocol.ItemStack) bool { return b == block })
}

// ClearSpecial removes the recipes BDS works out by its own rules, such as fireworks.
func (s *Recipes) ClearSpecial() { s.pk.MultiRecipes = nil }

// ClearBrewing removes every brewing stand recipe.
func (s *Recipes) ClearBrewing() {
	s.pk.PotionRecipes, s.pk.PotionContainerChangeRecipes = nil, nil
}

// Remove removes the recipes with an ID. It reports whether there were any.
func (s *Recipes) Remove(id string) bool {
	return s.remove(func(rid, _ string, _ []protocol.ItemStack) bool { return rid == id }) != 0
}

// RemoveFor removes every recipe that makes an item and returns how many there were.
func (s *Recipes) RemoveFor(item string) int {
	return s.remove(func(_, _ string, output []protocol.ItemStack) bool {
		return slices.ContainsFunc(output, func(st protocol.ItemStack) bool { return itemName(s.items, st) == item })
	})
}

// remove takes out the recipes a test picks, from each list of recipes with an ID.
func (s *Recipes) remove(pick func(id, block string, output []protocol.ItemStack) bool) int {
	p := s.pk
	before := len(p.ShapedRecipes) + len(p.ShapelessRecipes) + len(p.UserDataShapelessRecipes)
	p.ShapedRecipes = slices.DeleteFunc(p.ShapedRecipes, func(r protocol.ShapedRecipe) bool { return pick(r.RecipeID, r.Block, r.Output) })
	p.ShapelessRecipes = slices.DeleteFunc(p.ShapelessRecipes, func(r protocol.ShapelessRecipe) bool { return pick(r.RecipeID, r.Block, r.Output) })
	p.UserDataShapelessRecipes = slices.DeleteFunc(p.UserDataShapelessRecipes, func(r protocol.UserDataShapelessRecipe) bool { return pick(r.RecipeID, r.Block, r.Output) })
	return before - len(p.ShapedRecipes) - len(p.ShapelessRecipes) - len(p.UserDataShapelessRecipes)
}

// Set changes the recipe with r's ID, or adds it if there is none. A changed recipe
// keeps its network ID; where two shared the ID, one takes the place of both.
func (s *Recipes) Set(r Recipe) error {
	if r.ID == "" {
		return errors.New("registry: recipe has no ID")
	}
	if err := checkRecipeShape(r); err != nil {
		return err
	}
	if slices.ContainsFunc(s.pk.UserDataShapelessRecipes, func(o protocol.UserDataShapelessRecipe) bool { return o.RecipeID == r.ID }) {
		return fmt.Errorf("registry: recipe %s keeps an item's data, so it can only be removed", r.ID)
	}
	output := make([]protocol.ItemStack, 0, len(r.Output))
	for _, o := range r.Output {
		st, err := recipeResult(s.items, s.blocks, r.ID, o.Name, o.Meta, o.Count)
		if err != nil {
			return err
		}
		output = append(output, st)
	}
	input := make([]protocol.ItemDescriptorCount, len(r.Input))
	for i, in := range r.Input {
		input[i] = in.Descriptor()
	}
	block := r.Block
	if block == "" {
		block = defaultRecipeBlock
	}
	id, identity, unlock := s.keptFields(r.ID)
	s.Remove(r.ID)
	if r.Shaped {
		s.pk.ShapedRecipes = append(s.pk.ShapedRecipes, protocol.ShapedRecipe{
			RecipeID: r.ID, Width: int32(r.Width), Height: int32(r.Height), Input: input, Output: output, UUID: identity, //nolint:gosec // checked small
			Block: block, Priority: r.Priority, AssumeSymmetry: r.Mirrored, UnlockRequirement: unlock, RecipeNetworkID: id,
		})
		return nil
	}
	s.pk.ShapelessRecipes = append(s.pk.ShapelessRecipes, protocol.ShapelessRecipe{
		RecipeID: r.ID, Input: input, Output: output, UUID: identity, Block: block, Priority: r.Priority,
		UnlockRequirement: unlock, RecipeNetworkID: id,
	})
	return nil
}

// keptFields are what a changed recipe keeps, or a new recipe's own: a recipe made by a
// plugin is unlocked for everyone.
func (s *Recipes) keptFields(id string) (uint32, uuid.UUID, protocol.Optional[protocol.RecipeUnlockRequirement]) {
	if i := slices.IndexFunc(s.pk.ShapedRecipes, func(o protocol.ShapedRecipe) bool { return o.RecipeID == id }); i >= 0 {
		o := s.pk.ShapedRecipes[i]
		return o.RecipeNetworkID, o.UUID, o.UnlockRequirement
	}
	if i := slices.IndexFunc(s.pk.ShapelessRecipes, func(o protocol.ShapelessRecipe) bool { return o.RecipeID == id }); i >= 0 {
		o := s.pk.ShapelessRecipes[i]
		return o.RecipeNetworkID, o.UUID, o.UnlockRequirement
	}
	return nextRecipeID(s.pk), uuid.New(), protocol.Option(protocol.RecipeUnlockRequirement{Context: protocol.RecipeUnlockContextAlwaysUnlocked})
}

// checkRecipeShape checks a recipe could ever be made.
func checkRecipeShape(r Recipe) error {
	if len(r.Output) == 0 {
		return fmt.Errorf("registry: recipe %s makes nothing", r.ID)
	}
	takes := slices.ContainsFunc(r.Input, func(in crafting.Ingredient) bool { return !in.Empty() })
	if !takes || len(r.Input) > maxRecipeInput {
		return fmt.Errorf("registry: recipe %s must take 1 to %d items", r.ID, maxRecipeInput)
	}
	if !r.Shaped {
		return nil
	}
	if r.Width < 1 || r.Height < 1 || r.Width > maxRecipeSide || r.Height > maxRecipeSide || r.Width*r.Height != len(r.Input) {
		return fmt.Errorf("registry: recipe %s: %d cells do not make %d by %d", r.ID, len(r.Input), r.Width, r.Height)
	}
	return nil
}

// EditRecipes changes the recipes while the server runs, and everyone online gets the
// new list once edit returns. If edit returns an error nothing changes. Runs: any
// goroutine.
func (r *Registry) EditRecipes(edit func(s *Recipes) error) error {
	var err error
	r.Crafting.Update(func(p *packet.CraftingData) {
		err = applyRecipes(p, r.Items, r.Blocks, edit)
	})
	return err
}

// applyRecipes runs one edit, putting everything back if it fails.
func applyRecipes(p *packet.CraftingData, items *Items, blocks *Blocks, edit func(s *Recipes) error) error {
	saved := cloneCrafting(p)
	if err := edit(&Recipes{pk: p, items: items, blocks: blocks}); err != nil {
		*p = *saved
		return err
	}
	return nil
}

// EditRecipes changes the recipes at boot, after every block and item is registered.
// An edit's error stops the server starting.
func (s *Setup) EditRecipes(edit func(s *Recipes) error) error {
	if s.s.frozen {
		return ErrFrozen
	}
	s.s.recipeEdits = append(s.s.recipeEdits, edit)
	return nil
}
