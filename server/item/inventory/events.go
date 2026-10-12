package inventory

import (
	"github.com/helix-bedrock/helix/server/hook"
	"github.com/helix-bedrock/helix/server/item"
)

// Events holds the listener lists of inventory events (Helix, RFC 0001).
// They replace Dragonfly's per-inventory Handler. A server shares one Events
// between its players, which fire them for the inventories they act on.
type Events struct {
	Take  hook.List[TakeEvent]
	Place hook.List[PlaceEvent]
	Drop  hook.List[DropEvent]
}

// Action is embedded in inventory events.
type Action struct {
	hook.Cancellable
	// Holder is who acted, usually the player.
	Holder Holder
	// Inventory is the inventory acted on.
	Inventory *Inventory
	Slot      int
	Item      item.Stack
}

// TakeEvent is called when a stack is taken from a slot. It may be the whole
// stack or part of it.
type TakeEvent struct{ Action }

// PlaceEvent is called when a stack is placed in a slot.
type PlaceEvent struct{ Action }

// DropEvent is called when a stack is dropped out of a slot.
type DropEvent struct{ Action }

// FireTake fires TakeEvent and reports whether it was cancelled. ev may be nil.
func (ev *Events) FireTake(h Holder, inv *Inventory, slot int, it item.Stack) (cancelled bool) {
	if ev == nil || !ev.Take.Active() {
		return false
	}
	return ev.Take.Fire(&TakeEvent{Action{Holder: h, Inventory: inv, Slot: slot, Item: it}})
}

// FirePlace fires PlaceEvent and reports whether it was cancelled. ev may be
// nil.
func (ev *Events) FirePlace(h Holder, inv *Inventory, slot int, it item.Stack) (cancelled bool) {
	if ev == nil || !ev.Place.Active() {
		return false
	}
	return ev.Place.Fire(&PlaceEvent{Action{Holder: h, Inventory: inv, Slot: slot, Item: it}})
}

// FireDrop fires DropEvent and reports whether it was cancelled. ev may be
// nil.
func (ev *Events) FireDrop(h Holder, inv *Inventory, slot int, it item.Stack) (cancelled bool) {
	if ev == nil || !ev.Drop.Active() {
		return false
	}
	return ev.Drop.Fire(&DropEvent{Action{Holder: h, Inventory: inv, Slot: slot, Item: it}})
}
