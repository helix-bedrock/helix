package player

import (
	"maps"
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// An item actor's numbers, from BDS 1.26.60.
const (
	itemHalfSize = 0.125
	// itemLifetime is how many ticks an item stays in the world.
	itemLifetime = 6000
	// itemMergeInterval is how often, in ticks, an item looks for items to
	// join.
	itemMergeInterval = 25
	// itemMergeReach is how far past its box an item joins others.
	itemMergeReach = 0.5
	// itemNeverPickedUp is a pick-up delay that never counts down.
	itemNeverPickedUp = 0xFFFF
	// itemRestSpeed is the squared speed under which an item on the ground
	// moves only every fourth tick.
	itemRestSpeed = 0.0001
	itemRestTicks = 4

	itemGravity        = 0.04
	itemAirDrag        = 0.98
	itemGroundDrag     = 0.58800006
	itemWaterDrag      = 0.99
	itemLavaDrag       = 0.95
	itemBuoyancyTarget = 0.06
	itemBuoyancyAdd    = 0.0005
	itemBuoyancyRate   = 0.01

	// itemIdentifier is what an item is saved as; itemHealth is the health
	// BDS gives a new one.
	itemIdentifier = "minecraft:item"
	itemHealth     = 5

	// itemUniqueIDBase keeps items' unique IDs apart from players', which
	// come from saved data or small runtime IDs.
	itemUniqueIDBase = int64(1) << 48
	// liquidFalling is the bit of liquid_depth that marks a falling liquid.
	liquidFalling = 8
)

// ItemActor is an item lying in the world. Its stack is the server's: it was
// taken from a slot the server checked and is never replaced by anything a
// client sends. Runs: owner of its entity.
//
// First pass from BDS 1.26.60: items do not burn in lava or fire and are not
// pushed out of a block placed on them.
type ItemActor struct {
	s      *Host
	entity *world.Entity
	stack  ItemStack
	vel    mgl32.Vec3

	age, pickupDelay, throwTime int32
	onGround, taken             bool
	// saved keeps what BDS saved for the item that Helix does not use (its
	// health, owner and the actor fields), written back as it was.
	saved map[string]any
}

// Stack returns the item's stack.
func (i *ItemActor) Stack() ItemStack { return i.stack }

// Entity returns the item's entity.
func (i *ItemActor) Entity() *world.Entity { return i.entity }

// spawnItem adds an item to the dimension.
func (s *Host) spawnItem(d *world.Dimension, stack ItemStack, pos, vel mgl32.Vec3, rot world.Rotation, pickupDelay, throwTime int32) *ItemActor {
	// A stack in the world has no slot, so no network ID the client names.
	stack.netID = 0
	i := &ItemActor{s: s, stack: stack, vel: vel, pickupDelay: pickupDelay, throwTime: throwTime}
	// Random, since items saved by an earlier run keep their IDs.
	i.entity = d.AddEntity(i.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), pos, rot)) //nolint:gosec // an entity ID, not a secret
	return i
}

// config is the item's entity.
func (i *ItemActor) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: i, Spawn: i.spawnPackets, Move: i.movePacket, Input: i.tick, Save: i.saveEntity,
	}
}

// saveEntity is the item as BDS saves it: the actor's fields, then Health, Age
// and the Item. The pick-up delays are not saved, so they end on a reload.
func (i *ItemActor) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if i.taken || i.stack.Empty() {
		return chunk.Entity{}, false
	}
	data := maps.Clone(i.saved)
	if data == nil {
		data = map[string]any{"Health": int16(itemHealth)}
	}
	pos, rot := e.Position(), e.Rotation()
	var onGround uint8
	if i.onGround {
		onGround = 1
	}
	data["identifier"] = itemIdentifier
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Motion"] = []float32{i.vel[0], i.vel[1], i.vel[2]}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	data["OnGround"] = onGround
	data["Age"] = int16(i.age) //nolint:gosec // never past the lifetime
	data["Item"] = i.stack.nbt(0, false)
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadEntity is the world's EntityLoader: it brings back items, as BDS loads
// their Age and Item, with no pick-up delays. Anything else stays saved until
// the engine runs it.
func (s *Host) loadEntity(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
	if id, _ := saved.Data["identifier"].(string); id != itemIdentifier {
		return false
	}
	item, ok := saved.Data["Item"].(map[string]any)
	pos, okPos := floats(saved.Data["Pos"])
	if !ok || !okPos || len(pos) != 3 {
		return false
	}
	stack := itemFromNBT(item)
	if stack.Empty() {
		return false
	}
	i := &ItemActor{s: s, stack: stack, saved: saved.Data}
	if v, isList := floats(saved.Data["Motion"]); isList && len(v) == 3 {
		i.vel = mgl32.Vec3{v[0], v[1], v[2]}
	}
	var rot world.Rotation
	if v, isList := floats(saved.Data["Rotation"]); isList && len(v) == 2 {
		rot = world.Rotation{Yaw: v[0], Pitch: v[1], HeadYaw: v[0]}
	}
	age, _ := saved.Data["Age"].(int16)
	i.age = int32(age)
	onGround, _ := saved.Data["OnGround"].(uint8)
	i.onGround = onGround != 0
	i.entity = r.AddLoadedEntity(at, i.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}, rot))
	return true
}

// spawnPackets show the item to a player. The data is a first pass, not yet
// checked against what BDS sends.
func (i *ItemActor) spawnPackets(e *world.Entity) []packet.Packet {
	return []packet.Packet{&packet.AddItemActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), Item: i.stack.network(i.s.reg),
		Position: e.Position(), Velocity: i.vel,
		EntityMetadata: protocol.EntityMetadata{
			protocol.EntityDataKeyFlags:  int64(1<<protocol.EntityDataFlagHasGravity | 1<<protocol.EntityDataFlagHasCollision),
			protocol.EntityDataKeyWidth:  float32(2 * itemHalfSize),
			protocol.EntityDataKeyHeight: float32(2 * itemHalfSize),
			protocol.EntityDataKeyScale:  float32(1),
		},
	}}
}

// movePacket moves the item for the players who see it.
func (i *ItemActor) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	var flags byte
	if e.OnGround() {
		flags = packet.MoveFlagOnGround
	}
	return &packet.MoveActorAbsolute{
		EntityRuntimeID: e.RuntimeID(), Flags: flags, Position: e.Position(), Rotation: mgl32.Vec3{rot.Pitch, rot.Yaw, rot.HeadYaw},
	}
}

// tick is BDS's item actor tick: delays, liquids, merging, movement, age.
func (i *ItemActor) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || i.taken {
		return
	}
	if i.pickupDelay > 0 && i.pickupDelay != itemNeverPickedUp {
		i.pickupDelay--
	}
	if i.throwTime > 0 {
		i.throwTime--
	}
	i.applyFlow(r, e.Position())
	gravity, inWater := i.liquidForces(r, e.Position())
	if i.age > 0 && i.age%itemMergeInterval == 0 {
		i.mergeNearby(r, e.Position())
	}
	if i.taken {
		return
	}
	rest := i.onGround && i.vel.Dot(i.vel) <= itemRestSpeed && !inWater && i.age%itemRestTicks != 0
	if !rest {
		if gravity {
			i.vel[1] -= itemGravity
		}
		i.moveAndDrag(r, e)
	}
	i.age++
	if i.age >= itemLifetime {
		i.taken = true
		e.Remove()
	}
}

// liquidForces applies water's and lava's pull to the velocity. It returns
// whether gravity applies and whether the item is in water.
func (i *ItemActor) liquidForces(r *world.Region, pos mgl32.Vec3) (gravity, inWater bool) {
	at := floorPos(pos)
	k, falling := liquidAt(i.s.reg, r, at)
	if k == 0 || falling {
		return true, k == registry.MaterialWater
	}
	drag := float32(itemWaterDrag)
	if k == registry.MaterialLava {
		drag = itemLavaDrag
	}
	y := i.vel[1] + itemBuoyancyAdd
	i.vel = mgl32.Vec3{i.vel[0] * drag, (itemBuoyancyTarget-y)*itemBuoyancyRate + y, i.vel[2] * drag}
	return false, k == registry.MaterialWater
}

// liquidAt returns the Material of the liquid at pos (0 for none) and
// whether it is falling, in either block layer.
func liquidAt(reg *registry.Registry, r *world.Region, pos cube.Pos) (material uint8, falling bool) {
	for layer := uint8(0); layer < 2; layer++ {
		b, ok := r.Block(pos, layer)
		if !ok {
			continue
		}
		m := reg.Blocks.Properties(b).Material
		if m == registry.MaterialWater || m == registry.MaterialLava {
			depth, _ := reg.Blocks.States(b)["liquid_depth"].(int32)
			return m, depth&liquidFalling != 0
		}
	}
	return 0, false
}

// mergeNearby joins the item with the items around it.
func (i *ItemActor) mergeNearby(r *world.Region, pos mgl32.Vec3) {
	reach := float32(itemHalfSize + itemMergeReach)
	var buf [16]*world.Entity
	for _, o := range r.AppendEntitiesIn(buf[:0], pos[0]-reach, pos[2]-reach, pos[0]+reach, pos[2]+reach) {
		other, ok := o.Owner().(*ItemActor)
		if !ok || other == i || other.taken {
			continue
		}
		if p := o.Position(); boxesApart(pos, p, 2*itemHalfSize+itemMergeReach) {
			continue
		}
		if i.merge(r, other) && i.taken {
			return
		}
	}
}

// boxesApart reports whether two item positions are further apart than d on
// any axis.
func boxesApart(a, b mgl32.Vec3, d float32) bool {
	for n := range 3 {
		if diff := a[n] - b[n]; diff > d || diff < -d {
			return true
		}
	}
	return false
}

// merge joins two items as BDS does: the larger stack stays, the other on a
// tie, if the stacks match and fit one slot.
func (i *ItemActor) merge(r *world.Region, o *ItemActor) bool {
	if i.pickupDelay == itemNeverPickedUp || o.pickupDelay == itemNeverPickedUp || !o.stack.stacksWith(i.stack) {
		return false
	}
	keep, gone := o, i
	if o.stack.Count < i.stack.Count {
		keep, gone = i, o
	}
	if int(keep.stack.Count)+int(gone.stack.Count) > int(keep.stack.maxStack(i.s.reg)) {
		return false
	}
	keep.stack.Count += gone.stack.Count
	keep.pickupDelay = max(keep.pickupDelay, gone.pickupDelay)
	keep.age = min(keep.age, gone.age)
	gone.taken = true
	gone.entity.Remove()
	keep.sendStackSize(r)
	return true
}

// sendStackSize tells the players near the item its new count.
func (i *ItemActor) sendStackSize(r *world.Region) {
	r.Broadcast(floorPos(i.entity.Position()), &packet.ActorEvent{
		EntityRuntimeID: i.entity.RuntimeID(), EventType: packet.ActorEventUpdateStackSize, EventData: int32(i.stack.Count),
	}, nil)
}

// moveAndDrag moves the item by its velocity through the blocks' collision
// boxes, then slows it by the air, or the block under it on the ground.
func (i *ItemActor) moveAndDrag(r *world.Region, e *world.Entity) {
	pos, hitY := i.collide(r, e.Position())
	onGround := hitY && i.vel[1] < 0
	if hitY {
		i.vel[1] = 0
	}
	i.onGround = onGround
	if !e.Apply(world.Movement{Pos: pos, Rot: e.Rotation(), OnGround: onGround, Tick: e.Tick()}) {
		i.vel = mgl32.Vec3{}
		return
	}
	drag := float32(itemAirDrag)
	if hitY {
		drag = itemGroundDrag
		below := cube.Pos{itemFloor(pos[0]), itemFloor(pos[1]-itemHalfSize) - 1, itemFloor(pos[2])}
		if b, ok := r.Block(below, 0); ok && b != i.s.reg.Blocks.Air() {
			drag = i.s.reg.Blocks.Properties(b).Friction * itemAirDrag
		}
	}
	i.vel = mgl32.Vec3{i.vel[0] * drag, i.vel[1] * itemAirDrag, i.vel[2] * drag}
}

// collide returns where the item ends up, clipping y, then x, then z against
// the blocks, and stops the velocity on x and z where it hit. hitY reports a
// hit on the y axis; the caller still needs the velocity's sign.
func (i *ItemActor) collide(r *world.Region, pos mgl32.Vec3) (out mgl32.Vec3, hitY bool) {
	for _, axis := range [...]int{1, 0, 2} {
		d := i.clip(r, pos, axis, i.vel[axis])
		if d != i.vel[axis] {
			if axis == 1 {
				hitY = true
			} else {
				i.vel[axis] = 0
			}
		}
		pos[axis] += d
	}
	return pos, hitY
}

// clip returns how far of d the item's box can move along axis before it
// hits a block. A block not loaded stops it.
func (i *ItemActor) clip(r *world.Region, pos mgl32.Vec3, axis int, d float32) float32 {
	if d == 0 {
		return 0
	}
	lo := mgl32.Vec3{pos[0] - itemHalfSize, pos[1] - itemHalfSize, pos[2] - itemHalfSize}
	hi := mgl32.Vec3{pos[0] + itemHalfSize, pos[1] + itemHalfSize, pos[2] + itemHalfSize}
	sweepLo, sweepHi := lo, hi
	if d < 0 {
		sweepLo[axis] += d
	} else {
		sweepHi[axis] += d
	}
	blocks := i.s.reg.Blocks
	for x := itemFloor(sweepLo[0]); x <= itemFloor(sweepHi[0]); x++ {
		for y := itemFloor(sweepLo[1]); y <= itemFloor(sweepHi[1]); y++ {
			for z := itemFloor(sweepLo[2]); z <= itemFloor(sweepHi[2]); z++ {
				b, ok := r.Block(cube.Pos{x, y, z}, 0)
				if !ok {
					return 0
				}
				origin := mgl32.Vec3{float32(x), float32(y), float32(z)}
				for _, box := range blocks.Properties(b).Collision {
					d = clipBox(lo, hi, origin, box, axis, d)
				}
			}
		}
	}
	return d
}

// clipBox shortens d if the box lo..hi moving along axis hits box, a block's
// collision box at origin. A box it is already inside does not stop it.
func clipBox(lo, hi, origin mgl32.Vec3, box registry.Box, axis int, d float32) float32 {
	for n := range 3 {
		if n != axis && (hi[n] <= origin[n]+box[n] || lo[n] >= origin[n]+box[n+3]) {
			return d
		}
	}
	if d > 0 {
		if gap := origin[axis] + box[axis] - hi[axis]; gap >= 0 && gap < d {
			return gap
		}
		return d
	}
	if gap := origin[axis] + box[axis+3] - lo[axis]; gap <= 0 && gap > d {
		return gap
	}
	return d
}

func itemFloor(f float32) int { return int(math.Floor(float64(f))) }

func floorPos(p mgl32.Vec3) cube.Pos {
	return cube.Pos{itemFloor(p[0]), itemFloor(p[1]), itemFloor(p[2])}
}
