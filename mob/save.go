package mob

import "github.com/helix-bedrock/entity/definition"

// What a mob keeps when it leaves the world and comes back: with its chunk (the player
// package's mobsave.go writes BDS's actor format from these) or in a bucket.

// BDS's actor flag and data numbers for a name (ActorFlags, ActorDataIDs).
const (
	// FlagCanShowName is set by NameableComponent::nameEntity (1.26.60 0x7139940).
	FlagCanShowName = 14
	// FlagAlwaysShowName is minecraft:nameable's always_show.
	FlagAlwaysShowName = 15
	// dataName is the name actor data key.
	dataName = 4
)

// Stack is the mob's definition group stack after its base components, as BDS saves it
// under "definitions".
func (m *Mob) Stack() []definition.StackEntry { return m.inst.Stack() }

// Restore puts back what a save holds before the mob's first tick: its group stack and its
// health (0 keeps the definition's). Runs: before the mob is in the world.
func (m *Mob) Restore(stack []definition.StackEntry, health float32) {
	m.inst.SetStack(stack)
	m.savedHealth = health
}

// SetNameTag names the mob as BDS's Actor::setNameTag does: the name actor data and the can
// show name flag; "" takes the name away. A name never makes the mob persistent by itself:
// naming with a name tag does that (Name). Runs: the mob's region, outside its think.
func (m *Mob) SetNameTag(name string) {
	m.nameTag = name
	m.setData(dataName, name)
	m.SetFlag(FlagCanShowName, name != "")
}

// SetNameVisible sets the always show name flag (BDS's CustomNameVisible).
func (m *Mob) SetNameVisible(on bool) { m.SetFlag(FlagAlwaysShowName, on) }

// Nameable reports whether a name tag may name the mob: it has minecraft:nameable and the
// component allows renaming (NameableComponent::getInteraction, 1.26.60 0x7139820, offers
// nothing when the first field, allow_name_tag_renaming, is off).
func (m *Mob) Nameable() bool {
	return m.states != nil && m.states.Nameable != nil && m.states.Nameable.CanRename()
}

// Name is a name tag used on the mob, NameableComponent::nameEntity (1.26.60 0x7139940):
// the name and its flag, the persistent marker (the component Actor::load also adds for
// Persistent), always_show, then the name_actions event whose filter lists the name, else
// default_trigger. False when the mob cannot be named. Runs: the mob's region, outside its
// think.
func (m *Mob) Name(name string) bool {
	if !m.Nameable() || name == "" {
		return false
	}
	m.SetNameTag(name)
	m.persistent = true
	if m.states.Nameable.Config().AlwaysShow {
		m.SetNameVisible(true)
	}
	m.states.Nameable.Named(&m.stateEnv, name)
	return true
}

// SetRotation puts back a saved yaw and pitch before the first tick.
func (m *Mob) SetRotation(yaw, pitch float32) {
	m.yaw, m.headYaw, m.bodyYaw, m.prevYaw = yaw, yaw, yaw, yaw
	m.pitch, m.prevPitch = pitch, pitch
}
