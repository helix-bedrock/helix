package player

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
)

// TestInventoryAPI checks what each player sees can differ from what the server keeps,
// that a cancelled open keeps the window shut and a cancelled explosion does nothing.
func TestInventoryAPI(t *testing.T) {
	p := testPlayer(t)
	p.name = "Steve"
	p.abilities = DefaultAbilities()
	p.flush = func() {}
	setup, err := NewSetup(p.s.log)
	if err != nil {
		t.Fatal(err)
	}
	if err := OnInventoryView(setup, func(e *InventoryViewEvent) {
		for i, it := range e.Items {
			if !it.Empty() {
				e.Items[i] = it.WithLore("Price for " + e.Player.Name())
			}
		}
	}, Normal); err != nil {
		t.Fatal(err)
	}
	if err := OnContainerOpen(setup, func(e *ContainerOpenEvent) {
		if e.Kind == ContainerAnvil {
			e.Cancel()
		}
	}, Normal); err != nil {
		t.Fatal(err)
	}
	if err := OnExplosionStart(setup, func(e *ExplosionStartEvent) { e.Cancel() }, Normal); err != nil {
		t.Fatal(err)
	}
	if p.s.game, err = setup.freezeGameplay(); err != nil {
		t.Fatal(err)
	}

	inv := p.Inventory()
	if inv.Size() != inventorySize || inv.Item(2).Name != "minecraft:emerald" {
		t.Fatalf("inventory %d slots, slot 2 %+v", inv.Size(), inv.Item(2))
	}
	seen := inv.viewFor(p, []int{2}, []ItemStack{inv.Item(2)})
	if l := seen[0].Lore(); len(l) != 1 || l[0] != "Price for Steve" {
		t.Errorf("seen lore %v", l)
	}
	if l := inv.Item(2).Lore(); len(l) != 0 {
		t.Errorf("the server's item got lore %v", l)
	}
	inv.SetView(func(e *InventoryViewEvent) { e.Items[0] = e.Items[0].WithName("Shiny") })
	if seen = inv.viewFor(p, []int{2}, []ItemStack{inv.Item(2)}); customName(seen[0]) != "Shiny" || len(seen[0].Lore()) != 1 {
		t.Errorf("own view: %+v", seen[0].Tag)
	}
	if left := inv.AddItem(ItemStack{Name: "minecraft:emerald", Count: 10}); !left.Empty() || inv.Item(2).Count != 64 || inv.Item(1).Count != 10 {
		t.Errorf("add left %+v, slots %d %d", left, inv.Item(2).Count, inv.Item(1).Count)
	}
	if p.Offhand().Size() != 1 || p.EnderChest().Size() != chestSize || p.EnderChest().Viewers() != nil {
		t.Error("own inventories wrong")
	}

	if p.openStation(nil, &BlockUse{Pos: cube.Pos{1, 2, 3}}, ContainerAnvil, byte(protocol.ContainerTypeAnvil)) || p.window.open {
		t.Error("a cancelled anvil opened")
	}
	if !p.openStation(nil, &BlockUse{Pos: cube.Pos{1, 2, 3}}, ContainerGrindstone, byte(protocol.ContainerTypeGrindstone)) || p.window.kind != ContainerGrindstone {
		t.Error("a grindstone did not open")
	}
	if p.s.Explode(nil, NewExplosion(mgl32.Vec3{}, 4)) {
		t.Error("a cancelled explosion went off")
	}
}
