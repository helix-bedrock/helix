package world

import (
	"testing"

	"github.com/helix-bedrock/world/cube"
)

// benchQueueTicks is a full chunk section's worth of waiting ticks: far more than a real
// contraption keeps, so the heap is deep and every swap writes the index.
const benchQueueTicks = 16 * 16 * 64

// fillBenchQueue fills q with one tick per position, due over the next 64 ticks.
func fillBenchQueue(q *tickQueue) {
	for i := range benchQueueTicks {
		pos := cube.Pos{i & 15, i >> 8, i >> 4 & 15}
		q.push(pos, 1, 1, int64(i%64), 0)
	}
}

// BenchmarkTickQueueCycle runs a big queue in steady state: each op takes the earliest tick
// and asks for it again later, as a clock does. Must report 0 allocs/op.
func BenchmarkTickQueueCycle(b *testing.B) {
	var q tickQueue
	fillBenchQueue(&q)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		t := q.pop()
		q.push(t.pos, t.block, t.typ, t.time+64, 0)
	}
}

// BenchmarkTickQueueMerge asks again for ticks already waiting, at earlier and later times,
// as a contraption spamming one block does. Must report 0 allocs/op.
func BenchmarkTickQueueMerge(b *testing.B) {
	var q tickQueue
	fillBenchQueue(&q)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		j := i % benchQueueTicks
		pos := cube.Pos{j & 15, j >> 8, j >> 4 & 15}
		q.push(pos, 1, 1, int64(i%128)-32, 0)
	}
}
