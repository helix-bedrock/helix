package player

import (
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/protocol/packet"
)

// mobArmourSlots are the slots a mob wears armour in: head to feet, then the body slot
// (horse and wolf armour).
var mobArmourSlots = [...]int{mob.EquipHead, mob.EquipChest, mob.EquipLegs, mob.EquipFeet, mob.EquipBody}

// AfterArmor is what a mob's armour leaves of damage v of cause: BDS 1.26.60's Mob hurt
// takes the same steps for every mob as for players: a helmet against falling blocks, the
// armour share (Mob::getDamageAfterArmorReduction, 0xBFE8020) from the worn pieces'
// protection and toughness, then the protection enchantments (0xC4A2AA0). The worn pieces
// wear by the damage before armour, as the health change's Mob::hurtArmor (0xBFE91C0) does.
// Resistance and the absorption attribute come last, as the health delegate (0xBC45336)
// takes them.
func (m *Mob) AfterArmor(_ *mob.Mob, v float32, cause int) float32 {
	c := DamageCause(cause)
	if !armorBypassed(c) {
		raw := v
		v = m.armourShare(v, c)
		m.wearArmour(raw, c)
	}
	if c != causeEnchantsSkip {
		v = m.protectionShare(v, c)
	}
	return m.absorb(m.effectResist(v, c), c)
}

// armourShare takes the helmet and the worn armour points off v.
func (m *Mob) armourShare(v float32, c DamageCause) float32 {
	a := &m.items.stacks
	if !a[mob.EquipHead].Empty() && c >= 0 && c < causeFallingLimit && causesFallingHit>>c&1 != 0 {
		v *= helmetFalling
	}
	var armor, tough float32
	for _, slot := range mobArmourSlots {
		if s := a[slot]; !s.Empty() {
			if p, ok := m.s.game.combat.armor[s.Name]; ok {
				armor += float32(p.Protection)
				tough += float32(p.Toughness)
			}
		}
	}
	if armor <= 0 {
		return v
	}
	x := v/(tough*toughnessShare+toughnessBase) + armor
	share := float32(armorMaxShare)
	if x <= armorFullPoints {
		share = max(x, armor*armorMinShare) / armorPerPoint
	}
	return v * (1 - share)
}

// protectionShare takes the worn protection enchantments off v.
func (m *Mob) protectionShare(v float32, c DamageCause) float32 {
	var points float32
	for _, slot := range mobArmourSlots {
		s := m.items.stacks[slot]
		if s.Empty() {
			continue
		}
		enchantLevels(s, func(id, level int16) {
			if e, ok := m.s.game.enchant(id); ok && e.Protection != nil && points < maxProtectionCount {
				points += float32(e.Protection(level, c))
			}
		})
	}
	counted := max(min(points, protectionLimit), 0)
	return v * (1 - counted*protectionPoint)
}

// wearArmour is Mob::hurtArmorSlots (0xBFE9500) over every armour slot: each worn piece
// takes a quarter of the damage, at least one, by the item's own hurtAndBreak (Unbreaking,
// Unbreakable); fire spares fire resistant pieces. A broken piece plays its break sound at
// the mob and those who see the mob are sent its armour again; wear alone sends nothing,
// as BDS's Mob sends nothing for it (only players are told their armour's damage).
func (m *Mob) wearArmour(damage float32, c DamageCause) {
	g := m.s.game
	if g == nil {
		return
	}
	n := max(int32(damage)/armorWearShare, armorWearMin)
	fire := c >= 0 && c < causeFireLimit && causesFire>>c&1 != 0
	broke := false
	for _, slot := range mobArmourSlots {
		s := &m.items.stacks[slot]
		if s.Empty() || fire && g.fireResistant[s.Name] {
			continue
		}
		if _, b := g.wearStack(s, n, lootRandom{}); b {
			broke = true
		}
	}
	if !broke || m.entity == nil {
		return
	}
	if r := m.entity.Region(); r != nil {
		at := m.entity.Position()
		playSound(r, feetBlock(at), soundEvent{name: packet.SoundEventBreak, pos: at, extra: -1, entity: m.m.Type(), actor: m.entity.UniqueID()}, nil)
	}
	m.entity.Send(m.armourPacket())
}
