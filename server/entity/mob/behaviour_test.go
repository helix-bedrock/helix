package mob

import (
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/item"
	"github.com/helix-bedrock/helix/server/world"
)

func ticks(w *world.World, n int) {
	for range n {
		w.AdvanceTick()
	}
}

func count(w *world.World, match func(world.Entity) bool) int {
	n := 0
	w.Do(func(tx *world.Tx) {
		for e := range tx.Entities() {
			if match(e) {
				n++
			}
		}
	})
	return n
}

func TestCreeperExplodesNearTarget(t *testing.T) {
	w := mobWorld(t)
	ch := spawn(t, w, "minecraft:creeper", mgl64.Vec3{0.5, 1, 0.5})
	cow := spawn(t, w, "minecraft:cow", mgl64.Vec3{2.5, 1, 0.5})
	var lit bool
	for range 120 {
		w.AdvanceTick()
		w.Do(func(tx *world.Tx) {
			c, ok := ch.Entity(tx)
			if !ok {
				return
			}
			target, _ := cow.Entity(tx)
			c.(*Mob).SetTarget(target)
			if _, l := c.(*Mob).FuseLit(); l {
				lit = true
			}
		})
	}
	if !lit {
		t.Fatal("creeper fuse never lit with its target 2 blocks away")
	}
	var gone bool
	w.Do(func(tx *world.Tx) { _, ok := ch.Entity(tx); gone = !ok })
	if !gone {
		t.Fatal("creeper did not explode")
	}
	w.Do(func(tx *world.Tx) {
		if _, ok := tx.Block(cube.Pos{0, 0, 0}).(block.Air); !ok {
			t.Error("explosion left the floor under the creeper")
		}
	})
}

func TestSkeletonShootsArrows(t *testing.T) {
	w := mobWorld(t)
	sh := spawn(t, w, "minecraft:skeleton", mgl64.Vec3{0.5, 1, 0.5})
	cow := spawn(t, w, "minecraft:cow", mgl64.Vec3{10.5, 1, 0.5})
	ticks(w, 2)
	withMob(t, w, sh, func(m *Mob) {
		if main, _ := m.HeldItems(); itemName(main) != "minecraft:bow" {
			t.Errorf("skeleton holds %q, want a bow", itemName(main))
		}
	})
	for range 200 {
		w.AdvanceTick()
		w.Do(func(tx *world.Tx) {
			s, ok := sh.Entity(tx)
			c, cok := cow.Entity(tx)
			if ok && cok {
				s.(*Mob).SetTarget(c)
			}
		})
		if count(w, func(e world.Entity) bool { return e.H().Type() == entity.ArrowType }) > 0 {
			return
		}
	}
	t.Fatal("skeleton never shot an arrow at a target 10 blocks away")
}

func TestFedCowsBreed(t *testing.T) {
	w := mobWorld(t)
	var hs [2]*world.EntityHandle
	for i := range hs {
		hs[i] = spawn(t, w, "minecraft:cow", mgl64.Vec3{float64(i) * 3, 1, 0.5})
		withMob(t, w, hs[i], func(m *Mob) {
			m.TriggerEvent("minecraft:spawn_adult")
			if !m.Interact(nil, item.NewStack(item.Wheat{}, 1)) {
				t.Fatal("adult cow did not take wheat")
			}
		})
	}
	isCow := func(e world.Entity) bool { return e.H().Type().EncodeEntity() == "minecraft:cow" }
	for range 400 {
		w.AdvanceTick()
		if count(w, isCow) == 3 {
			babies := count(w, func(e world.Entity) bool { m, ok := e.(*Mob); return ok && isCow(e) && m.Baby() })
			if babies != 1 {
				t.Fatalf("%d babies", babies)
			}
			return
		}
	}
	t.Fatal("two cows in love never bred")
}

func TestFishSwimsAndBatFlies(t *testing.T) {
	w := mobWorld(t)
	w.Do(func(tx *world.Tx) {
		for x := -6; x <= 6; x++ {
			for z := -6; z <= 6; z++ {
				for y := 1; y <= 5; y++ {
					tx.SetLiquid(cube.Pos{x, y, z}, block.Water{Still: true, Depth: 8})
				}
			}
		}
	})
	fish := spawn(t, w, "minecraft:cod", mgl64.Vec3{0.5, 3, 0.5})
	bat := spawn(t, w, "minecraft:bat", mgl64.Vec3{15.5, 6, 0.5})
	ticks(w, 600)
	withMob(t, w, fish, func(m *Mob) {
		if m.Position().Sub(mgl64.Vec3{0.5, 3, 0.5}).Len() < 1 {
			t.Errorf("cod never swam: %v", m.Position())
		}
		if !m.s.inWater {
			t.Errorf("cod left the water: %v", m.Position())
		}
	})
	withMob(t, w, bat, func(m *Mob) {
		if m.Position()[1] < 2 {
			t.Errorf("bat fell to the floor: %v", m.Position())
		}
	})
}
