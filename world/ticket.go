package world

import (
	"cmp"
	"iter"
	"slices"
	"sync/atomic"
	"time"

	"github.com/helix-bedrock/world/mcdb"
)

// Tickets keep chunks loaded without a player (RFC 0003, "Loading and
// unloading"). A ticket holds every chunk of an area the way a player's
// simulation zone does: the chunks load, stay loaded and claim their
// sections, so regions form and merge around them as around players. A
// ticking ticket also gives its chunks their block ticks.

// Area is a set of chunks picked out by a shape. It only names chunks;
// nothing about it is stored.
type Area = iter.Seq[mcdb.ChunkPos]

// Chunks is the area of the chunks given.
func Chunks(pos ...mcdb.ChunkPos) Area { return slices.Values(pos) }

// Box is the area of every chunk holding a block between the corners
// (x0, z0) and (x1, z1), block coordinates, both included.
func Box(x0, z0, x1, z1 int32) Area {
	return func(yield func(mcdb.ChunkPos) bool) {
		for x := min(x0, x1) >> 4; x <= max(x0, x1)>>4; x++ {
			for z := min(z0, z1) >> 4; z <= max(z0, z1)>>4; z++ {
				if !yield(mcdb.ChunkPos{x, z}) {
					return
				}
			}
		}
	}
}

// Radius is the area of every chunk holding a block within r blocks of the
// block (x, z), in X and Z.
func Radius(x, z, r int32) Area {
	return func(yield func(mcdb.ChunkPos) bool) {
		r2 := int64(r) * int64(r)
		for cx := (x - r) >> 4; cx <= (x+r)>>4; cx++ {
			for cz := (z - r) >> 4; cz <= (z+r)>>4; cz++ {
				dx := int64(min(max(x, cx<<4), cx<<4+15) - x)
				dz := int64(min(max(z, cz<<4), cz<<4+15) - z)
				if dx*dx+dz*dz <= r2 && !yield(mcdb.ChunkPos{cx, cz}) {
					return
				}
			}
		}
	}
}

// Ticket keeps an area's chunks loaded until Release. Its methods are safe
// for use by many goroutines at once.
type Ticket struct {
	d        *Dimension
	id       uint64
	ticking  bool
	released atomic.Bool
}

// ticketHold is what a region holds for a ticket: the ticket's chunks in
// its sections, in the ticket's order.
type ticketHold struct {
	t      *Ticket
	chunks []mcdb.ChunkPos
}

// KeepLoaded loads every chunk of area and keeps it loaded until the
// ticket is released. With ticking set the chunks get their block ticks
// each tick, as in a player's simulation zone; without, they stay loaded
// but frozen (a ticking player's zone still ticks them). After Close it
// holds nothing. Runs: anywhere.
func (d *Dimension) KeepLoaded(area Area, ticking bool) *Ticket {
	var chunks []mcdb.ChunkPos
	seen := map[mcdb.ChunkPos]bool{}
	for pos := range area {
		if !seen[pos] {
			seen[pos] = true
			chunks = append(chunks, pos)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ticketSeq++
	t := &Ticket{d: d, id: d.ticketSeq, ticking: ticking}
	if d.closing {
		t.released.Store(true)
		return t
	}
	// One entry per owned section, so it follows the section if its region
	// splits; the chunks of unowned sections go to one new region, which
	// claims them.
	bySection := map[sectionPos][]mcdb.ChunkPos{}
	var order []sectionPos
	var free []mcdb.ChunkPos
	for _, pos := range chunks {
		s := d.sectionOf(pos)
		if _, ok := d.sections[s]; !ok {
			free = append(free, pos)
			continue
		}
		if _, ok := bySection[s]; !ok {
			order = append(order, s)
		}
		bySection[s] = append(bySection[s], pos)
	}
	for _, s := range order {
		held := bySection[s]
		d.sections[s].post(entry{fn: func(r *Region) { r.holdTicket(t, held) }, pos: held[0], at: true})
	}
	if len(free) > 0 {
		n := d.newRegion(time.Now())
		n.post(entry{fn: func(r *Region) { r.holdTicket(t, free) }})
		d.schedule(n)
	}
	return t
}

// Release lets the ticket's chunks unload once nothing else holds them.
// Releasing twice does nothing. Runs: anywhere.
func (t *Ticket) Release() {
	if t.released.Swap(true) {
		return
	}
	d := t.d
	d.mu.Lock()
	defer d.mu.Unlock()
	// The holds may be in any region, or on their way to one: every region
	// gets the release, and holds not yet taken see the ticket released.
	for _, r := range d.regions {
		r.post(entry{fn: func(r *Region) { r.releaseTicket(t) }, every: true})
	}
}

// holdTicket holds chunks for t, unless it was released meanwhile.
func (r *Region) holdTicket(t *Ticket, chunks []mcdb.ChunkPos) {
	if t.released.Load() {
		return
	}
	for _, pos := range chunks {
		r.hold(pos)
	}
	r.addTicketHold(ticketHold{t: t, chunks: chunks})
}

// addTicketHold adds h to the region's ticket holds, kept in ticket order
// so the chunks tick in the same order on any number of cores.
func (r *Region) addTicketHold(h ticketHold) {
	i, ok := slices.BinarySearchFunc(r.tickets, h.t.id, func(o ticketHold, id uint64) int { return cmp.Compare(o.t.id, id) })
	if ok {
		r.tickets[i].chunks = append(r.tickets[i].chunks, h.chunks...)
		return
	}
	r.tickets = slices.Insert(r.tickets, i, h)
}

// releaseTicket drops the region's holds for t.
func (r *Region) releaseTicket(t *Ticket) {
	i, ok := slices.BinarySearchFunc(r.tickets, t.id, func(o ticketHold, id uint64) int { return cmp.Compare(o.t.id, id) })
	if !ok {
		return
	}
	chunks := r.tickets[i].chunks
	r.tickets = slices.Delete(r.tickets, i, i+1)
	for _, pos := range chunks {
		r.release(pos)
	}
}
