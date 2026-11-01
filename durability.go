package helix

import (
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/protocol"
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
	if n <= 0 {
		return
	}
	g := p.s.game
	inv := &p.inventory
	s := &inv.main[inv.held]
	d, ok := g.durability[s.Name]
	if s.Empty() || !ok || d.MaxDamage <= 0 {
		return
	}
	if u, _ := s.Tag["Unbreakable"].(uint8); u != 0 {
		return
	}
	skip := false
	enchantLevels(*s, func(id, lvl int16) {
		if e, ok := g.enchants[id]; ok && e.DamageChance != nil && lvl > 0 && p.rng.Int32N(100) >= e.DamageChance(lvl) {
			skip = true
		}
	})
	if skip {
		return
	}
	damage := itemDamage(*s) + n
	if s.Tag == nil {
		s.Tag = map[string]any{}
	}
	s.Tag["Damage"] = damage
	if damage > int32(d.MaxDamage) { //nolint:gosec // max damage is small
		r := p.entity.Region()
		if r != nil {
			pos := p.entity.Position()
			r.Broadcast(p.blockPos(), &packet.LevelSoundEvent{SoundType: packet.SoundEventBreak, Position: pos, ExtraData: -1}, nil)
		}
		s.Count--
		if s.Count == 0 {
			*s = ItemStack{}
		} else {
			delete(s.Tag, "Damage")
		}
	}
	_ = p.conn.WritePacket(&packet.InventorySlot{WindowID: protocol.WindowIDInventory, Slot: uint32(inv.held), NewItem: s.network(p.s.reg)})
	p.flush()
}

// durabilityTable indexes helix-data's durability by item name.
func durabilityTable(b *data.BreakPlace) map[string]data.Durability { return b.Items }
