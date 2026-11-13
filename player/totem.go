package player

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// Numbers of BDS 1.26.60's totem.
const (
	itemTotem = "minecraft:totem_of_undying"
	// totemHealth is the health a totem leaves.
	totemHealth = 1
)

// totemEffects are the effects a totem gives, in BDS's order.
func totemEffects() []ActiveEffect {
	return []ActiveEffect{
		{ID: EffectRegeneration, Amplifier: 1, Duration: 900},
		{ID: EffectAbsorption, Amplifier: 1, Duration: 100},
		{ID: EffectFireResistance, Amplifier: 0, Duration: 800},
	}
}

// PlayerTotemEvent fires when a totem is about to save a player. Health and Effects
// may be changed; Cancel lets the player die and keeps the totem.
type PlayerTotemEvent struct {
	Cancellable
	Player *Player
	Source DamageSource
	// Health is what the player is left with.
	Health float32
	// Effects replace the ones it had.
	Effects []ActiveEffect
}

// OnPlayerTotem listens to totems saving players.
func OnPlayerTotem(s *Setup, fn func(e *PlayerTotemEvent), p Priority) error {
	return addListener(s, &s.h.game.totemEv, fn, p)
}

// TotemFunc saves a player a hit would kill; false if it dies. The player's health is
// still what it was before the hit.
type TotemFunc func(p *Player, src DamageSource) bool

// heldTotem is the totem that would save the player: the offhand first, then the held
// item, as BDS looks for it.
func (p *Player) heldTotem() *ItemStack {
	inv := &p.inventory
	if inv.offhand[0].Name == itemTotem {
		return &inv.offhand[0]
	}
	if s := &inv.main[inv.held]; s.Name == itemTotem {
		return s
	}
	return nil
}

// vanillaTotem is BDS's totem: not against the void or self destruction. The player
// is left with its totem health, loses its effects and gets the totem's, those who
// see it get the event and the sound, and one totem is gone.
func vanillaTotem(p *Player, src DamageSource) bool {
	if src.Cause == DamageVoid || src.Cause == DamageSelfDestruct {
		return false
	}
	totem := p.heldTotem()
	if totem == nil {
		return false
	}
	health, effects := p.Tuning(TuneTotemHealth), totemEffects()
	if g := p.s.game; g.totemEv.Has() {
		e := PlayerTotemEvent{Player: p, Source: src, Health: health, Effects: effects}
		g.totemEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
		health, effects = e.Health, e.Effects
	}
	p.SetHealth(health)
	p.ClearEffects()
	for _, e := range effects {
		p.AddEffect(e.ID, e.Amplifier, e.Duration)
	}
	if r := p.entity.Region(); r != nil {
		at, pos := p.blockPos(), p.entity.Position()
		r.Broadcast(at, actorEvent(p.runtimeID, packet.ActorEventTalismanActivate), nil)
		r.Broadcast(at, &packet.LevelEvent{EventType: packet.LevelEventSoundTotemUsed, Position: world.Vec32(pos)}, nil)
	}
	p.consumeTotem(totem)
	return true
}

// consumeTotem takes the totem that saved the player out of its slot. BDS empties the
// slot whatever the count; totems do not stack.
func (p *Player) consumeTotem(totem *ItemStack) {
	offhand := totem == &p.inventory.offhand[0]
	*totem = ItemStack{}
	if offhand {
		p.sendOffhand()
	} else {
		p.sendHeld()
	}
	p.sendEquipment()
}
