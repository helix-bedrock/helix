package player

import (
	"strings"

	"github.com/helix-bedrock/helix/effect"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// EffectInfinite is the duration of an effect that never runs out, as BDS's /effect
// infinite gives.
const EffectInfinite = effect.Infinite

// ActiveEffect is an effect a player has; Duration is the ticks left (EffectInfinite for
// an endless one).
type ActiveEffect = effect.Instance

// Effect returns the amplifier of the player's effect id, false if it does
// not have it. Runs: the player's region.
func (p *Player) Effect(id int32) (int32, bool) { return p.effects.Amplifier(id) }

// Effects returns the player's effects. Runs: the player's region.
func (p *Player) Effects() []ActiveEffect { return p.effects.Clone() }

// AddEffect gives the player effect id at amplifier (0 is level I), for
// duration ticks, with particles. Runs: the player's region.
func (p *Player) AddEffect(id, amplifier, duration int32) {
	p.GiveEffect(ActiveEffect{ID: id, Amplifier: amplifier, Duration: duration})
}

// GiveEffect gives the player an effect. An instant effect does its work at once, at full
// strength. One the player has already is merged by BDS's MobEffectInstance update
// (0xCAB7840, mergeEffect) and the player is told of it again, as BDS's onEffectUpdated.
// Effects are kept in ID order, as BDS keeps them in a vector by ID. Runs: the player's
// region.
func (p *Player) GiveEffect(e ActiveEffect) {
	def, ok := p.s.game.effects[e.ID]
	if ok && def.Instant {
		if def.Apply != nil {
			def.Apply(p, e.Amplifier, 1)
		}
		p.flush()
		return
	}
	if ok && def.NoParticles {
		e.Hidden = true
	}
	now, added := p.effects.Give(e)
	op := byte(packet.MobEffectModify)
	if added {
		op = packet.MobEffectAdd
	}
	p.effectChanged(now, op, def, ok)
}

// effectChanged tells the player and those who see it about an effect given or changed,
// runs its Added hook and puts its modifiers on.
func (p *Player) effectChanged(e ActiveEffect, op byte, def *Effect, known bool) {
	p.send(&packet.MobEffect{
		EntityRuntimeID: p.runtimeID, Operation: op, EffectType: e.ID, Amplifier: e.Amplifier,
		Particles: !e.Hidden, Ambient: e.Ambient, Duration: e.Duration, Tick: p.tick(),
	})
	if known && def.Added != nil {
		def.Added(p, e.Amplifier)
	}
	p.applyEffectModifiers()
	p.sendVisibleEffects()
	p.flush()
}

// ApplyInstantEffect does an instant effect's work at scale (1 is full, as a splash
// potion right on the player), without giving the effect. False if id is not an instant
// effect. Runs: the player's region.
func (p *Player) ApplyInstantEffect(id, amplifier int32, scale float32) bool {
	def, ok := p.s.game.effects[id]
	if !ok || !def.Instant || def.Apply == nil {
		return false
	}
	def.Apply(p, amplifier, scale)
	p.flush()
	return true
}

// RemoveEffect takes effect id away from the player, with any weaker one stored behind
// it. Runs: the player's region.
func (p *Player) RemoveEffect(id int32) {
	if !p.effects.Remove(id) {
		return
	}
	p.effectGone(id)
}

// ClearEffects takes every effect away, as milk and death do. Runs: the player's region.
func (p *Player) ClearEffects() {
	for _, e := range p.Effects() {
		p.RemoveEffect(e.ID)
	}
}

// effectGone tells the player and those who see it that an effect went and runs its
// Removed hook.
func (p *Player) effectGone(id int32) {
	p.send(&packet.MobEffect{EntityRuntimeID: p.runtimeID, Operation: packet.MobEffectRemove, EffectType: id, Tick: p.tick()})
	if e, ok := p.s.game.effects[id]; ok && e.Removed != nil {
		e.Removed(p)
	}
	p.applyEffectModifiers()
	p.sendVisibleEffects()
	p.flush()
}

// tickEffects runs each effect's Tick and counts it down one tick, as BDS's
// MobEffectInstance tick (0xCAB7FD0): the kept effects count down too and go at 0; one
// that runs out gives way to the first kept effect, if any, else goes. The player is told
// of a kept effect coming back only when its level differs, as BDS's effect system
// (0x76B5550). Runs: the player's region, once per tick.
func (p *Player) tickEffects() {
	for i := 0; i < len(p.effects); {
		id := p.effects[i].ID
		left := p.effects[i].Left()
		if def, ok := p.s.game.effects[id]; ok && def.Tick != nil {
			def.Tick(p, p.effects[i].Amplifier, left)
		}
		// A Tick may have killed the player and cleared its effects.
		if i >= len(p.effects) || p.effects[i].ID != id {
			continue
		}
		switch p.effects[i].Countdown() {
		case effect.Gone:
			p.RemoveEffect(id)
			continue
		case effect.Restored:
			def, ok := p.s.game.effects[id]
			p.effectChanged(p.effects[i], packet.MobEffectModify, def, ok)
		}
		i++
	}
}

// effectsDied runs the Died hooks of the effects a dying player has (BDS's
// MobEffect::onActorDied), before death takes them away.
func (p *Player) effectsDied() {
	for _, e := range p.Effects() {
		if def, ok := p.s.game.effects[e.ID]; ok && def.Died != nil {
			def.Died(p, e.Amplifier)
		}
	}
}

// effectsHurt runs the Hurt hooks of the player's effects after it was hurt (BDS's
// MobEffect::onActorHurt).
func (p *Player) effectsHurt(src DamageSource) {
	for _, e := range p.Effects() {
		if def, ok := p.s.game.effects[e.ID]; ok && def.Hurt != nil {
			def.Hurt(p, e.Amplifier, src)
		}
	}
}

// applyEffectModifiers works out the attributes the effects' modifiers change, as BDS's
// AttributeInstance: the base plus additions, times one plus the base multipliers, times
// each total multiplier; on a maximum the same from its definition's maximum. Value
// modifiers apply to attributes whose value is their base (movement); an attribute that
// had one goes back to its base when the last one goes. Max modifiers keep the value
// within the new maximum.
func (p *Player) applyEffectModifiers() {
	sums := p.sumEffectModifiers()
	var changed []protocol.Attribute
	health := false
	for i := range playerAttributes {
		bit := uint32(1) << i
		if sums.maxed&bit != 0 || p.effectMaxed&bit != 0 {
			if p.applyMaxModifier(i, &sums) {
				changed = append(changed, p.attributes.entry(i))
				health = health || i == attrHealth
			}
		}
		if sums.valued&bit != 0 || p.effectValued&bit != 0 {
			if p.applyValueModifier(i, &sums) {
				changed = append(changed, p.attributes.entry(i))
			}
		}
	}
	p.effectValued, p.effectMaxed = sums.valued, sums.maxed
	// While loading there is no entity yet; the join sends every attribute.
	if p.entity == nil {
		return
	}
	if len(changed) > 0 {
		p.send(&packet.UpdateAttributes{EntityRuntimeID: p.runtimeID, Attributes: changed, Tick: p.entity.Tick()})
	}
	if health {
		p.showHealth()
	}
}

// modifierSums are the effects' attribute modifiers summed per attribute, for values and
// for maximums, with a bit per attribute that has any.
type modifierSums struct {
	add, mulBase, mulTotal          [len(playerAttributes)]float32
	maxAdd, maxMulBase, maxMulTotal [len(playerAttributes)]float32
	valued, maxed                   uint32
}

// sumEffectModifiers adds up the modifiers of every effect the player has, each times
// its amplifier plus one.
func (p *Player) sumEffectModifiers() modifierSums {
	var m modifierSums
	for i := range m.mulTotal {
		m.mulTotal[i], m.maxMulTotal[i] = 1, 1
	}
	for _, e := range p.effects {
		def, ok := p.s.game.effects[e.ID]
		if !ok {
			continue
		}
		for _, mod := range def.Modifiers {
			i, ok := attributeIndex(mod.Attribute)
			if !ok {
				continue
			}
			v := mod.Amount * float32(e.Amplifier+1)
			a, mb, mt := &m.add[i], &m.mulBase[i], &m.mulTotal[i]
			if mod.OnMax {
				a, mb, mt = &m.maxAdd[i], &m.maxMulBase[i], &m.maxMulTotal[i]
				m.maxed |= 1 << i
			} else {
				m.valued |= 1 << i
			}
			switch mod.Operation {
			case ModifierAdd:
				*a += v
			case ModifierMultiplyBase:
				*mb += v
			case ModifierMultiplyTotal:
				*mt *= 1 + v
			}
		}
	}
	return m
}

// applyMaxModifier sets attribute i's maximum bonus from the sums, none if no effect
// changes it any more. True if it changed.
func (p *Player) applyMaxModifier(i int, m *modifierSums) bool {
	a := &p.attributes[i]
	top := playerAttributes[i].max
	if a.limit != 0 {
		top = a.limit
	}
	bonus := float32(0)
	if m.maxed&(1<<i) != 0 {
		bonus = (top+m.maxAdd[i])*(1+m.maxMulBase[i])*m.maxMulTotal[i] - top
	}
	if bonus == a.maxBonus {
		return false
	}
	a.maxBonus = bonus
	p.attributes.set(i, a.current)
	return true
}

// applyValueModifier sets attribute i's value from its base and the sums, the base alone
// if no effect changes it any more. True if it changed.
func (p *Player) applyValueModifier(i int, m *modifierSums) bool {
	a := &p.attributes[i]
	v := a.base
	if m.valued&(1<<i) != 0 {
		v = (a.base + m.add[i]) * (1 + m.mulBase[i]) * m.mulTotal[i]
	}
	return p.attributes.set(i, v)
}

// visibleEffects is the actor data those who see the player draw its effects' particles
// from: up to eight effects with particles, seven bits each (the ID and the ambient
// bit).
func (p *Player) visibleEffects() int64 { return p.effects.Visible() }

// sendVisibleEffects shows the player and those who see it its effects' particles. The
// player gets it too: its own client draws the particles around it from this data.
func (p *Player) sendVisibleEffects() {
	if p.entity == nil {
		return
	}
	pk := actorDataAt(p.runtimeID, protocol.EntityMetadata{protocol.EntityDataKeyVisibleMobEffects: p.visibleEffects()}, p.entity.Tick())
	p.send(pk)
	if r := p.entity.Region(); r != nil {
		r.Broadcast(p.blockPos(), pk, p.viewer)
	}
}

// saveEffects writes the player's effects as BDS saves them; no effects, no key.
func (p *Player) saveEffects(data map[string]any) { p.effects.Save(data) }

// loadEffects reads the effects BDS saved. They are put back without their Added hooks,
// since what those set (Absorption's extra health) was saved with the attributes.
func (p *Player) loadEffects(data map[string]any) {
	p.effects = effect.Load(data)
	p.applyEffectModifiers()
}

// sendEffects tells a joining player the effects it was saved with.
func (p *Player) sendEffects() {
	for _, e := range p.effects {
		p.send(&packet.MobEffect{
			EntityRuntimeID: p.runtimeID, Operation: packet.MobEffectAdd, EffectType: e.ID, Amplifier: e.Amplifier,
			Particles: !e.Hidden, Ambient: e.Ambient, Duration: e.Duration,
		})
	}
}

// EffectNamed returns a copy of the registered effect with the name, with or without the
// "minecraft:" prefix. Runs: anywhere.
func (s *Host) EffectNamed(name string) (Effect, bool) {
	name = strings.TrimPrefix(name, "minecraft:")
	for _, e := range s.game.effects {
		if e.Name == name {
			return *e, true
		}
	}
	return Effect{}, false
}

// tick is the player's entity's tick, 0 before it has one.
func (p *Player) tick() uint64 {
	if p.entity == nil {
		return 0
	}
	return p.entity.Tick()
}
