package player

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
)

// Fishing as BDS 1.26.60 does it: FishingRodItem::use (0xC990440) casts and reels in, and the
// FishingHook actor (vtable 0xFAFAA08) runs its tick (0xC08F3B0: floating, hooking entities,
// the fish coming and biting), its retrieve (0xC08DBF0: what reeling in does) and its pull
// (0xC08DAA0, [PullVelocity]). 1.21.3 was read for names only.
const (
	nameFishingRod  = "minecraft:fishing_rod"
	nameFishingHook = "minecraft:fishing_hook"

	// hookHalf and hookHeight are its box: reloadHardcoded (0xC08D360) sets 0.5 by 0.5,
	// over the definition's 0.15.
	hookHalf   = 0.25
	hookHeight = 0.5
	// hookCastSpeed is how long the look direction is before the angler's own speed is
	// added; hookCastPower what the direction, made one long, is multiplied by after its
	// gaussian spread of hookSpread (constructor 0xC08D150, reloadHardcoded).
	hookCastSpeed = 0.4
	hookCastPower = 1.5
	hookSpread    = 0.0075
	// hookWaterSlices is how many slices of its box the water share is counted in.
	hookWaterSlices = 5

	// hookDrag is what is left of its speed each tick, hookDragTouching when it is on the
	// ground or against a wall; hookDragWater takes a further share in water.
	hookDrag         = 0.92
	hookDragTouching = 0.5
	hookDragWater    = 0.9
	// hookSink is what each tick adds downward out of water and upward fully in it.
	hookSink = 0.04
	// hookBobStep, hookBobSize: the bobbing wave on the surface.
	hookBobStep = 0.1
	hookBobSize = 0.035
	// hookFullWater is the float height when the block above is water too.
	hookFullWater = 1.01
	// hookFloatPull and hookFloatMost: how fast it rises to its float height, at most
	// hookFloatMost above its fall slowed by hookFloatSlow.
	hookFloatPull = 0.15
	hookFloatSlow = 0.8
	hookFloatMost = 0.05

	// hookOwnerReach2 is the squared distance from its angler at which it is gone.
	hookOwnerReach2 = 1024
	// hookOwnerAge is how many ticks it must fly before it can hook its own angler.
	hookOwnerAge = 5
	// hookTargetGrow is how much a target's box grows for the hit check, hookScanGrow how
	// far around its path it looks.
	hookTargetGrow = 0.3
	hookScanGrow   = 1
	// hookTargetHeight is how high up its target's body it hangs.
	hookTargetHeight = 0.8

	// The fish. Lure time: hookLureBase + nextInt(hookLureRand) - hookLurePerLevel per
	// level of Lure, a quarter with water animals within hookLureCrowd, at least one, in
	// seconds. Then the fish comes for nextInt(hookComeRand) + hookComeBase ticks, and bites
	// for nextInt(hookBiteRand) + hookBiteBase.
	hookLureBase     = 5
	hookLureRand     = 25
	hookLurePerLevel = 5
	hookLureCrowd    = 8
	hookLureCrowdCut = 0.25
	hookComeBase     = 20
	hookComeRand     = 60
	hookBiteBase     = 10
	hookBiteRand     = 20
	// hookAngleStep is the gaussian turn of the coming fish each tick; hookFishStep how far
	// off each tick left puts it.
	hookAngleStep = 0.001953125
	hookFishStep  = 0.1
	// hookBiteDip is how far the hook drops when a fish bites; hookNibble scales the
	// random dip while it bites.
	hookBiteDip = 0.5
	hookNibble  = 0.5
	// Lure speed: hookRainChance and hookOceanChance to fish twice as fast,
	// hookSkyChance to fish slower where the sky is not seen.
	hookOceanChance = 0.25
	hookRainChance  = 0.25
	hookSkyChance   = 0.5
	// Tease bubbles: from hookTeaseBase more likely as the lure time runs out.
	hookTeaseBase = 0.15
	hookTeaseFar  = 25
	hookTeaseMore = 35

	// hookPullScale is what retrieving pulls with, toward hookPullAbove over the angler's
	// head.
	hookPullScale = 0.1
	hookPullAbove = 1
	// What reeling in costs the rod: an entity, a catch, the hook in the ground.
	hookWearEntity = 3
	hookWearCatch  = 1
	hookWearGround = 2
	// hookCatchXP is the most experience a catch gives, at least one; hookXPAhead how far
	// in front of the angler's body the orbs appear.
	hookCatchXP     = 5
	hookXPAhead     = 0.5
	hookCatchDelay  = mobDropDelay
	radianToDegrees = 57.295776

	// The enchantments a rod reads, by BDS's IDs.
	enchantLuckOfTheSea = int16(23)
	enchantLure         = int16(24)

	// The biome tags fishing reads.
	biomeTagOcean  = "ocean"
	biomeTagJungle = "jungle"
	// hookJungleGroup is the definition's group for a hook cast in a jungle.
	hookJungleGroup = "loot_jungle"

	hookFlags = 1 << protocol.EntityDataFlagHasCollision
)

// waterAnimals are BDS's WaterAnimal actor types (ActorType 0x2300) a crowd of which
// shortens the lure time.
var waterAnimals = []string{
	"minecraft:cod", "minecraft:salmon", "minecraft:pufferfish", "minecraft:tropicalfish",
	"minecraft:squid", "minecraft:glow_squid", "minecraft:dolphin",
}

// FishingHook is a cast fishing hook: BDS's FishingHook actor. It belongs to the player who
// cast it, floats where it lands in water, hooks entities it hits, and catches what the
// fishing loot table gives when reeled in while a fish bites. Runs: owner of its entity.
type FishingHook struct {
	s      *Host
	entity *world.Entity
	owner  *Player
	// target is what it hooked, nil for nothing.
	target Living
	vel    mgl32.Vec3
	rot    world.Rotation
	moves  actorMoves
	// table is its loot table, picked by the biome it was cast in.
	table string

	age                         int32
	bob, fishAngle              float32
	untilHooked, untilLured     int32
	biting, lureSpeed           int32
	inGround, onGround, against bool
	gravity, waiting, gone      bool
	found                       []*world.Entity
}

// Entity returns its entity.
func (h *FishingHook) Entity() *world.Entity { return h.entity }

// Owner returns the player who cast it.
func (h *FishingHook) Owner() *Player { return h.owner }

// Target returns what it hooked: a *Player or *Mob, or nil.
func (h *FishingHook) Target() Living { return h.target }

// Biting reports a fish on the hook: reeling in now catches something.
func (h *FishingHook) Biting() bool { return h.biting > 0 }

// FishingHook returns the hook the player has out, or nil. Runs: the player's region.
func (p *Player) FishingHook() *FishingHook { return p.fishHook }

// FishCatchEvent is a player reeling in while a fish bites: Items are thrown to the player,
// Experience appears in orbs in front of it, once per item as BDS does. Change them, or
// cancel for no catch. Runs: owner of the region.
type FishCatchEvent struct {
	Cancellable
	Player     *Player
	Hook       *FishingHook
	Items      []ItemStack
	Experience []int32
}

// OnFishCatch adds a listener for catches.
func OnFishCatch(s *Setup, fn func(e *FishCatchEvent), p Priority) error {
	return addListener(s, &s.h.game.fishCatchEv, fn, p)
}

// useFishingRod is FishingRodItem::use: with a hook out it reels in and the rod wears by
// what that did; without one it casts a hook. Runs: the player's region.
func (p *Player) useFishingRod() bool {
	if p.entity == nil {
		return false
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	now := r.CurrentTick()
	// A click on a block and a click on the air may both arrive for one use.
	if now == p.thrownAt {
		return true
	}
	p.thrownAt = now
	if h := p.fishHook; h != nil {
		p.fishHook = nil
		if wear := h.retrieve(r); wear > 0 && !p.creative() {
			p.hurtHeld(wear)
		}
		return true
	}
	p.fishHook = p.s.castHook(r, p)
	eyes := p.entity.Position()
	playSound(r, p.blockPos(), soundEvent{name: packet.SoundEventThrow, pos: eyes, extra: -1, entity: "minecraft:player"}, nil)
	return true
}

// castHook adds a hook in front of p as reloadHardcoded aims it: the look direction times
// hookCastSpeed plus the angler's speed, made one long, spread by a gaussian on each axis,
// times hookCastPower. It starts at the angler's eyes; BDS's start is the player's drop
// attach point, which is not read yet.
func (s *Host) castHook(r *world.Region, p *Player) *FishingHook {
	eyes, rot := p.entity.Position(), p.entity.Rotation()
	dir := lookDirection(rot).Mul(hookCastSpeed).Add(p.move.velocity)
	if dir.Len() > 0 {
		dir = dir.Normalize()
	}
	for n := range 3 {
		dir[n] += float32(rand.NormFloat64()) * hookSpread //nolint:gosec // an aim, not a secret
	}
	h := &FishingHook{s: s, owner: p, vel: dir.Mul(hookCastPower), gravity: true, waiting: true, table: s.hookTable(r, eyes)}
	h.rot = flightRotation(h.vel)
	h.entity = r.Dimension().AddEntity(h.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), eyes, h.rot)) //nolint:gosec // an ID, not a secret
	return h
}

// hookLoot is the minecraft:loot part of the hook's definition this reads.
type hookLoot struct {
	Entity struct {
		Components map[string]json.RawMessage            `json:"components"`
		Groups     map[string]map[string]json.RawMessage `json:"component_groups"`
	} `json:"minecraft:entity"`
}

// hookTable is the hook's loot table from its definition: its own, or its jungle group's
// when cast in a jungle biome, as its entity_spawned event adds.
func (s *Host) hookTable(r *world.Region, at mgl32.Vec3) string {
	t, ok := s.reg.EntityTypes.ByName(nameFishingHook)
	if !ok {
		return ""
	}
	var d hookLoot
	if json.Unmarshal(t.Definition, &d) != nil {
		return ""
	}
	raw := d.Entity.Components["minecraft:loot"]
	if s.biomeHasTag(r, floorPos(at), biomeTagJungle) {
		if g, has := d.Entity.Groups[hookJungleGroup]["minecraft:loot"]; has {
			raw = g
		}
	}
	var c struct {
		Table string `json:"table"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	return c.Table
}

// biomeHasTag reports whether the biome at pos has tag.
func (s *Host) biomeHasTag(r *world.Region, pos cube.Pos, tag string) bool {
	id, ok := r.Biome(pos)
	if !ok {
		return false
	}
	b, ok := s.reg.Biomes.ByID(id)
	return ok && slices.Contains(b.Tags, tag)
}

func (h *FishingHook) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	spec, _ := h.s.projectileSpec(nameFishingHook, "")
	dropped, far := spec.MoveDroppedTicks, spec.MoveFarDistance
	if dropped == 0 {
		dropped, far = projectileMoveDropped, projectileMoveFar
	}
	h.moves.start(pos, rot, dropped, far)
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: h, Spawn: h.spawnPackets, Move: h.movePacket, Input: h.tick, Save: h.saveEntity,
	}
}

// saveEntity saves nothing: the hook's definition is minecraft:transient.
func (h *FishingHook) saveEntity(*world.Entity) (chunk.Entity, bool) { return chunk.Entity{}, false }

func (h *FishingHook) flags() int64 {
	f := int64(hookFlags)
	if h.gravity {
		f |= 1 << protocol.EntityDataFlagHasGravity
	}
	return f
}

func (h *FishingHook) targetID() int64 {
	if h.target == nil {
		return 0
	}
	if e := livingEntity(h.target); e != nil {
		return e.UniqueID()
	}
	return 0
}

// spawnPackets show it to a player, with its angler as owner so the line is drawn. Not yet
// compared with what BDS sends.
func (h *FishingHook) spawnPackets(e *world.Entity) []packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags: h.flags(), protocol.EntityDataKeyWidth: float32(hookHalf * 2),
		protocol.EntityDataKeyHeight: float32(hookHeight), protocol.EntityDataKeyScale: float32(1),
		protocol.EntityDataKeyTarget: h.targetID(),
	}
	if h.owner != nil && h.owner.entity != nil {
		meta[protocol.EntityDataKeyOwner] = h.owner.entity.UniqueID()
	}
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: nameFishingHook,
		Position: e.Position(), Velocity: h.vel, Pitch: h.rot.Pitch, Yaw: h.rot.Yaw, HeadYaw: h.rot.HeadYaw,
		EntityMetadata: meta,
	}}
}

func (h *FishingHook) movePacket(e *world.Entity) packet.Packet {
	h.moves.update(e, h.vel)
	return nil
}

// livingEntity is the entity of a player or mob.
func livingEntity(l Living) *world.Entity {
	switch t := l.(type) {
	case *Player:
		return t.entity
	case *Mob:
		return t.entity
	}
	return nil
}

// remove takes it out of the world and from its angler.
func (h *FishingHook) remove() {
	if h.gone {
		return
	}
	h.gone = true
	if h.owner != nil && h.owner.fishHook == h {
		h.owner.fishHook = nil
	}
	if h.entity != nil && h.entity.Region() != nil {
		h.entity.Remove()
	}
}

// tick is the hook's tick (0xC08F3B0) in its order: floating and sinking, then hanging
// on its target, or the hit check and the move with the fish in water, then the angler
// checks, and last its place on its target.
func (h *FishingHook) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || h.gone {
		return
	}
	if h.s.checkVoid(e); e.Region() == nil {
		h.remove()
		return
	}
	// The players who see a new one are told after entities tick, as for projectiles.
	if h.waiting {
		h.waiting = false
		return
	}
	h.age++
	pos := e.Position()
	if h.float(r, pos) {
		return
	}
	if h.target != nil {
		te := livingEntity(h.target)
		if te == nil || te.Region() != r || h.target.Dead() {
			h.remove()
			return
		}
		pos, h.rot = te.Position(), te.Rotation()
		if m, ok := h.target.(Moving); ok {
			h.vel = m.Velocity()
		}
	} else {
		h.setGravity(true)
		moved := false
		if !h.inGround {
			moved = h.hitCheck(r, pos)
		}
		if !moved && h.target == nil {
			pos = h.move(r, pos)
			if h.waterShare(r, pos) > 0 {
				h.fish(r, pos)
			}
		}
	}
	if !h.ownerHolds(r, pos) {
		h.remove()
		return
	}
	if h.target != nil && !h.target.Dead() {
		if lo, hi, ok := h.target.BlastBox(); ok {
			te := livingEntity(h.target).Position()
			pos = mgl32.Vec3{te[0], lo[1] + (hi[1]-lo[1])*hookTargetHeight, te[2]}
		}
	}
	e.Apply(world.Movement{Pos: pos, Rot: h.rot, OnGround: h.onGround, Tick: e.Tick()})
}

// float is the start of the tick (BDS's _updateGravity inlined): the drag, lava taking the
// hook back, and in water the rise to the surface with its bobbing. True if it is gone.
func (h *FishingHook) float(r *world.Region, pos mgl32.Vec3) bool {
	drag := float32(hookDrag)
	if h.onGround || h.against {
		drag = hookDragTouching
	}
	h.bob += hookBobStep
	at := floorPos(pos)
	here, _ := liquidAt(h.s.reg, r, at)
	if here == registry.MaterialLava {
		h.retrieve(r)
		return true
	}
	var level, dip float32
	if here == registry.MaterialWater {
		if above, _ := liquidAt(h.s.reg, r, at.Add(cube.Pos{0, 1, 0})); above == registry.MaterialWater {
			level = hookFullWater
		} else {
			level = float32(at[1]) - pos[1] + 1
			dip = (float32(math.Sin(float64(h.bob))) + 1) * hookBobSize
		}
	}
	vy := (h.waterShare(r, pos)*2-1)*hookSink + h.vel[1]
	if level != 0 {
		drag *= hookDragWater
		vy = min((level-dip)*hookFloatPull, vy*hookFloatSlow+hookFloatMost)
	}
	h.vel = mgl32.Vec3{h.vel[0] * drag, vy * drag, h.vel[2] * drag}
	return false
}

// box is its box at pos, its feet.
func hookBox(pos mgl32.Vec3) (lo, hi mgl32.Vec3) {
	return mgl32.Vec3{pos[0] - hookHalf, pos[1], pos[2] - hookHalf}, mgl32.Vec3{pos[0] + hookHalf, pos[1] + hookHeight, pos[2] + hookHalf}
}

// waterShare is _getWaterPercentage (0xC08F210): of hookWaterSlices slices of its box from
// the bottom, the share that touch water.
func (h *FishingHook) waterShare(r *world.Region, pos mgl32.Vec3) float32 {
	lo, hi := hookBox(pos)
	var share float32
	step := (hi[1] - lo[1]) / hookWaterSlices
	for n := range hookWaterSlices {
		sLo, sHi := lo, hi
		sLo[1], sHi[1] = lo[1]+float32(n)*step, lo[1]+float32(n+1)*step
		if h.s.boxTouchesWater(r, sLo, sHi) {
			share += 1.0 / hookWaterSlices
		}
	}
	return share
}

// boxTouchesWater reports water in any block the box lo..hi overlaps.
func (s *Host) boxTouchesWater(r *world.Region, lo, hi mgl32.Vec3) bool {
	for x := itemFloor(lo[0]); x <= itemFloor(hi[0]); x++ {
		for y := itemFloor(lo[1]); y <= itemFloor(hi[1]); y++ {
			for z := itemFloor(lo[2]); z <= itemFloor(hi[2]); z++ {
				if m, _ := liquidAt(s.reg, r, cube.Pos{x, y, z}); m == registry.MaterialWater {
					return true
				}
			}
		}
	}
	return false
}

// setGravity shows the gravity flag: on flying free, off hanging on an entity.
func (h *FishingHook) setGravity(on bool) {
	if h.gravity == on {
		return
	}
	h.gravity = on
	h.entity.Send(actorData(h.entity.RuntimeID(), protocol.EntityMetadata{protocol.EntityDataKeyFlags: h.flags()}))
}

// hitCheck is the hook's look along this tick's flight: a block first, then the nearest
// player or mob whose box, grown by hookTargetGrow, the flight enters before that block.
// A hit entity becomes its target; a block leaves it in the ground. Either way it does not
// move this tick. BDS also hurts the hooked entity for 0 through its hurt, which is not
// done here, and skips the angler's vehicle and riders and a list of actor types, which
// are not read yet. True if it hit something.
func (h *FishingHook) hitCheck(r *world.Region, from mgl32.Vec3) bool {
	to := from.Add(h.vel)
	limit := 2.0
	hitBlock := false
	if at, _, _, _, ok := h.s.clipBlocks(r, from, to); ok {
		limit, hitBlock = at, true
	}
	lo, hi := minVec(from, to), maxVec(from, to)
	h.found = r.AppendEntitiesIn(h.found[:0], lo[0]-hookScanGrow, lo[2]-hookScanGrow, hi[0]+hookScanGrow, hi[2]+hookScanGrow)
	grow := mgl32.Vec3{hookTargetGrow, hookTargetGrow, hookTargetGrow}
	var best Living
	for _, o := range h.found {
		var l Living
		switch t := o.Owner().(type) {
		case *Player:
			if t.dead() || t == h.owner && h.age < hookOwnerAge {
				continue
			}
			l = t
		case *Mob:
			if t.Dead() {
				continue
			}
			l = t
		default:
			continue
		}
		bLo, bHi, ok := l.BlastBox()
		if !ok {
			continue
		}
		bLo, bHi = bLo.Sub(grow), bHi.Add(grow)
		at := 0.0
		if !insideBox(from, bLo, bHi) {
			var enters bool
			if at, enters = segmentEntersBox(from, to, bLo, bHi); !enters {
				continue
			}
		}
		if at < limit {
			limit, best = at, l
		}
	}
	switch {
	case best != nil:
		h.target = best
		h.setGravity(false)
		h.entity.Send(actorData(h.entity.RuntimeID(), protocol.EntityMetadata{protocol.EntityDataKeyTarget: h.targetID()}))
		return true
	case hitBlock:
		h.inGround = true
		return true
	}
	return false
}

// move is Actor::move by its speed through the blocks' boxes, y then x then z; a blocked
// axis loses its speed.
func (h *FishingHook) move(r *world.Region, pos mgl32.Vec3) mgl32.Vec3 {
	h.onGround, h.against = false, false
	for _, axis := range [...]int{1, 0, 2} {
		lo, hi := hookBox(pos)
		d := h.s.clipSweep(r, lo, hi, axis, h.vel[axis])
		if d != h.vel[axis] {
			if axis == 1 {
				h.onGround = h.vel[1] < 0
			} else {
				h.against = true
			}
			h.vel[axis] = 0
		}
		pos[axis] += d
	}
	return pos
}

// ownerHolds is the check that ends the tick: the angler must be alive in this region,
// holding a fishing rod, within hookOwnerReach2 of the hook.
func (h *FishingHook) ownerHolds(r *world.Region, pos mgl32.Vec3) bool {
	p := h.owner
	if p == nil || p.entity == nil || p.dead() || p.entity.Region() != r || p.fishHook != h {
		return false
	}
	if p.inventory.Held().Name != nameFishingRod {
		return false
	}
	at := p.entity.Position()
	d := mgl32.Vec3{pos[0] - at[0], pos[1] - (at[1] - eyeHeight), pos[2] - at[2]}
	return d.Dot(d) <= hookOwnerReach2
}

// lureSpeedAt is how fast the fish come this tick: one, two by chance in an ocean or in
// rain, one less by chance where the sky is not seen.
func (h *FishingHook) lureSpeedAt(r *world.Region, pos mgl32.Vec3) int32 {
	at := floorPos(pos)
	speed := int32(1)
	if rand.Float32() < hookOceanChance && h.s.biomeHasTag(r, at, biomeTagOcean) { //nolint:gosec // gameplay randomness
		speed = 2
	}
	up := at.Add(cube.Pos{0, 1, 0})
	if rand.Float32() < hookRainChance && h.s.inRain(r, mgl32.Vec3{float32(up[0]), float32(up[1]), float32(up[2])}) { //nolint:gosec // gameplay randomness
		speed = 2
	}
	if rand.Float32() < hookSkyChance { //nolint:gosec // gameplay randomness
		rng := r.Dimension().Range()
		if up[1] < rng.Max()+1 {
			if up[1] < rng.Min() {
				speed--
			} else if height, ok := r.Height(up[0], up[2]); ok && up[1] < height {
				speed--
			}
		}
	}
	return speed
}

// fish is BDS's fish in water (_updateServer inlined): a bite counting down, the fish
// coming with its trail of bubbles, the lure time with its teasing bubbles, or a new lure
// time.
func (h *FishingHook) fish(r *world.Region, pos mgl32.Vec3) {
	if h.biting > 0 {
		h.biting--
		if h.biting == 0 {
			h.untilHooked, h.untilLured = 0, 0
		}
		h.vel[1] -= rand.Float32() * rand.Float32() * rand.Float32() * hookNibble //nolint:gosec // gameplay randomness
		return
	}
	h.lureSpeed = h.lureSpeedAt(r, pos)
	rid := h.entity.RuntimeID()
	switch {
	case h.untilHooked > 0:
		h.untilHooked -= h.lureSpeed
		if h.untilHooked > 0 {
			h.fishAngle += float32(rand.NormFloat64()) * hookAngleStep //nolint:gosec // gameplay randomness
			// BDS turns the angle to degrees, then takes the sine of that as radians.
			deg := h.fishAngle * radianToDegrees
			fx := float32(math.Sin(float64(deg))) * float32(h.untilHooked) * hookFishStep
			fz := float32(math.Cos(float64(deg))) * float32(h.untilHooked) * hookFishStep
			h.entity.Send(actorData(rid, protocol.EntityMetadata{
				protocol.EntityDataKeyFishX: fx, protocol.EntityDataKeyFishZ: fz, protocol.EntityDataKeyFishAngle: deg,
			}))
			at := cube.Pos{itemFloor(pos[0] + fx), itemFloor(pos[1]), itemFloor(pos[2] + fz)}
			if m, _ := liquidAt(h.s.reg, r, at); m == registry.MaterialWater {
				h.entity.Send(actorEvent(rid, packet.ActorEventFishhookFishPosition))
			}
			return
		}
		h.biting = rand.Int32N(hookBiteRand) + hookBiteBase //nolint:gosec // gameplay randomness
		h.entity.Send(actorEvent(rid, packet.ActorEventFishhookHookTime))
		// The event's own handling on the server: the hook dips and splashes.
		h.vel[1] -= hookBiteDip
		playSound(r, floorPos(pos), soundEvent{name: packet.SoundEventSplash, pos: pos, extra: -1, entity: nameFishingHook}, nil)
	case h.untilLured > 0:
		h.untilLured -= h.lureSpeed
		chance := float32(hookTeaseBase)
		switch l := h.untilLured; {
		case l < 20:
			chance += float32(20-l) * 0.05
		case l < 40:
			chance += float32(40-l) * 0.02
		case l < 60:
			chance += float32(60-l) * 0.01
		}
		if chance > rand.Float32() { //nolint:gosec // gameplay randomness
			a := float64(rand.Float32()*360) * radianToDegrees  //nolint:gosec // gameplay randomness
			dist := rand.Float32()*hookTeaseMore + hookTeaseFar //nolint:gosec // gameplay randomness
			fx := float32(math.Sin(a))*dist*hookFishStep + pos[0]
			fz := float32(math.Cos(a))*dist*hookFishStep + pos[2]
			h.entity.Send(actorData(rid, protocol.EntityMetadata{protocol.EntityDataKeyFishX: fx, protocol.EntityDataKeyFishZ: fz}))
			h.entity.Send(actorEvent(rid, packet.ActorEventFishhookTease))
		}
		if h.untilLured <= 0 {
			h.fishAngle = rand.Float32() * 360                       //nolint:gosec // gameplay randomness
			h.untilHooked = rand.Int32N(hookComeRand) + hookComeBase //nolint:gosec // gameplay randomness
		}
	default:
		lure := int32(0)
		if h.owner != nil {
			lure = heldEnchant(h.owner.inventory.Held(), enchantLure)
		}
		h.untilLured = hookLureBase - hookLurePerLevel*lure + rand.Int32N(hookLureRand) //nolint:gosec // gameplay randomness
		if h.waterAnimalsNear(r, pos) {
			h.untilLured = int32(math.Floor(float64(h.untilLured) * hookLureCrowdCut))
		}
		h.untilLured = 20 * max(h.untilLured, 1)
	}
}

// waterAnimalsNear reports a water animal within hookLureCrowd of the hook's box.
func (h *FishingHook) waterAnimalsNear(r *world.Region, pos mgl32.Vec3) bool {
	lo, hi := hookBox(pos)
	grow := mgl32.Vec3{hookLureCrowd, hookLureCrowd, hookLureCrowd}
	lo, hi = lo.Sub(grow), hi.Add(grow)
	h.found = r.AppendEntitiesIn(h.found[:0], lo[0], lo[2], hi[0], hi[2])
	for _, o := range h.found {
		m, ok := o.Owner().(*Mob)
		if !ok || m.Dead() || !slices.Contains(waterAnimals, m.Type()) {
			continue
		}
		at := o.Position()
		if at[1] >= lo[1] && at[1] <= hi[1] {
			return true
		}
	}
	return false
}

// pullPoint is where reeling in pulls toward: hookPullAbove over the angler's head. BDS uses
// the head attach point, taken as the eyes here.
func (h *FishingHook) pullPoint() mgl32.Vec3 {
	return h.owner.entity.Position().Add(mgl32.Vec3{0, hookPullAbove, 0})
}

// retrieve is FishingHook::retrieve (0xC08DBF0): a hooked entity is pulled to the angler, a
// biting fish is caught, and the hook goes. It returns what the rod wears by.
func (h *FishingHook) retrieve(r *world.Region) int32 {
	var wear int32
	owned := h.owner != nil && h.owner.entity != nil
	switch {
	case h.target != nil && !h.target.Dead() && owned:
		if m, ok := h.target.(Moving); ok {
			m.Pull(h.pullPoint(), hookPullScale)
		}
		wear = hookWearEntity
	case h.biting > 0 && owned:
		if h.catch(r) {
			wear = hookWearCatch
		}
	}
	h.biting = 0
	if h.inGround {
		wear = hookWearGround
	}
	h.remove()
	return wear
}

// catch rolls the hook's loot table with the angler's luck (Luck of the Sea and the luck
// attribute) and throws each item to the angler, with 1 to hookCatchXP experience for each
// in front of it. False if the hook has no table.
func (h *FishingHook) catch(r *world.Region) bool {
	s, p := h.s, h.owner
	if h.table == "" || s.entityLoot == nil || !s.entityLoot.Has(h.table) {
		return false
	}
	luck := float32(heldEnchant(p.inventory.Held(), enchantLuckOfTheSea)) + p.attributes.get(attrLuck)
	c := &loot.EntityContext{Random: lootRandom{}, Difficulty: s.level.Difficulty, Luck: luck}
	ev := FishCatchEvent{Player: p, Hook: h}
	for _, d := range s.entityLoot.Roll(h.table, c, nil) {
		for _, it := range s.lootStacks(d) {
			ev.Items = append(ev.Items, it)
			ev.Experience = append(ev.Experience, rand.Int32N(hookCatchXP)+1) //nolint:gosec // gameplay randomness
		}
	}
	if g := s.game; g.fishCatchEv.Has() {
		g.fishCatchEv.fire(s.log, &ev)
		if ev.Cancelled() {
			return true
		}
	}
	at := h.entity.Position()
	to := h.pullPoint()
	lo, hi, _ := p.BlastBox()
	orbs := lo.Add(hi).Mul(0.5).Add(lookDirection(p.entity.Rotation()).Mul(hookXPAhead))
	for i, it := range ev.Items {
		s.spawnItem(r.Dimension(), s.freshStack(it), at, PullVelocity(at, to, hookPullScale), world.Rotation{}, hookCatchDelay, 0)
		if i < len(ev.Experience) {
			s.SpawnExperience(r, orbs, ev.Experience[i])
		}
	}
	return true
}
