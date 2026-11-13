package player

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

func TestMobSavesAndLoads(t *testing.T) {
	p, d, y := cartWorld(t, "minecraft:stone", nil)
	p.s.setUpMobs()
	o, ok := p.s.SpawnMob(d, "minecraft:cow", mgl32.Vec3{10.5, float32(y) + 1, 10.5}, 30)
	if !ok {
		t.Skip("no cow definition")
	}
	waitRegion(t, d, func(*world.Region) bool { return o.entity != nil && o.entity.Region() != nil })
	onRegion(d, func(r *world.Region) {
		o.m.SetNameTag("Bessie")
		o.m.SetPersistent()
		if _, fed := o.m.Feed(playerActor{p}, mob.HeldItem{Name: "minecraft:wheat"}); !fed {
			t.Error("cow did not take wheat")
		}
		saved, ok := o.saveEntity(o.entity)
		if love, _ := saved.Data["InLove"].(int32); love <= 0 {
			t.Errorf("saved InLove %v", saved.Data["InLove"])
		}
		if !ok || saved.Data["identifier"] != "minecraft:cow" || saved.Data["CustomName"] != "Bessie" || saved.Data["Persistent"] != uint8(1) {
			t.Fatalf("saved %v", saved.Data)
		}
		if !p.s.loadEntity(r, mcdb.ChunkPos{}, chunk.Entity{ID: 99, Data: saved.Data}) {
			t.Fatal("did not load")
		}
	})
	waitRegion(t, d, func(r *world.Region) bool {
		var buf [16]*world.Entity
		for _, e := range r.AppendEntitiesIn(buf[:0], 0, 0, 16, 16) {
			if m, ok := e.Owner().(*Mob); ok && e.UniqueID() == 99 {
				return m.m.NameTag() == "Bessie" && m.m.Persistent()
			}
		}
		return false
	})
}

func TestSpawnerRoundTrip(t *testing.T) {
	p, _, _ := cartWorld(t, "minecraft:stone", nil)
	sp := p.s.loadSpawner(map[string]any{"id": "MobSpawner", "EntityIdentifier": "minecraft:zombie", "Delay": int16(5)})
	data := sp.SaveData(cube.Pos{1, 2, 3})
	if data["EntityIdentifier"] != "minecraft:zombie" || data["Delay"] != int16(5) || data["MaxNearbyEntities"] != int16(spawnerMaxNearby) {
		t.Fatalf("saved %v", data)
	}
	if spawnEggMob("minecraft:evoker_spawn_egg") != "minecraft:evocation_illager" || spawnEggMob("minecraft:cow_spawn_egg") != "minecraft:cow" {
		t.Error("spawn egg names")
	}
}
