package mob

import (
	"encoding/json"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/ride"
)

// Riding, as BDS 1.26.60. A mob with minecraft:rideable is a vehicle: Helix's ride package
// keeps its seats, as it does for minecarts and boats, and both players and mobs sit in
// them. A riding mob does not move by itself (BDS's movement systems leave out entities
// with a PassengerComponent): its vehicle puts it in its seat after the vehicle's own move,
// in Apply. The engine shows links and lands riders that get off (RideHooks).

// flagRiding is the actor flag of an entity in a seat.
const flagRiding = 2

// flagRotationLockedToVehicle is minecraft:rotation_locked_to_vehicle's flag: the rider
// faces where its vehicle faces.
const flagRotationLockedToVehicle = 126

// The vehicle's riding rules: dismount_mode's names.
const (
	dismountOnTopCenter = "on_top_center"
	dismountLookDir     = "look_direction"
)

// RideHooks is what an engine with riding implements on its Hooks. Runs: Apply.
type RideHooks interface {
	// Linked tells those who see the vehicle that the rider with the ID got on or off.
	Linked(vehicle *Mob, rider behavior.ID, on bool)
	// Landed puts a mob that got off where BDS's ExitVehicleSystem lands it.
	Landed(vehicle, rider *Mob)
	// EjectPlayer takes the player with the ID off the vehicle and lands it.
	EjectPlayer(vehicle *Mob, rider behavior.ID)
	// SpawnRider spawns a mob of the type at the vehicle, starting with the event (none
	// when empty), as minecraft:addrider does; nil when it could not.
	SpawnRider(vehicle *Mob, typ, event string) *Mob
}

// riding is a mob's riding state.
type riding struct {
	// rules and seats are the mob's own vehicle, nil seats without minecraft:rideable.
	rules ride.Rules
	seats *ride.Vehicle
	// riders are the IDs of who sits in the seats, kept with them.
	riders []behavior.ID
	// vehicle is what the mob rides, NoID for nothing.
	vehicle behavior.ID
	// mountPath is find_mount's path to its mount.
	mountPath *path.Path
	// driven is set while a player's client moves the mob (a ridden horse); the mob then
	// skips its own move for the tick.
	driven bool
	// addRiders is set until minecraft:addrider's riders were spawned; a loaded mob does
	// not spawn them again.
	addRiders bool
}

// Seats is the mob's vehicle, nil when it has no minecraft:rideable. Runs: the region's
// goroutine, outside the think phase.
func (m *Mob) Seats() *ride.Vehicle { return m.ride.seats }

// RideID implements ride.Rider.
func (m *Mob) RideID() int64 { return int64(m.id) }

// RideBody implements ride.Rider: a mob's position is its feet.
func (m *Mob) RideBody() ride.Body {
	return ride.Body{Width: m.body.Width, Height: m.body.Height, Families: m.families}
}

// Riders implements behavior.Actor.
func (m *Mob) Riders() []behavior.ID { return m.ride.riders }

// Vehicle implements behavior.Actor.
func (m *Mob) Vehicle() behavior.ID { return m.ride.vehicle }

// readRideable builds the mob's seats from minecraft:rideable, keeping who sits in them.
func (m *Mob) readRideable() {
	if m.states == nil || m.states.Rideable == nil {
		if m.ride.seats != nil && len(m.ride.seats.Riders()) > 0 {
			m.do(op{kind: opEjectRiders})
		}
		m.ride.seats = nil
		return
	}
	rideRules(m.states.Rideable.Config(), &m.ride.rules)
	if m.ride.seats == nil {
		m.ride.seats = ride.New(&m.ride.rules)
	}
	m.ride.seats.SetScale(m.scale)
	if m.ride.addRiders && m.states.AddRider != nil {
		m.ride.addRiders = false
		m.do(op{kind: opAddRiders})
	}
}

// rideRules turns minecraft:rideable into the ride package's rules.
func rideRules(c state.RideableConfig, r *ride.Rules) {
	r.SeatCount, r.ControllingSeat = int(c.SeatCount), int(c.ControllingSeat)
	r.CrouchingSkipInteract, r.RiderCanInteract, r.PullIn = c.CrouchingSkipInteract, c.RiderCanInteract, c.PullInEntities
	r.MaxWidth, r.Families = c.PassengerMaxWidth, c.FamilyTypes
	switch c.DismountMode {
	case dismountOnTopCenter:
		r.Dismount = ride.DismountOnTopCenter
	case dismountLookDir:
		r.Dismount = ride.DismountLookDirection
	default:
		r.Dismount = ride.DismountDefault
	}
	r.Seats = r.Seats[:0]
	for _, s := range c.Seats {
		seat := ride.Seat{
			Position:  mgl32.Vec3{s.Position.X, s.Position.Y, s.Position.Z},
			MinRiders: int(s.MinRiderCount), MaxRiders: int(s.MaxRiderCount),
			LockRotation: s.LockRiderRotation != 0, LockDegrees: s.LockRiderRotation,
		}
		// rotate_rider_by may be Molang; only a number is read so far.
		var by float32
		if len(s.RotateRiderBy) > 0 && json.Unmarshal(s.RotateRiderBy, &by) == nil {
			seat.Rotates, seat.RotateBy = true, by
		}
		r.Seats = append(r.Seats, seat)
	}
}

// ridersChanged copies the seats' riders into their IDs.
func (m *Mob) ridersChanged() {
	m.ride.riders = m.ride.riders[:0]
	if m.ride.seats == nil {
		return
	}
	for _, r := range m.ride.seats.Riders() {
		m.ride.riders = append(m.ride.riders, behavior.ID(r.RideID()))
	}
}

// AddRider seats a rider the engine puts on the mob (a player), as canAddPassenger allows.
// Runs: the region's goroutine, outside the think phase.
func (m *Mob) AddRider(r ride.Rider, a behavior.Actor) bool {
	if m.ride.seats == nil || m.states == nil || m.states.Rideable == nil || m.stateEnv.Self == nil {
		return false
	}
	if !m.states.Rideable.CanAddPassenger(&m.stateEnv, a) || !m.ride.seats.Add(r) {
		return false
	}
	m.ridersChanged()
	m.riderTrigger(m.states.Rideable.Config().OnRiderEnter)
	return true
}

// RemoveRider takes a rider the engine seated off. Runs: the region's goroutine, outside
// the think phase.
func (m *Mob) RemoveRider(r ride.Rider) {
	if m.ride.seats == nil || !m.ride.seats.Remove(r) {
		return
	}
	m.ridersChanged()
	if m.states != nil && m.states.Rideable != nil {
		m.riderTrigger(m.states.Rideable.Config().OnRiderExit)
	}
}

// riderTrigger fires on_rider_enter or on_rider_exit on the vehicle.
func (m *Mob) riderTrigger(t *definition.Trigger) {
	if t != nil {
		m.Trigger(t, m.tester(nil), nil)
	}
}

// Driven moves the mob where the client of the player steering it put it, as BDS takes a
// client-predicted horse's place, and keeps the mob's own move off this tick. Runs: the
// region's goroutine, outside the think phase.
func (m *Mob) Driven(pos behavior.Vec3, yaw float32) {
	m.ride.driven = true
	m.body.PrevPos = m.body.Pos
	m.body.Pos = pos
	m.body.Box = boxAt(pos, m.body.Width, m.body.Height)
	m.body.Vel = behavior.Vec3{}
	m.body.FallDistance = 0
	m.yaw, m.bodyYaw, m.headYaw = yaw, yaw, yaw
}

// ridingStill reports that the mob does not move by itself this tick: it rides something,
// or a player's client moves it.
func (m *Mob) ridingStill() bool {
	if m.ride.driven {
		m.ride.driven = false
		return true
	}
	return m.ride.vehicle != behavior.NoID
}

// StartRiding implements behavior.MountSeeker: the mob gets on at Apply.
func (m *Mob) StartRiding(mount behavior.ID) { m.do(op{kind: opStartRiding, id: mount}) }

// GiveUpRiding implements behavior.MountSeeker: the mob gets off at Apply.
func (m *Mob) GiveUpRiding() { m.do(op{kind: opStopRiding}) }

// DismountPassengers implements behavior.Mob: every rider gets off at Apply.
func (m *Mob) DismountPassengers() { m.do(op{kind: opEjectRiders}) }

// AcceptsRider implements behavior.MountSeeker: the mount's canAddPassenger for the mob, from
// its snapshot.
func (m *Mob) AcceptsRider(mount behavior.ID) bool {
	v := m.rt.Env.Mob(m.v.r, mount)
	if v == nil {
		return false
	}
	s := v.Snapshot()
	if !s.alive || !s.rideable || s.id == m.id || len(s.riders) >= s.seatCount || slices.Contains(s.riders, m.id) {
		return false
	}
	if len(s.rideFamilies) == 0 {
		return true
	}
	for _, f := range s.rideFamilies {
		if slices.Contains(m.families, f) {
			return true
		}
	}
	return false
}

// CanRide implements behavior.MountSeeker: BDS 1.26.60 0xB3C4ED0 asks the mount's rideable
// component, its canAddPassenger and the passenger_max_width fit.
func (m *Mob) CanRide(mount behavior.ID) bool {
	if !m.AcceptsRider(mount) {
		return false
	}
	s := m.rt.Env.Mob(m.v.r, mount).Snapshot()
	return s.rideMaxWidth == 0 || s.rideMaxWidth > m.body.Width
}

// ActorSleeping implements behavior.MountSeeker.
func (m *Mob) ActorSleeping(id behavior.ID) bool {
	a := m.rt.Env.Actor(m.v.r, id)
	return a != nil && a.Flag(behavior.FlagSleeping)
}

// ActorWet implements behavior.MountSeeker: another mob was in water last tick.
func (m *Mob) ActorWet(id behavior.ID) bool {
	v := m.rt.Env.Mob(m.v.r, id)
	return v != nil && v.Snapshot().inWater
}

// PathToActor implements behavior.MountSeeker: a path to the actor, kept for FollowPath.
func (m *Mob) PathToActor(a behavior.Actor) (ok, complete bool) {
	if m.nv == nil || a == nil {
		m.ride.mountPath = nil
		return false, false
	}
	nm := m.navMob()
	p := m.nv.CreatePathToMob(&nm, pathfinder{m}, a.Pos())
	m.ride.mountPath = p
	return p != nil, p != nil && p.Status == path.StatusComplete
}

// FollowPath implements behavior.MountSeeker.
func (m *Mob) FollowPath(speed float32) {
	if m.nv == nil || m.ride.mountPath == nil {
		return
	}
	nm := m.navMob()
	m.nv.Follow(pathWorld{&m.v}, &nm, m.ride.mountPath, speed)
	m.ride.mountPath = nil
}

// runRideOp does a riding op at Apply.
func (m *Mob) runRideOp(o *op) {
	if o.kind == opAddRiders {
		m.spawnRiders()
		return
	}
	switch o.kind {
	case opStartRiding:
		m.startRiding(m.rt.Env.Mob(m.v.r, o.id))
	case opStopRiding:
		m.stopRiding(true)
	case opEjectRiders:
		m.ejectRiders()
	}
}

// spawnRiders is minecraft:addrider (BDS's AddRiderComponent::reloadComponent): each rider
// is spawned at the mob, with its event, and gets on.
func (m *Mob) spawnRiders() {
	h, ok := m.hooks.(RideHooks)
	if !ok || m.states == nil || m.states.AddRider == nil || m.Dead() {
		return
	}
	for _, r := range m.states.AddRider.Riders {
		if c := h.SpawnRider(m, ns(r.EntityType), r.SpawnEvent); c != nil {
			c.startRiding(m)
		}
	}
}

// startRiding puts the mob in a seat of v, as canAddPassenger allows.
func (m *Mob) startRiding(v *Mob) bool {
	if v == nil || v == m || m.ride.vehicle != behavior.NoID || m.Dead() || v.Dead() {
		return false
	}
	if !v.AddRider(m, m) {
		return false
	}
	m.ride.vehicle = v.id
	m.SetFlag(flagRiding, true)
	m.body.Vel = behavior.Vec3{}
	m.body.FallDistance = 0
	if m.nv != nil {
		nm := m.navMob()
		m.nv.Stop(&nm)
	}
	if h, ok := m.hooks.(RideHooks); ok {
		h.Linked(v, m.id, true)
	}
	v.placeRiders()
	return true
}

// stopRiding takes the mob off what it rides; land puts it where riders get off.
func (m *Mob) stopRiding(land bool) {
	if m.ride.vehicle == behavior.NoID {
		return
	}
	v := m.rt.Env.Mob(m.v.r, m.ride.vehicle)
	m.ride.vehicle = behavior.NoID
	m.SetFlag(flagRiding, false)
	if v == nil {
		return
	}
	v.RemoveRider(m)
	h, ok := m.hooks.(RideHooks)
	if !ok {
		return
	}
	h.Linked(v, m.id, false)
	if land {
		h.Landed(v, m)
	}
}

// ejectRiders takes every rider off, last first, as Actor::removeAllPassengers.
func (m *Mob) ejectRiders() {
	if m.ride.seats == nil {
		return
	}
	all := m.ride.seats.Riders()
	for i := len(all) - 1; i >= 0; i-- {
		switch r := all[i].(type) {
		case *Mob:
			r.stopRiding(true)
		default:
			if h, ok := m.hooks.(RideHooks); ok {
				h.EjectPlayer(m, behavior.ID(r.RideID()))
			}
		}
	}
}

// Unlink takes the mob off its vehicle and its riders off it, without landing it: it is
// leaving the world. Runs: the region's goroutine, outside the think phase.
func (m *Mob) Unlink() {
	m.stopRiding(false)
	m.ejectRiders()
}

// tickRiders is the vehicle's part of Apply: a dead vehicle lets everyone off, and mob
// riders that are gone leave their seats; the others sit where their seats are now.
func (m *Mob) tickRiders() {
	if m.ride.seats == nil || len(m.ride.seats.Riders()) == 0 {
		return
	}
	if m.Dead() {
		m.ejectRiders()
		return
	}
	all := m.ride.seats.Riders()
	for i := len(all) - 1; i >= 0; i-- {
		// A rider spawned this tick has not ticked yet and knows no region.
		if r, ok := all[i].(*Mob); ok && (r.Dead() || r.v.r != nil && r.v.r != m.v.r) {
			r.stopRiding(false)
		}
	}
	m.placeRiders()
}

// placeRiders puts each mob rider in its seat, turned with the vehicle when its rotation is
// locked to it (BDS's positionPassenger after the vehicle's move).
func (m *Mob) placeRiders() {
	at := mgl32.Vec3{m.body.Pos.X, m.body.Pos.Y, m.body.Pos.Z}
	for _, rr := range m.ride.seats.Riders() {
		r, ok := rr.(*Mob)
		if !ok {
			continue
		}
		p := ride.Position(at, m.yaw, m.ride.seats.SeatOffset(r))
		r.body.PrevPos = r.body.Pos
		r.body.Pos = behavior.Vec3{X: p[0], Y: p[1], Z: p[2]}
		r.body.Box = boxAt(r.body.Pos, r.body.Width, r.body.Height)
		r.body.Vel = behavior.Vec3{}
		r.body.FallDistance = 0
		if r.flags.Has(flagRotationLockedToVehicle) {
			r.yaw, r.bodyYaw = m.yaw, m.yaw
		}
	}
}
