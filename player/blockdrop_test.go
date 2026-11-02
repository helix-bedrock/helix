package player

import (
	"sort"
	"testing"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/loot"
)

// TestBlockDrops checks a real drop through the registry, and that every item and
// state the drop rules name exists, so no rule silently drops nothing.
func TestBlockDrops(t *testing.T) {
	p := testPlayer(t)
	s := p.s
	table, err := loot.New()
	if err != nil {
		t.Fatal(err)
	}
	s.drops = table
	setup, err := NewSetup(s.log)
	if err != nil {
		t.Fatal(err)
	}
	if s.game, err = setup.freezeGameplay(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		block, item string
		want        bool
	}{
		{"minecraft:dirt", "", true},
		{"minecraft:stone", "", false},
		{"minecraft:stone", "minecraft:wooden_pickaxe", true},
		{"minecraft:diamond_ore", "minecraft:wooden_pickaxe", false},
	} {
		b := BreakInfo{Player: p, Name: c.block}
		if c.item != "" {
			b.Item = ItemStack{Name: c.item, Count: 1}
		}
		if got := s.game.harvests(&b); got != c.want {
			t.Errorf("%s with %q harvests = %v", c.block, c.item, got)
		}
	}
	stone, _ := s.reg.Blocks.Block("minecraft:stone")
	items, _ := vanillaDrops(s, stone, &loot.Context{})
	if len(items) != 1 || items[0].Name != "minecraft:cobblestone" || items[0].Count != 1 || items[0].Block == nil {
		t.Fatalf("stone dropped %+v", items)
	}
	snow, ok := s.reg.Blocks.StateID("minecraft:snow_layer", map[string]any{"height": int32(3), "covered_bit": uint8(0)})
	if !ok {
		t.Fatal("no snow layer state")
	}
	if items, _ := vanillaDrops(s, snow, &loot.Context{}); len(items) != 2 || items[0].Name != "minecraft:snowball" {
		t.Fatalf("snow of height 3 dropped %+v", items)
	}

	drops, err := data.BlockDrops()
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	var walk func(block string, n *data.DropNode)
	walk = func(block string, n *data.DropNode) {
		if n == nil {
			return
		}
		switch n.Kind {
		case "self", "self_states", "popcount_multiface":
			if _, ok := s.reg.Items.ByName(block); !ok {
				missing = append(missing, block+" (itself)")
			}
		case "item", "item_nine_minus_six":
			if _, ok := s.reg.Items.ByName(n.Name); !ok {
				missing = append(missing, block+" -> "+n.Name)
			}
		case "actor_item":
			name := n.Name
			if name == "" {
				name = block
			}
			if _, ok := s.reg.Items.ByName(name); !ok {
				missing = append(missing, block+" -> "+name)
			}
		}
		if n.State != "" {
			id, _ := s.reg.Blocks.Block(block)
			if _, ok := blockStateNumber(s.reg.Blocks.States(id), n.State); !ok {
				missing = append(missing, block+" state "+n.State)
			}
		}
		for _, c := range []*data.DropNode{n.Count, n.Of, n.A, n.B, n.If, n.Then, n.Else} {
			walk(block, c)
		}
	}
	for name, d := range drops {
		walk(name, d.Drops)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d rules name what does not exist: %v", len(missing), missing)
	}
}
