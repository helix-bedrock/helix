package helix

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
// duration ticks shown on the client, replacing the same effect. Effects do
// not run out yet. Runs: the player's region.
func (p *Player) AddEffect(id, amplifier, duration int32) {
	op := byte(packet.MobEffectAdd)
	if i := slices.IndexFunc(p.effects, func(e ActiveEffect) bool { return e.ID == id }); i >= 0 {
		p.effects[i].Amplifier = amplifier
		op = packet.MobEffectModify
	} else {
		p.effects = append(p.effects, ActiveEffect{ID: id, Amplifier: amplifier})
	}
	_ = p.conn.WritePacket(&packet.MobEffect{
		EntityRuntimeID: p.runtimeID, Operation: op, EffectType: id, Amplifier: amplifier,
		Particles: true, Duration: duration,
	})
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
	_ = p.conn.WritePacket(&packet.MobEffect{EntityRuntimeID: p.runtimeID, Operation: packet.MobEffectRemove, EffectType: id})
	p.flush()
}

// SetBreakSpeed sets the multiplier of the player's break speed, 1 by
// default. Runs: the player's region.
func (p *Player) SetBreakSpeed(m float32) { p.breakSpeed = m }

// SetReachScale sets the multiplier of the player's reach for breaking and
// placing (6.7 blocks, 12 in creative), 1 by default. Runs: the player's
// region.
func (p *Player) SetReachScale(m float32) { p.reachScale = m }
