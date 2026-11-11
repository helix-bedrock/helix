package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
)

// Equipment: what a mob holds and wears, as the goals and filter tests see it. The engine
// owns the real stacks (enchantments, damage) and tells the mob their names with
// SetEquipment; the mob rolls nothing itself. BDS rolls the definition's
// minecraft:equipment table once, when the mob is created and not when it is loaded
// (Actor::initializeComponents 0xB38F1A0, 1.26.60; Actor::equip 0xB3C8A40).

// The equipment slots, BDS's SharedTypes::Legacy::EquipmentSlot order: hands, then armour
// head to feet, then the body slot (horse and wolf armour).
const (
	EquipMainhand = iota
	EquipOffhand
	EquipHead
	EquipChest
	EquipLegs
	EquipFeet
	EquipBody
	EquipSlots
)

// equipSlotNames are the slot names minecraft:equipment's slot_drop_chance uses.
var equipSlotNames = map[string]int{
	"slot.weapon.mainhand": EquipMainhand, "slot.weapon.offhand": EquipOffhand,
	"slot.armor.head": EquipHead, "slot.armor.chest": EquipChest, "slot.armor.legs": EquipLegs,
	"slot.armor.feet": EquipFeet, "slot.armor.body": EquipBody,
}

// EquipSlotByName returns the slot of a slot_drop_chance name.
func EquipSlotByName(name string) (int, bool) {
	s, ok := equipSlotNames[name]
	return s, ok
}

// itemCrossbow is the item that makes ranged_attack charge before shooting; it and
// itemBow are the ranged weapons of has_ranged_weapon.
const (
	itemCrossbow = "minecraft:crossbow"
	itemBow      = "minecraft:bow"
)

// Equipment is the item in a slot; empty for none or a slot out of range.
func (m *Mob) Equipment(slot int) behavior.Item {
	if slot < 0 || slot >= EquipSlots {
		return behavior.Item{}
	}
	return m.equip[slot]
}

// SetEquipment sets the item in a slot. The engine shows it to players. Runs: Apply or the
// engine's tick of the mob.
func (m *Mob) SetEquipment(slot int, it behavior.Item) {
	if slot >= 0 && slot < EquipSlots {
		m.equip[slot] = it
		if slot == EquipMainhand && it.Name != itemCrossbow {
			m.charged = false
		}
	}
}

// Slot is the item in a hand slot, for the item goals.
func (m *Mob) Slot(slot int) behavior.Item { return m.Equipment(slot) }

// Armor is the item in an armour slot, 0 head to 3 feet and 4 body.
func (m *Mob) Armor(slot int) behavior.Item { return m.Equipment(EquipHead + slot) }

// equipmentCount is how many slots hold something: Molang's query.equipment_count.
func (m *Mob) equipmentCount() int32 {
	n := int32(0)
	for _, it := range m.equip {
		if !it.Empty() {
			n++
		}
	}
	return n
}

// equipmentNames appends the names of the items in the slots a filter domain names
// (has_equipment, all_slots_empty).
func equipmentNames(equip *[EquipSlots]behavior.Item, domain string, dst []string) []string {
	add := func(slots ...int) {
		for _, s := range slots {
			if it := equip[s]; !it.Empty() {
				dst = append(dst, it.Name)
			}
		}
	}
	switch domain {
	case "hand", "main_hand":
		add(EquipMainhand)
	case "offhand":
		add(EquipOffhand)
	case "head":
		add(EquipHead)
	case "torso":
		add(EquipChest)
	case "leg":
		add(EquipLegs)
	case "feet":
		add(EquipFeet)
	case "body":
		add(EquipBody)
	case "armor":
		add(EquipHead, EquipChest, EquipLegs, EquipFeet)
	case "inventory":
	default:
		add(EquipMainhand, EquipOffhand, EquipHead, EquipChest, EquipLegs, EquipFeet, EquipBody)
	}
	return dst
}

// EquipmentTable is the definition's minecraft:equipment, for the engine to roll when the
// mob is created. ready is false until the mob has read its components (its first tick).
func (m *Mob) EquipmentTable() (cfg *state.EquipmentConfig, ready bool) {
	if m.states == nil {
		return nil, false
	}
	return m.states.Equipment, true
}

// LootTable is the definition's minecraft:loot table, empty for none.
func (m *Mob) LootTable() string {
	if m.states == nil || m.states.Loot == nil {
		return ""
	}
	return m.states.Loot.Table
}

// DeathExperience is minecraft:experience_reward's on_death, its expressions summed and
// rounded down; 0 without one. Molang that does not compile counts 0.
func (m *Mob) DeathExperience() int32 {
	if m.states == nil || m.states.ExperienceReward == nil {
		return 0
	}
	var sum float32
	for _, src := range m.states.ExperienceReward.OnDeath {
		if e, err := parseMolang(src); err == nil {
			sum += e.eval(m)
		}
	}
	return int32(max(sum, 0))
}

// LastHitByPlayer reports a player hurt the mob in the last 100 ticks (BDS's
// lastHurtByPlayerTime), which death loot and experience ask.
func (m *Mob) LastHitByPlayer() bool { return m.playerHitTime > 0 }

// Rand is the mob's own random, which its loot and equipment rolls draw from.
func (m *Mob) Rand() *Random { return m.v.rnd }

// --- behavior.Ranged ---

// Crossbow reports a crossbow in the main hand and whether it is charged.
func (m *Mob) Crossbow() (held, charged bool) {
	return m.equip[EquipMainhand].Name == itemCrossbow, m.charged
}

// UnchargeCrossbow empties the crossbow.
func (m *Mob) UnchargeCrossbow() { m.charged = false }

// ChargeAmount and SetChargeAmount are the charge actor data. A charge that reaches the
// goal's charged trigger leaves the crossbow charged. Not sent to clients yet.
func (m *Mob) ChargeAmount() int8 { return m.chargeAmount }

func (m *Mob) SetChargeAmount(v int8) {
	if v == 0 && m.chargeAmount > 0 && m.equip[EquipMainhand].Name == itemCrossbow {
		m.charged = true
	}
	m.chargeAmount = v
}

// Shoot is ShooterComponent::onShoot: the projectile its minecraft:shooter picks for the
// target, fired at Apply by the engine. Nothing without the component or a target.
func (m *Mob) Shoot() {
	if m.states == nil || m.states.Shooter == nil || m.target == behavior.NoID {
		return
	}
	t := m.actor(m.target)
	if t == nil {
		return
	}
	cfg := m.states.Shooter
	def, aux, _ := cfg.Pick(&m.stateEnv, t)
	if def == "" {
		return
	}
	m.do(op{kind: opShoot, id: m.target, name: def, n: aux, amount: cfg.Power, other: behavior.ID(cfg.Sound)})
}

// RestartAngry restarts minecraft:angry's timer.
func (m *Mob) RestartAngry() {
	if m.states != nil && m.states.Angry != nil && m.v.rnd != nil {
		m.states.Angry.Restart(m.v.rnd)
	}
}

// RangedAttack is Actor::performRangedAttack, which in BDS only sends a gameplay event: the
// shot itself is Shoot's.
func (m *Mob) RangedAttack(behavior.Actor, float32) {}

var _ behavior.Ranged = (*Mob)(nil)

// Variant and Color are minecraft:variant and minecraft:color's values, 0 without them.
func (m *Mob) Variant() int32 {
	v, _ := intComponent(m.inst.Components(), "minecraft:variant")
	return v
}

func (m *Mob) Color() int32 {
	v, _ := intComponent(m.inst.Components(), "minecraft:color")
	return v
}

// BoolProperty is a bool property's value; ok is false without the property.
func (m *Mob) BoolProperty(name string) (v, ok bool) {
	p, ok := m.inst.Property(name)
	return ok && !p.IsString && p.Number != 0, ok
}
