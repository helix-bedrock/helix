package player

import "github.com/helix-bedrock/helix/mob"

// mobArmourSlots are the slots a mob wears armour in: head to feet, then the body slot
// (horse and wolf armour).
var mobArmourSlots = [...]int{mob.EquipHead, mob.EquipChest, mob.EquipLegs, mob.EquipFeet, mob.EquipBody}

// AfterArmor is what a mob's armour leaves of damage v of cause: BDS 1.26.60's Mob hurt
// takes the same steps for every mob as for players: a helmet against falling blocks, the
// armour share (Mob::getDamageAfterArmorReduction, 0xBFE8020) from the worn pieces'
// protection and toughness, then the protection enchantments (0xC4A2AA0). Mob armour does
// not wear down yet.
func (m *Mob) AfterArmor(_ *mob.Mob, v float32, cause int) float32 {
	c := DamageCause(cause)
	if !armorBypassed(c) {
		v = m.armourShare(v, c)
	}
	if c != causeEnchantsSkip {
		v = m.protectionShare(v, c)
	}
	return v
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
