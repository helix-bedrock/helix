package world

import (
	"strings"
	"time"
)

// The parts of a region tick, in the order they run, timed for the over-budget warning and
// the tick observer (Dimension.SetTickObserver).
const (
	PhaseDrain = iota
	PhaseEntities
	PhaseViewers
	PhaseBeforeBlocks
	PhaseBlocks
	PhaseLight
	PhaseChunks
	PhaseMovement
	PhaseFlush
	PhaseSave
	PhaseHandOff
	PhaseCount
)

// PhaseName names part p of a region tick, as the over-budget warning does: "drain",
// "entities", "viewers", "before_blocks", "blocks", "light", "chunks", "movement", "flush",
// "save" or "hand_off". Empty for a number that is no part.
func PhaseName(p int) string {
	switch p {
	case PhaseDrain:
		return "drain"
	case PhaseEntities:
		return "entities"
	case PhaseViewers:
		return "viewers"
	case PhaseBeforeBlocks:
		return "before_blocks"
	case PhaseBlocks:
		return "blocks"
	case PhaseLight:
		return "light"
	case PhaseChunks:
		return "chunks"
	case PhaseMovement:
		return "movement"
	case PhaseFlush:
		return "flush"
	case PhaseSave:
		return "save"
	case PhaseHandOff:
		return "hand_off"
	}
	return ""
}

// TickStats are the numbers of one region tick, given to the tick observer.
type TickStats struct {
	// Region is the region's ID, Tick the world tick the scheduler was at when it began.
	Region uint64
	Tick   int64
	// Start is when the tick began and Took how long it ran; Phases split Took into the
	// parts of the tick, indexed by the Phase constants.
	Start  time.Time
	Took   time.Duration
	Phases [PhaseCount]time.Duration
	// Chunks, Viewers and Entities are what the region held when the tick ended.
	Chunks, Viewers, Entities int
}

// TickObserver receives the numbers of every region tick of a dimension. It runs on the
// scheduler goroutine that ran the tick, right after it, for many regions at once: it must
// be safe for concurrent use and return quickly, since the region's next tick waits for it.
type TickObserver func(TickStats)

// SetTickObserver sets the function given every region tick's numbers, for load tests and
// tick time displays; nil stops it. Runs: anywhere.
func (d *Dimension) SetTickObserver(fn TickObserver) {
	if fn == nil {
		d.tickObserver.Store(nil)
		return
	}
	d.tickObserver.Store(&fn)
}

// observe gives the tick observer, if any, the numbers of r's tick that began at start.
func (d *Dimension) observe(r *Region, start time.Time, took time.Duration, tick int64) {
	fn := d.tickObserver.Load()
	if fn == nil {
		return
	}
	(*fn)(TickStats{
		Region: r.id, Tick: tick, Start: start, Took: took, Phases: r.phases.took,
		Chunks: len(r.chunks), Viewers: len(r.viewers), Entities: len(r.entities),
	})
}

// tickPhases is how long each part of one region tick took. Timing costs one clock read per
// part, so it always runs: a slow tick can then say what was slow.
type tickPhases struct {
	// first is when the tick began and last when the last part ended, fineClock readings.
	first, last int64
	took        [PhaseCount]time.Duration
}

// start begins timing a tick.
func (t *tickPhases) start() {
	t.first = fineClock()
	t.last = t.first
	t.took = [PhaseCount]time.Duration{}
}

// mark ends part p.
func (t *tickPhases) mark(p int) {
	now := fineClock()
	t.took[p] = time.Duration(now - t.last)
	t.last = now
}

// total is how long the tick took from start to the last mark.
func (t *tickPhases) total() time.Duration { return time.Duration(t.last - t.first) }

// String lists the parts that took a millisecond or more.
func (t *tickPhases) String() string {
	var b strings.Builder
	for p, d := range t.took {
		if d < time.Millisecond {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(PhaseName(p))
		b.WriteByte('=')
		b.WriteString(d.Round(100 * time.Microsecond).String())
	}
	return b.String()
}
