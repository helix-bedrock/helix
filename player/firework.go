package player

import (
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// BDS 1.26.60's firework rockets (FireworksRocketActor, FireworksItem).
const (
	nameFireworkRocket = "minecraft:firework_rocket"
	nameFireworksActor = "minecraft:fireworks_rocket"

	// fireworkHalf is half of its 0.25 box.
	fireworkHalf = 0.125
	// Lifetime (0xBD5EFC0): fireworkLifeBase ticks, or fireworkLifePerFlight per Flight
	// level plus fireworkLifeBase with one, plus nextInt(6) + nextInt(7).
	fireworkLifeBase      = 10
	fireworkLifePerFlight = 10
	fireworkLifeRandA     = 6
	fireworkLifeRandB     = 7
	// It starts with a gaussian push of fireworkJitter and fireworkThrust along its
	// direction (0xBD5EB40); flying free its speed grows by fireworkSpeedUp and
	// fireworkThrust along its direction each tick (0x75ECFC0).
	fireworkJitter  = 0.001
	fireworkThrust  = 0.03
	fireworkSpeedUp = 1.05
	// Its explosion (0xBD5F2E0) hurts fireworkDamageBase plus fireworkDamagePerStar per
	// explosion in it: in full the one it is attached to, and those within fireworkReach
	// it sees by sqrt((reach - distance) / reach) of that.
	fireworkDamageBase    = 5
	fireworkDamagePerStar = 2
	fireworkReach         = 5

	// The synched data BDS gives a rocket (0xBD5ED60): its item's tag, its direction and
	// the unique ID of what it is attached to, -1 for nothing.
	fireworkKeyItem      = protocol.EntityDataKeyDisplayFirework
	fireworkKeyDirection = protocol.EntityDataKeyDisplayOffset
	fireworkKeyAttached  = protocol.EntityDataKeyCustomDisplay
	fireworkNotAttached  = int64(-1)

	// fireworkUseRise is the direction, straight up, of a rocket launched from a block
	// (0xC98CF10).
	fireworkUseRise = 1
)

// FireworkRocket is a firework rocket in flight: BDS's FireworksRocketActor. It flies up
// along its direction, or rides with the player it is attached to, and explodes when its
// lifetime is up. Runs: owner of its entity.
type FireworkRocket struct {
	s      *Host
	entity *world.Entity
	vel    mgl32.Vec3
	dir    mgl32.Vec3

	// item is the rocket item it was made from; its tag holds the Fireworks compound the
	// clients draw the explosion from.
	item     ItemStack
	age      int32
	lifetime int32
	// attached is the player it rides with, if any.
	attached *Player
	// dispensed is set for rockets from a dispenser (BDS's +944).
	dispensed bool
	gone      bool
}

// Entity returns the rocket's entity.
func (f *FireworkRocket) Entity() *world.Entity { return f.entity }

// Item returns the rocket item it was made from.
func (f *FireworkRocket) Item() ItemStack { return f.item }

// Attached returns the player the rocket rides with, nil if it flies free.
func (f *FireworkRocket) Attached() *Player { return f.attached }

// Direction returns the way it flies when free.
func (f *FireworkRocket) Direction() mgl32.Vec3 { return f.dir }

// Lifetime returns the tick of its age it explodes after.
func (f *FireworkRocket) Lifetime() int32 { return f.lifetime }

// SetLifetime sets the tick of its age it explodes after.
func (f *FireworkRocket) SetLifetime(ticks int32) { f.lifetime = ticks }

// Dispensed reports whether a dispenser shot it.
func (f *FireworkRocket) Dispensed() bool { return f.dispensed }

// Age returns how many ticks it has flown.
func (f *FireworkRocket) Age() int32 { return f.age }

// Explosions returns how many explosions (stars) its item holds.
func (f *FireworkRocket) Explosions() int { return fireworkExplosions(f.item) }

// FireworkFunc is a firework rocket exploding in r: what its clients are shown and whom it
// hurts.
type FireworkFunc func(r *world.Region, f *FireworkRocket)

// FireworkEvent is a firework rocket exploding. Particles is whether those who see it are
// shown the explosion; Damage is the damage before distance, 0 for a rocket without stars.
// Cancel it for neither. Runs: owner of the region.
type FireworkEvent struct {
	Cancellable
	Region    *world.Region
	Rocket    *FireworkRocket
	Particles bool
	Damage    float32
}

// OnFirework listens to firework rockets exploding.
func OnFirework(s *Setup, fn func(e *FireworkEvent), p Priority) error {
	return addListener(s, &s.h.game.fireworkEv, fn, p)
}

// fireworksTag is the Fireworks compound of an item's tag.
func fireworksTag(item ItemStack) map[string]any {
	m, _ := item.Tag["Fireworks"].(map[string]any)
	return m
}

// fireworkExplosions counts the explosions in the item's Fireworks compound.
func fireworkExplosions(item ItemStack) int {
	switch l := fireworksTag(item)["Explosions"].(type) {
	case []any:
		return len(l)
	case []map[string]any:
		return len(l)
	}
	return 0
}

// fireworkLifetime is BDS's lifetime (0xBD5EFC0) for an item.
func fireworkLifetime(item ItemStack) int32 {
	life := int32(fireworkLifeBase)
	if v, ok := fireworksTag(item)["Flight"]; ok {
		life = fireworkLifePerFlight*nbtInt(v) + fireworkLifeBase
	}
	return life + rand.Int32N(fireworkLifeRandA) + rand.Int32N(fireworkLifeRandB) //nolint:gosec // a flight, not a secret
}

// LaunchFirework adds a firework rocket made from item at pos, flying along dir, or riding
// with attached when it is not nil, as BDS's rocket init (0xBD5EB40) starts one. Runs:
// owner of the chunk at pos.
func (s *Host) LaunchFirework(d *world.Dimension, pos, dir mgl32.Vec3, item ItemStack, attached *Player) *FireworkRocket {
	f := &FireworkRocket{s: s, dir: dir, item: item, attached: attached, lifetime: fireworkLifetime(item)}
	jitter := mgl32.Vec3{float32(rand.NormFloat64()), float32(rand.NormFloat64()), float32(rand.NormFloat64())} //nolint:gosec // a flight, not a secret
	f.vel = jitter.Mul(fireworkJitter).Add(dir.Mul(fireworkThrust))
	f.entity = d.AddEntity(world.EntityConfig{
		UniqueID: itemUniqueIDBase | rand.Int64N(itemUniqueIDBase), RuntimeID: s.runtimeIDs.Add(1), //nolint:gosec // an ID, not a secret
		Pos: pos, Rot: flightRotation(f.vel),
		Owner: f, Spawn: f.spawnPackets, Move: f.movePacket, Input: f.tick,
	})
	return f
}

// metadata is its synched data.
func (f *FireworkRocket) metadata() protocol.EntityMetadata {
	tag := f.item.Tag
	if tag == nil {
		tag = map[string]any{}
	}
	attached := fireworkNotAttached
	if f.attached != nil && f.attached.entity != nil {
		attached = f.attached.entity.UniqueID()
	}
	return protocol.EntityMetadata{
		fireworkKeyItem: tag, fireworkKeyDirection: f.dir, fireworkKeyAttached: attached,
		protocol.EntityDataKeyWidth: float32(2 * fireworkHalf), protocol.EntityDataKeyHeight: float32(2 * fireworkHalf),
		protocol.EntityDataKeyScale: float32(1),
	}
}

// spawnPackets show it to a player. Not yet compared byte for byte with what BDS sends.
func (f *FireworkRocket) spawnPackets(e *world.Entity) []packet.Packet {
	rot := e.Rotation()
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: nameFireworksActor,
		Position: e.Position(), Velocity: f.vel, Pitch: rot.Pitch, Yaw: rot.Yaw, HeadYaw: rot.HeadYaw,
		EntityMetadata: f.metadata(),
	}}
}

func (f *FireworkRocket) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	return &packet.MoveActorAbsolute{EntityRuntimeID: e.RuntimeID(), Position: e.Position(), Rotation: mgl32.Vec3{rot.Pitch, rot.Yaw, rot.HeadYaw}}
}

// tick is BDS's rocket tick (0xBD5F2E0): the launch sound on its first tick, its age, and
// the explosion once its age passes its lifetime; then its movement (0x75ECFC0), a system
// of its own in BDS whose place in the tick was not worked out.
func (f *FireworkRocket) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || f.gone {
		return
	}
	if f.s.checkVoid(e); f.gone {
		return
	}
	if f.age == 0 {
		playSound(r, vecPos(e.Position()), soundEvent{name: packet.SoundEventLaunch, pos: e.Position(), extra: -1}, nil)
	}
	f.age++
	if f.age > f.lifetime {
		f.gone = true
		f.s.game.sys.Firework(r, f)
		e.Remove()
		return
	}
	f.move(r, e)
}

// move is BDS's rocket movement (0x75ECFC0): attached, it is where the player is; free,
// its speed grows by 1.05 and 0.03 along its direction, and it moves through the blocks'
// collision boxes, turned along its flight.
func (f *FireworkRocket) move(r *world.Region, e *world.Entity) {
	if f.attached != nil {
		pe := f.attached.entity
		if pe == nil || pe.Region() != r || f.attached.dead() {
			return
		}
		f.vel = f.attached.move.velocity
		e.Apply(world.Movement{Pos: pe.Position(), Rot: flightRotation(f.vel), Tick: e.Tick()})
		return
	}
	f.vel = f.vel.Mul(fireworkSpeedUp).Add(f.dir.Mul(fireworkThrust))
	pos := e.Position()
	for _, axis := range [...]int{1, 0, 2} {
		d := f.s.clipMove(r, pos, fireworkHalf, axis, f.vel[axis])
		if d != f.vel[axis] {
			f.vel[axis] = 0
		}
		pos[axis] += d
	}
	if !e.Apply(world.Movement{Pos: pos, Rot: flightRotation(f.vel), Tick: e.Tick()}) {
		f.vel = mgl32.Vec3{}
	}
}

// vanillaFirework is BDS's rocket explosion (0xBD5F2E0): its item's tag is sent again and
// the explosion event (ActorEvent 25), from which every client draws the explosion's
// particles and plays its sounds, the rocket attached to a gliding player too. With stars
// it hurts with cause 23: the attached player the full 5 + 2 per star, and every other
// player within 5 blocks whose head it sees that times sqrt((5 - distance) / 5).
// Spectators are left out. BDS's hurt does not push here.
func vanillaFirework(r *world.Region, f *FireworkRocket) {
	e := f.entity
	ev := FireworkEvent{Region: r, Rocket: f, Particles: true}
	if n := f.Explosions(); n > 0 {
		ev.Damage = fireworkDamageBase + fireworkDamagePerStar*float32(n)
	}
	if g := f.s.game; g.fireworkEv.Has() {
		g.fireworkEv.fire(f.s.log, &ev)
		if ev.Cancelled() {
			return
		}
	}
	if ev.Particles {
		e.Send(actorData(e.RuntimeID(), protocol.EntityMetadata{fireworkKeyItem: f.metadata()[fireworkKeyItem]}))
		e.Send(actorEvent(e.RuntimeID(), packet.ActorEventFireworksExplode))
	}
	if ev.Damage <= 0 {
		return
	}
	src := DamageSource{Cause: DamageFireworks}
	hurt := f.s.game.sys.Hurt
	if a := f.attached; a != nil && a.entity != nil && !a.dead() {
		hurt(a, ev.Damage, src)
	}
	at := e.Position()
	for _, o := range r.AppendEntitiesIn(nil, at[0]-fireworkReach, at[2]-fireworkReach, at[0]+fireworkReach, at[2]+fireworkReach) {
		p, ok := o.Owner().(*Player)
		if !ok || p == f.attached || p.dead() || p.mode() == GameModeSpectator {
			continue
		}
		head := o.Position()
		dist := head.Sub(at).Len()
		if dist > fireworkReach || f.s.rayBlocked(r, at, head) {
			continue
		}
		hurt(p, ev.Damage*float32(math.Sqrt(float64((fireworkReach-dist)/fireworkReach))), src)
	}
}

// OutOfWorld removes the rocket unexploded.
func (f *FireworkRocket) OutOfWorld(_ *Host, e *world.Entity) {
	f.gone = true
	e.Remove()
}

// useFirework is BDS's FireworksItem::use (0xC98D580): only while gliding, a rocket
// attached to the player, one item used up outside creative, and the glide boost for the
// rocket's lifetime sent to the player (MovementEffect), which its client flies by.
func (p *Player) useFirework(held ItemStack) bool {
	if !p.Gliding() || p.entity == nil {
		return false
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	f := p.s.LaunchFirework(r.Dimension(), p.entity.Position(), mgl32.Vec3{}, held, p)
	p.useUpHeld()
	p.send(&packet.MovementEffect{EntityRuntimeID: p.runtimeID, Type: packet.MovementEffectTypeGlideBoost, Duration: f.lifetime, Tick: p.entity.Tick()})
	p.flush()
	return true
}

// useFireworkOn is BDS's FireworksItem::useOn (0xC98CF10): not while gliding; a rocket
// flying up from where the block was clicked, one item used up outside creative.
func (p *Player) useFireworkOn(r *world.Region, clicked cube.Pos, click mgl32.Vec3, held ItemStack) bool {
	if p.Gliding() {
		return true
	}
	at := blockVec(clicked).Add(click)
	p.s.LaunchFirework(r.Dimension(), at, mgl32.Vec3{0, fireworkUseRise, 0}, held, nil)
	p.useUpHeld()
	p.flush()
	return true
}

// useUpHeld takes one of the held item outside creative.
func (p *Player) useUpHeld() {
	if p.creative() {
		return
	}
	inv := &p.inventory
	s := &inv.main[inv.held]
	s.Count--
	if s.Count == 0 {
		*s = ItemStack{}
	}
	p.sendHeld()
}

// dispenseFirework is BDS's FireworksItem::dispense (0xC98DC70): a rocket flying out of
// the face, just outside it, with the dispenser's click. False if the item is not a rocket.
func (s *Host) dispenseFirework(r *world.Region, pos cube.Pos, face cube.Face, at mgl32.Vec3, item ItemStack) bool {
	if item.Name != nameFireworkRocket {
		return false
	}
	dir := faceVec(face)
	var start mgl32.Vec3
	for n := range 3 {
		start[n] = float32(math.Floor(float64(at[n]-dir[n]))) + 0.5 + dir[n]*0.5 + dir[n]*fireworkHalf
	}
	if face == cube.FaceDown || face == cube.FaceUp {
		start[1] -= fireworkHalf
	}
	one := item
	one.Count = 1
	f := s.LaunchFirework(r.Dimension(), start, dir, one, nil)
	f.dispensed = true
	playSound(r, pos, soundEvent{name: packet.SoundEventBlockClick, pos: at, extra: -1}, nil)
	return true
}
