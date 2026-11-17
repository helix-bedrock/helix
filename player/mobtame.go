package player

import (
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/protocol/packet"
)

// The taming, trusting, tame-mount and sitting parts of a player's use on a mob
// (mobInteractions has their order). The mob decides (mob/tame.go); the engine does the
// item work.

// tameMob is minecraft:tameable: a tame item is used up (or becomes its result) whether the
// try succeeds or not.
func (p *Player) tameMob(o *Mob) bool {
	held := p.inventory.Held()
	if held.Empty() {
		return false
	}
	result, ok := o.m.Tame(actorOf(p.entity), mob.HeldItem{Name: held.Name})
	if !ok {
		return false
	}
	p.useFed(result)
	return true
}

// trustMob is minecraft:trusting: the mob shows eating the item (Actor::feed), the item is
// used up, then the mob rolls.
func (p *Player) trustMob(o *Mob) bool {
	held := p.inventory.Held()
	if held.Empty() || !o.m.FeedTrust(mob.HeldItem{Name: held.Name}) {
		return false
	}
	p.showFeed(o, held)
	p.useUpHeld()
	p.flush()
	return true
}

// bribeMob is minecraft:bribeable: Player::useItem uses up the item (method 11), the mob
// shows eating it (Actor::feed) and is bribed (callback 0x7404580).
func (p *Player) bribeMob(o *Mob) bool {
	held := p.inventory.Held()
	if held.Empty() || !o.m.Bribe(mob.HeldItem{Name: held.Name}) {
		return false
	}
	p.useUpHeld()
	p.showFeed(o, held)
	p.flush()
	return true
}

// giveMob is minecraft:giveable: one of the held item goes into the mob's inventory and the
// player's is used up (callback 0x734AF40).
func (p *Player) giveMob(o *Mob) bool {
	held := p.inventory.Held()
	if held.Empty() || !o.m.Give(actorOf(p.entity), held.Name) {
		return false
	}
	p.useUpHeld()
	p.flush()
	return true
}

// showFeed is Actor::feed (0xB3AF5A0): ActorEvent 57 with the item's ID and aux for those
// who see the mob, and the eat game event at its mouth (the eyes stand in: mobs have no
// attach points yet).
func (p *Player) showFeed(o *Mob, held ItemStack) {
	if t, ok := p.s.reg.Items.ByName(held.Name); ok {
		o.entity.Send(actorEventData(o.m.RuntimeID(), packet.ActorEventFeed, int32(t.ID)<<16|int32(uint16(held.Meta))))
	}
	o.m.EatGameEvent(o.m.Eyes())
}

// feedMount is minecraft:tamemount's feeding: a feed item raises the mount's temper and is
// used up; an auto-reject item (a saddle on a wild horse) makes the mount rear up and is
// kept, and the player's arm swings, sent to the player too (a packet capture of BDS 1.26.60:
// Animate with the interact swing source, then the horse's mad sound and flags).
func (p *Player) feedMount(o *Mob) bool {
	held := p.inventory.Held()
	if held.Empty() {
		return false
	}
	r, ok := o.m.FeedMount(mob.HeldItem{Name: held.Name})
	if !ok {
		return false
	}
	switch r {
	case state.InteractFeed:
		p.useUpHeld()
		p.flush()
	case state.InteractReject:
		if reg := p.entity.Region(); reg != nil {
			reg.Broadcast(p.blockPos(), &packet.Animate{
				ActionType: packet.AnimateActionSwingArm, EntityRuntimeID: p.runtimeID, SwingSource: packet.AnimateSwingSourceInteract,
			}, nil)
		}
	}
	return true
}

// sitMob is minecraft:sittable: the owner makes the mob sit or stand.
func (p *Player) sitMob(o *Mob) bool {
	return o.m.ToggleSit(actorOf(p.entity))
}
