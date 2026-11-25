package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/protocol"
)

// Taming, trusting, healing and sitting: the interactions a player's use on a mob runs, as
// BDS 1.26.60's PlayerInteractionSystem (its list, built in
// BDS's code, is tried in order by
// BDS's code, and the first component that offers an interaction
// wins). The engine calls these in that order and does the item work.

// Owner is the entity that tamed the mob, behavior.NoID for none.
func (m *Mob) Owner() behavior.ID { return m.owner }

// SetOwner makes an entity the mob's owner and shows it to those who see the mob, as
// BDS's code. Loading a saved owner calls it too.
func (m *Mob) SetOwner(id behavior.ID) {
	m.owner = id
	m.setData(protocol.EntityDataKeyOwner, int64(id))
}

// Tame is a player trying to tame the mob with the item in hand, as BDS 1.26.60's
// TameableComponent (getInteraction, its callback): a mob that is not
// tame yet takes one of tame_items, then tames with the probability or sends the failed
// event. It returns the item the used one turns into ("" to use it up). Runs: the mob's
// region.
func (m *Mob) Tame(player behavior.Actor, h state.Hand) (result string, ok bool) {
	if m.states == nil || m.states.Tameable == nil || m.stateEnv.Self == nil || player == nil || m.Dead() {
		return "", false
	}
	t := m.states.Tameable
	i := t.Feed(&m.stateEnv, player, h)
	if i < 0 {
		return "", false
	}
	return t.Config().TameItems[i].Result, true
}

// FeedTrust is a player feeding a mob with minecraft:trusting, as BDS 1.26.60's
// TrustingComponent (getInteraction, its callback): a mob that does not
// trust yet takes one of trust_items and trusts with the probability. The engine shows the
// eating first and uses up the item. Runs: the mob's region.
func (m *Mob) FeedTrust(h state.Hand) bool {
	if m.states == nil || m.states.Trusting == nil || m.stateEnv.Self == nil || m.Dead() {
		return false
	}
	c := m.states.Trusting
	if state.TrustingInteraction(stateMob{m}, c, h) == "" {
		return false
	}
	state.TryTrust(&m.stateEnv, c)
	return true
}

// Heal is a player feeding a mob with minecraft:healable one of its items: it heals and may
// get the item's effects. The engine uses up the item. Runs: the mob's region.
func (m *Mob) Heal(h state.Hand) bool {
	if m.states == nil || m.states.Healable == nil || m.stateEnv.Self == nil || m.Dead() {
		return false
	}
	return m.states.Healable.Feed(&m.stateEnv, h) >= 0
}

// FeedMount is a player using an item on an unridden mob with minecraft:tamemount: a feed
// item raises its temper (the engine uses it up), an auto-reject item is refused.
func (m *Mob) FeedMount(h state.Hand) (state.MountResult, bool) {
	if m.states == nil || m.states.Tamemount == nil || m.stateEnv.Self == nil || m.Dead() {
		return state.InteractNone, false
	}
	r, _ := m.states.Tamemount.Feed(&m.stateEnv, h)
	if r == state.InteractReject {
		// A packet capture of BDS 1.26.60 shows each use of an auto_reject_items item
		// (a saddle on a wild horse) running BDS's code.
		stateMob{m}.MakeMad()
	}
	return r, r != state.InteractNone
}

// ToggleSit is a player's use on a mob with minecraft:sittable: its owner makes it sit or
// stand (state.Sit). Runs: the mob's region.
func (m *Mob) ToggleSit(player behavior.Actor) bool {
	if m.states == nil || m.states.Sittable == nil || m.stateEnv.Self == nil || m.Dead() {
		return false
	}
	return state.Sit(&m.stateEnv, m.states.Sittable, player)
}

// The horse's refusal, as BDS 1.26.60's BDS's code: it stands up
// (BDS's code: actor flag 39, and on the server HorseStandCounterComponent
// set to 1, which HorsePostTravelSystem counts down and clears, see standTick), raises
// horse flag 128 for 30 ticks (counted down in the horse's tick) and
// plays the mad sound (LevelSoundEvent 19) at its head.
const (
	flagStanding   = 39
	dataHorseFlags = 16
	horseFlagMad   = 128
	madTicks       = 30
	soundMad       = 19
)

// MakeMad is the mount throwing its rider (state.Mob).
func (s stateMob) MakeMad() {
	if !horseClass[s.typ] {
		return
	}
	s.SetFlag(flagStanding, true)
	s.standTime = 1
	s.madTime = 1
	s.setHorseFlags(horseFlagMad, true)
	s.do(op{kind: opSound, n: soundMad, at: s.Eyes()})
}

// madTick counts the rear-up down. Runs: think.
func (m *Mob) madTick() {
	if m.madTime <= 0 {
		return
	}
	t := m.madTime
	m.madTime++
	if t >= madTicks {
		m.madTime = 0
		m.setHorseFlags(horseFlagMad, false)
	}
}

// standTicks is how long a horse stands: HorsePostTravelSystem ends the stand on the tick
// its counter was already 20.
const standTicks = 20

// standTick is HorsePostTravelSystem's stand step: a standing
// horse's counter goes up each tick, and once it was 20 the counter goes back to 0 and
// actor flag 39 is cleared, so the horse comes down. BDS also ends the stand when
// HorseLandedOnGroundFlagComponent is set (a jump landing); horse jumps are not built, so
// that never happens here. Runs: think.
func (m *Mob) standTick() {
	if m.standTime <= 0 {
		return
	}
	t := m.standTime
	m.standTime++
	if t < standTicks {
		return
	}
	m.standTime = 0
	if m.Flag(flagStanding) {
		m.SetFlag(flagStanding, false)
	}
}

// setHorseFlags sets or clears bits of actor data 16, the horse flags.
func (m *Mob) setHorseFlags(bits int32, on bool) {
	var cur int32
	for _, d := range m.data {
		if v, ok := d.Value.(int32); ok && d.Key == dataHorseFlags {
			cur = v
		}
	}
	if on {
		cur |= bits
	} else {
		cur &^= bits
	}
	m.setData(dataHorseFlags, cur)
}

// horseClass are the mobs BDS writes as its Horse class, whose makeMad rears them up; other
// mounts with minecraft:tamemount (camels, llamas) do not.
var horseClass = map[string]bool{
	"minecraft:horse": true, "minecraft:donkey": true, "minecraft:mule": true,
	"minecraft:skeleton_horse": true, "minecraft:zombie_horse": true,
}

// DropItem spawns one of the item a descriptor names at the mob,
// at Apply.
func (s stateMob) DropItem(item string) bool {
	name := ns(item)
	if name == "" {
		return false
	}
	s.Mob.DropItem(behavior.Item{Name: name, Count: 1})
	return true
}

// Bribe is a player bribing a mob with minecraft:bribeable one of its bribe_items, as BDS
// 1.26.60's BribeableComponent (getInteraction, callback): offered while
// the bribe time and the cooldown are over; a mob not yet bribed gets the bribe time and
// the treasure flag. The engine shows the eating and uses up the item. Runs:
// the mob's region.
func (m *Mob) Bribe(h state.Hand) bool {
	if m.states == nil || m.states.Bribeable == nil || m.stateEnv.Self == nil || m.Dead() {
		return false
	}
	b := m.states.Bribeable
	if !b.Takes(h) {
		return false
	}
	b.Bribe(stateMob{m})
	return true
}

// Give is a player giving a mob with minecraft:giveable the item in hand, as BDS 1.26.60's
// GiveableComponent (getInteraction, callback): the first trigger not
// cooling down that takes the item, when the inventory has room; one of the item (no aux,
// no user data) goes into the inventory and on_give fires. The engine uses up the item.
// BDS adds the item before on_give; here on_give runs first. Runs: the mob's region.
func (m *Mob) Give(player behavior.Actor, name string) bool {
	if m.states == nil || m.states.Giveable == nil || m.stateEnv.Self == nil || m.Dead() || m.items.Inventory == nil {
		return false
	}
	it := behavior.Item{Name: name, Count: 1}
	if m.states.Giveable.Give(&m.stateEnv, player, HeldItem{Name: name}, m.inv.CanAdd(it)) < 0 {
		return false
	}
	m.inv.Add(it)
	return true
}
