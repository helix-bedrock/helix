package helix

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/hx"
	"github.com/helix-bedrock/world/hxdb"
	"github.com/helix-bedrock/world/lsm"
	"github.com/helix-bedrock/world/mcdb"
)

// TestBDSWorldStart converts a copy of the BDS world in HELIX_BDS_WORLD as a
// server start does, loads every chunk through the engine's provider, stores
// each back, checks what a reload gives, and exports it to BDS's format.
func TestBDSWorldStart(t *testing.T) {
	src := os.Getenv("HELIX_BDS_WORLD")
	if src == "" {
		t.Skip("HELIX_BDS_WORLD not set")
	}
	worlds := t.TempDir()
	dir := filepath.Join(worlds, "world")
	copyDir(t, src, dir)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	start := time.Now()
	if err := prepareWorld(log, dir); err != nil {
		t.Fatal(err)
	}
	t.Logf("converted in %v", time.Since(start).Round(time.Millisecond))
	if k, _ := kindOf(dir); k != worldHelix {
		t.Fatalf("world folder holds kind %d after conversion", k)
	}
	if k, _ := kindOf(dir + bdsSuffix); k != worldBDS {
		t.Fatal("the BDS world was not kept")
	}
	if _, err := os.Stat(dir + convertingSuffix); !os.IsNotExist(err) {
		t.Fatal("conversion folder left behind")
	}
	// A second start finds a Helix world and leaves it.
	if err := prepareWorld(log, dir); err != nil {
		t.Fatal(err)
	}

	s := registry.NewSetup(nil, "minecraft")
	if err := registry.Vanilla(s); err != nil {
		t.Fatal(err)
	}
	reg, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	type counts struct{ chunks, entities, blockEntities, blockEntityBytes int }
	walk := func(store bool) counts {
		db, err := hxdb.Config{Blocks: reg.Blocks, SkipBlockEntities: true}.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		var c counts
		for _, dim := range []hx.Dimension{hx.Overworld, hx.Nether, hx.End} {
			var all []hx.ChunkPos
			if err := db.World().EachChunk(dim, func(pos hx.ChunkPos) bool { all = append(all, pos); return true }); err != nil {
				t.Fatal(err)
			}
			for _, pos := range all {
				col, err := db.LoadColumn(mcdb.ChunkPos(pos), mcdb.Dimension(dim))
				if err != nil {
					t.Fatalf("chunk %v in %s: %v", pos, dim.Dir(), err)
				}
				c.chunks++
				c.entities += len(col.Entities)
				if err := db.EachBlockEntity(mcdb.ChunkPos(pos), mcdb.Dimension(dim), func(_ cube.Pos, raw []byte) error {
					c.blockEntities++
					c.blockEntityBytes += len(raw)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if store {
					// As the engine saves a chunk whose block entities did not change.
					col.KeepBlockEntities = true
					if err := db.StoreColumn(mcdb.ChunkPos(pos), mcdb.Dimension(dim), col); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	start = time.Now()
	first := walk(true)
	t.Logf("loaded and stored %+v in %v", first, time.Since(start).Round(time.Millisecond))
	second := walk(false)
	if first != second {
		t.Fatalf("after a save: %+v, before %+v", second, first)
	}

	out := filepath.Join(worlds, "exported")
	start = time.Now()
	if err := ExportBDSWorld(dir, out, nil); err != nil {
		t.Fatal(err)
	}
	t.Logf("exported in %v", time.Since(start).Round(time.Millisecond))
	db, err := lsm.Open(filepath.Join(out, "db"), lsm.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	orig, err := lsm.Open(filepath.Join(dir+bdsSuffix, "db"), lsm.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer orig.Close()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("keys: BDS world %d, exported %d; heap from the OS %d MB", countKeys(t, orig), countKeys(t, db), ms.HeapSys>>20)
}

func countKeys(t *testing.T, db *lsm.DB) int {
	t.Helper()
	it, err := db.NewIterator(nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for ok := it.First(); ok; ok = it.Next() {
		n++
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	return n
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
