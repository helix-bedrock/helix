package player

import (
	"slices"

	"github.com/helix-bedrock/protocol/packet"
)

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
// duration ticks, replacing the same effect. Runs: the player's region.
func (p *Player) AddEffect(id, amplifier, duration int32) {
	op := byte(packet.MobEffectAdd)
	if i := slices.IndexFunc(p.effects, func(e ActiveEffect) bool { return e.ID == id }); i >= 0 {
		p.effects[i].Amplifier, p.effects[i].Duration = amplifier, duration
		op = packet.MobEffectModify
	} else {
		p.effects = append(p.effects, ActiveEffect{ID: id, Amplifier: amplifier, Duration: duration})
	}
	p.send(&packet.MobEffect{
		EntityRuntimeID: p.runtimeID, Operation: op, EffectType: id, Amplifier: amplifier,
		Particles: true, Duration: duration,
	})
	if e, ok := p.s.game.effects[id]; ok && e.Added != nil {
		e.Added(p, amplifier)
	}
	p.flush()
}

// RemoveEffect takes effect id away from the player. Runs: the player's
// region.
func (p *Player) RemoveEffect(id int32) {
	n := len(p.effects)
	p.effects = slices.DeleteFunc(p.effects, func(e ActiveEffect) bool { return e.ID == id })
	if len(p.effects) == n {
		return
	}
	p.send(&packet.MobEffect{EntityRuntimeID: p.runtimeID, Operation: packet.MobEffectRemove, EffectType: id})
	if e, ok := p.s.game.effects[id]; ok && e.Removed != nil {
		e.Removed(p)
	}
	p.flush()
}

// Numbers of BDS 1.26.60's effects.
const (
	// regenerationBase is shifted down by the level for the ticks between heals.
	regenerationBase = 50
	regenerationHeal = 1
	// absorptionPerLevel is the extra health Absorption gives per level.
	absorptionPerLevel = 4
)

// regenerationTick is BDS's Regeneration: a heal when the ticks left divide by its
// interval, 50 halved per level and at least 1.
func regenerationTick(p *Player, amplifier, left int32) {
	every := max(int32(regenerationBase)>>min(amplifier, 31), 1)
	if left%every == 0 && !p.dead() {
		p.SetHealth(p.Health() + regenerationHeal)
	}
}

// absorptionAdded is BDS's Absorption: what the player has of it starts over at the
// effect's amount.
func absorptionAdded(p *Player, amplifier int32) {
	p.setAttributes(attrAbsorption, float32(amplifier+1)*absorptionPerLevel)
}

// absorptionRemoved takes what is left of Absorption away.
func absorptionRemoved(p *Player) { p.setAttributes(attrAbsorption, float32(0)) }

// tickEffects counts the player's effects down one tick and removes those
// that ran out. Runs: the player's region, once per tick.
func (p *Player) tickEffects() {
	for i := 0; i < len(p.effects); {
		if e, ok := p.s.game.effects[p.effects[i].ID]; ok && e.Tick != nil {
			e.Tick(p, p.effects[i].Amplifier, p.effects[i].Duration)
		}
		p.effects[i].Duration--
		if p.effects[i].Duration > 0 {
			i++
			continue
		}
		p.RemoveEffect(p.effects[i].ID)
	}
}
