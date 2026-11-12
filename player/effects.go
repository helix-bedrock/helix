package player

import (
	"slices"
	"strings"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// EffectInfinite is the duration of an effect that never runs out, as BDS's /effect
// infinite gives.
const EffectInfinite int32 = -1

// ActiveEffect is an effect a player has; Duration is the ticks left (EffectInfinite for
// an endless one).
type ActiveEffect struct {
	ID        int32
	Amplifier int32
	Duration  int32
	// Ambient marks an effect from a beacon or conduit (fainter particles); Hidden hides
	// its particles.
	Ambient, Hidden bool
	// age is the ticks an endless effect has had, which its Tick is given.
	age int32
	// stored is a weaker effect of the same kind with a longer duration, which comes back
	// when this one runs out (BDS's StoredEffects).
	stored *ActiveEffect
}

// Effect returns the amplifier of the player's effect id, false if it does
// not have it. Runs: the player's region.
func (p *Player) Effect(id int32) (int32, bool) {
	for _, e := range p.effects {
		if e.ID == id {
			return e.Amplifier, true
		}
	}
	return 0, false
}

// Effects returns the player's effects. Runs: the player's region.
func (p *Player) Effects() []ActiveEffect { return slices.Clone(p.effects) }

// AddEffect gives the player effect id at amplifier (0 is level I), for
// duration ticks, with particles. Runs: the player's region.
func (p *Player) AddEffect(id, amplifier, duration int32) {
	p.GiveEffect(ActiveEffect{ID: id, Amplifier: amplifier, Duration: duration})
}

// GiveEffect gives the player an effect. An instant effect does its work at once, at full
// strength. One the player has already is merged as BDS keeps its StoredEffects: a
// stronger one takes over (keeping the old one for later if the old one lasts longer), a
// weaker one that lasts longer waits behind it, and the same level takes the longer
// duration. Runs: the player's region.
func (p *Player) GiveEffect(e ActiveEffect) {
	def, ok := p.s.game.effects[e.ID]
	if ok && def.Instant {
		if def.Apply != nil {
			def.Apply(p, e.Amplifier, 1)
		}
		p.flush()
		return
	}
	e.age, e.stored = 0, nil
	i := slices.IndexFunc(p.effects, func(o ActiveEffect) bool { return o.ID == e.ID })
	if i < 0 {
		p.effects = append(p.effects, e)
		p.effectChanged(e, packet.MobEffectAdd, def, ok)
		return
	}
	if !mergeEffect(&p.effects[i], e) {
		return
	}
	p.effectChanged(p.effects[i], packet.MobEffectModify, def, ok)
}

// mergeEffect merges e into the effect old, true if what the player has changed.
func mergeEffect(old *ActiveEffect, e ActiveEffect) bool {
	switch {
	case e.Amplifier > old.Amplifier:
		if longer(old.Duration, e.Duration) {
			kept := *old
			e.stored = &kept
		}
		*old = e
		return true
	case e.Amplifier < old.Amplifier:
		if longer(e.Duration, old.Duration) {
			if old.stored == nil {
				c := e
				old.stored = &c
			} else {
				mergeEffect(old.stored, e)
			}
		}
		return false
	case longer(e.Duration, old.Duration) || e.Ambient != old.Ambient || e.Hidden != old.Hidden:
		stored := old.stored
		*old = e
		old.stored = stored
		return true
	}
	return false
}

// longer reports whether duration a outlasts b; an endless one outlasts all.
func longer(a, b int32) bool {
	if b == EffectInfinite {
		return false
	}
	return a == EffectInfinite || a > b
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
	n := len(p.effects)
	p.effects = slices.DeleteFunc(p.effects, func(e ActiveEffect) bool { return e.ID == id })
	if len(p.effects) == n {
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

// tickEffects runs each effect's Tick and counts it down one tick. One that runs out
// gives way to the effect stored behind it, if any, else goes. Runs: the player's
// region, once per tick.
func (p *Player) tickEffects() {
	for i := 0; i < len(p.effects); {
		e := &p.effects[i]
		id := e.ID
		left := e.Duration
		if left == EffectInfinite {
			left = -e.age
			e.age++
		}
		if def, ok := p.s.game.effects[id]; ok && def.Tick != nil {
			def.Tick(p, e.Amplifier, left)
		}
		// A Tick may have killed the player and cleared its effects.
		if i >= len(p.effects) || p.effects[i].ID != id {
			continue
		}
		e = &p.effects[i]
		if e.Duration == EffectInfinite {
			i++
			continue
		}
		e.Duration--
		if e.Duration > 0 {
			i++
			continue
		}
		if next := e.stored; next != nil {
			*e = *next
			def, ok := p.s.game.effects[id]
			p.effectChanged(*e, packet.MobEffectModify, def, ok)
			i++
			continue
		}
		p.RemoveEffect(id)
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
	var add, mulBase, maxAdd, maxMulBase [len(playerAttributes)]float32
	var mulTotal, maxMulTotal [len(playerAttributes)]float32
	for i := range mulTotal {
		mulTotal[i], maxMulTotal[i] = 1, 1
	}
	var valued, maxed uint32
	for _, e := range p.effects {
		def, ok := p.s.game.effects[e.ID]
		if !ok {
			continue
		}
		for _, m := range def.Modifiers {
			i, ok := attributeIndex(m.Attribute)
			if !ok {
				continue
			}
			v := m.Amount * float32(e.Amplifier+1)
			a, mb, mt := &add[i], &mulBase[i], &mulTotal[i]
			if m.OnMax {
				a, mb, mt = &maxAdd[i], &maxMulBase[i], &maxMulTotal[i]
				maxed |= 1 << i
			} else {
				valued |= 1 << i
			}
			switch m.Operation {
			case ModifierAdd:
				*a += v
			case ModifierMultiplyBase:
				*mb += v
			case ModifierMultiplyTotal:
				*mt *= 1 + v
			}
		}
	}
	var changed []protocol.Attribute
	health := false
	for i := range playerAttributes {
		bit := uint32(1) << i
		a := &p.attributes[i]
		if maxed&bit != 0 || p.effectMaxed&bit != 0 {
			top := playerAttributes[i].max
			if a.limit != 0 {
				top = a.limit
			}
			bonus := float32(0)
			if maxed&bit != 0 {
				bonus = (top+maxAdd[i])*(1+maxMulBase[i])*maxMulTotal[i] - top
			}
			if bonus != a.maxBonus {
				a.maxBonus = bonus
				p.attributes.set(i, a.current)
				changed = append(changed, p.attributes.entry(i))
				health = health || i == attrHealth
			}
		}
		if valued&bit != 0 || p.effectValued&bit != 0 {
			v := a.base
			if valued&bit != 0 {
				v = (a.base + add[i]) * (1 + mulBase[i]) * mulTotal[i]
			}
			if p.attributes.set(i, v) {
				changed = append(changed, p.attributes.entry(i))
			}
		}
	}
	p.effectValued, p.effectMaxed = valued, maxed
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

// visibleEffectsMax is how many effects the visible effects actor data holds.
const visibleEffectsMax = 8

// visibleEffects is the actor data those who see the player draw its effects' particles
// from: up to eight effects with particles, seven bits each (the ID and the ambient
// bit).
func (p *Player) visibleEffects() int64 {
	var v int64
	n := 0
	for _, e := range p.effects {
		if e.Hidden || n == visibleEffectsMax {
			continue
		}
		bit := int64(0)
		if e.Ambient {
			bit = 1
		}
		v = v<<7 | int64(e.ID&0x3F)<<1 | bit
		n++
	}
	return v
}

// sendVisibleEffects shows those who see the player its effects' particles.
func (p *Player) sendVisibleEffects() {
	if p.entity == nil {
		return
	}
	if r := p.entity.Region(); r != nil {
		r.Broadcast(p.blockPos(), actorDataAt(p.runtimeID, protocol.EntityMetadata{protocol.EntityDataKeyVisibleMobEffects: p.visibleEffects()}, p.entity.Tick()), p.viewer)
	}
}

// Keys of an effect in a saved player's ActiveEffects, as BDS 1.26.60 writes them
// (MobEffectInstance save 0xCAB8B90, load 0xCAB9E10).
const (
	effectsTag         = "ActiveEffects"
	effectTagID        = "Id"
	effectTagAmplifier = "Amplifier"
	effectTagDuration  = "Duration"
	effectTagEasy      = "DurationEasy"
	effectTagNormal    = "DurationNormal"
	effectTagHard      = "DurationHard"
	effectTagAmbient   = "Ambient"
	effectTagParticles = "ShowParticles"
	effectTagAnimation = "DisplayOnScreenTextureAnimation"
	effectTagStored    = "StoredEffects"
)

// saveEffects writes the player's effects as BDS saves them; no effects, no key.
func (p *Player) saveEffects(data map[string]any) {
	if len(p.effects) == 0 {
		delete(data, effectsTag)
		return
	}
	list := make([]any, 0, len(p.effects))
	for i := range p.effects {
		list = append(list, effectNBT(&p.effects[i]))
	}
	data[effectsTag] = list
}

// effectNBT is one effect as BDS saves it. BDS keeps a duration per difficulty; Helix has
// one, written to all three.
func effectNBT(e *ActiveEffect) map[string]any {
	m := map[string]any{
		effectTagID: uint8(e.ID), effectTagAmplifier: uint8(min(max(e.Amplifier, 0), 255)), //nolint:gosec // clamped, BDS saves bytes
		effectTagDuration: e.Duration, effectTagEasy: e.Duration, effectTagNormal: e.Duration, effectTagHard: e.Duration,
		effectTagAmbient: boolByte(e.Ambient), effectTagParticles: boolByte(!e.Hidden), effectTagAnimation: uint8(0),
	}
	if e.stored != nil {
		m[effectTagStored] = []any{effectNBT(e.stored)}
	}
	return m
}

// loadEffects reads the effects BDS saved. They are put back without their Added hooks,
// since what those set (Absorption's extra health) was saved with the attributes.
func (p *Player) loadEffects(data map[string]any) {
	list, ok := data[effectsTag].([]any)
	if !ok {
		return
	}
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if e, ok := effectFromNBT(m); ok {
			p.effects = append(p.effects, e)
		}
	}
	p.applyEffectModifiers()
}

// effectFromNBT reads one saved effect; false without an ID or duration.
func effectFromNBT(m map[string]any) (ActiveEffect, bool) {
	id, ok := m[effectTagID].(uint8)
	dur, okd := m[effectTagDuration].(int32)
	if !ok || !okd || id == 0 {
		return ActiveEffect{}, false
	}
	amp, _ := m[effectTagAmplifier].(uint8)
	ambient, _ := m[effectTagAmbient].(uint8)
	particles, okp := m[effectTagParticles].(uint8)
	e := ActiveEffect{ID: int32(id), Amplifier: int32(amp), Duration: dur, Ambient: ambient != 0, Hidden: okp && particles == 0}
	if stored, ok := m[effectTagStored].([]any); ok && len(stored) > 0 {
		if sm, ok := stored[0].(map[string]any); ok {
			if s, ok := effectFromNBT(sm); ok {
				e.stored = &s
			}
		}
	}
	return e, true
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
