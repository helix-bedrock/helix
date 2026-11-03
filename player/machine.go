package player

import (
	"maps"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Hopper, dispenser and dropper rules from BDS 1.26.60.
const (
	// hopperDepth is how far a hopper first ticks the hoppers it feeds.
	hopperDepth = 50
	// hopperCooldown is how long a hopper waits after moving something, and a
	// hopper that was just given its first item waits hopperFirstItem, one tick more
	// if it has not ticked yet this tick.
	hopperCooldown  = 8
	hopperFirstItem = 4
	// hopperReachLow is how far into the block above the hopper's item box starts,
	// from the top of the hopper's own block; hopperReachHigh where it ends, from
	// the bottom.
	hopperReachLow  = 0.625
	hopperReachHigh = 2
	// A dispensed item starts ejectReach from the block's centre towards its front,
	// ejectLow above the bottom of the block when it faces a side.
	ejectReach = 0.7
	ejectLow   = 0.3
	ejectUp    = 0.2
	// The item leaves at ejectSpeed plus up to ejectSpeedExtra, and a gaussian
	// ejectSpread on each axis.
	ejectSpeed      = 0.2
	ejectSpeedExtra = 0.1
	ejectSpread     = 0.0075 * 6
	// levelEventDispenseSmoke is BDS's smoke, with data 4 + x + 3z of the face.
	levelEventDispenseSmoke = 2000
	// transferCooldownTag is the saved hopper cooldown.
	transferCooldownTag = "TransferCooldown"
	// stateHopperOff turns a hopper off.
	stateHopperOff = "toggle_bit"
	// stateFacingDirection is the way these blocks face.
	stateFacingDirection = "facing_direction"
)

// machineSpec is what makes each of these block entities differ.
type machineSpec struct {
	id     string
	size   int
	window byte
	hopper bool
	// dropper pushes into a container in front of it.
	dropper bool
}

// machineSpecs are the blocks that have only slots.
var machineSpecs = map[string]*machineSpec{
	"minecraft:hopper":    {id: "Hopper", size: 5, window: byte(protocol.ContainerTypeHopper), hopper: true},
	"minecraft:dispenser": {id: "Dispenser", size: 9, window: byte(protocol.ContainerTypeDispenser)},
	"minecraft:dropper":   {id: "Dropper", size: 9, window: byte(protocol.ContainerTypeDropper), dropper: true},
}

// machineByID is the spec of a saved id, nil if it is no such block.
func machineByID(id string) *machineSpec {
	for _, spec := range machineSpecs {
		if spec.id == id {
			return spec
		}
	}
	return nil
}

// loadMachine brings back a saved hopper, dispenser or dropper.
func (s *Host) loadMachine(pos cube.Pos, saved map[string]any, spec *machineSpec) *Chest {
	c := &Chest{s: s, pos: pos, lead: true, machine: spec, saved: maps.Clone(saved)}
	for _, item := range compounds(saved["Items"]) {
		slot, _ := item["Slot"].(uint8)
		if int(slot) < spec.size {
			c.items[slot] = withID(itemFromNBT(item), s.nextStackID)
		}
	}
	c.cooldown, _ = saved[transferCooldownTag].(int32)
	delete(c.saved, "Items")
	delete(c.saved, transferCooldownTag)
	return c
}

// halfSize is how many slots this block entity itself has.
func (c *Chest) halfSize() int {
	if c.machine != nil {
		return c.machine.size
	}
	return chestSize
}

// windowType is the kind of window the client opens for it.
func (c *Chest) windowType() byte {
	if c.machine != nil {
		return c.machine.window
	}
	return byte(protocol.ContainerTypeContainer)
}

// frontOf is the block a hopper or dispenser faces. A hopper has no up.
func (s *Host) frontOf(block uint32, pos cube.Pos) (cube.Face, cube.Pos) {
	f := cube.FaceDown
	if v, ok := s.reg.Blocks.States(block)[stateFacingDirection].(int32); ok && v >= int32(cube.FaceDown) && v <= int32(cube.FaceEast) {
		f = cube.Face(v)
	}
	return f, pos.Side(f)
}

// tickHopper is BDS's hopper tick: once per world tick; the hoppers this one feeds
// first, so an item goes all the way down a chain the same way each tick; then the
// move, unless the hopper is turned off.
func (c *Chest) tickHopper(r *world.Region, pos cube.Pos, depth int) {
	now := r.CurrentTick()
	if c.lastTick >= now {
		return
	}
	c.lastTick = now
	waited := c.cooldown <= 1
	c.cooldown--
	if !waited {
		return
	}
	c.cooldown = 0
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	_, front := c.s.frontOf(block, pos)
	if depth > 0 {
		c.tickFed(r, pos, front, depth-1)
	}
	if v, _ := c.s.reg.Blocks.States(block)[stateHopperOff].(uint8); v != 0 {
		return
	}
	if c.moveItems(r, pos, front) {
		c.cooldown = hopperCooldown
	}
}

// tickFed ticks the hopper under this one, the one under its target and the one
// under the other half of its target, a random one of them last.
func (c *Chest) tickFed(r *world.Region, pos, front cube.Pos, depth int) {
	fed := [3]*Chest{c.hopperAt(r, pos.Side(cube.FaceDown)), c.hopperAt(r, front.Side(cube.FaceDown))}
	if t := c.s.chestAt(r, front); t != nil {
		if mate := t.mate(r); mate != nil {
			fed[2] = c.hopperAt(r, mate.pos.Side(cube.FaceDown))
		}
	}
	if rand.IntN(2) == 0 {
		fed[0], fed[1] = fed[1], fed[0]
	}
	if i := rand.IntN(3); i != 2 {
		fed[i], fed[2] = fed[2], fed[i]
	}
	for _, h := range fed {
		if h != nil && h != c {
			h.tickHopper(r, h.pos, depth)
		}
	}
}

// hopperAt is the hopper at pos, or nil.
func (c *Chest) hopperAt(r *world.Region, pos cube.Pos) *Chest {
	if h := c.s.chestAt(r, pos); h != nil && h.machine != nil && h.machine.hopper {
		return h
	}
	return nil
}

// moveItems is BDS's hopper move: push one item on, then if there is room pull one
// from the container above, or items from the block above. True if something moved.
func (c *Chest) moveItems(r *world.Region, pos, front cube.Pos) bool {
	size := c.halfSize()
	pushed := false
	if !c.emptySlots(size) {
		pushed = c.pushTo(r, front)
	}
	if c.fullSlots(size) {
		return pushed
	}
	above := pos.Side(cube.FaceUp)
	if c.pullFrom(r, above) {
		return true
	}
	if b, ok := r.Block(above, 0); ok && c.s.attach.Solid(b) {
		return pushed
	}
	return c.collectItems(r, pos) || pushed
}

// emptySlots reports whether it holds nothing.
func (c *Chest) emptySlots(size int) bool {
	for i := range size {
		if !c.items[i].Empty() {
			return false
		}
	}
	return true
}

// fullSlots reports whether every slot holds a full stack.
func (c *Chest) fullSlots(size int) bool {
	for i := range size {
		s := c.items[i]
		if s.Empty() || s.Count < s.maxStack(c.s.reg) {
			return false
		}
	}
	return true
}

// pushTo moves one item into the container at at. A hopper given its first item
// waits, so a chain does not outrun itself.
func (c *Chest) pushTo(r *world.Region, at cube.Pos) bool {
	to := c.s.chestAt(r, at)
	if to == nil || to == c || to.ender {
		return false
	}
	wasEmpty := to.machine != nil && to.machine.hopper && to.emptySlots(to.machine.size)
	for i := range c.halfSize() {
		s := c.items[i]
		if s.Empty() {
			continue
		}
		if !to.takeOne(r, s) {
			continue
		}
		s.Count--
		c.SetItem(r, i, s)
		if wasEmpty {
			to.cooldown = hopperFirstItem
			if to.lastTick < r.CurrentTick() {
				to.cooldown++
			}
		}
		return true
	}
	return false
}

// takeOne puts one of s in the first slot that has room for it.
func (c *Chest) takeOne(r *world.Region, s ItemStack) bool {
	limit := s.maxStack(c.s.reg)
	for i, size := 0, c.Size(r); i < size; i++ {
		cur := c.Item(r, i)
		switch {
		case cur.Empty():
			one := s
			one.Count = 1
			c.SetItem(r, i, one)
			return true
		case cur.stacksWith(s) && cur.Count < limit:
			cur.Count++
			c.SetItem(r, i, cur)
			return true
		}
	}
	return false
}

// pullFrom takes one item from the container at at, from its first slot that this
// one has room for.
func (c *Chest) pullFrom(r *world.Region, at cube.Pos) bool {
	from := c.s.chestAt(r, at)
	if from == nil || from == c || from.ender {
		return false
	}
	for i, size := 0, from.Size(r); i < size; i++ {
		s := from.Item(r, i)
		if s.Empty() || !c.takeOne(r, s) {
			continue
		}
		s.Count--
		from.SetItem(r, i, s)
		return true
	}
	return false
}

// CircuitHooks are the blocks redstone answers for. Any may be nil.
type CircuitHooks struct {
	// Use is a player using a block; false if it does nothing.
	Use func(r *world.Region, pos cube.Pos, block uint32) bool
	// Attack is a player starting to break a block.
	Attack func(r *world.Region, pos cube.Pos, block uint32)
	// ChestViewers says how many have the trapped chest at pos open.
	ChestViewers func(r *world.Region, pos cube.Pos, n int32)
}

// SetCircuitHooks sets what redstone answers for. Call it before any player joins.
func (s *Host) SetCircuitHooks(h CircuitHooks) { s.circuits = h }

// viewersChanged tells a trapped chest, both halves of a double one, who has it open.
func (c *Chest) viewersChanged(r *world.Region) {
	report := c.s.circuits.ChestViewers
	if report == nil {
		return
	}
	b, ok := r.Block(c.pos, 0)
	if !ok || c.s.chests.kind[b] != chestTrapped {
		return
	}
	lead, follower := c.halves(r)
	n := int32(len(lead.viewers)) //nolint:gosec // few players
	report(r, lead.pos, n)
	if follower != nil {
		report(r, follower.pos, n)
	}
}

// EntityBox is what pressure plates see of an entity: players and dropped items, not
// experience orbs. Runs: owner of the entity's region.
func (s *Host) EntityBox(e *world.Entity) (lo, hi mgl32.Vec3, mob, ok bool) {
	at := e.Position()
	switch e.Owner().(type) {
	case *Player:
		lo = mgl32.Vec3{at[0] - playerHalfWidth, at[1] - eyeHeight, at[2] - playerHalfWidth}
		return lo, mgl32.Vec3{at[0] + playerHalfWidth, lo[1] + playerHeight, at[2] + playerHalfWidth}, true, true
	case *ItemActor:
		lo = mgl32.Vec3{at[0] - itemHalfSize, at[1], at[2] - itemHalfSize}
		return lo, mgl32.Vec3{at[0] + itemHalfSize, at[1] + 2*itemHalfSize, at[2] + itemHalfSize}, false, true
	}
	return lo, hi, false, false
}

// collectItems takes in whatever items lie in the box above the hopper. A stack that
// fits goes in whole.
func (c *Chest) collectItems(r *world.Region, pos cube.Pos) bool {
	x, y, z := float32(pos[0]), float32(pos[1]), float32(pos[2])
	var buf [16]*world.Entity
	for _, o := range r.AppendEntitiesIn(buf[:0], x, z, x+1, z+1) {
		i, ok := o.Owner().(*ItemActor)
		if !ok || i.taken || i.custom() || i.pickupDelay == itemNeverPickedUp {
			continue
		}
		at := o.Position()
		if at[0] < x || at[0] > x+1 || at[2] < z || at[2] > z+1 || at[1] < y+hopperReachLow || at[1] > y+hopperReachHigh {
			continue
		}
		left := c.AddItem(r, i.stack)
		if left.Count == i.stack.Count {
			continue
		}
		if left.Empty() {
			i.taken = true
			i.entity.Remove()
		} else {
			i.stack.Count = left.Count
			i.sendStackSize()
		}
		return true
	}
	return false
}

// Dispense fires the dispenser or dropper at pos, as BDS does: a random slot holding
// something; a dropper's goes into a container in front of it if there is one, and
// otherwise the item flies out, with smoke and a click. Nothing to give clicks and
// fails. Items with a behaviour of their own in a dispenser (arrows, buckets and so
// on) are not built, so those fly out too. Runs: owner of the chunk.
func (s *Host) Dispense(r *world.Region, pos cube.Pos, block uint32) {
	c := s.chestAt(r, pos)
	if c == nil || c.machine == nil {
		return
	}
	slot := -1
	seen := 0
	for i := range c.machine.size {
		if c.items[i].Empty() {
			continue
		}
		seen++
		if rand.IntN(seen) == 0 {
			slot = i
		}
	}
	center := blockVec(pos).Add(mgl32.Vec3{0.5, 0.5, 0.5})
	if slot < 0 {
		r.Broadcast(pos, &packet.LevelSoundEvent{SoundType: packet.SoundEventBlockClickFail, Position: center, ExtraData: -1}, nil)
		return
	}
	face, front := s.frontOf(block, pos)
	it := c.items[slot]
	if c.machine.dropper && c.pushOne(r, front, slot) {
		return
	}
	one := it
	one.Count = 1
	it.Count--
	c.SetItem(r, slot, it)
	dir := faceVec(face)
	spawn := center.Add(dir.Mul(ejectReach))
	if dir[1] == 0 {
		spawn[1] = float32(pos[1]) + ejectLow
	}
	speed := ejectSpeed + rand.Float32()*ejectSpeedExtra
	vel := mgl32.Vec3{dir[0] * speed, ejectUp, dir[2] * speed}
	if dir[1] != 0 {
		vel[1] = dir[1] * speed
	}
	for n := range 3 {
		vel[n] += float32(rand.NormFloat64()) * ejectSpread
	}
	s.spawnItem(r.Dimension(), one, spawn, vel, world.Rotation{}, chestPickupDelay, 0)
	levelEvent(r, pos, spawn, levelEventDispenseSmoke, int32(dir[0])+3*int32(dir[2])+4)
	r.Broadcast(pos, &packet.LevelSoundEvent{SoundType: packet.SoundEventBlockClick, Position: center, ExtraData: -1}, nil)
}

// pushOne moves one item of a slot into the container at at, false if there is none
// or it has no room.
func (c *Chest) pushOne(r *world.Region, at cube.Pos, slot int) bool {
	to := c.s.chestAt(r, at)
	if to == nil || to.ender {
		return false
	}
	s := c.items[slot]
	if !to.takeOne(r, s) {
		return false
	}
	s.Count--
	c.SetItem(r, slot, s)
	return true
}

// faceVec is a face as a direction.
func faceVec(f cube.Face) mgl32.Vec3 {
	switch f {
	case cube.FaceDown:
		return mgl32.Vec3{0, -1, 0}
	case cube.FaceUp:
		return mgl32.Vec3{0, 1, 0}
	case cube.FaceNorth:
		return mgl32.Vec3{0, 0, -1}
	case cube.FaceSouth:
		return mgl32.Vec3{0, 0, 1}
	case cube.FaceWest:
		return mgl32.Vec3{-1, 0, 0}
	}
	return mgl32.Vec3{1, 0, 0}
}
