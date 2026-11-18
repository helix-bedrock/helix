package player

import (
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// Dispensers putting armour on, as BDS 1.26.60 does it.
//
// Humanoid armour (BDS's code, through the equip helper)
// goes on the first mob in the block in front whose armour slot for it is
// empty and that may wear it (the filter): a mob whose class has both the
// Monster and Humanoid categories, an armour stand, or a player who is not spectating.
// One item goes on, it leaves the dispenser, and the click plays at the front block's
// corner. No mob there takes it, and the item is thrown out as usual.
//
// Horse armour goes into the first equip slot of a
// mob in front whose accepted_items take it, when that inventory slot is
// empty; armor.equip_generic and the click play at the dispense point. BDS does not
// check that the horse is tamed.
//
// Not built: wolf armour, saddles, harnesses, nautilus armour, heads, shields and items
// with a wearable component (ComponentItem, SaddleItem, HarnessItem, NautilusArmorItem,
// SkullItem and ShieldItem each have a dispense of their own, not read).

// typeArmorStand is the armour stand's actor type (ActorType 0x13D).
const typeArmorStand = 61

// humanoidArmourSlots are the head, chest, legs and feet slots humanoid armour goes in.
const humanoidArmourSlots = 4

// humanoidMonsterType reports the actor types whose BDS class sets both the Monster and
// Humanoid categories (0xC): the Zombie class (zombie, husk, drowned,
// frostbite) and its subclasses PigZombie and ZombieVillager (both villager versions), the
// Skeleton class (skeleton, stray, wither skeleton, bogged, parched), witches,
// vindicators, evokers, pillagers and the Piglin class (piglin, piglin brute). Read from
// the class constructors and the actor factory.
func humanoidMonsterType(id int32) bool {
	switch id {
	case 32, 47, 110, 155, // Zombie
		36,      // PigZombie
		44, 116, // ZombieVillager
		34, 46, 48, 144, 151, // Skeleton
		45, 57, 104, 114, // witch, vindicator, evoker, pillager
		123, 127: // Piglin
		return true
	}
	return false
}

// dispenseArmour runs the armour dispense of the item fired, if it has one. False if the
// item is no armour or nothing in front took it.
func (s *Host) dispenseArmour(d dispense) bool {
	it := d.c.items[d.slot]
	t, ok := s.reg.Items.ByName(it.Name)
	if !ok {
		return false
	}
	switch {
	case slices.Contains(t.Tags, tagHorseArmor):
		return s.dispenseHorseArmour(d, it)
	case t.Wearable && !t.BodyArmor && t.ArmorSlot < humanoidArmourSlots:
		if _, armour := s.game.combat.armor[it.Name]; armour {
			return s.dispenseHumanoidArmour(d, it, int(t.ArmorSlot))
		}
	}
	return false
}

// frontEntities lists the entities standing in the chunks of the block in front, and that
// block's box (BDS floors the dispense point, which is the block in front). The caller
// checks boxes. A dispenser fires at most once per redstone pulse, so the list is made
// fresh rather than kept where regions running in parallel would share it.
func frontEntities(d dispense) (lo, hi mgl32.Vec3, found []*world.Entity) {
	lo = mgl32.Vec3{float32(d.front[0]), float32(d.front[1]), float32(d.front[2])}
	hi = lo.Add(mgl32.Vec3{1, 1, 1})
	return lo, hi, d.r.AppendEntitiesIn(nil, lo[0], lo[2], hi[0], hi[2])
}

// boxesTouch reports two boxes overlapping.
func boxesTouch(alo, ahi, blo, bhi mgl32.Vec3) bool {
	return alo[0] < bhi[0] && ahi[0] > blo[0] && alo[1] < bhi[1] && ahi[1] > blo[1] && alo[2] < bhi[2] && ahi[2] > blo[2]
}

// dispenseHumanoidArmour is the equip helper for a helmet, chestplate, leggings, boots,
// turtle shell or elytra worn in armour slot armour (0 head to 3 feet).
func (s *Host) dispenseHumanoidArmour(d dispense, it ItemStack, armour int) bool {
	lo, hi, found := frontEntities(d)
	one := it
	one.Count = 1
	for _, e := range found {
		if !s.equipDispensed(e, lo, hi, armour, one) {
			continue
		}
		d.takeOne()
		playSound(d.r, d.front, soundEvent{name: packet.SoundEventBlockClick, pos: lo, extra: -1}, nil)
		return true
	}
	return false
}

// equipDispensed puts one dispensed armour piece on the entity, if it may wear it and its
// slot is empty.
func (s *Host) equipDispensed(e *world.Entity, lo, hi mgl32.Vec3, armour int, one ItemStack) bool {
	switch o := e.Owner().(type) {
	case *Player:
		return o.wearDispensed(lo, hi, armour, one)
	case *Mob:
		if o.gone || !s.wearsDispensedArmour(o) {
			return false
		}
		blo, bhi, ok := o.mobBox()
		slot := mob.EquipHead + armour
		if !ok || !boxesTouch(lo, hi, blo, bhi) || !o.items.stacks[slot].Empty() {
			return false
		}
		o.setItem(slot, one)
		o.sendEquipment()
		return true
	}
	return false
}

// wearsDispensedArmour is the filter for a mob: a humanoid monster or an armour
// stand.
func (s *Host) wearsDispensedArmour(o *Mob) bool {
	t, ok := s.reg.EntityTypes.ByName(o.m.Type())
	return ok && (humanoidMonsterType(t.RuntimeID) || t.RuntimeID == typeArmorStand)
}

// mobBox is the mob's box, dead or alive, as BDS's entity search finds it.
func (m *Mob) mobBox() (lo, hi mgl32.Vec3, ok bool) {
	if m.entity == nil {
		return lo, hi, false
	}
	b := m.m.Box()
	return mglVec(b.Min), mglVec(b.Max), true
}

// wearDispensed puts a dispensed armour piece on the player when the player is not
// spectating, its box touches the block in
// front and the armour slot is empty. Runs: the player's region.
func (p *Player) wearDispensed(lo, hi mgl32.Vec3, armour int, one ItemStack) bool {
	if p.entity == nil || p.mode() == GameModeSpectator {
		return false
	}
	at := p.entity.Position()
	blo := mgl32.Vec3{at[0] - playerHalfWidth, at[1] - eyeHeight, at[2] - playerHalfWidth}
	bhi := mgl32.Vec3{at[0] + playerHalfWidth, blo[1] + playerHeight, at[2] + playerHalfWidth}
	inv := &p.inventory
	if !boxesTouch(lo, hi, blo, bhi) || armour >= len(inv.armor) || !inv.armor[armour].Empty() {
		return false
	}
	inv.armor[armour] = p.s.freshStack(one)
	p.sendArmour()
	p.sendEquipment()
	p.flush()
	return true
}

// dispenseHorseArmour puts one horse armour into the first equip slot that takes it of a
// mob in front, when that slot is empty.
func (s *Host) dispenseHorseArmour(d dispense, it ItemStack) bool {
	lo, hi, found := frontEntities(d)
	one := it
	one.Count = 1
	for _, e := range found {
		o, ok := e.Owner().(*Mob)
		if !ok || o.gone {
			continue
		}
		blo, bhi, ok := o.mobBox()
		if !ok || !boxesTouch(lo, hi, blo, bhi) {
			continue
		}
		k, ok := horseArmourSlot(o, it.Name)
		if !ok || !o.m.Inventory()[k].Empty() {
			continue
		}
		o.setInventorySlot(k, mobItem(s.freshStack(one)))
		playSound(d.r, d.front, soundEvent{name: packet.SoundEventEquipGeneric, pos: d.at, extra: -1}, nil)
		playSound(d.r, d.front, soundEvent{name: packet.SoundEventBlockClick, pos: d.at, extra: -1}, nil)
		d.takeOne()
		return true
	}
	return false
}

// horseArmourSlot is the first of the mob's equip slots whose accepted_items take the
// item, and that its inventory has.
func horseArmourSlot(o *Mob, name string) (int, bool) {
	n := min(len(o.m.EquipSlots()), len(o.m.Inventory()))
	for k := range n {
		if o.m.EquipSlotTakes(k, name) {
			return k, true
		}
	}
	return 0, false
}
