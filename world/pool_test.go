package world

import (
	"sync"
	"testing"

	"github.com/helix-bedrock/world/mcdb"
)

// TestPoolOrder checks that jobs of one chunk run in order, even when a later
// one is more urgent, and that urgent chunks go first.
func TestPoolOrder(t *testing.T) {
	p := newPool(1)
	var mu sync.Mutex
	var order []string
	note := func(s string) func() {
		return func() {
			mu.Lock()
			order = append(order, s)
			mu.Unlock()
		}
	}
	block := make(chan struct{})
	a, b, c := mcdb.ChunkPos{1, 1}, mcdb.ChunkPos{2, 2}, mcdb.ChunkPos{3, 3}
	// Hold the only worker so the rest queue up.
	p.do(mcdb.ChunkPos{9, 9}, prioNeeded, func() { <-block })
	p.do(a, prioAutoSave, note("a save"))
	p.do(b, viewPriority(100), note("b far"))
	p.do(c, viewPriority(1), note("c near"))
	p.do(a, prioNeeded, note("a load"))
	close(block)
	p.close()
	want := []string{"a save", "a load", "c near", "b far"}
	if len(order) != len(want) {
		t.Fatalf("ran %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ran %v, want %v", order, want)
		}
	}
}
