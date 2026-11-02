package player

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Health returns the player's health. Runs: the player's region.
func (p *Player) Health() float32 { return p.attributes.get(attrHealth) }

// SetHealth sets the player's health, clamped to 0 to 20, and tells the
// player. Runs: the player's region.
func (p *Player) SetHealth(v float32) { p.setAttributes(attrHealth, v) }

// invulnerable reports whether nothing but the void hurts the player:
// creative and spectator, as BDS's game modes have it.
func (p *Player) invulnerable() bool {
	mode := p.gameMode
	if mode == GameModeDefault {
		mode = p.s.level.GameMode
	}
	return mode == GameModeCreative || mode == GameModeSpectator
}

// vanillaHurt is a first pass of BDS's hurt: the damage comes off the
// player's health, the player and those who see it get the hurt event,
// and the damage costs the damage exhaustion. Armour, enchantments,
// resistance, absorption, the invulnerability ticks after a hit, knockback
// are not built yet. At no health left the Die system runs. Returns whether
// the player was hurt.
func vanillaHurt(p *Player, damage float32, cause DamageCause) bool {
	if damage <= 0 || p.invulnerable() || p.dead() {
		return false
	}
	p.SetHealth(p.Health() - damage)
	if r := p.entity.Region(); r != nil {
		r.Broadcast(p.blockPos(), &packet.ActorEvent{EntityRuntimeID: p.runtimeID, EventType: packet.ActorEventHurt}, nil)
	}
	p.s.game.sys.Exhaust(p, p.s.game.sys.Exhaustion.Damage)
	if p.dead() {
		p.s.game.sys.Die(p, cause)
	}
	return true
}

// setAttributes sets attributes by index and value pairs and sends the
// player the ones that changed in one UpdateAttributes.
func (p *Player) setAttributes(kv ...any) {
	var changed []protocol.Attribute
	for i := 0; i+1 < len(kv); i += 2 {
		idx, ok := kv[i].(int)
		v, okv := kv[i+1].(float32)
		if ok && okv && p.attributes.set(idx, v) {
			changed = append(changed, p.attributes.entry(idx))
		}
	}
	if len(changed) > 0 {
		p.send(&packet.UpdateAttributes{EntityRuntimeID: p.runtimeID, Attributes: changed, Tick: p.entity.Tick()})
	}
}
