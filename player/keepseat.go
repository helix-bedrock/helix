package player

import (
	"slices"

	"github.com/helix-bedrock/helix/ride"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Vehicles that keep a rider's seat while it is out of the game, as BDS saves riders in a
// vehicle's LinksTag and seats them again when they come back: cushions and boats.
const (
	// reseatTicks is how long a joining player looks for the vehicle it left on.
	reseatTicks = 100
	// reseatReach is how near a returning player must be to that vehicle.
	reseatReach = 2
	// The keys of LinksTag.
	linksTag      = "LinksTag"
	linkEntityTag = "entityID"
	linkIDTag     = "LinkID"
)

// seatKeeper is a vehicle that holds the seats of riders who left the game.
type seatKeeper interface {
	vehicle
	// away are the unique IDs of riders out of the game whose seats wait for them.
	away() *[]int64
	// keeperGone reports whether it was broken or removed.
	keeperGone() bool
}

// keepSeats lets go of riders who died or went to another region, and holds the seat of
// one who left the game. Owner of the vehicle.
func keepSeats(v seatKeeper) {
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
		if !p.dead() && (p.entity == nil || p.entity.Region() == nil) {
			*v.away() = append(*v.away(), p.uniqueID)
			continue
		}
		e.Send(&packet.SetActorLink{EntityLink: ride.Link(e.UniqueID(), p.uniqueID, protocol.EntityLinkRemove)})
	}
}

// seatsHeld reports whether riders and held seats fill it.
func seatsHeld(v seatKeeper) bool {
	return len(v.riders().Riders())+len(*v.away()) >= v.riders().Rules().SeatCount
}

// reseat puts a player who left the game riding back on its vehicle, for its first
// reseatTicks ticks back, while the chunks around it load. Region tick.
func (p *Player) reseat() {
	if p.reseatTicks <= 0 || p.entity == nil {
		return
	}
	p.reseatTicks--
	r := p.entity.Region()
	if r == nil || p.vehicle != nil {
		return
	}
	pos := p.entity.Position()
	p.touching = r.AppendEntitiesIn(p.touching[:0], pos[0]-reseatReach, pos[2]-reseatReach, pos[0]+reseatReach, pos[2]+reseatReach)
	for _, e := range p.touching {
		v, ok := e.Owner().(seatKeeper)
		if !ok || v.keeperGone() {
			continue
		}
		held := v.away()
		i := slices.Index(*held, p.uniqueID)
		if i < 0 {
			continue
		}
		*held = slices.Delete(*held, i, i+1)
		p.reseatTicks = 0
		if !v.riders().Add(p) {
			return
		}
		p.vehicle = v
		e.Send(&packet.SetActorLink{EntityLink: ride.Link(e.UniqueID(), p.uniqueID, protocol.EntityLinkRider)})
		p.showSeat(true)
		applyMovement(p, moveTo(p.seatPosition(), p.entity.Rotation(), p.entity.Tick()))
		return
	}
}

// saveLinks writes a vehicle's riders and held seats into its saved data as BDS's LinksTag.
func saveLinks(data map[string]any, v seatKeeper) {
	links := []any{}
	for i, r := range v.riders().Riders() {
		links = append(links, map[string]any{linkEntityTag: r.RideID(), linkIDTag: int32(i)}) //nolint:gosec // few seats
	}
	for _, id := range *v.away() {
		links = append(links, map[string]any{linkEntityTag: id, linkIDTag: int32(len(links))}) //nolint:gosec // few seats
	}
	if len(links) > 0 {
		data[linksTag] = links
	} else {
		delete(data, linksTag)
	}
}

// loadLinks are the riders a saved vehicle's LinksTag names: all wait for their seats.
func loadLinks(data map[string]any) []int64 {
	var ids []int64
	for _, l := range compounds(data[linksTag]) {
		if id, ok := l[linkEntityTag].(int64); ok {
			ids = append(ids, id)
		}
	}
	return ids
}
