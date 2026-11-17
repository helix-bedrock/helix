package mob

import (
	"math"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/effect"
)

// A mob's effects (Actor's effects vector in BDS 1.26.60). The mob keeps the list; the
// engine gives, removes and ticks effects through EffectHooks, since what each effect does
// is the engine's. Goals and components read the list while the mob thinks and ask for
// changes, which land at Apply; the list changes only on the region's goroutine.

// EffectHooks is what an engine with mob effects implements on its Hooks.
type EffectHooks interface {
	// EffectID is the ID of the effect with the name, with or without "minecraft:". Runs:
	// any goroutine.
	EffectID(name string) (int32, bool)
	// GiveEffect gives the entity with the ID (m itself, another mob or a player) the
	// effect, as Actor::addEffect. Runs: m's Apply.
	GiveEffect(m *Mob, target behavior.ID, e effect.Instance)
	// RemoveEffect takes effect id away from the entity with the ID. Runs: m's Apply.
	RemoveEffect(m *Mob, target behavior.ID, id int32)
	// ClearEffects takes all of m's effects away (Actor::removeAllEffects). Runs: m's Apply.
	ClearEffects(m *Mob)
	// EffectTicks is the ticks left of effect id on the entity with the ID, 0 for none.
	// Runs: m's think.
	EffectTicks(m *Mob, target behavior.ID, id int32) int32
	// CanReceiveEffect is Actor::canReceiveMobEffectsFromGameplay of the entity with the
	// ID and its not being invulnerable to an attack by the entity by. Runs: m's think.
	CanReceiveEffect(m *Mob, target, by behavior.ID) bool
}

// Effects is the mob's effect list, for the engine. Runs: the region's goroutine, outside
// the think phase.
func (m *Mob) Effects() *effect.List { return &m.effects }

// HasEffect implements behavior.Mob.
func (m *Mob) HasEffect(id int) bool {
	_, ok := m.effects.Get(int32(id)) //nolint:gosec // effect IDs are small
	return ok
}

// EffectAmplifier implements behavior.Jumper and behavior.BlockJumper.
func (m *Mob) EffectAmplifier(id int) (int32, bool) {
	return m.effects.Amplifier(int32(id)) //nolint:gosec // effect IDs are small
}

// AddEffect implements behavior.PlayDeader: the effect lands at Apply.
func (m *Mob) AddEffect(id int, ticks, amplifier int32) {
	m.do(op{kind: opGiveEffect, id: m.id, n: int32(id), effect: effect.Instance{Amplifier: amplifier, Duration: ticks}}) //nolint:gosec // effect IDs are small
}

// RemoveAllEffects implements behavior drink's milk: the effects go at Apply.
func (m *Mob) RemoveAllEffects() { m.do(op{kind: opClearEffects}) }

// LastDamage implements behavior.PlayDeader: Mob's last hurt timestamp, cause and amount.
func (m *Mob) LastDamage() (stamp uint64, cause int, amount float32) {
	return m.hurtAt, m.lastCause, m.lastHurtAmount
}

// Health implements behavior.PlayDeader: the health attribute rounded up, as the health
// component's integer value.
func (m *Mob) Health() int32 { return int32(math.Ceil(float64(m.health))) }

// effectHooks are the mob's EffectHooks, false if its engine has none.
func (m *Mob) effectHooks() (EffectHooks, bool) {
	h, ok := m.hooks.(EffectHooks)
	return h, ok
}

// effectID is the ID of the named effect, false if unknown or the engine has no effects.
func (m *Mob) effectID(name string) (int32, bool) {
	h, ok := m.effectHooks()
	if !ok {
		return 0, false
	}
	return h.EffectID(name)
}

// giveEffectNamed queues the named effect for target.
func (m *Mob) giveEffectNamed(target behavior.ID, name string, ticks, amplifier int32, ambient bool) {
	m.do(op{kind: opGiveEffect, id: target, name: name, effect: effect.Instance{Amplifier: amplifier, Duration: ticks, Ambient: ambient}})
}

// runEffectOp does an effect op at Apply.
func (m *Mob) runEffectOp(o *op) {
	if o.kind == opDrinkPotion {
		if h, ok := m.hooks.(PotionHooks); ok && !m.Dead() {
			h.DrinkPotion(m, int16(o.n)) //nolint:gosec // carried from an int16
		}
		return
	}
	h, ok := m.effectHooks()
	if !ok {
		return
	}
	switch o.kind {
	case opGiveEffect:
		e := o.effect
		e.ID = o.n
		if o.name != "" {
			if e.ID, ok = h.EffectID(o.name); !ok {
				return
			}
		}
		h.GiveEffect(m, o.id, e)
	case opRemoveEffect:
		if id, ok := h.EffectID(o.name); ok {
			h.RemoveEffect(m, o.id, id)
		}
	case opClearEffects:
		h.ClearEffects(m)
	}
}

// AddEffect implements state.Mob: healable's feed effects.
func (s stateMob) AddEffect(name string, duration, amplifier int32) {
	s.giveEffectNamed(s.id, name, duration, amplifier, false)
}

// CanReceiveEffect implements state.Actions.
func (a stateActions) CanReceiveEffect(target behavior.Actor, by behavior.ID) bool {
	h, ok := a.m.effectHooks()
	return ok && target != nil && h.CanReceiveEffect(a.m, target.ID(), by)
}

// GiveEffect implements state.Actions: the effect lands at Apply.
func (a stateActions) GiveEffect(target behavior.Actor, name string, ticks, amplifier int32, ambient bool) {
	if target != nil {
		a.m.giveEffectNamed(target.ID(), name, ticks, amplifier, ambient)
	}
}

// EffectTicks implements state.Actions.
func (a stateActions) EffectTicks(target behavior.Actor, name string) int32 {
	h, ok := a.m.effectHooks()
	if !ok || target == nil {
		return 0
	}
	id, ok := h.EffectID(name)
	if !ok {
		return 0
	}
	if target.ID() == a.m.id {
		if e, ok := a.m.effects.Get(id); ok {
			return e.Duration
		}
		return 0
	}
	return h.EffectTicks(a.m, target.ID(), id)
}

// RemoveEffect implements state.Actions: the effect goes at Apply.
func (a stateActions) RemoveEffect(target behavior.Actor, name string) {
	if target != nil {
		a.m.do(op{kind: opRemoveEffect, id: target.ID(), name: name})
	}
}

// PotionHooks is what an engine with potions implements on its Hooks, for drink_potion.
type PotionHooks interface {
	// UseDuration is Item::getMaxUseDuration of the stack, 0 for none. Runs: m's think.
	UseDuration(m *Mob, it behavior.Item) int32
	// DrinkPotion gives m the effects of the potion with the aux value. Runs: m's Apply.
	DrinkPotion(m *Mob, aux int16)
}

// drinkSpeedPenalty is DrinkPotionGoal's "Drinking speed penalty" modifier (UUID from BDS
// 1.26.60's static init 0xBFA7E60, made in 0x587D500): an addition on the movement value,
// its amount the goal's speed_modifier.
const drinkSpeedPenalty = "5CD17E52-A79A-43D3-A529-90FDE04B181E"

// UseDuration implements behavior.PotionDrinker.
func (m *Mob) UseDuration(it behavior.Item) int32 {
	if h, ok := m.hooks.(PotionHooks); ok {
		return h.UseDuration(m, it)
	}
	return 0
}

// DrinkPotion implements behavior.PotionDrinker: the effects land at Apply.
func (m *Mob) DrinkPotion(aux int16) { m.do(op{kind: opDrinkPotion, n: int32(aux)}) }

// SpeedModifier implements behavior.PotionDrinker.
func (m *Mob) SpeedModifier(on bool, amount float32) {
	if !on {
		m.removeSpeedModifier(drinkSpeedPenalty)
		return
	}
	m.addSpeedModifier(attrModifier{id: drinkSpeedPenalty, op: modAdd, amount: amount})
}

// SetEffectSpeed puts an effect's modifier on the movement attribute (multiply total, as
// BDS's Speed and Slowness), or takes it off at amount 0. Runs: the region's goroutine.
func (m *Mob) SetEffectSpeed(id string, amount float32) {
	m.removeSpeedModifier(id)
	if amount != 0 {
		m.addSpeedModifier(attrModifier{id: id, op: modMultiplyTotal, amount: amount})
	}
}

// EffectImmune reports that minecraft:mob_effect_immunity keeps the named effect off the
// mob.
func (m *Mob) EffectImmune(name string) bool {
	return m.states != nil && m.states.MobEffectImmunity != nil && m.states.MobEffectImmunity.Immune(name)
}
