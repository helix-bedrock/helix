package mob

import "github.com/helix-bedrock/entity/behavior"

// Small hooks single goals need.

// Jump implements behavior.Celebrator: the jump control jumps this tick.
func (m *Mob) Jump() { m.jump.Jumping = true }

// Remove implements behavior.Silverfish: the mob leaves the level without dying (Actor::remove),
// so it drops nothing; its entity goes when its tick is applied.
func (m *Mob) Remove() { m.despawned = true }

// Carried implements behavior.Begger: the item a player holds in its main hand. Only players
// are asked; any other actor carries nothing.
func (m *Mob) Carried(a behavior.Actor) (string, bool) {
	p, ok := a.(PlayerInfo)
	if !ok {
		return "", false
	}
	var buf [1]string
	held := p.Equipment("main_hand", buf[:0])
	if len(held) == 0 || held[0] == "" {
		return "", false
	}
	return held[0], true
}
