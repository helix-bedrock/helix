package world

import (
	"runtime"
	"sync"
)

// Pool is a fixed set of goroutines that run the regions of one or more
// worlds (Helix). A region with work waits in the pool's queue until a
// worker is free; a region with none costs nothing. Without a pool every
// region needed a goroutine of its own, and a server with many regions ran
// far more goroutines than cores.
//
// Transactions run on pool workers, so a transaction must never block
// waiting for another transaction: with every worker blocked, nothing would
// run. Use Do or DoAt and return instead of World.Call or Task.Wait.
//
// A Pool is safe to use from anywhere.
type Pool struct {
	mu     sync.Mutex
	cond   *sync.Cond
	q      []*region
	closed bool
	wg     sync.WaitGroup
}

// poolBatch is how many transactions a worker runs for one region before
// giving other regions a turn, so a busy region cannot starve the rest.
const poolBatch = 64

// NewPool starts a pool of n workers. If n is 0 or less, it uses one worker
// per core (GOMAXPROCS).
func NewPool(n int) *Pool {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	p := &Pool{}
	p.cond = sync.NewCond(&p.mu)
	p.wg.Add(n)
	for range n {
		go p.work()
	}
	return p
}

// Close stops the workers once they finish what they are running. Regions
// still queued are dropped, so close the worlds using the pool first.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	p.q = nil
	p.cond.Broadcast()
	p.mu.Unlock()
	p.wg.Wait()
}

// submit queues r to run. The caller made sure r is not queued already.
func (p *Pool) submit(r *region) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		r.unschedule()
		return
	}
	p.q = append(p.q, r)
	p.cond.Signal()
	p.mu.Unlock()
}

func (p *Pool) work() {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		for len(p.q) == 0 && !p.closed {
			p.cond.Wait()
		}
		if p.closed {
			p.mu.Unlock()
			return
		}
		r := p.q[0]
		p.q[0] = nil
		p.q = p.q[1:]
		p.mu.Unlock()

		if r.drain() {
			p.submit(r)
		}
	}
}

// unschedule marks r as no longer queued in the pool.
func (r *region) unschedule() {
	r.qmu.Lock()
	r.scheduled = false
	r.qmu.Unlock()
}

// drain runs up to poolBatch of r's transactions on a pool worker. It
// returns true if r has more work and should be queued again.
func (r *region) drain() bool {
	// busy keeps World.close waiting until this run is over; after closing
	// starts, queued work is dropped as the old per-region goroutine did.
	r.busy.RLock()
	defer r.busy.RUnlock()
	select {
	case <-r.closing:
		r.qmu.Lock()
		r.q, r.scheduled = nil, false
		r.qmu.Unlock()
		return false
	default:
	}
	for range poolBatch {
		r.qmu.Lock()
		if r.merged != nil || r.stopped || len(r.q) == 0 {
			// A merged or stopped region handed its queue on already.
			r.scheduled = false
			r.qmu.Unlock()
			return false
		}
		t := r.q[0]
		r.q[0] = nil
		r.q = r.q[1:]
		r.qmu.Unlock()

		r.mu.Lock()
		if m := r.mergedInto(); m != nil {
			// Merged between taking t and starting it: m runs it.
			r.mu.Unlock()
			m.post(t)
			continue
		}
		t.Run(r)
		r.mu.Unlock()
	}
	r.qmu.Lock()
	defer r.qmu.Unlock()
	if len(r.q) == 0 || r.merged != nil || r.stopped {
		r.scheduled = false
		return false
	}
	return true
}
