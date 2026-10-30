package world

import (
	"sync"
	"testing"
	"time"

	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// testTicker records the ticks it is given; every block but air takes
// random ticks.
type testTicker struct {
	air uint32
	mu  sync.Mutex
	// tried counts the layer 0 blocks asked about for a random tick.
	tried  int
	queued []cube.Pos
	random []cube.Pos
}

func (tt *testTicker) RandomTicks(block uint32, layer uint8) bool {
	if layer == 0 {
		tt.mu.Lock()
		tt.tried++
		tt.mu.Unlock()
	}
	return block != tt.air
}

func (tt *testTicker) RandomTick(_ *Region, pos cube.Pos, _ uint32, _ uint8) {
	tt.mu.Lock()
	tt.random = append(tt.random, pos)
	tt.mu.Unlock()
}

func (tt *testTicker) QueuedTick(_ *Region, pos cube.Pos, _ uint32) {
	tt.mu.Lock()
	tt.queued = append(tt.queued, pos)
	tt.mu.Unlock()
}

func (tt *testTicker) queuedTicks() []cube.Pos {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	return append([]cube.Pos(nil), tt.queued...)
}

func TestTickQueueOrder(t *testing.T) {
	var q tickQueue
	q.push(cube.Pos{1}, 0, 5, 0)
	q.push(cube.Pos{2}, 0, 3, 1)
	q.push(cube.Pos{3}, 0, 3, -1)
	q.push(cube.Pos{4}, 0, 5, 0)
	q.push(cube.Pos{5}, 0, 3, 1)
	var got []int
	for len(q.ticks) > 0 {
		got = append(got, q.pop().pos[0])
	}
	want := []int{3, 2, 5, 1, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// TestBlockTicks checks queued ticks (order, the block check, the cap of
// 100 a tick), random ticks (BDS's count) and that queues are saved.
func TestBlockTicks(t *testing.T) {
	reg := vanillaRegistry(t)
	dir := t.TempDir()
	tt := &testTicker{air: reg.Blocks.Air()}
	d, db := openDimWith(t, reg, dir, func(c *Config) { c.Ticker = tt })
	v := d.AddViewer(func(packet.Packet) {}, func() {})
	v.Move(mcdb.ChunkPos{}, 2)
	select {
	case <-v.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("zone never loaded")
	}
	stone, _ := reg.Blocks.Block("minecraft:stone")
	dirt, _ := reg.Blocks.Block("minecraft:dirt")
	a, b := cube.Pos{1, -55, 1}, cube.Pos{2, -55, 2}
	runAt(t, d, mcdb.ChunkPos{}, func(r *Region) {
		r.SetBlock(a, 0, stone)
		r.SetBlock(b, 0, stone)
		r.ScheduleTick(a, stone, 2, 0)
		r.ScheduleTick(b, stone, 2, -1)
		r.ScheduleTick(a, dirt, 1, 0) // another block: never runs
	})
	waitFor(t, "the queued ticks", func() bool { return len(tt.queuedTicks()) >= 2 })
	time.Sleep(3 * TickDuration)
	if got := tt.queuedTicks(); len(got) != 2 || got[0] != b || got[1] != a {
		t.Fatalf("queued ticks %v, want %v then %v", got, b, a)
	}

	runAt(t, d, mcdb.ChunkPos{}, func(r *Region) {
		c := r.chunks[mcdb.ChunkPos{}]
		for i := range 150 {
			c.ticks.push(cube.Pos{i % 16, -40, i / 16}, stone, c.ticks.clock, 0)
		}
		r.runQueue(c, &c.ticks, c.ticks.clock+1)
		if len(c.ticks.ticks) != 50 {
			t.Errorf("%d ticks left after one run, want 50", len(c.ticks.ticks))
		}
		c.ticks.ticks = c.ticks.ticks[:0]

		// The flat world has blocks in its bottom sub chunk only: BDS
		// rounds 1 * 2.5 down to 2 random ticks, times the speed 3.
		tt.mu.Lock()
		tt.random, tt.tried = nil, 0
		tt.mu.Unlock()
		r.randomTicks(c, 3)
		tt.mu.Lock()
		got, tried := append([]cube.Pos(nil), tt.random...), tt.tried
		tt.mu.Unlock()
		if tried != 6 {
			t.Errorf("%d random tick positions at speed 3, want 6", tried)
		}
		for _, p := range got {
			if p[0] < 0 || p[0] > 15 || p[2] < 0 || p[2] > 15 || p[1] < -64 || p[1] > -49 {
				t.Errorf("random tick at %v, outside the bottom sub chunk", p)
			}
		}
		r.ScheduleTick(a, stone, 100000, 0)
	})
	v.Close()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	d, db = openDimWith(t, reg, dir, func(c *Config) { c.Ticker = tt })
	defer func() { _ = db.Close() }()
	defer func() { _ = d.Close() }()
	runAt(t, d, mcdb.ChunkPos{}, func(r *Region) {
		if !r.HasScheduledTick(a, stone) {
			t.Error("the scheduled tick was not saved")
		}
	})
}
