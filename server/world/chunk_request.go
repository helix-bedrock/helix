package world

import (
	"fmt"
	"sync"

	"github.com/helix-bedrock/helix/server/world/chunk"
)

// chunkRequest tracks a chunk that is being loaded or generated in the
// background. All callers waiting for the same chunk share a single request.
type chunkRequest struct {
	pos       ChunkPos
	callbacks []chunkCallback
	added     bool
	signalled bool

	done   chan struct{}
	col    *chunk.Column
	err    error
	result *Column
}

// defaultChunkLoadWorkers is the number of chunk load workers started when
// Config.ChunkLoadWorkers is not set.
const defaultChunkLoadWorkers = 1

// chunkCallback is called with a chunk once it has been added to the world.
type chunkCallback = func(tx *Tx, col *Column)

// chunkWorkerPool runs chunk requests on a fixed number of background workers.
type chunkWorkerPool struct {
	w     *World
	queue chan *chunkRequest
	wg    sync.WaitGroup

	mu     sync.Mutex
	closed bool
}

func newChunkWorkerPool(w *World) *chunkWorkerPool {
	return &chunkWorkerPool{w: w, queue: make(chan *chunkRequest, 4096)}
}

// doImmediate blocks until the chunk is ready, adds it to the world and
// returns it. The callbacks run later from signal, at the top level of a
// transaction, so they never run nested inside another chunk's callback
// (Dragonfly PR #1381: that re-entered Loader.viewChunk and deadlocked).
func (r *chunkRequest) doImmediate(tx *Tx) *Column {
	<-r.done
	r.add(tx)
	return r.result
}

// load loads or generates the chunk and hands it back to the world to be
// added.
func (r *chunkRequest) load(w *World) {
	func() {
		// Helix: a corrupt chunk or a broken generator must not stop the
		// server; the chunk fails to load instead.
		defer func() {
			if v := recover(); v != nil {
				r.col, r.err = nil, fmt.Errorf("panic loading chunk: %v", v)
			}
		}()
		r.col, r.err = w.loadChunk(r.pos)
	}()
	close(r.done)
	// Helix: finish on the region that owns the chunk.
	w.postAt(r.pos, r.signal)
}

// abort cancels a request that will never be carried out because the world is
// closing, releasing any callers waiting on it.
func (r *chunkRequest) abort() {
	close(r.done)
}

// schedule hands r to the workers without blocking. It returns false if the
// request cannot be accepted, e.g. when the world is closing.
func (p *chunkWorkerPool) schedule(r *chunkRequest) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.w.closed.Load() {
		p.closed = true
		return false
	}
	select {
	case p.queue <- r:
		return true
	default:
		return false
	}
}

// handle continuously processes chunk requests until the world starts closing.
func (p *chunkWorkerPool) handle() {
	defer p.wg.Done()
	for {
		if p.w.closed.Load() {
			p.drainAndAbort()
			return
		}
		select {
		case r := <-p.queue:
			r.load(p.w)
		case <-p.w.closeStarted:
			p.drainAndAbort()
			return
		}
	}
}

// drainAndAbort cancels all remaining requests and stops accepting new ones.
func (p *chunkWorkerPool) drainAndAbort() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for {
		select {
		case r := <-p.queue:
			r.abort()
		default:
			return
		}
	}
}

// add adds the finished chunk to the world, at most once.
func (r *chunkRequest) add(tx *Tx) {
	if r.added {
		return
	}
	r.added = true

	w := tx.r
	pos := r.pos

	delete(w.chunkRequests, pos)
	if w.closed.Load() {
		return
	}
	if r.err != nil {
		w.conf.Log.Error("load chunk: "+r.err.Error(), "X", pos[0], "Z", pos[1])
		return
	}
	r.result = w.addChunk(pos, r.col)
}

// signal adds the finished chunk to the world and hands it to all callers
// waiting for it. It always runs at the top level of a world transaction.
func (r *chunkRequest) signal(tx *Tx) {
	if r.signalled {
		return
	}
	r.signalled = true

	r.add(tx)
	if tx.w.closed.Load() {
		return
	}
	callbacks := r.callbacks
	r.callbacks = nil
	for _, recv := range callbacks {
		recv(tx, r.result)
	}
}
