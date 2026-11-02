package liquid_test

import (
	"testing"
	"time"

	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// TestWaterSpreads pours a water source on a flat world and checks it
// spreads seven blocks out, one depth per block, and stays a source.
func TestWaterSpreads(t *testing.T) {
	s := registry.NewSetup(nil, "minecraft")
	if err := registry.Vanilla(s); err != nil {
		t.Fatal(err)
	}
	reg, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
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
	liq := liquid.New(reg.Blocks)
	conf := world.DefaultConfig()
	conf.Store, conf.Blocks, conf.Biomes, conf.Generator, conf.Ticker = db, reg.Blocks, reg.Biomes, flat, liq
	d, err := world.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	v := d.AddViewer(func(packet.Packet) {}, func() {})
	defer v.Close()
	v.Move(mcdb.ChunkPos{}, 2)
	select {
	case <-v.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("zone never loaded")
	}
	source, _ := liq.Block(liquid.Water, 0)
	at := cube.Pos{8, -60, 8}
	run := func(fn func(*world.Region)) {
		done := make(chan struct{})
		d.RunAt(mcdb.ChunkPos{}, func(r *world.Region) { fn(r); close(done) })
		<-done
	}
	run(func(r *world.Region) {
		r.SetBlock(at, 0, source)
		r.UpdateNeighbours(at)
		r.ScheduleTick(at, source, liquid.TickDelay(liquid.Water, false), 0)
	})
	depth := func(r *world.Region, p cube.Pos) int {
		b, _ := r.Block(p, 0)
		if k, d, ok := liq.Liquid(b); ok && k == liquid.Water {
			return d
		}
		return -1
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var got [9]int
		run(func(r *world.Region) {
			for i := range got {
				got[i] = depth(r, at.Add(cube.Pos{i, 0, 0}))
			}
		})
		if got == [9]int{0, 1, 2, 3, 4, 5, 6, 7, -1} {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("depths east of the source %v", got)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
