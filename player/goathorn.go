package player

import (
	"github.com/helix-bedrock/protocol/packet"
)

// Goat horns as BDS 1.26.60 runs them (GoatHornItem::use, 0xC913C30). The research note is
// private.
const (
	nameGoatHorn = "minecraft:goat_horn"
	// goatHornCategory and goatHornCooldown are its cooldown, in ticks.
	goatHornCategory = "goat_horn"
	goatHornCooldown = 140
)

// goatHornSounds are the horn calls by the horn's aux value; BDS clamps the value into them.
var goatHornSounds = [...]string{
	packet.SoundEventGoatCall0, packet.SoundEventGoatCall1, packet.SoundEventGoatCall2, packet.SoundEventGoatCall3,
	packet.SoundEventGoatCall4, packet.SoundEventGoatCall5, packet.SoundEventGoatCall6, packet.SoundEventGoatCall7,
}

// Cooldown returns the ticks left of the cooldown of category, 0 if none. Runs: owner of the
// player's region.
func (p *Player) Cooldown(category string) int64 {
	r := p.entity.Region()
	end, ok := p.cooldowns[category]
	if r == nil || !ok {
		return 0
	}
	return max(end-r.CurrentTick(), 0)
}

// StartCooldown starts a cooldown of category for ticks and tells the client, as BDS's
// startItemCooldown with its PlayerStartItemCooldownPacket. Runs: owner of the player's region.
func (p *Player) StartCooldown(category string, ticks int32) {
	r := p.entity.Region()
	if r == nil {
		return
	}
	if p.cooldowns == nil {
		p.cooldowns = map[string]int64{}
	}
	p.cooldowns[category] = r.CurrentTick() + int64(ticks)
	p.send(&packet.ClientStartItemCooldown{Category: category, Duration: ticks})
	p.flush()
}

// useGoatHorn plays the horn's call where the player is, to everyone near, and starts its
// cooldown. Refused while the cooldown runs.
func (p *Player) useGoatHorn(held ItemStack) bool {
	r := p.entity.Region()
	if r == nil || p.Cooldown(goatHornCategory) > 0 {
		return false
	}
	call := min(max(int(held.Meta), 0), len(goatHornSounds)-1)
	pos := p.entity.Position()
	playSound(r, p.blockPos(), soundEvent{name: goatHornSounds[call], pos: pos, extra: -1}, nil)
	p.StartCooldown(goatHornCategory, goatHornCooldown)
	return true
}
