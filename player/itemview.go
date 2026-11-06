package player

import (
	"maps"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// ItemView is what one player is shown of an item. Changing it changes nothing
// on the server: the real stack stays, and that is what a pick-up gives.
type ItemView struct {
	// Name is a name tag always shown over the item; empty shows none.
	Name string
	// Stack is the item shown. Starts as the real stack. A change reaches a
	// player who already sees the item through [ItemActor.Refresh].
	Stack ItemStack
}

// ItemDropEvent fires before an item a player gave up enters the world. Item
// has no entity yet, but its rules and stack may be set. Cancel keeps the
// stack with the player.
type ItemDropEvent struct {
	Cancellable
	Player *Player
	Item   *ItemActor
}

// ItemLoadEvent fires when a saved item comes back with its chunk. Visibility
// rules and views are plugin code and are not saved, so this is where they are
// set again.
type ItemLoadEvent struct {
	Item *ItemActor
}

// SetVisibility decides which players see the item; nil is everyone in range.
// A player the rule refuses is sent nothing about the item and cannot pick it
// up. The rule runs each tick for each player in range, so it may change its
// answer. Runs: owner of the item's entity, or in an [ItemDropEvent].
func (i *ItemActor) SetVisibility(rule func(p *Player) bool) {
	i.rule = rule
	if i.entity != nil {
		i.entity.SetVisibleTo(i.visibleFunc())
	}
}

// SetView sets what each player is shown of the item, called once a second for
// each player who sees it and when it is shown to one; nil shows the real item.
// Runs: owner of the item's entity, or in an [ItemDropEvent].
func (i *ItemActor) SetView(view func(p *Player, v *ItemView)) {
	i.view = view
	if i.entity != nil {
		i.entity.SetSpawnFor(i.spawnForFunc())
		i.entity.Respawn()
	}
}

// SetStack changes the real item, for everyone who sees it, and it is saved
// with it. An empty stack removes the item. Runs: owner of the item's entity,
// or in an [ItemDropEvent].
func (i *ItemActor) SetStack(s ItemStack) {
	s.netID = 0
	same := s.stacksWith(i.stack)
	i.stack = s
	if i.entity == nil {
		return
	}
	switch {
	case s.Empty():
		i.taken = true
		i.entity.Remove()
	case same && i.view == nil:
		i.sendStackSize()
	default:
		i.entity.Respawn()
	}
}

// Refresh shows the item again to the players who see it, for a view that now
// shows another Stack. Runs: owner of the item's entity.
func (i *ItemActor) Refresh() {
	if i.entity != nil {
		i.entity.Respawn()
	}
}

// CanSee reports whether the item's visibility rule lets p see it.
func (i *ItemActor) CanSee(p *Player) bool {
	if i.rule == nil {
		return true
	}
	defer func() {
		if r := recover(); r != nil {
			i.s.log.Error("item visibility rule panicked", "panic", r)
		}
	}()
	return i.rule(p)
}

// setApart reports whether a plugin keeps this item from merging with o: a
// visibility rule on either, so a private item never joins a public one, or a
// different lifetime. A view only changes what players are shown, so it does not
// keep items apart; the item that stays keeps its view.
func (i *ItemActor) setApart(o *ItemActor) bool {
	return i.rule != nil || o.rule != nil || i.lifetime != o.lifetime
}

// visibleFunc is the rule for the world, nil when everyone sees the item.
func (i *ItemActor) visibleFunc() func(*world.Entity) bool {
	if i.rule == nil {
		return nil
	}
	return func(seer *world.Entity) bool {
		p, ok := seer.Owner().(*Player)
		return ok && i.CanSee(p)
	}
}

// spawnForFunc is the view for the world, nil for the shared spawn packets.
func (i *ItemActor) spawnForFunc() func(*world.Entity) []packet.Packet {
	if i.view == nil {
		return nil
	}
	return func(seer *world.Entity) []packet.Packet {
		return []packet.Packet{i.addPacket(i.entity, i.viewFor(seer))}
	}
}

// viewFor builds what the player behind seer is shown.
func (i *ItemActor) viewFor(seer *world.Entity) ItemView {
	v := ItemView{Stack: i.stack}
	p, ok := seer.Owner().(*Player)
	if !ok || i.view == nil {
		return v
	}
	defer func() {
		if r := recover(); r != nil {
			i.s.log.Error("item view panicked", "panic", r)
		}
	}()
	i.view(p, &v)
	return v
}

// addPacket shows the item as v says.
func (i *ItemActor) addPacket(e *world.Entity, v ItemView) packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyWidth:  float32(2 * itemHalfSize),
		protocol.EntityDataKeyHeight: float32(2 * itemHalfSize),
		protocol.EntityDataKeyScale:  float32(1),
	}
	maps.Copy(meta, nameMetadata(v.Name))
	return &packet.AddItemActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), Item: v.Stack.network(i.s.reg),
		Position: e.Position(), Velocity: i.vel, EntityMetadata: meta,
	}
}

// sendViews updates each seer's name tag once a second.
func (i *ItemActor) sendViews(e *world.Entity) {
	if i.view == nil || i.age%itemViewInterval != 0 {
		return
	}
	e.EachSeer(func(seer *world.Entity) {
		seer.SendTo(actorData(e.RuntimeID(), nameMetadata(i.viewFor(seer).Name)))
	})
}

// nameMetadata is the actor flags and name of an item with this name tag.
func nameMetadata(name string) protocol.EntityMetadata {
	if name == "" {
		return protocol.EntityMetadata{protocol.EntityDataKeyFlags: int64(itemFlags), protocol.EntityDataKeyName: "", protocol.EntityDataKeyAlwaysShowNameTag: uint8(0)}
	}
	return protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:             int64(itemFlags | 1<<protocol.EntityDataFlagShowName | 1<<protocol.EntityDataFlagAlwaysShowName),
		protocol.EntityDataKeyName:              name,
		protocol.EntityDataKeyAlwaysShowNameTag: uint8(1),
	}
}

// sendStackSize tells the players who see the item its new count.
func (i *ItemActor) sendStackSize() {
	i.entity.Send(actorEventData(i.entity.RuntimeID(), packet.ActorEventUpdateStackSize, int32(i.stack.Count)))
}
