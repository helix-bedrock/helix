package player

import (
	"encoding/json"
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// A player using an item on a mob: the mob's minecraft:interact (the entity library picks
// the interaction, starts its cooldown and fires on_interact, so a sheep turns sheared), then
// the item work the interaction's JSON asks for, which is the engine's. Shearing sheep, snow
// golems, bogged and mooshrooms, and milking cows, goats and mooshrooms, all go through
// here; a mob a bucket takes is taken first (mobbucket.go). transform_to_item gives its item
// as a bucket fills (takeFilled); how BDS's interaction hands it over was not read.
//
// Done of the item work: hurt_item, use_item, transform_to_item, spawn_items, play_sounds,
// swing. Not done: add_items, equip_item_slot, drop_item_slot, health_amount, particles,
// vibration (game events) and interact_text.
//
// Spawned items are BDS 1.26.60's (the interact runtime 0x71FABF0): each item of the table
// appears y_offset above the mob's feet, with the item's own spawn speed (ItemActor's
// constructor 0xBD61040: up 0.2, sideways up to 0.1 each way) plus a push of (r1-r2)*0.1,
// r3*0.05, (r4-r5)*0.1, and a 10 tick pick-up delay. y_offset's default of 1 is Mojang's
// documented default, not read from BDS.

// interaction is the item work of one minecraft:interact interaction.
type interaction struct {
	UseItem bool `json:"use_item"`
	// TransformToItem is what the used item becomes, as "name:aux".
	TransformToItem string          `json:"transform_to_item"`
	HurtItem        int32           `json:"hurt_item"`
	Swing           bool            `json:"swing"`
	PlaySounds      json.RawMessage `json:"play_sounds"`
	SpawnItems      *struct {
		Table   string   `json:"table"`
		YOffset *float32 `json:"y_offset"`
	} `json:"spawn_items"`
}

// The spawned items' numbers.
const (
	// spawnItemsYOffset is y_offset's default.
	spawnItemsYOffset = 1
	// spawnPushSide and spawnPushUp scale the push each spawned item gets.
	spawnPushSide = 0.1
	spawnPushUp   = 0.05
)

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
	if p.nameMob(o) {
		return
	}
	if p.bucketMob(r, o) {
		return
	}
	raw, ok := o.m.Interact(actorOf(p.entity))
	if !ok {
		// BDS's order after minecraft:interact: ageable, breedable, leashable.
		if !p.feedMob(o) {
			p.leashMob(o)
		}
		return
	}
	var it interaction
	if err := json.Unmarshal(raw, &it); err != nil {
		p.s.log.Debug("mob interact", "err", err)
		return
	}
	p.runInteraction(r, o, it)
	p.flush()
}

// runInteraction does the item work of a minecraft:interact that applied: spawn_items,
// play_sounds, hurt_item, transform_to_item or use_item, and swing.
func (p *Player) runInteraction(r *world.Region, o *Mob, it interaction) {
	at := o.entity.Position()
	p.spawnInteractItems(r, o, it)
	for _, name := range it.sounds() {
		playSound(r, feetBlock(at), soundEvent{name: name, pos: at, extra: -1, entity: o.m.Type()}, nil)
	}
	if it.HurtItem > 0 && !p.creative() {
		p.hurtHeld(it.HurtItem)
	}
	switch name, ok := itemByAux(it.TransformToItem); {
	case it.TransformToItem != "" && ok:
		p.takeFilled(ItemStack{Name: name, Count: 1, netID: p.s.stackIDs.Add(1)})
	case it.UseItem:
		p.useUpHeld()
	}
	if it.Swing {
		r.Broadcast(p.blockPos(), &packet.Animate{
			ActionType: packet.AnimateActionSwingArm, EntityRuntimeID: p.runtimeID, SwingSource: packet.AnimateSwingSourceInteract,
		}, p.viewer)
	}
}

// spawnInteractItems rolls the interaction's spawn_items table and throws the items out
// above the mob.
func (p *Player) spawnInteractItems(r *world.Region, o *Mob, it interaction) {
	if it.SpawnItems == nil || it.SpawnItems.Table == "" || p.s.entityLoot == nil {
		return
	}
	y := float32(spawnItemsYOffset)
	if it.SpawnItems.YOffset != nil {
		y = *it.SpawnItems.YOffset
	}
	from := o.entity.Position().Add(mgl32.Vec3{0, y, 0})
	for _, d := range p.s.entityLoot.Roll(it.SpawnItems.Table, o.lootContext(r), nil) {
		for _, st := range p.s.lootStacks(d) {
			p.s.spawnInteractDrop(r, st, from)
		}
	}
}

// spawnInteractDrop throws out one item of an interaction's spawn_items at from: the item's
// spawn speed plus the interaction's push.
func (s *Host) spawnInteractDrop(r *world.Region, it ItemStack, from mgl32.Vec3) {
	vel := mgl32.Vec3{
		dropDrift() + (rand.Float32()-rand.Float32())*spawnPushSide, //nolint:gosec // an item's scatter, not a secret
		dropUp + rand.Float32()*spawnPushUp,                         //nolint:gosec // an item's scatter, not a secret
		dropDrift() + (rand.Float32()-rand.Float32())*spawnPushSide, //nolint:gosec // an item's scatter, not a secret
	}
	s.spawnItem(r.Dimension(), s.freshStack(it), from, vel, world.Rotation{}, mobDropDelay, 0)
}

// feetBlock is the block a position is in.
func feetBlock(v mgl32.Vec3) cube.Pos {
	return cube.Pos{int(math.Floor(float64(v[0]))), int(math.Floor(float64(v[1]))), int(math.Floor(float64(v[2])))}
}

// nameTagItem is the name tag.
const nameTagItem = "minecraft:name_tag"

// nameMob is a renamed name tag used on a mob, NameableComponent's interaction (1.26.60
// getInteraction 0x7139820, its callback 0x72009D0): the mob takes the tag's custom name
// (nameEntity: name, flag, persistent, name actions) and the player uses the item up
// (Player::useItem, so not in creative). A name tag without a custom name does nothing, as
// does a mob without minecraft:nameable. True if the mob was named.
func (p *Player) nameMob(o *Mob) bool {
	held := p.inventory.Held()
	if held.Name != nameTagItem || o.gone || o.m.Dead() {
		return false
	}
	name := held.CustomName()
	if name == "" || !o.m.Name(name) {
		return false
	}
	p.useUpHeld()
	p.flush()
	return true
}
