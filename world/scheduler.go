package world

import (
	"container/heap"
	"log/slog"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// Scheduler ticks regions on a pool of goroutines (RFC 0002, "Scheduling").
// Every region has its own 20 TPS schedule; the region whose tick is due
// earliest runs first, so a slow region never slows the others. One
// Scheduler is shared by every dimension of a server.
type Scheduler struct {
	mu    sync.Mutex
	queue regionQueue
	wake  chan struct{}
	work  chan *Region
	stop  chan struct{}
	wg    sync.WaitGroup
	once  sync.Once
	seq   atomic.Uint64
	// clock is the world's tick clock: a tick count and when it was set.
	clock atomic.Pointer[tickClock]
	// panicked is told of a panic in a tick or a chunk job (SetPanicHandler).
	panicked atomic.Pointer[PanicHandler]
	// help hands a region's parallel phase to idle helpers (Region.Parallel).
	help     chan *parallelJob
	helpWG   sync.WaitGroup
	helpOnce sync.Once
}

// PanicHandler is told of a panic the world recovered: what was panicked with and the stack.
// The panicking region stops ticking, so the server should stop. Runs: the goroutine that
// panicked.
type PanicHandler func(v any, stack []byte)

// SetPanicHandler sets what is told when a region's tick or a chunk job panics; nil only logs.
// Safe from any goroutine.
func (s *Scheduler) SetPanicHandler(fn PanicHandler) {
	if fn == nil {
		s.panicked.Store(nil)
		return
	}
	s.panicked.Store(&fn)
}

// recovered is deferred around ticks and jobs: it recovers a panic and passes it on, so one
// goroutine's panic does not kill the process before the players are told.
func (s *Scheduler) recovered(log *slog.Logger) {
	v := recover()
	if v == nil {
		return
	}
	stack := debug.Stack()
	if fn := s.panicked.Load(); fn != nil {
		(*fn)(v, stack)
		return
	}
	log.Error("World goroutine panicked", "panic", v, "stack", string(stack))
}

type tickClock struct {
	tick  int64
	start time.Time
}

// SetCurrentTick sets the world's tick, level.dat's currentTick, to tick
// now; it then advances one every TickDuration. Safe from any goroutine.
func (s *Scheduler) SetCurrentTick(tick int64) {
	s.clock.Store(&tickClock{tick: tick, start: time.Now()})
}

// CurrentTick returns the world's tick: the one set plus the ticks since.
// Every region and dimension ticking on s shares it, so tick times stay
// valid when chunks move between regions. Safe from any goroutine.
func (s *Scheduler) CurrentTick() int64 {
	c := s.clock.Load()
	return c.tick + int64(time.Since(c.start)/TickDuration)
}

// NewScheduler starts a scheduler with n tick goroutines; 0 means one per
// CPU. With 1, regions tick one after another in a fixed order. Call Close
// after closing every dimension that uses it.
func NewScheduler(n int) *Scheduler {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	s := &Scheduler{wake: make(chan struct{}, 1), work: make(chan *Region), stop: make(chan struct{})}
	s.SetCurrentTick(0)
	s.startHelpers(n)
	s.wg.Add(n + 1)
	go s.dispatch()
	for range n {
		go func() {
			defer s.wg.Done()
			for r := range s.work {
				r.d.tickSafely(r)
			}
		}()
	}
	return s
}

// Close stops the scheduler. Regions still scheduled are not ticked again.
func (s *Scheduler) Close() {
	s.once.Do(func() { close(s.stop) })
	s.wg.Wait()
	s.helpOnce.Do(s.stopHelpers)
}

// schedule queues r for its tick at r.next.
func (s *Scheduler) schedule(r *Region) {
	s.mu.Lock()
	heap.Push(&s.queue, r)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// dispatch hands each region to a tick goroutine when its tick is due.
func (s *Scheduler) dispatch() {
	defer s.wg.Done()
	defer close(s.work)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		s.mu.Lock()
		wait := time.Hour
		if len(s.queue) > 0 {
			r := s.queue[0]
			if wait = time.Until(r.next); wait <= 0 {
				heap.Pop(&s.queue)
				s.mu.Unlock()
				select {
				case s.work <- r:
				case <-s.stop:
					return
				}
				continue
			}
		}
		s.mu.Unlock()
		timer.Reset(wait)
		select {
		case <-s.stop:
			return
		case <-s.wake:
		case <-timer.C:
		}
	}
}

// regionQueue orders regions by their next tick, then by when they were
// made, so the order is fixed for equal deadlines.
type regionQueue []*Region

func (q regionQueue) Len() int { return len(q) }
func (q regionQueue) Less(i, j int) bool {
	if !q[i].next.Equal(q[j].next) {
		return q[i].next.Before(q[j].next)
	}
	return q[i].seq < q[j].seq
}
func (q regionQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *regionQueue) Push(x any) {
	if r, ok := x.(*Region); ok {
		*q = append(*q, r)
	}
}
func (q *regionQueue) Pop() any {
	old := *q
	r := old[len(old)-1]
	old[len(old)-1] = nil
	*q = old[:len(old)-1]
	return r
}
