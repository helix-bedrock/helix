package enchantment

import (
	"github.com/helix-bedrock/helix/server/item"
	"github.com/helix-bedrock/helix/server/world"
)

// AquaAffinity is a helmet enchantment that increases underwater mining speed.
var AquaAffinity aquaAffinity

type aquaAffinity struct{}

// Name ...
func (aquaAffinity) Name() string {
	return "Aqua Affinity"
}

// MaxLevel ...
func (aquaAffinity) MaxLevel() int {
	return 1
}

// Cost ...
func (aquaAffinity) Cost(int) (int, int) {
	return 1, 41
}

// Rarity ...
func (aquaAffinity) Rarity() item.EnchantmentRarity {
	return item.EnchantmentRarityRare
}

// CompatibleWithEnchantment ...
func (aquaAffinity) CompatibleWithEnchantment(item.EnchantmentType) bool {
	return true
}

// CompatibleWithItem ...
func (aquaAffinity) CompatibleWithItem(i world.Item) bool {
	h, ok := i.(item.HelmetType)
	return ok && h.Helmet()
}
