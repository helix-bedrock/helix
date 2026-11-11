package world

import (
	"strings"
	"time"
)

// The parts of a region tick, timed for the over-budget warning.
const (
	phaseDrain = iota
	phaseEntities
	phaseViewers
	phaseBeforeBlocks
	phaseBlocks
	phaseLight
	phaseChunks
	phaseMovement
	phaseFlush
	phaseSave
	phaseHandOff
	phaseCount
)

// phaseNames name the parts in the warning.
var phaseNames = [phaseCount]string{"drain", "entities", "viewers", "before_blocks", "blocks", "light", "chunks", "movement", "flush", "save", "hand_off"}

// tickPhases is how long each part of one region tick took. Timing costs one clock read per
// part, so it always runs: a slow tick can then say what was slow.
type tickPhases struct {
	last time.Time
	took [phaseCount]time.Duration
}

// start begins timing a tick.
func (t *tickPhases) start() {
	t.last = time.Now()
	t.took = [phaseCount]time.Duration{}
}

// mark ends part p.
func (t *tickPhases) mark(p int) {
	now := time.Now()
	t.took[p] = now.Sub(t.last)
	t.last = now
}

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
		b.WriteString(phaseNames[p])
		b.WriteByte('=')
		b.WriteString(d.Round(100 * time.Microsecond).String())
	}
	return b.String()
}
