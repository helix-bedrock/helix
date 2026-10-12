// Package hook holds the listener lists that Helix events are called
// through (RFC 0001). The engine calls a List directly where something
// happens; plugins add listeners to it through the helix package.
//
// An event nobody listens to costs one atomic load: call sites check Active
// before building the event.
package hook

import (
	"fmt"
	"slices"
	"sync/atomic"
)

// Priority orders listeners of one event. Lower priorities run first, so
// higher ones see and may override their changes. Monitor runs last and
// should only observe.
type Priority int

// Priorities, in the order listeners run.
const (
	Lowest Priority = iota
	Low
	Normal
	High
	Highest
	Monitor
)

// Cancellable is embedded in events that can be cancelled.
type Cancellable struct{ cancelled bool }

// Cancel stops the action the event is about. Later listeners still run,
// unless they were registered to ignore cancelled events.
func (c *Cancellable) Cancel() { c.cancelled = true }

// Uncancel undoes Cancel, so the action happens after all.
func (c *Cancellable) Uncancel() { c.cancelled = false }

// Cancelled reports whether a listener cancelled the event.
func (c *Cancellable) Cancelled() bool { return c.cancelled }

// Owner is who registered a listener: a plugin.
type Owner interface {
	// Enabled reports whether the owner's listeners should run.
	Enabled() bool
	// Run runs f, which calls the listener for the event named, recovering
	// and reporting a panic in it.
	Run(event string, f func())
}

type listener[E any] struct {
	o               Owner
	fn              func(*E)
	priority        Priority
	ignoreCancelled bool
	filter          func(*E) bool
}

// List holds the listeners of one event, sorted by priority. It is read
// from every region without locking and replaced whole when a listener is
// added. The zero value is an empty list.
type List[E any] struct {
	ls   atomic.Pointer[[]listener[E]]
	name atomic.Pointer[string]
}

// Add adds a listener. filter, if not nil, is checked first and skips the
// listener when it returns false.
func (l *List[E]) Add(o Owner, fn func(*E), p Priority, ignoreCancelled bool, filter func(*E) bool) {
	if l.name.Load() == nil {
		n := fmt.Sprintf("%T", (*E)(nil))[1:]
		l.name.Store(&n)
	}
	for {
		old := l.ls.Load()
		var cur []listener[E]
		if old != nil {
			cur = *old
		}
		next := append(slices.Clone(cur), listener[E]{o: o, fn: fn, priority: p, ignoreCancelled: ignoreCancelled, filter: filter})
		slices.SortStableFunc(next, func(a, b listener[E]) int { return int(a.priority - b.priority) })
		if l.ls.CompareAndSwap(old, &next) {
			return
		}
	}
}

// Active reports whether any listener is registered. Call sites check it
// before building an event.
func (l *List[E]) Active() bool {
	p := l.ls.Load()
	return p != nil && len(*p) > 0
}

type cancellable interface{ Cancelled() bool }

// Fire calls the listeners for e in priority order and reports whether the
// event ended up cancelled.
func (l *List[E]) Fire(e *E) (cancelled bool) {
	p := l.ls.Load()
	if p == nil {
		return false
	}
	c, isCancellable := any(e).(cancellable)
	name := ""
	if n := l.name.Load(); n != nil {
		name = *n
	}
	for _, x := range *p {
		if !x.o.Enabled() {
			continue
		}
		if x.ignoreCancelled && isCancellable && c.Cancelled() {
			continue
		}
		if x.filter != nil && !x.filter(e) {
			continue
		}
		x.o.Run(name, func() { x.fn(e) })
	}
	return isCancellable && c.Cancelled()
}
