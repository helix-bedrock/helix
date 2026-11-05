package player

import (
	"maps"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/ride"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Minecarts, from BDS 1.26.60's Minecart classes and movement systems, and their
// definitions in the behaviour pack.
const (
	nameMinecart       = "minecraft:minecart"
	nameChestMinecart  = "minecraft:chest_minecart"
	nameHopperMinecart = "minecraft:hopper_minecart"
	nameTNTMinecart    = "minecraft:tnt_minecart"
	nameRail           = "minecraft:rail"
	nameGoldenRail     = "minecraft:golden_rail"
	nameDetectorRail   = "minecraft:detector_rail"
	nameActivatorRail  = "minecraft:activator_rail"

	stateRailDirection = "rail_direction"
	stateRailData      = "rail_data_bit"

	// cartHalfWidth is half its 0.98 box, cartHeight its height, cartOffset how far above
	// the bottom of its box its position is.
	cartHalfWidth = 0.49
	cartHeight    = 0.7
	cartOffset    = 0.35
	// cartAboveRail lifts it clear of what the rail stands on.
	cartAboveRail = 0.00001

	cartGravity = 0.04
	// cartMaxSpeed is rail_movement's default, in blocks a tick along one axis.
	cartMaxSpeed = 0.4
	// cartRailSpeedCap stops it gaining speed for ever on the rail.
	cartRailSpeedCap = 2
	// cartRiddenScale slows the move of a cart with a rider, not its speed.
	cartRiddenScale = 0.75
	// cartSlowRidden and cartSlowEmpty are the slowdowns on rails; a cart with slots
	// slows by cartSlowFull plus cartSlowPerSignal for each signal below full.
	cartSlowRidden    = 0.997
	cartSlowEmpty     = 0.96
	cartSlowFull      = 0.98
	cartSlowPerSignal = 0.001
	// cartSlopePush is what a slope adds a tick.
	cartSlopePush = 0.0078125
	// cartBrake halves the speed on a powered rail with no power, down to cartBrakeMin,
	// below which it stops. Those are squared lengths.
	cartBrake    = 0.5
	cartBrakeMin = 0.0009
	// cartBoost is what a powered rail adds, cartBoostFrom the speed it needs to add it,
	// cartKick what it gives a cart at rest against a block.
	cartBoost     = 0.06
	cartBoostFrom = 0.01
	cartKick      = 0.02
	// cartHillGain is the speed gained for each block fallen along the rail.
	cartHillGain = 0.05
	// cartGroundDrag is off rails on the ground.
	cartGroundDrag = 0.5
	// cartPushInput is a rider's push when it walks forward, up to cartPushInputLimit
	// (a squared speed).
	cartPushInput      = 0.1
	cartPushInputLimit = 0.01
	// cartPush is pushable_by_entity's legacy_minecart strength times its push_scale_self;
	// cartPushMin a squared min_distance.
	cartPush    = 0.05
	cartPushMin = 0.0001
	// cartPushReach is how far the box grows for what pushes; cartPushAlong how far along
	// its rail a cart pushes others; cartPushAligned how straight two carts must meet to
	// push each other; cartPushKeep what is left of their speed when they do.
	cartPushReach   = 0.2
	cartPushAlong   = 0.2
	cartPushAligned = 0.8
	cartPushKeep    = 0.2
	// cartTurnFlip is how many degrees of turn in a tick means it now runs backwards.
	cartTurnFlip = 170
	// cartMoved is the squared distance it needs to move to turn.
	cartMoved = 0.001

	// cartIntegrity is how much a hit takes off at most; damage counts cartHitScale times.
	cartIntegrity = 20
	cartHitScale  = 8
	cartHurtTime  = 10

	// cartDispenseOut is how far from the middle of the block a dispenser puts a cart.
	cartDispenseOut = 0.125
	// cartDropDelay is the pick-up delay of what a broken cart leaves.
	cartDropDelay = 10

	// cartTNTPower is its explosion, cartTNTFuse its fuse in ticks. Fire or lava lights it
	// for cartTNTBurnMin plus up to cartTNTBurnExtra ticks; a fall over cartTNTFall blocks
	// or a blast sets it off at once.
	cartTNTPower     = 3
	cartTNTFuse      = 80
	cartTNTBurnMin   = 10
	cartTNTBurnExtra = 20
	cartTNTFall      = 3.5

	// cartSeatY is rideable's seat, from entities/minecart.json.
	cartSeatY = -0.2
	// cartReach is how far a player may be from a cart to use it.
	cartReach = 12
	// cartScan is how far to look for what touches a cart.
	cartScan = 2

	cartFlags = 1<<protocol.EntityDataFlagHasGravity | 1<<protocol.EntityDataFlagHasCollision | 1<<protocol.EntityDataFlagStackable

	cartKeyItems = "Items"
	cartKeyFuse  = "Fuse"
	cartKeyLit   = "IsFuseLit"
)

// railExits are BDS's rail table: for each direction, the two ends of a rail, as x, y, z.
var railExits = [10][6]int32{
	{0, 0, -1, 0, 0, 1},
	{-1, 0, 0, 1, 0, 0},
	{-1, -1, 0, 1, 0, 0},
	{-1, 0, 0, 1, -1, 0},
	{0, 0, -1, 0, -1, 1},
	{0, -1, -1, 0, 0, 1},
	{0, 0, 1, 1, 0, 0},
	{0, 0, 1, -1, 0, 0},
	{0, 0, -1, -1, 0, 0},
	{0, 0, -1, 1, 0, 0},
}

// cartSpec is one kind of minecart.
type cartSpec struct {
	id string
	// block is the item its block drops as, empty for a plain cart.
	block string
	// machine is its slots, if it has any. Its window is what the cart says it holds;
	// opens is the window the client is told to open, which BDS sets apart (0x737E270):
	// a chest cart opens a plain container and a hopper cart a hopper.
	machine *machineSpec
	opens   byte
	rides   bool
	hopper  bool
	tnt     bool
}

var cartSpecs = map[string]*cartSpec{
	nameMinecart:      {id: nameMinecart, rides: true},
	nameChestMinecart: {id: nameChestMinecart, block: "minecraft:chest", opens: byte(protocol.ContainerTypeContainer), machine: &machineSpec{id: "Chest", size: chestSize, window: byte(protocol.ContainerTypeCartChest)}},
	nameHopperMinecart: {id: nameHopperMinecart, block: "minecraft:hopper", hopper: true, opens: byte(protocol.ContainerTypeHopper),
		machine: &machineSpec{id: "Hopper", size: 5, window: byte(protocol.ContainerTypeCartHopper)}},
	nameTNTMinecart: {id: nameTNTMinecart, block: nameTNT, tnt: true},
}

// Minecart is any of the four minecarts. Its numbers are its own, so a plugin can change
// them: see [Minecart.SetMaxSpeed]. Runs: owner of its entity.
type Minecart struct {
	s      *Host
	spec   *cartSpec
	entity *world.Entity
	vel    mgl32.Vec3

	maxSpeed float32
	onGround bool
	gone     bool
	flipped  bool

	seats              *ride.Vehicle
	moved              mgl32.Vec3
	hurtTime, hurtSide int32
	integrity          int32

	// chest is its slots, a [Chest] with no block behind it.
	chest *Chest
	// hopperOff is an activator rail having stopped its hopper; hopperWait the ticks to
	// its next go.
	hopperOff  bool
	hopperWait int32
	// lit and fuse are a TNT cart's.
	lit  bool
	fuse int32

	saved map[string]any
	near  []*world.Entity
}

// Entity returns the cart's entity.
func (m *Minecart) Entity() *world.Entity { return m.entity }

// Velocity returns its speed in blocks a tick.
func (m *Minecart) Velocity() mgl32.Vec3 { return m.vel }

// SetVelocity sets its speed in blocks a tick.
func (m *Minecart) SetVelocity(v mgl32.Vec3) { m.vel = v }

// SetMaxSpeed sets how fast it may go along one axis.
func (m *Minecart) SetMaxSpeed(blocksPerTick float32) { m.maxSpeed = blocksPerTick }

// Rider returns who rides it, if anyone.
func (m *Minecart) Rider() *Player { return m.rider() }

// rider is who sits in it, if anyone.
func (m *Minecart) rider() *Player {
	if m.seats == nil {
		return nil
	}
	p, _ := m.seats.First().(*Player)
	return p
}

// Chest returns its slots, nil for a plain or TNT cart.
func (m *Minecart) Chest() *Chest { return m.chest }

// IsCartItem reports whether the item called name is a minecart.
func IsCartItem(name string) bool { _, ok := cartSpecs[name]; return ok }

// SpawnMinecart adds the cart whose entity and item is called id. Nil if there is none
// by that name. Runs: any goroutine.
func (s *Host) SpawnMinecart(d *world.Dimension, id string, pos mgl32.Vec3) *Minecart {
	m := s.newCart(id)
	if m == nil {
		return nil
	}
	m.entity = d.AddEntity(m.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), pos, world.Rotation{})) //nolint:gosec // an entity ID, not a secret
	return m
}

func (s *Host) newCart(id string) *Minecart {
	spec, ok := cartSpecs[id]
	if !ok {
		return nil
	}
	m := &Minecart{s: s, spec: spec, maxSpeed: cartMaxSpeed, integrity: cartIntegrity}
	if spec.rides {
		m.seats = ride.New(cartRideRules())
	}
	if spec.machine != nil {
		m.chest = &Chest{s: s, lead: true, machine: spec.machine, cart: m}
	}
	return m
}

func (m *Minecart) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: m, Spawn: m.spawnPackets, Move: m.movePacket, Input: m.tick, Save: m.saveEntity,
	}
}

// spawnPackets show it to a player. Not yet compared with what BDS sends.
func (m *Minecart) spawnPackets(e *world.Entity) []packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags: int64(m.flags()), protocol.EntityDataKeyWidth: float32(2 * cartHalfWidth),
		protocol.EntityDataKeyHeight: float32(cartHeight), protocol.EntityDataKeyScale: float32(1),
		protocol.EntityDataKeyStructuralIntegrity: m.integrity,
	}
	if spec := m.spec.machine; spec != nil {
		meta[protocol.EntityDataKeyContainerType] = spec.window
		meta[protocol.EntityDataKeyContainerSize] = int32(spec.size) //nolint:gosec // small
	}
	if m.lit {
		meta[protocol.EntityDataKeyFuseTime] = m.fuse
	}
	add := &packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: m.spec.id,
		Position: e.Position(), Velocity: m.vel, EntityMetadata: meta,
	}
	if m.seats != nil {
		add.EntityLinks = m.seats.Links(e.UniqueID())
	}
	return []packet.Packet{add}
}

func (m *Minecart) flags() uint64 {
	f := uint64(cartFlags)
	if m.lit {
		f |= 1 << protocol.EntityDataFlagIgnited
	}
	return f
}

func (m *Minecart) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	var flags byte
	if e.OnGround() {
		flags = packet.MoveFlagOnGround
	}
	return &packet.MoveActorAbsolute{
		EntityRuntimeID: e.RuntimeID(), Flags: flags, Position: e.Position(), Rotation: mgl32.Vec3{rot.Pitch, rot.Yaw, rot.HeadYaw},
	}
}

// tick is one tick of the cart in BDS's order: hurt and gravity, the rail systems, what
// it touches, then what kind of cart it is.
func (m *Minecart) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || m.gone {
		return
	}
	if m.s.checkVoid(e); m.gone {
		return
	}
	m.settleRiders()
	m.dropGoneViewers(r)
	start := e.Position()
	m.preTick(r, e, start)
	pos := m.move(r, start)
	m.moved = pos.Sub(start)
	rot := m.turn(start, pos, e.Rotation())
	if !e.Apply(world.Movement{Pos: pos, Rot: rot, OnGround: m.onGround, Tick: e.Tick()}) {
		m.vel = mgl32.Vec3{}
		pos = start
	}
	m.pushes(r, e, pos)
	if h := m.s.circuits.CartOnRail; h != nil {
		h(r, floorPos(pos))
	}
	m.railSensor(r, pos)
	switch {
	case m.spec.hopper:
		m.tickHopper(r, pos)
	case m.spec.tnt:
		m.tickFuse(r, e)
	}
}

// preTick is BDS's preNormalTick: the hurt counters, gravity, and slowing down on a rail.
func (m *Minecart) preTick(r *world.Region, e *world.Entity, pos mgl32.Vec3) {
	changed := false
	if m.hurtTime > 0 {
		m.hurtTime--
		changed = true
	}
	if m.integrity < cartIntegrity {
		m.integrity++
		changed = true
	}
	if changed {
		m.sendHurt(e)
	}
	m.vel[1] -= cartGravity
	at := floorPos(pos)
	if !m.s.railBlock(r, at) && m.s.railBlock(r, at.Side(cube.FaceDown)) {
		at = at.Side(cube.FaceDown)
	}
	if m.s.railBlock(r, at) {
		m.slowDown(r)
	}
}

// slowDown is applyNaturalSlowdown: a rider barely slows it, slots slow it by how full
// they are. It also takes away what gravity gave it.
func (m *Minecart) slowDown(r *world.Region) {
	scale := float32(cartSlowEmpty)
	switch {
	case m.chest != nil:
		scale = cartSlowFull + float32(fullPower-m.chest.ComparatorSignal(r, cube.Pos{}))*cartSlowPerSignal
	case m.rider() != nil:
		scale = cartSlowRidden
	}
	m.vel = mgl32.Vec3{m.vel[0] * scale, 0, m.vel[2] * scale}
}

// fullPower is the strongest signal a comparator reads.
const fullPower = 15

// railBlock reports whether the block at pos is a rail.
func (s *Host) railBlock(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && isRailName(s.reg.Blocks.Name(b))
}

func isRailName(name string) bool {
	return name == nameRail || name == nameGoldenRail || name == nameDetectorRail || name == nameActivatorRail
}

// findRail is MinecartCanSnapOnRailSystem: the rail under the cart, looking as far down as
// it will fall this tick, stopped by anything solid.
func (m *Minecart) findRail(r *world.Region, pos mgl32.Vec3) (cube.Pos, uint32, bool) {
	at := floorPos(pos)
	n := int32(1)
	if m.vel[1] < 0 {
		n = int32(math.Ceil(float64(-m.vel[1])))
	}
	for range n + 1 {
		b, ok := r.Block(at, 0)
		if !ok {
			break
		}
		if isRailName(m.s.reg.Blocks.Name(b)) {
			return at, b, true
		}
		if m.s.solidBlock(b) {
			break
		}
		at = at.Side(cube.FaceDown)
	}
	return cube.Pos{}, 0, false
}

// solidBlock is Block::isSolid as far as Helix has it.
func (s *Host) solidBlock(b uint32) bool {
	return s.attach != nil && s.attach.Solid(b)
}

// move is the rail systems around one actor move, in BDS's order: on to the rail, the
// speed along it, the move itself, back on to the rail and what a powered rail does.
func (m *Minecart) move(r *world.Region, start mgl32.Vec3) mgl32.Vec3 {
	rail, block, snapped := m.findRail(r, start)
	if !snapped {
		m.vel[0] = clampAbs(m.vel[0], m.maxSpeed)
		m.vel[2] = clampAbs(m.vel[2], m.maxSpeed)
		if m.onGround {
			m.vel[0] *= cartGroundDrag
			m.vel[2] *= cartGroundDrag
		}
		return m.moveBy(r, start, m.vel)
	}
	blocks := m.s.reg.Blocks
	states := blocks.States(block)
	dir := railDir(states)
	pos := railLine(rail, dir, start)
	golden := blocks.Name(block) == nameGoldenRail
	powered := golden && railBit(states)
	brake := golden && !powered
	step := m.railSpeed(dir, &brake)
	pos = m.moveBy(r, pos, step)
	pos = m.afterRail(r, rail, start, dir, pos)
	if powered && !brake {
		m.boost(r, rail, dir)
	}
	return pos
}

// railDir is the direction of a rail state, held to the table.
func railDir(states map[string]any) int {
	v, _ := states[stateRailDirection].(int32)
	if v < 0 || int(v) >= len(railExits) {
		return 0
	}
	return int(v)
}

// railBit is whether a rail state is on.
func railBit(states map[string]any) bool {
	v, _ := states[stateRailData].(uint8)
	return v != 0
}

// railLine is BDS's position before a rail move: on the line of the rail, above it.
func railLine(rail cube.Pos, dir int, pos mgl32.Vec3) mgl32.Vec3 {
	ex := railExits[dir]
	y := float32(rail[1])
	if dir >= 2 && dir <= 5 {
		y++
	}
	cx, cz := float32(rail[0])+0.5, float32(rail[2])+0.5
	ax, az := float32(ex[0])*0.5+cx, float32(ex[2])*0.5+cz
	dx, dz := float32(ex[3])*0.5+cx-ax, float32(ex[5])*0.5+cz-az
	t := railAlong(dx, dz, pos, rail, ax, az)
	return mgl32.Vec3{dx*t + ax, y + cartOffset + cartAboveRail, dz*t + az}
}

// railAlong is how far along a rail's line a position is.
func railAlong(dx, dz float32, pos mgl32.Vec3, rail cube.Pos, ax, az float32) float32 {
	switch {
	case dx == 0:
		return pos[2] - float32(rail[2])
	case dz == 0:
		return pos[0] - float32(rail[0])
	}
	v := (pos[0]-ax)*dx + (pos[2]-az)*dz
	return v + v
}

// railSpeed is BDS's calculateMoveVelocity: slopes, the speed turned along the rail, a
// rider's push, the brake of a rail with no power. It returns the move, which may be
// slower than the cart's speed.
func (m *Minecart) railSpeed(dir int, brake *bool) mgl32.Vec3 {
	switch dir {
	case 2:
		m.vel[0] -= cartSlopePush
	case 3:
		m.vel[0] += cartSlopePush
	case 4:
		m.vel[2] += cartSlopePush
	case 5:
		m.vel[2] -= cartSlopePush
	}
	ex := railExits[dir]
	dx, dz := float64(ex[3]-ex[0]), float64(ex[5]-ex[2])
	vx, vz := float64(m.vel[0]), float64(m.vel[2])
	if vx*dx+vz*dz < 0 {
		dx, dz = -dx, -dz
	}
	speed := min(cartRailSpeedCap, math.Sqrt(vx*vx+vz*vz))
	length := math.Sqrt(dx*dx + dz*dz)
	m.vel[0], m.vel[2] = float32(dx*speed/length), float32(dz*speed/length)
	if m.riderPush() {
		*brake = false
	}
	if *brake {
		if m.vel[0]*m.vel[0]+m.vel[2]*m.vel[2] >= cartBrakeMin {
			m.vel = mgl32.Vec3{m.vel[0] * cartBrake, 0, m.vel[2] * cartBrake}
		} else {
			m.vel = mgl32.Vec3{}
		}
	}
	x, z := m.vel[0], m.vel[2]
	if m.rider() != nil {
		x *= cartRiddenScale
		z *= cartRiddenScale
	}
	return mgl32.Vec3{clampAbs(x, m.maxSpeed), 0, clampAbs(z, m.maxSpeed)}
}

// riderPush is a rider walking forward starting a cart that is almost still, the way it
// looks. True if it pushed.
func (m *Minecart) riderPush() bool {
	p := m.rider()
	if p == nil || p.entity == nil || p.move.forward <= 0 {
		return false
	}
	if m.vel[0]*m.vel[0]+m.vel[2]*m.vel[2] >= cartPushInputLimit {
		return false
	}
	yaw := float64(p.entity.Rotation().Yaw) * math.Pi / 180
	m.vel[0] -= float32(math.Sin(yaw)) * cartPushInput
	m.vel[2] += float32(math.Cos(yaw)) * cartPushInput
	return true
}

// moveBy is the actor move: stopped by the blocks' collision boxes, Y first.
func (m *Minecart) moveBy(r *world.Region, pos, step mgl32.Vec3) mgl32.Vec3 {
	lo := mgl32.Vec3{pos[0] - cartHalfWidth, pos[1] - cartOffset, pos[2] - cartHalfWidth}
	hi := mgl32.Vec3{pos[0] + cartHalfWidth, lo[1] + cartHeight, pos[2] + cartHalfWidth}
	hitY := false
	for _, axis := range [...]int{1, 0, 2} {
		d := m.s.clipSweep(r, lo, hi, axis, step[axis])
		if d != step[axis] {
			m.vel[axis] = 0
			if axis == 1 {
				hitY = true
			}
		}
		lo[axis] += d
		hi[axis] += d
		pos[axis] += d
	}
	m.onGround = hitY && step[1] < 0
	return pos
}

// clampAbs holds v to within limit of 0.
func clampAbs(v, limit float32) float32 { return max(-limit, min(limit, v)) }

// afterRail is BDS's position after a rail move: up or down a slope at its end, the speed
// the height changed gives, and the speed turned on to the next block's line.
func (m *Minecart) afterRail(r *world.Region, rail cube.Pos, before mgl32.Vec3, dir int, pos mgl32.Vec3) mgl32.Vec3 {
	ex := railExits[dir]
	fx, fz := cartFloor(pos[0]), cartFloor(pos[2])
	rx, rz := int32(rail[0]), int32(rail[2]) //nolint:gosec // in the world
	switch {
	case (dir == 5 || dir == 2) && fx-rx == ex[0] && fz-rz == ex[2]:
		pos[1] += float32(ex[1])
	case (dir == 3 || dir == 4) && fx-rx == ex[3] && fz-rz == ex[5]:
		pos[1] += float32(ex[4])
	}
	yBefore, okBefore := m.s.railHeight(r, before)
	yNow, okNow := m.s.railHeight(r, pos)
	if okBefore && okNow {
		if h := hypot(m.vel[0], m.vel[2]); h > 0 {
			scale := ((yBefore-yNow)*cartHillGain + h) / h
			m.vel[0] *= scale
			m.vel[2] *= scale
		}
		pos[1] = yNow
	}
	if nx := cartFloor(pos[0]); nx != rx || fz != rz {
		h := hypot(m.vel[0], m.vel[2])
		m.vel[0], m.vel[2] = float32(nx-rx)*h, float32(fz-rz)*h
	}
	return pos
}

func hypot(x, z float32) float32 { return float32(math.Sqrt(float64(x*x + z*z))) }

func cartFloor(v float32) int32 { return int32(math.Floor(float64(v))) }

// railHeight is BDS's getPos: the height of the rail at a position, if there is one there
// or one lower.
func (s *Host) railHeight(r *world.Region, pos mgl32.Vec3) (float32, bool) {
	at := floorPos(pos)
	if s.railBlock(r, at.Side(cube.FaceDown)) {
		at = at.Side(cube.FaceDown)
	}
	b, ok := r.Block(at, 0)
	if !ok || !isRailName(s.reg.Blocks.Name(b)) {
		return 0, false
	}
	ex := railExits[railDir(s.reg.Blocks.States(b))]
	cx, cy, cz := float32(at[0])+0.5, float32(at[1])+0.5, float32(at[2])+0.5
	ax, ay, az := float32(ex[0])*0.5+cx, float32(ex[1])*0.5+cy, float32(ex[2])*0.5+cz
	dx := float32(ex[3])*0.5 + cx - ax
	dy := float32(ex[4])*0.5 + cy - ay
	dz := float32(ex[5])*0.5 + cz - az
	rise := dy + dy
	t := railAlong(dx, dz, pos, at, ax, az)
	y := t*rise + ay
	if rise < 0 {
		y++
	} else if rise > 0 {
		y += 0.5
	}
	return y, true
}

// boost is BDS's calculateGoldenRailSpeedIncrease: more speed if moving, and if not, a
// push away from a solid block behind it.
func (m *Minecart) boost(r *world.Region, rail cube.Pos, dir int) {
	h := hypot(m.vel[0], m.vel[2])
	if h > cartBoostFrom {
		m.vel[0] += m.vel[0] / h * cartBoost
		m.vel[2] += m.vel[2] / h * cartBoost
		return
	}
	switch dir {
	case 1:
		if m.s.railKickOff(r, rail.Side(cube.FaceWest)) {
			m.vel[0] = cartKick
		} else if m.s.railKickOff(r, rail.Side(cube.FaceEast)) {
			m.vel[0] = -cartKick
		}
	case 0:
		if m.s.railKickOff(r, rail.Side(cube.FaceNorth)) {
			m.vel[2] = cartKick
		} else if m.s.railKickOff(r, rail.Side(cube.FaceSouth)) {
			m.vel[2] = -cartKick
		}
	}
}

// railKickOff is Block::isSolidBlockingBlockAndNotSignalSource: BDS's flags as Helix has
// them, with the full blocks that give power left out by name.
func (s *Host) railKickOff(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	if !ok || !s.solidBlock(b) {
		return false
	}
	switch s.reg.Blocks.Name(b) {
	case "minecraft:redstone_block", "minecraft:observer", "minecraft:target":
		return false
	}
	return true
}

// turn is BDS's postNormalTick rotation: it faces the way it moves, and flips over rather
// than turn right round.
func (m *Minecart) turn(from, to mgl32.Vec3, rot world.Rotation) world.Rotation {
	yaw := rot.Yaw
	dx, dz := from[0]-to[0], from[2]-to[2]
	if dx*dx+dz*dz > cartMoved {
		yaw = float32(math.Atan2(float64(dz), float64(dx)) * 180 / math.Pi)
		if m.flipped {
			yaw += 180
		}
	}
	d := wrapDegrees(yaw - rot.Yaw)
	if d < -cartTurnFlip || d >= cartTurnFlip {
		yaw += 180
		m.flipped = !m.flipped
	}
	yaw = wrapDegrees(yaw)
	return world.Rotation{Yaw: yaw, HeadYaw: yaw}
}

func wrapDegrees(v float32) float32 {
	v = float32(math.Mod(float64(v), 360))
	switch {
	case v >= 180:
		v -= 360
	case v < -180:
		v += 360
	}
	return v
}

// box is the cart's box at pos.
func cartBox(pos mgl32.Vec3) (lo, hi mgl32.Vec3) {
	lo = mgl32.Vec3{pos[0] - cartHalfWidth, pos[1] - cartOffset, pos[2] - cartHalfWidth}
	return lo, mgl32.Vec3{pos[0] + cartHalfWidth, lo[1] + cartHeight, pos[2] + cartHalfWidth}
}

// pushes is what touches the cart: players walking into it push it (BDS's Mob::pushActors
// from the player's side), and it pushes other carts it runs into along its rail
// (postNormalTick).
func (m *Minecart) pushes(r *world.Region, e *world.Entity, pos mgl32.Vec3) {
	lo, hi := cartBox(pos)
	along := m.pushAlong(r, pos)
	m.near = r.AppendEntitiesIn(m.near[:0], pos[0]-cartScan, pos[2]-cartScan, pos[0]+cartScan, pos[2]+cartScan)
	for _, o := range m.near {
		if o == e {
			continue
		}
		switch owner := o.Owner().(type) {
		case *Player:
			if owner == m.rider() || owner.dead() || owner.gameMode == GameModeSpectator {
				continue
			}
			pLo, pHi, _, ok := m.s.EntityBox(o)
			if !ok || !overlaps(lo.Sub(mgl32.Vec3{cartPushReach, 0, cartPushReach}), hi.Add(mgl32.Vec3{cartPushReach, 0, cartPushReach}), pLo, pHi) {
				continue
			}
			at := o.Position()
			if v, ok := cartPushVec(pos, mgl32.Vec3{at[0], pos[1], at[2]}); ok {
				m.vel = m.vel.Sub(v)
			}
		case *Minecart:
			oLo, oHi := cartBox(o.Position())
			if owner.gone || !overlaps(lo.Sub(along), hi.Add(along), oLo, oHi) {
				continue
			}
			owner.pushedBy(m, pos)
		}
	}
}

// pushAlong is how far along its rail the cart pushes others: nothing on a curve or off
// rails.
func (m *Minecart) pushAlong(r *world.Region, pos mgl32.Vec3) mgl32.Vec3 {
	at := floorPos(pos)
	if !m.s.railBlock(r, at) && m.s.railBlock(r, at.Side(cube.FaceDown)) {
		at = at.Side(cube.FaceDown)
	}
	b, ok := r.Block(at, 0)
	if !ok || !isRailName(m.s.reg.Blocks.Name(b)) {
		return mgl32.Vec3{}
	}
	switch railDir(m.s.reg.Blocks.States(b)) {
	case 1, 2, 3:
		return mgl32.Vec3{cartPushAlong, 0, 0}
	case 0, 4, 5:
		return mgl32.Vec3{0, 0, cartPushAlong}
	}
	return mgl32.Vec3{}
}

func overlaps(aLo, aHi, bLo, bHi mgl32.Vec3) bool {
	return aHi[0] > bLo[0] && aLo[0] < bHi[0] && aHi[1] > bLo[1] && aLo[1] < bHi[1] && aHi[2] > bLo[2] && aLo[2] < bHi[2]
}

// cartPushVec is the legacy_minecart push between two positions: BDS's direction, scaled
// down within a block.
func cartPushVec(self, other mgl32.Vec3) (mgl32.Vec3, bool) {
	d := mgl32.Vec3{other[0] - self[0], 0, other[2] - self[2]}
	d2 := d.Dot(d)
	if d2 < cartPushMin {
		return mgl32.Vec3{}, false
	}
	length := float32(math.Sqrt(float64(d2)))
	return d.Mul(min(1, 1/length) / length * cartPush), true
}

// pushedBy is PushableComponent's minecart rule between carts: only when they meet along
// the way this one faces, and then they mostly share their speed.
func (m *Minecart) pushedBy(o *Minecart, oPos mgl32.Vec3) {
	pos := m.entity.Position()
	v, ok := cartPushVec(pos, oPos)
	if !ok {
		return
	}
	yaw := float64(m.entity.Rotation().Yaw) * math.Pi / 180
	dir := v.Normalize()
	if abs32(dir[0]*float32(math.Cos(yaw))+dir[2]*float32(math.Sin(yaw))) < cartPushAligned {
		return
	}
	m.vel[0], m.vel[2] = m.vel[0]*cartPushKeep, m.vel[2]*cartPushKeep
	o.vel[0], o.vel[2] = o.vel[0]*cartPushKeep, o.vel[2]*cartPushKeep
	avgX, avgZ := (m.vel[0]+o.vel[0])/2, (m.vel[2]+o.vel[2])/2
	m.vel[0] += avgX - v[0]
	m.vel[2] += avgZ - v[2]
	o.vel[0] += avgX + v[0]
	o.vel[2] += avgZ + v[2]
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// railSensor is RailActivatorSystem: every tick on an activator rail, with no edge. Power
// throws out a rider, stops a hopper and lights TNT; a rail with no power starts a hopper
// again. Other blocks do nothing: no vanilla cart sets check_block_types.
func (m *Minecart) railSensor(r *world.Region, pos mgl32.Vec3) {
	at := floorPos(pos)
	if m.s.railBlock(r, at.Side(cube.FaceDown)) {
		at = at.Side(cube.FaceDown)
	}
	b, ok := r.Block(at, 0)
	if !ok || m.s.reg.Blocks.Name(b) != nameActivatorRail {
		return
	}
	on := railBit(m.s.reg.Blocks.States(b))
	switch {
	case m.spec.rides:
		if on {
			letOffAll(m)
		}
	case m.spec.hopper:
		m.hopperOff = on
	case m.spec.tnt:
		if on {
			m.prime(cartTNTFuse)
		}
	}
}

// tickHopper is HopperComponent::pullInItems, on HopperSystem's timer: the same pull as a
// hopper block from where the cart's middle is, then an item lying at the cart.
func (m *Minecart) tickHopper(r *world.Region, pos mgl32.Vec3) {
	if m.hopperOff || m.chest == nil {
		return
	}
	if m.hopperWait > 0 {
		m.hopperWait--
		return
	}
	m.hopperWait = m.s.redstone.Get().CartHopper.Every - 1
	lo, hi := cartBox(pos)
	centre := floorPos(mgl32.Vec3{pos[0], (lo[1] + hi[1]) / 2, pos[2]})
	if !m.chest.fullSlots(m.chest.halfSize()) && m.chest.pullOrCollect(r, centre, &m.s.redstone.Get().Hopper) != hopperIdle {
		return
	}
	m.pickUpAround(r, lo, hi)
}

// pickUpAround takes the first item lying within reach of the cart's box.
func (m *Minecart) pickUpAround(r *world.Region, lo, hi mgl32.Vec3) {
	reach := m.s.redstone.Get().CartHopper.Reach
	grow := mgl32.Vec3{reach, 0, reach}
	lo, hi = lo.Sub(grow), hi.Add(grow)
	m.near = r.AppendEntitiesIn(m.near[:0], lo[0], lo[2], hi[0], hi[2])
	for _, o := range m.near {
		i, ok := o.Owner().(*ItemActor)
		if !ok || i.taken || i.rule != nil || i.pickupDelay == itemNeverPickedUp {
			continue
		}
		iLo, iHi, _, _ := m.s.EntityBox(o)
		if !overlaps(lo, hi, iLo, iHi) {
			continue
		}
		left := m.chest.Inventory(r).AddItem(i.stack)
		switch {
		case left.Empty():
			i.taken = true
			i.entity.Remove()
		case left.Count != i.stack.Count:
			i.stack.Count = left.Count
			i.sendStackSize()
		}
		return
	}
}

// prime lights a TNT cart, unless it is lit or the game rule says no.
func (m *Minecart) prime(fuse int32) {
	if !m.spec.tnt || m.lit || !m.s.level.TNTExplodes {
		return
	}
	m.lit, m.fuse = true, fuse
	e := m.entity
	e.Send(actorData(e.RuntimeID(), protocol.EntityMetadata{
		protocol.EntityDataKeyFlags: int64(m.flags()), protocol.EntityDataKeyFuseTime: m.fuse,
	}))
	if r := e.Region(); r != nil {
		levelEvent(r, floorPos(e.Position()), e.Position(), packet.LevelEventSoundFuse, 0)
	}
}

// tickFuse burns the fuse of a lit TNT cart and sets it off at the end.
func (m *Minecart) tickFuse(r *world.Region, e *world.Entity) {
	if !m.lit {
		return
	}
	if m.fuse > 0 {
		m.fuse--
		e.Send(actorData(e.RuntimeID(), protocol.EntityMetadata{protocol.EntityDataKeyFuseTime: m.fuse}))
		return
	}
	m.gone = true
	if m.s.level.TNTExplodes {
		at := e.Position()
		x := NewExplosion(at, cartTNTPower)
		x.Source, x.Cause, x.Kind = e, DamageEntityExplosion, ExplosionTNTMinecart
		if !m.s.level.TNTExplosionDropDecay {
			x.DropRadius = 1
		}
		m.s.Explode(r, x)
	}
	m.remove(r)
}

// remove takes the cart out of the world with its rider and viewers let go.
func (m *Minecart) remove(r *world.Region) {
	m.gone = true
	letOffAll(m)
	if m.chest != nil {
		m.chest.closeViewers(r)
	}
	m.entity.Remove()
}

func (m *Minecart) sendHurt(e *world.Entity) {
	e.Send(actorData(e.RuntimeID(), protocol.EntityMetadata{
		protocol.EntityDataKeyHurt: m.hurtTime, protocol.EntityDataKeyHurtDirection: m.hurtSide,
		protocol.EntityDataKeyStructuralIntegrity: m.integrity,
	}))
}

// Hurt is BDS's Minecart::_hurt: the shake, and it breaks once its integrity is gone or at
// once for a player in creative, who gets no drops. True if it broke.
func (m *Minecart) Hurt(r *world.Region, damage float32, by *Player) bool {
	if m.gone {
		return false
	}
	m.hurtSide = -m.hurtSide
	if m.hurtSide == 0 {
		m.hurtSide = 1
	}
	m.hurtTime = cartHurtTime
	m.integrity = int32(float32(m.integrity) - damage*cartHitScale)
	m.sendHurt(m.entity)
	creative := by != nil && by.creative()
	if !creative && m.integrity > 0 {
		return false
	}
	m.Break(r, !creative)
	return true
}

// Break is BDS's destroy: what is inside spills, and with drops the cart leaves its item
// and its block's item. A TNT cart in a fire is another matter: see [Minecart.Burn].
func (m *Minecart) Break(r *world.Region, drops bool) {
	if m.gone {
		return
	}
	pos := m.entity.Position()
	if m.chest != nil {
		for i, size := 0, m.chest.halfSize(); i < size; i++ {
			if s := m.chest.items[i]; !s.Empty() {
				m.s.spawnItem(r.Dimension(), s, pos, mgl32.Vec3{}, world.Rotation{}, cartDropDelay, 0)
				m.chest.items[i] = ItemStack{}
			}
		}
	}
	if drops {
		if m.spec.block != "" {
			at := pos
			if m.spec.tnt {
				at[1]++
			}
			m.s.spawnItem(r.Dimension(), ItemStack{Name: m.spec.block, Count: 1}, at, mgl32.Vec3{}, world.Rotation{}, cartDropDelay, 0)
		}
		m.s.spawnItem(r.Dimension(), ItemStack{Name: nameMinecart, Count: 1}, pos, mgl32.Vec3{}, world.Rotation{}, cartDropDelay, 0)
	}
	m.remove(r)
}

// Burn is a TNT cart taken by fire or lava: BDS lights it instead of breaking it.
func (m *Minecart) Burn() {
	m.prime(cartTNTBurnMin + rand.Int32N(cartTNTBurnExtra)) //nolint:gosec // a fuse, not a secret
}

// FallOn is a fall ending: a TNT cart goes off at once after a long one.
func (m *Minecart) FallOn(distance float32) {
	if distance > cartTNTFall {
		m.prime(0)
	}
}

// BlastBox is its box.
func (m *Minecart) BlastBox() (lo, hi mgl32.Vec3, ok bool) {
	if m.gone || m.entity == nil {
		return lo, hi, false
	}
	lo, hi = cartBox(m.entity.Position())
	return lo, hi, true
}

// BlastHit lights a TNT cart and hurts any other.
func (m *Minecart) BlastHit(damage float32, _ DamageSource) {
	if m.spec.tnt {
		m.prime(cartTNTFuse)
		return
	}
	if r := m.entity.Region(); r != nil {
		m.Hurt(r, damage, nil)
	}
}

// BlastPush adds to its speed.
func (m *Minecart) BlastPush(push mgl32.Vec3) {
	m.vel = m.vel.Add(push)
	pushed(m.entity, m.vel)
}

// use is a player using the cart: sit in it, open it, or light it.
func (m *Minecart) use(p *Player) {
	switch {
	case m.spec.rides:
		mount(m, p)
	case m.chest != nil:
		p.openCart(m)
	case m.spec.tnt:
		if ignites(p.inventory.Held()) && !m.lit && m.s.level.TNTExplodes {
			m.prime(cartTNTFuse)
			p.hurtHeld(1)
		}
	}
}

// cartRideRules is the rideable component of entities/minecart.json.
func cartRideRules() *ride.Rules {
	return &ride.Rules{
		SeatCount: 1, CrouchingSkipInteract: true, PullIn: true,
		Seats: []ride.Seat{{Position: mgl32.Vec3{0, cartSeatY, 0}}},
	}
}

// riders, rideEntity, rideFrame and rideMoved make the cart a vehicle.
func (m *Minecart) riders() *ride.Vehicle { return m.seats }

func (m *Minecart) rideEntity() *world.Entity { return m.entity }

func (m *Minecart) rideMoved() mgl32.Vec3 { return m.moved }

func (m *Minecart) rideTop() float32 { return m.entity.Position()[1] - cartOffset + cartHeight }

// settleRiders lets go of riders who have left, died or gone away.
func (m *Minecart) settleRiders() {
	if m.seats != nil {
		settleRiders(m)
	}
}

// openCart opens the cart's slots for the player, as BDS's ContainerComponent (0x737E1B0,
// 0x737E270) then its window (0x4A43A40): refused without the ability, for one who is dead or
// has a window open, and for a cart that is gone. The window names the block the cart is in
// and the cart.
func (p *Player) openCart(m *Minecart) {
	r := p.entity.Region()
	w := &p.window
	if r == nil || w.open || m.gone || m.chest == nil || p.dead() || !p.abilities.Flag(AbilityOpenContainers) || m.entity.Region() != r {
		return
	}
	o := windowOpening{
		kind: m.chest.Kind(), window: m.spec.opens, pos: floorPos(m.entity.Position()), entity: m.entity.UniqueID(),
		inv: m.chest.Inventory(r), cart: m,
	}
	if !p.mayOpen(r, &o) {
		return
	}
	p.showWindow(r, &o)
	m.chest.addViewer(r, p)
}

// windowChest is the chest the player has open, whatever holds it. Nil if it is gone. A cart
// also counts as gone once it is not in this region, unloaded or handed on: another core owns
// its slots then, and the items in memory may already be saved, so taking them would make
// more.
func (p *Player) windowChest(r *world.Region) *Chest {
	if m := p.window.cart; m != nil {
		if m.gone || m.entity.Region() != r {
			return nil
		}
		return m.chest
	}
	return p.s.chestAt(r, p.window.pos)
}

// dropGoneViewers lets go of those who have the cart open but are in no region with it, who
// cannot do so themselves.
func (m *Minecart) dropGoneViewers(r *world.Region) {
	if m.chest == nil || len(m.chest.viewers) == 0 {
		return
	}
	for _, v := range slices.Clone(m.chest.viewers) {
		if v.entity == nil || v.entity.Region() != r {
			m.chest.removeViewer(r, v)
		}
	}
}

// cartNear finds a minecart near the player by its runtime ID.
func (p *Player) cartNear(id uint64) *Minecart {
	r := p.entity.Region()
	if r == nil {
		return nil
	}
	pos := p.entity.Position()
	p.touching = r.AppendEntitiesIn(p.touching[:0], pos[0]-attackScan, pos[2]-attackScan, pos[0]+attackScan, pos[2]+attackScan)
	for _, e := range p.touching {
		if e.RuntimeID() != id {
			continue
		}
		if m, ok := e.Owner().(*Minecart); ok && !m.gone {
			return m
		}
	}
	return nil
}

// cartInReach finds a minecart close enough for the player to use, by its runtime ID.
func (p *Player) cartInReach(id uint64) *Minecart {
	m := p.cartNear(id)
	if m == nil || p.entity.Position().Sub(m.entity.Position()).Len() > cartReach {
		return nil
	}
	return m
}

// useOnCart is a use on actor transaction aimed at a minecart. False if it was not.
func (p *Player) useOnCart(d *protocol.UseItemOnEntityTransactionData) bool {
	m := p.cartNear(d.TargetEntityRuntimeID)
	if m == nil {
		return false
	}
	pos := p.entity.Position()
	if pos.Sub(m.entity.Position()).Len() > cartReach {
		return true
	}
	if d.ActionType == protocol.UseItemOnEntityActionInteract {
		m.use(p)
		return true
	}
	if d.ActionType == protocol.UseItemOnEntityActionAttack {
		if r := p.entity.Region(); r != nil {
			held := p.inventory.Held()
			damage := p.attributes.get(attrAttackDamage) + p.Tuning(TuneAttackDamage)
			if !held.Empty() {
				damage += float32(p.s.game.combat.damage[held.Name])
			}
			m.Hurt(r, damage, p)
		}
	}
	return true
}

// placeCart is BDS's MinecartItem::_useOn: on a rail with nothing else in the way, in the
// middle of its block. The use counts either way.
func (p *Player) placeCart(r *world.Region, clicked cube.Pos, id string) bool {
	if !p.s.railBlock(r, clicked) || !p.inReach(clicked) {
		return false
	}
	lo := blockVec(clicked)
	hi := lo.Add(mgl32.Vec3{1, 1, 1})
	p.touching = r.AppendEntitiesIn(p.touching[:0], lo[0]-cartScan, lo[2]-cartScan, hi[0]+cartScan, hi[2]+cartScan)
	for _, e := range p.touching {
		if m, ok := e.Owner().(*Minecart); ok && !m.gone {
			mLo, mHi := cartBox(e.Position())
			if overlaps(lo, hi, mLo, mHi) {
				return true
			}
		}
	}
	p.s.SpawnMinecart(r.Dimension(), id, lo.Add(mgl32.Vec3{0.5, 0.5, 0.5}))
	if !p.creative() {
		p.useHeld()
	}
	return true
}

// dispenseCart is BDS's MinecartItem::dispense: a rail in front, or a rail under an empty
// block in front. False throws the item out as any other.
func (s *Host) dispenseCart(r *world.Region, front cube.Pos, dir mgl32.Vec3, id string) bool {
	y := float32(front[1])
	if !s.railBlock(r, front) {
		b, ok := r.Block(front, 0)
		if !ok || b != s.reg.Blocks.Air() || !s.railBlock(r, front.Side(cube.FaceDown)) {
			return false
		}
		y--
	}
	s.SpawnMinecart(r.Dimension(), id, mgl32.Vec3{
		float32(front[0]) + 0.5 + dir[0]*cartDispenseOut, y + 0.5, float32(front[2]) + 0.5 + dir[2]*cartDispenseOut,
	})
	return true
}

// CartBox says whether an entity is a minecart and what a comparator reads of it, -1 if
// it holds no items. Runs: owner of the entity's region.
func (s *Host) CartBox(e *world.Entity) (int32, bool) {
	m, ok := e.Owner().(*Minecart)
	if !ok || m.gone {
		return 0, false
	}
	if m.chest == nil {
		return -1, true
	}
	r := e.Region()
	if r == nil {
		return -1, true
	}
	return m.chest.ComparatorSignal(r, cube.Pos{}), true
}

// cartsAt calls fn for every cart with slots whose box overlaps the block at pos, as BDS's
// hoppers see containers in the block's space, and stops when fn says so.
func (s *Host) cartsAt(r *world.Region, pos cube.Pos, skip *Chest, fn func(c *Chest) bool) bool {
	lo := blockVec(pos)
	hi := lo.Add(mgl32.Vec3{1, 1, 1})
	var buf [16]*world.Entity
	for _, e := range r.AppendEntitiesIn(buf[:0], lo[0]-1, lo[2]-1, hi[0]+1, hi[2]+1) {
		m, ok := e.Owner().(*Minecart)
		if !ok || m.gone || m.chest == nil || m.chest == skip {
			continue
		}
		mLo, mHi := cartBox(e.Position())
		if overlaps(lo, hi, mLo, mHi) && fn(m.chest) {
			return true
		}
	}
	return false
}

func (m *Minecart) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if m.gone {
		return chunk.Entity{}, false
	}
	data := maps.Clone(m.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos, rot := e.Position(), e.Rotation()
	data["identifier"] = m.spec.id
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Motion"] = []float32{m.vel[0], m.vel[1], m.vel[2]}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	if m.chest != nil {
		items := []any{}
		for i, s := range m.chest.items {
			if !s.Empty() {
				items = append(items, s.nbt(i, true))
			}
		}
		data[cartKeyItems] = items
	}
	if m.spec.tnt {
		data[cartKeyFuse] = uint8(min(max(m.fuse, 0), math.MaxUint8)) //nolint:gosec // clamped
		data[cartKeyLit] = boolByte(m.lit)
	}
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadCart brings back a saved minecart.
func (s *Host) loadCart(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity, id string) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	m := s.newCart(id)
	if m == nil {
		return false
	}
	m.saved = maps.Clone(saved.Data)
	if v, isList := floats(saved.Data["Motion"]); isList && len(v) == 3 {
		m.vel = mgl32.Vec3{v[0], v[1], v[2]}
	}
	var rot world.Rotation
	if v, isList := floats(saved.Data["Rotation"]); isList && len(v) == 2 {
		rot = world.Rotation{Yaw: v[0], Pitch: v[1], HeadYaw: v[0]}
	}
	if m.chest != nil {
		for _, item := range compounds(saved.Data[cartKeyItems]) {
			slot, _ := item["Slot"].(uint8)
			if int(slot) < m.chest.halfSize() {
				m.chest.items[slot] = withID(itemFromNBT(item), s.nextStackID)
			}
		}
		delete(m.saved, cartKeyItems)
	}
	if m.spec.tnt {
		fuse, _ := saved.Data[cartKeyFuse].(uint8)
		lit, _ := saved.Data[cartKeyLit].(uint8)
		m.fuse, m.lit = int32(fuse), lit != 0
	}
	m.entity = r.AddLoadedEntity(at, m.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}, rot))
	return true
}
