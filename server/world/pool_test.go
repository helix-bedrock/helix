package world

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/helix-bedrock/helix/server/hook"
)

// TestPoolRunsManyRegionsOnFewWorkers loads chunks in many far apart
// regions of two worlds sharing a one-worker pool: every one must still
// run.
func TestPoolRunsManyRegionsOnFewWorkers(t *testing.T) {
	p := NewPool(1)
	a, b := Config{SectionSize: 1, Pool: p}.New(), Config{SectionSize: 1, Pool: p}.New()
	for i := range 20 {
		load(t, a, ChunkPos{int32(i) * 50, 0})
		load(t, b, ChunkPos{0, int32(i) * 50})
	}
	if a.RegionCount() != 20 || b.RegionCount() != 20 {
		t.Fatalf("got %d and %d regions, want 20 each", a.RegionCount(), b.RegionCount())
	}
	_ = a.Close()
	_ = b.Close()
	p.Close()
}

// TestBusyRegionDoesNotStarveOthers keeps one region's queue full and
// checks another region on the same single worker still runs.
func TestBusyRegionDoesNotStarveOthers(t *testing.T) {
	p := NewPool(1)
	w := Config{SectionSize: 1, Pool: p}.New()
	defer p.Close()
	defer func() { _ = w.Close() }()
	load(t, w, ChunkPos{0, 0})
	load(t, w, ChunkPos{100, 0})
	var stop atomic.Bool
	var spin func(tx *Tx)
	spin = func(tx *Tx) {
		if !stop.Load() {
			w.postAt(ChunkPos{0, 0}, spin)
		}
	}
	for range poolBatch * 2 {
		w.postAt(ChunkPos{0, 0}, spin)
	}
	load(t, w, ChunkPos{100, 0})
	stop.Store(true)
}

func TestSlowTickEvent(t *testing.T) {
	ev := &Events{}
	got := make(chan *SlowTickEvent, 1)
	ev.SlowTick.Add(testOwner{}, func(e *SlowTickEvent) {
		select {
		case got <- e:
		default:
		}
	}, hook.Normal, false, nil)
	w := Config{Synchronous: true, Events: ev}.New()
	defer func() { _ = w.Close() }()
	w.Do(func(tx *Tx) {
		tx.r.checkBudget(tx, time.Second/20)
		select {
		case <-got:
			t.Error("SlowTickEvent for a tick within budget")
		default:
		}
		tx.r.checkBudget(tx, time.Second)
	})
	select {
	case e := <-got:
		if e.Tx == nil || e.Took != time.Second {
			t.Fatalf("bad event %+v", e)
		}
	default:
		t.Fatal("no SlowTickEvent")
	}
}
