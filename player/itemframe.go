package player

import (
	"maps"
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Item frames and glow item frames as BDS 1.26.60 runs them: ItemFrameBlock and its block
// actor. The research note is private.
const (
	frameBlock           = "minecraft:frame"
	glowFrameBlock       = "minecraft:glow_frame"
	frameEntityID        = "ItemFrame"
	glowFrameEntityID    = "GlowItemFrame"
	frameItemTag         = "Item"
	frameRotationTag     = "ItemRotation"
	frameDropChanceTag   = "ItemDropChance"
	frameMapBitState     = "item_frame_map_bit"
	frameFilledMap       = "minecraft:filled_map"
	frameRotationStep    = 45
	frameFullTurn        = 360
	frameMaxSignal       = 14
	frameDropDelay       = 10
	frameDefaultChance   = 1
	levelEventFrameAdd   = 1040
	levelEventFrameTake  = 1043
	levelEventFrameTurn  = 1044
	frameDropFromCorner  = 0.5
	frameNoSignal        = 0
	frameFirstSignalStep = 1
)

// isFrameBlock reports whether name is an item frame or a glow item frame.
func isFrameBlock(name string) bool { return name == frameBlock || name == glowFrameBlock }

// ItemFrame is an item frame's block entity: the item shown, how far it is turned and the
// chance it drops when it comes out. Runs: owner of its chunk.
type ItemFrame struct {
	s    *Host
	glow bool
	item ItemStack
	// rotation is in degrees, a multiple of 45 below 360.
	rotation   float32
	dropChance float32
	saved      map[string]any
}

func (s *Host) newFrame(glow bool) *ItemFrame {
	return &ItemFrame{s: s, glow: glow, dropChance: frameDefaultChance}
}

// Item returns the item shown, empty if none.
func (f *ItemFrame) Item() ItemStack { return f.item }

// Rotation returns how far the item is turned, in degrees.
func (f *ItemFrame) Rotation() float32 { return f.rotation }

// DropChance returns the chance, 0 to 1, that the item drops when it comes out.
func (f *ItemFrame) DropChance() float32 { return f.dropChance }

// SetDropChance sets the chance, 0 to 1, that the item drops when it comes out.
func (f *ItemFrame) SetDropChance(c float32) { f.dropChance = c }

// loadFrame brings a frame back as BDS reads one (0xD4197C0).
func (s *Host) loadFrame(saved map[string]any, glow bool) *ItemFrame {
	f := s.newFrame(glow)
	f.saved = maps.Clone(saved)
	if item, ok := saved[frameItemTag].(map[string]any); ok {
		f.item = s.loadedItem(item)
	}
	if v, ok := saved[frameRotationTag].(float32); ok {
		f.rotation = v
	}
	if v, ok := saved[frameDropChanceTag].(float32); ok {
		f.dropChance = v
	}
	for _, k := range [...]string{frameItemTag, frameRotationTag, frameDropChanceTag} {
		delete(f.saved, k)
	}
	return f
}

// SaveData is what BDS saves of a frame (0xD419FC0): the item, its rotation and drop chance
// only while it holds one.
func (f *ItemFrame) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(f.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = frameEntityID
	if f.glow {
		m["id"] = glowFrameEntityID
	}
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	if !f.item.Empty() {
		m[frameItemTag] = f.item.nbt(0, false)
		m[frameRotationTag] = f.rotation
		m[frameDropChanceTag] = f.dropChance
	}
	return m
}

// NetworkData is what is saved, with the item as shown (shownNBT).
func (f *ItemFrame) NetworkData(pos cube.Pos) map[string]any {
	m := f.SaveData(pos)
	if !f.item.Empty() {
		m[frameItemTag] = f.item.shownNBT()
	}
	return m
}

// ComparatorSignal is BDS's frame signal: one more than the eighths turned, up to 15, with an
// item; 0 without. The circuit package checks the frame faces the comparator's way.
func (f *ItemFrame) ComparatorSignal(*world.Region, cube.Pos) int32 {
	if f.item.Empty() {
		return frameNoSignal
	}
	return min(int32(f.rotation/frameRotationStep), frameMaxSignal) + frameFirstSignalStep
}

// setMapBit sets the frame block's map bit to whether it shows a map, as BDS does on every
// change of item.
func (f *ItemFrame) setMapBit(r *world.Region, pos cube.Pos) {
	blocks := f.s.reg.Blocks
	b, ok := r.Block(pos, 0)
	if !ok || !isFrameBlock(blocks.Name(b)) {
		return
	}
	states := maps.Clone(blocks.States(b))
	var bit uint8
	if f.item.Name == frameFilledMap {
		bit = 1
	}
	if v, _ := states[frameMapBitState].(uint8); v == bit {
		return
	}
	states[frameMapBitState] = bit
	if state, ok := blocks.StateID(blocks.Name(b), states); ok {
		r.SetBlock(pos, 0, state)
	}
}

// Put shows item in the frame, one of it, turned back to 0, as BDS's setItem (0xD41AB60).
// Runs: owner of the region.
func (f *ItemFrame) Put(r *world.Region, pos cube.Pos, item ItemStack) {
	item.Count = 1
	f.item, f.rotation = withID(item, f.s.nextStackID), 0
	f.setMapBit(r, pos)
	levelEvent(r, pos, blockVec(pos), levelEventFrameAdd, 0)
	r.SendBlockEntity(pos)
}

// Turn turns the item an eighth, as clicking a full frame does. Runs: owner of the region.
func (f *ItemFrame) Turn(r *world.Region, pos cube.Pos) {
	f.rotation = float32(math.Mod(float64(f.rotation+frameRotationStep), frameFullTurn))
	levelEvent(r, pos, blockVec(pos), levelEventFrameTurn, 0)
	r.SendBlockEntity(pos)
}

// Take takes the item out, as BDS's dropItem (0xD41A3B0, 0xD41A680): it drops at the middle of
// the block if drop is set and the frame's drop chance comes up, and with sound reports it
// to players. Runs: owner of the region.
func (f *ItemFrame) Take(r *world.Region, pos cube.Pos, drop, sound bool) {
	if f.item.Empty() {
		return
	}
	item := f.item
	f.item, f.rotation = ItemStack{}, 0
	if drop && f.dropChance > 0 && f.dropChance >= rand.Float32() { //nolint:gosec // a drop, not a secret
		at := blockVec(pos).Add(mgl32.Vec3{frameDropFromCorner, frameDropFromCorner, frameDropFromCorner})
		f.s.spawnItem(r.Dimension(), item, at, mgl32.Vec3{}, world.Rotation{}, frameDropDelay, 0)
	}
	f.setMapBit(r, pos)
	if sound {
		levelEvent(r, pos, blockVec(pos), levelEventFrameTake, 0)
	}
	r.SendBlockEntity(pos)
}

// BlockEntityRemoved drops the item of a frame that is gone, as the block's removal does
// (0xCAEB620), without the sound.
func (f *ItemFrame) BlockEntityRemoved(r *world.Region, pos cube.Pos) {
	if f.item.Empty() {
		return
	}
	item := f.item
	f.item = ItemStack{}
	if f.dropChance > 0 && f.dropChance >= rand.Float32() { //nolint:gosec // a drop, not a secret
		at := blockVec(pos).Add(mgl32.Vec3{frameDropFromCorner, frameDropFromCorner, frameDropFromCorner})
		f.s.spawnItem(r.Dimension(), item, at, mgl32.Vec3{}, world.Rotation{}, frameDropDelay, 0)
	}
}

// FrameAt returns the item frame at pos, if one is there. Runs: owner of the region.
func FrameAt(r *world.Region, pos cube.Pos) (*ItemFrame, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	f, ok := be.(*ItemFrame)
	return f, ok
}

// useFrame is BDS's frame interaction (0xCAEAF10): an empty frame takes one of the held item,
// which is used up outside creative; a full one turns its item. An empty hand on an empty
// frame does not take the click.
func (p *Player) useFrame(r *world.Region, u *BlockUse) bool {
	f, ok := FrameAt(r, u.Pos)
	if !ok {
		return false
	}
	if !f.item.Empty() {
		f.Turn(r, u.Pos)
		return true
	}
	if u.Item.Empty() {
		return false
	}
	f.Put(r, u.Pos, u.Item)
	if !p.creative() {
		p.useHeld()
	}
	return true
}

// punchFrame is ItemFrameBlock's part of starting to break it (0xCAEBD80): a frame holding
// an item gives it up, dropping it unless the player is in creative, and does not break.
// True if it gave one up.
func (p *Player) punchFrame(r *world.Region, pos cube.Pos) bool {
	f, ok := FrameAt(r, pos)
	if !ok || f.item.Empty() {
		return false
	}
	f.Take(r, pos, !p.creative(), true)
	return true
}
