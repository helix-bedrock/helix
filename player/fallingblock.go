package player

import (
	"maps"
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/fall"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// BDS 1.26.60's falling block actor (FallingBlockActor; its tick is
// FallingBlockNormalTickSystem, sub_BD5C480; breaking into an item sub_BD5BAF0; saving
// sub_BD5D620 and loading sub_BD5D8E0).
const (
	fallingBlockID = "minecraft:falling_block"
	// fallingHalf is half of its 0.98 box; its position is the box's middle.
	fallingHalf = 0.49

	fallingGravity    = 0.04
	fallingDrag       = 0.98
	fallingGroundDrag = 0.7
	fallingBounce     = -0.5
	// fallingMaxTime is how long it falls before it breaks into its item; after
	// fallingVoidTime ticks it is removed below the bottom of the world.
	fallingMaxTime  = 600
	fallingVoidTime = 100
	// fallingRemoveAfter is how many ticks a placed falling block stays before it goes.
	fallingRemoveAfter = 2
	// fallingPickupDelay is the pick-up delay of the item it breaks into.
	fallingPickupDelay = 10

	fallingFlags = 1<<protocol.EntityDataFlagHasGravity | 1<<protocol.EntityDataFlagHasCollision

	fallingKeyBlock = "FallingBlock"
	fallingKeyTime  = "Time"
)

// FallingBlock is a block falling as an entity: BDS's FallingBlockActor. Runs: owner of its
// entity.
type FallingBlock struct {
	s      *Host
	entity *world.Entity
	vel    mgl32.Vec3
	block  uint32
	time   int32
	// landed is set once it put its block down; it goes fallingRemoveAfter ticks later.
	landed bool
	wait   int32
	// noDrop is BDS's flag for one that vanishes instead of breaking into its item.
	noDrop bool
	gone   bool
	saved  map[string]any
	// moves is what those who see it were last told of its movement.
	moves actorMoves
}

// Entity returns its entity.
func (f *FallingBlock) Entity() *world.Entity { return f.entity }

// Block returns the block it carries.
func (f *FallingBlock) Block() uint32 { return f.block }

// SetNoDrop makes it vanish where it cannot land instead of breaking into its item.
func (f *FallingBlock) SetNoDrop(v bool) { f.noDrop = v }

// SetFalling gives the Host the falling rules; without them nothing lands. Set before any
// player joins.
func (s *Host) SetFalling(t *fall.Table) { s.falling = t }

// BlockFallEvent is a block about to start falling. Cancel it to keep the block where it is.
// Runs: owner of the region.
type BlockFallEvent struct {
	Cancellable
	Region *world.Region
	Pos    cube.Pos
	Block  uint32
}

// OnBlockFall listens to blocks starting to fall.
func OnBlockFall(s *Setup, fn func(e *BlockFallEvent), p Priority) error {
	return addListener(s, &s.h.game.blockFallEv, fn, p)
}

// StartFalling turns block at pos into a falling block, as BDS's startFalling: the entity in
// the middle of the block, then the block taken away. False if a BlockFallEvent listener
// stopped it. Runs: owner of the region.
func (s *Host) StartFalling(r *world.Region, pos cube.Pos, block uint32) bool {
	if s.game != nil && s.game.blockFallEv.Has() {
		e := BlockFallEvent{Region: r, Pos: pos, Block: block}
		s.game.blockFallEv.fire(s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	s.SpawnFallingBlock(r.Dimension(), blockCenter(pos), block)
	if s.falling != nil {
		s.falling.Lift(r, pos)
	} else {
		r.SetBlock(pos, 0, s.reg.Blocks.Air())
		r.UpdateNeighbours(pos)
	}
	return true
}

// DestroySnow removes a snow layer left with nothing to lie on, with the break particles.
// Not compared with BDS's destroy call (its drops are not known), so nothing drops. Runs:
// owner of the region.
func (s *Host) DestroySnow(r *world.Region, pos cube.Pos, block uint32) {
	r.SetBlock(pos, 0, s.reg.Blocks.Air())
	r.UpdateNeighbours(pos)
	levelEvent(r, pos, blockCenter(pos), levelEventDestroyParticles, int32(s.reg.Blocks.Hash(block))) //nolint:gosec // hashes are sent as int32
}

// SpawnFallingBlock adds a falling block carrying block with its middle at pos.
func (s *Host) SpawnFallingBlock(d *world.Dimension, pos mgl32.Vec3, block uint32) *FallingBlock {
	f := &FallingBlock{s: s, block: block}
	f.entity = d.AddEntity(f.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), pos, world.Rotation{})) //nolint:gosec // an entity ID, not a secret
	return f
}

func (f *FallingBlock) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	f.moves.start(pos, rot, actorMoveDropped, actorMoveFar)
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: f, Spawn: f.spawnPackets, Move: f.movePacket, Input: f.tick, Save: f.saveEntity,
	}
}

// spawnPackets show it: the client draws the block its variant names. Not yet compared
// with what BDS sends.
func (f *FallingBlock) spawnPackets(e *world.Entity) []packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:   int64(fallingFlags),
		protocol.EntityDataKeyVariant: int32(f.s.reg.Blocks.Hash(f.block)), //nolint:gosec // hashes are sent as int32
		protocol.EntityDataKeyWidth:   float32(2 * fallingHalf), protocol.EntityDataKeyHeight: float32(2 * fallingHalf),
		protocol.EntityDataKeyScale: float32(1),
	}
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: fallingBlockID,
		Position: e.Position(), Velocity: f.vel, EntityMetadata: meta,
	}}
}

// movePacket sends its move as BDS's actor movement sync does: clients fall it themselves
// from AddActor's velocity and their gravity, and get MoveActorDelta when it moved. Nil: the
// sync sends its own.
func (f *FallingBlock) movePacket(e *world.Entity) packet.Packet {
	f.moves.update(e, f.vel)
	return nil
}

// tick is BDS's falling block tick: once placed it waits two ticks and goes; otherwise it
// falls, and when it stops it lands or breaks into its item.
func (f *FallingBlock) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || f.gone {
		return
	}
	if f.landed {
		f.wait++
		if f.wait > fallingRemoveAfter {
			f.remove(e)
		}
		return
	}
	if f.s.checkVoid(e); f.gone || e.Region() == nil {
		return
	}
	f.time++
	onGround := f.move(r, e)
	f.vel = f.vel.Mul(fallingDrag)
	pos := e.Position()
	at := cube.Pos{itemFloor(pos[0]), itemFloor(pos[1]), itemFloor(pos[2])}
	if f.s.falling != nil && f.s.falling.StopsFalling(r, at, f.block, onGround) {
		f.vel = mgl32.Vec3{f.vel[0] * fallingGroundDrag, f.vel[1] * fallingBounce, f.vel[2] * fallingGroundDrag}
		if f.noDrop {
			f.remove(e)
			return
		}
		if f.s.falling.Land(r, at, f.block) {
			f.landed, f.wait, f.vel = true, 0, mgl32.Vec3{}
			return
		}
		f.breakApart(r, e, at)
		return
	}
	if f.time > fallingMaxTime {
		f.breakApart(r, e, at)
		return
	}
	if f.time > fallingVoidTime && pos[1] < float32(r.Dimension().Range()[0]) {
		f.remove(e)
	}
}

// move applies gravity and moves through the blocks' collision boxes. It reports whether
// it stands on something.
func (f *FallingBlock) move(r *world.Region, e *world.Entity) bool {
	f.vel[1] -= fallingGravity
	pos, hitY := e.Position(), false
	for _, axis := range [...]int{1, 0, 2} {
		d := f.s.clipMove(r, pos, fallingHalf, axis, f.vel[axis])
		if d != f.vel[axis] {
			if axis == 1 {
				hitY = true
			} else {
				f.vel[axis] = 0
			}
		}
		pos[axis] += d
	}
	onGround := hitY && f.vel[1] < 0
	if hitY {
		f.vel[1] = 0
	}
	if !e.Apply(world.Movement{Pos: pos, Rot: e.Rotation(), OnGround: onGround, Tick: e.Tick()}) {
		f.vel = mgl32.Vec3{}
	}
	return onGround
}

// breakApart is BDS's breakBlock: the block's item where it is (a snow layer's drops
// instead), the break particles, and it goes.
func (f *FallingBlock) breakApart(r *world.Region, e *world.Entity, at cube.Pos) {
	s := f.s
	var items []ItemStack
	if s.falling != nil && s.falling.Kind(f.block) == fall.Snow {
		drops := vanillaDrops
		if s.game != nil && s.game.sys.Drops != nil {
			drops = s.game.sys.Drops
		}
		items, _ = drops(s, f.block, &loot.Context{})
	} else if it, ok := s.pickedItem(f.block); ok {
		items = []ItemStack{it}
	}
	pos := e.Position()
	for _, it := range items {
		s.spawnItem(r.Dimension(), it, pos, mgl32.Vec3{}, world.Rotation{}, fallingPickupDelay, 0)
	}
	levelEvent(r, at, pos, levelEventDestroyParticles, int32(s.reg.Blocks.Hash(f.block))) //nolint:gosec // hashes are sent as int32
	f.remove(e)
}

func (f *FallingBlock) remove(e *world.Entity) {
	f.gone = true
	e.Remove()
}

// OutOfWorld removes it in the void.
func (f *FallingBlock) OutOfWorld(_ *Host, e *world.Entity) { f.remove(e) }

func (f *FallingBlock) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if f.gone || f.landed {
		return chunk.Entity{}, false
	}
	data := maps.Clone(f.saved)
	if data == nil {
		data = map[string]any{}
	}
	blocks := f.s.reg.Blocks
	pos, rot := e.Position(), e.Rotation()
	data["identifier"] = fallingBlockID
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Motion"] = []float32{f.vel[0], f.vel[1], f.vel[2]}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	data[fallingKeyBlock] = map[string]any{"name": blocks.Name(f.block), "states": blocks.States(f.block), "version": blocks.Version(f.block)}
	data[fallingKeyTime] = uint8(min(max(f.time, 0), math.MaxUint8)) //nolint:gosec // clamped, BDS saves a byte
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadFallingBlock brings back a saved falling block from its FallingBlock compound.
func (s *Host) loadFallingBlock(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	nbt, _ := saved.Data[fallingKeyBlock].(map[string]any)
	name, _ := nbt["name"].(string)
	states, _ := nbt["states"].(map[string]any)
	block, ok := s.reg.Blocks.StateID(name, states)
	if !ok {
		return false
	}
	f := &FallingBlock{s: s, block: block, saved: saved.Data}
	if v, isList := floats(saved.Data["Motion"]); isList && len(v) == 3 {
		f.vel = mgl32.Vec3{v[0], v[1], v[2]}
	}
	var rot world.Rotation
	if v, isList := floats(saved.Data["Rotation"]); isList && len(v) == 2 {
		rot = world.Rotation{Yaw: v[0], Pitch: v[1], HeadYaw: v[0]}
	}
	t, _ := saved.Data[fallingKeyTime].(uint8)
	f.time = int32(t)
	f.entity = r.AddLoadedEntity(at, f.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}, rot))
	return true
}
