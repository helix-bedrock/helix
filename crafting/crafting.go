// Package crafting holds the recipes a player crafts with and BDS 1.26.60's rule
// for whether a crafting grid fits one. It knows nothing of players: the player
// package checks requests and moves items.
package crafting

import (
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// anyMeta is the aux value of an ingredient that takes every variant of its item.
const anyMeta = 0x7FFF

// auxNameColons is how many colons a name has that ends in its aux value.
const auxNameColons = 2

// Sizes of the grids. A recipe wider or taller than the small grid, or one with
// more ingredients than it has slots, needs the large one.
const (
	smallSide    = 2
	largeSide    = 3
	smallSlots   = smallSide * smallSide
	largeOffsets = largeSide
)

// Item is what a recipe reads of an item in a grid. An empty Name is an empty slot.
type Item struct {
	Name string
	Meta int16
	Tags []string
}

// Empty reports whether the slot holds nothing.
func (i Item) Empty() bool { return i.Name == "" }

// Ingredient is one thing a recipe takes: an item, or any item with a tag. A zero
// Ingredient takes nothing and marks an empty cell of a shaped recipe.
type Ingredient struct {
	Name string
	Meta int32
	Tag  string
}

// Empty reports whether the cell takes nothing.
func (in Ingredient) Empty() bool { return in.Name == "" && in.Tag == "" }

// Matches reports whether the item is this ingredient.
func (in Ingredient) Matches(it Item) bool {
	switch {
	case it.Empty() || in.Empty():
		return false
	case in.Tag != "":
		return slices.Contains(it.Tags, in.Tag)
	}
	return in.Name == it.Name && (in.Meta == anyMeta || in.Meta == int32(it.Meta))
}

// Grid is the items in a crafting grid, row by row.
type Grid struct {
	Items []Item
	Width int
}

// at is the item at a column and row; outside the grid is empty.
func (g Grid) at(x, y int) Item {
	if x >= g.Width {
		return Item{}
	}
	if i := x + y*g.Width; i < len(g.Items) {
		return g.Items[i]
	}
	return Item{}
}

// Recipe is one recipe a client can name by its network ID. Treat it as read-only:
// it is shared by every region.
type Recipe struct {
	ID        string
	NetworkID uint32
	// Block is the tag saying where the recipe is made, such as "crafting_table".
	Block string
	// Shaped recipes have their Input cells row by row, Width by Height; shapeless
	// ones take their Input in any order.
	Shaped        bool
	Width, Height int
	// Mirrored says a shaped recipe also fits flipped left to right.
	Mirrored bool
	Input    []Ingredient
	Output   []protocol.ItemStack
}

// Large reports whether the recipe does not fit the player's own small grid, by
// BDS's rule per kind of recipe.
func (r *Recipe) Large() bool {
	if r.Shaped {
		return r.Width > smallSide || r.Height > smallSide
	}
	return len(r.Input) > smallSlots
}

// Ingredients are the cells of the recipe that take something.
func (r *Recipe) Ingredients() []Ingredient {
	out := make([]Ingredient, 0, len(r.Input))
	for _, in := range r.Input {
		if !in.Empty() {
			out = append(out, in)
		}
	}
	return out
}

// Fits reports whether the grid holds this recipe and nothing else, as BDS matches:
// a shaped recipe at any offset, as it is or mirrored; a shapeless one by taking
// each item off the list at the first ingredient it matches.
func (r *Recipe) Fits(g Grid) bool {
	if !r.Shaped {
		return r.fitsShapeless(g)
	}
	if r.Width > largeSide || r.Height > largeSide {
		return false
	}
	for x := 0; x <= largeOffsets-r.Width; x++ {
		for y := 0; y <= largeOffsets-r.Height; y++ {
			if r.fitsAt(g, x, y, false) || r.Mirrored && r.fitsAt(g, x, y, true) {
				return true
			}
		}
	}
	return false
}

// fitsAt checks every cell of the large grid against the recipe placed at an offset.
func (r *Recipe) fitsAt(g Grid, offX, offY int, mirror bool) bool {
	for y := range largeSide {
		for x := range largeSide {
			it, in := g.at(x, y), r.cell(x-offX, y-offY, mirror)
			if it.Empty() != in.Empty() || !it.Empty() && !in.Matches(it) {
				return false
			}
		}
	}
	return true
}

// cell is the ingredient at a column and row of the recipe; outside it is empty.
func (r *Recipe) cell(x, y int, mirror bool) Ingredient {
	if x < 0 || y < 0 || x >= r.Width || y >= r.Height {
		return Ingredient{}
	}
	if mirror {
		x = r.Width - 1 - x
	}
	if i := x + y*r.Width; i < len(r.Input) {
		return r.Input[i]
	}
	return Ingredient{}
}

func (r *Recipe) fitsShapeless(g Grid) bool {
	left := slices.Clone(r.Input)
	for _, it := range g.Items {
		if it.Empty() {
			continue
		}
		i := slices.IndexFunc(left, func(in Ingredient) bool { return in.Matches(it) })
		if i < 0 {
			return false
		}
		left = slices.Delete(left, i, i+1)
	}
	return len(left) == 0
}

// Table is the recipes of one CraftingData packet by network ID. It does not change
// after it is built.
type Table struct {
	source   *packet.CraftingData
	recipes  map[uint32]*Recipe
	smithing map[uint32]*SmithingRecipe
	// special is BDS's special recipes (its multi recipes), worked out in code, by
	// network ID to the UUID that says which one.
	special map[uint32]uuid.UUID
	// smelt and smeltTags are the one-item recipes of furnaces and campfires, by block,
	// then by the input's name or tag.
	smelt, smeltTags map[string]map[string][]*Recipe
}

// smeltBlocks are the blocks whose recipes take one item, looked up by that item.
var smeltBlocks = map[string]bool{"furnace": true, "blast_furnace": true, "smoker": true, "campfire": true, "soul_campfire": true}

// NewTable reads the shaped and shapeless recipes of a packet. The rest of the packet
// is what other screens or special rules use, so it is not in the table.
func NewTable(pk *packet.CraftingData) *Table {
	t := &Table{
		source: pk, recipes: make(map[uint32]*Recipe, len(pk.ShapedRecipes)+len(pk.ShapelessRecipes)),
		smithing: make(map[uint32]*SmithingRecipe, len(pk.SmithingTransformRecipes)),
		special:  make(map[uint32]uuid.UUID, len(pk.MultiRecipes)),
	}
	for _, r := range pk.MultiRecipes {
		t.special[r.RecipeNetworkID] = r.UUID
	}
	for _, r := range pk.SmithingTransformRecipes {
		t.smithing[r.RecipeNetworkID] = &SmithingRecipe{
			ID: r.RecipeID, NetworkID: r.RecipeNetworkID, Block: r.Block, Result: r.Result,
			Template: ReadIngredient(r.Template), Base: ReadIngredient(r.Base), Addition: ReadIngredient(r.Addition),
		}
	}
	for _, r := range pk.SmithingTrimRecipes {
		t.smithing[r.RecipeNetworkID] = &SmithingRecipe{
			ID: r.RecipeID, NetworkID: r.RecipeNetworkID, Block: r.Block, Trim: true,
			Template: ReadIngredient(r.Template), Base: ReadIngredient(r.Base), Addition: ReadIngredient(r.Addition),
		}
	}
	for _, r := range pk.ShapedRecipes {
		t.recipes[r.RecipeNetworkID] = &Recipe{
			ID: r.RecipeID, NetworkID: r.RecipeNetworkID, Block: r.Block, Shaped: true,
			Width: int(r.Width), Height: int(r.Height), Mirrored: r.AssumeSymmetry,
			Input: ingredients(r.Input), Output: r.Output,
		}
	}
	for _, r := range pk.ShapelessRecipes {
		rec := &Recipe{
			ID: r.RecipeID, NetworkID: r.RecipeNetworkID, Block: r.Block,
			Input: ingredients(r.Input), Output: r.Output,
		}
		t.recipes[r.RecipeNetworkID] = rec
		if smeltBlocks[r.Block] && len(rec.Input) == 1 && !rec.Input[0].Empty() && len(rec.Output) != 0 {
			t.addSmelt(rec)
		}
	}
	return t
}

// addSmelt files a one-item recipe under its block and input.
func (t *Table) addSmelt(r *Recipe) {
	lists, key := &t.smelt, r.Input[0].Name
	if r.Input[0].Tag != "" {
		lists, key = &t.smeltTags, r.Input[0].Tag
	}
	if *lists == nil {
		*lists = map[string]map[string][]*Recipe{}
	}
	byInput := (*lists)[r.Block]
	if byInput == nil {
		byInput = map[string][]*Recipe{}
		(*lists)[r.Block] = byInput
	}
	byInput[key] = append(byInput[key], r)
}

// Smelt returns the recipe block makes from one item, or nil: one naming the item's own
// aux value first, then one taking any variant, then one taking a tag the item has, the
// lowest network ID among equals. It allocates nothing, so a furnace may ask every tick.
func (t *Table) Smelt(block string, it Item) *Recipe {
	if it.Empty() {
		return nil
	}
	var exact, variant *Recipe
	byInput := t.smelt[block]
	for _, r := range byInput[it.Name] {
		switch in := r.Input[0]; {
		case in.Meta == int32(it.Meta) && (exact == nil || r.NetworkID < exact.NetworkID):
			exact = r
		case in.Meta == anyMeta && (variant == nil || r.NetworkID < variant.NetworkID):
			variant = r
		}
	}
	if exact != nil {
		return exact
	}
	if variant != nil {
		return variant
	}
	var tagged *Recipe
	byTag := t.smeltTags[block]
	for _, tag := range it.Tags {
		for _, r := range byTag[tag] {
			if tagged == nil || r.NetworkID < tagged.NetworkID {
				tagged = r
			}
		}
	}
	return tagged
}

// Special returns the UUID of the special recipe (BDS's multi recipe) with a network ID.
func (t *Table) Special(id uint32) (uuid.UUID, bool) {
	u, ok := t.special[id]
	return u, ok
}

// Recipe returns the recipe with a network ID.
func (t *Table) Recipe(id uint32) (*Recipe, bool) {
	r, ok := t.recipes[id]
	return r, ok
}

// Find returns the recipe for block that fits the grid, the lowest network ID if several do,
// or nil. It walks every recipe: for what crafts now and then, not every tick.
func (t *Table) Find(block string, g Grid) *Recipe {
	var found *Recipe
	for _, r := range t.recipes {
		if r.Block == block && (found == nil || r.NetworkID < found.NetworkID) && r.Fits(g) {
			found = r
		}
	}
	return found
}

// ingredients reads a packet's cells. Kinds a vanilla packet does not use take
// nothing, so a recipe with one never fits a grid holding an item there.
func ingredients(list []protocol.ItemDescriptorCount) []Ingredient {
	out := make([]Ingredient, len(list))
	for i, d := range list {
		switch x := d.Descriptor.(type) {
		case *protocol.DefaultItemDescriptor:
			out[i] = defaultIngredient(x)
		case *protocol.ItemTagItemDescriptor:
			out[i] = Ingredient{Tag: x.Tag}
		}
	}
	return out
}

// defaultIngredient reads an item ingredient. BDS writes the items it turned from an
// old aux value into their own name with the aux at the end of the name, such as
// "minecraft:milk_bucket:0".
func defaultIngredient(d *protocol.DefaultItemDescriptor) Ingredient {
	in := Ingredient{Name: d.Name, Meta: d.MetadataValue}
	if i := strings.LastIndexByte(d.Name, ':'); i > 0 && strings.Count(d.Name, ":") == auxNameColons {
		if aux, err := strconv.ParseInt(d.Name[i+1:], 10, 32); err == nil {
			in.Name, in.Meta = d.Name[:i], int32(aux)
		}
	}
	return in
}

// Book keeps the table of the recipes now in force. They can change while the server
// runs, so it builds a new table when given a new packet. Any goroutine may use it.
type Book struct {
	cur atomic.Pointer[Table]
}

// Table returns the table of a packet.
func (b *Book) Table(pk *packet.CraftingData) *Table {
	if t := b.cur.Load(); t != nil && t.source == pk {
		return t
	}
	t := NewTable(pk)
	b.cur.Store(t)
	return t
}
