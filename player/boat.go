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

// Boats and chest boats as BDS 1.26.60 runs them: BoatItem::_useOn (0xC9D4AC0) places them,
// Boat::_hurt (0xBD543F0) and destroy (0xBD54C60, ChestBoat 0xBD56CB0) break them, the boat's
// normal tick (0xBD55D40) brings its integrity back, and entities/boat.json and
// chest_boat.json give their seats and sizes. A boat someone steers is predicted by that
// player's client, as BDS's are, and Helix takes its place from the client as it takes the
// player's; one no one steers moves by itself (boatphysics.go). Not built yet: lava
// damage, pulling mobs in, dispensers placing boats, and the paddles other players see.
const (
	nameBoat      = "minecraft:boat"
	nameChestBoat = "minecraft:chest_boat"

	// boatWidth and boatHeight are collision_box.
	boatWidth  = 1.4
	boatHeight = 0.455
	// boatHeightOffset is Boat::reloadHardcoded's height offset (1.26.60 0xBD54040): the
	// boat's position, which BDS sends, saves and runs its physics on, is this far above
	// the bottom of its box. Actor::moveTo adds it to where a boat is put.
	boatHeightOffset = 0.375
	// boatIntegrity is what the boat starts with and comes back to, a point a tick.
	boatIntegrity = 40
	// boatHitScale is how much integrity a point of damage takes (1.26.60; 1.21.3 took one).
	boatHitScale = 10
	boatHurtTime = 10
	// boatDropDelay is the pick-up delay of what a broken boat leaves.
	boatDropDelay = 10
	// boatTurn is what BoatItem adds to the player's yaw for the boat's.
	boatTurn = 90

	// The seats of entities/boat.json: one rider in the middle, two side by side; a raft's
	// sit higher. Chest boats have only the front seat.
	boatSeatY      = -0.2
	raftSeatY      = 0.1
	boatSeatFront  = 0.2
	boatSeatBack   = -0.6
	boatSeatLock   = 90
	boatSeatTurn   = -90
	boatMaxWidth   = 1.375
	boatSeatsCount = 2

	// woodBamboo is the raft, whose seats differ.
	woodBamboo = 7

	// boatTraceTicks is how long an empty boat's movement is logged at debug level after
	// its driver gets out.
	boatTraceTicks = 40

	boatKeyVariant = "Variant"
	boatKeyItems   = "Items"
)

// boatWoods are the boat items by their wood number, as BDS registers BoatItem and
// ChestBoatItem (0xC66BA90) and the boat's Variant counts. Poplar is behind an experiment.
var boatWoods = [...]string{"oak", "spruce", "birch", "jungle", "acacia", "dark_oak", "mangrove", "bamboo", "cherry", "pale_oak", "poplar"}

// boatItemName is the item of a boat of wood, with a chest or not.
func boatItemName(wood int32, chest bool) string {
	if wood < 0 || int(wood) >= len(boatWoods) {
		wood = 0
	}
	if wood == woodBamboo {
		if chest {
			return "minecraft:bamboo_chest_raft"
		}
		return "minecraft:bamboo_raft"
	}
	if chest {
		return "minecraft:" + boatWoods[wood] + "_chest_boat"
	}
	return "minecraft:" + boatWoods[wood] + "_boat"
}

// boatItem reports which boat the item called name places: its wood and whether it has a
// chest.
func boatItem(name string) (wood int32, chest, ok bool) {
	for i := range boatWoods {
		w := int32(i) //nolint:gosec // a handful of woods
		switch name {
		case boatItemName(w, false):
			return w, false, true
		case boatItemName(w, true):
			return w, true, true
		}
	}
	return 0, false, false
}

// IsBoatItem reports whether the item called name is a boat or chest boat.
func IsBoatItem(name string) bool { _, _, ok := boatItem(name); return ok }

// Boat is a boat or chest boat. Runs: owner of its entity.
type Boat struct {
	s      *Host
	entity *world.Entity
	wood   int32
	name   string

	gone               bool
	seats              *ride.Vehicle
	moved              mgl32.Vec3
	hurtTime, hurtSide int32
	integrity          int32
	// motion is its own movement while no one steers it (boatphysics.go).
	motion boatMotion
	// lastEyes is the position the driver's client last sent; traceTicks counts down the
	// ticks logged at debug level after the driver got out.
	lastEyes   mgl32.Vec3
	traceTicks int32
	driven     bool
	// predicted is whether the driver's client said last it predicts the boat.
	predicted bool
	// awayIDs are riders who left the game in it: their seats wait for them.
	awayIDs []int64

	// chest is a chest boat's slots, a [Chest] with no block behind it.
	chest *Chest
	saved map[string]any
}

// Entity returns the boat's entity.
func (b *Boat) Entity() *world.Entity { return b.entity }

// Wood returns its wood number, the Variant BDS saves: 0 oak to 10 poplar, 7 the raft.
func (b *Boat) Wood() int32 { return b.wood }

// Chest returns a chest boat's slots, nil for a plain boat.
func (b *Boat) Chest() *Chest { return b.chest }

// Driver returns who steers it, if anyone: its first rider.
func (b *Boat) Driver() *Player {
	p, _ := b.seats.First().(*Player)
	return p
}

// SpawnBoat adds a boat of wood, with a chest or not, with the bottom of its box at feet,
// facing yaw. Runs: any goroutine.
func (s *Host) SpawnBoat(d *world.Dimension, wood int32, chest bool, feet mgl32.Vec3, yaw float32) *Boat {
	b := s.newBoat(wood, chest)
	pos := feet.Add(mgl32.Vec3{0, boatHeightOffset, 0})
	b.entity = d.AddEntity(b.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), pos, world.Rotation{Yaw: yaw, HeadYaw: yaw})) //nolint:gosec // an entity ID, not a secret
	return b
}

func (s *Host) newBoat(wood int32, chest bool) *Boat {
	if wood < 0 || int(wood) >= len(boatWoods) {
		wood = 0
	}
	b := &Boat{s: s, wood: wood, integrity: boatIntegrity}
	b.seats = ride.New(boatRideRules(wood, chest))
	if chest {
		b.chest = &Chest{s: s, lead: true, machine: chestBoatSlots, boat: b}
	}
	return b
}

// chestBoatSlots are a chest boat's: entities/chest_boat.json's inventory, 27 slots of
// container type chest_boat. BDS opens them as a plain container (0x737E270), as a chest cart.
var chestBoatSlots = &machineSpec{id: "ChestBoat", size: chestSize, window: byte(protocol.ContainerTypeChestBoat)}

// boatRideRules is the rideable of can_ride_default or can_ride_bamboo. Every family the
// second seats' rotate_rider_by names turns by -90, players among them; Helix seats only
// players.
func boatRideRules(wood int32, chest bool) *ride.Rules {
	y := float32(boatSeatY)
	if wood == woodBamboo {
		y = raftSeatY
	}
	seat := func(x float32, minRiders, maxRiders int) ride.Seat {
		return ride.Seat{
			Position: mgl32.Vec3{x, y, 0}, MinRiders: minRiders, MaxRiders: maxRiders,
			LockRotation: true, LockDegrees: boatSeatLock, Rotates: true, RotateBy: boatSeatTurn,
		}
	}
	if chest {
		return &ride.Rules{
			SeatCount: 1, CrouchingSkipInteract: true, PullIn: true, MaxWidth: boatMaxWidth, ExitAbove: true,
			Seats: []ride.Seat{seat(boatSeatFront, 0, 1)},
		}
	}
	return &ride.Rules{
		SeatCount: boatSeatsCount, CrouchingSkipInteract: true, PullIn: true, MaxWidth: boatMaxWidth, ExitAbove: true,
		Seats: []ride.Seat{seat(0, 0, 1), seat(boatSeatFront, 2, 2), seat(boatSeatBack, 2, 2)},
	}
}

func (b *Boat) id() string {
	if b.chest != nil {
		return nameChestBoat
	}
	return nameBoat
}

func (b *Boat) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: b, Spawn: b.spawnPackets, Move: b.movePacket, Input: b.tick, Save: b.saveEntity,
	}
}

// spawnPackets show it to a player. Not yet compared with what BDS sends.
func (b *Boat) spawnPackets(e *world.Entity) []packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:   int64(1<<protocol.EntityDataFlagHasGravity | 1<<protocol.EntityDataFlagHasCollision | 1<<protocol.EntityDataFlagStackable),
		protocol.EntityDataKeyVariant: b.wood,
		protocol.EntityDataKeyWidth:   float32(boatWidth), protocol.EntityDataKeyHeight: float32(boatHeight),
		protocol.EntityDataKeyScale:               float32(1),
		protocol.EntityDataKeyStructuralIntegrity: b.integrity,
		protocol.EntityDataKeyBuoyancyData:        boatBuoyancyData,
	}
	if b.chest != nil {
		meta[protocol.EntityDataKeyContainerType] = b.chest.machine.window
		meta[protocol.EntityDataKeyContainerSize] = int32(b.chest.machine.size) //nolint:gosec // small
	}
	if b.name != "" {
		meta[protocol.EntityDataKeyName] = b.name
	}
	rot := e.Rotation()
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: b.id(),
		Position: e.Position(), Yaw: rot.Yaw, HeadYaw: rot.Yaw, BodyYaw: rot.Yaw, EntityMetadata: meta,
		EntityLinks: b.seats.Links(e.UniqueID()),
	}}
}

func (b *Boat) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	var flags byte
	if e.OnGround() {
		flags = packet.MoveFlagOnGround
	}
	return &packet.MoveActorAbsolute{
		EntityRuntimeID: e.RuntimeID(), Flags: flags, Position: e.Position(), Rotation: mgl32.Vec3{rot.Pitch, rot.Yaw, rot.Yaw},
	}
}

// tick is the boat's normal tick: its riders, then the hurt counter and integrity as
// 0xBD55D40 brings them back.
func (b *Boat) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || b.gone {
		return
	}
	if b.s.checkVoid(e); b.gone {
		return
	}
	keepSeats(b)
	b.dropGoneViewers(r)
	if b.Driver() == nil {
		if b.driven {
			b.driven, b.traceTicks = false, boatTraceTicks
			b.s.log.Debug("boat left", "eyes", b.lastEyes, "boat", e.Position())
		}
		from := e.Position()
		b.physics(r, e)
		b.moved = e.Position().Sub(from)
		if b.traceTicks > 0 {
			b.traceTicks--
			b.s.log.Debug("boat after", "pos", e.Position(), "vel", b.motion.vel)
		}
	} else {
		b.driven = true
		b.motion.vel = mgl32.Vec3{}
	}
	changed := false
	if b.hurtTime > 0 {
		b.hurtTime--
		changed = true
	}
	if b.integrity > 0 && b.integrity < boatIntegrity {
		b.integrity++
		changed = true
	}
	if changed {
		b.sendHurt(e)
	}
}

// drive moves the boat where its driver's client put it, turned to yaw: at pos itself when
// the client predicts the boat, else so that the rider at pos (its eyes) is in its seat.
// Region tick of the driver.
func (b *Boat) drive(p *Player, pos mgl32.Vec3, yaw float32, predicted bool) {
	if b.gone || b.Driver() != p {
		return
	}
	e := b.entity
	b.lastEyes = pos
	if predicted != b.predicted {
		b.predicted = predicted
		b.s.log.Debug("boat prediction", "predicted", predicted, "pos", pos, "boat", e.Position())
	}
	if !predicted {
		off := b.seats.SeatOffset(p)
		sin, cos := math.Sincos(float64(yaw) * -math.Pi / 180)
		s, c := float32(sin), float32(cos)
		pos = mgl32.Vec3{pos[0] - off[0]*c - off[2]*s, pos[1] - off[1], pos[2] - off[2]*c + off[0]*s}
	}
	from := e.Position()
	if e.Apply(world.Movement{Pos: pos, Rot: world.Rotation{Yaw: yaw, HeadYaw: yaw}, Tick: e.Tick()}) {
		b.moved = pos.Sub(from)
	}
}

func (b *Boat) sendHurt(e *world.Entity) {
	e.Send(actorData(e.RuntimeID(), protocol.EntityMetadata{
		protocol.EntityDataKeyHurt: b.hurtTime, protocol.EntityDataKeyHurtDirection: b.hurtSide,
		protocol.EntityDataKeyStructuralIntegrity: b.integrity,
	}))
}

// Hurt is BDS's Boat::_hurt: the shake, damage times ten off its integrity, and it breaks
// once that is gone or at once for a player in creative, who gets no drops. True if it
// broke.
func (b *Boat) Hurt(r *world.Region, damage float32, by *Player) bool {
	if b.gone {
		return false
	}
	b.hurtSide = -b.hurtSide
	if b.hurtSide == 0 {
		b.hurtSide = 1
	}
	b.hurtTime = boatHurtTime
	b.integrity = int32(float32(b.integrity) - damage*boatHitScale)
	b.sendHurt(b.entity)
	creative := by != nil && by.creative()
	if !creative && b.integrity > 0 {
		return false
	}
	b.Break(r, !creative)
	return true
}

// Break is BDS's destroy: a chest boat's items spill first, then with drops and
// doentitydrops on the boat leaves its own item, named as the boat was.
func (b *Boat) Break(r *world.Region, drops bool) {
	if b.gone {
		return
	}
	pos := b.entity.Position()
	if b.chest != nil {
		size := b.chest.halfSize()
		b.s.spillSlots(r, b.chest.items[:size], pos, false, boatDropDelay)
		clear(b.chest.items[:size])
	}
	if drops && b.s.level.DoEntityDrops {
		item := ItemStack{Name: boatItemName(b.wood, b.chest != nil), Count: 1}
		if b.name != "" {
			item = item.WithName(b.name)
		}
		b.s.spawnItem(r.Dimension(), item, pos, mgl32.Vec3{}, world.Rotation{}, boatDropDelay, 0)
	}
	b.remove(r)
}

// remove takes the boat out of the world with its riders and viewers let go.
func (b *Boat) remove(r *world.Region) {
	b.gone = true
	letOffAll(b)
	if b.chest != nil {
		b.chest.closeViewers(r)
	}
	b.entity.Remove()
}

// OutOfWorld removes the boat, letting its riders go; its items go with it.
func (b *Boat) OutOfWorld(_ *Host, e *world.Entity) {
	if r := e.Region(); r != nil {
		b.remove(r)
	}
}

// BlastBox is its box.
func (b *Boat) BlastBox() (lo, hi mgl32.Vec3, ok bool) {
	if b.gone || b.entity == nil {
		return lo, hi, false
	}
	lo, hi = boatBox(b.entity.Position())
	return lo, hi, true
}

// BlastHit hurts it.
func (b *Boat) BlastHit(damage float32, _ DamageSource) {
	if r := b.entity.Region(); r != nil {
		b.Hurt(r, damage, nil)
	}
}

// BlastPush adds an explosion's push to an empty boat's movement.
func (b *Boat) BlastPush(push mgl32.Vec3) { b.motion.vel = b.motion.vel.Add(push) }

// boatBox is the boat's box at pos, its position (boatHeightOffset above the bottom).
func boatBox(pos mgl32.Vec3) (lo, hi mgl32.Vec3) {
	half := float32(boatWidth / 2)
	y := pos[1] - boatHeightOffset
	return mgl32.Vec3{pos[0] - half, y, pos[2] - half}, mgl32.Vec3{pos[0] + half, y + boatHeight, pos[2] + half}
}

// riders, rideEntity, rideMoved and rideTop make the boat a vehicle.
func (b *Boat) riders() *ride.Vehicle { return b.seats }

func (b *Boat) rideEntity() *world.Entity { return b.entity }

func (b *Boat) rideMoved() mgl32.Vec3 { return b.moved }

// away and keeperGone make it a seatKeeper.
func (b *Boat) away() *[]int64 { return &b.awayIDs }

func (b *Boat) keeperGone() bool { return b.gone }

func (b *Boat) rideTop() float32 { return b.entity.Position()[1] - boatHeightOffset + boatHeight }

// use is a player using the boat: get in, or with a chest boat while crouching (which
// rideable skips) open its chest.
func (b *Boat) use(p *Player) {
	if b.chest != nil && p.Moving(MoveSneaking) {
		p.openBoat(b)
		return
	}
	if seatsHeld(b) {
		return
	}
	mount(b, p)
}

// openBoat opens a chest boat's slots for the player, as BDS's ContainerComponent opens a
// chest cart's: refused without the ability, for one who is dead or has a window open, and
// for a boat that is gone.
func (p *Player) openBoat(b *Boat) {
	r := p.entity.Region()
	w := &p.window
	if r == nil || w.open || b.gone || b.chest == nil || p.dead() || !p.abilities.Flag(AbilityOpenContainers) || b.entity.Region() != r {
		return
	}
	o := windowOpening{
		kind: ContainerChestBoat, window: byte(protocol.ContainerTypeContainer), pos: floorPos(b.entity.Position()), entity: b.entity.UniqueID(),
		inv: b.chest.Inventory(r), boat: b,
	}
	if !p.mayOpen(r, &o) {
		return
	}
	p.showWindow(r, &o)
	b.chest.addViewer(r, p)
}

// dropGoneViewers lets go of those who have the boat open but are in no region with it.
func (b *Boat) dropGoneViewers(r *world.Region) {
	if b.chest == nil || len(b.chest.viewers) == 0 {
		return
	}
	for _, v := range slices.Clone(b.chest.viewers) {
		if v.entity == nil || v.entity.Region() != r {
			b.chest.removeViewer(r, v)
		}
	}
}

// boatNear finds a boat near the player by its runtime ID.
func (p *Player) boatNear(id uint64) *Boat {
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
		if b, ok := e.Owner().(*Boat); ok && !b.gone {
			return b
		}
	}
	return nil
}

// boatInReach finds a boat close enough for the player to use, by its runtime ID.
func (p *Player) boatInReach(id uint64) *Boat {
	b := p.boatNear(id)
	if b == nil || p.entity.Position().Sub(b.entity.Position()).Len() > cartReach {
		return nil
	}
	return b
}

// useOnBoat is a use on actor transaction aimed at a boat. False if it was not.
func (p *Player) useOnBoat(d *protocol.UseItemOnEntityTransactionData) bool {
	b := p.boatNear(d.TargetEntityRuntimeID)
	if b == nil {
		return false
	}
	if p.entity.Position().Sub(b.entity.Position()).Len() > cartReach {
		return true
	}
	switch d.ActionType {
	case protocol.UseItemOnEntityActionInteract:
		b.use(p)
	case protocol.UseItemOnEntityActionAttack:
		if r := p.entity.Region(); r != nil {
			held := p.inventory.Held()
			damage := p.attributes.get(attrAttackDamage) + p.Tuning(TuneAttackDamage)
			if !held.Empty() {
				damage += float32(p.s.game.combat.damage[held.Name])
			}
			b.Hurt(r, damage, p)
		}
	}
	return true
}

// placeBoat is BDS's BoatItem::_useOn: only on the top of a block with air above, at the
// point clicked, turned to the player's yaw plus 90, and only where no entity but a dropped
// item is in the box BDS checks. The boat takes the item's name. The use counts once a
// boat is made; the item is used up outside creative.
func (p *Player) placeBoat(r *world.Region, clicked cube.Pos, face int32, click mgl32.Vec3, held ItemStack) bool {
	wood, chest, ok := boatItem(held.Name)
	if !ok || face != int32(cube.FaceUp) || !p.inReach(clicked) {
		return false
	}
	if above, loaded := r.Block(clicked.Side(cube.FaceUp), 0); !loaded || above != p.s.reg.Blocks.Air() {
		return false
	}
	pos := blockVec(clicked).Add(click)
	// BDS builds this box from the boat's size as x and z by half its height, y up by its
	// width (getAABBDim's two values, 0xC9D4AC0 and 1.21.3 alike).
	half := float32(boatHeight / 2)
	lo := mgl32.Vec3{pos[0] - half, pos[1], pos[2] - half}
	hi := mgl32.Vec3{pos[0] + half, pos[1] + boatWidth, pos[2] + half}
	p.touching = r.AppendEntitiesIn(p.touching[:0], lo[0]-cartScan, lo[2]-cartScan, hi[0]+cartScan, hi[2]+cartScan)
	for _, e := range p.touching {
		if _, item := e.Owner().(*ItemActor); item {
			continue
		}
		eLo, eHi, _, known := p.s.EntityBox(e)
		if known && overlaps(lo, hi, eLo, eHi) {
			return false
		}
	}
	b := p.s.SpawnBoat(r.Dimension(), wood, chest, pos, p.entity.Rotation().Yaw+boatTurn)
	if name := customName(held); name != "" {
		b.name = name
	}
	if !p.creative() {
		p.useHeld()
	}
	return true
}

func (b *Boat) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if b.gone {
		return chunk.Entity{}, false
	}
	data := maps.Clone(b.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos, rot := e.Position(), e.Rotation()
	data["identifier"] = b.id()
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	data[boatKeyVariant] = b.wood
	if b.name != "" {
		data["CustomName"] = b.name
	} else {
		delete(data, "CustomName")
	}
	if b.chest != nil {
		items := []any{}
		for i, s := range b.chest.items {
			if !s.Empty() {
				items = append(items, s.nbt(i, true))
			}
		}
		data[boatKeyItems] = items
	}
	saveLinks(data, b)
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadBoat brings back a saved boat or chest boat.
func (s *Host) loadBoat(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity, id string) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	wood, _ := saved.Data[boatKeyVariant].(int32)
	b := s.newBoat(wood, id == nameChestBoat)
	b.saved = maps.Clone(saved.Data)
	b.name, _ = saved.Data["CustomName"].(string)
	b.awayIDs = loadLinks(saved.Data)
	var rot world.Rotation
	if v, isList := floats(saved.Data["Rotation"]); isList && len(v) == 2 {
		rot = world.Rotation{Yaw: v[0], Pitch: v[1], HeadYaw: v[0]}
	}
	if b.chest != nil {
		for _, item := range compounds(saved.Data[boatKeyItems]) {
			slot, _ := item["Slot"].(uint8)
			if int(slot) < b.chest.halfSize() {
				b.chest.items[slot] = withID(itemFromNBT(item), s.nextStackID)
			}
		}
		delete(b.saved, boatKeyItems)
	}
	b.entity = r.AddLoadedEntity(at, b.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}, rot))
	return true
}
