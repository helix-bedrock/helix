package crafting

import "github.com/helix-bedrock/protocol"

// Tags BDS 1.26.60 lets into a smithing table's slots.
const (
	tagTransformTemplates = "minecraft:transform_templates"
	tagTrimTemplates      = "minecraft:trim_templates"
	tagTransformable      = "minecraft:transformable_items"
	tagTrimmable          = "minecraft:trimmable_armors"
	tagTransformMaterials = "minecraft:transform_materials"
	tagTrimMaterials      = "minecraft:trim_materials"
)

// SmithingSlot is one of a smithing table's three input slots.
type SmithingSlot int

// The slots of a smithing table.
const (
	SmithingTemplate SmithingSlot = iota
	SmithingBase
	SmithingAddition
)

// SmithingRecipe is a recipe a smithing table makes: a transform turns the base into
// Result, keeping the base's data. A trim recipe has no Result. Treat it as read-only:
// it is shared by every region.
type SmithingRecipe struct {
	ID        string
	NetworkID uint32
	Block     string
	// Template, Base and Addition are what goes in each slot. An empty Template
	// means the recipe takes no template.
	Template, Base, Addition Ingredient
	Result                   protocol.ItemStack
	// Trim marks the armour trim recipe, which writes a trim on the base instead.
	Trim bool
}

// Fits reports whether the three slots make this recipe, as BDS's transform checks.
func (r *SmithingRecipe) Fits(template, base, addition Item) bool {
	if !r.Template.Empty() && !r.Template.Matches(template) {
		return false
	}
	return r.Base.Matches(base) && r.Addition.Matches(addition)
}

// ingredient is the recipe's ingredient for a slot.
func (r *SmithingRecipe) ingredient(slot SmithingSlot) Ingredient {
	switch slot {
	case SmithingTemplate:
		return r.Template
	case SmithingBase:
		return r.Base
	}
	return r.Addition
}

// Smithing returns the smithing recipe with a network ID.
func (t *Table) Smithing(id uint32) (*SmithingRecipe, bool) {
	r, ok := t.smithing[id]
	return r, ok
}

// SmithingAllows reports whether an item may go in a smithing table's slot: BDS's
// tags for the slot, and, so that a plugin's recipe works on a server, anything a
// recipe takes there.
func (t *Table) SmithingAllows(slot SmithingSlot, it Item) bool {
	for _, tag := range smithingSlotTags(slot) {
		if (Ingredient{Tag: tag}).Matches(it) {
			return true
		}
	}
	for _, r := range t.smithing {
		if r.ingredient(slot).Matches(it) {
			return true
		}
	}
	return false
}

func smithingSlotTags(slot SmithingSlot) [2]string {
	switch slot {
	case SmithingTemplate:
		return [2]string{tagTransformTemplates, tagTrimTemplates}
	case SmithingBase:
		return [2]string{tagTransformable, tagTrimmable}
	}
	return [2]string{tagTransformMaterials, tagTrimMaterials}
}

// ReadIngredient reads a packet's description of an ingredient.
func ReadIngredient(d protocol.ItemDescriptorCount) Ingredient {
	return ingredients([]protocol.ItemDescriptorCount{d})[0]
}

// Descriptor writes an ingredient as a packet describes it.
func (in Ingredient) Descriptor() protocol.ItemDescriptorCount {
	switch {
	case in.Tag != "":
		return protocol.ItemDescriptorCount{Descriptor: &protocol.ItemTagItemDescriptor{Tag: in.Tag}, Count: 1}
	case in.Name != "":
		return protocol.ItemDescriptorCount{Descriptor: &protocol.DefaultItemDescriptor{Name: in.Name, MetadataValue: in.Meta}, Count: 1}
	}
	return protocol.ItemDescriptorCount{Descriptor: &protocol.InvalidItemDescriptor{}}
}

// AnyVariant is an ingredient taking every aux value of an item.
func AnyVariant(name string) Ingredient { return Ingredient{Name: name, Meta: anyMeta} }

// Tagged is an ingredient taking any item with a tag.
func Tagged(tag string) Ingredient { return Ingredient{Tag: tag} }
