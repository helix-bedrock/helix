package player

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// TestItemFallsAndIsTouched runs a real region: a dropped item falls to the
// ground, its delays end, and the player standing by it touches it.
func TestItemFallsAndIsTouched(t *testing.T) {
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

	var touched atomic.Pointer[ItemActor]
	p.s.game = &gameplay{}
	p.s.game.sys.PickUp = func(_ *Player, i *ItemActor) { touched.Store(i) }
	p.attributes = DefaultAttributes()
	feetY := float32(-60)
	pos := mgl32.Vec3{8, feetY + eyeHeight, 8}
	p.viewer = d.AddViewer(func(packet.Packet) {}, func() {})
	defer p.viewer.Close()
	p.viewer.Move(world.ChunkPos(8, 8), 2)
	p.entity = d.AddEntity(world.EntityConfig{
		UniqueID: 1, RuntimeID: p.s.runtimeIDs.Add(1), Pos: pos, Viewer: p.viewer,
		Input: func(*world.Entity) { p.tickPickup() },
	})
	it := p.s.spawnItem(d, ItemStack{Name: "minecraft:emerald", Count: 3}, mgl32.Vec3{8.5, feetY + 1.12, 8.5}, mgl32.Vec3{}, world.Rotation{}, dropPickupDelay, dropThrowTime)

	deadline := time.Now().Add(15 * time.Second)
	for touched.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatalf("never touched: delay %d, throw %d, age %d", it.pickupDelay, it.throwTime, it.age)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if touched.Load() != it {
		t.Fatal("touched another item")
	}
}

// flowWorld is a few blocks for the flow to read; the rest is air.
type flowWorld struct {
	air    uint32
	blocks map[cube.Pos]uint32
}

func (w flowWorld) Block(pos cube.Pos, layer uint8) (uint32, bool) {
	if b, ok := w.blocks[pos]; ok && layer == 0 {
		return b, true
	}
	return w.air, true
}

// TestLiquidFlow checks water runs from the deep side to the shallow side, and
// an item is pushed that way.
func TestLiquidFlow(t *testing.T) {
	p := testPlayer(t)
	reg := p.s.reg
	water := func(depth int32) uint32 {
		id, ok := reg.Blocks.StateID("minecraft:water", map[string]any{"liquid_depth": depth})
		if !ok {
			t.Fatalf("no water of depth %d", depth)
		}
		return id
	}
	stone, _ := reg.Blocks.Block("minecraft:stone")
	w := flowWorld{air: reg.Blocks.Air(), blocks: map[cube.Pos]uint32{}}
	for x := -3; x <= 3; x++ {
		for z := -3; z <= 3; z++ {
			w.blocks[cube.Pos{x, 63, z}] = stone
		}
	}
	w.blocks[cube.Pos{-1, 64, 0}] = water(1)
	w.blocks[cube.Pos{0, 64, 0}] = water(2)
	w.blocks[cube.Pos{1, 64, 0}] = water(3)
	f := liquid.Flow(reg.Blocks, w, cube.Pos{0, 64, 0}, 5)
	if f[0] <= 0 || f[1] != 0 {
		t.Fatalf("flow %v, want towards +x", f)
	}
	i := &ItemActor{s: p.s}
	i.applyFlow(w, mgl32.Vec3{0.5, 64.5, 0.5})
	if i.vel[0] <= 0 {
		t.Fatalf("velocity %v, want towards +x", i.vel)
	}
}

// TestItemsMergeInRegion runs a real region: two coal items lying together,
// one with a view, merge into one stack of 63 at their 25th tick.
func TestItemsMergeInRegion(t *testing.T) {
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
	p.s.game = &gameplay{}
	v := d.AddViewer(func(packet.Packet) {}, func() {})
	defer v.Close()
	v.Move(world.ChunkPos(8, 8), 2)

	at := mgl32.Vec3{8.5, -59.875, 8.5}
	a := p.s.spawnItem(d, ItemStack{Name: "minecraft:coal", Count: 30}, at, mgl32.Vec3{}, world.Rotation{}, chestPickupDelay, 0)
	b := p.s.spawnItem(d, ItemStack{Name: "minecraft:coal", Count: 33}, at.Add(mgl32.Vec3{0.6, 0, 0.6}), mgl32.Vec3{}, world.Rotation{}, chestPickupDelay, 0)
	var done atomic.Bool
	d.RunAt(world.ChunkPos(8, 8), func(*world.Region) { a.SetView(func(*Player, *ItemView) {}) })
	deadline := time.Now().Add(10 * time.Second)
	for !done.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("never merged: %d taken %v, %d taken %v", a.stack.Count, a.taken, b.stack.Count, b.taken)
		}
		d.RunAt(world.ChunkPos(8, 8), func(*world.Region) {
			done.Store(a.taken && !b.taken && b.stack.Count == 63 && b.view != nil)
		})
		time.Sleep(50 * time.Millisecond)
	}
}
