package world

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// A crowded region must not sit on one core (API_DESIGN goal 8, "Crowded regions work in
// parallel inside the tick"). Region.Parallel is the one way any system spreads a phase of a
// region's tick over idle cores: mob AI first, later player input, redstone and the rest.
// Every item of a phase only reads the region and changes its own state; what it does to
// shared state it queues, and the system applies the queue afterwards in a fixed order. The
// same code runs inline below the threshold, so results never depend on the core count.

const (
	// DefaultParallelMin is how many items a phase needs before it spreads over more cores.
	// Below it, handing work to other goroutines costs more than it saves.
	DefaultParallelMin = 32
	// DefaultParallelHelpers is how many goroutines at most help one region's phase,
	// besides the region's own: the "two or three idle cores" a crowded region may take.
	DefaultParallelHelpers = 3
	// parallelSlices is how many slices each goroutine's share is cut into, so a goroutine
	// that drew cheap items takes more while another is busy with a slow one.
	parallelSlices = 8
)

// parallelJob is one phase being spread over goroutines. Each region keeps one and reuses it,
// so a phase allocates nothing.
type parallelJob struct {
	fn    func(i int)
	n     int64
	batch int64
	next  atomic.Int64
	wg    sync.WaitGroup
	log   *slog.Logger
	sched *Scheduler
}

// run takes slices of the items until none are left.
func (j *parallelJob) run() {
	for {
		end := j.next.Add(j.batch)
		start := end - j.batch
		if start >= j.n {
			return
		}
		for i := start; i < min(end, j.n); i++ {
			j.fn(int(i))
		}
	}
}

// help runs j on a helper goroutine. A panic goes to the scheduler's panic handler, as a panic
// in the region's own tick does.
func (j *parallelJob) help() {
	defer j.wg.Done()
	defer j.sched.recovered(j.log)
	j.run()
}

// startHelpers starts n goroutines that help regions with their parallel phases. A helper
// only joins a phase when it is idle, so a busy server never queues behind one.
func (s *Scheduler) startHelpers(n int) {
	s.help = make(chan *parallelJob)
	s.helpWG.Add(n)
	for range n {
		go func() {
			defer s.helpWG.Done()
			for j := range s.help {
				j.help()
			}
		}()
	}
}

// stopHelpers ends the helpers once no region ticks any more.
func (s *Scheduler) stopHelpers() {
	close(s.help)
	s.helpWG.Wait()
}

// SetParallel sets how many items a phase of Region.Parallel needs before it spreads over more
// cores (min, at least 1) and how many goroutines at most help one region besides its own
// (helpers; 0 keeps every phase on the region's goroutine). Safe from any goroutine; it takes
// effect from the next phase.
func (d *Dimension) SetParallel(min, helpers int) {
	d.parallelMin.Store(int64(max(min, 1)))
	d.parallelHelpers.Store(int64(max(helpers, 0)))
}

// Parallel calls fn(i) once for each i in [0, n), on the region's goroutine and, when n reaches
// the dimension's threshold (SetParallel), also on idle helper goroutines. It returns once
// every call has returned.
//
// While it runs the region is read-only: fn may read blocks, light, heights, entities and its
// own item's state, and change only its own item's state. It must not change blocks or other
// entities, send packets, call Local or start another Parallel; it queues those and applies
// them after Parallel returns, in an order that does not depend on which goroutine ran what.
// Then the results are the same on one core and many. Runs: owner.
func (r *Region) Parallel(n int, fn func(i int)) {
	helpers := 0
	if int64(n) >= r.d.parallelMin.Load() {
		helpers = int(min(r.d.parallelHelpers.Load(), int64(n-1)))
	}
	if helpers <= 0 {
		for i := range n {
			fn(i)
		}
		return
	}
	// Reads flush pending light work and cache the last chunk; settle both first, so the
	// reads of the phase only read.
	r.flushLight()
	r.light.lastChunk = nil
	j := &r.job
	j.fn, j.n, j.log, j.sched = fn, int64(n), r.d.log, r.d.sched
	j.batch = max(int64(n)/int64((helpers+1)*parallelSlices), 1)
	j.next.Store(0)
	r.parallel.Store(true)
hand:
	for range helpers {
		j.wg.Add(1)
		select {
		case r.d.sched.help <- j:
		default:
			// No helper is idle: the cores are busy with other regions anyway.
			j.wg.Done()
			break hand
		}
	}
	j.run()
	j.wg.Wait()
	r.parallel.Store(false)
	j.fn = nil
}

// InParallel reports that a Parallel phase is running on the region, when the read-only rules
// apply. Runs: owner and Parallel's goroutines.
func (r *Region) InParallel() bool { return r.parallel.Load() }
