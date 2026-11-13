package player

import (
	"maps"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/ride"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// playerFamily is the player's type_family that family_types test.
const playerFamily = "player"

// playerRidingOffset is what BDS's player constructor gives its riding offset component.
const playerRidingOffset = -0.4

// rideExitUp lifts a landing a little, as ride.Exit does.
const rideExitUp = 0.001

// vehicle is what a player can sit on: anything that keeps riders in the ride package and
// is an entity. Minecarts are the first; mobs will be the same.
type vehicle interface {
	// riders is who sits on it.
	riders() *ride.Vehicle
	// rideEntity is the entity riders are linked to.
	rideEntity() *world.Entity
	// rideMoved is how far it went in its last tick.
	rideMoved() mgl32.Vec3
	// rideTop is the height of the top of its box.
	rideTop() float32
}

// RideID is the player's unique ID, for links.
func (p *Player) RideID() int64 { return p.uniqueID }

// RideBody is the player's size for riding.
func (p *Player) RideBody() ride.Body {
	return ride.Body{
		HeightOffset: eyeHeight, RidingOffset: playerRidingOffset, Width: playerHalfWidth * 2, Height: playerHeight,
		Families: []string{playerFamily},
	}
}

// seatPosition is where the player's seat puts it. The server decides this, not the client.
func (p *Player) seatPosition() mgl32.Vec3 {
	e := p.vehicle.rideEntity()
	return ride.Position(e.Position(), e.Rotation().Yaw, p.vehicle.riders().SeatOffset(p))
}

// mount seats a player, as RideableComponent's interaction does, and says whether it could.
func mount(v vehicle, p *Player) bool {
	seats := v.riders()
	if p.entity == nil || p.vehicle != nil {
		return false
	}
	if seats.Rules().CrouchingSkipInteract && p.Moving(MoveSneaking) {
		return false
	}
	if !seats.Add(p) {
		return false
	}
	p.vehicle = v
	p.move.fall = 0
	e := v.rideEntity()
	e.Send(&packet.SetActorLink{EntityLink: ride.Link(e.UniqueID(), p.uniqueID, protocol.EntityLinkRider)})
	p.showSeat(true)
	return true
}

// letOff takes a player off and lands it where ExitVehicleSystem does.
func letOff(v vehicle, p *Player) {
	seats := v.riders()
	if !seats.Has(p) {
		return
	}
	e := v.rideEntity()
	var seat mgl32.Vec3
	if p.entity != nil {
		seat = p.seatPosition()
	}
	seats.Remove(p)
	p.vehicle = nil
	p.leftVehicle = e
	p.move.forward = 0
	e.Send(&packet.SetActorLink{EntityLink: ride.Link(e.UniqueID(), p.uniqueID, protocol.EntityLinkRemove)})
	if p.entity == nil {
		p.riding = false
		return
	}
	p.showSeat(false)
	r := e.Region()
	if r == nil {
		return
	}
	at := ride.Exit(rideBlocks{p.s, p.s.reg.Blocks, r}, p.RideBody(), e.Position(), v.rideMoved(), seat, v.rideTop())
	if seats.Rules().ExitAbove {
		at = ride.ExitAbove(rideBlocks{p.s, p.s.reg.Blocks, r}, p.RideBody(), seat, v.rideTop())
	}
	if seats.Rules().Dismount == ride.DismountOnTopCenter {
		pos := e.Position()
		at = mgl32.Vec3{pos[0], v.rideTop() + p.RideBody().HeightOffset + rideExitUp, pos[2]}
	}
	applyMovement(p, moveTo(at, p.entity.Rotation(), p.entity.Tick()))
}

// letOffAll takes everyone off, last rider first as BDS's ejectAllPassengers does.
func letOffAll(v vehicle) {
	seats := v.riders()
	if seats == nil {
		return
	}
	all := seats.Riders()
	for i := len(all) - 1; i >= 0; i-- {
		if p, ok := all[i].(*Player); ok {
			letOff(v, p)
		}
	}
}

// settleRiders lets go of riders who have left, died or gone away. They do not land: they
// are not there.
func settleRiders(v vehicle) {
	seats := v.riders()
	e := v.rideEntity()
	all := seats.Riders()
	for i := len(all) - 1; i >= 0; i-- {
		p, ok := all[i].(*Player)
		if !ok || p.entity != nil && !p.dead() && p.entity.Region() == e.Region() {
			continue
		}
		seats.Remove(p)
		p.vehicle, p.riding = nil, false
		e.Send(&packet.SetActorLink{EntityLink: ride.Link(e.UniqueID(), p.uniqueID, protocol.EntityLinkRemove)})
	}
}

// showSeat tells everyone the player sits or no longer does, with where.
func (p *Player) showSeat(on bool) {
	p.riding = on
	meta := protocol.EntityMetadata{protocol.EntityDataKeyFlags: p.actorFlags()}
	if on {
		maps.Copy(meta, p.vehicle.riders().Metadata(p))
	}
	pk := actorData(p.entity.RuntimeID(), meta)
	p.entity.Send(pk)
	// A player with no connection has no client to tell.
	if p.conn != nil {
		p.send(pk)
		p.flush()
	}
}

// rideBlocks is the world for the search for a place to land.
type rideBlocks struct {
	s      *Host
	blocks *registry.Blocks
	r      *world.Region
}

// Boxes returns a block's collision boxes. A block not loaded is no place to land.
func (b rideBlocks) Boxes(pos cube.Pos) []registry.Box {
	s, ok := b.r.Block(pos, 0)
	if !ok {
		return []registry.Box{{0, 0, 0, 1, 1, 1}}
	}
	return b.s.boxesAt(b.r, pos, s)
}

// PassThrough is ignoredExitCollisionBlock: open doors, trapdoors and gates, scaffolding
// and powder snow. BDS also passes a block property it tests by bit, not yet identified.
func (b rideBlocks) PassThrough(pos cube.Pos) bool {
	s, ok := b.r.Block(pos, 0)
	if !ok {
		return false
	}
	name := b.blocks.Name(s)
	switch name {
	case "minecraft:scaffolding", "minecraft:powder_snow":
		return true
	}
	if !strings.Contains(name, "door") && !strings.Contains(name, "fence_gate") {
		return false
	}
	open, _ := b.blocks.States(s)["open_bit"].(uint8)
	return open != 0
}
