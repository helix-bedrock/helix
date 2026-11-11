package player

import (
	"maps"
	"math/rand/v2"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Campfires and soul campfires, from BDS 1.26.60: the block entity's tick (vtable 0xFBFEE70,
// tick 0xD448180, cooking one slot 0xD448C20, load 0xD449070, save 0xD449C70), the block's use
// (food goes on, flint and steel lights it, a shovel puts it out) and its entityInside (0xCB446B0:
// a lit campfire burns players and mobs). Going out and lighting are the fire package's.
//
// Not sent: the smoke and the particles over cooking food, which BDS adds through the level's
// particle call that a server does not send; clients draw campfire smoke themselves.

// BDS's names and numbers for a campfire.
const (
	campfireEntityID = "Campfire"
	nameCampfire     = "minecraft:campfire"
	nameSoulCampfire = "minecraft:soul_campfire"
	// campfireSlots is how many items cook on one at once.
	campfireSlots = 4
	// campfireCookTicks is how long each item cooks.
	campfireCookTicks = 600
	// campfireCrackleOdds is the one in n chance each tick of the crackle sound.
	campfireCrackleOdds = 50
	// campfireDropDelay is the pick-up delay of cooked and thrown out food (Spawner::spawnItem).
	campfireDropDelay = 10
	// campfireDamage and soulCampfireDamage are the fire damage a lit one deals each tick.
	campfireDamage     = 1
	soulCampfireDamage = 2
	// enchantFrostWalker on boots stops a campfire hurting.
	enchantFrostWalker = int16(25)
	// armorFeet is the boots' armour slot.
	armorFeet = 3
	// The saved fields: Item1 to Item4 and ItemTime1 to ItemTime4.
	campfireItemTag = "Item"
	campfireTimeTag = "ItemTime"
	// stateExtinguished is set on a campfire that is out.
	stateExtinguished = "extinguished"
)

// shovels are the items that put a campfire out (BDS's use lists the six).
var shovels = map[string]bool{
	"minecraft:wooden_shovel": true, "minecraft:stone_shovel": true, "minecraft:iron_shovel": true,
	"minecraft:golden_shovel": true, "minecraft:diamond_shovel": true, "minecraft:netherite_shovel": true,
}

// isCampfireName reports whether name is a campfire or soul campfire.
func isCampfireName(name string) bool { return name == nameCampfire || name == nameSoulCampfire }

// Campfire is a campfire's or soul campfire's block entity: the four items cooking on it and
// how long each has cooked. Get it with [CampfireAt]. Runs: owner of its chunk.
type Campfire struct {
	s     *Host
	saved map[string]any
	items [campfireSlots]ItemStack
	times [campfireSlots]int32
	// cookTicks replaces campfireCookTicks when not zero; not saved.
	cookTicks int32
	chunk     world.ChunkHandle
}

// CampfireAt returns the campfire block entity at pos. Runs: owner of the chunk.
func CampfireAt(r *world.Region, pos cube.Pos) (*Campfire, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	c, ok := be.(*Campfire)
	return c, ok
}

// Item returns what cooks in slot 0 to 3.
func (c *Campfire) Item(slot int) ItemStack {
	if slot < 0 || slot >= campfireSlots {
		return ItemStack{}
	}
	return c.items[slot]
}

// CookTime returns how many ticks the item in slot has cooked.
func (c *Campfire) CookTime(slot int) int32 {
	if slot < 0 || slot >= campfireSlots {
		return 0
	}
	return c.times[slot]
}

// SetItem puts one of item in slot 0 to 3, starting it from nothing; an empty item clears the
// slot. Clients are told.
func (c *Campfire) SetItem(r *world.Region, pos cube.Pos, slot int, item ItemStack) {
	if slot < 0 || slot >= campfireSlots {
		return
	}
	if !item.Empty() {
		item.Count = 1
	}
	c.items[slot], c.times[slot] = item, 0
	c.chunk.Changed()
	r.SendBlockEntity(pos)
}

// CookTicks returns how long an item takes to cook: 600 ticks unless set.
func (c *Campfire) CookTicks() int32 {
	if c.cookTicks > 0 {
		return c.cookTicks
	}
	return campfireCookTicks
}

// SetCookTicks makes this campfire cook each item in ticks; 0 goes back to 600. It is kept
// only while the chunk is loaded.
func (c *Campfire) SetCookTicks(ticks int32) { c.cookTicks = max(ticks, 0) }

// BindBlockEntity keeps the chunk the campfire is in.
func (c *Campfire) BindBlockEntity(h world.ChunkHandle) { c.chunk = h }

// loadCampfire brings back a saved campfire as BDS's load does.
func (s *Host) loadCampfire(saved map[string]any) *Campfire {
	c := &Campfire{s: s, saved: maps.Clone(saved)}
	for i := range campfireSlots {
		item, time := campfireItemTag+strconv.Itoa(i+1), campfireTimeTag+strconv.Itoa(i+1)
		if m, ok := saved[item].(map[string]any); ok {
			c.items[i] = withID(itemFromNBT(m), s.nextStackID)
		}
		c.times[i], _ = saved[time].(int32)
		delete(c.saved, item)
		delete(c.saved, time)
	}
	return c
}

// SaveData is what BDS saves of a campfire: each slot's time, and the items there are.
func (c *Campfire) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(c.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = campfireEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	for i := range campfireSlots {
		n := strconv.Itoa(i + 1)
		m[campfireTimeTag+n] = c.times[i]
		if !c.items[i].Empty() {
			m[campfireItemTag+n] = c.items[i].nbt(0, false)
		}
	}
	return m
}

// NetworkData is the same: the client draws the items from it.
func (c *Campfire) NetworkData(pos cube.Pos) map[string]any { return c.SaveData(pos) }

// recipeBlock is the recipe tag the campfire at pos cooks by.
func (c *Campfire) recipeBlock(r *world.Region, pos cube.Pos) string {
	if b, ok := r.Block(pos, 0); ok && c.s.reg.Blocks.Name(b) == nameSoulCampfire {
		return "soul_campfire"
	}
	return "campfire"
}

// result is what the campfire at pos makes of one item, false for none.
func (c *Campfire) result(r *world.Region, pos cube.Pos, in ItemStack) (ItemStack, bool) {
	if in.Empty() {
		return ItemStack{}, false
	}
	one := in
	one.Count = 1
	rec := c.s.Recipes().Smelt(c.recipeBlock(r, pos), c.s.craftItem(one))
	if rec == nil || len(rec.Output) == 0 {
		return ItemStack{}, false
	}
	out := itemFromNetwork(c.s.reg, rec.Output[0])
	return out, !out.Empty()
}

// TickBlockEntity is BDS's campfire tick: an unlit one forgets how far its items cooked; a lit
// one cooks each item one tick, one done at 600 drops out cooked, and now and then crackles.
func (c *Campfire) TickBlockEntity(r *world.Region, pos cube.Pos) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	if v, _ := c.s.reg.Blocks.States(b)[stateExtinguished].(uint8); v != 0 {
		if c.times != [campfireSlots]int32{} {
			c.times = [campfireSlots]int32{}
			c.chunk.Changed()
		}
		return
	}
	for i := range campfireSlots {
		if c.items[i].Empty() {
			continue
		}
		c.times[i]++
		if c.times[i] >= c.CookTicks() {
			c.cook(r, pos, i)
		}
	}
	if rand.IntN(campfireCrackleOdds) == 0 { //nolint:gosec // a sound's timing, not a secret
		playSound(r, pos, soundEvent{name: packet.SoundEventCampfireCrackle, pos: blockVec(pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}), extra: -1}, nil)
	}
	c.chunk.Changed()
}

// CampfireCookEvent is a campfire done cooking the item in Slot. Result is what drops out of
// it; cancel to keep the item cooking from the start again.
type CampfireCookEvent struct {
	Cancellable
	Region   *world.Region
	Pos      cube.Pos
	Campfire *Campfire
	Slot     int
	Input    ItemStack
	Result   ItemStack
}

// OnCampfireCook listens to campfires cooking items.
func OnCampfireCook(s *Setup, fn func(e *CampfireCookEvent), p Priority) error {
	return addListener(s, &s.h.game.campfireCookEv, fn, p)
}

// cook is BDS's cooking of one slot: the result drops at the middle of the block, unmoving,
// and the slot empties, which clients are told.
func (c *Campfire) cook(r *world.Region, pos cube.Pos, slot int) {
	out, _ := c.result(r, pos, c.items[slot])
	if g := c.s.game; g != nil && g.campfireCookEv.Has() {
		e := CampfireCookEvent{Region: r, Pos: pos, Campfire: c, Slot: slot, Input: c.items[slot], Result: out}
		g.campfireCookEv.fire(c.s.log, &e)
		if e.Cancelled() {
			c.times[slot] = 0
			return
		}
		out = e.Result
	}
	if !out.Empty() {
		c.s.spawnItem(r.Dimension(), out, blockCenter(pos), mgl32.Vec3{}, world.Rotation{}, campfireDropDelay, 0)
	}
	c.items[slot], c.times[slot] = ItemStack{}, 0
	r.SendBlockEntity(pos)
}

// DropItems throws out everything on the campfire, as BDS's dropAllItems: each item at the
// middle of the block. Runs: owner of the chunk.
func (c *Campfire) DropItems(r *world.Region, pos cube.Pos) {
	dropped := false
	for i := range campfireSlots {
		if c.items[i].Empty() {
			continue
		}
		c.s.spawnItem(r.Dimension(), c.items[i], blockCenter(pos), mgl32.Vec3{}, world.Rotation{}, campfireDropDelay, 0)
		c.items[i], c.times[i] = ItemStack{}, 0
		dropped = true
	}
	if dropped {
		c.chunk.Changed()
		r.SendBlockEntity(pos)
	}
}

// BlockEntityRemoved is BDS's onRemove: a broken campfire throws out its food.
func (c *Campfire) BlockEntityRemoved(r *world.Region, pos cube.Pos) {
	for i := range campfireSlots {
		if !c.items[i].Empty() {
			c.s.spawnItem(r.Dimension(), c.items[i], blockCenter(pos), mgl32.Vec3{}, world.Rotation{}, campfireDropDelay, 0)
			c.items[i] = ItemStack{}
		}
	}
}

// DropCampfireItems throws out the food of the campfire at pos, for water reaching it. Runs:
// owner of the region.
func (s *Host) DropCampfireItems(r *world.Region, pos cube.Pos) {
	if c, ok := CampfireAt(r, pos); ok {
		c.DropItems(r, pos)
	}
}

// useCampfire is BDS's CampfireBlock::use: flint and steel or a Fire Aspect item lights an unlit
// campfire, a shovel puts out a lit one (either wears the item by one), and food with a recipe
// here goes in the first free slot, used up outside creative. Anything else, or nothing done,
// leaves the click to the held item.
func (p *Player) useCampfire(r *world.Region, u *BlockUse) bool {
	held := u.Item
	f := p.s.fire
	switch {
	case held.Empty():
		return false
	case ignitesCampfire(held):
		if f == nil || !f.LightCampfire(r, u.Pos) {
			return false
		}
		if !p.creative() {
			p.hurtHeld(1)
		}
		return true
	case shovels[held.Name]:
		if f == nil || !f.DouseCampfire(r, u.Pos, false) {
			return false
		}
		if !p.creative() {
			p.hurtHeld(1)
		}
		return true
	}
	if f != nil && f.WaterIn(r, u.Pos) {
		return false
	}
	c, ok := CampfireAt(r, u.Pos)
	if !ok {
		return false
	}
	if _, ok := c.result(r, u.Pos, held); !ok {
		return false
	}
	slot := -1
	for i := range campfireSlots {
		if c.items[i].Empty() {
			slot = i
			break
		}
	}
	if slot < 0 {
		return false
	}
	c.SetItem(r, u.Pos, slot, held)
	levelEvent(r, u.Pos, blockVec(u.Pos), packet.LevelEventSoundAddItem, 0)
	if !p.creative() {
		p.useHeld()
	}
	return true
}

// ignitesCampfire reports whether an item lights a campfire: flint and steel, or anything with
// Fire Aspect.
func ignitesCampfire(s ItemStack) bool {
	if s.Name == nameFlintSteel {
		return true
	}
	for _, e := range s.Enchants() {
		if e.ID == enchantFireAspect {
			return true
		}
	}
	return false
}

// campfireTouch is what a lit campfire in an entity's box does to it: BDS's entityInside.
type campfireTouch struct {
	// damage is the lit campfire's fire damage, 0 for none.
	damage float32
	// unlit is an unlit campfire in the box, which something burning lights.
	unlit    bool
	unlitPos cube.Pos
}

// frostWalking reports whether the player wears boots with Frost Walker.
func (p *Player) frostWalking() bool {
	return hasEnchant(p.inventory.armor[armorFeet], enchantFrostWalker)
}

// campfireHurt is BDS's CampfireBlock::entityInside for a player or mob: something burning
// lights an unlit campfire; a lit one deals its fire damage (1, a soul one 2) unless the
// entity does not burn or wears Frost Walker boots, then sets it on fire for 8 seconds unless
// it is wet. Dropped items are not hurt (BDS's category check).
func (s *Host) campfireHurt(r *world.Region, l Living, c campfireTouch) {
	if c.unlit && l.OnFire() > 0 && s.fire != nil {
		s.fire.LightCampfire(r, c.unlitPos)
	}
	if c.damage <= 0 || l.FireImmune() {
		return
	}
	if p, ok := l.(*Player); ok && p.frostWalking() {
		return
	}
	l.TakeDamage(c.damage, DamageSource{Cause: DamageFire})
	if !l.Dead() && !l.Wet() {
		l.SetOnFire(burnSeconds)
	}
}

// touchCampfire notes the campfire b at pos in an entity's box: a lit one's damage, the
// strongest when there are several, or the first unlit one.
func (s *Host) touchCampfire(c *campfireTouch, pos cube.Pos, b uint32) {
	if v, _ := s.reg.Blocks.States(b)[stateExtinguished].(uint8); v != 0 {
		if !c.unlit {
			c.unlit, c.unlitPos = true, pos
		}
		return
	}
	damage := float32(campfireDamage)
	if s.reg.Blocks.Name(b) == nameSoulCampfire {
		damage = soulCampfireDamage
	}
	c.damage = max(c.damage, damage)
}
