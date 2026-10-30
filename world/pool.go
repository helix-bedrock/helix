package world

import (
	"sync"

	"github.com/helix-bedrock/world/mcdb"
)

// pool runs chunk loads, generation, encoding and saves off the tick. Jobs
// for one chunk position always go to the same worker and run in the order
// they were queued, so a load never overtakes the save of the same chunk.
// Queues never block the caller: the tick never waits for a worker.
type pool struct {
	workers []*worker
	wg      sync.WaitGroup
}

type worker struct {
	mu     sync.Mutex
	cond   *sync.Cond
	jobs   []func()
	closed bool
}

func newPool(n int) *pool {
	p := &pool{workers: make([]*worker, max(n, 1))}
	for i := range p.workers {
		w := &worker{}
		w.cond = sync.NewCond(&w.mu)
		p.workers[i] = w
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			w.run()
		}()
	}
	return p
}

// do queues job on the worker of pos.
func (p *pool) do(pos mcdb.ChunkPos, job func()) {
	h := uint32(pos[0])*0x9E3779B1 ^ uint32(pos[1])*0x85EBCA77 //nolint:gosec // only mixing bits
	w := p.workers[h%uint32(len(p.workers))]                   //nolint:gosec // few workers
	w.mu.Lock()
	w.jobs = append(w.jobs, job)
	w.mu.Unlock()
	w.cond.Signal()
}

// close runs every queued job, then stops the workers.
func (p *pool) close() {
	for _, w := range p.workers {
		w.mu.Lock()
		w.closed = true
		w.mu.Unlock()
		w.cond.Signal()
	}
	p.wg.Wait()
}

func (w *worker) run() {
	var batch []func()
	for {
		w.mu.Lock()
		for len(w.jobs) == 0 && !w.closed {
			w.cond.Wait()
		}
		if len(w.jobs) == 0 {
			w.mu.Unlock()
			return
		}
		batch, w.jobs = w.jobs, batch[:0]
		w.mu.Unlock()
		for i, job := range batch {
			job()
			batch[i] = nil
		}
	}
}
