package mob

import (
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/world"
)

// TestMobsWalkBelowZero walks a mob on a floor at y=-61, like a superflat
// world's.
func TestMobsWalkBelowZero(t *testing.T) {
	w := mobWorld(t)
	w.Do(func(tx *world.Tx) {
		for x := -24; x <= 24; x++ {
			for z := -24; z <= 24; z++ {
				tx.SetBlock(cube.Pos{x, -61, z}, block.Grass{}, nil)
			}
		}
	})
	h := spawn(t, w, "minecraft:zombie", mgl64.Vec3{0.5, -60, 0.5})
	for range 20 {
		w.AdvanceTick()
	}
	withMob(t, w, h, func(m *Mob) { m.Navigator().MoveTo(mgl64.Vec3{8.5, -60, 0.5}, 1) })
	for range 200 {
		w.AdvanceTick()
		withMob(t, w, h, func(m *Mob) { m.s.goals = selector{} })
	}
	withMob(t, w, h, func(m *Mob) {
		if d := m.Position().Sub(mgl64.Vec3{8.5, -60, 0.5}).Len(); d > 1.5 {
			t.Fatalf("zombie at %v, %.1f blocks from its destination", m.Position(), d)
		}
	})
}

func TestMobsWanderOnTheirOwn(t *testing.T) {
	w := mobWorld(t)
	for _, id := range []string{"minecraft:zombie", "minecraft:cow", "minecraft:pig"} {
		start := mgl64.Vec3{0.5, 1, 0.5}
		h := spawn(t, w, id, start)
		for range 1200 {
			w.AdvanceTick()
		}
		withMob(t, w, h, func(m *Mob) {
			if m.Position().Sub(start).Len() < 1 {
				t.Errorf("%s never moved from %v", id, start)
			}
			_ = m.Close()
		})
	}
}

// TestZombieFindsVillager checks the zombie's own targeting: it must pick
// the villager as its target. (Hitting it is not checked: villagers flee
// zombies and may outrun this one.)
func TestZombieFindsVillager(t *testing.T) {
	w := mobWorld(t)
	zh := spawn(t, w, "minecraft:zombie", mgl64.Vec3{0.5, 1, 0.5})
	vh := spawn(t, w, "minecraft:villager_v2", mgl64.Vec3{6.5, 1, 0.5})
	for range 400 {
		w.AdvanceTick()
		var found bool
		withMob(t, w, zh, func(m *Mob) {
			tg, ok := m.Target()
			found = ok && tg.H() == vh
		})
		if found {
			return
		}
	}
	t.Fatal("zombie never targeted the villager")
}
