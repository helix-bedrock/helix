package player

import (
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Brewing stands, from BDS 1.26.60's BrewingStandBlockActor (vtable 0xFBFDE90, tick
// 0xD442FA0, load 0xD441E70, save 0xD442400) and PotionBrewing: blaze powder gives 20 brews,
// a brew takes 400 ticks and stops when the ingredient changes or nothing can be brewed, and
// each bottle with a mix for the ingredient becomes its result. The recipes are BDS's own,
// from the CraftingData packet ([crafting.Table.Brew]). Hoppers and the window take the
// items BDS's container rules take.

// The five slots.
const (
	brewIngredient = 0
	brewBottleA    = 1
	brewBottleC    = 3
	brewFuel       = 4
	brewSize       = 5
)

// Brewing rules from BDS 1.26.60.
const (
	// brewTicks is how long one brew takes.
	brewTicks = 400
	// blazePowderBrews is how many brews one blaze powder gives (PotionBrewing::getFuelValue).
	blazePowderBrews = 20
	nameBlazePowder  = "minecraft:blaze_powder"
	namePufferfish   = "minecraft:pufferfish"
	nameGlassBottle  = "minecraft:glass_bottle"
	brewingStandName = "minecraft:brewing_stand"
	// The saved fields, all shorts.
	brewTimeTag  = "CookTime"
	brewFuelTag  = "FuelAmount"
	brewTotalTag = "FuelTotal"
)

// bottleStates are the block states that show a bottle in slots 1 to 3.
var bottleStates = [3]string{"brewing_stand_slot_a_bit", "brewing_stand_slot_b_bit", "brewing_stand_slot_c_bit"}

// potionItems are BDS's potion items (ItemStackBase::isPotionItem), which hoppers put in the
// bottle slots.
var potionItems = map[string]bool{
	"minecraft:potion": true, "minecraft:splash_potion": true, "minecraft:lingering_potion": true,
}

// brewingStandSpec is the brewing stand's machine spec.
var brewingStandSpec = &machineSpec{id: "BrewingStand", size: brewSize, window: byte(protocol.ContainerTypeBrewingStand), brewing: true}

// BrewingStand is the brewing part of a brewing stand's block entity: its fuel and how far the
// brew is. Get it with [Chest.BrewingStand]. Runs: owner of its chunk.
type BrewingStand struct {
	c *Chest
	// brewTime counts down from 400 while brewing; fuel is how many brews are left of the
	// blaze powder burning, fuelTotal how many it gave.
	brewTime, fuel, fuelTotal int32
	// ingredient is the item brewing started with: another one stops it.
	ingredient string
	// brewTicks replaces brewTicks when not zero; not saved.
	ticks int32
	// shown is what the windows were last told.
	shown [3]int32
}

// BrewingStand returns the brewing part of a brewing stand, nil for any other container.
func (c *Chest) BrewingStand() *BrewingStand { return c.brewing }

// Chest returns the block entity the brewing stand is part of, for its slots and viewers.
func (b *BrewingStand) Chest() *Chest { return b.c }

// BrewTime returns how many ticks the brew has left, 0 when not brewing.
func (b *BrewingStand) BrewTime() int32 { return b.brewTime }

// SetBrewTime sets how many ticks the brew has left.
func (b *BrewingStand) SetBrewTime(ticks int32) {
	b.brewTime = max(ticks, 0)
	b.c.chunk.Changed()
}

// Fuel returns how many brews are left of the burning fuel, and how many it gave.
func (b *BrewingStand) Fuel() (left, total int32) { return b.fuel, b.fuelTotal }

// SetFuel sets how many brews are left of the burning fuel, and how many it gave, which
// the window's bar is drawn from.
func (b *BrewingStand) SetFuel(left, total int32) {
	b.fuel, b.fuelTotal = max(left, 0), max(total, left, 0)
	b.c.chunk.Changed()
}

// BrewTicks returns how long one brew takes: 400 ticks unless set.
func (b *BrewingStand) BrewTicks() int32 {
	if b.ticks > 0 {
		return b.ticks
	}
	return brewTicks
}

// SetBrewTicks makes this stand take ticks for a brew; 0 goes back to 400. The window's
// bubbles are scaled to match. It is kept only while the chunk is loaded.
func (b *BrewingStand) SetBrewTicks(ticks int32) { b.ticks = max(ticks, 0) }

// load reads the stand's own saved fields.
func (b *BrewingStand) load(saved map[string]any) {
	t, _ := saved[brewTimeTag].(int16)
	f, _ := saved[brewFuelTag].(int16)
	total, _ := saved[brewTotalTag].(int16)
	b.brewTime, b.fuel, b.fuelTotal = int32(t), int32(f), int32(total)
	b.ingredient = b.c.items[brewIngredient].Name
	for _, k := range [...]string{brewTimeTag, brewFuelTag, brewTotalTag} {
		delete(b.c.saved, k)
	}
}

// save writes its fields as BDS does: three shorts.
func (b *BrewingStand) save(m map[string]any) {
	m[brewTimeTag] = int16(min(b.brewTime, 0x7FFF))   //nolint:gosec // clamped
	m[brewFuelTag] = int16(min(b.fuel, 0x7FFF))       //nolint:gosec // clamped
	m[brewTotalTag] = int16(min(b.fuelTotal, 0x7FFF)) //nolint:gosec // clamped
}

// BrewInfo is the brewing stand a system asks about.
type BrewInfo struct {
	Region *world.Region
	Pos    cube.Pos
	Stand  *BrewingStand
}

// BrewFuelFunc returns how many brews one fuel item gives, 0 for an item that is no fuel.
type BrewFuelFunc func(b BrewInfo, fuel ItemStack) int32

// BrewResultFunc returns what a bottle becomes with an ingredient, false for nothing.
// Returning false for every bottle stops the ingredient brewing.
type BrewResultFunc func(b BrewInfo, ingredient, bottle ItemStack) (ItemStack, bool)

// vanillaBrewFuel is BDS's: blaze powder gives 20 brews.
func vanillaBrewFuel(_ BrewInfo, fuel ItemStack) int32 {
	if fuel.Name == nameBlazePowder {
		return blazePowderBrews
	}
	return 0
}

// vanillaBrewResult is BDS's PotionBrewing::mix over the recipes now in force.
func vanillaBrewResult(b BrewInfo, ingredient, bottle ItemStack) (ItemStack, bool) {
	if b.Stand == nil || ingredient.Empty() || bottle.Empty() {
		return ItemStack{}, false
	}
	s := b.Stand.c.s
	in, ok1 := s.brewItem(bottle)
	reagent, ok2 := s.brewItem(ingredient)
	if !ok1 || !ok2 {
		return ItemStack{}, false
	}
	out, ok := s.Recipes().Brew(in, reagent)
	if !ok {
		return ItemStack{}, false
	}
	t, ok := s.reg.Items.ByID(int16(out.ID)) //nolint:gosec // network IDs are int16
	if !ok {
		return ItemStack{}, false
	}
	return ItemStack{Name: t.Name, Meta: int16(out.Meta), Count: 1}, true //nolint:gosec // aux values are int16
}

// brewItem is an item as the brewing recipes name it.
func (s *Host) brewItem(it ItemStack) (crafting.BrewItem, bool) {
	t, ok := s.reg.Items.ByName(it.Name)
	if !ok {
		return crafting.BrewItem{}, false
	}
	return crafting.BrewItem{ID: int32(t.ID), Meta: int32(it.Meta)}, true
}

// brewSystems returns the brewing systems, BDS's where none are set.
func (s *Host) brewSystems() (BrewFuelFunc, BrewResultFunc) {
	fuel, result := BrewFuelFunc(vanillaBrewFuel), BrewResultFunc(vanillaBrewResult)
	if s.game == nil {
		return fuel, result
	}
	if f := s.game.sys.BrewFuel; f != nil {
		fuel = f
	}
	if f := s.game.sys.BrewResult; f != nil {
		result = f
	}
	return fuel, result
}

// brewIngredient is BDS's PotionBrewing::isIngredient: the reagent of a mix, or pufferfish.
func (s *Host) brewIngredient(it ItemStack) bool {
	if it.Empty() {
		return false
	}
	if it.Name == namePufferfish {
		return true
	}
	b, ok := s.brewItem(it)
	return ok && s.Recipes().BrewIngredient(b)
}

// brewBottle is BDS's PotionBrewing::isPotionRecipeInput, what a player may put in a bottle
// slot: a potion of any kind or a potion some mix starts from.
func (s *Host) brewBottle(it ItemStack) bool {
	if it.Empty() {
		return false
	}
	if potionItems[it.Name] {
		return true
	}
	b, ok := s.brewItem(it)
	return ok && s.Recipes().BrewBottle(b)
}

// BrewEvent is a brewing stand finishing a brew. Results are what slots 1 to 3 become (an
// empty one stays as it is); clear Consume to keep the ingredient. Cancel to brew nothing:
// the fuel and ingredient stay.
type BrewEvent struct {
	Cancellable
	Region     *world.Region
	Pos        cube.Pos
	Stand      *BrewingStand
	Ingredient ItemStack
	Results    [3]ItemStack
	Consume    bool
}

// OnBrew listens to brewing stands finishing brews.
func OnBrew(s *Setup, fn func(e *BrewEvent), p Priority) error {
	return addListener(s, &s.h.game.brewEv, fn, p)
}

// canBrew is BDS's canBrew: an ingredient, fuel left, and a bottle with a mix for it.
func (b *BrewingStand) canBrew(info BrewInfo, result BrewResultFunc) bool {
	c := b.c
	in := c.items[brewIngredient]
	if in.Empty() || b.fuel <= 0 || !c.s.brewIngredient(in) {
		return false
	}
	for i := brewBottleA; i <= brewBottleC; i++ {
		if _, ok := result(info, in, c.items[i]); ok {
			return true
		}
	}
	return false
}

// tickBrewing is BDS's brewing stand tick: with no fuel left a fuel item is used, a brew
// starts when one can, counts down while the ingredient stays, and at the end the bottles
// are brewed, one fuel is used and the brewed sound plays. The block shows which bottle slots
// hold something.
func (c *Chest) tickBrewing(r *world.Region, pos cube.Pos) {
	b := c.brewing
	fuelFn, resultFn := c.s.brewSystems()
	info := BrewInfo{Region: r, Pos: pos, Stand: b}
	changed := false
	if fuel := c.items[brewFuel]; b.fuel == 0 && !fuel.Empty() {
		if n := fuelFn(info, fuel); n > 0 {
			b.fuel, b.fuelTotal = n, n
			fuel.Count--
			c.ownInventory().SetItem(brewFuel, fuel)
			changed = true
		}
	}
	can := b.canBrew(info, resultFn)
	switch {
	case b.brewTime <= 0:
		if can {
			b.brewTime, b.ingredient = b.BrewTicks(), c.items[brewIngredient].Name
			changed = true
		}
	default:
		b.brewTime--
		switch {
		case b.brewTime == 0 && can:
			b.brew(info, resultFn)
			changed = true
		case b.brewTime == 0, !can, c.items[brewIngredient].Name != b.ingredient:
			b.brewTime = 0
			changed = true
		}
	}
	if changed {
		c.chunk.Changed()
	}
	c.showBottles(r, pos)
	b.show()
}

// brew is BDS's brew: each bottle with a mix becomes its result, then one ingredient is used.
// One fuel goes and the brewed sound plays.
func (b *BrewingStand) brew(info BrewInfo, resultFn BrewResultFunc) {
	c := b.c
	in := c.items[brewIngredient]
	e := BrewEvent{Region: info.Region, Pos: info.Pos, Stand: b, Ingredient: in, Consume: true}
	for i := range e.Results {
		bottle := c.items[brewBottleA+i]
		if out, ok := resultFn(info, in, bottle); ok && !(out.Name == bottle.Name && out.Meta == bottle.Meta) {
			e.Results[i] = out
		}
	}
	if g := c.s.game; g != nil && g.brewEv.Has() {
		g.brewEv.fire(c.s.log, &e)
		if e.Cancelled() {
			return
		}
	}
	inv := c.ownInventory()
	for i, out := range e.Results {
		if !out.Empty() {
			out.Count = 1
			out.netID = c.s.nextStackID()
			inv.SetItem(brewBottleA+i, out)
		}
	}
	if e.Consume {
		in.Count--
		inv.SetItem(brewIngredient, in)
	}
	b.fuel--
	playSound(info.Region, info.Pos, soundEvent{name: packet.SoundEventPotionBrewed, pos: blockCenter(info.Pos), extra: -1}, nil)
}

// showBottles is BDS's onChanged: the block's three bits say which bottle slots hold
// something.
func (c *Chest) showBottles(r *world.Region, pos cube.Pos) {
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	blocks := c.s.reg.Blocks
	if blocks.Name(block) != brewingStandName {
		return
	}
	states := blocks.States(block)
	same := true
	for i, name := range bottleStates {
		want := boolByte(!c.items[brewBottleA+i].Empty())
		if v, _ := states[name].(uint8); v != want {
			same = false
		}
	}
	if same {
		return
	}
	next := make(map[string]any, len(states))
	for k, v := range states {
		next[k] = v
	}
	for i, name := range bottleStates {
		next[name] = boolByte(!c.items[brewBottleA+i].Empty())
	}
	if id, ok := blocks.StateID(brewingStandName, next); ok && r.SetBlock(pos, 0, id) {
		r.UpdateNeighbours(pos)
	}
}

// brewData are the keys BDS's brewing stand window reads.
var brewData = [3]int32{packet.ContainerDataBrewingStandBrewTime, packet.ContainerDataBrewingStandFuelAmount, packet.ContainerDataBrewingStandFuelTotal}

// windowValues are what the window shows: the brew time scaled to 400, which the client
// counts from, then the fuel bar.
func (b *BrewingStand) windowValues() [3]int32 {
	t := b.brewTime
	if ticks := b.BrewTicks(); ticks != brewTicks {
		t = int32(int64(t) * brewTicks / int64(ticks))
	}
	return [3]int32{t, b.fuel, b.fuelTotal}
}

// show tells everyone with it open what changed.
func (b *BrewingStand) show() {
	now := b.windowValues()
	if now == b.shown {
		return
	}
	for _, v := range b.c.viewers {
		for i, val := range now {
			if val != b.shown[i] {
				v.send(&packet.ContainerSetData{WindowID: v.window.id, Key: brewData[i], Value: val})
			}
		}
		v.flushNow()
	}
	b.shown = now
}

// showAll tells a player who just opened it everything.
func (b *BrewingStand) showAll(p *Player) {
	for i, val := range b.windowValues() {
		p.send(&packet.ContainerSetData{WindowID: p.window.id, Key: brewData[i], Value: val})
	}
	p.flush()
}

// brewTakeOne is BDS's canPushInItem for a hopper or dropper putting one of s in: from above
// into the ingredient slot if it is an ingredient; from any other side into a free bottle
// slot if it is a potion or glass bottle, else the fuel slot if it is fuel.
func (c *Chest) brewTakeOne(r *world.Region, s ItemStack, fromAbove bool) bool {
	fuelFn, _ := c.s.brewSystems()
	var slots []int
	switch {
	case fromAbove:
		if !c.s.brewIngredient(s) {
			return false
		}
		slots = []int{brewIngredient}
	case potionItems[s.Name] || s.Name == nameGlassBottle:
		slots = []int{1, 2, 3}
	case fuelFn(BrewInfo{Region: r, Pos: c.pos, Stand: c.brewing}, s) > 0:
		slots = []int{brewFuel}
	default:
		return false
	}
	for _, slot := range slots {
		cur := c.items[slot]
		limit := s.maxStack(c.s.reg)
		if slot >= brewBottleA && slot <= brewBottleC {
			limit = 1
		}
		switch {
		case cur.Empty():
			one := s
			one.Count = 1
			c.ownInventory().SetItem(slot, one)
			return true
		case cur.stacksWith(s) && cur.Count < limit:
			cur.Count++
			c.ownInventory().SetItem(slot, cur)
			return true
		}
	}
	return false
}

// brewGiveOne is BDS's canPullOutItem: a hopper under it takes from the bottle slots only.
func (c *Chest) brewGiveOne(r *world.Region, to *Chest) bool {
	for slot := brewBottleA; slot <= brewBottleC; slot++ {
		s := c.items[slot]
		if s.Empty() || !to.takeOne(r, s) {
			continue
		}
		s.Count--
		c.ownInventory().SetItem(slot, s)
		return true
	}
	return false
}

// brewingSlot reports whether a stack request's container and slot are a slot of the
// brewing stand the player has open.
func (p *Player) brewingSlot(container, slot byte) bool {
	w := &p.window
	if !w.open || w.inv == nil || w.kind != ContainerBrewingStand {
		return false
	}
	switch container {
	case protocol.ContainerBrewingStandInput:
		return slot == brewIngredient
	case protocol.ContainerBrewingStandResult:
		return slot >= brewBottleA && slot <= brewBottleC
	case protocol.ContainerBrewingStandFuel:
		return slot == brewFuel
	}
	return false
}

// brewingAllows is BDS's container rules for the open brewing stand: the ingredient slot takes
// ingredients, the bottle slots one potion each, the fuel slot fuel.
func (p *Player) brewingAllows(container byte, s ItemStack) bool {
	switch container {
	case protocol.ContainerBrewingStandInput:
		return p.s.brewIngredient(s)
	case protocol.ContainerBrewingStandResult:
		return s.Count == 1 && p.s.brewBottle(s)
	case protocol.ContainerBrewingStandFuel:
		fuelFn, _ := p.s.brewSystems()
		return fuelFn(BrewInfo{}, s) > 0
	}
	return false
}
