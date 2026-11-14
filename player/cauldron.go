package player

import (
	"maps"
	"slices"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Cauldrons as BDS 1.26.60 runs them: the cauldron's use 0xCB4AAE0 (CauldronBlock::use in
// 1.21.3's names), its level setter 0xCB4DC30, its level event 0xCB4DF60, the dye mix
// 0xD44CE40/0xD44CC90 and the block actor's save. The research note is private.
//
// The block holds the liquid and its level (fill_level 0 to 6, cauldron_liquid); the block
// entity holds a potion (an ID, -1 for none, and the potion item it came from) and a dye
// colour mixed into water. Every click on a cauldron with something in hand is taken, as BDS's
// use returns true on every path.
const (
	nameCauldron      = "minecraft:cauldron"
	cauldronEntityID  = "Cauldron"
	cauldronLevelKey  = "fill_level"
	cauldronLiquidKey = "cauldron_liquid"
	// The tags BDS saves a cauldron with.
	cauldronPotionTag = "PotionId"
	cauldronTypeTag   = "PotionType"
	cauldronColorTag  = "CustomColor"
	cauldronItemsTag  = "Items"

	cauldronFull = 6
	// cauldronBottle is what a bottle takes or gives, in levels.
	cauldronBottle = 2
	// arrowsPerLevel is how many arrows one level of a potion tips.
	arrowsPerLevel = 16
	// firstTippablePotion is the first potion ID arrows take (BDS's check potion >= 5): water,
	// mundane, long mundane, thick and awkward potions tip nothing.
	firstTippablePotion = 5
	// lastPlainPotion is the awkward potion's ID; bottles up to it are not kept in a cauldron.
	lastPlainPotion = 4

	// cauldronWaterColour is BDS's CauldronBlockActor::WATER_COLOR (0.2065, 0.3185, 0.35,
	// alpha 0.75) as the ARGB its level events carry.
	cauldronWaterColour uint32 = 0xBF345159
	// customColorTag is the item tag a dyed armour piece keeps its colour in.
	customColorTag = "customColor"

	nameLavaBucket       = "minecraft:lava_bucket"
	nameUndyedShulkerBox = "minecraft:undyed_shulker_box"
	nameWolfArmor        = "minecraft:wolf_armor"
	nameLeatherHorse     = "minecraft:leather_horse_armor"
	tagArmor             = "minecraft:is_armor"
	tagHorseArmor        = "minecraft:horse_armor"
	tagLeatherTier       = "minecraft:leather_tier"

	// Game events a cauldron posts (BDS's GameEventRegistry fluid_pickup and fluid_place).
	gameEventFluidPickup int32 = 29
	gameEventFluidPlace  int32 = 30
)

// The liquids a cauldron holds, as its cauldron_liquid state names them.
const (
	cauldronWater      = "water"
	cauldronLava       = "lava"
	cauldronPowderSnow = "powder_snow"
)

// cauldronDyeColours is BDS's dye colour table the cauldron mixes with (0xFC02528), RGB by
// dye colour (black 0 to white 15). Black is not the sign table's pure black.
var cauldronDyeColours = [16]uint32{
	0x1d1d21, 0xb02e26, 0x5e7c16, 0x835432, 0x3c44aa, 0x8932b8, 0x169c9c, 0x9d9d97,
	0x474f52, 0xf38baa, 0x80c71f, 0xfed83d, 0x3ab3da, 0xc74ebd, 0xf9801d, 0xf0f0f0,
}

// potionItems are the potion items by the potion type a cauldron saves.
var potionItemsByType = [...]string{namePotion, nameSplashPotion, nameLingeringPotion}

// Cauldron is a cauldron's block entity.
type Cauldron struct {
	s     *Host
	saved map[string]any
	// potion is the potion in it, -1 for none; potionType the item it came in (0 drinkable,
	// 1 splash, 2 lingering).
	potion     int32
	potionType int16
	// colour is the dye mixed into its water, ARGB, when dyed is set.
	colour uint32
	dyed   bool
	chunk  world.ChunkHandle
}

// newCauldron is the block entity of a cauldron just placed.
func (s *Host) newCauldron() *Cauldron { return &Cauldron{s: s, potion: -1} }

// loadCauldron brings back a saved cauldron.
func (s *Host) loadCauldron(saved map[string]any) *Cauldron {
	c := &Cauldron{s: s, saved: maps.Clone(saved), potion: -1}
	if id, ok := saved[cauldronPotionTag].(int16); ok {
		c.potion = int32(id)
	}
	c.potionType, _ = saved[cauldronTypeTag].(int16)
	if col, ok := saved[cauldronColorTag].(int32); ok {
		c.colour, c.dyed = uint32(col), true //nolint:gosec // ARGB bits
	}
	for _, k := range [...]string{cauldronPotionTag, cauldronTypeTag, cauldronColorTag} {
		delete(c.saved, k)
	}
	return c
}

// BindBlockEntity keeps the chunk the cauldron is in.
func (c *Cauldron) BindBlockEntity(h world.ChunkHandle) { c.chunk = h }

// KeepBlockEntity keeps the potion and the colour while the level changes.
func (c *Cauldron) KeepBlockEntity(block uint32) bool {
	return c.s.reg.Blocks.Name(block) == nameCauldron
}

// SaveData is what BDS saves of a cauldron: the potion, its type and the colour when dyed.
func (c *Cauldron) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(c.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = cauldronEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	if _, ok := m[cauldronItemsTag]; !ok {
		m[cauldronItemsTag] = []any{}
	}
	m[cauldronPotionTag] = int16(c.potion) //nolint:gosec // potion IDs fit
	m[cauldronTypeTag] = c.potionType
	if c.dyed {
		m[cauldronColorTag] = int32(c.colour) //nolint:gosec // ARGB bits
	}
	return m
}

// NetworkData is what is saved: the client colours the liquid from it.
func (c *Cauldron) NetworkData(pos cube.Pos) map[string]any { return c.SaveData(pos) }

// Potion returns the potion in the cauldron, -1 for none.
func (c *Cauldron) Potion() int32 { return c.potion }

// Colour returns the dye colour of its water (ARGB), and whether it is dyed.
func (c *Cauldron) Colour() (uint32, bool) { return c.colour, c.dyed }

// changed marks it for saving and tells the clients.
func (c *Cauldron) changed(r *world.Region, pos cube.Pos) {
	c.chunk.Changed()
	r.SendBlockEntity(pos)
}

// clear takes out the potion and the dye.
func (c *Cauldron) clear() {
	c.potion, c.dyed, c.colour = -1, false, 0
}

// shownColour is BDS's getColor: the potion's colour, else the dye, else water's.
func (c *Cauldron) shownColour() uint32 {
	switch {
	case c.potion >= 0:
		pot, _ := c.s.Potion(c.potion)
		return c.s.potionColour(pot.Effects)
	case c.dyed:
		return c.colour
	}
	return cauldronWaterColour
}

// mixDye is BDS's mixDyes with one dye added: the colour it had counts once, the dye once,
// and the mix is their average, each channel cut down to a whole step.
func (c *Cauldron) mixDye(dye uint8) {
	var r, g, b, n float32
	if c.dyed {
		r, g, b, n = channel(c.colour, 16), channel(c.colour, 8), channel(c.colour, 0), 1
	}
	rgb := cauldronDyeColours[dye&colorMask]
	r, g, b, n = r+channel(rgb, 16), g+channel(rgb, 8), b+channel(rgb, 0), n+1
	toByte := func(v float32) uint32 { return uint32(min(max(v/n, 0), 1) * 0xFF) }
	c.colour = 0xFF000000 | toByte(r)<<16 | toByte(g)<<8 | toByte(b)
	c.dyed = true
}

// channel is one colour channel of an RGB value, from 0 to 1.
func channel(rgb uint32, shift uint) float32 { return float32(rgb>>shift&0xFF) / 0xFF }

// cauldronAt is the cauldron's block entity at pos.
func cauldronAt(r *world.Region, pos cube.Pos) (*Cauldron, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	c, ok := be.(*Cauldron)
	return c, ok
}

// cauldronState is the cauldron's level and liquid.
func (s *Host) cauldronState(block uint32) (int32, string) {
	states := s.reg.Blocks.States(block)
	level, _ := states[cauldronLevelKey].(int32)
	liquid, _ := states[cauldronLiquidKey].(string)
	return level, liquid
}

// setCauldronLevel is BDS's setLiquidLevel: the level, kept to 0 to 6, and the liquid, with the
// neighbours told; an empty cauldron loses its potion.
func (s *Host) setCauldronLevel(r *world.Region, pos cube.Pos, c *Cauldron, level int32, liquid string) {
	level = min(max(level, 0), cauldronFull)
	id, ok := s.reg.Blocks.StateID(nameCauldron, map[string]any{cauldronLevelKey: level, cauldronLiquidKey: liquid})
	if ok && r.SetBlock(pos, 0, id) {
		r.UpdateNeighbours(pos)
	}
	if level == 0 {
		c.potion = -1
	}
	c.changed(r, pos)
}

// cauldronEvent is BDS's _spawnCauldronEvent: the level event at the liquid's surface,
// carrying the colour shown.
func (s *Host) cauldronEvent(r *world.Region, pos cube.Pos, c *Cauldron, event int32) {
	b, _ := r.Block(pos, 0)
	level, _ := s.cauldronState(b)
	at := mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + float32(level)*0.125 + 0.375, float32(pos[2]) + 0.5}
	levelEvent(r, pos, at, event, int32(c.shownColour())) //nolint:gosec // ARGB bits
}

// explodeCauldron is BDS's _explodeCauldronContents: two liquids that do not mix empty it.
func (s *Host) explodeCauldron(r *world.Region, pos cube.Pos, c *Cauldron) {
	s.setCauldronLevel(r, pos, c, 0, cauldronWater)
	s.cauldronEvent(r, pos, c, packet.LevelEventCauldronExplode)
}

// cauldronUse is a click on a cauldron with what it holds read once.
type cauldronUse struct {
	p      *Player
	r      *world.Region
	pos    cube.Pos
	c      *Cauldron
	held   ItemStack
	level  int32
	liquid string
	// water is water with no potion in it, potion a potion, as BDS's use reads them.
	water, potion bool
}

// useCauldron is BDS's cauldron use. False only without the block entity, as in BDS.
func (p *Player) useCauldron(r *world.Region, u *BlockUse) bool {
	c, ok := cauldronAt(r, u.Pos)
	if !ok {
		return false
	}
	held := p.inventory.Held()
	if held.Empty() {
		return true
	}
	level, liquid := p.s.cauldronState(u.Block)
	cu := &cauldronUse{p: p, r: r, pos: u.Pos, c: c, held: held, level: level, liquid: liquid}
	cu.potion = level != 0 && c.potion >= 0
	cu.water = level != 0 && liquid == cauldronWater && !cu.potion
	switch held.Name {
	case nameWaterBucket:
		cu.pour(cauldronWater, packet.LevelEventCauldronFillWater)
	case nameLavaBucket:
		cu.pour(cauldronLava, packet.LevelEventCauldronFillLava)
	case namePowderSnowBucket:
		cu.pour(cauldronPowderSnow, packet.LevelEventCauldronFillPowderSnow)
	case emptyBucket:
		cu.fillBucket()
	case nameGlassBottle:
		if level >= cauldronBottle {
			cu.fillBottle()
		} else {
			cu.dip()
		}
	case nameArrow:
		cu.tipArrows()
	case namePotion, nameSplashPotion, nameLingeringPotion:
		cu.pourPotion()
	default:
		cu.dip()
	}
	p.flush()
	return true
}

// give is BDS's _useInventory: n of the held item go and the replacement takes its place when
// none are left, else goes to the inventory (or drops). In creative nothing is used and the
// replacement is added.
func (cu *cauldronUse) give(replacement ItemStack, n int) {
	p := cu.p
	replacement = p.s.freshStack(replacement)
	if !p.creative() {
		inv := &p.inventory
		held := &inv.main[inv.held]
		if int(held.Count) <= n {
			*held = replacement
			p.sendHeld()
			p.sendEquipment()
			return
		}
		held.Count -= uint8(n) //nolint:gosec // n is below the count
		p.sendHeld()
	}
	if rest := p.Give(replacement); !rest.Empty() {
		p.s.game.sys.DropItem(p, rest, false)
	}
}

// setHeld puts the changed held item back.
func (cu *cauldronUse) setHeld(it ItemStack) {
	inv := &cu.p.inventory
	inv.main[inv.held] = cu.p.s.freshStack(it)
	cu.p.sendHeld()
	cu.p.sendEquipment()
}

// post posts a game event at the cauldron.
func (cu *cauldronUse) post(id int32) {
	cu.p.s.postBlockGameEvent(cu.r, id, cu.pos, cu.p, 0, false)
}

// bottleSound plays bottle.fill or bottle.empty at the cauldron's centre.
func (cu *cauldronUse) bottleSound(name string) {
	playSound(cu.r, cu.pos, soundEvent{name: name, pos: blockCenter(cu.pos), extra: -1}, nil)
}

// matches is whether the cauldron holds this liquid already (water with no potion).
func (cu *cauldronUse) matches(liquid string) bool {
	if liquid == cauldronWater {
		return cu.water
	}
	return cu.liquid == liquid
}

// pour is a full bucket: an empty cauldron or one of the same liquid is filled; another liquid
// or a potion empties it and the bucket is still poured.
func (cu *cauldronUse) pour(liquid string, event int32) {
	s := cu.p.s
	empty := ItemStack{Name: emptyBucket, Count: 1}
	switch {
	case cu.matches(liquid) || cu.level == 0:
		cu.give(empty, 1)
		cu.c.clear()
		s.setCauldronLevel(cu.r, cu.pos, cu.c, cauldronFull, liquid)
		cu.post(gameEventFluidPlace)
		s.cauldronEvent(cu.r, cu.pos, cu.c, event)
	case cu.potion || cu.water || cu.liquid != liquid:
		cu.give(empty, 1)
		cu.post(gameEventFluidPlace)
		s.explodeCauldron(cu.r, cu.pos, cu.c)
	}
}

// fillBucket is an empty bucket on a full cauldron with no potion: the liquid's bucket, and
// the cauldron is emptied.
func (cu *cauldronUse) fillBucket() {
	if cu.level < cauldronFull || cu.potion {
		return
	}
	var item string
	var event int32
	switch cu.liquid {
	case cauldronWater:
		item, event = nameWaterBucket, packet.LevelEventCauldronTakeWater
	case cauldronLava:
		item, event = nameLavaBucket, packet.LevelEventCauldronTakeLava
	case cauldronPowderSnow:
		item, event = namePowderSnowBucket, packet.LevelEventCauldronTakePowderSnow
	default:
		return
	}
	cu.give(ItemStack{Name: item, Count: 1}, 1)
	cu.p.s.cauldronEvent(cu.r, cu.pos, cu.c, event)
	cu.c.clear()
	cu.p.s.setCauldronLevel(cu.r, cu.pos, cu.c, 0, cauldronWater)
	cu.post(gameEventFluidPickup)
}

// fillBottle is a glass bottle on two levels or more: a water bottle from water, the potion
// from a potion, nothing from lava or powder snow.
func (cu *cauldronUse) fillBottle() {
	s := cu.p.s
	switch {
	case cu.water:
		cu.give(ItemStack{Name: namePotion, Meta: potionWater, Count: 1}, 1)
		s.cauldronEvent(cu.r, cu.pos, cu.c, packet.LevelEventCauldronTakeWater)
	case cu.potion:
		name := namePotion
		if t := int(cu.c.potionType); t >= 0 && t < len(potionItemsByType) {
			name = potionItemsByType[t]
		}
		cu.give(ItemStack{Name: name, Meta: int16(cu.c.potion), Count: 1}, 1) //nolint:gosec // potion IDs fit
		s.cauldronEvent(cu.r, cu.pos, cu.c, packet.LevelEventCauldronTakePotion)
	default:
		return
	}
	cu.bottleSound(packet.SoundEventBottleFill)
	if cu.level <= cauldronBottle {
		cu.c.clear()
	}
	s.setCauldronLevel(cu.r, cu.pos, cu.c, cu.level-cauldronBottle, cauldronWater)
	cu.post(gameEventFluidPickup)
}

// tipArrows is plain arrows in a potion that has effects: up to 16 a level (one level when two
// or fewer are left) become tipped arrows of the potion, and the potion goes when two levels or
// fewer remain.
func (cu *cauldronUse) tipArrows() {
	if cu.held.Meta != 0 || !cu.potion || cu.c.potion < firstTippablePotion {
		return
	}
	levels := cu.level - cauldronBottle
	if cu.level <= cauldronBottle {
		levels = 1
	}
	n := min(int(arrowsPerLevel*max(levels, 0)), int(cu.held.Count))
	left := cu.level - int32((n+arrowsPerLevel-1)/arrowsPerLevel)                         //nolint:gosec // a stack's worth
	cu.give(ItemStack{Name: nameArrow, Meta: int16(cu.c.potion + 1), Count: uint8(n)}, n) //nolint:gosec // potion IDs and a stack fit
	if left <= cauldronBottle {
		cu.c.potion = -1
		left = 0
	}
	cu.p.s.setCauldronLevel(cu.r, cu.pos, cu.c, left, cauldronWater)
	cu.post(gameEventFluidPickup)
}

// pourPotion is a potion bottle: a water bottle adds two levels of water, a potion with effects
// two levels of itself; anything else in the cauldron is emptied. Mundane, thick and awkward
// potions do nothing.
func (cu *cauldronUse) pourPotion() {
	s := cu.p.s
	bottle := ItemStack{Name: nameGlassBottle, Count: 1}
	id := int32(cu.held.Meta)
	if id <= lastPlainPotion {
		if id != potionWater {
			return
		}
		switch {
		case cu.water || cu.level == 0:
			cu.give(bottle, 1)
			cu.c.clear()
			s.setCauldronLevel(cu.r, cu.pos, cu.c, cu.level+cauldronBottle, cauldronWater)
			cu.post(gameEventFluidPlace)
			s.cauldronEvent(cu.r, cu.pos, cu.c, packet.LevelEventCauldronFillWater)
			cu.bottleSound(packet.SoundEventBottleEmpty)
		case cu.potion || cu.liquid != cauldronWater:
			cu.give(bottle, 1)
			s.explodeCauldron(cu.r, cu.pos, cu.c)
			cu.post(gameEventFluidPlace)
		}
		return
	}
	same := cu.potion && cu.c.potion == id
	switch {
	case cu.level == 0 || same && cu.level < cauldronFull:
		cu.c.potion = id
		cu.c.potionType = potionTypeOf(cu.held.Name)
		s.setCauldronLevel(cu.r, cu.pos, cu.c, cu.level+cauldronBottle, cauldronWater)
		cu.give(bottle, 1)
		cu.post(gameEventFluidPlace)
		s.cauldronEvent(cu.r, cu.pos, cu.c, packet.LevelEventCauldronFillPotion)
		cu.bottleSound(packet.SoundEventBottleEmpty)
	case !same:
		cu.give(bottle, 1)
		cu.post(gameEventFluidPlace)
		s.explodeCauldron(cu.r, cu.pos, cu.c)
		cu.c.dyed, cu.c.colour = false, 0
		cu.c.changed(cu.r, cu.pos)
		cu.bottleSound(packet.SoundEventBottleEmpty)
	}
}

// potionTypeOf is the potion type a cauldron saves for a potion item.
func potionTypeOf(name string) int16 {
	return int16(max(slices.Index(potionItemsByType[:], name), 0)) //nolint:gosec // three types
}

// dip is any other item in water with no potion: dyeable armour takes the dye or is washed,
// a dyed shulker box is washed, a dye colours the water and a banner loses its top pattern.
func (cu *cauldronUse) dip() {
	if !cu.water {
		return
	}
	held := cu.held
	switch {
	case cu.p.s.cauldronDyeable(held.Name):
		cu.dyeArmour()
	case isShulkerBox(held.Name) && held.Name != nameUndyedShulkerBox && !cu.c.dyed:
		cu.washShulker()
	default:
		if dye, ok := cu.p.s.banners().Dyes[held.Name]; ok {
			cu.addDye(dye)
			return
		}
		if held.Name == bannerItem {
			cu.washBanner()
		}
	}
}

// cauldronDyeable is an item a cauldron dyes and washes: humanoid armour, horse armour and
// wolf armour.
func (s *Host) cauldronDyeable(name string) bool {
	if name == nameWolfArmor {
		return true
	}
	t, ok := s.reg.Items.ByName(name)
	if !ok {
		return false
	}
	if slices.Contains(t.Tags, tagHorseArmor) {
		return true
	}
	return slices.Contains(t.Tags, tagArmor) && humanoidArmourName(name)
}

// humanoidArmourName is a helmet, chestplate, leggings or boots (the elytra is no armour piece).
func humanoidArmourName(name string) bool {
	for _, suffix := range [...]string{"_helmet", "_chestplate", "_leggings", "_boots"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// keepsColour is whether an item's colour shows and can be washed off: leather armour, leather
// horse armour and wolf armour (BDS's hasCustomColor, which checks the tier).
func (s *Host) keepsColour(it ItemStack) bool {
	if _, ok := it.Tag[customColorTag]; !ok {
		return false
	}
	if it.Name == nameWolfArmor || it.Name == nameLeatherHorse {
		return true
	}
	t, ok := s.reg.Items.ByName(it.Name)
	return ok && slices.Contains(t.Tags, tagLeatherTier)
}

// dyeArmour is armour in water: dyed water colours it (any armour, as BDS's setColor), plain
// water washes the colour off one that keeps it. One level goes either way.
func (cu *cauldronUse) dyeArmour() {
	s := cu.p.s
	held := cu.held
	held.Tag = maps.Clone(held.Tag)
	event := int32(packet.LevelEventCauldronDyeArmor)
	switch {
	case cu.c.dyed:
		if held.Tag == nil {
			held.Tag = map[string]any{}
		}
		held.Tag[customColorTag] = int32(cu.c.colour) //nolint:gosec // ARGB bits
	case s.keepsColour(held):
		delete(held.Tag, customColorTag)
		if len(held.Tag) == 0 {
			held.Tag = nil
		}
		event = packet.LevelEventCauldronCleanArmor
	default:
		return
	}
	cu.setHeld(held)
	s.setCauldronLevel(cu.r, cu.pos, cu.c, cu.level-1, cauldronWater)
	cu.post(gameEventFluidPickup)
	s.cauldronEvent(cu.r, cu.pos, cu.c, event)
	if cu.level <= 1 {
		cu.c.clear()
		cu.c.changed(cu.r, cu.pos)
	}
}

// washShulker is a dyed shulker box in plain water: it becomes the undyed box with its items.
func (cu *cauldronUse) washShulker() {
	s := cu.p.s
	blocks := s.reg.Blocks
	id, ok := blocks.Block(nameUndyedShulkerBox)
	if !ok {
		return
	}
	box := ItemStack{Name: nameUndyedShulkerBox, Count: 1, Tag: cu.held.Tag}
	box.Block = map[string]any{"name": nameUndyedShulkerBox, "states": blocks.States(id), "version": blocks.Version(id)}
	cu.setHeld(box)
	s.setCauldronLevel(cu.r, cu.pos, cu.c, cu.level-1, cauldronWater)
	cu.post(gameEventFluidPickup)
	s.cauldronEvent(cu.r, cu.pos, cu.c, packet.LevelEventCauldronCleanArmor)
}

// addDye is a dye in water: one is used (none in creative) and mixed into the colour; the level
// stays.
func (cu *cauldronUse) addDye(dye uint8) {
	s := cu.p.s
	if !cu.p.creative() {
		cu.p.useHeld()
	}
	cu.c.mixDye(dye)
	s.setCauldronLevel(cu.r, cu.pos, cu.c, cu.level, cauldronWater)
	cu.post(gameEventBlockChange)
	s.cauldronEvent(cu.r, cu.pos, cu.c, packet.LevelEventCauldronAddDye)
}

// washBanner is a player's banner with patterns in water: one banner loses its top pattern.
// The illager banner is not washed.
func (cu *cauldronUse) washBanner() {
	s := cu.p.s
	b := s.bannerOf(cu.held)
	if b.Type != BannerNormal || len(b.Patterns) == 0 {
		return
	}
	washed := cu.held
	washed.Count = 1
	washed.Tag = maps.Clone(washed.Tag)
	if len(b.Patterns) == 1 {
		delete(washed.Tag, bannerPatternsTag)
	} else {
		washed.Tag[bannerPatternsTag] = patternList(b.Patterns[:len(b.Patterns)-1])
	}
	if len(washed.Tag) == 0 {
		washed.Tag = nil
	}
	cu.give(washed, 1)
	s.setCauldronLevel(cu.r, cu.pos, cu.c, cu.level-1, cauldronWater)
	cu.post(gameEventFluidPickup)
	s.cauldronEvent(cu.r, cu.pos, cu.c, packet.LevelEventCauldronCleanBanner)
}
