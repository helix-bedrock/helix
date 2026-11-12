package sim

import (
	"context"
	"fmt"
	"runtime"
	"runtime/metrics"
	"sync"
	"time"

	"github.com/helix-bedrock/helix/world"
)

const (
	// DefaultWarmup is how long the load runs before it is measured: long enough for loads
	// that build up over seconds (TNT on a four second fuse) to reach their steady state.
	DefaultWarmup = 5 * time.Second
	// DefaultMeasure is how long the load is measured.
	DefaultMeasure = 10 * time.Second
	// maxRegions bounds the region ticks kept per world tick, so the observer never grows
	// its buffer while measuring; more are counted as dropped.
	maxRegions = 64
)

// Scenario is one kind of load.
type Scenario struct {
	// Name is how cmd/sim picks it; About says what it is.
	Name, About string
	// Unit names what n counts, for the cost per unit ("items", "TNT").
	Unit string
	// Size is the side in blocks of the square around 0, 0 kept loaded and ticking; 0 means
	// DefaultSize.
	Size int32
	// Start adds n units of load. It runs once, after the area is loaded. Nil adds nothing.
	Start func(ctx context.Context, w *World, n int) error
	// Tick runs every 50 ms while the load runs, tick counting from 0, for loads that end
	// by themselves and must be replaced to stay steady (TNT explodes, rockets burst). Nil
	// for none.
	Tick func(w *World, n, tick int)
}

// Run starts a fresh server, adds n units of the scenario's load, runs it for the warmup,
// measures it, and stops the server.
func Run(ctx context.Context, conf Config, sc Scenario, n int) (Report, error) {
	w, err := Start(ctx, conf, sc.Size)
	if err != nil {
		return Report{}, err
	}
	rep, err := measure(ctx, conf, w, sc, n)
	if cerr := w.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return rep, err
}

// Sweep runs the scenario once for each count, each on a fresh server, and fills in the
// cost per unit against the run with the smallest count.
func Sweep(ctx context.Context, conf Config, sc Scenario, counts []int) ([]Report, error) {
	out := make([]Report, 0, len(counts))
	for _, n := range counts {
		rep, err := Run(ctx, conf, sc, n)
		if err != nil {
			return out, fmt.Errorf("sim: %s n=%d: %w", sc.Name, n, err)
		}
		out = append(out, rep)
	}
	setPerUnit(out)
	return out, nil
}

// measure adds the load, waits out the warmup and measures.
func measure(ctx context.Context, conf Config, w *World, sc Scenario, n int) (Report, error) {
	warmup, length := conf.Warmup, conf.Measure
	if warmup <= 0 {
		warmup = DefaultWarmup
	}
	if length <= 0 {
		length = DefaultMeasure
	}
	if sc.Start != nil {
		if err := sc.Start(ctx, w, n); err != nil {
			return Report{}, err
		}
	}
	c := newCollector(length)
	w.Dim.SetTickObserver(c.observe)
	defer w.Dim.SetTickObserver(nil)

	tick := 0
	if err := drive(ctx, w, sc, n, &tick, warmup); err != nil {
		return Report{}, err
	}
	before := readProcess()
	c.setOn(true)
	start := time.Now()
	if err := drive(ctx, w, sc, n, &tick, length); err != nil {
		return Report{}, err
	}
	c.setOn(false)
	took := time.Since(start)
	after := readProcess()
	return c.report(sc, n, conf.Cores, took, after.since(before)), nil
}

// drive runs the scenario's Tick every 50 ms for d.
func drive(ctx context.Context, w *World, sc Scenario, n int, tick *int, d time.Duration) error {
	end := time.Now().Add(d)
	t := time.NewTicker(world.TickDuration)
	defer t.Stop()
	for time.Now().Before(end) {
		if sc.Tick != nil {
			sc.Tick(w, n, *tick)
		}
		*tick++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// collector keeps the region ticks seen while measuring. Its buffer is made up front, so
// the observer does not allocate inside the ticks it measures.
type collector struct {
	mu      sync.Mutex
	on      bool
	ticks   []world.TickStats
	dropped int
}

// newCollector makes room for every region tick of a measurement of length d.
func newCollector(d time.Duration) *collector {
	worldTicks := int(d/world.TickDuration) + 1
	return &collector{ticks: make([]world.TickStats, 0, worldTicks*maxRegions)}
}

// observe is the dimension's tick observer.
func (c *collector) observe(t world.TickStats) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.on {
		return
	}
	if len(c.ticks) == cap(c.ticks) {
		c.dropped++
		return
	}
	c.ticks = append(c.ticks, t)
}

// setOn starts or stops keeping ticks.
func (c *collector) setOn(on bool) {
	c.mu.Lock()
	c.on = on
	c.mu.Unlock()
}

// The runtime metrics a run reads, in the order of process.values.
const (
	metricAllocObjects = iota
	metricAllocBytes
	metricGCCPU
	metricTotalCPU
	metricHeapLive
	metricGCCycles
	metricCount
)

// metricNames are the runtime/metrics names, indexed by the metric constants.
func metricNames() [metricCount]string {
	return [metricCount]string{
		metricAllocObjects: "/gc/heap/allocs:objects",
		metricAllocBytes:   "/gc/heap/allocs:bytes",
		metricGCCPU:        "/cpu/classes/gc/total:cpu-seconds",
		metricTotalCPU:     "/cpu/classes/total:cpu-seconds",
		metricHeapLive:     "/memory/classes/heap/objects:bytes",
		metricGCCycles:     "/gc/cycles/total:gc-cycles",
	}
}

// process is a reading of the whole process's runtime metrics.
type process struct {
	allocs, bytes, gcCycles, heap uint64
	gcCPU, cpu                    float64
}

// readProcess reads the runtime metrics now.
func readProcess() process {
	names := metricNames()
	samples := make([]metrics.Sample, metricCount)
	for i, name := range names {
		samples[i].Name = name
	}
	metrics.Read(samples)
	return process{
		allocs:   uintValue(samples[metricAllocObjects]),
		bytes:    uintValue(samples[metricAllocBytes]),
		gcCycles: uintValue(samples[metricGCCycles]),
		heap:     uintValue(samples[metricHeapLive]),
		gcCPU:    floatValue(samples[metricGCCPU]),
		cpu:      floatValue(samples[metricTotalCPU]),
	}
}

// since is what changed from before to p; the heap is p's.
func (p process) since(before process) process {
	return process{
		allocs: p.allocs - before.allocs, bytes: p.bytes - before.bytes,
		gcCycles: p.gcCycles - before.gcCycles, heap: p.heap,
		gcCPU: p.gcCPU - before.gcCPU, cpu: p.cpu - before.cpu,
	}
}

// uintValue is a counter's value, 0 if this Go has no such metric.
func uintValue(s metrics.Sample) uint64 {
	if s.Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s.Value.Uint64()
}

// floatValue is a float metric's value, 0 if this Go has no such metric.
func floatValue(s metrics.Sample) float64 {
	if s.Value.Kind() != metrics.KindFloat64 {
		return 0
	}
	return s.Value.Float64()
}

// cores is how many cores the server ticks on for a Config's Cores.
func cores(n int) int {
	if n <= 0 {
		return runtime.GOMAXPROCS(0)
	}
	return n
}
