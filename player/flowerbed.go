package player

import (
	"maps"

	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/registry"
)

// Flower beds (wildflowers, pink petals, leaf litter), BDS 1.26.60's FlowerBedBlock (vtable
// 0xFB8B378; leaf litter's LeafLitterBlock 0xFB7F6C0 keeps the same two rules). A bed is built
// over only by its own block while its growth is below 3 (canBeBuiltOver 0xCB02E70), and
// placing it there adds one to the growth and keeps the bed's facing (getPlacementBlock
// 0xCB03150), so one block holds up to four. A new bed faces by BDS's placement_direction trait,
// cardinal with a 180 degree offset (the block registrations at 0x5C948E2 and 0x5D12F7B). A
// broken bed drops growth + 1 of its item (helix-data's block drops). Natural beds are the
// same blocks, so they stack and drop the same way.
//
// Not done: canBeBuiltOver's other branch, which lets a block with a flag (+264 bit 3) build
// over a bed when a check on the region (sub_D277CF0, value 3) passes; what both mean was not read.

// flowerBeds are the blocks of FlowerBedBlock.
var flowerBeds = map[string]bool{
	"minecraft:wildflowers": true,
	"minecraft:pink_petals": true,
	"minecraft:leaf_litter": true,
}

const (
	// flowerBedGrowth is the bed's growth state, one less than the flowers it holds.
	flowerBedGrowth = "growth"
	// flowerBedMaxGrowth is the growth a bed stops taking more at.
	flowerBedMaxGrowth = 3
	// flowerBedFacingOffset is the placement_direction trait's y rotation offset.
	flowerBedFacingOffset = 180
)

// flowerBedBuiltOver is canBeBuiltOver: the same bed placed on a bed that is not full.
func flowerBedBuiltOver(blocks *registry.Blocks, b, placed uint32) bool {
	if blocks.Name(b) != blocks.Name(placed) {
		return false
	}
	g, _ := blocks.States(b)[flowerBedGrowth].(int32)
	return g < flowerBedMaxGrowth
}

// flowerBedState is getPlacementBlock: one more on the bed already there, else a new bed
// turned by the trait.
func flowerBedState(pl *Placement) (uint32, bool) {
	p := pl.Player
	blocks := p.s.reg.Blocks
	name := blocks.Name(pl.Block)
	if r := p.entity.Region(); r != nil {
		if b, ok := r.Block(pl.Pos, 0); ok && blocks.Name(b) == name {
			states := maps.Clone(blocks.States(b))
			g, _ := states[flowerBedGrowth].(int32)
			states[flowerBedGrowth] = g + 1
			return blocks.StateID(name, states)
		}
	}
	states := maps.Clone(blocks.States(pl.Block))
	look := direction.FromSteps((p.entity.Rotation().Yaw-flowerBedFacingOffset)/90 + 0.5)
	states[direction.CardinalState] = cardinalNames[look]
	return blocks.StateID(name, states)
}
