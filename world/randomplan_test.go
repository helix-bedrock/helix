package world

import (
	"slices"
	"testing"
	"time"

	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// TestPlannedRandomTicksMatch checks that random ticks planned on several goroutines run the
// same blocks in the same order as drawing them one by one, also for a chunk changed after
// the plan and with chunks of different heights.
func TestPlannedRandomTicksMatch(t *testing.T) {
	reg := vanillaRegistry(t)
	tt := &testTicker{air: reg.Blocks.Air()}
	d, db := openDimWith(t, reg, t.TempDir(), func(c *Config) { c.Ticker = tt; c.ParallelMin = 1 })
	defer func() { _ = db.Close() }()
	defer func() { _ = d.Close() }()
	v := d.AddViewer(func(packet.Packet) {}, func() {})
	defer v.Close()
	v.Move(mcdb.ChunkPos{}, 2)
	select {
	case <-v.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("zone never loaded")
	}
	stone, _ := reg.Blocks.Block("minecraft:stone")
	runAt(t, d, mcdb.ChunkPos{}, func(r *Region) {
		// Taller chunks draw more positions, so the generator's jumps differ per chunk.
		r.SetBlock(cube.Pos{3, 40, 3}, 0, stone)
		r.SetBlock(cube.Pos{-12, 10, 5}, 0, stone)
		const now, speed = -7, 3
		take := func() []cube.Pos {
			tt.mu.Lock()
			defer tt.mu.Unlock()
			out := tt.random
			tt.random = nil
			return out
		}
		take()
		start := r.lcg
		r.planRandomTicks(now, speed)
		if len(r.plans) < 2 {
			t.Fatalf("%d chunks planned, want several", len(r.plans))
		}
		order := make([]*Chunk, len(r.plans))
		for i := range r.plans {
			order[i] = r.plans[i].c
		}
		// A change after the plan: that chunk reads its blocks again.
		r.SetBlock(cube.Pos{1, -60, 1}, 0, reg.Blocks.Air())
		for _, c := range order {
			if !r.runPlannedTicks(c, now, speed) {
				t.Fatalf("chunk %v fell back to drawing one by one", c.pos)
			}
		}
		planned, after := take(), r.lcg

		r.lcg = start
		for _, c := range order {
			r.randomTicks(c, speed)
		}
		if one := take(); !slices.Equal(planned, one) {
			t.Fatalf("planned ticks differ from one by one: %d against %d", len(planned), len(one))
		}
		if r.lcg != after {
			t.Fatalf("generator %d after the plan, %d one by one", after, r.lcg)
		}
		clear(r.plans)
		r.plans = r.plans[:0]
	})
}
