package sim

import (
	"slices"
	"sync"
	"time"
)

// maxLatencies bounds the delays kept per run; more are counted, not kept.
const maxLatencies = 1 << 20

// latencies are the delays from a bot sending an action to the server's answer reaching
// it, kept while measuring.
type latencies struct {
	mu      sync.Mutex
	on      bool
	took    []time.Duration
	dropped int
}

// add keeps one delay, if measuring.
func (l *latencies) add(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.on {
		return
	}
	if len(l.took) == maxLatencies {
		l.dropped++
		return
	}
	l.took = append(l.took, d)
}

// setOn starts or stops keeping delays.
func (l *latencies) setOn(on bool) {
	l.mu.Lock()
	l.on = on
	l.mu.Unlock()
}

// spread is the median, 99th percentile and worst delay, and how many there were.
func (l *latencies) spread() (median, p99, worst time.Duration, n int) {
	l.mu.Lock()
	took := slices.Clone(l.took)
	l.mu.Unlock()
	if len(took) == 0 {
		return 0, 0, 0, 0
	}
	slices.Sort(took)
	return took[len(took)/2], took[len(took)*99/100], took[len(took)-1], len(took)
}
