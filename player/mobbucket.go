package player

import (
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Buckets that hold a mob, powder snow, and milk, from BDS 1.26.60's BucketItem.
//
// Its mob table (0xFB618C8) names each mob bucket's mob and whether it is a water animal,
// poured with water, or a land animal (the sulfur cube), poured alone. A water bucket takes
// a water animal and an empty bucket the sulfur cube (PlayerInteractionSystem's bucketable
// mapping, 0x73754F0); a baby sulfur cube stays (0xC9B2370, which also refuses one with a
// component not named here). The mob goes, its saved data goes in the bucket (less Motion,
// FallDistance, OnGround, Air, HurtTime, PortalCooldown, ActiveEffects and TargetID), the
// fill sound plays at it, and the held bucket becomes the full one; in creative the full one
// is added to the inventory if it is not there, and the held bucket stays.
//
// Emptying (_emptyBucket, 0xC99EC80) spawns the mob from that data at the middle of the
// block's floor, then pours water for a water animal. Powder snow goes where its block may
// be placed. Mobs are not saved by Helix yet, so the data is what Helix has of one: its
// definition group stack and health, in BDS's keys. Not done: the display strings BDS adds
// for the item's name (axolotl ColorID and BodyID, tropical fish GroupName), names (mobs
// have none yet), the bucket a fish achievement, and the empty sounds of mob buckets, which
// are taken as bucket.empty.fish and bucket.empty.land_animal (not read where BDS plays them).
//
// Milk (useTimeDepleted, 0xC999F60) is drunk in 32 ticks like every bucket's use time; it
// clears every effect and, outside creative, leaves an empty bucket. While it is drunk the
// eating event goes out as for food, which the client plays the drink sound for (taken to
// be the same for milk as for food, not read).

// mobBucket is one entry of BDS's mob bucket table.
type mobBucket struct {
	item, mob string
	// water is set for a water animal, poured with water and taken with a water bucket.
	water bool
	// event is what a mob poured from a bucket with no data starts with, "" for spawned.
	event string
}

var mobBuckets = [...]mobBucket{
	{item: "minecraft:cod_bucket", mob: "minecraft:cod", water: true},
	{item: "minecraft:salmon_bucket", mob: "minecraft:salmon", water: true},
	{item: "minecraft:tropical_fish_bucket", mob: "minecraft:tropicalfish", water: true},
	{item: "minecraft:pufferfish_bucket", mob: "minecraft:pufferfish", water: true},
	{item: "minecraft:axolotl_bucket", mob: "minecraft:axolotl", water: true},
	{item: "minecraft:tadpole_bucket", mob: "minecraft:tadpole", water: true},
	{item: "minecraft:sulfur_cube_bucket", mob: "minecraft:sulfur_cube", event: "minecraft:spawn_medium"},
}

// bucketByAux is BDS's BucketFillType, the aux value of the old bucket item, which
// interactions still name ("bucket:1" is milk).
var bucketByAux = map[int]string{
	0: emptyBucket, 1: nameMilkBucket, 2: "minecraft:cod_bucket", 3: "minecraft:salmon_bucket",
	4: "minecraft:tropical_fish_bucket", 5: "minecraft:pufferfish_bucket", 8: "minecraft:water_bucket",
	10: "minecraft:lava_bucket", 11: namePowderSnowBucket, 12: "minecraft:axolotl_bucket",
	13: "minecraft:tadpole_bucket", 14: "minecraft:sulfur_cube_bucket",
}

const (
	nameMilkBucket       = "minecraft:milk_bucket"
	namePowderSnowBucket = "minecraft:powder_snow_bucket"
	namePowderSnow       = "minecraft:powder_snow"
	nameWaterBucket      = "minecraft:water_bucket"
	// bucketUseTicks is every bucket's use time; only milk is used.
	bucketUseTicks = 32
	// The keys of a mob's data in its bucket.
	bucketDefinitions = "definitions"
	bucketIdentifier  = "identifier"
	bucketAttributes  = "Attributes"
	attrHealthName    = "minecraft:health"
)

// mobBucketItem is the table entry of a mob bucket item.
func mobBucketItem(name string) (mobBucket, bool) {
	for _, b := range mobBuckets {
		if b.item == name {
			return b, true
		}
	}
	return mobBucket{}, false
}

// mobBucketOf is the table entry for a mob type.
func mobBucketOf(typ string) (mobBucket, bool) {
	for _, b := range mobBuckets {
		if b.mob == typ {
			return b, true
		}
	}
	return mobBucket{}, false
}

// itemByAux resolves an interaction's "name:aux" item, as old item names with aux values
// still are. Only the bucket's aux values name other items now.
func itemByAux(s string) (string, bool) {
	name, auxText, hasAux := strings.Cut(s, ":")
	if name == "minecraft" {
		name, auxText, hasAux = strings.Cut(auxText, ":")
	}
	aux := 0
	if hasAux {
		n, err := strconv.Atoi(auxText)
		if err != nil {
			return "", false
		}
		aux = n
	}
	if name == "bucket" {
		it, ok := bucketByAux[aux]
		return it, ok
	}
	if aux != 0 {
		return "", false
	}
	return "minecraft:" + name, true
}

// bucketMob takes the mob into the held bucket if it is one a bucket takes: true if it was
// taken. Runs: the player's region.
func (p *Player) bucketMob(r *world.Region, o *Mob) bool {
	b, ok := mobBucketOf(o.Type())
	if !ok || o.Dead() || o.gone {
		return false
	}
	held := p.inventory.Held()
	if held.Meta != 0 || b.water && held.Name != nameWaterBucket || !b.water && (held.Name != emptyBucket || o.m.Flag(behavior.FlagBaby)) {
		return false
	}
	full := ItemStack{Name: b.item, Count: 1, Tag: o.bucketData(), netID: p.s.stackIDs.Add(1)}
	at := o.entity.Position()
	o.remove(o.entity)
	sound := packet.SoundEventBucketFillFish
	if !b.water {
		sound = packet.SoundEventBucketFillLandAnimal
	}
	playSound(r, feetBlock(at), soundEvent{name: sound, pos: blockVec(feetBlock(at)), extra: -1}, nil)
	r.Broadcast(p.blockPos(), &packet.Animate{
		ActionType: packet.AnimateActionSwingArm, EntityRuntimeID: p.runtimeID, SwingSource: packet.AnimateSwingSourceInteract,
	}, p.viewer)
	p.takeFilled(full)
	return true
}

// takeFilled is the held bucket becoming full: replaced when it is the only one, else one
// is used and the full one given (thrown when there is no room). In creative the full one
// is only added, if the inventory has none.
func (p *Player) takeFilled(full ItemStack) {
	if p.creative() {
		if !p.inventory.has(full.Name) {
			p.Give(full)
		}
		p.flush()
		return
	}
	inv := &p.inventory
	if inv.main[inv.held].Count <= 1 {
		inv.main[inv.held] = full
		p.sendHeld()
		p.sendEquipment()
		p.flush()
		return
	}
	p.useHeld()
	if rest := p.Give(full); !rest.Empty() {
		p.s.game.sys.DropItem(p, rest, false)
	}
	p.flush()
}

// bucketData is the mob's data as its bucket keeps it.
func (o *Mob) bucketData() map[string]any {
	stack := o.m.Stack()
	defs := make([]any, 0, len(stack)+1)
	defs = append(defs, "+"+o.Type())
	for _, e := range stack {
		sign := "-"
		if e.Added {
			sign = "+"
		}
		defs = append(defs, sign+e.Group)
	}
	return map[string]any{
		bucketIdentifier:  o.Type(),
		bucketDefinitions: defs,
		bucketAttributes: []any{map[string]any{
			"Name": attrHealthName, "Current": o.m.HealthValue(), "Max": o.m.MaxHealth(),
		}},
	}
}

// restoreBucketData reads what bucketData wrote: the group stack after the base and the
// health, 0 for none.
func restoreBucketData(tag map[string]any) ([]definition.StackEntry, float32) {
	var stack []definition.StackEntry
	if defs, ok := tag[bucketDefinitions].([]any); ok {
		for i, d := range defs {
			s, ok := d.(string)
			if !ok || i == 0 || len(s) < 2 {
				continue
			}
			stack = append(stack, definition.StackEntry{Group: s[1:], Added: s[0] == '+'})
		}
	}
	var health float32
	if attrs, ok := tag[bucketAttributes].([]any); ok {
		for _, a := range attrs {
			if m, ok := a.(map[string]any); ok && m["Name"] == attrHealthName {
				health = nbtFloat(m["Current"])
			}
		}
	}
	return stack, health
}

// pourMobBucket empties a mob bucket: the mob comes out at the middle of the block's floor,
// with water for a water animal. False if nothing was poured.
func (p *Player) pourMobBucket(clicked cube.Pos, face int32, held ItemStack, b mobBucket) bool {
	r := p.entity.Region()
	if r == nil || !p.inReach(clicked) {
		return false
	}
	var pos cube.Pos
	sound := packet.SoundEventBucketEmptyFish
	if b.water {
		at, _, ok := p.pourLiquid(r, clicked, face, bucketLiquids[nameWaterBucket], pourFish)
		if !ok {
			return false
		}
		pos = at
	} else {
		at, ok := p.landTarget(r, clicked, face)
		if !ok {
			return false
		}
		pos, sound = at, packet.SoundEventBucketEmptyLandAnimal
	}
	p.s.spawnBucketMob(r, b, held, pos)
	playSound(r, pos, soundEvent{name: sound, pos: blockCenter(pos), extra: -1}, nil)
	p.emptyHeldBucket()
	return true
}

// landTarget is where a land animal comes out of its bucket: bucketTarget, which must be air
// or not of solid material.
func (p *Player) landTarget(r *world.Region, clicked cube.Pos, face int32) (cube.Pos, bool) {
	blocks := p.s.reg.Blocks
	pos := p.bucketTarget(r, clicked, face, pourLand)
	if b, ok := r.Block(pos, 0); !ok || b != blocks.Air() && blocks.Properties(b).SolidMaterial {
		return cube.Pos{}, false
	}
	return pos, true
}

// spawnBucketMob is BDS's mob out of a bucket (0xC99A3F0): from the bucket's data if it has
// any, else a new one (with the table's event), at the middle of the block's floor.
func (s *Host) spawnBucketMob(r *world.Region, b mobBucket, held ItemStack, pos cube.Pos) {
	at := blockVec(pos).Add(mgl32.Vec3{0.5, 0, 0.5})
	if _, ok := held.Tag[bucketDefinitions]; ok {
		o, ok := s.newMob(b.mob, at, 0, definition.InitLoaded, "")
		if !ok {
			return
		}
		o.m.Restore(restoreBucketData(held.Tag))
		o.add(r.Dimension())
		return
	}
	method := definition.InitSpawned
	if b.event != "" {
		method = definition.InitEvent
	}
	if o, ok := s.newMob(b.mob, at, 0, method, b.event); ok {
		o.add(r.Dimension())
	}
}

// pourPowderSnow empties a powder snow bucket at bucketTarget, if powder snow may go there
// (BDS asks the block's mayPlace; Helix takes it as the block there being built over).
// False if it may not.
func (p *Player) pourPowderSnow(clicked cube.Pos, face int32) bool {
	r := p.entity.Region()
	if r == nil || !p.inReach(clicked) {
		return false
	}
	blocks := p.s.reg.Blocks
	snow, ok := blocks.Block(namePowderSnow)
	if !ok {
		return false
	}
	pos := p.bucketTarget(r, clicked, face, pourLiquid)
	target, ok := r.Block(pos, 0)
	if !ok || !p.s.game.replaceable(p, r, pos, target, snow) {
		return false
	}
	if target != blocks.Air() && blocks.Properties(target).LiquidReaction == registry.LiquidPopped {
		p.s.PopBlock(r, pos, target)
	}
	if !r.SetBlock(pos, 0, snow) {
		return false
	}
	r.UpdateNeighbours(pos)
	playSound(r, pos, soundEvent{name: packet.SoundEventBucketEmptyPowderSnow, pos: blockCenter(pos), extra: -1}, nil)
	p.emptyHeldBucket()
	return true
}

// drinkMilk is milk drunk: every effect goes, and outside creative the milk becomes an
// empty bucket.
func (p *Player) drinkMilk() {
	for _, e := range p.Effects() {
		p.RemoveEffect(e.ID)
	}
	if !p.creative() {
		p.useHeld()
		left := ItemStack{Name: emptyBucket, Count: 1, netID: p.s.stackIDs.Add(1)}
		if rest := p.Give(left); !rest.Empty() {
			p.s.game.sys.DropItem(p, rest, false)
		}
	}
	p.flush()
}

// has reports whether any inventory slot holds the item.
func (inv *playerItems) has(name string) bool {
	for _, s := range inv.main {
		if !s.Empty() && s.Name == name {
			return true
		}
	}
	return false
}
