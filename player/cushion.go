package player

import (
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/ride"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Cushions as BDS 1.26.60 runs them (CushionItem, the Cushion actor, UpdateCushionSurvivalSystem,
// entities/cushion.json). The research note is private.
const (
	cushionID         = "minecraft:cushion"
	cushionItemSuffix = "_cushion"
	// cushionWidth and cushionHeight are its collision box.
	cushionWidth  = 0.999
	cushionHeight = 0.249
	// cushionSeatY is its seat's height and cushionSeatTurn how the rider is turned on it.
	cushionSeatY    = 0.0625
	cushionSeatTurn = -90
	// cushionClick is how far above and below the click BDS looks for a cushion already there.
	cushionClick = 0.001
	// A cushion checks it still rests on something every cushionCheckMin plus up to
	// cushionCheckSpread ticks, and looks cushionRest below itself for it.
	cushionCheckMin    = 80
	cushionCheckSpread = 41
	cushionRest        = 1.0 / 64
	// cushionDropDelay is the pick-up delay of a broken cushion's item.
	cushionDropDelay = 10
	// The keys BDS saves a cushion's colour and riders under.
	cushionVariantTag = "Variant"
)

// dyeColors are the colours in dye order (black first), the order a cushion's variant is
// read in by the client and saved in. Wool and beds count the other way, from white.
var dyeColors = [16]string{
	"black", "red", "green", "brown", "blue", "purple", "cyan", "light_gray",
	"gray", "pink", "lime", "yellow", "light_blue", "magenta", "orange", "white",
}

// cushionColor is the colour of the cushion item called name; false if it is none.
func cushionColor(name string) (uint8, bool) {
	c, ok := strings.CutPrefix(name, "minecraft:")
	if !ok {
		return 0, false
	}
	c, ok = strings.CutSuffix(c, cushionItemSuffix)
	if !ok {
		return 0, false
	}
	i := slices.Index(dyeColors[:], c)
	return uint8(i), i >= 0 //nolint:gosec // below 16
}

// Cushion is a cushion: one seat, on whatever it was put on. A player sitting on it stays
// sitting through leaving the game and the server stopping, until it gets off. Runs: owner
// of its entity.
type Cushion struct {
	s      *Host
	entity *world.Entity
	color  uint8
	name   string
	seats  *ride.Vehicle
	gone   bool
	// awayIDs are riders who left the game while sitting: their seat waits for them.
	awayIDs []int64
	// check is the ticks to its next look at what it rests on.
	check int32
	saved map[string]any
}

// Entity returns the cushion's entity.
func (c *Cushion) Entity() *world.Entity { return c.entity }

// Color returns its colour, in BDS's dye order.
func (c *Cushion) Color() uint8 { return c.color }

// Rider returns who sits on it, if anyone online.
func (c *Cushion) Rider() *Player {
	p, _ := c.seats.First().(*Player)
	return p
}

// cushionRideRules is the rideable component of entities/cushion.json.
func cushionRideRules() *ride.Rules {
	return &ride.Rules{
		SeatCount: 1, CrouchingSkipInteract: true, Dismount: ride.DismountOnTopCenter,
		Seats: []ride.Seat{{Position: mgl64.Vec3{0, cushionSeatY, 0}, Rotates: true, RotateBy: cushionSeatTurn}},
	}
}

func (s *Host) newCushion(color uint8) *Cushion {
	return &Cushion{s: s, color: color, seats: ride.New(cushionRideRules()), check: cushionCheckMin + rand.Int32N(cushionCheckSpread)} //nolint:gosec // a delay, not a secret
}

// SpawnCushion adds a cushion of the colour given, facing yaw. Runs: any goroutine.
func (s *Host) SpawnCushion(d *world.Dimension, pos mgl64.Vec3, yaw float64, color uint8) *Cushion {
	c := s.newCushion(color & 0xf)
	c.entity = d.AddEntity(c.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), pos, world.Rotation{Yaw: float32(yaw), HeadYaw: float32(yaw)})) //nolint:gosec // an entity ID, not a secret
	return c
}

func (c *Cushion) config(uniqueID int64, runtimeID uint64, pos mgl64.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: c, Spawn: c.spawnPackets, Move: c.movePacket, Input: c.tick, Save: c.saveEntity,
	}
}

// spawnPackets show it to a player. Not yet compared with what BDS sends.
func (c *Cushion) spawnPackets(e *world.Entity) []packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:   int64(1 << protocol.EntityDataFlagHasCollision),
		protocol.EntityDataKeyVariant: int32(c.color),
		protocol.EntityDataKeyWidth:   float32(cushionWidth), protocol.EntityDataKeyHeight: float32(cushionHeight),
		protocol.EntityDataKeyScale: float32(1),
	}
	if c.name != "" {
		meta[protocol.EntityDataKeyName] = c.name
	}
	rot := e.Rotation()
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: cushionID,
		Position: world.Vec32(e.Position()), Yaw: rot.Yaw, HeadYaw: rot.Yaw, BodyYaw: rot.Yaw, EntityMetadata: meta,
		EntityLinks: c.seats.Links(e.UniqueID()),
	}}
}

func (c *Cushion) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	return &packet.MoveActorAbsolute{EntityRuntimeID: e.RuntimeID(), Position: world.Vec32(e.Position()), Rotation: mgl32.Vec3{float32(rot.Pitch), float32(rot.Yaw), float32(rot.HeadYaw)}}
}

// tick keeps its riders and, now and then, checks it still rests on something; in lava it
// burns up.
func (c *Cushion) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || c.gone {
		return
	}
	if c.s.checkVoid(e); c.gone {
		return
	}
	keepSeats(c)
	if b, ok := r.Block(floorPos(e.Position()), 0); ok && c.s.lavaBlock(b) {
		c.Break(r, true)
		return
	}
	if c.check--; c.check > 0 {
		return
	}
	c.check = cushionCheckMin + rand.Int32N(cushionCheckSpread) //nolint:gosec // a delay, not a secret
	if !c.resting(r) {
		c.Break(r, true)
	}
}

// lavaBlock reports whether a block is lava.
func (s *Host) lavaBlock(b uint32) bool {
	return s.reg.Blocks.Properties(b).Material == registry.MaterialLava
}

// box is its collision box at pos.
func cushionBox(pos mgl64.Vec3) (lo, hi mgl64.Vec3) {
	const half = cushionWidth / 2
	return mgl64.Vec3{pos[0] - half, pos[1], pos[2] - half}, mgl64.Vec3{pos[0] + half, pos[1] + cushionHeight, pos[2] + half}
}

// resting is BDS's sub_C087200: something under it within cushionRest has a box its own
// box, moved down that far, touches; and it is not stuck inside solid blocks.
func (c *Cushion) resting(r *world.Region) bool {
	pos := c.entity.Position()
	below := mgl64.Vec3{pos[0], pos[1] - cushionRest, pos[2]}
	at := floorPos(below)
	b, ok := r.Block(at, 0)
	if !ok {
		return true
	}
	lo, hi := cushionBox(below)
	held := false
	base := blockVec(at)
	for _, box := range c.s.reg.Blocks.Properties(b).Collision {
		bLo := base.Add(mgl64.Vec3{float64(box[0]), float64(box[1]), float64(box[2])})
		bHi := base.Add(mgl64.Vec3{float64(box[3]), float64(box[4]), float64(box[5])})
		if overlaps(lo, hi, bLo, bHi) {
			held = true
			break
		}
	}
	if !held {
		return false
	}
	lo, hi = cushionBox(pos)
	first, _ := r.Block(floorPos(lo), 0)
	last, _ := r.Block(floorPos(hi), 0)
	return !c.s.solidBlock(first) || !c.s.solidBlock(last)
}

// away and keeperGone make it a seatKeeper.
func (c *Cushion) away() *[]int64 { return &c.awayIDs }

func (c *Cushion) keeperGone() bool { return c.gone }

// riders, rideEntity, rideMoved and rideTop make it a vehicle.
func (c *Cushion) riders() *ride.Vehicle { return c.seats }

func (c *Cushion) rideEntity() *world.Entity { return c.entity }

func (c *Cushion) rideMoved() mgl64.Vec3 { return mgl64.Vec3{} }

func (c *Cushion) rideTop() float64 { return c.entity.Position()[1] + cushionHeight }

// use is a player using it: sit down, unless someone holds the seat.
func (c *Cushion) use(p *Player) {
	if seatsHeld(c) {
		return
	}
	mount(c, p)
}

// Hurt is BDS's Cushion hurt: a player in adventure mode does nothing; any other hit breaks
// it, with its item unless a creative player hit it. True if it broke.
func (c *Cushion) Hurt(r *world.Region, by *Player) bool {
	if c.gone {
		return false
	}
	if by != nil && by.mode() == GameModeAdventure {
		return false
	}
	c.Break(r, by == nil || !by.creative())
	return true
}

// Break takes it away, leaving its item if drops is set and doentitydrops is on.
func (c *Cushion) Break(r *world.Region, drops bool) {
	if c.gone {
		return
	}
	c.gone = true
	letOffAll(c)
	if drops && c.s.level.DoEntityDrops {
		item := ItemStack{Name: "minecraft:" + dyeColors[c.color&0xf] + cushionItemSuffix, Count: 1}
		if c.name != "" {
			item = item.WithName(c.name)
		}
		c.s.spawnItem(r.Dimension(), item, c.entity.Position(), mgl64.Vec3{}, world.Rotation{}, cushionDropDelay, 0)
	}
	c.entity.Remove()
}

// BlastBox is its box.
func (c *Cushion) BlastBox() (lo, hi mgl64.Vec3, ok bool) {
	if c.gone || c.entity == nil {
		return lo, hi, false
	}
	lo, hi = cushionBox(c.entity.Position())
	return lo, hi, true
}

// BlastHit breaks it, as any hurt does.
func (c *Cushion) BlastHit(float64, DamageSource) {
	if r := c.entity.Region(); r != nil {
		c.Hurt(r, nil)
	}
}

// BlastPush does nothing: a cushion does not move.
func (c *Cushion) BlastPush(mgl64.Vec3) {}

// isCushionItem reports whether the item called name is a cushion.
func isCushionItem(name string) bool { _, ok := cushionColor(name); return ok }

// placeCushion is BDS's CushionItem useOn: only on a top face, at the very height clicked,
// in the middle of the block, turned to the nearest quarter from where the player looks, and
// never on or into another cushion. The use counts once it is tried.
func (p *Player) placeCushion(r *world.Region, clicked cube.Pos, face int32, click mgl64.Vec3, held ItemStack) bool {
	if cube.Face(face) != cube.FaceUp || !p.inReach(clicked) {
		return false
	}
	color, _ := cushionColor(held.Name)
	pos := mgl64.Vec3{float64(clicked[0]) + 0.5, float64(clicked[1]) + click[1], float64(clicked[2]) + 0.5}
	point := mgl64.Vec3{float64(clicked[0]) + click[0], pos[1], float64(clicked[2]) + click[2]}
	lo, hi := cushionBox(pos)
	p.touching = r.AppendEntitiesIn(p.touching[:0], lo[0]-1, lo[2]-1, hi[0]+1, hi[2]+1)
	for _, e := range p.touching {
		o, ok := e.Owner().(*Cushion)
		if !ok || o.gone {
			continue
		}
		oLo, oHi := cushionBox(e.Position())
		under := point[0] >= oLo[0] && point[0] <= oHi[0] && point[2] >= oLo[2] && point[2] <= oHi[2] &&
			point[1]+cushionClick >= oLo[1] && point[1]-cushionClick <= oHi[1]
		if under || overlaps(lo, hi, oLo, oHi) {
			return false
		}
	}
	yaw := cushionYaw(float64(p.entity.Rotation().Yaw))
	c := p.s.SpawnCushion(r.Dimension(), pos, yaw, color)
	if name := customName(held); name != "" {
		c.name = name
	}
	playSound(r, clicked, soundEvent{name: packet.SoundEventSpawn, pos: pos, extra: -1}, nil)
	if !p.creative() {
		p.useHeld()
	}
	return true
}

// cushionYaw is the player's yaw turned to the nearest quarter, as BDS turns a placed cushion.
func cushionYaw(yaw float64) float64 {
	y := float64(math.Mod(float64(yaw), 360))
	if y < 0 {
		y += 360
	}
	return float64(math.Floor(float64((y-180+45)/90))) * 90
}

// cushionNear finds a cushion near the player by its runtime ID.
func (p *Player) cushionNear(id uint64) *Cushion {
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
		if c, ok := e.Owner().(*Cushion); ok && !c.gone {
			return c
		}
	}
	return nil
}

// useOnCushion is a use on actor transaction aimed at a cushion. False if it was not.
func (p *Player) useOnCushion(d *protocol.UseItemOnEntityTransactionData) bool {
	c := p.cushionNear(d.TargetEntityRuntimeID)
	if c == nil {
		return false
	}
	if p.entity.Position().Sub(c.entity.Position()).Len() > cartReach {
		return true
	}
	switch d.ActionType {
	case protocol.UseItemOnEntityActionInteract:
		c.use(p)
	case protocol.UseItemOnEntityActionAttack:
		if r := p.entity.Region(); r != nil {
			c.Hurt(r, p)
		}
	}
	return true
}

// saveEntity saves it as BDS does, with whoever sits on it or waits for its seat as links.
func (c *Cushion) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if c.gone {
		return chunk.Entity{}, false
	}
	data := maps.Clone(c.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos, rot := e.Position(), e.Rotation()
	data["identifier"] = cushionID
	data["Pos"] = []float32{float32(pos[0]), float32(pos[1]), float32(pos[2])}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	data[cushionVariantTag] = int32(c.color)
	if c.name != "" {
		data["CustomName"] = c.name
	}
	saveLinks(data, c)
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadCushion brings back a saved cushion; its riders wait for their seat until they join.
func (s *Host) loadCushion(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	color, _ := saved.Data[cushionVariantTag].(int32)
	c := s.newCushion(uint8(color & 0xf)) //nolint:gosec // masked to a colour
	c.saved = maps.Clone(saved.Data)
	c.name, _ = saved.Data["CustomName"].(string)
	c.awayIDs = loadLinks(saved.Data)
	var rot world.Rotation
	if v, isList := floats(saved.Data["Rotation"]); isList && len(v) == 2 {
		rot = world.Rotation{Yaw: v[0], Pitch: v[1], HeadYaw: v[0]}
	}
	c.entity = r.AddLoadedEntity(at, c.config(saved.ID, s.runtimeIDs.Add(1), mgl64.Vec3{float64(pos[0]), float64(pos[1]), float64(pos[2])}, rot))
	return true
}
