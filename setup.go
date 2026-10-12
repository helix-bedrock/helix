package helix

import (
	"fmt"
	"slices"

	"github.com/helix-bedrock/helix/event"
	"github.com/helix-bedrock/helix/server/hook"
)

//go:generate go run ./internal/gen/events

// Option changes how a listener is registered.
type Option func(*options)

type options struct {
	priority        event.Priority
	ignoreCancelled bool
	blocks          []string
	worlds          []string
}

// Priority sets when the listener runs relative to others of the same
// event. The default is event.Normal.
func Priority(p event.Priority) Option { return func(o *options) { o.priority = p } }

// IgnoreCancelled skips the listener if an earlier one cancelled the event.
func IgnoreCancelled() Option { return func(o *options) { o.ignoreCancelled = true } }

// OnlyBlock calls the listener only for events about one of the blocks
// named, such as "minecraft:diamond_ore". It may only be used with events
// about a block; using it with another event fails the plugin's load.
func OnlyBlock(names ...string) Option {
	return func(o *options) { o.blocks = append(o.blocks, names...) }
}

// OnlyWorld calls the listener only for events in one of the worlds named.
// It may only be used with events that happen in a world.
func OnlyWorld(names ...string) Option {
	return func(o *options) { o.worlds = append(o.worlds, names...) }
}

type blockNamed interface{ BlockName() string }
type worldNamed interface{ WorldName() string }

// Setup is passed to a plugin's Setup method to register listeners. It is
// only valid during Setup.
type Setup struct {
	state   *pluginState
	srv     *Server
	errs    []error
	pending []func()
}

// commit makes the registered listeners live, all at once, after Setup
// succeeded.
func (r *Setup) commit() {
	for _, f := range r.pending {
		f()
	}
	r.pending = nil
}

func register[E any](r *Setup, l *hook.List[E], fn func(*E), opts []Option) {
	name := fmt.Sprintf("%T", (*E)(nil))[1:]
	if fn == nil {
		r.errs = append(r.errs, fmt.Errorf("%s: listener is nil", name))
		return
	}
	o := options{priority: event.Normal}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	var zero E
	var filters []func(*E) bool
	if len(o.blocks) > 0 {
		if _, ok := any(&zero).(blockNamed); !ok {
			r.errs = append(r.errs, fmt.Errorf("%s: OnlyBlock used with an event that is not about a block", name))
			return
		}
		blocks := slices.Clone(o.blocks)
		filters = append(filters, func(e *E) bool {
			return slices.Contains(blocks, any(e).(blockNamed).BlockName())
		})
	}
	if len(o.worlds) > 0 {
		if _, ok := any(&zero).(worldNamed); !ok {
			r.errs = append(r.errs, fmt.Errorf("%s: OnlyWorld used with an event that is not about a world", name))
			return
		}
		worlds := slices.Clone(o.worlds)
		filters = append(filters, func(e *E) bool {
			return slices.Contains(worlds, any(e).(worldNamed).WorldName())
		})
	}
	var filter func(*E) bool
	switch len(filters) {
	case 0:
	case 1:
		filter = filters[0]
	default:
		filter = func(e *E) bool {
			for _, f := range filters {
				if !f(e) {
					return false
				}
			}
			return true
		}
	}
	s, p, ic := r.state, o.priority, o.ignoreCancelled
	r.pending = append(r.pending, func() { l.Add(s, fn, p, ic, filter) })
}
