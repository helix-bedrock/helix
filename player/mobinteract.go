package player

import (
	"encoding/json"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// A player using an item on a mob: the mob's minecraft:interact (the entity library picks
// the interaction, starts its cooldown and fires on_interact, so a sheep turns sheared), then
// the item work the interaction's JSON asks for, which is the engine's. Shearing sheep, snow
// golems, bogged and mooshrooms all go through here.
//
// Done of the item work: hurt_item, use_item, spawn_items, play_sounds, swing. Not done:
// transform_to_item, add_items, equip_item_slot, drop_item_slot, health_amount, particles,
// vibration (game events) and interact_text, and BDS's own numbers for where spawned items
// appear (here: at the mob, as its death drops).

// interaction is the item work of one minecraft:interact interaction.
type interaction struct {
	UseItem    bool            `json:"use_item"`
	HurtItem   int32           `json:"hurt_item"`
	Swing      bool            `json:"swing"`
	PlaySounds json.RawMessage `json:"play_sounds"`
	SpawnItems *struct {
		Table string `json:"table"`
	} `json:"spawn_items"`
}

// sounds reads play_sounds, one name or a list.
func (it *interaction) sounds() []string {
	if len(it.PlaySounds) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(it.PlaySounds, &one) == nil {
		return []string{one}
	}
	var list []string
	_ = json.Unmarshal(it.PlaySounds, &list)
	return list
}

// interactMob runs the player's interaction with the mob, if one applies.
func (p *Player) interactMob(o *Mob, d *protocol.UseItemOnEntityTransactionData) {
	if !p.selectSlot(d.HotBarSlot, d.HeldItem.Stack.NetworkID) {
		p.sendHeld()
		p.flush()
		return
	}
	r := p.entity.Region()
	if r == nil || p.entity.Position().Sub(o.entity.Position()).Len() > attackRangeCreative {
		return
	}
	raw, ok := o.m.Interact(actorOf(p.entity))
	if !ok {
		return
	}
	var it interaction
	if err := json.Unmarshal(raw, &it); err != nil {
		p.s.log.Debug("mob interact", "err", err)
		return
	}
	at := o.entity.Position()
	if it.SpawnItems != nil && it.SpawnItems.Table != "" && p.s.entityLoot != nil {
		for _, d := range p.s.entityLoot.Roll(it.SpawnItems.Table, o.lootContext(r), nil) {
			for _, st := range p.s.lootStacks(d) {
				p.s.spawnMobDrop(r, st, at)
			}
		}
	}
	for _, name := range it.sounds() {
		playSound(r, feetBlock(at), soundEvent{name: name, pos: at, extra: -1, entity: o.m.Type()}, nil)
	}
	if it.HurtItem > 0 && !p.creative() {
		p.hurtHeld(it.HurtItem)
	}
	if it.UseItem {
		p.useUpHeld()
	}
	if it.Swing {
		r.Broadcast(p.blockPos(), &packet.Animate{
			ActionType: packet.AnimateActionSwingArm, EntityRuntimeID: p.runtimeID, SwingSource: packet.AnimateSwingSourceInteract,
		}, p.viewer)
	}
	p.flush()
}

// feetBlock is the block a position is in.
func feetBlock(v mgl32.Vec3) cube.Pos {
	return cube.Pos{int(math.Floor(float64(v[0]))), int(math.Floor(float64(v[1]))), int(math.Floor(float64(v[2])))}
}
