package mob

import "github.com/helix-bedrock/entity/definition"

// What a mob keeps when it leaves the world and comes back, as a fish in a bucket does.
// Mobs are not saved with their chunks yet; this is the part a bucket carries.

// Stack is the mob's definition group stack after its base components, as BDS saves it
// under "definitions".
func (m *Mob) Stack() []definition.StackEntry { return m.inst.Stack() }

// Restore puts back what a save holds before the mob's first tick: its group stack and its
// health (0 keeps the definition's). Runs: before the mob is in the world.
func (m *Mob) Restore(stack []definition.StackEntry, health float32) {
	m.inst.SetStack(stack)
	m.savedHealth = health
}
