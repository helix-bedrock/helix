package registry

import (
	"errors"
	"testing"
)

func vanilla(t *testing.T) *Setup {
	t.Helper()
	s := NewSetup(nil, "minecraft")
	if err := Vanilla(s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestVanilla checks the vanilla table against helix-data's counts and a
// few values BDS holds.
func TestVanilla(t *testing.T) {
	r, err := vanilla(t).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	b := r.Blocks
	if b.Count() != 23960 || len(b.names) != 1480 {
		t.Fatalf("%d states of %d blocks, want 23960 of 1480", b.Count(), len(b.names))
	}
	if b.Name(b.Air()) != Air || b.Name(b.Unknown()) != Unknown {
		t.Fatal("air or unknown has the wrong ID")
	}
	stone, _ := b.Block("minecraft:stone")
	if p := b.Properties(stone); p.LightDampening != 15 || p.DestroyTime != 1.5 {
		t.Fatalf("stone: %+v", p)
	}
	for id := range uint32(b.Count()) { //nolint:gosec // test
		name, states, _ := b.RuntimeIDToState(id)
		if got, ok := b.StateID(name, states); !ok || got != id {
			t.Fatalf("state %d %s %v comes back as %d", id, name, states, got)
		}
		if got, _ := b.HashToRuntimeID(b.Hash(id)); got != id {
			t.Fatalf("hash of state %d comes back as %d", id, got)
		}
	}
	if id, _ := b.StateToRuntimeID("minecraft:no_such_block", nil); id != b.Unknown() {
		t.Fatalf("unknown block loads as %s", b.Name(id))
	}
}

// TestDeleteAndReplace checks deleting, replacing and freezing.
func TestDeleteAndReplace(t *testing.T) {
	s := vanilla(t)
	if err := s.DeleteBlock(Air, ""); err == nil {
		t.Fatal("air was deleted")
	}
	plugin := s.As("plugin")
	if err := plugin.DeleteBlock("minecraft:diamond_ore", "minecraft:stone"); err != nil {
		t.Fatal(err)
	}
	if err := plugin.DeleteBlock("minecraft:emerald_ore", ""); err != nil {
		t.Fatal(err)
	}
	if err := plugin.RegisterBlock(BlockType{Name: "plugin:glow", States: []BlockState{{Properties: Properties{LightEmission: 15}}}}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	b := r.Blocks
	if !errors.Is(s.RegisterBlock(BlockType{}), ErrFrozen) {
		t.Fatal("register after freeze")
	}
	stone, _ := b.Block("minecraft:stone")
	if id, _ := b.StateToRuntimeID("minecraft:diamond_ore", map[string]any{}); id != stone {
		t.Fatalf("deleted diamond ore loads as %s", b.Name(id))
	}
	if id, _ := b.StateToRuntimeID("minecraft:emerald_ore", map[string]any{}); id != b.Unknown() {
		t.Fatalf("deleted emerald ore loads as %s", b.Name(id))
	}
	glow, ok := b.Block("plugin:glow")
	if !ok || b.LightBlock(glow) != 15 {
		t.Fatal("custom block missing")
	}
}

// TestUpdateProperties checks that a live change is seen and old tables
// stay as they were.
func TestUpdateProperties(t *testing.T) {
	r, err := vanilla(t).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	b := r.Blocks
	stone, _ := b.Block("minecraft:stone")
	before := b.PropertyTable()
	b.UpdateProperties(func(table []Properties) { table[stone].Friction = 0.98 })
	if b.Properties(stone).Friction != 0.98 || before[stone].Friction == 0.98 {
		t.Fatal("update not applied or old table changed")
	}
}

func BenchmarkFilteringBlock(b *testing.B) {
	r, err := NewSetup(nil, "minecraft").Freeze()
	if err == nil || r != nil {
		b.Fatal("empty setup froze")
	}
	s := NewSetup(nil, "minecraft")
	if err := Vanilla(s); err != nil {
		b.Fatal(err)
	}
	r, err = s.Freeze()
	if err != nil {
		b.Fatal(err)
	}
	blocks := r.Blocks
	b.ReportAllocs()
	b.ResetTimer()
	var sum int
	for i := range b.N {
		sum += int(blocks.FilteringBlock(uint32(i % blocks.Count()))) //nolint:gosec // bench
	}
	_ = sum
}

// TestItems checks the vanilla item table and custom item IDs.
func TestItems(t *testing.T) {
	s := vanilla(t)
	if err := s.As("plugin").RegisterItem(ItemType{Name: "plugin:wand", MaxStack: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteItem("minecraft:stick"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	it := r.Items
	if it.Count() != 2080 {
		t.Fatalf("%d items, want 2080 (BDS's 2,080 less stick plus wand)", it.Count())
	}
	if _, ok := it.ByName("minecraft:stick"); ok {
		t.Fatal("deleted stick still registered")
	}
	boat, ok := it.ByName("minecraft:acacia_boat")
	if !ok || boat.MaxStack != 1 {
		t.Fatalf("acacia boat: %+v", boat)
	}
	wand, _ := it.ByName("plugin:wand")
	if got, ok := it.ByID(wand.ID); !ok || got.Name != "plugin:wand" || wand.ID <= 0 {
		t.Fatalf("wand ID %d", wand.ID)
	}
	if len(it.RegistryPacket().Items) != it.Count() {
		t.Fatal("registry packet is missing items")
	}
}
