package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/ride"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Riding mobs, as BDS 1.26.60: a mob with minecraft:rideable is a vehicle like a minecart
// (riding.go). Its seats live on the mob (mob/ride.go), which seats mob riders itself; the
// engine seats players, shows the links and lands those who get off. A tamed, saddled horse
// whose rider steers it is client-predicted, as BDS's
// SetShouldBeSimulatedSystem::tickHorseSetIsClientPredicted (0x76D4E40): the rider's client
// moves it and Helix takes its place, as for boats.

// flagSaddled is the actor flag of a saddled mob; a client-predicted horse needs it and
// flagTamed.
const flagSaddled = 8

// horseTypes are the mobs with BDS's HorseFlagComponent: the Horse actor class.
var horseTypes = map[string]bool{
	"minecraft:horse": true, "minecraft:donkey": true, "minecraft:mule": true,
	"minecraft:skeleton_horse": true, "minecraft:zombie_horse": true,
}

func (m *Mob) riders() *ride.Vehicle { return m.m.Seats() }

func (m *Mob) rideEntity() *world.Entity { return m.entity }

func (m *Mob) rideMoved() mgl32.Vec3 {
	b := m.m.Body()
	return mglVec(b.Pos).Sub(mglVec(b.PrevPos))
}

func (m *Mob) rideTop() float32 { return m.m.Body().Box.Max.Y }

// rideLinks are the links of the mob's riders, sent with it.
func (m *Mob) rideLinks(e *world.Entity) []protocol.EntityLink {
	seats := m.m.Seats()
	if seats == nil || len(seats.Riders()) == 0 {
		return nil
	}
	return seats.Links(e.UniqueID())
}

// mountMob seats the player on the mob, as RideableComponent's interaction: not while
// sneaking when crouching_skip_interact is set, and only as canAddPassenger allows.
func (p *Player) mountMob(o *Mob) bool {
	seats := o.m.Seats()
	if seats == nil || p.entity == nil || p.vehicle != nil || o.m.Dead() {
		return false
	}
	if seats.Rules().CrouchingSkipInteract && p.Moving(MoveSneaking) {
		return false
	}
	if !o.m.AddRider(p, actorOf(p.entity)) {
		return false
	}
	p.vehicle = o
	p.move.fall = 0
	o.entity.Send(&packet.SetActorLink{EntityLink: ride.Link(o.entity.UniqueID(), p.uniqueID, protocol.EntityLinkRider)})
	p.showSeat(true)
	return true
}

// removeRider takes a rider out of a vehicle's seats; a mob is told so it keeps its riders.
func removeRider(v vehicle, r ride.Rider) {
	if o, ok := v.(*Mob); ok {
		o.m.RemoveRider(r)
		return
	}
	v.riders().Remove(r)
}

// driveMob moves the mob the player steers where its client put it, when BDS lets the
// client predict it: a tamed, saddled horse whose controlling seat the player has.
// Otherwise the player only sits. Region tick of the rider.
func (o *Mob) driveMob(p *Player, pos mgl32.Vec3, yaw float32, predicted bool) {
	if !o.clientDriven(p) {
		return
	}
	if !predicted {
		off := o.m.Seats().SeatOffset(p)
		pos = ride.Position(pos, yaw, off.Mul(-1))
	}
	o.m.Driven(vec(pos), yaw)
}

// clientDriven reports that p's client predicts the mob: a horse, tamed and saddled, with
// p in its controlling seat.
func (o *Mob) clientDriven(p *Player) bool {
	seats := o.m.Seats()
	if seats == nil || !horseTypes[o.m.Type()] || !o.m.Flag(flagTamed) || !o.m.Flag(flagSaddled) {
		return false
	}
	all, c := seats.Riders(), seats.Rules().ControllingSeat
	return c >= 0 && c < len(all) && all[c] == ride.Rider(p)
}

// Linked implements mob.RideHooks.
func (m *Mob) Linked(v *mob.Mob, rider behavior.ID, on bool) {
	r := m.entity.Region()
	if r == nil {
		return
	}
	o, ok := m.s.entityByID(r, v.ID()).(*Mob)
	if !ok {
		return
	}
	kind := byte(protocol.EntityLinkRider)
	if !on {
		kind = protocol.EntityLinkRemove
	}
	o.entity.Send(&packet.SetActorLink{EntityLink: ride.Link(o.entity.UniqueID(), int64(rider), kind)})
}

// Landed implements mob.RideHooks: the rider lands where ExitVehicleSystem puts it.
func (m *Mob) Landed(v, rider *mob.Mob) {
	r := m.entity.Region()
	if r == nil {
		return
	}
	o, ok := m.s.entityByID(r, v.ID()).(*Mob)
	if !ok {
		return
	}
	body := rider.RideBody()
	seat := mglVec(rider.Body().Pos)
	at := ride.Exit(rideBlocks{m.s, m.s.reg.Blocks, r}, body, o.entity.Position(), o.rideMoved(), seat, o.rideTop())
	if v.Seats() != nil && v.Seats().Rules().Dismount == ride.DismountOnTopCenter {
		pos := o.entity.Position()
		at = mgl32.Vec3{pos[0], o.rideTop() + rideExitUp, pos[2]}
	}
	rider.Teleport(vec(at))
}

// SpawnRider implements mob.RideHooks: minecraft:addrider's rider, spawned where the vehicle
// is and facing its way.
func (m *Mob) SpawnRider(v *mob.Mob, typ, event string) *mob.Mob {
	if m.entity == nil {
		return nil
	}
	r := m.entity.Region()
	if r == nil {
		return nil
	}
	d := r.Dimension()
	method := definition.InitSpawned
	if event != "" {
		method = definition.InitEvent
	}
	o, ok := m.s.newMob(typ, mglVec(v.Body().Pos), m.entity.Rotation().Yaw, method, event)
	if !ok {
		return nil
	}
	// Its components are read now, as the spawner's mobs are, so the vehicle's seats can
	// test its families when it gets on.
	o.m.Ready(r)
	o.add(d)
	return o.m
}

// EjectPlayer implements mob.RideHooks.
func (m *Mob) EjectPlayer(v *mob.Mob, rider behavior.ID) {
	r := m.entity.Region()
	if r == nil {
		return
	}
	o, ok := m.s.entityByID(r, v.ID()).(*Mob)
	if !ok {
		return
	}
	if p, ok := m.s.entityByID(r, rider).(*Player); ok {
		letOff(o, p)
		return
	}
	// A player no longer in the region only leaves its seat.
	for _, rr := range v.Seats().Riders() {
		if behavior.ID(rr.RideID()) == rider {
			v.RemoveRider(rr)
			break
		}
	}
}

// boostMount is a boost item used by a player riding a mob with minecraft:boostable (BDS
// 1.26.60 0x736ACB0): the mob boosts, the used stack takes the boost item's damage
// (hurtAndBreak, 0x736B0B0) and, if that broke it, becomes the boost item's replace_item.
func (p *Player) boostMount(held ItemStack) bool {
	o, ok := p.vehicle.(*Mob)
	if !ok {
		return false
	}
	bi, ok := o.m.Boost(mobItem(held))
	if !ok {
		return false
	}
	inv := &p.inventory
	p.hurtHeld(bi.Damage)
	if inv.main[inv.held].Empty() && bi.ReplaceItem != "" {
		inv.main[inv.held] = p.s.freshStack(ItemStack{Name: itemName(bi.ReplaceItem), Count: 1})
		p.sendHeld()
		p.flush()
	}
	return true
}
