package player

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Vehicle check: Helix's own rule, beyond BDS. The driver's client moves a boat or a horse it
// steers and Helix takes the place it sends, so a boat or horse cheat flies, speeds or climbs
// as it likes. Each move of a client-steered vehicle is held to what its physics reaches from
// the speed it had, as speed.go and vertical.go hold the player:
//
//   - A boat: the speed it had slowed by water (0.9), the air (0.9) or the block under it (ice
//     keeps more), plus a paddle's push; up, gravity 0.04 and drag 0.98, water lifting it as it
//     floats, a bubble column's push.
//   - A horse: the speed it had slowed by the ground's or the air's friction, plus one tick of
//     its movement speed's push and a jump's forward push; up, a jump as strong as its jump
//     strength from the ground, a step of a block, gravity and drag, swimming up in water.
//
// Every move is also swept against the blocks' collision boxes (a horse with its step up), so no
// vehicle goes through walls. Server pushes on the vehicle are allowed for a while, and a
// vehicle the driver just got on frees one move. A move past the limits is cut to them and the
// client is corrected there.

const (
	// vehBoatDrag is what water or the air leaves of a boat's speed each tick, vehBoatPush the
	// most a paddle adds.
	vehBoatDrag, vehBoatPush = 0.9, 0.05
	// vehBoatGravity is what gravity takes from a boat's vertical speed each tick.
	vehBoatGravity = 0.04
	// vehBoatFloat and vehBoatFloatKeep are the most water lifts a boat each tick, and how much of
	// its rising speed it keeps.
	vehBoatFloat, vehBoatFloatKeep = 0.15, 0.7
	// vehHorseAirPush is how much of a horse's movement speed a tick in the air adds, and
	// vehHorseJumpPush the forward push of a jump.
	vehHorseAirPush, vehHorseJumpPush = 0.2, 0.4
	// vehHorseStep is how high a horse steps, with room to spare; vehHorseJump its jump without a
	// jump strength.
	vehHorseStep, vehHorseJump = 1.1, 0.42
	// vehHorseSwim is the most water lifts or sinks a horse each tick.
	vehHorseSwim = 0.3
	// vehBank and vehRefill are how far past the limits a vehicle may go in all, and how much
	// comes back each move.
	vehBank, vehRefill = 0.6, 0.02
	// vehPushTicks is how many moves a push on the vehicle stays allowed.
	vehPushTicks = 40
)

// vehicleState is what the vehicle check knows of the vehicle the player drives. Region tick.
type vehicleState struct {
	// of is the vehicle the rest is about.
	of *world.Entity
	// last is its horizontal speed on its last move and vy its vertical speed.
	last, vy float32
	// push is a speed the server gave it, pushTicks how many more moves it waits.
	push      float32
	pushTicks int32
	bank      float32
}

// vehicleKind is the physics a client-steered vehicle moves by.
type vehicleKind struct {
	boat bool
	// speed is a horse's movement speed and jump its jump strength.
	speed, jump float32
}

// noteVehiclePush allows the vehicle the player drives the speed of v, which the server gave it.
// Region tick.
func (p *Player) noteVehiclePush(v mgl32.Vec3) {
	s := &p.move.vehicle
	s.push = max(s.push, v.Len())
	s.pushTicks = vehPushTicks
}

// vehicleCheck returns where the vehicle e the player steers may go from from to to: to itself,
// or the limits of its physics, after correcting the client.
func (p *Player) vehicleCheck(r *world.Region, e *world.Entity, from, to mgl32.Vec3, k vehicleKind, tick uint64) mgl32.Vec3 {
	s := &p.move.vehicle
	d := to.Sub(from)
	if s.of != e {
		*s = vehicleState{of: e, last: flatSpeed(d), vy: d[1], bank: vehBank}
		return to
	}
	lo, hi, ok := p.vehicleBox(e, from)
	if !ok {
		return to
	}
	tlo, thi, _ := p.vehicleBox(e, to)
	groundFrom, groundTo := p.groundUnderBox(r, lo, hi, e), p.groundUnderBox(r, tlo, thi, e)
	wet := p.liquidInBox(r, lo, hi) || p.liquidInBox(r, tlo, thi)
	limit := p.vehicleFlat(r, lo, hi, k, groundFrom && d[1] > 0, groundFrom, wet)
	up, down := p.vehicleUpDown(r, lo, hi, k, groundFrom, groundTo, wet)
	if s.pushTicks > 0 {
		s.pushTicks--
		limit, up, down = max(limit, s.push), max(up, s.push), min(down, -s.push)
	}
	s.bank = min(s.bank+vehRefill, vehBank)
	dist := flatSpeed(d)
	over := max(dist-limit, d[1]-up, down-d[1]) - speedSlack
	if over <= 0 || over <= s.bank {
		s.bank -= max(over, 0)
		if at, through := p.vehicleSweep(r, lo, hi, from, to, k); !through {
			return p.vehicleCut(e, from, to, at, groundTo, tick)
		}
		s.last, s.vy = dist, d[1]
		if groundTo && d[1] <= 0 {
			s.vy = 0
		}
		return to
	}
	at := from
	if dist > 0 {
		scale := min(limit/dist, 1)
		at[0], at[2] = from[0]+d[0]*scale, from[2]+d[2]*scale
	}
	at[1] = from[1] + min(max(d[1], down), up)
	at, _ = p.vehicleSweep(r, lo, hi, from, at, k)
	return p.vehicleCut(e, from, to, at, groundTo, tick)
}

// vehicleSweep sweeps the vehicle's box lo..hi from from toward to against the blocks, as the
// phase check sweeps the player (a horse also with its step up), and returns where it gets and
// whether that is to.
func (p *Player) vehicleSweep(r *world.Region, lo, hi mgl32.Vec3, from, to mgl32.Vec3, k vehicleKind) (mgl32.Vec3, bool) {
	d := to.Sub(from)
	got, ok := p.s.sweepMove(r, lo, hi, d, 0)
	if !ok || reached(got, d) {
		return to, true
	}
	if !k.boat {
		stepped, ok := p.s.sweepMove(r, lo, hi, d, vehHorseStep)
		if !ok || reached(stepped, d) {
			return to, true
		}
	}
	return from.Add(got), false
}

// vehicleCut puts the vehicle e at at instead of to and corrects its driver's client there.
func (p *Player) vehicleCut(e *world.Entity, from, to, at mgl32.Vec3, groundTo bool, tick uint64) mgl32.Vec3 {
	s := &p.move.vehicle
	s.last, s.vy = flatSpeed(at.Sub(from)), at[1]-from[1]
	p.s.log.Debug("vehicle move cut", "name", p.name, "from", from, "to", to, "at", at)
	p.send(&packet.CorrectPlayerMovePrediction{
		PredictionType: packet.PredictionTypeVehicle, Position: at, Rotation: mgl32.Vec2{0, e.Rotation().Yaw},
		OnGround: groundTo, Tick: tick,
	})
	return at
}

// vehicleFlat is the most the vehicle may move sideways this move.
func (p *Player) vehicleFlat(r *world.Region, lo, hi mgl32.Vec3, k vehicleKind, jumping, ground, wet bool) float32 {
	s := &p.move.vehicle
	under := vecPos(mgl32.Vec3{(lo[0] + hi[0]) / 2, lo[1] - 0.5, (lo[2] + hi[2]) / 2})
	if k.boat {
		friction := float32(vehBoatDrag)
		if ground && !wet {
			friction = max(friction, p.s.frictionOf(r, under))
		}
		return s.last*friction + vehBoatPush
	}
	if !ground {
		return s.last*speedAirFriction + k.speed*vehHorseAirPush
	}
	friction := max(p.s.frictionOf(r, under), speedMinFriction) * speedAirFriction
	limit := s.last*friction + k.speed*speedGroundPush/(friction*friction*friction)
	if jumping {
		limit += vehHorseJumpPush
	}
	return limit
}

// vehicleUpDown are the most the vehicle may rise and drop this move.
func (p *Player) vehicleUpDown(r *world.Region, lo, hi mgl32.Vec3, k vehicleKind, groundFrom, groundTo, wet bool) (up, down float32) {
	vy := p.move.vehicle.vy
	if k.boat {
		up = (vy - vehBoatGravity) * vertDrag
		down = up
		if wet {
			up = max(up, max(vy, 0)*vehBoatFloatKeep+vehBoatFloat)
		}
	} else {
		up = (vy - vertGravity) * vertDrag
		down = up
		if groundFrom {
			up = max(up, k.jump)
			if groundTo {
				up = max(up, vehHorseStep)
			}
		}
		if wet {
			up, down = max(up, vehHorseSwim), min(down, -vehHorseSwim)
		}
	}
	if groundTo {
		up = max(up, 0)
	}
	if p.bubbleInBox(r, lo, hi) {
		up = max(up, min(max(vy, 0)+vertBubbleStep, vertBubbleUp))
		down = min(down, max(min(vy, 0)-vertBubbleStep, -vertBubbleDown))
	}
	return up, down
}

// vehicleBox is the box of the vehicle e if it were at pos.
func (p *Player) vehicleBox(e *world.Entity, pos mgl32.Vec3) (lo, hi mgl32.Vec3, ok bool) {
	lo, hi, _, ok = p.s.EntityBox(e)
	if !ok {
		return lo, hi, false
	}
	off := pos.Sub(e.Position())
	return lo.Add(off), hi.Add(off), true
}

// liquidInBox reports whether water or lava touches the box lo..hi, or lies just under it.
func (p *Player) liquidInBox(r *world.Region, lo, hi mgl32.Vec3) bool {
	for x := itemFloor(lo[0]); x <= itemFloor(hi[0]); x++ {
		for y := itemFloor(lo[1] - vertReach); y <= itemFloor(hi[1]); y++ {
			for z := itemFloor(lo[2]); z <= itemFloor(hi[2]); z++ {
				if _, _, ok := p.liquidIn(r, cube.Pos{x, y, z}); ok {
					return true
				}
			}
		}
	}
	return false
}

// bubbleInBox reports whether a bubble column touches the box lo..hi or the block under it.
func (p *Player) bubbleInBox(r *world.Region, lo, hi mgl32.Vec3) bool {
	blocks := p.s.reg.Blocks
	for x := itemFloor(lo[0]); x <= itemFloor(hi[0]); x++ {
		for y := itemFloor(lo[1]) - 1; y <= itemFloor(hi[1]); y++ {
			for z := itemFloor(lo[2]); z <= itemFloor(hi[2]); z++ {
				if b, ok := r.Block(cube.Pos{x, y, z}, 0); ok && blocks.Name(b) == nameBubbleColumn {
					return true
				}
			}
		}
	}
	return false
}

// noteDrivenPush tells the client-steering driver of o, if any, that the server pushed o by v.
// Runs: owner of o's region.
func noteDrivenPush(o *world.Entity, v mgl32.Vec3) {
	if p := clientDriver(o); p != nil {
		p.noteVehiclePush(v)
	}
}

// clientDriver is the player whose client steers o: a boat's driver, a horse's rider in its
// controlling seat. Nil for anything else.
func clientDriver(o *world.Entity) *Player {
	switch t := o.Owner().(type) {
	case *Boat:
		return t.Driver()
	case *Mob:
		seats := t.m.Seats()
		if seats == nil {
			return nil
		}
		all, c := seats.Riders(), seats.Rules().ControllingSeat
		if c < 0 || c >= len(all) {
			return nil
		}
		if p, ok := all[c].(*Player); ok && t.clientDriven(p) {
			return p
		}
	}
	return nil
}
