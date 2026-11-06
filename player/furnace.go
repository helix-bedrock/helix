package player

import (
	"math/rand/v2"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Furnaces, blast furnaces and smokers, ported from BDS 1.26.60's furnace block actor:
// its tick, fuel and experience rules, the slots hoppers and players may use, and the
// swap between the lit and unlit block. Research notes are private.

// The three slots.
const (
	furnaceInput  = 0
	furnaceFuel   = 1
	furnaceResult = 2
	furnaceSize   = 3
)

// Furnace rules from BDS 1.26.60.
const (
	// A lit furnace plays its sound after furnaceSoundMin plus up to furnaceSoundSpread-1
	// ticks, again and again.
	furnaceSoundMin    = 20
	furnaceSoundSpread = 80
	// furnaceCoolDown is how much cooking a furnace loses each tick it cannot cook.
	furnaceCoolDown = 2
	// furnaceXPScale is how BDS keeps stored experience: in hundredths.
	furnaceXPScale = 100
	// furnaceFuelSecond turns a minecraft:fuel component's seconds into cooks: the cook
	// time times seconds over ten.
	furnaceFuelSecond = 10
	// The saved fields.
	furnaceBurnTag     = "BurnTime"
	furnaceCookTag     = "CookTime"
	furnaceDurationTag = "BurnDuration"
	furnaceXPTag       = "StoredXPInt"
	furnaceOldXPTag    = "StoredXP"
	// The items BDS's fuel and sponge rules name.
	lavaBucket  = "minecraft:lava_bucket"
	waterBucket = "minecraft:water_bucket"
	wetSponge   = "minecraft:wet_sponge"
)

// furnaceKind is what tells a furnace, a blast furnace and a smoker apart.
type furnaceKind struct {
	kind ContainerKind
	// recipes is the block their recipes name, and the key of the cook time.
	recipes string
	// unlit and lit are the two blocks it swaps between.
	unlit, lit string
	// sound is what a lit one plays now and then.
	sound string
	// ingredient is the container a stack request names its input slot by.
	ingredient byte
}

// The kinds, by BDS's block actor constructors.
var (
	furnaceKindFurnace = &furnaceKind{
		kind: ContainerFurnace, recipes: "furnace", unlit: "minecraft:furnace", lit: "minecraft:lit_furnace",
		sound: packet.SoundEventFurnaceUse, ingredient: protocol.ContainerFurnaceIngredient,
	}
	furnaceKindBlast = &furnaceKind{
		kind: ContainerBlastFurnace, recipes: "blast_furnace", unlit: "minecraft:blast_furnace", lit: "minecraft:lit_blast_furnace",
		sound: packet.SoundEventBlastFurnaceUse, ingredient: protocol.ContainerBlastFurnaceIngredient,
	}
	furnaceKindSmoker = &furnaceKind{
		kind: ContainerSmoker, recipes: "smoker", unlit: "minecraft:smoker", lit: "minecraft:lit_smoker",
		sound: packet.SoundEventSmokerUse, ingredient: protocol.ContainerSmokerIngredient,
	}
)

// The specs of the three, each shared by the lit and unlit block.
var (
	furnaceSpec      = &machineSpec{id: "Furnace", size: furnaceSize, window: byte(protocol.ContainerTypeFurnace), furnace: furnaceKindFurnace}
	blastFurnaceSpec = &machineSpec{id: "BlastFurnace", size: furnaceSize, window: byte(protocol.ContainerTypeBlastFurnace), furnace: furnaceKindBlast}
	smokerSpec       = &machineSpec{id: "Smoker", size: furnaceSize, window: byte(protocol.ContainerTypeSmoker), furnace: furnaceKindSmoker}
)

// furnaceFacts are BDS's numbers, from helix-data.
type furnaceFacts struct {
	cook        map[string]int32
	fuel        map[string]float32
	fuelSeconds map[string]float32
	xp          map[string]float32
}

// noFurnaceFacts stand in for a Host built without them: nothing burns or gives
// experience. They never change.
var noFurnaceFacts = &furnaceFacts{}

// furnaceFacts returns the Host's furnace facts.
func (s *Host) furnaceFacts() *furnaceFacts {
	if s.furnaces == nil {
		return noFurnaceFacts
	}
	return s.furnaces
}

// loadFurnaceFacts reads helix-data's furnace facts.
func loadFurnaceFacts() (*furnaceFacts, error) {
	d, err := data.FurnaceData()
	if err != nil {
		return nil, err
	}
	return &furnaceFacts{cook: d.CookTicks, fuel: d.Fuel, fuelSeconds: d.FuelSeconds, xp: d.XP}, nil
}

// Furnace is the furnace part of a furnace's, blast furnace's or smoker's block entity:
// how long its fuel still burns and how far its item is cooked. Get it with
// [Chest.Furnace]. Runs: owner of its chunk.
type Furnace struct {
	c    *Chest
	kind *furnaceKind
	// burn is how many ticks the fuel still burns, burnDuration how long it burned
	// in all, cook how far the item is (BDS's BurnTime, BurnDuration, CookTime).
	burn, burnDuration, cook int32
	// storedXP is experience left by items hoppers took out, in hundredths.
	storedXP int32
	// soundIn counts the ticks since the last sound, soundNext how many to wait.
	soundIn, soundNext int32
	// started says the first tick has set the block to lit or unlit.
	started bool
	// swapping says the block is being swapped, so the block entity stays.
	swapping bool
	// cookTicks replaces the kind's cook time when not zero.
	cookTicks int32
	// input is the kind of item last seen in the input slot: a different one starts
	// cooking from nothing, as BDS's setItem does.
	input ItemStack
	// shown is what the windows were last told: progress, burn time, burn duration.
	shown [3]int32
	// The last smelting lookup, for the table it was made with.
	table      *crafting.Table
	lastIn     ItemStack
	lastOut    ItemStack
	lastResult bool
}

// FurnaceInfo is the furnace a system asks about.
type FurnaceInfo struct {
	Region  *world.Region
	Pos     cube.Pos
	Furnace *Furnace
}

// Kind returns what the furnace is: ContainerFurnace, ContainerBlastFurnace or
// ContainerSmoker.
func (f *Furnace) Kind() ContainerKind { return f.kind.kind }

// RecipeBlock returns the block its recipes name: "furnace", "blast_furnace" or "smoker".
func (f *Furnace) RecipeBlock() string { return f.kind.recipes }

// Chest returns the block entity the furnace is part of, for its slots and viewers.
func (f *Furnace) Chest() *Chest { return f.c }

// BurnTime returns how many more ticks the fuel burns; 0 means it is out.
func (f *Furnace) BurnTime() int32 { return f.burn }

// BurnDuration returns how many ticks the burning fuel burns in all.
func (f *Furnace) BurnDuration() int32 { return f.burnDuration }

// SetBurnTime sets how many more ticks the fuel burns, and how long it burns in all,
// which the window's flame is drawn from. The block lights or goes out on its next tick.
func (f *Furnace) SetBurnTime(left, duration int32) {
	f.burn, f.burnDuration = max(left, 0), max(duration, left, 0)
	f.c.chunk.Changed()
}

// CookProgress returns how many ticks the item in it has cooked.
func (f *Furnace) CookProgress() int32 { return f.cook }

// SetCookProgress sets how many ticks the item in it has cooked.
func (f *Furnace) SetCookProgress(ticks int32) {
	f.cook = max(ticks, 0)
	f.c.chunk.Changed()
}

// CookTicks returns how many ticks one item takes: 200 for a furnace, 100 for a blast
// furnace or smoker, unless set.
func (f *Furnace) CookTicks() int32 {
	if f.cookTicks > 0 {
		return f.cookTicks
	}
	return max(f.c.s.furnaceFacts().cook[f.kind.recipes], 1)
}

// SetCookTicks makes this furnace take ticks to smelt one item; 0 goes back to its
// kind's. As in BDS, fuel burns for a share of the cook time, so a faster furnace uses
// its fuel faster too and smelts the same number of items with it. The window's arrow
// is scaled to match. It is kept only while the chunk is loaded: it is not saved.
func (f *Furnace) SetCookTicks(ticks int32) { f.cookTicks = max(ticks, 0) }

// StoredExperience returns the experience waiting in it, from items hoppers took out.
// BDS gives it as orbs when the furnace breaks.
func (f *Furnace) StoredExperience() float32 { return float32(f.storedXP) / furnaceXPScale }

// SetStoredExperience sets the experience waiting in it.
func (f *Furnace) SetStoredExperience(xp float32) {
	f.storedXP = int32(max(xp, 0) * furnaceXPScale)
	f.c.chunk.Changed()
}

// Lit reports whether it is burning.
func (f *Furnace) Lit() bool { return f.burn > 0 }

// Furnace returns the furnace part of a furnace, blast furnace or smoker, nil for any
// other container.
func (c *Chest) Furnace() *Furnace { return c.furnace }

// newMachine makes the block entity of a block with a machine spec.
func (s *Host) newMachine(pos cube.Pos, spec *machineSpec) *Chest {
	c := &Chest{s: s, pos: pos, lead: true, machine: spec}
	if spec.furnace != nil {
		c.furnace = &Furnace{c: c, kind: spec.furnace}
	}
	return c
}

// load reads a furnace's own saved fields, as BDS's load does: StoredXPInt, or
// the older StoredXP short when that is missing.
func (f *Furnace) load(saved map[string]any) {
	burn, _ := saved[furnaceBurnTag].(int16)
	cook, _ := saved[furnaceCookTag].(int16)
	duration, _ := saved[furnaceDurationTag].(int16)
	f.burn, f.cook, f.burnDuration = int32(burn), int32(cook), int32(duration)
	if xp, ok := saved[furnaceXPTag].(int32); ok {
		f.storedXP = xp
	} else if xp, ok := saved[furnaceOldXPTag].(int16); ok {
		f.storedXP = int32(uint16(xp))
	}
	for _, k := range [...]string{furnaceBurnTag, furnaceCookTag, furnaceDurationTag, furnaceXPTag, furnaceOldXPTag} {
		delete(f.c.saved, k)
	}
}

// save writes its fields as BDS does: three shorts and an int.
func (f *Furnace) save(m map[string]any) {
	m[furnaceBurnTag] = int16(min(f.burn, 0x7FFF))             //nolint:gosec // clamped
	m[furnaceCookTag] = int16(min(f.cook, 0x7FFF))             //nolint:gosec // clamped
	m[furnaceDurationTag] = int16(min(f.burnDuration, 0x7FFF)) //nolint:gosec // clamped
	m[furnaceXPTag] = f.storedXP
}

// KeepBlockEntity keeps a furnace's block entity while it swaps between its lit and
// unlit block, as BDS's flag does.
func (c *Chest) KeepBlockEntity(uint32) bool { return c.furnace != nil && c.furnace.swapping }

// FurnaceFuelFunc returns how many ticks one fuel item burns for in a furnace, 0 for an
// item that is no fuel.
type FurnaceFuelFunc func(f FurnaceInfo, fuel ItemStack) int32

// FurnaceResultFunc returns what a furnace makes of one input item, false for an item
// it does not smelt. Returning false for an item stops furnaces smelting it; f says
// which furnace, so the answer may differ per block.
type FurnaceResultFunc func(f FurnaceInfo, input ItemStack) (ItemStack, bool)

// FurnaceXPFunc returns the experience one smelted item gives, by the item made.
type FurnaceXPFunc func(f FurnaceInfo, made ItemStack) float32

// vanillaFurnaceFuel is BDS's burn time: a minecraft:fuel component burns for the cook
// time times its seconds over ten, at least one tick; any other fuel for the cook time
// times the item's share, cut to whole ticks.
func vanillaFurnaceFuel(f FurnaceInfo, fuel ItemStack) int32 {
	if fuel.Empty() || f.Furnace == nil {
		return 0
	}
	facts := f.Furnace.c.s.furnaceFacts()
	cook := float32(f.Furnace.CookTicks())
	if sec, ok := facts.fuelSeconds[fuel.Name]; ok {
		return int32(max(1, cook*sec/furnaceFuelSecond))
	}
	return int32(cook * facts.fuel[fuel.Name])
}

// vanillaFurnaceResult is the furnace's recipe for one item, from the recipes now in
// force. The last answer is kept, so a furnace cooking one item looks it up once.
func vanillaFurnaceResult(f FurnaceInfo, input ItemStack) (ItemStack, bool) {
	fu := f.Furnace
	if input.Empty() || fu == nil {
		return ItemStack{}, false
	}
	s := fu.c.s
	table := s.Recipes()
	if fu.table == table && fu.lastIn.Name == input.Name && fu.lastIn.Meta == input.Meta {
		return fu.lastOut, fu.lastResult
	}
	out, ok := ItemStack{}, false
	if r := table.Smelt(fu.kind.recipes, s.craftItem(input)); r != nil {
		out = itemFromNetwork(s.reg, r.Output[0])
		ok = !out.Empty()
	}
	fu.table, fu.lastIn, fu.lastOut, fu.lastResult = table, ItemStack{Name: input.Name, Meta: input.Meta}, out, ok
	return out, ok
}

// vanillaFurnaceXP is BDS's experience per item made.
func vanillaFurnaceXP(f FurnaceInfo, made ItemStack) float32 {
	if f.Furnace == nil {
		return 0
	}
	return f.Furnace.c.s.furnaceFacts().xp[made.Name]
}

// FurnaceBurnEvent is a furnace about to burn a new fuel item, because it went out with
// something to smelt. Cancel it to leave the furnace out this tick. BurnTime is how long
// the item burns; clear Consume to burn it without using it up.
type FurnaceBurnEvent struct {
	Cancellable
	Region   *world.Region
	Pos      cube.Pos
	Furnace  *Furnace
	Fuel     ItemStack
	BurnTime int32
	Consume  bool
}

// FurnaceSmeltEvent is a furnace done cooking one item. Cancel it to make nothing: the
// item stays and starts again. Result is what goes in the result slot; clear Consume to
// keep the input item.
type FurnaceSmeltEvent struct {
	Cancellable
	Region  *world.Region
	Pos     cube.Pos
	Furnace *Furnace
	Input   ItemStack
	Result  ItemStack
	Consume bool
}

// OnFurnaceBurn listens to furnaces burning fuel.
func OnFurnaceBurn(s *Setup, fn func(e *FurnaceBurnEvent), p Priority) error {
	return addListener(s, &s.h.game.furnaceBurnEv, fn, p)
}

// OnFurnaceSmelt listens to furnaces smelting items.
func OnFurnaceSmelt(s *Setup, fn func(e *FurnaceSmeltEvent), p Priority) error {
	return addListener(s, &s.h.game.furnaceSmeltEv, fn, p)
}

// furnaceSystems returns the furnace systems, BDS's where none are set.
func (s *Host) furnaceSystems() (FurnaceFuelFunc, FurnaceResultFunc, FurnaceXPFunc) {
	fuel, result, xp := FurnaceFuelFunc(vanillaFurnaceFuel), FurnaceResultFunc(vanillaFurnaceResult), FurnaceXPFunc(vanillaFurnaceXP)
	if s.game == nil {
		return fuel, result, xp
	}
	if f := s.game.sys.FurnaceFuel; f != nil {
		fuel = f
	}
	if f := s.game.sys.FurnaceResult; f != nil {
		result = f
	}
	if f := s.game.sys.FurnaceXP; f != nil {
		xp = f
	}
	return fuel, result, xp
}

// canSmelt is BDS's check before cooking: an input with a recipe whose result is not
// the input itself, and room for it in the result slot.
func (f *Furnace) canSmelt(info FurnaceInfo, result FurnaceResultFunc) (ItemStack, bool) {
	c := f.c
	in := c.items[furnaceInput]
	if in.Empty() {
		return ItemStack{}, false
	}
	one := in
	one.Count = 1
	out, ok := result(info, one)
	if !ok || out.Empty() || out.Name == in.Name && out.Meta == in.Meta {
		return ItemStack{}, false
	}
	have := c.items[furnaceResult]
	if have.Empty() {
		return out, true
	}
	if !have.stacksWith(out) {
		return ItemStack{}, false
	}
	return out, have.Count < have.maxStack(c.s.reg)
}

// tickFurnace is BDS's furnace tick: the fuel burns down with its sound, a new item of
// fuel is lit when there is something to smelt, the item cooks or cools, a done item is
// smelted, and the block swaps when the fire starts or stops.
func (c *Chest) tickFurnace(r *world.Region, pos cube.Pos) {
	f := c.furnace
	fuelFn, resultFn, _ := c.s.furnaceSystems()
	info := FurnaceInfo{Region: r, Pos: pos, Furnace: f}
	if in := c.items[furnaceInput]; in.Empty() != f.input.Empty() || in.Name != f.input.Name || in.Meta != f.input.Meta {
		// A loaded furnace keeps the progress it saved.
		if f.started {
			f.cook = 0
		}
		f.input = ItemStack{Name: in.Name, Meta: in.Meta, Count: min(in.Count, 1)}
	}
	wasLit := f.burn > 0
	_, can := f.canSmelt(info, resultFn)
	if f.burn > 0 {
		if f.soundIn >= f.soundNext {
			f.soundNext = rand.Int32N(furnaceSoundSpread) + furnaceSoundMin //nolint:gosec // a sound's timing, not a secret
			f.soundIn = 0
			playSound(r, pos, soundEvent{name: f.kind.sound, pos: blockCenter(pos), extra: -1}, nil)
		}
		f.soundIn++
		f.burn--
	}
	if !f.started {
		f.started = true
		c.setLit(r, pos, f.burn > 0)
	}
	changed := false
	if f.burn == 0 && can {
		changed = f.lightFuel(info, fuelFn)
	}
	cookTicks := f.CookTicks()
	if f.burn > 0 && can {
		f.cook++
		if f.cook >= cookTicks {
			f.cook = 0
			if out, ok := f.canSmelt(info, resultFn); ok {
				f.smelt(info, out)
			}
			changed = true
		}
	} else if f.cook > 0 {
		f.cook = min(max(f.cook-furnaceCoolDown, 0), cookTicks)
	}
	if lit := f.burn > 0; lit != wasLit {
		changed = true
		c.setLit(r, pos, lit)
	}
	if changed {
		c.chunk.Changed()
	}
	f.show()
}

// lightFuel burns the next fuel item, using it up: a lava bucket leaves its bucket. It
// reports whether the furnace is now lit.
func (f *Furnace) lightFuel(info FurnaceInfo, fuelFn FurnaceFuelFunc) bool {
	c := f.c
	fuel := c.items[furnaceFuel]
	ticks := fuelFn(info, fuel)
	consume := true
	if g := c.s.game; g != nil && g.furnaceBurnEv.Has() && !fuel.Empty() {
		e := FurnaceBurnEvent{Region: info.Region, Pos: info.Pos, Furnace: f, Fuel: fuel, BurnTime: ticks, Consume: true}
		g.furnaceBurnEv.fire(c.s.log, &e)
		if e.Cancelled() {
			return false
		}
		ticks, consume = e.BurnTime, e.Consume
	}
	f.burn, f.burnDuration = max(ticks, 0), max(ticks, 0)
	if f.burn <= 0 {
		return false
	}
	if consume && !fuel.Empty() {
		next := fuel
		if next.Count == 1 && next.Name == lavaBucket {
			next = ItemStack{Name: emptyBucket, Count: 1}
		} else {
			next.Count--
		}
		c.ownInventory().SetItem(furnaceFuel, next)
	}
	return true
}

// smelt makes one item: the result goes in its slot, a wet sponge turns a bucket in the
// fuel slot into a water bucket, and one input item is used.
func (f *Furnace) smelt(info FurnaceInfo, out ItemStack) {
	c := f.c
	in := c.items[furnaceInput]
	consume := true
	if g := c.s.game; g != nil && g.furnaceSmeltEv.Has() {
		one := in
		one.Count = 1
		e := FurnaceSmeltEvent{Region: info.Region, Pos: info.Pos, Furnace: f, Input: one, Result: out, Consume: true}
		g.furnaceSmeltEv.fire(c.s.log, &e)
		if e.Cancelled() {
			return
		}
		out, consume = e.Result, e.Consume
	}
	inv := c.ownInventory()
	if !out.Empty() {
		have := c.items[furnaceResult]
		switch {
		case have.Empty():
			inv.SetItem(furnaceResult, out)
		case have.stacksWith(out):
			// BDS adds the whole result; Helix stops at a full stack, which only a
			// plugin's recipe making several items could pass.
			have.Count = uint8(min(int(have.Count)+int(out.Count), int(have.maxStack(c.s.reg)))) //nolint:gosec // at most a stack
			inv.SetItem(furnaceResult, have)
		}
	}
	if fuel := c.items[furnaceFuel]; in.Name == wetSponge && fuel.Name == emptyBucket && fuel.Meta == 0 {
		inv.SetItem(furnaceFuel, ItemStack{Name: waterBucket, Count: 1})
	}
	if consume {
		in.Count--
		inv.SetItem(furnaceInput, in)
	}
}

// setLit swaps the block for its lit or unlit form, facing the same way, keeping the
// block entity.
func (c *Chest) setLit(r *world.Region, pos cube.Pos, lit bool) {
	f := c.furnace
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	blocks := c.s.reg.Blocks
	name := blocks.Name(block)
	want := f.kind.unlit
	if lit {
		want = f.kind.lit
	}
	if name == want || name != f.kind.lit && name != f.kind.unlit {
		return
	}
	next, ok := blocks.StateID(want, blocks.States(block))
	if !ok {
		return
	}
	f.swapping = true
	r.SetBlock(pos, 0, next)
	f.swapping = false
	r.UpdateNeighbours(pos)
	c.changed = true
}

// furnaceData are the keys BDS's furnace window reads.
var furnaceData = [3]int32{packet.ContainerDataFurnaceTickCount, packet.ContainerDataFurnaceLitTime, packet.ContainerDataFurnaceLitDuration}

// windowValues are what the window shows: the arrow scaled to the kind's own cook time,
// which the client divides by, then the flame's two numbers.
func (f *Furnace) windowValues() [3]int32 {
	progress := f.cook
	if base, ticks := f.c.s.furnaceFacts().cook[f.kind.recipes], f.CookTicks(); ticks > 0 && base != ticks {
		progress = int32(int64(f.cook) * int64(base) / int64(ticks))
	}
	return [3]int32{progress, f.burn, f.burnDuration}
}

// show tells everyone with it open what changed.
func (f *Furnace) show() {
	now := f.windowValues()
	if now == f.shown {
		return
	}
	for _, v := range f.c.viewers {
		for i, val := range now {
			if val != f.shown[i] {
				v.send(&packet.ContainerSetData{WindowID: v.window.id, Key: furnaceData[i], Value: val})
			}
		}
		v.flushNow()
	}
	f.shown = now
}

// showAll tells a player who just opened it everything.
func (f *Furnace) showAll(p *Player) {
	for i, val := range f.windowValues() {
		p.send(&packet.ContainerSetData{WindowID: p.window.id, Key: furnaceData[i], Value: val})
	}
	p.flush()
}

// fuelAllowed is BDS's rule for the fuel slot: fuel only.
func (s *Host) fuelAllowed(r *world.Region, f *Furnace, it ItemStack) bool {
	fuelFn, _, _ := s.furnaceSystems()
	return fuelFn(FurnaceInfo{Region: r, Pos: f.c.pos, Furnace: f}, it) > 0
}

// furnaceTakeOne is BDS's rule for what a hopper or dropper may put in: from above into the
// input, from any other side into the fuel slot if it is fuel. It puts in one of s.
func (c *Chest) furnaceTakeOne(r *world.Region, s ItemStack, fromAbove bool) bool {
	slot := furnaceInput
	if !fromAbove {
		if !c.s.fuelAllowed(r, c.furnace, s) {
			return false
		}
		slot = furnaceFuel
	}
	cur := c.items[slot]
	switch {
	case cur.Empty():
		one := s
		one.Count = 1
		c.ownInventory().SetItem(slot, one)
		return true
	case cur.stacksWith(s) && cur.Count < s.maxStack(c.s.reg):
		cur.Count++
		c.ownInventory().SetItem(slot, cur)
		return true
	}
	return false
}

// furnaceGiveOne is BDS's rule for what a hopper under it may take: the result, and an
// empty bucket left in the fuel slot. A result taken stores its experience.
func (c *Chest) furnaceGiveOne(r *world.Region, to *Chest) bool {
	for _, slot := range [...]int{furnaceResult, furnaceFuel} {
		s := c.items[slot]
		if s.Empty() || slot == furnaceFuel && s.Name != emptyBucket || !to.takeOne(r, s) {
			continue
		}
		if slot == furnaceResult {
			_, _, xpFn := c.s.furnaceSystems()
			one := s
			one.Count = 1
			c.furnace.storedXP += int32(xpFn(FurnaceInfo{Region: r, Pos: c.pos, Furnace: c.furnace}, one) * furnaceXPScale)
		}
		s.Count--
		c.ownInventory().SetItem(slot, s)
		return true
	}
	return false
}

// resultTaken gives a player who took count of made out of the result slot its
// experience, as BDS's furnace output slot does: the items' worth, rounded at random,
// as orbs at the player. The furnace's stored experience is then thrown away unless a
// hopper that is on stands under it, as BDS does.
func (c *Chest) resultTaken(r *world.Region, p *Player, made ItemStack, count int) {
	f := c.furnace
	_, _, xpFn := c.s.furnaceSystems()
	xp := roundRandom(float32(count) * xpFn(FurnaceInfo{Region: r, Pos: c.pos, Furnace: f}, made))
	if xp > 0 && p.entity != nil {
		c.s.SpawnExperience(r, p.entity.Position(), xp)
	}
	if !c.hopperBelowOn(r) {
		f.storedXP = 0
	}
	c.chunk.Changed()
}

// hopperBelowOn reports whether a hopper that is not turned off is under the furnace.
func (c *Chest) hopperBelowOn(r *world.Region) bool {
	below := c.pos.Side(cube.FaceDown)
	h := c.hopperAt(r, below)
	if h == nil {
		return false
	}
	b, ok := r.Block(below, 0)
	if !ok {
		return false
	}
	off, _ := c.s.reg.Blocks.States(b)[stateHopperOff].(uint8)
	return off == 0
}

// furnaceBroken gives the experience a broken furnace holds, as orbs at its corner: the
// stored experience and what its result slot is worth, each rounded at random.
func (c *Chest) furnaceBroken(r *world.Region, pos cube.Pos) {
	f := c.furnace
	_, _, xpFn := c.s.furnaceSystems()
	xp := roundRandom(float32(f.storedXP) / furnaceXPScale)
	if out := c.items[furnaceResult]; !out.Empty() {
		one := out
		one.Count = 1
		xp += roundRandom(float32(out.Count) * xpFn(FurnaceInfo{Region: r, Pos: pos, Furnace: f}, one))
	}
	f.storedXP = 0
	if xp > 0 {
		c.s.SpawnExperience(r, blockVec(pos), xp)
	}
}

// roundRandom is BDS's rounding of experience: down, or up with the chance of the part
// past the whole number.
func roundRandom(v float32) int32 {
	if v <= 0 {
		return 0
	}
	whole := int32(v)
	if frac := v - float32(whole); frac > 0 && rand.Float32() < frac { //nolint:gosec // experience, not a secret
		whole++
	}
	return whole
}

// furnaceSlot reports whether a stack request's container and slot are a slot of the
// furnace the player has open: its kind's input, or the fuel or result.
func (p *Player) furnaceSlot(container, slot byte) bool {
	w := &p.window
	if !w.open || w.inv == nil {
		return false
	}
	var kind *furnaceKind
	switch w.kind {
	case ContainerFurnace:
		kind = furnaceKindFurnace
	case ContainerBlastFurnace:
		kind = furnaceKindBlast
	case ContainerSmoker:
		kind = furnaceKindSmoker
	default:
		return false
	}
	switch container {
	case kind.ingredient:
		return slot == furnaceInput
	case protocol.ContainerFurnaceFuel:
		return slot == furnaceFuel
	case protocol.ContainerFurnaceResult:
		return slot == furnaceResult
	}
	return false
}

// furnaceFuelAllowed is BDS's check of an item put in the open furnace's fuel slot.
func (p *Player) furnaceFuelAllowed(s ItemStack) bool {
	if p.entity == nil {
		return false
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	c := p.windowChest(r)
	if c == nil || c.furnace == nil {
		return false
	}
	return p.s.fuelAllowed(r, c.furnace, s)
}
