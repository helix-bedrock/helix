package player

import (
	"maps"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Decorated pots as BDS 1.26.60: DecoratedPotBlockActor (saved as "DecoratedPot") with its
// save 0xD408E60 (sherds when not all bricks, item, animation), its tick 0xD409070 (the
// animation ends after 10 ticks for a wobble, 7 for an insert) and tryAddItem 0xD409480; the
// block's playerWillDestroy 0xCB102F0 (break or shatter sound). The rest follows
// DecoratedPotBlock and DecoratedPotBlockActor in the 1.21.3 names build: use adds the held
// item, onRemove drops what is inside, the dropped pot item keeps the sherds.
//
// Not built: hoppers and comparators (BDS's pot is a one-slot container), the loot table a
// generated pot unpacks on first use, and a projectile shattering a pot (onProjectileHit
// 0xCB10990, behind a game rule).
const (
	decoratedPotBlock     = "minecraft:decorated_pot"
	decoratedPotEntityID  = "DecoratedPot"
	decoratedPotSherdsTag = "sherds"
	decoratedPotItemTag   = "item"
	decoratedPotAnimTag   = "animation"
	decoratedPotSherds    = 4
	decoratedPotBrick     = "minecraft:brick"

	// The animations: none, the wobble of a refused item, the insert of an added one.
	potAnimNone   = 0
	potAnimWobble = 1
	potAnimInsert = 2
	// potWobbleTicks and potInsertTicks are how long each lasts (BDS: ticks/10 > 1, ticks/7 > 1).
	potWobbleTicks = 10
	potInsertTicks = 7

	// The insert sound's pitch is (count/2/max + 0.7) * 1000; potPitchNoItem is BDS's max
	// when the item has no type.
	potPitchBase   = 0.7
	potPitchScale  = 1000
	potPitchNoItem = 255
	// The insert particles rise from above the pot's middle.
	potParticleUp = 1.25

	// silkTouchID is the enchantment that keeps a pot whole.
	silkTouchID = 16
	tagTrident  = "minecraft:is_trident"
	tagTool     = "minecraft:is_tool"
)

// DecoratedPot is a decorated pot's block entity: its four sherds (back, left, right, front
// as BDS saves them), the one stack it holds and its animation. Get it with
// [DecoratedPotAt]. Runs: owner of its chunk.
type DecoratedPot struct {
	s      *Host
	saved  map[string]any
	sherds [decoratedPotSherds]string
	item   ItemStack
	anim   uint8
	ticks  int32
	chunk  world.ChunkHandle
}

// DecoratedPotAt returns the decorated pot block entity at pos. Runs: owner of the chunk.
func DecoratedPotAt(r *world.Region, pos cube.Pos) (*DecoratedPot, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	dp, ok := be.(*DecoratedPot)
	return dp, ok
}

// newDecoratedPot is a pot of four bricks with nothing in it.
func (s *Host) newDecoratedPot() *DecoratedPot {
	dp := &DecoratedPot{s: s}
	for i := range dp.sherds {
		dp.sherds[i] = decoratedPotBrick
	}
	return dp
}

// Item returns what the pot holds.
func (dp *DecoratedPot) Item() ItemStack { return dp.item }

// Sherds returns the pot's four sherd items.
func (dp *DecoratedPot) Sherds() [decoratedPotSherds]string { return dp.sherds }

// SetItem sets what the pot holds; clients are told.
func (dp *DecoratedPot) SetItem(r *world.Region, pos cube.Pos, item ItemStack) {
	dp.item = item
	dp.chunk.Changed()
	r.SendBlockEntity(pos)
}

// BindBlockEntity keeps the chunk the pot is in.
func (dp *DecoratedPot) BindBlockEntity(h world.ChunkHandle) { dp.chunk = h }

// TickBlockEntity is BDS's tick: a running animation ends after its length.
func (dp *DecoratedPot) TickBlockEntity(_ *world.Region, _ cube.Pos) {
	if dp.anim == potAnimNone {
		return
	}
	dp.ticks++
	limit := int32(potWobbleTicks)
	if dp.anim == potAnimInsert {
		limit = potInsertTicks
	}
	if dp.ticks > limit {
		dp.anim, dp.ticks = potAnimNone, 0
	}
}

// loadDecoratedPot brings back a saved pot.
func (s *Host) loadDecoratedPot(saved map[string]any) *DecoratedPot {
	dp := s.newDecoratedPot()
	dp.saved = maps.Clone(saved)
	dp.setSherds(saved)
	if m, ok := saved[decoratedPotItemTag].(map[string]any); ok {
		dp.item = withID(itemFromNBT(m), s.nextStackID)
	}
	dp.anim, _ = saved[decoratedPotAnimTag].(uint8)
	for _, k := range [...]string{decoratedPotSherdsTag, decoratedPotItemTag, decoratedPotAnimTag} {
		delete(dp.saved, k)
	}
	return dp
}

// setSherds reads a sherds list of four names, as BDS's tryGetSherdsFromTag; anything else
// leaves the sherds as they are.
func (dp *DecoratedPot) setSherds(tag map[string]any) {
	list, ok := tag[decoratedPotSherdsTag].([]any)
	if !ok || len(list) < decoratedPotSherds {
		return
	}
	for i := range dp.sherds {
		if name, ok := list[i].(string); ok {
			dp.sherds[i] = name
		}
	}
}

// defaultSherds reports whether every side is a brick.
func (dp *DecoratedPot) defaultSherds() bool {
	return !slices.ContainsFunc(dp.sherds[:], func(s string) bool { return s != decoratedPotBrick })
}

// sherdList is the sherds as BDS saves them.
func (dp *DecoratedPot) sherdList() []any {
	out := make([]any, decoratedPotSherds)
	for i, s := range dp.sherds {
		out[i] = s
	}
	return out
}

// SaveData is what BDS saves of a pot: sherds unless all bricks, the item, the animation.
func (dp *DecoratedPot) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(dp.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = decoratedPotEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	if !dp.defaultSherds() {
		m[decoratedPotSherdsTag] = dp.sherdList()
	}
	m[decoratedPotItemTag] = dp.item.nbt(0, false)
	m[decoratedPotAnimTag] = dp.anim
	return m
}

// NetworkData is the same: the client draws the sherds, the item and the animation from it.
func (dp *DecoratedPot) NetworkData(pos cube.Pos) map[string]any { return dp.SaveData(pos) }

// BlockEntityRemoved is BDS's onRemove: what the pot held drops.
func (dp *DecoratedPot) BlockEntityRemoved(r *world.Region, pos cube.Pos) {
	if dp.item.Empty() {
		return
	}
	dp.s.spillSlots(r, []ItemStack{dp.item}, blockVec(pos), false, chestPickupDelay)
	dp.item = ItemStack{}
}

// placedFrom gives a placed pot the sherds of the item it came from.
func (dp *DecoratedPot) placedFrom(pl *Placement) {
	if pl.Item.Tag != nil {
		dp.setSherds(pl.Item.Tag)
		dp.chunk.Changed()
	}
}

// decoratedPotDrops gives a dropped decorated pot item its sherds, as BDS's asItemInstance
// puts the block entity's sherds in the item's data.
func decoratedPotDrops(r *world.Region, pos cube.Pos, items []ItemStack) []ItemStack {
	dp, ok := DecoratedPotAt(r, pos)
	if !ok || dp.defaultSherds() {
		return items
	}
	for i, it := range items {
		if it.Name == decoratedPotBlock {
			items[i] = withTag(it, decoratedPotSherdsTag, dp.sherdList())
		}
	}
	return items
}

// useDecoratedPot is DecoratedPotBlock::use and the block entity's tryAddItem: one of the
// held item goes in when the pot is empty, or holds the same item with room left; otherwise
// the pot wobbles. Either way the pot plays its sound and posts block_change. Always taken.
func (p *Player) useDecoratedPot(r *world.Region, u *BlockUse) bool {
	dp, ok := DecoratedPotAt(r, u.Pos)
	if !ok {
		return true
	}
	held := p.inventory.Held()
	next, ok := p.potAdd(dp, held)
	if ok {
		dp.anim, dp.ticks = potAnimInsert, 0
		dp.SetItem(r, u.Pos, next)
		p.potInsertFeedback(r, u.Pos, next)
		if !p.creative() {
			p.useHeld()
		}
	} else {
		dp.anim, dp.ticks = potAnimWobble, 0
		dp.chunk.Changed()
		r.SendBlockEntity(u.Pos)
		playSound(r, u.Pos, soundEvent{name: packet.SoundEventDecoratedPotInsertFail, pos: blockVec(u.Pos), extra: -1}, nil)
	}
	p.s.postBlockGameEvent(r, gameEventBlockChange, u.Pos, p, 0, false)
	return true
}

// potAdd is what the pot holds after one of held goes in, and whether it can.
func (p *Player) potAdd(dp *DecoratedPot, held ItemStack) (ItemStack, bool) {
	if held.Empty() || itemLocked(held) {
		return ItemStack{}, false
	}
	if dp.item.Empty() {
		one := p.s.freshStack(held)
		one.Count = 1
		return one, true
	}
	if !dp.item.stacksWith(held) || dp.item.Count >= dp.item.maxStack(p.s.reg) {
		return ItemStack{}, false
	}
	next := dp.item
	next.Count++
	return next, true
}

// potInsertFeedback is BDS's _onInsertFeedback: the insert sound, higher the fuller the pot,
// and the dust plume above it.
func (p *Player) potInsertFeedback(r *world.Region, pos cube.Pos, item ItemStack) {
	most := float32(potPitchNoItem)
	if m := item.maxStack(p.s.reg); m > 0 {
		most = float32(m)
	}
	pitch := int32((float32(item.Count)*0.5/most + potPitchBase) * potPitchScale)
	playSound(r, pos, soundEvent{name: packet.SoundEventDecoratedPotInsert, pos: blockVec(pos), extra: pitch}, nil)
	at := blockVec(pos).Add(mgl32.Vec3{0.5, potParticleUp, 0.5})
	if pk := levelEventGeneric(packet.LevelEventDustPlume, map[string]any{"x": at[0], "y": at[1], "z": at[2]}); pk != nil {
		r.Broadcast(pos, pk, nil)
	}
}

// decoratedPotBreakSound is playerWillDestroy's sound: a trident, or a tool without silk
// touch, out of creative shatters the pot; anything else breaks it.
func (p *Player) decoratedPotBreakSound(r *world.Region, pos cube.Pos, block uint32) {
	sound := packet.SoundEventBreakDecoratedPot
	if !p.creative() {
		held := p.inventory.Held()
		tags := p.s.craftItem(held).Tags
		if slices.Contains(tags, tagTrident) || (slices.Contains(tags, tagTool) && !hasEnchant(held, silkTouchID)) {
			sound = packet.SoundEventShatterDecoratedPot
		}
	}
	playSound(r, pos, p.s.blockSound(sound, blockVec(pos), block), nil)
}
