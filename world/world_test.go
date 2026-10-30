package world

import (
	"sync"
	"testing"
	"time"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

func vanillaRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	s := registry.NewSetup(nil, "minecraft")
	if err := registry.Vanilla(s); err != nil {
		t.Fatal(err)
	}
	r, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// openDim opens the overworld of the world in dir as a flat world.
func openDim(t *testing.T, reg *registry.Registry, dir string) (*Dimension, *mcdb.DB) {
	t.Helper()
	db, err := mcdb.Config{Blocks: reg.Blocks}.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := mcdb.Overworld.Range()
	flat, err := ParseFlat("", reg.Blocks, reg.Biomes, r)
	if err != nil {
		t.Fatal(err)
	}
	conf := DefaultConfig()
	conf.Store, conf.Blocks, conf.Biomes, conf.Generator = db, reg.Blocks, reg.Biomes, flat
	d, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	return d, db
}

// onTick runs fn on d's tick goroutine and waits for it.
func onTick(d *Dimension, fn func()) {
	done := make(chan struct{})
	d.post(func() { fn(); close(done) })
	<-done
}

func TestViewerAndSave(t *testing.T) {
	reg := vanillaRegistry(t)
	dir := t.TempDir()
	d, db := openDim(t, reg, dir)

	var mu sync.Mutex
	sent := map[protocol.ChunkPos]*packet.LevelChunk{}
	v := d.AddViewer(func(pk *packet.LevelChunk) {
		mu.Lock()
		sent[pk.Position] = pk
		mu.Unlock()
	}, func() {})
	const radius = 6
	v.Move(mcdb.ChunkPos{0, 0}, radius)
	select {
	case <-v.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("simulation zone never sent")
	}
	// The whole view arrives within a few ticks.
	deadline := time.Now().Add(10 * time.Second)
	want := len(chunksInRadius(nil, mcdb.ChunkPos{}, radius))
	for {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sent %d chunks, want %d", n, want)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Only the simulation zone stays loaded.
	onTick(d, func() {
		simChunks := len(chunksInRadius(nil, mcdb.ChunkPos{}, d.conf.SimulationDistance))
		if len(d.chunks) != simChunks {
			t.Errorf("%d chunks loaded, want the simulation zone's %d", len(d.chunks), simChunks)
		}
	})

	// Flat world: grass at -61 is in the lowest sub chunk, air above.
	resp := v.SubChunks(&packet.SubChunkRequest{
		Position: protocol.SubChunkPos{0, -4, 0},
		Offsets:  []protocol.SubChunkOffset{{0, 0, 0}, {0, 1, 0}, {0, 30, 0}, {40, 0, 0}},
	})
	results := []byte{protocol.SubChunkResultSuccess, protocol.SubChunkResultSuccessAllAir,
		protocol.SubChunkResultIndexOutOfBounds, protocol.SubChunkResultChunkNotFound}
	for i, e := range resp.SubChunkEntries {
		if e.Result != results[i] {
			t.Errorf("entry %d: result %d, want %d", i, e.Result, results[i])
		}
	}
	if e := resp.SubChunkEntries[0]; e.HeightMapType != protocol.HeightMapDataHasData {
		t.Errorf("height map type %d, want HasData", e.HeightMapType)
	}
	if e := resp.SubChunkEntries[1]; e.HeightMapType != protocol.HeightMapDataTooLow {
		t.Errorf("height map type above the ground %d, want TooLow", e.HeightMapType)
	}

	stone, _ := reg.Blocks.Block("minecraft:stone")
	onTick(d, func() {
		if !d.setBlock(cube.Pos{1, 100, 1}, 0, stone) {
			t.Error("chunk at the centre not loaded")
		}
		if d.setBlock(cube.Pos{16 * 5, 100, 0}, 0, stone) {
			t.Error("chunk outside the simulation zone is loaded")
		}
	})
	v.Close()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := mcdb.Config{Blocks: reg.Blocks}.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	col, err := db.LoadColumn(mcdb.ChunkPos{0, 0}, mcdb.Overworld)
	if err != nil {
		t.Fatal(err)
	}
	if got := col.Chunk.Block(1, 100, 1, 0); got != stone {
		t.Fatalf("saved block %s, want stone", reg.Blocks.Name(got))
	}
	grass, _ := reg.Blocks.Block("minecraft:grass_block")
	if got := col.Chunk.Block(0, -61, 0, 0); got != grass {
		t.Fatalf("block at -61 is %s, want grass", reg.Blocks.Name(got))
	}
	if _, err := db.LoadColumn(mcdb.ChunkPos{1, 0}, mcdb.Overworld); err == nil {
		t.Fatal("an unchanged generated chunk was saved")
	}
}

func TestNetCache(t *testing.T) {
	c := newNetCache(100)
	for i := range int32(5) {
		c.put(mcdb.ChunkPos{i}, &NetChunk{pos: mcdb.ChunkPos{i}, size: 30})
	}
	if c.used > 100 || c.get(mcdb.ChunkPos{0}) != nil || c.get(mcdb.ChunkPos{4}) == nil {
		t.Fatalf("cache kept the wrong chunks (used %d)", c.used)
	}
}
