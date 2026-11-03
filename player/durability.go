package player

import (
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/protocol/packet"
)

// EnchantUnbreaking is BDS's Unbreaking enchantment ID.
const EnchantUnbreaking int16 = 17

// Durability rules BDS 1.26.60 wears held items by when they break a block
// (helix-data bds/break_place.json).
const (
	wearWeapon = "weapon"
	wearDigger = "digger"
	wearShears = "shears"
)

// WearFunc returns how much damage the held item takes for breaking b.
type WearFunc func(b *BreakInfo) int32

// unbreakingChance is BDS's default damage chance: 100 / (level+1)
// percent.
func unbreakingChance(level int16) int32 { return 100 / (int32(level) + 1) }

// wear is BDS's rule for the held item breaking a block: weapons take 2
// and diggers 1 on a block with a destroy time above 0, shears 1 on any
// block.
func (g *gameplay) wear(b *BreakInfo) int32 {
	d, ok := g.durability[b.Item.Name]
	if !ok {
		return 0
	}
	switch d.Rule {
	case wearWeapon:
		if b.DestroyTime > 0 {
			return 2
		}
	case wearDigger:
		if b.DestroyTime > 0 {
			return 1
		}
	case wearShears:
		return 1
	}
	return 0
}

// itemDamage is the damage saved in an item's tag (BDS's int "Damage").
func itemDamage(s ItemStack) int32 {
	d, _ := s.Tag["Damage"].(int32)
	return d
}

// hurtHeld damages the held item by n as BDS's hurtAndBreak does: nothing
// for an unbreakable item, each enchantment's DamageChance may skip it, and
// past its max damage the item breaks with the break sound.
func (p *Player) hurtHeld(n int32) {
	inv := &p.inventory
	if p.hurtStack(&inv.main[inv.held], n) {
		p.sendHeld()
		p.flush()
	}
}

// hurtStack damages a stack by n; true if it changed, for the caller to tell the
// client.
func (p *Player) hurtStack(s *ItemStack, n int32) bool {
	changed, broke := p.wearStack(s, n)
	if broke {
		if r := p.entity.Region(); r != nil {
			r.Broadcast(p.blockPos(), &packet.LevelSoundEvent{SoundType: packet.SoundEventBreak, Position: p.entity.Position(), ExtraData: -1}, nil)
		}
	}
	return changed
}

// wearStack damages a stack by n, without a sound; it reports whether the stack
// changed, for the caller to tell the client, and whether an item broke.
func (p *Player) wearStack(s *ItemStack, n int32) (changed, broke bool) {
	if n <= 0 {
		return false, false
	}
	g := p.s.game
	d, ok := g.durability[s.Name]
	if s.Empty() || !ok || d.MaxDamage <= 0 {
		return false, false
	}
	if u, _ := s.Tag["Unbreakable"].(uint8); u != 0 {
		return false, false
	}
	skip := false
	enchantLevels(*s, func(id, lvl int16) {
		if e, ok := g.enchant(id); ok && e.DamageChance != nil && lvl > 0 && p.rng.Int32N(100) >= e.DamageChance(lvl) {
			skip = true
		}
	})
	if skip {
		return false, false
	}
	damage := itemDamage(*s) + n
	if s.Tag == nil {
		s.Tag = map[string]any{}
	}
	s.Tag["Damage"] = damage
	if damage > int32(d.MaxDamage) { //nolint:gosec // max damage is small
		broke = true
		s.Count--
		if s.Count == 0 {
			*s = ItemStack{}
		} else {
			delete(s.Tag, "Damage")
		}
	}
	return true, broke
}

// durabilityTable indexes helix-data's durability by item name.
func durabilityTable(b *data.BreakPlace) map[string]data.Durability { return b.Items }
