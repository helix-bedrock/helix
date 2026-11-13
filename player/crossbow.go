package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// Crossbows, from BDS 1.26.60's CrossbowItem (vtable 0xFB61328, constructor 0xC9AEBE0):
// use 0xC9AF5C0 starts loading or shoots what is loaded, releaseUsing 0xC9B0EA0 loads once
// fully drawn, useTimeDepleted 0xC9B0E70 lets go by itself when the draw is done.
const (
	nameCrossbow = "minecraft:crossbow"

	// arrowGroupCrossbow is the part of the arrow's definition a player's crossbow gives it
	// (its entity_spawned event: a player holding a crossbow).
	arrowGroupCrossbow = "minecraft:player_crossbow_arrow"

	// crossbowChargedTag is where the loaded item is kept in the crossbow's tag.
	crossbowChargedTag = "chargedItem"

	// crossbowLoadTicks is a full draw, less crossbowQuickChargeTicks a Quick Charge level.
	crossbowLoadTicks        = 25
	crossbowQuickChargeTicks = 5
	// crossbowMultishotCount is how many a Multishot crossbow loads (+748), and
	// crossbowMultishotAngle how many degrees apart they fly (+752).
	crossbowMultishotCount = 3
	crossbowMultishotAngle = 10
	// crossbowWearArrow is one arrow's wear; crossbowWearMore a firework's or Multishot's.
	crossbowWearArrow = 1
	crossbowWearMore  = 3

	// The enchantments a crossbow reads, by BDS's IDs.
	enchantMultishot   = int16(33)
	enchantPiercing    = int16(34)
	enchantQuickCharge = int16(35)
)

// crossbowLoadTime is how many ticks loading takes, BDS's 25 - 5 per Quick Charge.
func crossbowLoadTime(held ItemStack) int32 {
	if q := heldEnchant(held, enchantQuickCharge); q > 0 {
		return crossbowLoadTicks - crossbowQuickChargeTicks*q
	}
	return crossbowLoadTicks
}

// crossbowCharged is what a crossbow has loaded, empty if nothing.
func crossbowCharged(s ItemStack) ItemStack {
	m, ok := s.Tag[crossbowChargedTag].(map[string]any)
	if !ok {
		return ItemStack{}
	}
	return itemFromNBT(m)
}

// crossbowAmmo is what the crossbow would load and from where: the offhand's arrows or
// rockets first, then arrows in the inventory (-1 for the offhand). False with none.
func (inv *playerItems) crossbowAmmo() (ItemStack, int, bool) {
	if o := inv.offhand[0]; o.Count > 0 && (o.Name == nameArrow || o.Name == nameFireworkRocket) {
		return o, -1, true
	}
	for i := range inv.main {
		if inv.main[i].Name == nameArrow && inv.main[i].Count > 0 {
			return inv.main[i], i, true
		}
	}
	return ItemStack{}, 0, false
}

// useCrossbow is BDS's CrossbowItem::use: a loaded crossbow shoots, an empty one starts
// loading if there is something to load (anything in creative). False if nothing happened.
func (p *Player) useCrossbow(held ItemStack) bool {
	if !crossbowCharged(held).Empty() {
		// The client keeps sending the use while the button stays held after loading; only
		// a new press shoots, so holding to load does not fire the moment it is loaded.
		if p.useRepeat {
			return true
		}
		p.shootCrossbow(held)
		return true
	}
	if _, _, ok := p.inventory.crossbowAmmo(); !ok && !p.creative() {
		return false
	}
	u := &p.using
	slot := p.inventory.held
	if u.active && u.slot == slot && u.item.Name == held.Name {
		return true
	}
	*u = usingState{active: true, slot: slot, item: held, left: crossbowLoadTime(held), crossbow: true}
	p.sendActorFlags()
	return true
}

// loadCrossbow is BDS's releaseUsing: only a full draw loads. The ammo is taken (not in
// creative, where an arrow is loaded even with none), Multishot loads three, and those who
// see the player are told it finished charging.
func (p *Player) loadCrossbow(held ItemStack, drawn int32) {
	if drawn < crossbowLoadTime(held) || p.entity == nil {
		return
	}
	creative := p.creative()
	ammo, slot, ok := p.inventory.crossbowAmmo()
	if !ok {
		if !creative {
			return
		}
		ammo = ItemStack{Name: nameArrow}
	}
	count := uint8(1)
	if heldEnchant(held, enchantMultishot) > 0 {
		count = crossbowMultishotCount
	}
	loaded := ammo
	loaded.Count = count
	loaded.netID, loaded.saved = 0, nil
	inv := &p.inventory
	bow := &inv.main[inv.held]
	bow.Tag = cloneTag(bow.Tag)
	bow.Tag[crossbowChargedTag] = loaded.nbt(0, false)
	if ok && !creative {
		if slot < 0 {
			inv.offhand[0].Count--
			if inv.offhand[0].Count == 0 {
				inv.offhand[0] = ItemStack{}
			}
			p.sendOffhand()
			p.sendEquipment()
		} else {
			s := &inv.main[slot]
			s.Count--
			if s.Count == 0 {
				*s = ItemStack{}
			}
			p.sendSlot(slot)
		}
	}
	p.sendHeld()
	p.entity.Send(actorEvent(p.runtimeID, packet.ActorEventFinishedChargingItem))
	p.flush()
}

// shootCrossbow is BDS's use of a loaded crossbow: each loaded item flies along the look,
// Multishot's at -10, 0 and +10 degrees, arrows with the crossbow's arrow definition,
// critical, only the middle one of three picked up; rockets fly free along the aim. The
// crossbow wears 1 for one arrow, 3 for a rocket or Multishot, then the shot sound and the
// crossbow is empty.
func (p *Player) shootCrossbow(held ItemStack) {
	r := p.entity.Region()
	if r == nil {
		return
	}
	loaded := crossbowCharged(held)
	n := int(loaded.Count)
	look := lookDirection(p.entity.Rotation())
	firework := loaded.Name == nameFireworkRocket
	for i := range n {
		dir := look
		if n != 1 {
			a := float64(i*crossbowMultishotAngle-crossbowMultishotAngle) * math.Pi / 180
			c, s := float32(math.Cos(a)), float32(math.Sin(a))
			dir = mgl32.Vec3{look[0]*c + look[2]*s, look[1], look[2]*c - look[0]*s}
		}
		if firework {
			p.shootCrossbowRocket(r, loaded, dir)
			continue
		}
		if !p.shootCrossbowArrow(r, held, loaded, dir, n != 1 && i != 1) {
			return
		}
	}
	playSound(r, p.blockPos(), soundEvent{name: packet.SoundEventCrossbowShoot, pos: p.AttachPos(AttachWeapon), extra: -1, entity: "minecraft:player"}, nil)
	p.unloadCrossbow()
	if !p.creative() {
		wear := int32(crossbowWearArrow)
		if firework || n >= 2 {
			wear = crossbowWearMore
		}
		p.hurtHeld(wear)
	}
	p.sendHeld()
	p.flush()
}

// shootCrossbowRocket launches one of the loaded rockets free along dir.
func (p *Player) shootCrossbowRocket(r *world.Region, loaded ItemStack, dir mgl32.Vec3) {
	one := loaded
	one.Count = 1
	f := p.s.LaunchFirework(r.Dimension(), p.entity.Position(), dir, one, nil)
	f.shot, f.shooter = true, p
}

// shootCrossbowArrow shoots one critical arrow along dir with the crossbow's arrow
// definition and Piercing; a side arrow of Multishot (side) cannot be picked up. False if
// arrows have no definition.
func (p *Player) shootCrossbowArrow(r *world.Region, held, loaded ItemStack, dir mgl32.Vec3, side bool) bool {
	spec, ok := p.s.projectileSpec(nameArrow, arrowGroupCrossbow)
	if !ok {
		return false
	}
	t := p.s.SpawnProjectile(r.Dimension(), spec, projectileEyes(spec, p.entity.Position(), p.entity.Rotation()), dir, spec.Power, p)
	t.critical = true
	t.pierce = heldEnchant(held, enchantPiercing)
	// BDS (0xC9CD650) gives the arrow the loaded arrow's potion; its effects wait for the
	// potion table, but the arrow keeps its aux so a tipped one is picked up tipped.
	if loaded.Meta > 0 {
		t.item = ItemStack{Name: nameArrow, Meta: loaded.Meta, Count: 1}
	}
	if p.creative() || side {
		t.pickup = arrowPickupCreative
	}
	return true
}

// unloadCrossbow takes the charge out of the held crossbow.
func (p *Player) unloadCrossbow() {
	inv := &p.inventory
	bow := &inv.main[inv.held]
	bow.Tag = cloneTag(bow.Tag)
	delete(bow.Tag, crossbowChargedTag)
	if len(bow.Tag) == 0 {
		bow.Tag = nil
	}
}

// cloneTag copies a tag so a change does not reach other copies of the stack.
func cloneTag(tag map[string]any) map[string]any {
	out := make(map[string]any, len(tag)+1)
	for k, v := range tag {
		out[k] = v
	}
	return out
}
