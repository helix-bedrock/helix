package world

import (
	"container/heap"
	"sync"

	"github.com/helix-bedrock/world/mcdb"
)

// pool runs chunk loads, generation, encoding and saves off the tick, the
// most urgent first (priority). Jobs for one chunk position run one at a time
// in the order they were queued, so a load never overtakes the save of the
// same chunk, while any free worker takes any chunk: a chunk that takes
// seconds to read holds up only its own jobs. Queues never block the caller:
// the tick never waits for a worker.
type pool struct {
	mu     sync.Mutex
	cond   *sync.Cond
	ready  chainHeap
	chains map[mcdb.ChunkPos]*chain
	// fifo is the ready chains in the order they became ready (with stale
	// entries left behind, recognised by seq): every oldestEvery-th pick
	// takes the oldest, so saves and far chunks are never starved.
	fifo   []fifoEntry
	picks  uint64
	seq    uint64
	closed bool
	spare  []*chain
	wg     sync.WaitGroup
}

// priority orders the pool's jobs: higher runs first.
type priority int64

const (
	// prioAutoSave is the periodic save of loaded chunks.
	prioAutoSave priority = iota << 40
	// prioSave is the save of a chunk unloading or of Close.
	prioSave
	// prioView is a chunk players are shown but that does not tick, nearer
	// first (see viewPriority).
	prioView
	// prioNeeded is work something waits on to go on: chunks held loaded
	// (simulation zones, tickets, RunAt) and block entities being opened.
	prioNeeded
)

// viewPriority is prioView for a chunk dist2 (squared chunks) from the player
// nearest to it.
func viewPriority(dist2 int64) priority {
	return prioView - priority(min(max(dist2, 0), 1<<39))
}

// oldestEvery is how often a worker takes the oldest ready chain instead of
// the most urgent.
const oldestEvery = 8

// chain is the queued jobs of one chunk position.
type chain struct {
	pos  mcdb.ChunkPos
	jobs []poolJob
	// prio is the highest priority of its jobs: a job behind an urgent one
	// of the same chunk must run first, so it runs as urgently.
	prio priority
	busy bool
	// idx is its place in the ready heap, -1 when not in it; seq when it
	// entered it.
	idx int
	seq uint64
}

type poolJob struct {
	fn   func()
	prio priority
}

type fifoEntry struct {
	c   *chain
	seq uint64
}

func newPool(n int) *pool {
	p := &pool{chains: map[mcdb.ChunkPos]*chain{}}
	p.cond = sync.NewCond(&p.mu)
	for range max(n, 1) {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.run()
		}()
	}
	return p
}

// do queues job for the chunk at pos.
func (p *pool) do(pos mcdb.ChunkPos, prio priority, job func()) {
	p.mu.Lock()
	c, ok := p.chains[pos]
	if !ok {
		c = p.newChain(pos)
	}
	c.jobs = append(c.jobs, poolJob{fn: job, prio: prio})
	p.raiseLocked(c, prio)
	if !c.busy && c.idx < 0 {
		p.push(c)
	}
	p.mu.Unlock()
	p.cond.Signal()
}

// raise makes the queued jobs of pos at least prio urgent: a player came
// nearer to a chunk already asked for.
func (p *pool) raise(pos mcdb.ChunkPos, prio priority) {
	p.mu.Lock()
	if c, ok := p.chains[pos]; ok {
		p.raiseLocked(c, prio)
	}
	p.mu.Unlock()
}

func (p *pool) raiseLocked(c *chain, prio priority) {
	for i := range c.jobs {
		c.jobs[i].prio = max(c.jobs[i].prio, prio)
	}
	if prio <= c.prio {
		return
	}
	c.prio = prio
	if c.idx >= 0 {
		heap.Fix(&p.ready, c.idx)
	}
}

func (p *pool) newChain(pos mcdb.ChunkPos) *chain {
	var c *chain
	if n := len(p.spare); n > 0 {
		c, p.spare = p.spare[n-1], p.spare[:n-1]
	} else {
		c = &chain{}
	}
	*c = chain{pos: pos, jobs: c.jobs[:0], prio: -1 << 62, idx: -1}
	p.chains[pos] = c
	return c
}

func (p *pool) push(c *chain) {
	p.seq++
	c.seq = p.seq
	heap.Push(&p.ready, c)
	p.fifo = append(p.fifo, fifoEntry{c: c, seq: c.seq})
}

// next takes the chain to run next. Under p.mu, with p.ready not empty.
func (p *pool) next() *chain {
	p.picks++
	if p.picks%oldestEvery == 0 {
		for len(p.fifo) > 0 {
			f := p.fifo[0]
			p.fifo[0] = fifoEntry{}
			p.fifo = p.fifo[1:]
			if f.c.idx >= 0 && f.c.seq == f.seq {
				heap.Remove(&p.ready, f.c.idx)
				return f.c
			}
		}
	}
	c, _ := heap.Pop(&p.ready).(*chain)
	if len(p.fifo) > 4*p.ready.Len()+64 {
		p.compactFifo()
	}
	return c
}

// compactFifo drops the stale entries of fifo.
func (p *pool) compactFifo() {
	n := 0
	for _, f := range p.fifo {
		if f.c.idx >= 0 && f.c.seq == f.seq {
			p.fifo[n] = f
			n++
		}
	}
	clear(p.fifo[n:])
	p.fifo = p.fifo[:n]
}

// close runs every queued job, then stops the workers.
func (p *pool) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
	p.wg.Wait()
}

func (p *pool) run() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		for p.ready.Len() == 0 && !(p.closed && p.idleLocked()) {
			p.cond.Wait()
		}
		if p.ready.Len() == 0 {
			p.cond.Broadcast()
			return
		}
		c := p.next()
		c.busy = true
		job := c.jobs[0]
		c.jobs[0] = poolJob{}
		c.jobs = c.jobs[1:]
		p.mu.Unlock()
		job.fn()
		p.mu.Lock()
		c.busy = false
		p.finished(c)
	}
}

// finished puts a chain with jobs left back in the queue, or forgets it.
func (p *pool) finished(c *chain) {
	if len(c.jobs) == 0 {
		delete(p.chains, c.pos)
		c.jobs = c.jobs[:0]
		p.spare = append(p.spare, c)
		if p.closed && p.idleLocked() {
			p.cond.Broadcast()
		}
		return
	}
	c.prio = c.jobs[0].prio
	for _, j := range c.jobs[1:] {
		c.prio = max(c.prio, j.prio)
	}
	p.push(c)
	p.cond.Signal()
}

// idleLocked reports whether no job is queued or running.
func (p *pool) idleLocked() bool { return len(p.chains) == 0 }

// chainHeap is the ready chains, most urgent first, then oldest.
type chainHeap []*chain

func (h chainHeap) Len() int { return len(h) }
func (h chainHeap) Less(i, j int) bool {
	if h[i].prio != h[j].prio {
		return h[i].prio > h[j].prio
	}
	return h[i].seq < h[j].seq
}
func (h chainHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx, h[j].idx = i, j
}
func (h *chainHeap) Push(x any) {
	c, _ := x.(*chain)
	c.idx = len(*h)
	*h = append(*h, c)
}
func (h *chainHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	old[n-1] = nil
	c.idx = -1
	*h = old[:n-1]
	return c
}
