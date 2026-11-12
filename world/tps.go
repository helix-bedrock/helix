package world

import (
	"time"
)

// MaxTPS is the tick rate everything in the world aims for: 20 ticks a second.
const MaxTPS = float64(time.Second / TickDuration)

// tickWindow is how many ticks a TickMeter remembers: 5 seconds at full speed.
const tickWindow = 100

// stallTicks is how many tick lengths may pass without a tick before a TickMeter counts
// the wait itself, so something that stopped ticking shows a falling rate at once.
const stallTicks = 2

// TickMeter works out a tick rate (TPS) and the mean tick time (MSPT) from the last
// tickWindow ticks. Region ticks use one, and so does each player for its client's ticks.
// The zero value is ready. It is not safe for concurrent use: its owner guards it.
type TickMeter struct {
	at   [tickWindow]int64
	took [tickWindow]time.Duration
	n    int
	next int
}

// Add records a tick that began at start and ran for took (0 when unknown).
func (m *TickMeter) Add(start time.Time, took time.Duration) {
	m.at[m.next] = start.UnixNano()
	m.took[m.next] = took
	m.next = (m.next + 1) % tickWindow
	m.n = min(m.n+1, tickWindow)
}

// Reset forgets every tick.
func (m *TickMeter) Reset() { *m = TickMeter{} }

// TPS is the ticks per second over the remembered ticks, at most MaxTPS. A meter with
// fewer than two ticks reads MaxTPS. If no tick came for longer than stallTicks tick
// lengths before now, the rate is no more than one tick per that wait.
func (m *TickMeter) TPS(now time.Time) float64 {
	if m.n < 2 {
		return MaxTPS
	}
	oldest := m.at[(m.next-m.n+tickWindow)%tickWindow]
	newest := m.at[(m.next-1+tickWindow)%tickWindow]
	tps := MaxTPS
	if span := newest - oldest; span > 0 {
		tps = float64(m.n-1) * float64(time.Second) / float64(span)
	}
	if gap := now.UnixNano() - newest; gap > int64(stallTicks*TickDuration) {
		tps = min(tps, float64(time.Second)/float64(gap))
	}
	return min(tps, MaxTPS)
}

// MSPT is the mean time the remembered ticks ran for, 0 with none.
func (m *TickMeter) MSPT() time.Duration {
	if m.n == 0 {
		return 0
	}
	var sum time.Duration
	for i := range m.n {
		sum += m.took[(m.next-1-i+tickWindow)%tickWindow]
	}
	return sum / time.Duration(m.n)
}

// TPS is the region's ticks per second over its last 5 seconds, at most MaxTPS: below it
// when its ticks run over TickDuration. A region made by a split starts at MaxTPS. Runs:
// anywhere.
func (r *Region) TPS() float64 {
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	return r.meter.TPS(time.Now())
}

// MSPT is the mean time the region's last ticks took. Runs: anywhere.
func (r *Region) MSPT() time.Duration {
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	return r.meter.MSPT()
}

// TPS is the mean TPS of the dimension's regions, MaxTPS with none. Runs: anywhere.
func (d *Dimension) TPS() float64 {
	sum, n := d.SumTPS()
	if n == 0 {
		return MaxTPS
	}
	return sum / float64(n)
}

// SumTPS is the sum of the TPS of the dimension's regions and how many there are, for
// averages over several dimensions. Runs: anywhere.
func (d *Dimension) SumTPS() (float64, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	sum := 0.0
	for _, r := range d.regions {
		sum += r.meter.TPS(now)
	}
	return sum, len(d.regions)
}
