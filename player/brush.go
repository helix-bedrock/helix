package player

import (
	"maps"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Brushing suspicious sand and gravel, as BDS 1.26.60 does it (research note: memory
// research-brushing.md):
//   - The brush's use on a block (BrushItem::_useOn, 0xC9D78D0) runs on every use the client
//     sends while the button is held. On air it does nothing. On a brushable block (block actor
//     type 52) it brushes it; when that finishes the block the brush wears 1 (hurtAndBreak,
//     none in creative) and, if the brush broke, nothing more. Then on any block, at most once
//     every 8 ticks per player (BrushEffectsCooldownComponent), the brush sound (LevelSoundEvent
//     462, "brush") plays at the block's centre with the default state of the block's type. The
//     dust is the client's.
//   - BrushableBlockActor::brush (0xD4457C0): one stroke per 10 ticks (the next stroke is due 10
//     ticks after the last was due, or after now on the first); the first stroke keeps the face
//     brushed and unpacks the loot table into the one slot; the count goes up by one and at 10
//     brushing completes; each stroke queues a block tick in 40 and marks it busy for 40 ticks.
//   - Completing (0xD445970): the item comes out on the side brushed, at that block's centre,
//     standing still, with throw time 10; brush_completed (463) plays with the block; level
//     event 2021 (destroy particles, no sound) at the block; the block becomes sand or gravel
//     (BrushableBlockUtils::getBrushedBlock); game event block_destroy at the block's bottom
//     centre.
//   - Its queued tick (BrushableBlockActor::update via BrushableBlock::tick): while busy it ticks
//     again in 20; else the count drops by 2 (to 0 at least) with another tick in 10; at 0 the
//     brushed face is forgotten.
//   - brushed_progress follows the count (onChanged): 0, 1 for 1-2, 2 for 3-5, 3 from 6, set
//     with block_change.
//
// Helix keeps the block's queued ticks on the block entity (BDS's queue does not merge ticks,
// so one is kept per stroke). They are not saved: a block left with a count decays from its
// first tick after loading. Not built: suspicious blocks falling (BrushableBlock is a falling
// block that breaks when it lands), other players seeing the brushing arm (startUsingItem).
const (
	brushItem             = "minecraft:brush"
	brushableEntityID     = "BrushableBlock"
	nameSuspiciousSand    = "minecraft:suspicious_sand"
	nameSuspiciousGravel  = "minecraft:suspicious_gravel"
	nameSand              = "minecraft:sand"
	nameGravel            = "minecraft:gravel"
	stateBrushedProgress  = "brushed_progress"
	brushTypeTag          = "type"
	brushCountTag         = "brush_count"
	brushDirectionTag     = "brush_direction"
	brushItemTag          = "item"
	brushLootTag          = "LootTable"
	brushLootSeedTag      = "LootTableSeed"
	brushStrokeTicks      = 10
	brushStrokesToFinish  = 10
	brushBusyTicks        = 40
	brushBusyRetickTicks  = 20
	brushDecayTicks       = 10
	brushDecayStep        = 2
	brushNoDirection      = 6
	brushSoundCooldown    = 8
	brushWear             = 1
	brushThrowTime        = 10
	brushMaxPendingTicks  = 64
	soundEventBrushedDone = packet.SoundEventBrushCompleted
)

// brushedBlocks is what each suspicious block turns into once brushed.
var brushedBlocks = map[string]string{nameSuspiciousSand: nameSand, nameSuspiciousGravel: nameGravel}

// isBrushable reports whether a block type is suspicious sand or gravel.
func isBrushable(name string) bool { _, ok := brushedBlocks[name]; return ok }

// Brushable is a suspicious block's block entity: BDS's BrushableBlockActor.
type Brushable struct {
	s     *Host
	saved map[string]any
	item  ItemStack
	count int32
	dir   uint8
	// unpacked is whether the loot table was rolled this load (BDS's +717).
	unpacked bool
	// nextStroke is the tick the next stroke may come after; stroked whether one came.
	nextStroke int64
	stroked    bool
	// busyUntil is the last tick of the busy time after a stroke.
	busyUntil int64
	// pending are its queued block ticks, by the tick they are due.
	pending []int64
	// decayOnLoad asks for a tick once it ticks, for a count loaded without its ticks.
	decayOnLoad bool
	chunk       world.ChunkHandle
}

// newBrushable is the block entity of a suspicious block just placed.
func (s *Host) newBrushable() *Brushable { return &Brushable{s: s, dir: brushNoDirection} }

// loadBrushable brings back a suspicious block's block entity as BDS's load does.
func (s *Host) loadBrushable(saved map[string]any) *Brushable {
	b := &Brushable{s: s, saved: maps.Clone(saved), dir: brushNoDirection}
	b.count, _ = saved[brushCountTag].(int32)
	if d, ok := saved[brushDirectionTag].(uint8); ok {
		b.dir = d
	}
	if item, ok := saved[brushItemTag].(map[string]any); ok {
		b.item = s.loadedItem(item)
	}
	b.decayOnLoad = b.count > 0
	for _, k := range [...]string{brushCountTag, brushDirectionTag, brushItemTag} {
		delete(b.saved, k)
	}
	return b
}

// BindBlockEntity keeps the chunk it is in.
func (b *Brushable) BindBlockEntity(h world.ChunkHandle) { b.chunk = h }

// SaveData is what BDS saves of a brushable block.
func (b *Brushable) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(b.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = brushableEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[brushCountTag] = b.count
	m[brushDirectionTag] = b.dir
	if !b.item.Empty() {
		m[brushItemTag] = b.item.nbt(0, false)
	}
	return m
}

// NetworkData is what is saved, with the item as shown (shownNBT): the client shows the
// item and the face from it.
func (b *Brushable) NetworkData(pos cube.Pos) map[string]any {
	m := b.SaveData(pos)
	if !b.item.Empty() {
		m[brushItemTag] = b.item.shownNBT()
	}
	return m
}

// changed marks it for saving and tells the clients.
func (b *Brushable) changed(r *world.Region, pos cube.Pos) {
	b.chunk.Changed()
	r.SendBlockEntity(pos)
}

// unpack rolls the loot table into its slot, as RandomizableBlockActorContainer's unpack: the
// table is used once and forgotten.
func (b *Brushable) unpack(r *world.Region, pos cube.Pos) {
	table, _ := b.saved[brushLootTag].(string)
	delete(b.saved, brushLootTag)
	delete(b.saved, brushLootSeedTag)
	s := b.s
	if table == "" || s.entityLoot == nil || !s.entityLoot.Has(table) {
		return
	}
	c := &loot.EntityContext{Random: lootRandom{}, Difficulty: s.level.Difficulty}
	for _, d := range s.entityLoot.Roll(table, c, nil) {
		if stacks := s.lootStacks(d); len(stacks) > 0 {
			b.item = s.freshStack(stacks[0])
			break
		}
	}
	b.changed(r, pos)
}

// brush is one stroke on face; true when it finished the block.
func (b *Brushable) brush(r *world.Region, pos cube.Pos, face uint8) bool {
	now := r.CurrentTick()
	if b.stroked && now <= b.nextStroke {
		return false
	}
	if b.dir == brushNoDirection {
		b.dir = face
	}
	if !b.stroked {
		b.stroked, b.nextStroke = true, now
	}
	b.nextStroke += brushStrokeTicks
	if !b.unpacked {
		b.unpack(r, pos)
		b.unpacked = true
	}
	b.count++
	if b.count >= brushStrokesToFinish {
		b.complete(r, pos)
		return true
	}
	b.queueTick(now + brushBusyTicks)
	b.busyUntil = now + brushBusyTicks
	b.progressChanged(r, pos)
	return false
}

// queueTick adds a queued tick due at tick.
func (b *Brushable) queueTick(tick int64) {
	if len(b.pending) < brushMaxPendingTicks {
		b.pending = append(b.pending, tick)
	}
}

// complete throws out the item and turns the block back into sand or gravel.
func (b *Brushable) complete(r *world.Region, pos cube.Pos) {
	s := b.s
	if !b.item.Empty() {
		out := pos
		if b.dir < brushNoDirection {
			out = pos.Side(cube.Face(b.dir))
		}
		s.spawnItem(r.Dimension(), s.freshStack(b.item), blockCenter(out), mgl32.Vec3{}, world.Rotation{}, dropPickupDelay, brushThrowTime)
		b.item = ItemStack{}
	}
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	blocks := s.reg.Blocks
	playSound(r, pos, s.blockSound(soundEventBrushedDone, blockCenter(pos), block), nil)
	levelEvent(r, pos, blockVec(pos), packet.LevelEventParticlesDestroyBlockNoSound, int32(blocks.Hash(block))) //nolint:gosec // hashes are sent as int32
	if next, ok := blocks.Block(brushedBlocks[blocks.Name(block)]); ok && r.SetBlock(pos, 0, next) {
		r.UpdateNeighbours(pos)
	}
	s.postGameEvent(r, gameEventBlockDestroy, blockVec(pos).Add(mgl32.Vec3{0.5, 0, 0.5}), nil, 0, false)
}

// progressChanged is onChanged: brushed_progress follows the count.
func (b *Brushable) progressChanged(r *world.Region, pos cube.Pos) {
	b.changed(r, pos)
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	blocks := b.s.reg.Blocks
	progress := brushedProgress(b.count)
	states := blocks.States(block)
	if have, _ := states[stateBrushedProgress].(int32); have == progress {
		return
	}
	next := maps.Clone(states)
	next[stateBrushedProgress] = progress
	id, ok := blocks.StateID(blocks.Name(block), next)
	if !ok || !r.SetBlock(pos, 0, id) {
		return
	}
	r.UpdateNeighbours(pos)
	b.s.postBlockGameEvent(r, gameEventBlockChange, pos, nil, block, true)
}

// brushedProgress is the brushed_progress state of a count.
func brushedProgress(count int32) int32 {
	switch {
	case count <= 0:
		return 0
	case count < 3:
		return 1
	case count < 6:
		return 2
	}
	return 3
}

// TickBlockEntity runs its queued ticks that are due.
func (b *Brushable) TickBlockEntity(r *world.Region, pos cube.Pos) {
	now := r.CurrentTick()
	if b.decayOnLoad {
		b.decayOnLoad = false
		b.queueTick(now + brushDecayTicks)
	}
	for i := 0; i < len(b.pending); {
		if b.pending[i] > now {
			i++
			continue
		}
		b.pending[i] = b.pending[len(b.pending)-1]
		b.pending = b.pending[:len(b.pending)-1]
		b.update(r, pos, now)
	}
}

// update is one queued tick (BrushableBlockActor::update).
func (b *Brushable) update(r *world.Region, pos cube.Pos, now int64) {
	if now <= b.busyUntil {
		b.queueTick(now + brushBusyRetickTicks)
		return
	}
	if b.count == 0 {
		b.dir = brushNoDirection
		return
	}
	b.count = max(b.count-brushDecayStep, 0)
	b.queueTick(now + brushDecayTicks)
	b.progressChanged(r, pos)
}

// useBrushOn is the brush's use on a block. False on air.
func (p *Player) useBrushOn(r *world.Region, clicked cube.Pos, face int32) bool {
	block, ok := r.Block(clicked, 0)
	if !ok || block == p.s.reg.Blocks.Air() {
		return false
	}
	if e, ok := r.BlockEntity(clicked); ok && face >= 0 && face < int32(brushNoDirection) {
		if b, ok := e.(*Brushable); ok && b.brush(r, clicked, uint8(face)) { //nolint:gosec // checked above
			if !p.creative() {
				p.hurtHeld(brushWear)
			}
			if p.inventory.Held().Empty() {
				p.flush()
				return true
			}
		}
	}
	now := r.CurrentTick()
	if now > p.brushSoundAt {
		blocks := p.s.reg.Blocks
		if def, ok := blocks.Block(blocks.Name(block)); ok {
			playSound(r, clicked, p.s.blockSound(packet.SoundEventBrush, blockCenter(clicked), def), nil)
		}
		p.brushSoundAt = now + brushSoundCooldown
	}
	p.flush()
	return true
}
