package player

import (
	"testing"

	"github.com/helix-bedrock/helix-data/data"
)

// Every item of the table is real and places a real block.
func TestPlantedBlocks(t *testing.T) {
	p := testPlayer(t)
	for item, block := range plantedBlocks {
		if _, ok := p.s.reg.Items.ByName(item); !ok {
			t.Errorf("no item %s", item)
		}
		id, ok := p.itemBlock(ItemStack{Name: item, Count: 1})
		if !ok || p.s.reg.Blocks.Name(id) != block {
			t.Errorf("%s places %s, want %s", item, p.s.reg.Blocks.Name(id), block)
		}
	}
}

// Redstone taken from the creative inventory places, as a player does it.
func TestCreativeRedstonePlaces(t *testing.T) {
	p := testPlayer(t)
	pk, err := data.CreativeContent()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"minecraft:redstone":   "minecraft:redstone_wire",
		"minecraft:repeater":   "minecraft:unpowered_repeater",
		"minecraft:comparator": "minecraft:unpowered_comparator",
	}
	for _, it := range pk.Items {
		s := itemFromNetwork(p.s.reg, it.Item)
		block, ok := want[s.Name]
		if !ok {
			continue
		}
		delete(want, s.Name)
		id, ok := p.itemBlock(s)
		if !ok || p.s.reg.Blocks.Name(id) != block {
			t.Errorf("%s places %s, want %s", s.Name, p.s.reg.Blocks.Name(id), block)
		}
	}
	if len(want) != 0 {
		t.Errorf("not in the creative inventory: %v", want)
	}
}
