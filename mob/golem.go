package mob

import "github.com/helix-bedrock/entity/behavior"

// target_when_pushed's hook (behavior.Pushable). BDS 1.26.60's push gives the
// pushed entity PushedByComponent with the pusher's ID before the push lands;
// RemoveFromAllEntitiesSystem<PushedByComponent> takes it off every entity once a tick.
// Pushes run in Apply here, so the goals read the push in the mob's next think, and the
// component goes once they have run.

// PushedBy implements behavior.Pushable.
func (m *Mob) PushedBy() (behavior.ID, bool) { return m.pushedBy, m.hasPushedBy }

// ClearPushedBy implements behavior.Pushable.
func (m *Mob) ClearPushedBy() { m.hasPushedBy = false }

// PushedBy implements state.PushWorld: it names the pusher on a pushed mob.
func (pushWorld) PushedBy(a, by behavior.Actor) {
	if o := mobOf(a); o != nil {
		o.pushedBy, o.hasPushedBy = by.ID(), true
	}
}
