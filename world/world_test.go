package world

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

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

// runAt runs fn on the region owning pos and waits for it.
func runAt(t *testing.T, d *Dimension, pos mcdb.ChunkPos, fn func(*Region)) {
	t.Helper()
	done := make(chan struct{})
	d.RunAt(pos, func(r *Region) { fn(r); close(done) })
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("RunAt %v never ran", pos)
	}
}

// waitFor polls cond until it holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// loadedChunks counts the chunks loaded in every region.
func loadedChunks(d *Dimension) int {
	n := 0
	for _, r := range d.Regions() {
		n += r.Chunks
	}
	return n
}

func TestViewerAndSave(t *testing.T) {
	reg := vanillaRegistry(t)
	dir := t.TempDir()
	d, db := openDim(t, reg, dir)

	var mu sync.Mutex
	sent := map[protocol.ChunkPos]*packet.LevelChunk{}
	v := d.AddViewer(func(p packet.Packet) {
		if pk, ok := p.(*packet.LevelChunk); ok {
			mu.Lock()
			sent[pk.Position] = pk
			mu.Unlock()
		}
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

	// As BDS: no sub chunks in LevelChunk, the count to request as limit.
	mu.Lock()
	pk := sent[protocol.ChunkPos{0, 0}]
	mu.Unlock()
	if limit, ok := pk.SubChunkLimit.Value(); pk.SubChunkCount != 0 || !ok || limit != 1 {
		t.Errorf("LevelChunk count %d, limit %v, want 0 and 1", pk.SubChunkCount, pk.SubChunkLimit)
	}

	// Only the simulation zone stays loaded.
	simChunks := len(chunksInRadius(nil, mcdb.ChunkPos{}, d.conf.SimulationDistance))
	waitFor(t, "the simulation zone alone loaded", func() bool { return loadedChunks(d) == simChunks })

	// Flat world: grass at -61 is in the lowest sub chunk, air above.
	resp := v.SubChunks(&packet.SubChunkRequest{
		Position: protocol.SubChunkPos{0, -4, 0},
		Offsets:  []protocol.SubChunkOffset{{0, 0, 0}, {0, 1, 0}, {0, 30, 0}, {40, 0, 0}},
	})
	results := []byte{protocol.SubChunkResultSuccess, protocol.SubChunkResultSuccess,
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
	runAt(t, d, mcdb.ChunkPos{0, 0}, func(r *Region) {
		if !r.SetBlock(cube.Pos{1, 100, 1}, 0, stone) {
			t.Error("chunk at the centre not loaded")
		}
		if r.SetBlock(cube.Pos{16 * 5, 100, 0}, 0, stone) {
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

// TestRegions checks that regions merge when players come close and split
// when they part, and that RunAt loads a chunk nobody holds.
func TestRegions(t *testing.T) {
	reg := vanillaRegistry(t)
	d, db := openDim(t, reg, t.TempDir())
	defer func() { _ = db.Close() }()
	defer func() { _ = d.Close() }()
	nop := func(packet.Packet) {}
	a := d.AddViewer(nop, func() {})
	b := d.AddViewer(nop, func() {})
	defer a.Close()
	defer b.Close()
	count := func(n int) func() bool { return func() bool { return len(d.Regions()) == n } }

	a.Move(mcdb.ChunkPos{0, 0}, 2)
	b.Move(mcdb.ChunkPos{1000, 0}, 2)
	waitFor(t, "two regions", count(2))
	b.Move(mcdb.ChunkPos{3, 0}, 2)
	waitFor(t, "a merge", count(1))
	waitFor(t, "both zones loaded", func() bool {
		r := d.Regions()
		return len(r) == 1 && r[0].Viewers == 2 && r[0].Chunks > 0
	})
	b.Move(mcdb.ChunkPos{1000, 0}, 2)
	waitFor(t, "a split", count(2))

	stone, _ := reg.Blocks.Block("minecraft:stone")
	runAt(t, d, mcdb.ChunkPos{-500, 0}, func(r *Region) {
		if !r.SetBlock(cube.Pos{-500 * 16, 0, 0}, 0, stone) {
			t.Error("RunAt ran before its chunk was loaded")
		}
	})
	waitFor(t, "the RunAt region to go", count(2))
}

// recorder keeps the entity packets a viewer was sent.
type recorder struct {
	mu  sync.Mutex
	pks []packet.Packet
}

func (r *recorder) send(pk packet.Packet) {
	if _, chunk := pk.(*packet.LevelChunk); chunk {
		return
	}
	r.mu.Lock()
	r.pks = append(r.pks, pk)
	r.mu.Unlock()
}

// has reports whether a packet matching fn was sent.
func (r *recorder) has(fn func(packet.Packet) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.pks, fn)
}

// TestEntities checks that players see each other within BDS's 57-chunk
// disc, or one set while running, get each other's movement, and are told
// when one leaves.
func TestEntities(t *testing.T) {
	reg := vanillaRegistry(t)
	d, db := openDim(t, reg, t.TempDir())
	defer func() { _ = db.Close() }()
	defer func() { _ = d.Close() }()
	add := func(id uint64, pos mgl32.Vec3) (*Entity, *recorder, *Viewer) {
		rec := &recorder{}
		v := d.AddViewer(rec.send, func() {})
		v.Move(chunkOf(pos), 2)
		e := d.AddEntity(EntityConfig{
			UniqueID: int64(id), RuntimeID: id, Pos: pos, Viewer: v, //nolint:gosec // test IDs
			Spawn: func(e *Entity) []packet.Packet {
				return []packet.Packet{&packet.AddPlayer{EntityRuntimeID: e.RuntimeID()}}
			},
			Move: func(e *Entity) packet.Packet {
				return &packet.MovePlayer{EntityRuntimeID: e.RuntimeID(), Position: e.Position()}
			},
		})
		return e, rec, v
	}
	a, recA, va := add(1, mgl32.Vec3{0, 0, 0})
	b, recB, vb := add(2, mgl32.Vec3{3 * 16, 0, 0})
	defer va.Close()
	defer vb.Close()
	sees := func(rec *recorder, id uint64) func() bool {
		return func() bool {
			return rec.has(func(p packet.Packet) bool { pk, ok := p.(*packet.AddPlayer); return ok && pk.EntityRuntimeID == id })
		}
	}
	waitFor(t, "a to see b", sees(recA, 2))
	waitFor(t, "b to see a", sees(recB, 1))

	move := func(pos mgl32.Vec3) {
		runAt(t, d, mcdb.ChunkPos{3, 0}, func(*Region) { b.Apply(Movement{Pos: pos}) })
	}
	move(mgl32.Vec3{3*16 + 1, 0, 0})
	waitFor(t, "a to get b's movement", func() bool {
		return recA.has(func(p packet.Packet) bool { pk, ok := p.(*packet.MovePlayer); return ok && pk.Position[0] == 3*16+1 })
	})
	if recB.has(func(p packet.Packet) bool { _, ok := p.(*packet.MovePlayer); return ok }) {
		t.Fatal("b was sent its own movement")
	}

	// Out of the disc: b at 5 chunks is removed for a.
	move(mgl32.Vec3{5 * 16, 0, 0})
	waitFor(t, "a to lose b", func() bool {
		return recA.has(func(p packet.Packet) bool { pk, ok := p.(*packet.RemoveActor); return ok && pk.EntityUniqueID == 2 })
	})

	// A wider disc set while running shows b again; one wider than the
	// gap between regions is refused.
	recA.mu.Lock()
	recA.pks = nil
	recA.mu.Unlock()
	if err := d.SetEntityViewRadius2(65); err == nil {
		t.Fatal("a disc wider than the gap between regions was accepted")
	}
	if err := d.SetEntityViewRadius2(36); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a to see b in a wider disc", sees(recA, 2))

	removed := make(chan struct{})
	b.conf.Removed = func(*Entity) { close(removed) }
	b.Remove()
	select {
	case <-removed:
	case <-time.After(10 * time.Second):
		t.Fatal("b never removed")
	}
	_ = a
}

// TestEntityHandOff checks that an entity cannot move into an unloaded
// chunk, goes to the region of the chunk it moved to, and unloads with its
// chunk.
func TestEntityHandOff(t *testing.T) {
	reg := vanillaRegistry(t)
	d, db := openDim(t, reg, t.TempDir())
	defer func() { _ = db.Close() }()
	defer func() { _ = d.Close() }()
	spawn := func(e *Entity) []packet.Packet {
		return []packet.Packet{&packet.AddPlayer{EntityRuntimeID: e.RuntimeID()}}
	}
	player := func(id uint64, chunkX int32) (*recorder, *Viewer) {
		rec := &recorder{}
		v := d.AddViewer(rec.send, func() {})
		v.Move(mcdb.ChunkPos{chunkX, 0}, 2)
		d.AddEntity(EntityConfig{UniqueID: int64(id), RuntimeID: id, Pos: mgl32.Vec3{float32(chunkX*16 + 8), 0, 8}, Viewer: v, Spawn: spawn}) //nolint:gosec // test IDs
		return rec, v
	}
	recA, va := player(1, 0)
	recB, vb := player(2, 100)
	defer va.Close()
	waitFor(t, "two regions", func() bool { return len(d.Regions()) == 2 })
	sees := func(rec *recorder, id uint64) func() bool {
		return func() bool {
			return rec.has(func(p packet.Packet) bool { pk, ok := p.(*packet.AddPlayer); return ok && pk.EntityRuntimeID == id })
		}
	}
	unloaded := make(chan struct{})
	mob := d.AddEntity(EntityConfig{
		UniqueID: 10, RuntimeID: 10, Pos: mgl32.Vec3{24, 0, 8}, Spawn: spawn,
		Unloaded: func(*Entity) { close(unloaded) },
	})
	waitFor(t, "a to see the mob", sees(recA, 10))

	var intoUnloaded, intoB bool
	runAt(t, d, mcdb.ChunkPos{1, 0}, func(*Region) {
		intoUnloaded = mob.Apply(Movement{Pos: mgl32.Vec3{50 * 16, 0, 8}})
		intoB = mob.Apply(Movement{Pos: mgl32.Vec3{101*16 + 8, 0, 8}})
	})
	if intoUnloaded || !intoB {
		t.Fatalf("moved into an unloaded chunk: %v, into b's region: %v", intoUnloaded, intoB)
	}
	waitFor(t, "b to see the mob", sees(recB, 10))
	waitFor(t, "a to lose the mob", func() bool {
		return recA.has(func(p packet.Packet) bool { pk, ok := p.(*packet.RemoveActor); return ok && pk.EntityUniqueID == 10 })
	})

	// b leaves: the mob's chunk unloads and the mob with it.
	vb.Close()
	select {
	case <-unloaded:
	case <-time.After(10 * time.Second):
		t.Fatal("the mob never unloaded with its chunk")
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
