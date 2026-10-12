package debug

import (
	"slices"
	"time"
)

// Tick measures one world tick. A nil *Tick does nothing.
type Tick struct {
	m      *Monitor
	world  string
	start  time.Time
	last   time.Time
	phases []Phase
	blocks map[string]*BlockTime
}

// Phase is one part of a tick: entities, scheduled updates and so on.
type Phase struct {
	Name string        `json:"name"`
	Took time.Duration `json:"took"`
}

// BlockTime is the time scheduled updates of one block type took.
type BlockTime struct {
	Name  string        `json:"name"`
	Count int           `json:"count"`
	Took  time.Duration `json:"took"`
}

// TickRecord is a finished tick.
type TickRecord struct {
	Start  time.Time     `json:"start"`
	Took   time.Duration `json:"took"`
	Slow   bool          `json:"slow"`
	Phases []Phase       `json:"phases"`
	Blocks []BlockTime   `json:"blocks,omitempty"`
}

type worldTicks struct {
	ticks []TickRecord // ring
	next  int
	full  bool
	slow  int64
	total int64
}

// StartTick starts measuring a tick of world. Call Phase after each part
// and End at the end.
func (m *Monitor) StartTick(world string) *Tick {
	if m == nil {
		return nil
	}
	now := time.Now()
	return &Tick{m: m, world: world, start: now, last: now}
}

// Phase records that the part called name just finished.
func (t *Tick) Phase(name string) {
	if t == nil {
		return
	}
	now := time.Now()
	t.phases = append(t.phases, Phase{Name: name, Took: now.Sub(t.last)})
	t.last = now
}

// Block records that a scheduled update of the block called name took d.
func (t *Tick) Block(name string, d time.Duration) {
	if t == nil {
		return
	}
	if t.blocks == nil {
		t.blocks = map[string]*BlockTime{}
	}
	b := t.blocks[name]
	if b == nil {
		b = &BlockTime{Name: name}
		t.blocks[name] = b
	}
	b.Count++
	b.Took += d
}

// Now returns the time if t measures, so callers only read the clock when
// debugging is on.
func (t *Tick) Now() time.Time {
	if t == nil {
		return time.Time{}
	}
	return time.Now()
}

// End finishes the tick.
func (t *Tick) End() {
	if t == nil {
		return
	}
	r := TickRecord{Start: t.start, Took: time.Since(t.start), Phases: t.phases}
	r.Slow = r.Took > t.m.conf.SlowTick
	for _, b := range t.blocks {
		r.Blocks = append(r.Blocks, *b)
	}
	slices.SortFunc(r.Blocks, func(a, b BlockTime) int { return int(b.Took - a.Took) })

	m := t.m
	m.tmu.Lock()
	w := m.worlds[t.world]
	if w == nil {
		w = &worldTicks{ticks: make([]TickRecord, m.conf.Ticks)}
		m.worlds[t.world] = w
	}
	w.ticks[w.next] = r
	w.next++
	if w.next == len(w.ticks) {
		w.next, w.full = 0, true
	}
	w.total++
	if r.Slow {
		w.slow++
	}
	m.tmu.Unlock()
}

// WorldTicks is the tick history of one world.
type WorldTicks struct {
	World string       `json:"world"`
	Total int64        `json:"total"`
	Slow  int64        `json:"slow"`
	Avg   float64      `json:"avgMs"`
	Max   float64      `json:"maxMs"`
	TPS   float64      `json:"tps"`
	Ticks []TickRecord `json:"ticks"`
}

// Ticks returns the recent ticks of every world, newest last.
func (m *Monitor) Ticks() []WorldTicks {
	if m == nil {
		return nil
	}
	m.tmu.Lock()
	defer m.tmu.Unlock()
	out := make([]WorldTicks, 0, len(m.worlds))
	for name, w := range m.worlds {
		var ts []TickRecord
		if w.full {
			ts = append(ts, w.ticks[w.next:]...)
		}
		ts = append(ts, w.ticks[:w.next]...)
		wt := WorldTicks{World: name, Total: w.total, Slow: w.slow, Ticks: ts}
		var sum time.Duration
		for _, t := range ts {
			sum += t.Took
			wt.Max = max(wt.Max, ms(t.Took))
		}
		if n := len(ts); n > 0 {
			wt.Avg = ms(sum) / float64(n)
			if span := ts[n-1].Start.Sub(ts[0].Start).Seconds(); span > 0 {
				wt.TPS = float64(n-1) / span
			}
		}
		out = append(out, wt)
	}
	slices.SortFunc(out, func(a, b WorldTicks) int { return compare(a.World, b.World) })
	return out
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
