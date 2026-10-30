package registry

import (
	"bytes"
	"errors"
	"testing"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
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

// encode returns a packet's payload bytes.
func encode(t *testing.T, pk packet.Packet) []byte {
	t.Helper()
	var buf bytes.Buffer
	pk.Marshal(protocol.NewWriter(&buf, 0))
	return buf.Bytes()
}

// TestRebuiltPacketsMatchBDS checks that the packets the registry builds
// from its own tables, as it does once a plugin changes something, are
// byte for byte BDS's when nothing was changed.
func TestRebuiltPacketsMatchBDS(t *testing.T) {
	s := vanilla(t)
	r, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encode(t, s.buildBiomePacket(r.Biomes.biomes)), encode(t, s.s.vanillaBiomes)) {
		t.Fatal("rebuilt BiomeDefinitionList differs from BDS's")
	}
	s2 := vanilla(t)
	s2.s.entitiesChanged = true
	r2, err := s2.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r2.EntityTypes.IdentifiersPacket().SerialisedEntityIdentifiers, s2.s.vanillaEntities.SerialisedEntityIdentifiers) {
		t.Fatal("rebuilt AvailableActorIdentifiers differs from BDS's")
	}
}

// TestBiomesAndEntities checks custom and deleted biomes and entity types.
func TestBiomesAndEntities(t *testing.T) {
	s := vanilla(t)
	p := s.As("plugin")
	if err := p.RegisterBiome(Biome{Name: "plugin:glow_fields", ID: -1, Temperature: 0.5, Rain: true, Tags: []string{"overworld"}}); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteBiome("minecraft:desert", ""); err != nil {
		t.Fatal(err)
	}
	if err := p.RegisterEntityType(EntityType{Name: "plugin:ghost", BaseID: "minecraft:"}); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteEntityType(Player); err == nil {
		t.Fatal("player was deleted")
	}
	r, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	plains, _ := r.Biomes.ByName(Plains)
	desert := int16(2) // BDS's ID
	if got := r.Biomes.Resolve(desert); got != plains.ID {
		t.Fatalf("deleted desert loads as %d, want plains %d", got, plains.ID)
	}
	glow, ok := r.Biomes.ByName("plugin:glow_fields")
	if !ok || glow.ID < 0 || glow.ID == desert {
		t.Fatalf("custom biome ID %d", glow.ID)
	}
	if n := len(r.Biomes.DefinitionPacket().BiomeDefinitions); n != 89 {
		t.Fatalf("%d biome definitions, want 89", n)
	}
	ghost, ok := r.EntityTypes.ByName("plugin:ghost")
	if !ok || ghost.RuntimeID <= 0 {
		t.Fatalf("custom entity runtime ID %d", ghost.RuntimeID)
	}
	if zombie, _ := r.EntityTypes.ByName("minecraft:zombie"); len(zombie.Definition) == 0 {
		t.Fatal("zombie has no definition")
	}
}

// TestLive checks live updates and their listeners.
func TestLive(t *testing.T) {
	r, err := vanilla(t).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	before := r.Crafting.Packet()
	n := len(before.ShapedRecipes)
	heard := 0
	remove := r.Crafting.OnChange(func(p *packet.CraftingData) { heard = len(p.ShapedRecipes) })
	r.Crafting.Update(func(p *packet.CraftingData) { p.ShapedRecipes = p.ShapedRecipes[:n-1] })
	remove()
	r.Crafting.Update(func(p *packet.CraftingData) { p.ShapedRecipes = nil })
	if heard != n-1 || len(before.ShapedRecipes) != n || len(r.Crafting.Packet().ShapedRecipes) != 0 {
		t.Fatalf("heard %d, old packet %d, new %d", heard, len(before.ShapedRecipes), len(r.Crafting.Packet().ShapedRecipes))
	}
	if len(r.Creative.Packet().Items) == 0 {
		t.Fatal("no creative items")
	}
}
