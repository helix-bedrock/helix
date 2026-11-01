package player

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
)

// Priority says when a listener runs (API_DESIGN.md, "Events"): First to
// Last, then Monitor. The last listener to run has the final say; Monitor
// listeners only look, after everything.
type Priority uint8

// The priorities, in the order they run.
const (
	First Priority = iota
	Early
	Normal
	Late
	Last
	Monitor
)

// Cancellable is embedded in events that can be cancelled.
type Cancellable struct{ cancelled bool }

// Cancel stops what the event is about. A later listener may Uncancel it.
func (c *Cancellable) Cancel() { c.cancelled = true }

// Uncancel undoes Cancel.
func (c *Cancellable) Uncancel() { c.cancelled = false }

// Cancelled reports whether the event was cancelled.
func (c *Cancellable) Cancelled() bool { return c.cancelled }

// listener is one registered listener of an event of type E.
type listener[E any] struct {
	owner    string
	priority Priority
	seq      int
	fn       func(e *E)
}

// listeners are an event's listeners, sorted by priority then registration
// order at freeze. Firing runs on the caller's goroutine and allocates
// nothing; with no listeners the event is never built (check Has first).
type listeners[E any] struct {
	list []listener[E]
}

// add registers fn; seq keeps registrations of one priority in order.
func (l *listeners[E]) add(owner string, p Priority, fn func(e *E)) {
	l.list = append(l.list, listener[E]{owner: owner, priority: p, seq: len(l.list), fn: fn})
}

// freeze sorts the listeners.
func (l *listeners[E]) freeze() {
	slices.SortStableFunc(l.list, func(a, b listener[E]) int { return cmp.Compare(a.priority, b.priority) })
}

// Has reports whether anyone listens, so an event nobody hears is never
// built.
func (l *listeners[E]) Has() bool { return len(l.list) > 0 }

// fire runs every listener in order. A panicking listener is logged with
// its plugin's name and skipped; the server keeps running.
func (l *listeners[E]) fire(log *slog.Logger, e *E) {
	for i := range l.list {
		l.call(log, &l.list[i], e)
	}
}

func (l *listeners[E]) call(log *slog.Logger, li *listener[E], e *E) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("event listener panicked", "plugin", li.owner, "event", fmt.Sprintf("%T", e), "panic", r)
		}
	}()
	li.fn(e)
}
