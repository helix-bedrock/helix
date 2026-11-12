package sim

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/helix-bedrock/helix/world"
)

// Report is what one run measured. Tick times are per region tick: what the players in
// that region feel. Process numbers are the whole server's, per world tick (50 ms).
type Report struct {
	Scenario string
	Unit     string
	N        int
	Cores    int
	// Measured is how long the measurement ran.
	Measured time.Duration

	// RegionTicks is how many region ticks ran; Regions how many regions ticked; TPS the
	// ticks per second of the average region (20 when none fell behind).
	RegionTicks int
	Regions     int
	TPS         float64
	// Median, P99 and Worst are region tick times; Mean their average.
	Median, P99, Worst, Mean time.Duration
	// Late is how many region ticks took longer than a tick (50 ms).
	Late int
	// Phases is the average time of each part of a region tick, by world.Phase constant.
	Phases [world.PhaseCount]time.Duration
	// Entities is the most entities one region held at the end of a tick.
	Entities int
	// TickCPU is the time all regions spent ticking per world tick, summed over cores.
	TickCPU time.Duration
	// PerUnit is the region tick time each unit adds, against the smallest run of a Sweep;
	// 0 outside a Sweep.
	PerUnit time.Duration

	// AllocsPerTick and BytesPerTick are what the whole process allocated per world tick.
	AllocsPerTick, BytesPerTick float64
	// GCShare is the share of the process's CPU time the garbage collector took; GCCycles
	// how many collections ran; HeapBytes the heap live at the last collection.
	GCShare   float64
	GCCycles  uint64
	HeapBytes uint64
	// Bots is how many bots played, JoinTime how long they took to log in, all together.
	Bots     int
	JoinTime time.Duration
	// ActionMedian, ActionP99 and ActionWorst are the delays from a bot sending a block
	// action to the server's block update reaching it, over Actions answered actions: what
	// a player feels between breaking a block and seeing it go.
	ActionMedian, ActionP99, ActionWorst time.Duration
	Actions                              int
	// MovesPerBot is the movement packets about others each bot got per second.
	MovesPerBot float64
	// Dropped is how many region ticks did not fit the collector's buffer (more than
	// maxRegions regions); their times are missing from the numbers above.
	Dropped int
}

// report builds the Report from the ticks kept and the process change p over took.
func (c *collector) report(sc Scenario, n, coreCount int, took time.Duration, p process) Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	worldTicks := float64(took) / float64(world.TickDuration)
	r := Report{
		Scenario: sc.Name, Unit: sc.Unit, N: n, Cores: cores(coreCount), Measured: took,
		RegionTicks: len(c.ticks), Dropped: c.dropped, GCCycles: p.gcCycles, HeapBytes: p.heap,
		AllocsPerTick: float64(p.allocs) / worldTicks, BytesPerTick: float64(p.bytes) / worldTicks,
	}
	if p.cpu > 0 {
		r.GCShare = p.gcCPU / p.cpu
	}
	if len(c.ticks) == 0 {
		return r
	}
	r.addTickTimes(c.ticks, worldTicks)
	r.addPhases(c.ticks)
	return r
}

// addTickTimes fills in the tick time spread, lateness, regions and rate.
func (r *Report) addTickTimes(ticks []world.TickStats, worldTicks float64) {
	took := make([]time.Duration, len(ticks))
	regions := map[uint64]bool{}
	var sum time.Duration
	for i, t := range ticks {
		took[i] = t.Took
		sum += t.Took
		regions[t.Region] = true
		r.Entities = max(r.Entities, t.Entities)
		if t.Took > world.TickDuration {
			r.Late++
		}
	}
	slices.Sort(took)
	r.Median, r.P99, r.Worst = took[len(took)/2], took[len(took)*99/100], took[len(took)-1]
	r.Mean = sum / time.Duration(len(took))
	r.Regions = len(regions)
	r.TickCPU = time.Duration(float64(sum) / worldTicks)
	r.TPS = float64(len(ticks)) / float64(r.Regions) / r.Measured.Seconds()
}

// addPhases fills in the average time of each part of a tick.
func (r *Report) addPhases(ticks []world.TickStats) {
	var sum [world.PhaseCount]time.Duration
	for _, t := range ticks {
		for p, d := range t.Phases {
			sum[p] += d
		}
	}
	for p := range sum {
		r.Phases[p] = sum[p] / time.Duration(len(ticks))
	}
}

// setPerUnit fills in each report's cost per unit against the one with the smallest count.
func setPerUnit(reps []Report) {
	if len(reps) < 2 {
		return
	}
	base := slices.MinFunc(reps, func(a, b Report) int { return a.N - b.N })
	for i := range reps {
		if extra := reps[i].N - base.N; extra > 0 {
			reps[i].PerUnit = (reps[i].Mean - base.Mean) / time.Duration(extra)
		}
	}
}

// WriteTable writes reports as a table: one row per run, then each run's slowest phases.
func WriteTable(out io.Writer, reps []Report) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "scenario\tn\tcores\tregions\ttps\tmedian\tp99\tworst\tlate\tmean\tper unit\tcpu/tick\tentities\tallocs/tick\tKB/tick\tgc%\theap MB\t")
	for _, r := range reps {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%.1f\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%d\t%.0f\t%.1f\t%.1f\t%.1f\t\n",
			r.Scenario, r.N, r.Cores, r.Regions, r.TPS, ms(r.Median), ms(r.P99), ms(r.Worst), r.Late,
			ms(r.Mean), us(r.PerUnit), ms(r.TickCPU), r.Entities, r.AllocsPerTick, r.BytesPerTick/1024,
			r.GCShare*100, float64(r.HeapBytes)/(1<<20))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, r := range reps {
		if _, err := fmt.Fprintf(out, "%s n=%d phases: %s\n", r.Scenario, r.N, r.phaseLine()); err != nil {
			return err
		}
		if r.Bots > 0 {
			if _, err := fmt.Fprintf(out, "  bots=%d joined in %s, actions answered=%d delay median=%s p99=%s worst=%s, moves seen per bot=%.0f/s\n",
				r.Bots, r.JoinTime.Round(time.Millisecond), r.Actions, ms(r.ActionMedian), ms(r.ActionP99), ms(r.ActionWorst), r.MovesPerBot); err != nil {
				return err
			}
		}
		if r.Dropped > 0 {
			if _, err := fmt.Fprintf(out, "  %d region ticks dropped: more than %d regions\n", r.Dropped, maxRegions); err != nil {
				return err
			}
		}
	}
	return nil
}

// phaseLine lists the parts of the average tick, slowest first, leaving out those under
// 10 µs.
func (r Report) phaseLine() string {
	order := make([]int, 0, world.PhaseCount)
	for p := range world.PhaseCount {
		if r.Phases[p] >= 10*time.Microsecond {
			order = append(order, p)
		}
	}
	slices.SortFunc(order, func(a, b int) int { return int(r.Phases[b] - r.Phases[a]) })
	parts := make([]string, len(order))
	for i, p := range order {
		parts[i] = world.PhaseName(p) + "=" + ms(r.Phases[p])
	}
	if len(parts) == 0 {
		return "all under 10µs"
	}
	return strings.Join(parts, " ")
}

// ms writes a duration in milliseconds with three decimals.
func ms(d time.Duration) string { return fmt.Sprintf("%.3fms", float64(d)/float64(time.Millisecond)) }

// us writes a duration in microseconds with two decimals, or "-" for none.
func us(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2fµs", float64(d)/float64(time.Microsecond))
}
