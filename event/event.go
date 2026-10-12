// Package event names the events plugins listen to (RFC 0001), their
// priorities and cancellation.
//
// The events are the engine's own types: the engine fires them directly
// where things happen, and this package gives them short names for plugins
// (event.BlockBreak is player.BlockBreakEvent). An event nobody listens to
// is never built.
//
// An event is passed to listeners as a pointer. Fields that are pointers may
// be changed to change the outcome (the drops of a broken block, a chat
// message). Events with Cancel can be cancelled, which stops the action.
//
// Listeners run on the region that owns what the event is about, during
// that region's tick, in parallel with other regions (RFC 0002). Tx is the
// region's transaction: use it for world access, and do not keep it or the
// event after the listener returns.
package event

import "github.com/helix-bedrock/helix/server/hook"

// Priority orders listeners of one event. Lower priorities run first, so
// higher ones see and may override their changes. Monitor runs last and
// should only observe.
type Priority = hook.Priority

// Priorities, in the order listeners run.
const (
	Lowest  = hook.Lowest
	Low     = hook.Low
	Normal  = hook.Normal
	High    = hook.High
	Highest = hook.Highest
	Monitor = hook.Monitor
)

// Cancellable is embedded in events that can be cancelled.
type Cancellable = hook.Cancellable
