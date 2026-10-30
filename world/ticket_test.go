package world

import (
	"slices"
	"testing"
	"time"

	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

func TestAreas(t *testing.T) {
	if n := len(slices.Collect(Box(0, 0, 31, 31))); n != 4 {
		t.Errorf("Box: %d chunks, want 4", n)
	}
	if n := len(slices.Collect(Chunks(mcdb.ChunkPos{}, mcdb.ChunkPos{1, 0}))); n != 2 {
		t.Errorf("Chunks: %d chunks, want 2", n)
	}
	// The 3 by 3 around the origin but the chunks whose nearest block is
	// 16 blocks away on one axis and at least 1 on the other.
	got := slices.Collect(Radius(0, 0, 16))
	if len(got) != 6 || slices.Contains(got, mcdb.ChunkPos{1, -1}) {
		t.Errorf("Radius: %v", got)
	}
}

// TestTickets checks that tickets load their chunks without a player, that
// only a ticking ticket ticks them, and that releasing unloads them.
func TestTickets(t *testing.T) {
	reg := vanillaRegistry(t)
	tt := &testTicker{air: reg.Blocks.Air()}
	d, db := openDimWith(t, reg, t.TempDir(), func(c *Config) { c.Ticker = tt })
	defer func() { _ = db.Close() }()
	defer func() { _ = d.Close() }()

	ticking := d.KeepLoaded(Box(0, 0, 31, 31), true)
	frozen := d.KeepLoaded(Box(160, 160, 191, 191), false)
	waitFor(t, "the tickets' chunks", func() bool { return loadedChunks(d) == 8 })

	stone, _ := reg.Blocks.Block("minecraft:stone")
	// Both in the middle of their ticket, so the chunks within 8 blocks
	// are loaded.
	a, b := cube.Pos{16, -55, 16}, cube.Pos{176, -55, 176}
	runAt(t, d, mcdb.ChunkPos{0, 0}, func(r *Region) {
		r.SetBlock(a, 0, stone)
		r.ScheduleTick(a, stone, 1, 0)
	})
	runAt(t, d, mcdb.ChunkPos{10, 10}, func(r *Region) {
		r.SetBlock(b, 0, stone)
		r.ScheduleTick(b, stone, 1, 0)
	})
	waitFor(t, "the ticking ticket's tick", func() bool { return slices.Contains(tt.queuedTicks(), a) })
	time.Sleep(3 * TickDuration)
	if slices.Contains(tt.queuedTicks(), b) {
		t.Error("a frozen ticket's chunk ticked")
	}

	ticking.Release()
	ticking.Release()
	waitFor(t, "the ticking ticket's chunks to unload", func() bool { return loadedChunks(d) == 4 })
	frozen.Release()
	waitFor(t, "every chunk to unload", func() bool { return loadedChunks(d) == 0 })
}
