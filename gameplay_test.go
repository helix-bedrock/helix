package helix

import (
	"log/slog"
	"testing"
)

// TestToolSpeed checks the digger rules against BDS 1.26.60's mining table.
func TestToolSpeed(t *testing.T) {
	m, err := loadMining()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		item, block string
		speed       float32
		harvest     bool
	}{
		{"", "minecraft:stone", 1, false},
		{"minecraft:wooden_pickaxe", "minecraft:stone", 2, true},
		{"minecraft:wooden_pickaxe", "minecraft:diamond_ore", 2, false},
		{"minecraft:iron_pickaxe", "minecraft:diamond_ore", 6, true},
		{"minecraft:iron_pickaxe", "minecraft:obsidian", 6, false},
		{"minecraft:diamond_pickaxe", "minecraft:obsidian", 8, true},
		{"minecraft:wooden_pickaxe", "minecraft:dirt", 1, true},
		{"minecraft:wooden_shovel", "minecraft:dirt", 2, true},
	}
	for _, c := range cases {
		b := BreakInfo{Name: c.block, Item: ItemStack{Name: c.item, Count: 1}}
		if c.item == "" {
			b.Item = ItemStack{}
		}
		if got := m.toolSpeed(&b); got != c.speed || b.Harvest != c.harvest {
			t.Errorf("%s on %s: speed %v harvest %v, want %v %v", c.item, c.block, got, b.Harvest, c.speed, c.harvest)
		}
	}
}

// TestEfficiency checks Efficiency adds level²+1 only to a tool whose own
// speed applied.
func TestEfficiency(t *testing.T) {
	b := &BreakInfo{Enchantable: true}
	if got := efficiencySpeed(b, 3, 6); got != 16 {
		t.Errorf("efficiency III on 6: %v, want 16", got)
	}
	b.Enchantable = false
	if got := efficiencySpeed(b, 3, 1); got != 1 {
		t.Errorf("efficiency on a wrong tool: %v, want 1", got)
	}
}

// TestHasteConduit checks only the higher of Haste and Conduit Power
// counts, Haste on a tie.
func TestHasteConduit(t *testing.T) {
	p := &Player{effects: []ActiveEffect{{ID: EffectHaste, Amplifier: 0}, {ID: EffectConduitPower, Amplifier: 1}}}
	b := &BreakInfo{Player: p}
	if got := hasteSpeed(EffectHaste)(b, 0, 1); got != 1 {
		t.Errorf("haste under stronger conduit: %v, want 1", got)
	}
	if got := hasteSpeed(EffectConduitPower)(b, 1, 1); got != 1.4 {
		t.Errorf("conduit II: %v, want 1.4", got)
	}
	p.effects[1].Amplifier = 0
	if hasteCounts(p, EffectConduitPower, 0) || !hasteCounts(p, EffectHaste, 0) {
		t.Error("tie: haste must count, conduit not")
	}
}

// TestEventOrder checks listeners run by priority, then registration
// order, and a panicking one does not stop the rest.
func TestEventOrder(t *testing.T) {
	var l listeners[BlockBreakEvent]
	var order []int
	l.add("a", Late, func(*BlockBreakEvent) { order = append(order, 3) })
	l.add("b", First, func(*BlockBreakEvent) { order = append(order, 1) })
	l.add("c", First, func(*BlockBreakEvent) { panic("boom") })
	l.add("d", Normal, func(e *BlockBreakEvent) { order = append(order, 2); e.Cancel() })
	l.freeze()
	e := BlockBreakEvent{}
	l.fire(slog.New(slog.DiscardHandler), &e)
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 || !e.Cancelled() {
		t.Errorf("order %v cancelled %v", order, e.Cancelled())
	}
}
