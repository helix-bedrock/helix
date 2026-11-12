package player

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/mob"
)

// Breeding, feeding and leashes on the engine side: the mob package decides (breed.go),
// this makes the babies, drops the experience and the leads, and runs a player's feed and
// lead clicks. Leash knots on fences are in leashknot.go.

// leadItem is the lead.
const leadItem = "minecraft:lead"

// flagTamed is BDS's tamed actor flag, which a born baby takes from a tame parent.
const flagTamed = 28

// Born makes a baby beside its parent with minecraft:entity_born, persistent as
// createBornActor (1.26.60 0xBFF2400) makes every baby, and tame when the birth says so.
// Its owner is not set: mob owners are not hooked up yet.
func (m *Mob) Born(_ *mob.Mob, b state.Birth) behavior.Actor {
	r := m.entity.Region()
	if r == nil || b.Baby == "" {
		return nil
	}
	_, yaw, _, _ := m.m.Rotation()
	baby, ok := m.s.newMob(b.Baby, m.entity.Position(), yaw, definition.InitBorn, "")
	if !ok {
		return nil
	}
	baby.m.SetPersistent()
	if b.Tame {
		baby.m.SetFlag(flagTamed, true)
	}
	baby.add(r.Dimension())
	return baby.m.Snapshot()
}

// Experience drops experience orbs at the mob.
func (m *Mob) Experience(_ *mob.Mob, amount int32) {
	if r := m.entity.Region(); r != nil {
		m.s.SpawnExperience(r, m.entity.Position(), amount)
	}
}

// LeashDropped drops the lead at the mob when its leash came off with one.
func (m *Mob) LeashDropped(_ *mob.Mob, dropLead bool) {
	r := m.entity.Region()
	if r == nil || !dropLead {
		return
	}
	m.s.spawnMobDrop(r, ItemStack{Name: leadItem, Count: 1}, m.entity.Position())
}

// feedMob is the ageable and breedable part of a player's use on a mob: the mob takes the
// held item, which is used up (not in creative) or turns into the component's
// transform_to_item. True if the mob took it.
func (p *Player) feedMob(o *Mob) bool {
	held := p.inventory.Held()
	if held.Empty() {
		return false
	}
	result, ok := o.m.Feed(actorOf(p.entity), mob.HeldItem{Name: held.Name})
	if !ok {
		return false
	}
	if name, isItem := itemByAux(result); result != "" && isItem {
		p.takeFilled(ItemStack{Name: name, Count: 1, netID: p.s.stackIDs.Add(1)})
	} else {
		p.useUpHeld()
	}
	p.flush()
	return true
}

// leashMob is the leashable part of a player's use on a mob: a lead ties an unleashed (or
// stealable) mob to the player and is used up outside creative; the player holding a mob's
// leash takes it off, and the lead drops.
func (p *Player) leashMob(o *Mob) bool {
	held := p.inventory.Held()
	a, ok := o.m.LeashInteract(actorOf(p.entity), mob.HeldItem{Name: held.Name})
	if !ok {
		return false
	}
	if a == state.LeashAttach {
		p.useUpHeld()
	}
	p.flush()
	return true
}
