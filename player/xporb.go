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

// Experience orbs and levels as BDS 1.26.60 has them.
const (
	xpOrbIdentifier = "minecraft:xp_orb"
	// xpOrbValueKey is the saved field holding an orb's value.
	xpOrbValueKey = "experience value"
	xpOrbLifetime = 6000
	// xpOrbMergeEvery and xpOrbMergeTick say on which ticks an orb joins others
	// in the block's space above and beside it.
	xpOrbMergeEvery = 20
	xpOrbMergeTick  = 1
	// xpOrbCrowded is how many orbs in that space make new experience go into
	// them instead of a new orb.
	xpOrbCrowded = 13
	// xpOrbSplitAbove is the amount past which experience makes xpOrbSplitParts
	// equal orbs and one of what is left, instead of the size table.
	xpOrbSplitAbove = 12000
	xpOrbSplitParts = 12
	xpOrbSplitBy    = 13
	// xpOrbSeek2 is the squared distance within which an orb follows a player, and
	// xpOrbSeekRetry how often, in ticks, it may look for a closer one.
	xpOrbSeek2     = 64
	xpOrbSeekRetry = 100
	xpOrbSeekGrace = 20
	// An orb's pull: the distance to the player scaled by xpOrbSeekScale, touching
	// at xpOrbTouch, pulled at xpOrbPull times (1-distance) squared.
	xpOrbSeekScale = 0.125
	xpOrbTouch     = 0.225
	xpOrbPull      = 0.1
	// xpOrbTakeOneIn is the chance per touching tick of a player taking the orb.
	xpOrbTakeOneIn = 6
	xpOrbGravity   = 0.04
	xpOrbDrag      = 0.98
	// xpOrbBounce turns the speed downward into upward on the ground.
	xpOrbBounce = -0.5
	// Lava throws an orb up and to a side.
	xpOrbLavaUp   = 0.2
	xpOrbLavaSide = 0.2
	// levelEventXPPickup is the LevelEvent of taking an orb.
	levelEventXPPickup = 1051
	// A level sound plays every levelSoundEvery levels, at most once in
	// levelSoundGap ticks, and is at its loudest from levelSoundLoudest.
	levelSoundEvery   = 5
	levelSoundGap     = 100
	levelSoundLoudest = 31
	levelSoundMax     = 12582911
	levelSoundScale   = 0.75 * 16777215 / 30
	attrLevel         = 15
	attrExperience    = 16
)

// xpOrbSizes are the values experience splits into, largest first: an amount above
// a size's limit makes an orb of that size.
var xpOrbSizes = [...]struct{ above, value int32 }{
	{2476, 2477}, {1236, 1237}, {616, 617}, {306, 307}, {148, 149}, {72, 73}, {36, 37}, {16, 17}, {6, 7}, {2, 3},
}

// xpOrbSize is the value of the next orb of an amount.
func xpOrbSize(amount int32) int32 {
	for _, s := range xpOrbSizes {
		if amount > s.above {
			return s.value
		}
	}
	return 1
}

// XPOrb is an experience orb. Runs: owner of its entity.
type XPOrb struct {
	s      *Host
	entity *world.Entity
	// body moves the orb with the item entities' collision and holds its speed.
	body       ItemActor
	value, age int32
	target     *Player
	lastSeek   int32
	taken      bool
	saved      map[string]any
	// health is what explosions take from.
	health int32
}

// Value returns the experience the orb gives.
func (o *XPOrb) Value() int32 { return o.value }

// SetValue changes the experience the orb gives.
func (o *XPOrb) SetValue(v int32) {
	o.value = v
	if o.entity != nil {
		o.entity.Send(&packet.SetActorData{EntityRuntimeID: o.entity.RuntimeID(), EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyValue: v}})
	}
}

// Entity returns the orb's entity.
func (o *XPOrb) Entity() *world.Entity { return o.entity }

// SpawnExperience puts experience in the world at pos as BDS does: split into orbs by
// its size table, or added to the orbs already there if the space is crowded. Runs:
// on the region's tick.
func (s *Host) SpawnExperience(r *world.Region, pos mgl32.Vec3, amount int32) {
	if amount <= 0 {
		return
	}
	if amount > xpOrbSplitAbove {
		part := amount / xpOrbSplitBy
		for range xpOrbSplitParts {
			s.spawnOrbOrMerge(r, pos, part)
		}
		s.spawnOrbOrMerge(r, pos, amount-xpOrbSplitParts*part)
		return
	}
	for amount > 0 {
		v := xpOrbSize(amount)
		s.spawnOrbOrMerge(r, pos, v)
		amount -= v
	}
}

// spawnOrbOrMerge adds one orb, or shares its value out if the space is crowded.
func (s *Host) spawnOrbOrMerge(r *world.Region, pos mgl32.Vec3, value int32) {
	near := s.orbsNear(r, pos, nil)
	if n := int32(len(near)); n >= xpOrbCrowded { //nolint:gosec // a few orbs
		share, rest := value/n, value%n
		for _, o := range near {
			if room := math.MaxInt32 - o.value; room >= share+rest {
				o.SetValue(o.value + share + rest)
				rest = 0
			} else {
				o.SetValue(math.MaxInt32)
			}
		}
		return
	}
	o := &XPOrb{s: s, value: value, health: itemHealth}
	o.body.s = s
	o.entity = r.Dimension().AddEntity(o.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), pos, world.Rotation{})) //nolint:gosec // an entity ID, not a secret
}

// orbsNear returns the orbs in the block-sized space from pos, but not skip.
func (s *Host) orbsNear(r *world.Region, pos mgl32.Vec3, skip *XPOrb) []*XPOrb {
	var out []*XPOrb
	for _, e := range r.AppendEntitiesIn(nil, pos[0], pos[2], pos[0]+1, pos[2]+1) {
		o, ok := e.Owner().(*XPOrb)
		if !ok || o == skip || o.taken {
			continue
		}
		at := e.Position()
		if at[0] >= pos[0] && at[0] <= pos[0]+1 && at[1] >= pos[1] && at[1] <= pos[1]+1 && at[2] >= pos[2] && at[2] <= pos[2]+1 {
			out = append(out, o)
		}
	}
	return out
}

func (o *XPOrb) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: o, Spawn: o.spawnPackets, Move: o.body.movePacket, Input: o.tick, Save: o.saveEntity,
	}
}

// spawnPackets show the orb to a player. Its value is the one data entry BDS's code
// sets on it; the rest of what BDS sends is not compared yet.
func (o *XPOrb) spawnPackets(e *world.Entity) []packet.Packet {
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: xpOrbIdentifier,
		Position: e.Position(), Velocity: o.body.vel, EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyValue: o.value},
	}}
}

func (o *XPOrb) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if o.taken {
		return chunk.Entity{}, false
	}
	data := maps.Clone(o.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos, rot := e.Position(), e.Rotation()
	vel := o.body.vel
	data["identifier"] = xpOrbIdentifier
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Motion"] = []float32{vel[0], vel[1], vel[2]}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	data["Age"] = int16(min(o.age, math.MaxInt16)) //nolint:gosec // clamped
	data[xpOrbValueKey] = o.value
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadOrb brings a saved orb back.
func (s *Host) loadOrb(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	o := &XPOrb{s: s, saved: saved.Data, health: itemHealth}
	if h, isShort := saved.Data["Health"].(int16); isShort {
		o.health = int32(h)
	}
	o.body.s = s
	o.value, _ = saved.Data[xpOrbValueKey].(int32)
	age, _ := saved.Data["Age"].(int16)
	o.age = int32(age)
	if v, isList := floats(saved.Data["Motion"]); isList && len(v) == 3 {
		o.body.vel = mgl32.Vec3{v[0], v[1], v[2]}
	}
	o.entity = r.AddLoadedEntity(at, o.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}, world.Rotation{}))
	return true
}

func (o *XPOrb) remove(e *world.Entity) {
	o.taken = true
	e.Remove()
}

// tick is BDS's orb tick: age, joining orbs, gravity, lava, moving, drag, and the
// nearest player's pull.
func (o *XPOrb) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || o.taken {
		return
	}
	o.age++
	if o.age >= xpOrbLifetime {
		o.remove(e)
		return
	}
	if o.age%xpOrbMergeEvery == xpOrbMergeTick {
		o.merge(r, e.Position())
	}
	vel := &o.body.vel
	vel[1] -= xpOrbGravity
	if m, _ := liquidAt(o.s.reg, r, floorPos(e.Position())); m == registry.MaterialLava {
		vel[1] = xpOrbLavaUp
		vel[0] = (rand.Float32() - rand.Float32()) * xpOrbLavaSide //nolint:gosec // an orb's path, not a secret
		vel[2] = (rand.Float32() - rand.Float32()) * xpOrbLavaSide //nolint:gosec // an orb's path, not a secret
		r.Broadcast(floorPos(e.Position()), &packet.LevelSoundEvent{SoundType: packet.SoundEventFizz, Position: e.Position()}, nil)
	}
	// BDS gives an orb's position as it is, where it takes half the box off an item's.
	o.body.pushOut(r, e, e.Position(), e.Position())
	pos, hitY := o.body.collide(r, e.Position())
	onGround := hitY && vel[1] < 0
	if hitY {
		vel[1] = 0
	}
	if !e.Apply(world.Movement{Pos: pos, Rot: e.Rotation(), OnGround: onGround, Tick: e.Tick()}) {
		*vel = mgl32.Vec3{}
	}
	drag := float32(xpOrbDrag)
	if onGround {
		if b, ok := r.Block(floorPos(pos).Side(cube.FaceDown), 0); ok && b != o.s.reg.Blocks.Air() {
			drag = o.s.reg.Blocks.Properties(b).Friction * xpOrbDrag
		}
	}
	*vel = mgl32.Vec3{vel[0] * drag, vel[1] * xpOrbDrag, vel[2] * drag}
	if onGround {
		vel[1] *= xpOrbBounce
	}
	o.follow(r, e)
}

// merge takes in the other orbs in the space, keeping the youngest age, as far as
// their values fit.
func (o *XPOrb) merge(r *world.Region, pos mgl32.Vec3) {
	for _, other := range o.s.orbsNear(r, pos, o) {
		if o.value < math.MaxInt32-other.value {
			o.value += other.value
			o.age = min(o.age, other.age)
			other.remove(other.entity)
			o.SetValue(o.value)
		}
	}
}

// follow finds the player to go to and moves toward it, touching it when close.
func (o *XPOrb) follow(r *world.Region, e *world.Entity) {
	pos := e.Position()
	if o.target != nil && (o.target.entity == nil || o.target.entity.Region() != r) {
		o.target = nil
	}
	retry := o.age - o.age%xpOrbSeekRetry - xpOrbSeekGrace
	if o.target == nil || o.lastSeek < retry || o.target.entity.Position().Sub(pos).LenSqr() > xpOrbSeek2 {
		o.target = o.nearestPlayer(r, pos)
		o.lastSeek = o.age
	}
	if o.target == nil {
		return
	}
	d := o.target.entity.Position().Sub(pos).Mul(xpOrbSeekScale)
	dist := d.Len()
	if dist <= xpOrbTouch {
		o.body.vel = d
		if !o.target.dead() {
			o.touch(r, e, o.target)
		}
		return
	}
	if f := 1 - dist; f > 0 {
		o.body.vel = o.body.vel.Add(d.Mul(1 / dist).Mul(f * f * xpOrbPull))
	}
}

// nearestPlayer returns the closest player within reach of the orb, nil if none.
func (o *XPOrb) nearestPlayer(r *world.Region, pos mgl32.Vec3) *Player {
	var best *Player
	bestD := float32(xpOrbSeek2)
	const reach = 8
	for _, e := range r.AppendEntitiesIn(nil, pos[0]-reach, pos[2]-reach, pos[0]+reach, pos[2]+reach) {
		p, ok := e.Owner().(*Player)
		if !ok {
			continue
		}
		if d := e.Position().Sub(pos).LenSqr(); d <= bestD {
			best, bestD = p, d
		}
	}
	return best
}

// touch is BDS's player touch: one chance in xpOrbTakeOneIn each tick, then the
// player takes it. Worn or held items with Mending drink what they can first.
func (o *XPOrb) touch(r *world.Region, e *world.Entity, p *Player) {
	if rand.IntN(xpOrbTakeOneIn) != 0 { //nolint:gosec // an orb's timing, not a secret
		return
	}
	e.Send(&packet.TakeItemActor{ItemEntityRuntimeID: e.RuntimeID(), TakerEntityRuntimeID: p.runtimeID})
	levelEvent(r, floorPos(e.Position()), e.Position(), levelEventXPPickup, 0)
	o.remove(e)
	if rest := p.mend(o.value); rest > 0 {
		p.AddExperience(rest)
	}
}

// ExperienceEvent is a player about to gain experience from any source. Change Amount
// to give more or less; cancel it to give none.
type ExperienceEvent struct {
	Cancellable
	Player *Player
	Amount int32
}

// OnExperience listens to players gaining experience.
func OnExperience(s *Setup, fn func(e *ExperienceEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.xpEv.add(s.owner, p, fn)
	return nil
}

// xpForLevel is the experience BDS asks for the next level.
func xpForLevel(level int32) int32 {
	switch level / 15 {
	case 0:
		return 2*level + 7
	case 1:
		return 5*level - 38
	}
	return 9*level - 158
}

// Level returns the player's level.
func (p *Player) Level() int32 { return int32(p.attributes.get(attrLevel)) }

// ExperienceProgress returns how far the player is to the next level, 0 to 1.
func (p *Player) ExperienceProgress() float32 { return p.attributes.get(attrExperience) }

// SetLevel sets the player's level and progress to the next, shown at once. Runs: the
// player's region.
func (p *Player) SetLevel(level int32, progress float32) {
	p.setAttributes(attrLevel, float32(max(level, 0)), attrExperience, max(min(progress, 1), 0))
}

// AddExperience gives the player experience as BDS does, levelling up as far as it
// goes. Runs: the player's region.
func (p *Player) AddExperience(amount int32) {
	if g := p.s.game; g.xpEv.Has() {
		e := ExperienceEvent{Player: p, Amount: amount}
		g.xpEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return
		}
		amount = e.Amount
	}
	if amount <= 0 {
		return
	}
	level, progress := gainExperience(p.Level(), p.ExperienceProgress(), amount, p.levelSound)
	p.SetLevel(level, progress)
}

// gainExperience is BDS's level sum: what is left over from a level counts at the
// next level's price. reached runs for each level reached.
func gainExperience(level int32, progress float32, amount int32, reached func(level int32)) (int32, float32) {
	next := xpForLevel(level)
	progress += float32(amount) / float32(next)
	for progress >= 1 {
		progress--
		level++
		reached(level)
		after := xpForLevel(level)
		progress = progress * float32(next) / float32(after)
		next = after
	}
	return level, progress
}

// levelSound plays BDS's sound of reaching a level, every few levels.
func (p *Player) levelSound(level int32) {
	r := p.entity.Region()
	tick := int64(p.entity.Tick()) //nolint:gosec // ticks fit
	if r == nil || level%levelSoundEvery != 0 || tick-levelSoundGap <= p.lastLevelSound {
		return
	}
	p.lastLevelSound = tick
	data := int32(levelSoundMax)
	if level < levelSoundLoudest {
		data = int32(float32(level) * levelSoundScale)
	}
	r.Broadcast(p.blockPos(), &packet.LevelSoundEvent{SoundType: packet.SoundEventLevelUp, Position: p.entity.Position(), ExtraData: data}, nil)
}
