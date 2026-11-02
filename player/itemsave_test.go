package player

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/mcdb"
)

// TestItemSavedWithChunk runs a real region: an item's chunk unloads, the item
// is in the store, and it comes back with its age when the chunk loads again.
func TestItemSavedWithChunk(t *testing.T) {
	p := testPlayer(t)
	reg := p.s.reg
	db, err := mcdb.Config{Blocks: reg.Blocks}.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	r, _ := mcdb.Overworld.Range()
	flat, err := world.ParseFlat("", reg.Blocks, reg.Biomes, r)
	if err != nil {
		t.Fatal(err)
	}
	conf := world.DefaultConfig()
	conf.Store, conf.Blocks, conf.Biomes, conf.Generator = db, reg.Blocks, reg.Biomes, flat
	d, err := world.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	var back atomic.Pointer[ItemActor]
	d.SetEntityLoader(func(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
		took := p.s.loadEntity(r, at, saved)
		if took {
			for _, e := range r.AppendEntitiesIn(nil, 0, 0, 15, 15) {
				if it, ok := e.Owner().(*ItemActor); ok {
					back.Store(it)
				}
			}
		}
		return took
	})

	at := world.ChunkPos(8, 8)
	ticket := d.KeepLoaded(world.Chunks(at), true)
	it := p.s.spawnItem(d, ItemStack{Name: "minecraft:emerald", Count: 3}, mgl32.Vec3{8.5, 40, 8.5}, mgl32.Vec3{}, world.Rotation{}, dropPickupDelay, dropThrowTime)
	time.Sleep(500 * time.Millisecond)
	ticket.Release()

	var col *chunk.Column
	deadline := time.Now().Add(15 * time.Second)
	for {
		col, err = db.LoadColumn(at, mcdb.Overworld)
		if err == nil && len(col.Entities) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("item not saved: %v, %+v", err, col)
		}
		time.Sleep(20 * time.Millisecond)
	}
	data := col.Entities[0].Data
	if data["identifier"] != itemIdentifier || col.Entities[0].ID != it.entity.UniqueID() {
		t.Fatalf("saved %+v", data)
	}
	age, _ := data["Age"].(int16)
	if age <= 0 {
		t.Fatalf("saved age %d", age)
	}

	again := d.KeepLoaded(world.Chunks(at), true)
	defer again.Release()
	for back.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("item not loaded back")
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := back.Load()
	if got.stack.Name != "minecraft:emerald" || got.stack.Count != 3 || got.age < int32(age) || got.pickupDelay != 0 || got.throwTime != 0 {
		t.Fatalf("loaded %+v", got)
	}
	if got.entity.UniqueID() != it.entity.UniqueID() {
		t.Fatal("unique ID changed")
	}
}
