package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/world"
)

// Mob items: the item components (minecraft:shareables, minecraft:inventory,
// minecraft:equip_item) as entity's behavior.Items runs them, for pickup_items and
// equip_item. The engine owns the real stacks: each behavior.Item carries its stack in
// Data, so what a goal moves comes back to the engine as the same item.
//
// A mob thinks on any core, so it only reads item entities while it thinks; taking one
// (PickUp) waits for its Apply, where the item entity is read again. BDS takes it inside
// the goal's tick, so in Helix the rest of that tick's goals do not see it yet.

// ItemEnv is the part of the Env the item goals need. An Env without it gives mobs no item
// entities to see.
type ItemEnv interface {
	// ItemOf is the stack an item entity holds; ok is false for other entities.
	ItemOf(a behavior.Actor) (behavior.Item, bool)
	// PickupDelay is an item entity's pick-up delay in ticks.
	PickupDelay(a behavior.Actor) int32
	// ItemOwner is the entity that dropped or threw an item entity, NoID for none.
	ItemOwner(a behavior.Actor) behavior.ID
	// Items appends the item entities whose boxes touch b.
	Items(r *world.Region, dst []behavior.Actor, b behavior.Box) []behavior.Actor
	// SpecialMultiplier is BDS's Level::getSpecialMultiplier for r's dimension.
	SpecialMultiplier(r *world.Region) float32
}

// ItemHooks are the Hooks of mobs that hold, take and drop items. They run in the mob's
// Apply.
type ItemHooks interface {
	// SetSlot puts the stack (its real item in Data, or a new one) in an equipment slot,
	// EquipMainhand to EquipBody, and shows it to those who see the mob. A goal put it
	// there, so it always drops on death.
	SetSlot(m *Mob, slot int, it behavior.Item)
	// DropItem spawns the stack as an item entity at the mob.
	DropItem(m *Mob, it behavior.Item)
	// SetItemCount sets the count of the item entity's stack, removing it at 0 or less.
	SetItemCount(m *Mob, item behavior.ID, n int32)
	// TakeItem shows the mob taking the item entity (TakeItemActor).
	TakeItem(m *Mob, item behavior.ID)
}

// pickUpChance is what Actor::reload multiplies the special multiplier by for the spawn
// roll of canPickupItems (0xB38E796, 1.26.60).
const pickUpChance = 0.55

// The pick-up flags packed into an op.
const (
	pickSameItems = 1 << iota
	pickToHand
	pickAnyItem
	pickStopIfHolding
)

// buildItems puts the item components together after the states are read. The inventory
// keeps its stacks while its size stays the same.
func (m *Mob) buildItems() {
	s := m.states
	m.hasItems = s != nil && m.rt.ItemRules != nil
	if !m.hasItems {
		m.items = behavior.Items{}
		return
	}
	m.items = behavior.Items{Body: m, World: goalWorld{&m.v}, Rules: m.rt.ItemRules, Shareables: s.Shareables}
	if s.Inventory != nil {
		m.inv.resize(m.containerSize(), m.rt.ItemRules)
		m.items.Inventory = &m.inv
		m.showContainer()
	}
	if s.EquipItem != nil {
		m.items.Equip = &m.equipState
	}
}

// rollPickUp is Actor::reload's spawn roll (0xB38E7B3, 1.26.60): once in the mob's life,
// with the level's thread random, saved as canPickupItems.
func (m *Mob) rollPickUp() {
	if m.canPickUpSet || m.v.r == nil {
		return
	}
	e, ok := m.rt.Env.(ItemEnv)
	if !ok {
		return
	}
	m.canPickUp = e.SpecialMultiplier(m.v.r)*pickUpChance > m.rnd.NextFloat()
	m.canPickUpSet = true
}

// CanPickUpLoot is the spawn roll pickup_based_on_chance reads.
func (m *Mob) CanPickUpLoot() bool { return m.canPickUp }

// PickUpLoot is the saved canPickupItems and hasSetCanPickupItems.
func (m *Mob) PickUpLoot() (can, set bool) { return m.canPickUp, m.canPickUpSet }

// SetPickUpLoot restores them from a save.
func (m *Mob) SetPickUpLoot(can, set bool) { m.canPickUp, m.canPickUpSet = can, set }

// --- behavior.ItemHolder ---

// SetSlot puts the stack in a hand slot. The mob holds it at once; the engine is told at
// Apply.
func (m *Mob) SetSlot(slot int, it behavior.Item) {
	if slot < 0 || slot > EquipOffhand {
		return
	}
	m.setEquip(slot, it)
}

// SetArmor puts the stack in an armour slot, 0 head to 3 feet and 4 body.
func (m *Mob) SetArmor(slot int, it behavior.Item) {
	if slot < 0 || EquipHead+slot >= EquipSlots {
		return
	}
	m.setEquip(EquipHead+slot, it)
}

func (m *Mob) setEquip(slot int, it behavior.Item) {
	if it.Empty() {
		it = behavior.Item{}
	}
	m.equip[slot] = it
	if slot == EquipMainhand && it.Name != itemCrossbow {
		m.charged = false
	}
	m.do(op{kind: opSetSlot, n: int32(slot), item: it})
}

// --- behavior.Hurter ---

// RecentlyHurt reports that an attacker hurt the mob less than ticks ago. BDS counts the
// mob's own ticks; Helix the world's, which differ only while the mob's chunk did not tick.
func (m *Mob) RecentlyHurt(ticks int32) bool {
	if m.lastHurtTick == 0 || m.v.r == nil {
		return false
	}
	now := uint64(max(m.v.r.CurrentTick(), 0)) //nolint:gosec // clamped
	return now-m.lastHurtTick < uint64(max(ticks, 0))
}

// --- behavior.Picker and behavior.Equipper ---

// HasShareables reports minecraft:shareables.
func (m *Mob) HasShareables() bool { return m.hasItems && m.items.HasShareables() }

// CanStoreHand reports an inventory with room for the item in the hand.
func (m *Mob) CanStoreHand() bool { return m.hasItems && m.items.CanStoreHand() }

// Admiring is minecraft:admire_item's admiring flag; Helix has no admire_item yet.
func (m *Mob) Admiring() bool { return m.hasItems && m.items.Admiring() }

// PickupWanted is pickup_items' test of one item entity.
func (m *Mob) PickupWanted(a behavior.Actor, f behavior.PickupFlags) bool {
	return m.hasItems && m.items.PickupWanted(a, f)
}

// PickUp takes the item entity at Apply.
func (m *Mob) PickUp(a behavior.Actor, f behavior.PickupFlags) {
	if !m.hasItems || a == nil {
		return
	}
	var n int32
	for bit, on := range [...]bool{f.SameItems, f.ToHand, f.AnyItem, f.StopIfHolding} {
		if on {
			n |= 1 << bit
		}
	}
	m.do(op{kind: opPickUp, id: a.ID(), n: n})
}

// pickUp runs a queued PickUp with the item entity as it is now.
func (m *Mob) pickUp(r *world.Region, id behavior.ID, n int32) {
	a := m.rt.Env.Actor(r, id)
	if a == nil || !m.hasItems {
		return
	}
	m.items.PickUp(a, behavior.PickupFlags{
		SameItems: n&pickSameItems != 0, ToHand: n&pickToHand != 0,
		AnyItem: n&pickAnyItem != 0, StopIfHolding: n&pickStopIfHolding != 0,
	})
}

// EquipPending reports a stack waiting for equip_item.
func (m *Mob) EquipPending() bool { return m.hasItems && m.items.EquipPending() }

// PendingEquip is the stack waiting for equip_item.
func (m *Mob) PendingEquip() (behavior.Item, bool) {
	if !m.hasItems {
		return behavior.Item{}, false
	}
	return m.items.PendingEquip()
}

// ClearPendingEquip drops the waiting stack.
func (m *Mob) ClearPendingEquip() {
	if m.hasItems {
		m.items.ClearPendingEquip()
	}
}

// ItemMatches reports the stack matching an item descriptor.
func (m *Mob) ItemMatches(descriptor string, it behavior.Item) bool {
	return m.rt.ItemRules != nil && m.rt.ItemRules.Matches(descriptor, it)
}

// Wearable is the armour slot an item goes in.
func (m *Mob) Wearable(it behavior.Item) (int, bool) {
	if m.rt.ItemRules == nil {
		return 0, false
	}
	return m.rt.ItemRules.Wearable(it)
}

// WearAllowed is minecraft:equip_item's rule from base game version 1.20.30 on: not one of
// its excluded_items, and can_wear_armor.
func (m *Mob) WearAllowed(it behavior.Item) bool {
	r := m.rt.ItemRules
	if r == nil || m.states == nil || m.states.EquipItem == nil || !r.BaseGameAtLeast(1, 20, 30) {
		return true
	}
	e := m.states.EquipItem
	for _, d := range e.ExcludedItems {
		if r.Matches(d, it) {
			return false
		}
	}
	return e.CanWearArmor
}

// Stacks reports two stacks that stack together.
func (m *Mob) Stacks(a, b behavior.Item) bool {
	return m.rt.ItemRules != nil && m.rt.ItemRules.Stacks(a, b)
}

// MaxStack is the item's maximum stack size.
func (m *Mob) MaxStack(it behavior.Item) int32 {
	if m.rt.ItemRules == nil {
		return 1
	}
	return m.rt.ItemRules.MaxStack(it)
}

// EntitiesOfType appends the entities of the BDS actor type whose boxes touch b. Item
// entities (64) and players (319) are told apart; any other type gives the players and
// mobs.
func (m *Mob) EntitiesOfType(dst []behavior.Actor, actorType int, self behavior.ID, b behavior.Box) []behavior.Actor {
	r := m.v.r
	if r == nil {
		return dst
	}
	switch actorType {
	case actorTypeItem:
		if e, ok := m.rt.Env.(ItemEnv); ok {
			return e.Items(r, dst, b)
		}
		return dst
	case actorTypePlayer:
		start := len(dst)
		dst = m.rt.Env.Entities(r, dst, self, b)
		keep := dst[:start]
		for _, a := range dst[start:] {
			if a.IsPlayer() {
				keep = append(keep, a)
			}
		}
		clear(dst[len(keep):])
		return keep
	}
	return m.rt.Env.Entities(r, dst, self, b)
}

// The BDS actor types EntitiesOfType tells apart.
const (
	actorTypeItem   = 64
	actorTypePlayer = 319
)

// --- behavior.ItemBody ---

// CanPickUpItem is Mob's own pick-up test (Actor::canPickupItem 0xB3C5000, true). Zombie's
// override (no pick-up of one item while riding a chicken, 0xBD1B9F0) is not ported.
func (m *Mob) CanPickUpItem(behavior.Item) bool { return true }

// DropItem spawns the stack at the mob, at Apply while it thinks.
func (m *Mob) DropItem(it behavior.Item) {
	if !it.Empty() {
		m.do(op{kind: opDropItem, item: it})
	}
}

// ItemOf is the stack an item entity holds.
func (m *Mob) ItemOf(a behavior.Actor) (behavior.Item, bool) {
	if e, ok := m.rt.Env.(ItemEnv); ok && a != nil {
		return e.ItemOf(a)
	}
	return behavior.Item{}, false
}

// SetItemCount sets an item entity's count. Only PickUp calls it, which runs at Apply.
func (m *Mob) SetItemCount(a behavior.Actor, n int32) {
	if h, ok := m.hooks.(ItemHooks); ok && a != nil {
		h.SetItemCount(m, a.ID(), n)
	}
}

// PickupDelay is an item entity's pick-up delay.
func (m *Mob) PickupDelay(a behavior.Actor) int32 {
	if e, ok := m.rt.Env.(ItemEnv); ok && a != nil {
		return e.PickupDelay(a)
	}
	return 0
}

// ItemOwner is who dropped an item entity.
func (m *Mob) ItemOwner(a behavior.Actor) behavior.ID {
	if e, ok := m.rt.Env.(ItemEnv); ok && a != nil {
		return e.ItemOwner(a)
	}
	return behavior.NoID
}

// SendTakeItem shows the mob taking the item entity. Only PickUp calls it, at Apply.
func (m *Mob) SendTakeItem(a behavior.Actor) {
	if h, ok := m.hooks.(ItemHooks); ok && a != nil {
		h.TakeItem(m, a.ID())
	}
}

// ItemsOf is another mob's item components, which share_items reads; not given yet.
func (m *Mob) ItemsOf(behavior.Actor) *behavior.Items { return nil }

// Inventory is the mob's minecraft:inventory slots, nil without the component.
func (m *Mob) Inventory() []behavior.Item {
	if m.items.Inventory == nil {
		return nil
	}
	return m.inv.slots
}

// SetInventory restores the inventory from a save; slots past its size are dropped.
func (m *Mob) SetInventory(slots []behavior.Item) {
	m.inv.saved = append(m.inv.saved[:0], slots...)
	m.inv.restore()
}

// runItemOp runs an item op at Apply.
func (m *Mob) runItemOp(r *world.Region, o *op) {
	switch o.kind {
	case opSetSlot:
		if h, ok := m.hooks.(ItemHooks); ok {
			h.SetSlot(m, int(o.n), o.item)
		}
	case opDropItem:
		if h, ok := m.hooks.(ItemHooks); ok {
			h.DropItem(m, o.item)
		}
	case opPickUp:
		m.pickUp(r, o.id, o.n)
	}
}

var (
	_ behavior.Picker    = (*Mob)(nil)
	_ behavior.Equipper  = (*Mob)(nil)
	_ behavior.ItemBody  = (*Mob)(nil)
	_ behavior.Container = (*inventory)(nil)
)

// inventory is a mob's minecraft:inventory: plain slots with BDS's container rules.
type inventory struct {
	slots []behavior.Item
	rules behavior.ItemRules
	// saved holds a loaded inventory until the components give its size.
	saved []behavior.Item
}

// resize gives the inventory n slots, keeping what fits.
func (c *inventory) resize(n int, rules behavior.ItemRules) {
	c.rules = rules
	if len(c.slots) != n {
		s := make([]behavior.Item, n)
		copy(s, c.slots)
		c.slots = s
	}
	c.restore()
}

// restore puts a loaded inventory back once the slots exist.
func (c *inventory) restore() {
	if len(c.saved) == 0 || len(c.slots) == 0 {
		return
	}
	copy(c.slots, c.saved)
	c.saved = c.saved[:0]
}

// Size is the number of slots.
func (c *inventory) Size() int { return len(c.slots) }

// Slot is the stack in slot k.
func (c *inventory) Slot(k int) behavior.Item {
	if k < 0 || k >= len(c.slots) {
		return behavior.Item{}
	}
	return c.slots[k]
}

// SetSlot replaces the stack in slot k.
func (c *inventory) SetSlot(k int, it behavior.Item) {
	if k >= 0 && k < len(c.slots) {
		if it.Empty() {
			it = behavior.Item{}
		}
		c.slots[k] = it
	}
}

// Count is how many of the item's kind the slots hold.
func (c *inventory) Count(it behavior.Item) int32 {
	var n int32
	for _, s := range c.slots {
		if !s.Empty() && c.rules.SameKind(s, it) {
			n += s.Count
		}
	}
	return n
}

// CanAdd reports an empty slot, or one the stack stacks onto that is not full (0xB4A5610).
func (c *inventory) CanAdd(it behavior.Item) bool {
	for _, s := range c.slots {
		if s.Empty() || c.rules.Stacks(s, it) && s.Count < c.rules.MaxStack(s) {
			return true
		}
	}
	return false
}

// Add fills the stacks it stacks onto in slot order, then the first empty slot takes the
// rest (0xB4A5790).
func (c *inventory) Add(it behavior.Item) (behavior.Item, bool) {
	fit := false
	for k := range c.slots {
		if it.Count <= 0 {
			break
		}
		s := &c.slots[k]
		if s.Empty() || !c.rules.Stacks(*s, it) {
			continue
		}
		n := min(c.rules.MaxStack(*s)-s.Count, it.Count)
		if n > 0 {
			s.Count += n
			it.Count -= n
			fit = true
		}
	}
	if it.Count > 0 {
		for k := range c.slots {
			if c.slots[k].Empty() {
				c.slots[k] = it
				it.Count = 0
				fit = true
				break
			}
		}
	}
	if it.Count <= 0 {
		it = behavior.Item{}
	}
	return it, fit
}

// Take removes n of the kind from the slots in order (0x7066EF0).
func (c *inventory) Take(kind behavior.Item, n int32) {
	for k := range c.slots {
		if n <= 0 {
			return
		}
		s := &c.slots[k]
		if s.Empty() || !c.rules.SameKind(*s, kind) {
			continue
		}
		t := min(s.Count, n)
		s.Count -= t
		n -= t
		if s.Count <= 0 {
			*s = behavior.Item{}
		}
	}
}
