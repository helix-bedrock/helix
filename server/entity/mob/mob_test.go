package mob

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/world"
)

func defs(t *testing.T) map[string]*Def {
	t.Helper()
	d, err := LoadDefs()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDefinitions(t *testing.T) {
	d := defs(t)
	z := d["minecraft:zombie"].spec(nil)
	if z.maxHealth != 20 || z.attack[0] != 3 || !z.hasFamily("undead") || !z.burnsInDaylight || !z.walks {
		t.Fatalf("zombie: %+v", z)
	}
	names := make([]string, 0, len(z.behaviours))
	for _, b := range z.behaviours {
		names = append(names, b.name)
	}
	for _, want := range []string{"melee_box_attack", "nearest_attackable_target", "random_stroll", "hurt_by_target"} {
		if !slices.Contains(names, want) {
			t.Errorf("zombie has no %s behaviour: %v", want, names)
		}
	}
	types := Types(d, func(id string) bool { return id == "minecraft:player" })
	if len(types) < 60 {
		t.Fatalf("only %d mob types", len(types))
	}
	for _, typ := range types {
		if typ.def.ID == "minecraft:player" || typ.def.ID == "minecraft:boat" {
			t.Errorf("%s is not a mob", typ.def.ID)
		}
	}
}

func TestSpawnEventPicksGroups(t *testing.T) {
	cow := &Type{def: defs(t)["minecraft:cow"]}
	adults := 0
	for i := range 100 {
		var data world.EntityData
		spawnConfig{t: cow, seed: [2]uint64{uint64(i), 7}}.Apply(&data)
		s := data.Data.(*state)
		if slices.Contains(s.groups, "minecraft:cow_adult") {
			adults++
		} else if !slices.Contains(s.groups, "minecraft:cow_baby") || !s.spec.baby {
			t.Fatalf("cow with neither adult nor baby group: %v", s.groups)
		}
	}
	// 95 in 100 are adults in vanilla.
	if adults < 80 || adults == 100 {
		t.Fatalf("%d adults in 100 cows", adults)
	}
}

func TestFilters(t *testing.T) {
	env := filterEnv{}
	cases := map[string]bool{
		`{"test":"is_family","subject":1,"operator":0,"value":"player"}`: false,
		`{"test":"is_family","subject":1,"operator":1,"value":"player"}`: false, // no subject: tests fail
		`{"test":"made_up","value":true}`:                                false,
		`{"any_of":[]}`:                                                  false,
		`[]`:                                                             true,
	}
	for f, want := range cases {
		if got := evalFilter(json.RawMessage(f), env); got != want {
			t.Errorf("%s: got %v, want %v", f, got, want)
		}
	}
}

// mobWorld returns a world with a stone floor at y=0 and every mob type.
func mobWorld(t *testing.T) *world.World {
	t.Helper()
	reg, err := Registry(entity.DefaultRegistry)
	if err != nil {
		t.Fatal(err)
	}
	w := world.Config{Synchronous: true, Entities: reg}.New()
	t.Cleanup(func() { _ = w.Close() })
	w.Do(func(tx *world.Tx) {
		for x := -24; x <= 24; x++ {
			for z := -24; z <= 24; z++ {
				tx.SetBlock(cube.Pos{x, 0, z}, block.Stone{}, nil)
			}
		}
	})
	// Night, so zombies do not burn.
	w.SetTime(18000)
	w.StopTime()
	return w
}

func spawn(t *testing.T, w *world.World, id string, pos mgl64.Vec3) *world.EntityHandle {
	t.Helper()
	var h *world.EntityHandle
	w.Do(func(tx *world.Tx) {
		typ, ok := tx.World().EntityRegistry().Lookup(id)
		if !ok {
			t.Fatalf("no %s", id)
		}
		h = typ.(*Type).Spawn(pos, 0)
		tx.AddEntity(h)
	})
	return h
}

func withMob(t *testing.T, w *world.World, h *world.EntityHandle, f func(m *Mob)) {
	t.Helper()
	w.Do(func(tx *world.Tx) {
		e, ok := h.Entity(tx)
		if !ok {
			t.Fatal("mob is gone")
		}
		f(e.(*Mob))
	})
}

func TestMobFallsAndWalksToTarget(t *testing.T) {
	w := mobWorld(t)
	h := spawn(t, w, "minecraft:zombie", mgl64.Vec3{0.5, 3, 0.5})
	for range 40 {
		w.AdvanceTick()
	}
	withMob(t, w, h, func(m *Mob) {
		if !m.OnGround() || m.Position()[1] != 1 {
			t.Fatalf("zombie did not land on the floor: %v on ground %v", m.Position(), m.OnGround())
		}
		m.Navigator().MoveTo(mgl64.Vec3{10.5, 1, 0.5}, 1)
	})
	for range 200 {
		w.AdvanceTick()
		withMob(t, w, h, func(m *Mob) {
			// Keep the goals from sending it elsewhere.
			m.s.goals = selector{}
		})
	}
	withMob(t, w, h, func(m *Mob) {
		if d := m.Position().Sub(mgl64.Vec3{10.5, 1, 0.5}).Len(); d > 1.5 {
			t.Fatalf("zombie at %v, %.1f blocks from its destination", m.Position(), d)
		}
	})
}

func TestZombieAttacksCow(t *testing.T) {
	w := mobWorld(t)
	zh := spawn(t, w, "minecraft:zombie", mgl64.Vec3{0.5, 1, 0.5})
	ch := spawn(t, w, "minecraft:cow", mgl64.Vec3{5.5, 1, 0.5})
	withMob(t, w, zh, func(m *Mob) {
		w.Do(func(tx *world.Tx) {
			cow, _ := ch.Entity(tx)
			m.SetTarget(cow)
		})
	})
	var hurt bool
	for range 400 {
		w.AdvanceTick()
		w.Do(func(tx *world.Tx) {
			if cow, ok := ch.Entity(tx); ok && cow.(*Mob).Health() < cow.(*Mob).MaxHealth() {
				hurt = true
			}
		})
		if hurt {
			break
		}
	}
	if !hurt {
		t.Fatal("zombie never hurt the cow it targeted")
	}
}

func TestDeathDropsLoot(t *testing.T) {
	w := mobWorld(t)
	h := spawn(t, w, "minecraft:cow", mgl64.Vec3{0.5, 1, 0.5})
	withMob(t, w, h, func(m *Mob) {
		m.TriggerEvent("minecraft:spawn_adult")
		m.Hurt(1000, entity.VoidDamageSource{})
		if !m.Dead() {
			t.Fatal("cow survived 1000 damage")
		}
	})
	for range 25 {
		w.AdvanceTick()
	}
	w.Do(func(tx *world.Tx) {
		if _, ok := h.Entity(tx); ok {
			t.Fatal("dead cow still in the world after its death animation")
		}
		items := 0
		for e := range tx.Entities() {
			if e.H().Type() == entity.ItemType {
				items++
			}
		}
		// Cows always drop beef (1-3), and maybe leather.
		if items == 0 {
			t.Fatal("dead cow dropped nothing")
		}
	})
}

func TestSaveAndLoad(t *testing.T) {
	cow := &Type{def: defs(t)["minecraft:cow"]}
	var saved world.EntityData
	spawnConfig{t: cow, seed: [2]uint64{1, 2}}.Apply(&saved)
	s := saved.Data.(*state)
	s.health.AddHealth(-3)
	m := cow.EncodeNBT(&world.EntityData{Data: s})
	var data world.EntityData
	cow.DecodeNBT(m, &data)
	got := data.Data.(*state)
	if !slices.Equal(got.groups, s.groups) || got.health.Health() != s.health.Health() {
		t.Fatalf("saved %v %.0f, loaded %v %.0f", s.groups, s.health.Health(), got.groups, got.health.Health())
	}
}
