package player

import (
	"maps"
	"math/rand/v2"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Hopper, dispenser and dropper rules from BDS 1.26.60.
const (
	// A dispensed item starts ejectReach from the block's centre towards its front,
	// ejectLow above the bottom of the block when it faces a side.
	ejectReach = 0.7
	// machineBucketPour is the click's data when a dispenser pours a bucket.
	machineBucketPour = 1000
	ejectLow          = 0.3
	ejectUp           = 0.2
	// The item appears this far below where it is thrown from.
	ejectDropSide     = -0.15625
	ejectDropVertical = -0.125
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
	// crafter makes recipes instead of giving out what it holds.
	crafter bool
	// furnace smelts: a furnace, blast furnace or smoker, lit or not.
	furnace *furnaceKind
	// brewing brews potions: a brewing stand.
	brewing bool
	// barrel shows open_bit while anyone has it open.
	barrel bool
}

// machineSpecs are the blocks that have only slots.
var machineSpecs = map[string]*machineSpec{
	"minecraft:hopper":            {id: "Hopper", size: 5, window: byte(protocol.ContainerTypeHopper), hopper: true},
	"minecraft:dispenser":         {id: "Dispenser", size: 9, window: byte(protocol.ContainerTypeDispenser)},
	"minecraft:dropper":           {id: "Dropper", size: 9, window: byte(protocol.ContainerTypeDropper), dropper: true},
	crafterBlock:                  {id: crafterEntityID, size: crafterSize, window: byte(protocol.ContainerTypeCrafter), crafter: true},
	"minecraft:furnace":           furnaceSpec,
	"minecraft:lit_furnace":       furnaceSpec,
	"minecraft:blast_furnace":     blastFurnaceSpec,
	"minecraft:lit_blast_furnace": blastFurnaceSpec,
	"minecraft:smoker":            smokerSpec,
	"minecraft:lit_smoker":        smokerSpec,
	brewingStandName:              brewingStandSpec,
	barrelBlock:                   {id: barrelEntityID, size: barrelSize, window: byte(protocol.ContainerTypeContainer), barrel: true},
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
	c := s.newMachine(pos, spec)
	c.saved = maps.Clone(saved)
	if c.furnace != nil {
		c.furnace.load(saved)
	}
	for _, item := range compounds(saved["Items"]) {
		slot, _ := item["Slot"].(uint8)
		if int(slot) < spec.size {
			c.items[slot] = withID(itemFromNBT(item), s.nextStackID)
		}
	}
	c.cooldown, _ = saved[transferCooldownTag].(int32)
	if v, ok := saved[disabledSlotsTag].(int16); ok {
		c.disabled = uint16(v) & allCrafterSlots //nolint:gosec // masked
	}
	if c.brewing != nil {
		c.brewing.load(saved)
	}
	delete(c.saved, disabledSlotsTag)
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
	conf := &c.s.redstone.Get().Hopper
	switch c.moveItems(r, pos, front, conf) {
	case hopperMoved:
		c.cooldown = conf.MoveCooldown
	case hopperPickedUp:
		c.cooldown = conf.PickupCooldown
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
// from the container above, or take items in the box above unless a container is there.
// Any other block only matters by where items can lie: on a slab or stair they are in
// the box, on a full block just out of it. It says what the move did. BDS has one
// cooldown for all of it, and both of Config's are that number by default.
func (c *Chest) moveItems(r *world.Region, pos, front cube.Pos, conf *circuit.HopperConfig) hopperResult {
	size := c.halfSize()
	result := hopperIdle
	for range conf.ItemsPerMove {
		if c.emptySlots(size) || !c.pushTo(r, front) && !(front == pos.Side(cube.FaceDown) && c.compostOne(r, front)) {
			break
		}
		result = hopperMoved
	}
	for range conf.ItemsPerMove {
		if c.fullSlots(size) {
			break
		}
		switch c.pullOrCollect(r, pos, conf) {
		case hopperIdle:
			return result
		case hopperPickedUp:
			return hopperPickedUp
		}
		result = hopperMoved
	}
	return result
}

// hopperResult is what one hopper move did.
type hopperResult uint8

const (
	hopperIdle hopperResult = iota
	hopperMoved
	hopperPickedUp
)

// pullOrCollect is the second half of a hopper's move: one item from the container or
// cart above, or items in the box above unless the block above is a container.
func (c *Chest) pullOrCollect(r *world.Region, pos cube.Pos, conf *circuit.HopperConfig) hopperResult {
	above := pos.Side(cube.FaceUp)
	if c.pullBoneMeal(r, above) {
		return hopperMoved
	}
	if c.pullFrom(r, above) {
		return hopperMoved
	}
	if b, ok := r.Block(above, 0); ok && containerBlock(c.s.reg.Blocks.Name(b)) {
		return hopperIdle
	}
	if c.collectItems(r, pos, conf) {
		return hopperPickedUp
	}
	return hopperIdle
}

// compostOne is a hopper facing down onto a composter with no container there (BDS's
// hopper, 0xBFC6FA0): one item of its first filled slot goes into the composter, used up
// whether or not it raised the level. False if the composter took none.
func (c *Chest) compostOne(r *world.Region, at cube.Pos) bool {
	f := c.s.farm
	if f == nil {
		return false
	}
	for i := range c.halfSize() {
		s := c.items[i]
		if s.Empty() {
			continue
		}
		if f.Compost(r, at, s.Name, 1) == 0 {
			return false
		}
		s.Count--
		c.Inventory(r).SetItem(i, s)
		return true
	}
	return false
}

// pullBoneMeal takes the bone meal of a full composter above (BDS's hopper, 0xBFC7AB0): the
// composter empties first, so with no room for it the bone meal is lost, as in BDS.
func (c *Chest) pullBoneMeal(r *world.Region, above cube.Pos) bool {
	f := c.s.farm
	if f == nil || !f.EmptyComposter(r, above) {
		return false
	}
	return c.takeOne(r, ItemStack{Name: nameBoneMeal, Count: 1})
}

// containerBlock reports whether the block is one BDS's block class calls a container:
// the classes that override that check, by the names of their blocks.
func containerBlock(name string) bool {
	switch name {
	case "minecraft:chest", "minecraft:hopper", "minecraft:dispenser", "minecraft:dropper", "minecraft:barrel",
		"minecraft:brewing_stand", "minecraft:crafter", "minecraft:furnace", "minecraft:lit_furnace",
		"minecraft:blast_furnace", "minecraft:lit_blast_furnace", "minecraft:smoker", "minecraft:lit_smoker":
		return true
	}
	return strings.HasSuffix(name, "_chest") || strings.HasSuffix(name, "_shulker_box")
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
	if to := c.s.chestAt(r, at); to != nil && to != c && !to.ender && c.pushInto(r, to) {
		return true
	}
	return c.s.cartsAt(r, at, c, func(to *Chest) bool { return c.pushInto(r, to) })
}

// pushInto moves one item into a container.
func (c *Chest) pushInto(r *world.Region, to *Chest) bool {
	wasEmpty := to.machine != nil && to.machine.hopper && to.emptySlots(to.machine.size)
	for i := range c.halfSize() {
		s := c.items[i]
		if s.Empty() {
			continue
		}
		if !c.putOne(r, to, s) {
			continue
		}
		s.Count--
		c.Inventory(r).SetItem(i, s)
		if wasEmpty {
			to.cooldown = c.s.redstone.Get().Hopper.FirstItemCooldown
			if to.lastTick < r.CurrentTick() {
				to.cooldown++
			}
		}
		return true
	}
	return false
}

// putOne puts one of s into to: a furnace takes it by the side it comes from, any other
// container in its first slot with room.
func (c *Chest) putOne(r *world.Region, to *Chest, s ItemStack) bool {
	if to.furnace != nil {
		return to.furnaceTakeOne(r, s, c.cart == nil && c.pos[1] > to.pos[1])
	}
	if to.brewing != nil {
		return to.brewTakeOne(r, s, c.cart == nil && c.pos[1] > to.pos[1])
	}
	return to.takeOne(r, s)
}

// takeOne puts one of s in the first slot that has room for it.
func (c *Chest) takeOne(r *world.Region, s ItemStack) bool {
	if c.shulker && isShulkerBox(s.Name) {
		return false
	}
	limit := s.maxStack(c.s.reg)
	for i, size := 0, c.Inventory(r).Size(); i < size; i++ {
		if c.machine != nil && c.machine.crafter && !c.crafterTakes(i, s) {
			continue
		}
		cur := c.Inventory(r).Item(i)
		switch {
		case cur.Empty():
			one := s
			one.Count = 1
			c.Inventory(r).SetItem(i, one)
			return true
		case cur.stacksWith(s) && cur.Count < limit:
			cur.Count++
			c.Inventory(r).SetItem(i, cur)
			return true
		}
	}
	return false
}

// pullFrom takes one item from the container at at, from its first slot that this
// one has room for.
func (c *Chest) pullFrom(r *world.Region, at cube.Pos) bool {
	if from := c.s.chestAt(r, at); from != nil && from != c && !from.ender && c.pullOne(r, from) {
		return true
	}
	return c.s.cartsAt(r, at, c, func(from *Chest) bool { return c.pullOne(r, from) })
}

// pullOne takes one item from a container, from its first slot this one has room for.
func (c *Chest) pullOne(r *world.Region, from *Chest) bool {
	if from.furnace != nil {
		return from.furnaceGiveOne(r, c)
	}
	if from.brewing != nil {
		return from.brewGiveOne(r, c)
	}
	for i, size := 0, from.Inventory(r).Size(); i < size; i++ {
		s := from.Inventory(r).Item(i)
		if s.Empty() || !c.takeOne(r, s) {
			continue
		}
		s.Count--
		from.Inventory(r).SetItem(i, s)
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
	// Destroy is a player about to break a block with the item called item in hand.
	Destroy func(r *world.Region, pos cube.Pos, block uint32, item string)
	// ChestViewers says how many have the trapped chest at pos open.
	ChestViewers func(r *world.Region, pos cube.Pos, n int32)
	// RingBell is a player with this yaw clicking the bell at pos, false if that does not
	// ring it.
	RingBell func(r *world.Region, pos cube.Pos, block uint32, yaw float32) bool
	// WindBell is a wind burst reaching the bell at pos, false if that does not ring it.
	WindBell func(r *world.Region, pos cube.Pos, block uint32) bool
	// JukeboxPower gives the jukebox at pos this power.
	JukeboxPower func(r *world.Region, pos cube.Pos, power int32)
	// HitTarget is a projectile landing at hit on the target block at pos, false if the
	// block is not a target. arrow says it was an arrow or trident.
	HitTarget func(r *world.Region, pos cube.Pos, hit mgl32.Vec3, arrow bool) bool
	// CartOnRail is a minecart in the block at pos.
	CartOnRail func(r *world.Region, pos cube.Pos)
	// Signal is the strength of the circuit part at pos, -1 for none: what an armour
	// stand takes its pose from.
	Signal func(r *world.Region, pos cube.Pos) int32
}

// SetCircuitHooks sets what redstone answers for. Call it before any player joins.
func (s *Host) SetCircuitHooks(h CircuitHooks) { s.circuits = h }

// viewersChanged tells a trapped chest, both halves of a double one, who has it open.
func (c *Chest) viewersChanged(r *world.Region) {
	report := c.s.circuits.ChestViewers
	if report == nil || c.cart != nil {
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

// EntityBox is what pressure plates see of an entity: players, dropped items and lit TNT, not
// experience orbs. Runs: owner of the entity's region.
func (s *Host) EntityBox(e *world.Entity) (lo, hi mgl32.Vec3, mob, ok bool) {
	at := e.Position()
	switch o := e.Owner().(type) {
	case *Player:
		lo = mgl32.Vec3{at[0] - playerHalfWidth, at[1] - eyeHeight, at[2] - playerHalfWidth}
		return lo, mgl32.Vec3{at[0] + playerHalfWidth, lo[1] + playerHeight, at[2] + playerHalfWidth}, true, true
	case *Mob:
		if o.gone {
			return lo, hi, false, false
		}
		bx := o.m.Box()
		return mglVec(bx.Min), mglVec(bx.Max), true, true
	case *ItemActor:
		lo = mgl32.Vec3{at[0] - itemHalfSize, at[1], at[2] - itemHalfSize}
		return lo, mgl32.Vec3{at[0] + itemHalfSize, at[1] + 2*itemHalfSize, at[2] + itemHalfSize}, false, true
	case *PrimedTNT:
		half := mgl32.Vec3{tntHalfSize, tntHalfSize, tntHalfSize}
		return at.Sub(half), at.Add(half), false, true
	case *Minecart:
		lo, hi = cartBox(at)
		return lo, hi, false, true
	case *Boat:
		lo, hi = boatBox(at)
		return lo, hi, false, true
	}
	return lo, hi, false, false
}

// PlateBox is what plates and buttons see of an entity: EntityBox, and projectiles, which pistons
// and carts do not take. Their box is taken as centred on their position. Runs: owner of the
// entity's region.
func (s *Host) PlateBox(e *world.Entity) (lo, hi mgl32.Vec3, mob, ok bool) {
	if t, isProjectile := e.Owner().(*Projectile); isProjectile {
		at, half := e.Position(), mgl32.Vec3{t.spec.Width / 2, t.spec.Height / 2, t.spec.Width / 2}
		return at.Sub(half), at.Add(half), false, true
	}
	return s.EntityBox(e)
}

// IsArrow says e is an arrow, what presses wood buttons.
func (s *Host) IsArrow(e *world.Entity) bool {
	t, isProjectile := e.Owner().(*Projectile)
	return isProjectile && t.spec.ID == nameArrow
}

// PistonPush moves an entity a piston runs into. A player's position is the client's, so
// the server moves it, stopped by blocks as BDS's actor move is, and corrects the client.
// Runs: owner of the entity's region.
func (s *Host) PistonPush(r *world.Region, e *world.Entity, by mgl32.Vec3) {
	if m, ok := e.Owner().(*Mob); ok {
		// The mob's body is where it is; the entity follows it at the mob's next Apply.
		if !m.gone && e.Apply(world.Movement{Pos: e.Position().Add(by), Rot: e.Rotation(), OnGround: e.OnGround(), Tick: e.Tick()}) {
			m.m.SetPos(vec(e.Position()))
		}
		return
	}
	p, isPlayer := e.Owner().(*Player)
	if !isPlayer {
		e.Apply(world.Movement{Pos: e.Position().Add(by), Rot: e.Rotation(), OnGround: e.OnGround(), Tick: e.Tick()})
		return
	}
	p.pistonPush(r, by)
}

// pistonPush moves the player and tells its client where it now is.
func (p *Player) pistonPush(r *world.Region, by mgl32.Vec3) {
	if p.entity == nil || p.dead() || p.gameMode == GameModeSpectator {
		return
	}
	lo, hi, _, ok := p.s.EntityBox(p.entity)
	if !ok {
		return
	}
	var moved mgl32.Vec3
	for axis := range 3 {
		moved[axis] = p.s.clipSweep(r, lo, hi, axis, by[axis])
		lo[axis] += moved[axis]
		hi[axis] += moved[axis]
	}
	if moved == (mgl32.Vec3{}) {
		return
	}
	rot, onGround, tick := p.entity.Rotation(), p.entity.OnGround(), p.entity.Tick()
	pos := p.entity.Position().Add(moved)
	p.s.handlers.movement(p, world.Movement{Pos: pos, Rot: rot, OnGround: onGround, Tick: tick})
	p.send(&packet.CorrectPlayerMovePrediction{
		PredictionType: packet.PredictionTypePlayer, Position: pos, Delta: p.move.velocity,
		Rotation: mgl32.Vec2{rot.Pitch, rot.Yaw}, OnGround: onGround, Tick: tick,
	})
	p.flush()
}

// collectItems takes in whatever items overlap the box above the hopper. A stack that
// fits goes in whole. An item only some players may see stays out, as those players' own;
// a name tag or lifetime changes nothing about what the item is.
func (c *Chest) collectItems(r *world.Region, pos cube.Pos, conf *circuit.HopperConfig) bool {
	x, y, z := float32(pos[0]), float32(pos[1]), float32(pos[2])
	var buf [16]*world.Entity
	for _, o := range r.AppendEntitiesIn(buf[:0], x, z, x+1, z+1) {
		i, ok := o.Owner().(*ItemActor)
		if !ok || i.taken || i.rule != nil || i.pickupDelay == itemNeverPickedUp {
			continue
		}
		at := o.Position()
		inset := conf.PickupInset
		if at[0]+itemHalfSize < x+inset || at[0]-itemHalfSize > x+1-inset ||
			at[2]+itemHalfSize < z+inset || at[2]-itemHalfSize > z+1-inset ||
			at[1]+itemHalfSize < y+conf.PickupLow+inset || at[1]-itemHalfSize > y+conf.PickupHigh-inset {
			continue
		}
		left := c.Inventory(r).AddItem(i.stack)
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

// HitTarget is a projectile landing at hit on the target block at pos, for whatever makes
// projectiles. False if the block is not a target. Runs: owner of the region.
func (s *Host) HitTarget(r *world.Region, pos cube.Pos, hit mgl32.Vec3, arrow bool) bool {
	if fn := s.circuits.HitTarget; fn != nil {
		return fn(r, pos, hit, arrow)
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
	if c.machine.crafter {
		c.craft(r, pos, block)
		return
	}
	slot := c.randomFullSlot()
	center := blockCenter(pos)
	if slot < 0 {
		playSound(r, pos, soundEvent{name: packet.SoundEventBlockClickFail, pos: center, extra: -1}, nil)
		return
	}
	face, front := s.frontOf(block, pos)
	if c.machine.dropper && c.pushOne(r, front, slot) {
		return
	}
	if c.machine.dropper && face == cube.FaceDown && s.dropIntoComposter(r, c, slot, front) {
		return
	}
	dir := faceVec(face)
	d := dispense{r: r, c: c, slot: slot, pos: pos, front: front, face: face, dir: dir, at: center.Add(dir.Mul(ejectReach))}
	if !c.machine.dropper && s.dispenseSpecial(d) {
		return
	}
	it := c.items[slot]
	one := it
	one.Count = 1
	d.takeOne()
	if one.Name == nameTNT && !c.machine.dropper && s.level.TNTExplodes {
		s.dispenseTNT(r, front)
	} else {
		s.eject(r, one, pos, dir, d.at)
	}
	d.smoke()
	d.click()
}

// dispense is one firing of a dispenser or dropper c at pos: the slot it fires from,
// the block in front, which way it faces and where items come out.
type dispense struct {
	r     *world.Region
	c     *Chest
	slot  int
	pos   cube.Pos
	front cube.Pos
	face  cube.Face
	dir   mgl32.Vec3
	at    mgl32.Vec3
}

// takeOne takes one item from the slot fired.
func (d dispense) takeOne() {
	it := d.c.items[d.slot]
	it.Count--
	d.c.Inventory(d.r).SetItem(d.slot, it)
}

// smoke shows the dispense smoke on the side fired from.
func (d dispense) smoke() {
	levelEvent(d.r, d.pos, d.at, levelEventDispenseSmoke, int32(d.dir[0])+3*int32(d.dir[2])+4)
}

// click plays the dispense click.
func (d dispense) click() {
	playSound(d.r, d.pos, soundEvent{name: packet.SoundEventBlockClick, pos: blockCenter(d.pos), extra: -1}, nil)
}

// dispenseSpecial runs what a dispenser does with an item that has a behaviour of its
// own: buckets, carts, bone meal, fireworks and projectiles. False if the item has none
// here, so it is thrown out.
func (s *Host) dispenseSpecial(d dispense) bool {
	r, c, it := d.r, d.c, d.c.items[d.slot]
	click := false
	switch {
	case c.useBucket(r, d.front, d.slot, d.at, d.pos, d.dir):
	case IsCartItem(it.Name) && s.dispenseCart(r, d.front, d.dir, it.Name):
		d.takeOne()
		click = true
	case it.Name == nameBoneMeal:
		s.dispenseBoneMeal(r, c, d.slot, d.front, d.at)
	case s.dispenseFirework(r, d.pos, d.face, d.at, it), s.dispenseProjectile(r, d.pos, d.at, d.dir, it.Name):
		d.takeOne()
	default:
		return false
	}
	d.smoke()
	if click {
		d.click()
	}
	return true
}

// randomFullSlot is a random slot of the machine holding something, each as likely, or
// -1 if all are empty.
func (c *Chest) randomFullSlot() int {
	slot, seen := -1, 0
	for i := range c.machine.size {
		if c.items[i].Empty() {
			continue
		}
		seen++
		if rand.IntN(seen) == 0 {
			slot = i
		}
	}
	return slot
}

// dropIntoComposter is BDS's dropper (0xD40C260) facing down onto a composter: it puts
// one item in and drops nothing, whether or not the composter took it. False if the
// block below is no composter, so the item is thrown out as usual.
func (s *Host) dropIntoComposter(r *world.Region, c *Chest, slot int, below cube.Pos) bool {
	if s.farm == nil {
		return false
	}
	b, ok := r.Block(below, 0)
	if !ok || !s.farm.IsComposter(b) {
		return false
	}
	it := c.items[slot]
	if s.farm.Compost(r, below, it.Name, 1) == 1 {
		it.Count--
		c.Inventory(r).SetItem(slot, it)
	}
	return true
}

// eject throws stack out of the machine at pos, as BDS's dispenser throws an item.
func (s *Host) eject(r *world.Region, stack ItemStack, pos cube.Pos, dir, at mgl32.Vec3) {
	if dir[1] == 0 {
		at[1] = float32(pos[1]) + ejectLow
	}
	eject := &s.redstone.Get().Dispenser
	speed := eject.Speed + rand.Float32()*eject.SpeedExtra
	vel := mgl32.Vec3{dir[0] * speed, ejectUp, dir[2] * speed}
	if dir[1] != 0 {
		vel[1] = dir[1] * speed
	}
	for n := range 3 {
		vel[n] += float32(rand.NormFloat64()) * eject.Spread
	}
	if dir[1] == 0 {
		at[1] += ejectDropSide
	} else {
		at[1] += ejectDropVertical
	}
	s.spawnItem(r.Dimension(), stack, at, vel, world.Rotation{}, chestPickupDelay, 0)
}

// useBucket is BDS's bucket in a dispenser: a filled one pours at front and an empty one
// takes the source there. The other bucket then goes in the dispenser, or out as an item
// if it is full. False if it did neither, and the bucket is thrown out like any item.
func (c *Chest) useBucket(r *world.Region, front cube.Pos, slot int, at mgl32.Vec3, pos cube.Pos, dir mgl32.Vec3) bool {
	s := c.s
	it := c.items[slot]
	var next string
	extra := int32(machineBucketPour)
	switch {
	case it.Name == emptyBucket:
		fill, ok := s.takeLiquid(r, front)
		if !ok {
			return false
		}
		next, extra = fill.item, -1
	default:
		liq, ok := bucketLiquids[it.Name]
		if !ok || !s.machinePour(r, front, liq) {
			return false
		}
		next = emptyBucket
	}
	playSound(r, pos, soundEvent{name: packet.SoundEventBlockClick, pos: at, extra: extra}, nil)
	it.Count--
	c.Inventory(r).SetItem(slot, it)
	if rest := c.Inventory(r).AddItem(ItemStack{Name: next, Count: 1, netID: s.stackIDs.Add(1)}); !rest.Empty() {
		s.eject(r, rest, pos, dir, at)
	}
	return true
}

// machinePour is BDS's pouring with nobody holding the bucket: the liquid goes in at pos,
// which a solid block refuses unless it holds water and water is poured. Not read: the
// nether's rule for water, and items a replaced block drops.
func (s *Host) machinePour(r *world.Region, pos cube.Pos, liq bucketLiquid) bool {
	blocks := s.reg.Blocks
	block, ok := blocks.Block(liq.block)
	if !ok {
		return false
	}
	main, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	second, ok := r.Block(pos, 1)
	if !ok {
		return false
	}
	props := blocks.Properties(main)
	water := liq.kind == liquid.Water
	if second != blocks.Air() && blocks.Properties(second).Material != registry.MaterialWater {
		return false
	}
	if props.SolidMaterial && (!water || !props.CanContainLiquid) {
		return false
	}
	layer := uint8(0)
	if water && main != blocks.Air() && props.CanContainLiquid {
		layer = 1
	}
	if !r.SetBlock(pos, layer, block) {
		return false
	}
	r.UpdateNeighbours(pos)
	r.ScheduleTick(pos, block, liquid.TickDelay(liq.kind, false), 0)
	return true
}

// pushOne moves one item of a slot into the container at at, false if there is none
// or it has no room.
func (c *Chest) pushOne(r *world.Region, at cube.Pos, slot int) bool {
	to := c.s.chestAt(r, at)
	if to == nil || to.ender {
		return false
	}
	s := c.items[slot]
	if !c.putOne(r, to, s) {
		return false
	}
	s.Count--
	c.Inventory(r).SetItem(slot, s)
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
