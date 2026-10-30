package world

import (
	"container/heap"
	"runtime"
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
}

// NewScheduler starts a scheduler with n tick goroutines; 0 means one per
// CPU. With 1, regions tick one after another in a fixed order. Call Close
// after closing every dimension that uses it.
func NewScheduler(n int) *Scheduler {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	s := &Scheduler{wake: make(chan struct{}, 1), work: make(chan *Region), stop: make(chan struct{})}
	s.wg.Add(n + 1)
	go s.dispatch()
	for range n {
		go func() {
			defer s.wg.Done()
			for r := range s.work {
				r.d.runTick(r)
			}
		}()
	}
	return s
}

// Close stops the scheduler. Regions still scheduled are not ticked again.
func (s *Scheduler) Close() {
	s.once.Do(func() { close(s.stop) })
	s.wg.Wait()
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
