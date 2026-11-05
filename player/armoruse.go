package player

// ArmorEquipEvent fires before a player puts on the armour it holds by using it. Cancel
// leaves things as they are.
type ArmorEquipEvent struct {
	Cancellable
	Player *Player
	// Item is what goes on, Worn what comes off, empty if nothing.
	Item, Worn ItemStack
}

// OnArmorEquip listens to players putting armour on by using it.
func OnArmorEquip(s *Setup, fn func(e *ArmorEquipEvent), p Priority) error {
	return addListener(s, &s.h.game.armorEquipEv, fn, p)
}

// EquipArmorFunc puts on the armour the player holds; false if held is not armour, or
// may not go on.
type EquipArmorFunc func(p *Player, held ItemStack) bool

// vanillaEquipArmor is BDS's use of a piece of armour: what is worn in its slot swaps
// with what is held. Items locked in their slot do not move, and neither does a worn
// piece with the Curse of Binding outside creative, the rules of moving them by hand.
// Other wearable items, such as heads, are not used this way in BDS.
func vanillaEquipArmor(p *Player, held ItemStack) bool {
	g := p.s.game
	t, ok := p.s.reg.Items.ByName(held.Name)
	if _, armor := g.combat.armor[held.Name]; !ok || !armor || !t.Wearable || int(t.ArmorSlot) >= len(p.inventory.armor) {
		return false
	}
	inv := &p.inventory
	worn := inv.armor[t.ArmorSlot]
	if lock, _ := held.Tag["minecraft:item_lock"].(uint8); lock == itemLockInSlot {
		return false
	}
	if !p.mayRemove(slotRef{kind: containerArmor, stack: &inv.armor[t.ArmorSlot]}, worn) {
		return false
	}
	if g.armorEquipEv.Has() {
		e := ArmorEquipEvent{Player: p, Item: held, Worn: worn}
		g.armorEquipEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	inv.armor[t.ArmorSlot], inv.main[inv.held] = held, worn
	p.sendArmour()
	p.sendHeld()
	p.sendEquipment()
	p.flush()
	return true
}
