package player

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/protocol/packet"
)

// The horse window: the inventory of a mob with minecraft:inventory of container_type horse
// (mob/horse.go has its slots). BDS 1.26.60 opens it from BDS's code
// (other mounts take the generic ContainerComponent path) when the mob has no
// rider or the player is its rider: ContainerOpen of type 12 at the mob's floored position
// with its unique ID, then UpdateEquip with the same window ID, which
// tells the client the equip slots and what each takes. The window shows the equip slots
// as container HorseEquip and the chest as LevelEntity, numbered after the equip slots.
// A player's use on the mob opens it when nothing else applies (BDS's code
// 's open-container interaction, checked by: alive, the player may open
// containers, and a private inventory only for its owner); so does a rider asking for its
// inventory, whose client opens the screen itself: a packet capture of BDS 1.26.60 shows
// a rider's Interact OpenInventory answered with UpdateEquip and InventoryContent only.

// ContainerHorse is a horse's, donkey's, mule's, llama's or camel's inventory.
const ContainerHorse = ContainerBeacon + 1

// The UpdateEquip compound's keys and the item descriptor's.
const (
	equipKeySlots    = "slots"
	equipKeySlotNum  = "slotNumber"
	equipKeyItem     = "item"
	equipKeyAccepted = "acceptedItems"
	equipKeySlotItem = "slotItem"
	itemKeyName      = "Name"
	itemKeyAux       = "Aux"
	// anyAux is an item descriptor's aux when it names none.
	anyAux int16 = 0x7FFF
	// flagContainerPrivate is the actor flag BDS's code checks before offering
	// the open-container interaction.
	flagContainerPrivate = 54
)

// horseHolder holds a mob's window slots: who has them open, and the mob each change goes
// back to.
type horseHolder struct {
	o       *Mob
	viewers []*Player
}

func (h *horseHolder) inventoryViewers() []*Player { return h.viewers }

// inventoryChanged puts the slot back on the mob, which runs its equip events.
func (h *horseHolder) inventoryChanged(slot int) {
	inv := h.o.horseInv
	if inv == nil {
		return
	}
	if st := inv.at(slot); st != nil {
		h.o.setInventorySlot(slot, mobItem(*st))
	}
}

// openHorse opens the mob's inventory window, if the player may. rider is true when the
// mob's rider asked for it (Interact OpenInventory), whose client has the screen open
// already: BDS then sends UpdateEquip and the slots with no ContainerOpen.
func (p *Player) openHorse(o *Mob, rider bool) bool {
	r := p.entity.Region()
	if r == nil || p.window.open || o.gone || o.m.Dead() || !o.m.HorseInventory() || !p.abilities.Flag(AbilityOpenContainers) {
		return false
	}
	if riders := o.m.Seats().Riders(); len(riders) > 0 && riders[0] != p {
		return false
	}
	o.syncHorseInv()
	ow := windowOpening{
		kind: ContainerHorse, window: byte(protocol.ContainerTypeHorse), pos: floorPos(o.entity.Position()),
		entity: o.entity.UniqueID(), inv: o.horseInv, screenOpen: rider,
	}
	if !p.mayOpen(r, &ow) {
		return false
	}
	p.window.horse = o
	p.showWindowThen(r, &ow, func() { p.send(o.updateEquip(p.window.id)) })
	o.horseView.viewers = append(o.horseView.viewers, p)
	return true
}

// mayOpenMob is the open-container interaction's test for a use on the mob.
func (p *Player) mayOpenMob(o *Mob) bool {
	return !o.m.Flag(flagContainerPrivate) && o.m.HorseInventory() && p.openHorse(o, false)
}

// syncHorseInv copies the mob's slots into its window inventory, resized to them.
func (o *Mob) syncHorseInv() {
	slots := o.m.Inventory()
	if o.horseView == nil {
		o.horseView = &horseHolder{o: o}
	}
	if o.horseInv == nil || len(o.horseInv.parts[0]) != len(slots) {
		o.horseInv = &Inventory{s: o.s, kind: ContainerHorse, parts: [2][]ItemStack{make([]ItemStack, len(slots))}, holder: o.horseView}
	}
	for i, it := range slots {
		o.horseInv.parts[0][i] = realStack(it)
	}
}

// updateEquip is the UpdateEquip packet for the window: each equip slot's number, the item
// it shows and the items it takes.
func (o *Mob) updateEquip(window byte) *packet.UpdateEquip {
	var slots []any
	for _, sl := range o.m.EquipSlots() {
		accepted := make([]any, 0, len(sl.AcceptedItems))
		for _, d := range sl.AcceptedItems {
			accepted = append(accepted, map[string]any{equipKeySlotItem: descriptorTag(d)})
		}
		e := map[string]any{equipKeySlotNum: sl.Slot, equipKeyAccepted: accepted}
		if sl.Item != "" {
			e[equipKeyItem] = descriptorTag(sl.Item)
		}
		slots = append(slots, e)
	}
	data, err := nbt.MarshalEncoding(map[string]any{equipKeySlots: slots}, nbt.NetworkLittleEndian)
	if err != nil {
		o.s.log.Debug("update equip", "err", err)
	}
	return &packet.UpdateEquip{
		WindowID: window, WindowType: byte(protocol.ContainerTypeHorse), EntityUniqueID: o.entity.UniqueID(), SerialisedInventoryData: data,
	}
}

// descriptorTag is an item descriptor as BDS writes it.
func descriptorTag(d string) map[string]any {
	return map[string]any{itemKeyName: itemName(d), itemKeyAux: anyAux}
}

// horseSlot resolves a horse window's slot to the flat slots: HorseEquip's are the equip
// slots, LevelEntity's come after them.
func (p *Player) horseSlot(container, slot byte) (int, bool) {
	o := p.window.horse
	if o == nil || p.window.kind != ContainerHorse {
		return 0, false
	}
	equip := len(o.m.EquipSlots())
	k := int(slot)
	switch container {
	case protocol.ContainerHorseEquip:
		if k >= equip {
			return 0, false
		}
	case containerLevelEntity:
		k += equip
	default:
		return 0, false
	}
	return k, true
}

// horseSlotsTake reports that every equip slot of an open horse window holds nothing or an
// item it takes, after a request's actions.
func (p *Player) horseSlotsTake() bool {
	o := p.window.horse
	if o == nil || p.window.kind != ContainerHorse {
		return true
	}
	work := p.requests.work.open
	for k := range o.m.EquipSlots() {
		if k < len(work) && !work[k].Empty() && (!o.m.EquipSlotTakes(k, work[k].Name) || work[k].Count > 1) {
			return false
		}
	}
	return true
}

// horseLive reports that the open horse window's mob still holds its slots here.
func (p *Player) horseLive(r *world.Region) bool {
	o := p.window.horse
	return o != nil && !o.gone && !o.m.Dead() && o.entity.Region() == r
}

// leaveHorse forgets the player as a viewer of the mob's window.
func (o *Mob) leaveHorse(p *Player) {
	if o == nil || o.horseView == nil {
		return
	}
	v := o.horseView.viewers
	for i := range v {
		if v[i] == p {
			o.horseView.viewers = append(v[:i], v[i+1:]...)
			return
		}
	}
}

// dropInventoryOnDeath drops every stack of the mob's minecraft:inventory where it died,
// as BDS (the container drop) does, and empties the slots.
func (o *Mob) dropInventoryOnDeath(r *world.Region) {
	inv := o.m.Inventory()
	if r == nil || len(inv) == 0 {
		return
	}
	at := o.entity.Position()
	for k, it := range inv {
		if it.Empty() {
			continue
		}
		o.s.spawnMobDrop(r, realStack(it), at)
		o.setInventorySlot(k, behavior.Item{})
	}
}
