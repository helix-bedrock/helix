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

// trustMob is minecraft:trusting: the mob shows eating the item (Actor::feed 0xB3AF5A0,
// ActorEvent 57 with the item's ID and aux), the item is used up, then the mob rolls. The
// eat game event Actor::feed also posts is not sent.
func (p *Player) trustMob(o *Mob) bool {
	held := p.inventory.Held()
	if held.Empty() || !o.m.FeedTrust(mob.HeldItem{Name: held.Name}) {
		return false
	}
	if t, ok := p.s.reg.Items.ByName(held.Name); ok {
		o.entity.Send(actorEventData(o.m.RuntimeID(), packet.ActorEventFeed, int32(t.ID)<<16|int32(uint16(held.Meta))))
	}
	p.useUpHeld()
	p.flush()
	return true
}

// feedMount is minecraft:tamemount's feeding: a feed item raises the mount's temper and is
// used up; an auto-reject item does nothing more (BDS's reject action was not read).
func (p *Player) feedMount(o *Mob) bool {
	held := p.inventory.Held()
	if held.Empty() {
		return false
	}
	r, ok := o.m.FeedMount(mob.HeldItem{Name: held.Name})
	if !ok {
		return false
	}
	if r == state.InteractFeed {
		p.useUpHeld()
		p.flush()
	}
	return true
}

// sitMob is minecraft:sittable: the owner makes the mob sit or stand.
func (p *Player) sitMob(o *Mob) bool {
	return o.m.ToggleSit(actorOf(p.entity))
}
