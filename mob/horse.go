package mob

import (
	"encoding/json"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/state"
)

// The horse inventory, as BDS 1.26.60 lays it out for a mob with minecraft:inventory of
// container_type "horse" (horses, donkeys, mules, llamas, camels): the slots of
// minecraft:equippable come first, in its list's order (the horse window's HorseEquip
// container, BDS's code puts the window's chest
// part after them), then the chest. The chest slots exist only while the mob is
// chested: strength times additional_slots_per_strength with minecraft:strength (llamas),
// otherwise inventory_size less the equip slots (a donkey's 15). Changing an equip slot
// (calling) runs the slot's on_equip when the new stack is one of its
// accepted_items and on_unequip otherwise, so a saddle saddles a horse.

// The container actor data: type, base size and slots per strength (InventoryComponent),
// and minecraft:strength's value and maximum.
const (
	dataContainerType   = 44
	dataContainerSize   = 45
	dataContainerStride = 46
	dataStrength        = 75
	dataStrengthMax     = 76
)

// containerTypeHorse is the horse container (ContainerType 12).
const (
	containerTypeHorse     = 12
	containerTypeHorseName = "horse"
	// flagChested is the chested actor flag (minecraft:is_chested), which reads.
	flagChested = 36
)

// readStrength reads minecraft:strength: its value and maximum, shown as actor data 75 and
// 76. Runs: rebuild.
func (m *Mob) readStrength(comps definition.Group) {
	raw, ok := comps["minecraft:strength"]
	m.strength, m.strengthMax = 0, 0
	if !ok {
		return
	}
	var s struct {
		Value int32 `json:"value"`
		Max   int32 `json:"max"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return
	}
	m.strength, m.strengthMax = s.Value, s.Max
	m.setData(dataStrength, s.Value)
	m.setData(dataStrengthMax, s.Max)
}

// HorseInventory reports a horse container: minecraft:inventory with container_type horse.
func (m *Mob) HorseInventory() bool {
	return m.states != nil && m.states.Inventory != nil && m.states.Inventory.ContainerType == containerTypeHorseName
}

// EquipSlots are minecraft:equippable's slots, nil without it.
func (m *Mob) EquipSlots() []state.EquipSlot {
	if m.states == nil || m.states.Equippable == nil {
		return nil
	}
	return m.states.Equippable.Config().Slots
}

// containerSize is the number of slots minecraft:inventory gives: a horse container's
// equip slots and chest, any other its inventory_size.
func (m *Mob) containerSize() int {
	inv := m.states.Inventory
	if !m.HorseInventory() {
		return int(max(inv.Size, 0))
	}
	equip := len(m.EquipSlots())
	return equip + m.chestSlots(equip)
}

// chestSlots is the chest part of a horse container.
func (m *Mob) chestSlots(equip int) int {
	if !m.flags.Has(flagChested) {
		return 0
	}
	inv := m.states.Inventory
	if m.strength > 0 {
		return int(max(inv.AdditionalSlotsPerStrength*m.strength, 0))
	}
	return max(int(inv.Size)-equip, 0)
}

// showContainer writes the container actor data. Runs: rebuild.
func (m *Mob) showContainer() {
	if !m.HorseInventory() {
		return
	}
	inv := m.states.Inventory
	m.setData(dataContainerType, byte(containerTypeHorse))
	m.setData(dataContainerSize, inv.Size)
	m.setData(dataContainerStride, inv.AdditionalSlotsPerStrength)
}

// SetInventorySlot puts a stack in an inventory slot, running the equip slot's events when
// the slot is one. Runs: the mob's region, outside its think.
func (m *Mob) SetInventorySlot(k int, it behavior.Item) {
	if m.items.Inventory == nil || k < 0 || k >= len(m.inv.slots) {
		return
	}
	m.inv.SetSlot(k, it)
	m.equipChanged(k, it)
}

// equipChanged runs an equip slot's on_equip or on_unequip for the stack now in it.
func (m *Mob) equipChanged(k int, it behavior.Item) {
	slots := m.EquipSlots()
	if k >= len(slots) || m.stateEnv.Self == nil {
		return
	}
	h := HeldItem{Name: it.Name}
	on := false
	for _, d := range slots[k].AcceptedItems {
		if !it.Empty() && h.Matches(d) {
			on = true
			break
		}
	}
	m.states.Equippable.Equipped(&m.stateEnv, k, on)
}

// EquipSlotTakes reports that equip slot k takes the item, by its accepted_items.
func (m *Mob) EquipSlotTakes(k int, name string) bool {
	slots := m.EquipSlots()
	if k < 0 || k >= len(slots) {
		return false
	}
	h := HeldItem{Name: name}
	for _, d := range slots[k].AcceptedItems {
		if h.Matches(d) {
			return true
		}
	}
	return false
}
