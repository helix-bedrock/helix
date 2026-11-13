package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// A dropped item's numbers, from BDS 1.26.60's drop.
const (
	dropHeightBelowEyes = 0.5
	dropSpeed           = 0.3
	dropSpread          = 0.02
	dropRandomSpeed     = 0.5
	dropRandomUp        = 0.2
	dropPickupDelay     = 10
	dropThrowTime       = 40
)

// worldSlotDrop is the slot of a world action that throws an item out.
const worldSlotDrop = 0

// DropItemFunc puts a stack the player gave up in the world; false if it
// could not, and the stack goes back to the player. The stack is the
// server's. randomly throws it in any direction, as a player's death does.
type DropItemFunc func(p *Player, s ItemStack, randomly bool) bool

// PickUpFunc gives the player an item it touches, once its delays ended.
type PickUpFunc func(p *Player, i *ItemActor)

// vanillaDropItem is BDS's player drop: the swing, then the item from below
// the eyes, thrown where the player looks or at random.
func vanillaDropItem(p *Player, s ItemStack, randomly bool) bool {
	if s.Empty() || p.entity == nil {
		return false
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	pos, rot := p.entity.Position(), p.entity.Rotation()
	pos[1] -= dropHeightBelowEyes
	i := p.s.newItem(s, p.dropVelocity(rot, randomly), dropPickupDelay, dropThrowTime)
	if g := p.s.game; g.itemDropEv.Has() {
		e := ItemDropEvent{Player: p, Item: i}
		g.itemDropEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
		if i.stack.Empty() {
			return true
		}
	}
	r.Broadcast(p.blockPos(), &packet.Animate{
		ActionType: packet.AnimateActionSwingArm, EntityRuntimeID: p.runtimeID, SwingSource: packet.AnimateSwingSourceDropItem,
	}, p.viewer)
	i.addTo(r.Dimension(), pos, rot)
	return true
}

// dropVelocity is the velocity BDS gives a dropped item.
func (p *Player) dropVelocity(rot world.Rotation, randomly bool) mgl64.Vec3 {
	if randomly {
		speed := p.rng.Float32() * dropRandomSpeed
		angle := p.rng.Float32() * 2 * math.Pi * direction.UnitsPerRadian
		return mgl64.Vec3{float64(-direction.Sin(angle) * speed), dropRandomUp, float64(direction.Cos(angle) * speed)}
	}
	yaw := direction.Units(rot.Yaw)
	pitch := direction.Units(rot.Pitch)
	flat := direction.Cos(pitch) * dropSpeed
	angle := p.rng.Float32() * 2 * math.Pi * direction.UnitsPerRadian
	spread := p.rng.Float32() * dropSpread
	return mgl64.Vec3{
		float64(-direction.Sin(yaw)*flat + direction.Cos(angle)*spread),
		float64(-direction.Sin(pitch) * dropSpeed),
		float64(direction.Cos(yaw)*flat + direction.Sin(angle)*spread),
	}
}

// dropAction is a stack request's drop: the server's stack in the slot the
// server checked, only as many as it holds. BDS clamps a count that is too
// high; Helix refuses the request, since an honest client never sends one.
func (p *Player) dropAction(a *protocol.DropStackRequestAction) (uint8, []slotRef) {
	src, ok := p.validate(a.Source, true)
	if !ok {
		return protocol.ItemStackResponseStatusFailedToValidateSrcSlot, nil
	}
	if !p.abilities.Flag(AbilityDoorsAndSwitches) || !p.mayRemove(src, *src.stack) {
		return protocol.ItemStackResponseStatusCannotDropItem, nil
	}
	if a.Count == 0 || a.Count > src.stack.Count {
		return protocol.ItemStackResponseStatusInvalidRemovedAmount, nil
	}
	dropped := *src.stack
	dropped.Count = a.Count
	src.stack.Count -= a.Count
	if src.stack.Count == 0 {
		*src.stack = ItemStack{}
	}
	st := &p.requests
	st.drops = append(st.drops, pendingDrop{stack: dropped, randomly: a.Randomly})
	return protocol.ItemStackResponseStatusOK, []slotRef{src}
}

// spawnDrops throws out what a request that passed dropped.
func (p *Player) spawnDrops() {
	st := &p.requests
	for i := range st.drops {
		p.dropOrGiveBack(st.drops[i].stack, st.drops[i].randomly)
		st.drops[i] = pendingDrop{}
	}
	st.drops = st.drops[:0]
}

// dropOrGiveBack drops a stack already out of the inventory. One that cannot
// be dropped returns to the inventory, so no item is lost.
func (p *Player) dropOrGiveBack(s ItemStack, randomly bool) {
	if !p.s.game.sys.DropItem(p, s, randomly) {
		p.Give(s)
	}
}

// handleNormalTransaction handles the legacy transaction the client drops a
// held item with (Q): one action takes items from an inventory slot, one
// throws them in the world. BDS checks the slot's old item, balances the two,
// then keeps the items the client named. Helix keeps none of them: only the
// slot and how many are read, and what is dropped comes from the server's
// stack, so its data cannot be forged. Anything else is refused and the
// client is sent the slot back.
func (p *Player) handleNormalTransaction(actions []protocol.InventoryAction) {
	slot, count, ok := p.legacyDrop(actions)
	if ok {
		ok = p.abilities.Flag(AbilityDoorsAndSwitches)
	}
	if ok {
		s := &p.inventory.main[slot]
		if ok = p.mayRemove(slotRef{kind: containerCombined, stack: s}, *s); ok {
			dropped := *s
			dropped.Count = count
			s.Count -= count
			if s.Count == 0 {
				*s = ItemStack{}
			}
			p.dropOrGiveBack(dropped, legacyDropRandom(actions))
		}
	}
	for _, a := range actions {
		if a.SourceType == protocol.InventoryActionSourceContainer && a.InventorySlot < inventorySize {
			if w, set := a.WindowID.Value(); set && w == protocol.WindowIDInventory {
				p.sendSlot(int(a.InventorySlot))
			}
		}
	}
	p.sendEquipment()
	p.flush()
}

// legacyDrop checks a legacy transaction against the server's items. It
// returns the slot and how many items leave it.
func (p *Player) legacyDrop(actions []protocol.InventoryAction) (slot int, count uint8, ok bool) {
	if len(actions) != 2 {
		return 0, 0, false
	}
	var from, out *protocol.InventoryAction
	for i := range actions {
		switch a := &actions[i]; a.SourceType {
		case protocol.InventoryActionSourceContainer:
			from = a
		case protocol.InventoryActionSourceWorld:
			out = a
		}
	}
	if from == nil || out == nil || out.InventorySlot != worldSlotDrop || from.InventorySlot >= inventorySize {
		return 0, 0, false
	}
	if w, set := from.WindowID.Value(); !set || w != protocol.WindowIDInventory {
		return 0, 0, false
	}
	slot = int(from.InventorySlot)
	s := p.inventory.main[slot]
	if s.Empty() || !p.sameAsClient(s, from.OldItem) || out.OldItem.Stack.Count != 0 || out.NewItem.Stack.Count == 0 {
		return 0, 0, false
	}
	left, thrown := from.NewItem.Stack.Count, out.NewItem.Stack.Count
	if left >= uint16(s.Count) || left+thrown != uint16(s.Count) {
		return 0, 0, false
	}
	if t, known := p.s.reg.Items.ByName(s.Name); !known || int32(t.ID) != out.NewItem.Stack.NetworkID {
		return 0, 0, false
	}
	return slot, uint8(thrown), true //nolint:gosec // less than the stack's count
}

// legacyDropRandom reads the world action's random flag.
func legacyDropRandom(actions []protocol.InventoryAction) bool {
	for _, a := range actions {
		if a.SourceType == protocol.InventoryActionSourceWorld {
			flags, _ := a.SourceFlags.Value()
			return flags&1 != 0
		}
	}
	return false
}

// sameAsClient reports whether a stack the client names is the one the server
// has: same item, aux value and count. Its data is never read: nothing takes
// it from the client.
func (p *Player) sameAsClient(s ItemStack, c protocol.ItemInstance) bool {
	t, ok := p.s.reg.Items.ByName(s.Name)
	return ok && int32(t.ID) == c.Stack.NetworkID && uint32(uint16(s.Meta)) == c.Stack.MetadataValue && uint16(s.Count) == c.Stack.Count
}

// tickPickup looks for items the player touches, in BDS's pick-up area: its
// box grown 1 block sideways and half a block up and down. Spectators never
// pick up (Helix's choice until BDS's check is read). Region tick.
func (p *Player) tickPickup() {
	if p.entity == nil || p.Health() <= 0 || p.gameMode == GameModeSpectator {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	const (
		reach = playerHalfWidth + pickupSideways
		upTo  = playerHeight - playerEyeHeight + pickupVertical
		down  = playerEyeHeight + pickupVertical
	)
	pos := p.entity.Position()
	p.touching = r.AppendEntitiesIn(p.touching[:0], pos[0]-reach-itemHalfSize, pos[2]-reach-itemHalfSize, pos[0]+reach+itemHalfSize, pos[2]+reach+itemHalfSize)
	for i, e := range p.touching {
		p.touching[i] = nil
		it, ok := e.Owner().(*ItemActor)
		if !ok || it.taken || it.pickupDelay != 0 || it.throwTime != 0 || !it.CanSee(p) {
			continue
		}
		ip := e.Position()
		if ip[0]+itemHalfSize < pos[0]-reach || ip[0]-itemHalfSize > pos[0]+reach ||
			ip[2]+itemHalfSize < pos[2]-reach || ip[2]-itemHalfSize > pos[2]+reach ||
			ip[1]+itemHalfSize < pos[1]-down || ip[1]-itemHalfSize > pos[1]+upTo {
			continue
		}
		p.s.game.sys.PickUp(p, it)
	}
}

// How far past the player's box it picks up items.
const (
	pickupSideways = 1
	pickupVertical = 0.5
)

// vanillaPickUp is BDS's item touch and take: nothing without room, what
// fits goes to the offhand and the inventory, the rest stays in the world. A
// creative player's rest is thrown away.
func vanillaPickUp(p *Player, i *ItemActor) {
	if !p.creative() && !p.inventory.canAdd(p.s.reg, i.stack) && !p.offhandTakes(i.stack) {
		return
	}
	// An item no region holds is not taken, so nothing is given for it.
	if i.taken || i.entity.Region() == nil {
		return
	}
	rest := p.Give(i.stack)
	if !rest.Empty() && !p.creative() {
		if rest.Count == i.stack.Count {
			return
		}
		i.stack.Count = rest.Count
		i.showCount()
		return
	}
	i.taken = true
	i.entity.Send(&packet.TakeItemActor{ItemEntityRuntimeID: i.entity.RuntimeID(), TakerEntityRuntimeID: p.runtimeID})
	i.entity.Remove()
}

// offhandTakes reports whether the offhand can hold more of s, as BDS's
// canStackInOffhand.
func (p *Player) offhandTakes(s ItemStack) bool {
	off := p.inventory.offhand[0]
	return !off.Empty() && off.stacksWith(s) && off.Count < s.maxStack(p.s.reg) && itemDamage(s) == 0
}

// Give puts a stack in the player's offhand and inventory, as BDS does when
// it picks up an item, tells the client, and returns what did not fit. Runs:
// owner of the player's entity.
func (p *Player) Give(s ItemStack) ItemStack {
	if p.offhandTakes(s) {
		off := &p.inventory.offhand[0]
		moved := min(s.maxStack(p.s.reg)-off.Count, s.Count)
		off.Count += moved
		s.Count -= moved
		p.sendOffhand()
	}
	if s.Count == 0 {
		p.sendEquipment()
		p.flush()
		return ItemStack{}
	}
	rest := p.inventory.add(p.s.reg, s, func() int32 { return p.s.stackIDs.Add(1) }, p.sendSlot)
	p.syncStorage()
	p.sendEquipment()
	p.flush()
	return rest
}

// sendSlot sends an inventory slot as the server has it.
func (p *Player) sendSlot(i int) {
	// A storage item put in gets its ID before the slot is sent.
	p.syncStorageIDs()
	p.Inventory().sendSlot(p, i, false)
}

// sendEquipment shows the players who see this one what it holds, if that
// changed.
func (p *Player) sendEquipment() {
	if p.armorChanged() {
		p.showArmor()
	}
	if p.offhandChanged() {
		p.showOffhand()
	}
	inv := &p.inventory
	held := inv.Held()
	if p.entity == nil || (held.netID == p.shownHeld.netID && held.Count == p.shownHeld.Count && inv.held == p.shownSlot) {
		return
	}
	p.shownHeld, p.shownSlot = held, inv.held
	r := p.entity.Region()
	if r == nil {
		return
	}
	r.Broadcast(p.blockPos(), &packet.MobEquipment{
		EntityRuntimeID: p.runtimeID, NewItem: held.network(p.s.reg), InventorySlot: inv.held, HotBarSlot: inv.held, WindowID: protocol.WindowIDInventory,
	}, p.viewer)
}

// offhandChanged reports whether the offhand item differs from what those who see the
// player were last shown.
func (p *Player) offhandChanged() bool {
	off := &p.inventory.offhand[0]
	return off.netID != p.shownOffhand.netID || off.Count != p.shownOffhand.Count
}

// offhandEquipment is the offhand item as those who see the player get it, as BDS
// sends a change of that slot: slots 0 in the offhand window.
func (p *Player) offhandEquipment() *packet.MobEquipment {
	return &packet.MobEquipment{EntityRuntimeID: p.runtimeID, NewItem: p.inventory.offhand[0].network(p.s.reg), WindowID: protocol.WindowIDOffHand}
}

// showOffhand shows those who see the player its offhand item. Its own client has it
// from its inventory.
func (p *Player) showOffhand() {
	p.shownOffhand = p.inventory.offhand[0]
	if p.entity != nil {
		p.entity.Send(p.offhandEquipment())
	}
}

// armorChanged reports whether what the player wears differs from what those who see
// it were last shown.
func (p *Player) armorChanged() bool {
	for i := range p.inventory.armor {
		if p.inventory.armor[i].netID != p.shownArmor[i] {
			return true
		}
	}
	return false
}

// armorEquipment is what the player wears as those who see it get it, as BDS's mob
// sends its armour: every piece in one packet.
func (p *Player) armorEquipment() *packet.MobArmourEquipment {
	a, reg := &p.inventory.armor, p.s.reg
	return &packet.MobArmourEquipment{
		EntityRuntimeID: p.runtimeID, Helmet: a[0].network(reg), Chestplate: a[1].network(reg),
		Leggings: a[2].network(reg), Boots: a[3].network(reg), Body: a[4].network(reg),
	}
}

// showArmor shows those who see the player what it wears. Its own client has it from
// its inventory.
func (p *Player) showArmor() {
	for i := range p.inventory.armor {
		p.shownArmor[i] = p.inventory.armor[i].netID
	}
	if p.entity != nil {
		p.entity.Send(p.armorEquipment())
	}
}
