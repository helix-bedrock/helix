package world

import (
	"context"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block/cube"
)

// regionWorld returns an async world with one-chunk sections, so regions
// form and merge over short distances.
func regionWorld(t *testing.T) *World {
	t.Helper()
	w := Config{SectionSize: 1}.New()
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// load loads the chunk at pos on the region that owns or claims it.
func load(t *testing.T, w *World, pos ChunkPos) {
	t.Helper()
	done := make(chan struct{})
	w.postAt(pos, func(tx *Tx) {
		tx.Block(cube.Pos{int(pos[0]) << 4, 0, int(pos[1]) << 4})
		close(done)
	})
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out loading chunk")
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func chunksIn(w *World) int {
	n := 0
	for _, r := range w.Regions() {
		n += r.Chunks
	}
	return n
}

func TestFarChunksGetTheirOwnRegions(t *testing.T) {
	w := regionWorld(t)
	load(t, w, ChunkPos{0, 0})
	load(t, w, ChunkPos{100, 100})
	load(t, w, ChunkPos{-100, 50})
	if got := len(w.Regions()); got != 3 {
		t.Fatalf("%d regions for 3 far chunks, want 3", got)
	}
	if a, b := w.ownerOf(ChunkPos{0, 0}), w.ownerOf(ChunkPos{100, 100}); a == nil || a == b {
		t.Fatal("far chunks share a region")
	}
}

func TestNearbyRegionsMerge(t *testing.T) {
	w := regionWorld(t)
	load(t, w, ChunkPos{0, 0})
	load(t, w, ChunkPos{4, 0}) // further than the gap of 2 sections
	if len(w.Regions()) != 2 {
		t.Fatalf("want 2 regions, got %d", len(w.Regions()))
	}
	// A chunk within the gap of both regions makes them merge. The first
	// try finds it unavailable while they merge; then it loads.
	eventually(t, "merge", func() bool {
		load(t, w, ChunkPos{2, 0}) // within the gap of both
		return len(w.Regions()) == 1 && chunksIn(w) == 3
	})
	if w.ownerOf(ChunkPos{0, 0}) != w.ownerOf(ChunkPos{4, 0}) {
		t.Fatal("merged chunks have different owners")
	}
}

func TestRegionSplits(t *testing.T) {
	w := regionWorld(t)
	load(t, w, ChunkPos{0, 0})
	// The spawn region claims a far chunk itself, so one region owns two
	// groups of sections.
	done := make(chan struct{})
	w.postAt(ChunkPos{0, 0}, func(tx *Tx) {
		tx.Block(cube.Pos{50 << 4, 0, 0})
		close(done)
	})
	<-done
	if len(w.Regions()) != 1 {
		t.Fatalf("want 1 region before the split, got %d", len(w.Regions()))
	}
	<-w.exec(func(tx *Tx) { tx.r.trySplit() })
	eventually(t, "split", func() bool { return len(w.Regions()) == 2 && chunksIn(w) == 2 })
	if w.ownerOf(ChunkPos{0, 0}) == w.ownerOf(ChunkPos{50, 0}) {
		t.Fatal("split groups share an owner")
	}
}

func TestEntityAddedInAnotherRegionMovesThere(t *testing.T) {
	w := regionWorld(t)
	load(t, w, ChunkPos{0, 0})
	load(t, w, ChunkPos{100, 0})
	far := w.ownerOf(ChunkPos{100, 0})

	h := NewEntity(taskTestEntityType{}, taskTestEntityConfig{})
	done := make(chan struct{})
	w.postAt(ChunkPos{0, 0}, func(tx *Tx) {
		tx.AddEntityAt(h, mgl64.Vec3{100<<4 + 1, 0, 1})
		close(done)
	})
	<-done
	eventually(t, "entity handed over", func() bool { return w.regionOfEntity(h) == far })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var ran *region
	if _, err := CallEntity(ctx, h, func(tx *Tx, e Entity) (struct{}, error) {
		ran = tx.r
		return struct{}{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if ran != far {
		t.Fatal("entity task ran on the wrong region")
	}
}
