// Package ride is riding, as BDS 1.26.60 does it for anything with a rideable component:
// who may get on, which seat each rider gets, where a rider sits, what the client is told,
// and where a rider lands on getting off.
//
// It knows nothing of minecarts or players. A vehicle keeps a Vehicle and its rider
// implements Rider, so a minecart, a mob or an entity that makes a block a seat share one
// system. It keeps no global state; a Vehicle belongs to the region its owner ticks in and
// is only touched from there.
package ride

import (
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
)

// DismountMode is rideable's dismount_mode.
type DismountMode int

// BDS's dismount modes. Only DismountDefault has its landing search here so far; the
// others land on top of the vehicle.
const (
	DismountDefault DismountMode = iota
	DismountOnTopCenter
	DismountLookDirection
)

// Seat is one entry of rideable's seats.
type Seat struct {
	// Position is where the seat is from the vehicle's position.
	Position mgl64.Vec3
	// MinRiders and MaxRiders say how many riders the vehicle must have for this seat to
	// be in use. Both zero means always.
	MinRiders, MaxRiders int
	// LockRotation and LockDegrees are lock_rider_rotation.
	LockRotation bool
	LockDegrees  float64
	// Rotates and RotateBy are rotate_rider_by, which BDS may work out from an expression
	// each tick. The vehicle sets the value.
	Rotates  bool
	RotateBy float64
}

// Rules is rideable's definition.
type Rules struct {
	// SeatCount is how many riders fit. ControllingSeat is the seat of who steers it.
	SeatCount, ControllingSeat int
	// CrouchingSkipInteract stops a crouching player getting on. BDS's default is true.
	CrouchingSkipInteract bool
	// RiderCanInteract lets someone on the vehicle use it.
	RiderCanInteract bool
	// PullIn lets the vehicle take those it touches on board.
	PullIn bool
	// MaxWidth is passenger_max_width: 0 is no limit, otherwise a rider must be narrower.
	MaxWidth float64
	// Families is family_types: who may ride. Empty means anyone.
	Families []string
	// Seats are the seats in order.
	Seats []Seat
	// Dismount is how riders get off.
	Dismount DismountMode
	// ExitAbove is the upward one of the three searches ExitVehicleSystemImpl picks from
	// (1.26.60 0xBFA8F80, against the eight places around of 0xBFA89D0): the rider gets up
	// where it sat, standing on the vehicle. Boats land riders this way in BDS.
	ExitAbove bool
}

// Body is what riding needs to know of a rider.
type Body struct {
	// HeightOffset is how far its position is above its feet: 1.62 for a player.
	HeightOffset float64
	// RidingOffset is BDS's offset added to HeightOffset in a seat, -0.4 for a player. A baby
	// does not take it.
	RidingOffset float64
	// RidingHeight replaces HeightOffset in a seat when HasRidingHeight is set, as BDS's
	// RidingHeightComponent does for a few mobs.
	RidingHeight    float64
	HasRidingHeight bool
	// Width and Height are its box.
	Width, Height float64
	// Families are its type_family.
	Families []string
}

// Rider is what can sit on a vehicle.
type Rider interface {
	// RideID is its unique actor ID.
	RideID() int64
	// RideBody is its size and family.
	RideBody() Body
}

// Vehicle is the riders of one vehicle.
type Vehicle struct {
	rules  *Rules
	scale  float64
	riders []Rider
}

// New returns a vehicle with no riders, at a scale of 1. The rules are not copied.
func New(rules *Rules) *Vehicle { return &Vehicle{rules: rules, scale: 1} }

// SetScale sets the vehicle's box scale, which seats grow with.
func (v *Vehicle) SetScale(scale float64) { v.scale = scale }

// Rules returns its rules.
func (v *Vehicle) Rules() *Rules { return v.rules }

// Riders returns who rides it, first rider first. The slice must not be changed.
func (v *Vehicle) Riders() []Rider { return v.riders }

// First returns the first rider, nil if there is none.
func (v *Vehicle) First() Rider {
	if len(v.riders) == 0 {
		return nil
	}
	return v.riders[0]
}

// Has says whether r rides it.
func (v *Vehicle) Has(r Rider) bool { return slices.Contains(v.riders, r) }

// Full says whether all its seats are taken.
func (v *Vehicle) Full() bool { return len(v.riders) >= v.rules.SeatCount }

// CanAdd is canAddPassenger: a seat free, and a rider of the right family that fits. It
// does not know whether the rider is on something else; the caller does.
func (v *Vehicle) CanAdd(r Rider) bool {
	if v.Full() || v.Has(r) {
		return false
	}
	body := r.RideBody()
	if v.rules.MaxWidth != 0 && v.rules.MaxWidth <= body.Width {
		return false
	}
	if len(v.rules.Families) == 0 {
		return true
	}
	for _, f := range body.Families {
		if slices.Contains(v.rules.Families, f) {
			return true
		}
	}
	return false
}

// Add seats a rider and says whether it could.
func (v *Vehicle) Add(r Rider) bool {
	if !v.CanAdd(r) {
		return false
	}
	v.riders = append(v.riders, r)
	return true
}

// Remove takes a rider off and says whether it was on. Those behind it move up a seat.
func (v *Vehicle) Remove(r Rider) bool {
	i := slices.Index(v.riders, r)
	if i < 0 {
		return false
	}
	v.riders = slices.Delete(v.riders, i, i+1)
	return true
}

// Seat is getSeatDescriptionOfPassenger: of the seats in use with this many riders, the
// one at the rider's place. A rider with no such seat gets the empty seat.
func (v *Vehicle) Seat(r Rider) Seat {
	i := slices.Index(v.riders, r)
	if i < 0 {
		return Seat{}
	}
	return seatFor(v.rules.Seats, len(v.riders), i)
}

func seatFor(seats []Seat, riders, index int) Seat {
	n := 0
	for _, s := range seats {
		if s.MinRiders == 0 && s.MaxRiders == 0 || riders >= s.MinRiders && riders <= s.MaxRiders {
			if n == index {
				return s
			}
			n++
		}
	}
	return Seat{}
}

// SeatOffset is what BDS keeps as a rider's seat offset: the seat, scaled, with the rider's
// own height and riding offset added. A rider's position is above its feet.
func (v *Vehicle) SeatOffset(r Rider) mgl64.Vec3 {
	return offset(v.Seat(r), v.scale, r.RideBody())
}

func offset(s Seat, scale float64, body Body) mgl64.Vec3 {
	height := body.HeightOffset + body.RidingOffset
	if body.HasRidingHeight {
		height = body.RidingHeight
	}
	return mgl64.Vec3{s.Position[0] * scale, s.Position[1]*scale + height, s.Position[2] * scale}
}

// Position is getNewPassengerPosition: where a seat offset puts a rider on a vehicle at pos
// that has turned to yaw degrees.
func Position(pos mgl64.Vec3, yaw float64, seatOffset mgl64.Vec3) mgl64.Vec3 {
	sin, cos := math.Sincos(float64(yaw) * -math.Pi / 180)
	s, c := float64(sin), float64(cos)
	return mgl64.Vec3{
		pos[0] + seatOffset[0]*c + seatOffset[2]*s,
		pos[1] + seatOffset[1],
		pos[2] + seatOffset[2]*c - seatOffset[0]*s,
	}
}

// Metadata is the seat data of a rider, the keys BDS's setValuesToSynchedActorData writes.
func (v *Vehicle) Metadata(r Rider) protocol.EntityMetadata {
	s := v.Seat(r)
	return protocol.EntityMetadata{
		protocol.EntityDataKeySeatOffset:                       v.SeatOffset(r),
		protocol.EntityDataKeySeatLockPassengerRotation:        boolByte(s.LockRotation),
		protocol.EntityDataKeySeatLockPassengerRotationDegrees: s.LockDegrees,
		protocol.EntityDataKeySeatRotationOffset:               boolByte(s.Rotates),
		protocol.EntityDataKeySeatRotationOffsetDegrees:        s.RotateBy,
	}
}

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// Link is the link that tells a client a rider got on or off a vehicle with this unique ID.
func Link(vehicleID, riderID int64, kind byte) protocol.EntityLink {
	return protocol.EntityLink{RiddenEntityUniqueID: vehicleID, RiderEntityUniqueID: riderID, Type: kind, RiderInitiated: true}
}

// Links are the links of all its riders, for showing the vehicle to someone.
func (v *Vehicle) Links(vehicleID int64) []protocol.EntityLink {
	links := make([]protocol.EntityLink, 0, len(v.riders))
	for _, r := range v.riders {
		links = append(links, Link(vehicleID, r.RideID(), protocol.EntityLinkRider))
	}
	return links
}

// Blocks is what the search for a place to land asks of the world.
type Blocks interface {
	// Boxes returns the collision boxes of a block, from its corner.
	Boxes(pos cube.Pos) []registry.Box
	// PassThrough is ignoredExitCollisionBlock: a block a rider may land in.
	PassThrough(pos cube.Pos) bool
}

// exitUp lifts every landing by a little in 1.26.60.
const exitUp = 0.001

// stillMoved is the squared distance below which a vehicle counts as not moving.
const stillMoved = 0.00009801

// Exit is ExitVehicleSystem: where a rider lands, as its position (above its feet).
//
// pos is the vehicle's position, moved how far it just went, and seat where the rider sat.
// It tries beside the vehicle, then behind and ahead, on the vehicle's level, then a block
// up and a block down. With nowhere to stand it lands on top of the vehicle, at top.
func Exit(b Blocks, body Body, pos, moved, seat mgl64.Vec3, top float64) mgl64.Vec3 {
	forward, right := directions(moved)
	base := mgl64.Vec3{floor(pos[0]) + 0.5, floor(pos[1]), floor(pos[2]) + 0.5}
	pattern := [...]mgl64.Vec3{
		right,
		right.Mul(-1),
		right.Sub(forward),
		right.Mul(-1).Sub(forward),
		right.Add(forward),
		forward.Sub(right),
		forward.Mul(-1),
		forward,
	}
	for _, layer := range [...]float64{0, 1, -1} {
		for _, step := range pattern {
			step[1] = layer
			if height, ok := canLand(b, body, base.Add(step)); ok {
				return mgl64.Vec3{
					seat[0] + step[0],
					floor(pos[1]) + height + layer + body.HeightOffset + exitUp,
					seat[2] + step[2],
				}
			}
		}
	}
	return mgl64.Vec3{pos[0], top + body.HeightOffset + exitUp, pos[2]}
}

// ExitAbove is where a rider of a vehicle with Rules.ExitAbove lands: over its seat,
// standing on top, the vehicle's top or one or two blocks higher, the first with room for
// the rider. With room nowhere it stands on top anyway.
func ExitAbove(b Blocks, body Body, seat mgl64.Vec3, top float64) mgl64.Vec3 {
	half := min(body.Width, 1) * 0.5
	for up := range 3 {
		feet := top + float64(up)
		lo := mgl64.Vec3{seat[0] - half, feet, seat[2] - half}
		hi := mgl64.Vec3{seat[0] + half, feet + body.Height, seat[2] + half}
		if room(b, lo, hi) {
			return mgl64.Vec3{seat[0], feet + body.HeightOffset + exitUp, seat[2]}
		}
	}
	return mgl64.Vec3{seat[0], top + body.HeightOffset + exitUp, seat[2]}
}

// room reports whether no block's box cuts into lo to hi.
func room(b Blocks, lo, hi mgl64.Vec3) bool {
	from, to := blockAt(lo), blockAt(hi)
	for x := from[0]; x <= to[0]; x++ {
		for y := from[1]; y <= to[1]; y++ {
			for z := from[2]; z <= to[2]; z++ {
				if blocked(b, cube.Pos{x, y, z}, lo, hi) {
					return false
				}
			}
		}
	}
	return true
}

// directions is calculateVehicleDirections: the way it moves along its main axis, and the
// way to its side. A vehicle at rest counts as going west.
func directions(moved mgl64.Vec3) (forward, right mgl64.Vec3) {
	dx, dz := moved[0], moved[2]
	if dx*dx+dz*dz <= stillMoved {
		return mgl64.Vec3{-1, 0, 0}, mgl64.Vec3{0, 0, -1}
	}
	if abs(dx) > abs(dz) {
		forward = mgl64.Vec3{sign(dx), 0, 0}
	} else {
		forward = mgl64.Vec3{0, 0, sign(dz)}
	}
	return forward, mgl64.Vec3{-forward[2], 0, forward[0]}
}

// canLand is the check of one place: there must be a floor, and room for the rider on it.
func canLand(b Blocks, body Body, at mgl64.Vec3) (float64, bool) {
	height, ok := floorHeight(b, blockAt(at))
	if !ok {
		return 0, false
	}
	half := min(body.Width, 1) * 0.5
	lo := mgl64.Vec3{at[0] - half, at[1] + height, at[2] - half}
	hi := mgl64.Vec3{at[0] + half, at[1] + height + body.Height, at[2] + half}
	from, to := blockAt(lo), blockAt(hi)
	for x := from[0]; x <= to[0]; x++ {
		for y := from[1]; y <= to[1]; y++ {
			for z := from[2]; z <= to[2]; z++ {
				if blocked(b, cube.Pos{x, y, z}, lo, hi) {
					return 0, false
				}
			}
		}
	}
	return height, true
}

func blocked(b Blocks, pos cube.Pos, lo, hi mgl64.Vec3) bool {
	boxLo, boxHi, ok := bounds(b.Boxes(pos))
	if !ok {
		return false
	}
	for n := range 3 {
		corner := float64(pos[n])
		if corner+boxHi[n] <= lo[n] || corner+boxLo[n] >= hi[n] {
			return false
		}
	}
	return !b.PassThrough(pos)
}

// floorHeight is calculateBlockFloorHeight: how far above the bottom of pos a rider would
// stand, if on anything. A block that fills the height of pos is no floor unless it can be
// landed in; then, as with air, the block below is what counts.
func floorHeight(b Blocks, pos cube.Pos) (float64, bool) {
	_, hi, ok := bounds(b.Boxes(pos))
	if ok {
		if hi[1] < 1 {
			return hi[1], true
		}
		if !b.PassThrough(pos) {
			return 0, false
		}
	}
	below := pos.Side(cube.FaceDown)
	_, hi, ok = bounds(b.Boxes(below))
	if !ok || b.PassThrough(below) {
		return 0, false
	}
	// A block lower than a full one leaves the rider hanging, which BDS does not allow.
	if height := hi[1] - 1; height >= 0 {
		return height, true
	}
	return 0, false
}

// bounds is the one box around a block's boxes, which is what BDS's getCollisionShape gives.
func bounds(boxes []registry.Box) (lo, hi mgl64.Vec3, ok bool) {
	for i, box := range boxes {
		for n := range 3 {
			if i == 0 || float64(box[n]) < lo[n] {
				lo[n] = float64(box[n])
			}
			if i == 0 || float64(box[n+3]) > hi[n] {
				hi[n] = float64(box[n+3])
			}
		}
	}
	return lo, hi, len(boxes) > 0
}

func blockAt(v mgl64.Vec3) cube.Pos {
	return cube.Pos{int(floor(v[0])), int(floor(v[1])), int(floor(v[2]))}
}

func floor(v float64) float64 { return math.Floor(v) }

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
