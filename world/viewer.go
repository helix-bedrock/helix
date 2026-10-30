package world

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/mcdb"
)

// Viewer is a player's two zones in a dimension: the simulation zone,
// kept loaded, and the view zone, sent to the client and released. Its
// methods are safe for use by many goroutines at once.
type Viewer struct {
	d *Dimension
	// id orders the viewers of a region, so they are updated in a fixed
	// order.
	id    uint64
	send  func(pk packet.Packet)
	flush func()

	// region is the region the viewer stands in. Guarded by d.mu.
	region *Region

	mu      sync.Mutex
	centre  mcdb.ChunkPos
	radius  int32
	placed  bool
	removed bool
	ready   chan struct{}

	// nets are the chunks the client has been told about, for its sub
	// chunk requests; read on the player's network goroutine.
	netMu sync.Mutex
	nets  map[mcdb.ChunkPos]*NetChunk

	// Owned by the viewer's region. lastRadius is -1 until the first Move
	// was seen.
	lastCentre mcdb.ChunkPos
	lastRadius int32
	known      map[mcdb.ChunkPos]bool // sent, or asked for (waiting)
	waiting    map[mcdb.ChunkPos]bool
	queue      []mcdb.ChunkPos
	held       map[mcdb.ChunkPos]bool
	readyDone  bool
	unflushed  bool
	scratch    []mcdb.ChunkPos
}

// AddViewer adds a player's zones in a region of their own, which merges
// with the regions nearby once the first Move places it. send queues a
// packet for the player (chunks, and the entities it sees) and flush sends
// what was queued; both are called on the region's tick and must not block. Nothing is loaded
// until the first Move. Runs: anywhere.
func (d *Dimension) AddViewer(send func(pk packet.Packet), flush func()) *Viewer {
	v := &Viewer{
		d: d, send: send, flush: flush, ready: make(chan struct{}), lastRadius: -1,
		nets: map[mcdb.ChunkPos]*NetChunk{}, known: map[mcdb.ChunkPos]bool{},
		waiting: map[mcdb.ChunkPos]bool{}, held: map[mcdb.ChunkPos]bool{},
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.viewerSeq++
	v.id = d.viewerSeq
	r := d.newRegion(time.Now())
	r.viewers = append(r.viewers, v)
	v.region = r
	d.schedule(r)
	return v
}

// Move sets the centre of the zones, a chunk position, and the view
// radius in chunks. Runs: anywhere.
func (v *Viewer) Move(centre mcdb.ChunkPos, radius int32) {
	v.mu.Lock()
	v.centre, v.radius, v.placed = centre, max(radius, 0), true
	v.mu.Unlock()
}

// Ready is closed once every chunk of the simulation zone around the first
// position was sent, when the player can be spawned. Runs: anywhere.
func (v *Viewer) Ready() <-chan struct{} { return v.ready }

// Close removes the zones; the chunks they held are released. Runs:
// anywhere.
func (v *Viewer) Close() {
	v.mu.Lock()
	v.removed = true
	v.mu.Unlock()
}

// SubChunks answers the client's request for sub chunks of chunks it was
// sent. Called on the player's network goroutine.
func (v *Viewer) SubChunks(pk *packet.SubChunkRequest) *packet.SubChunk {
	resp := &packet.SubChunk{Dimension: pk.Dimension, Position: pk.Position}
	resp.SubChunkEntries = make([]protocol.SubChunkEntry, 0, len(pk.Offsets))
	v.netMu.Lock()
	defer v.netMu.Unlock()
	for _, off := range pk.Offsets {
		if pk.Dimension != int32(v.d.conf.Dim) {
			resp.SubChunkEntries = append(resp.SubChunkEntries, protocol.SubChunkEntry{Offset: off, Result: protocol.SubChunkResultInvalidDimension})
			continue
		}
		pos := mcdb.ChunkPos{pk.Position[0] + int32(off[0]), pk.Position[2] + int32(off[2])}
		nc, ok := v.nets[pos]
		if !ok {
			resp.SubChunkEntries = append(resp.SubChunkEntries, protocol.SubChunkEntry{Offset: off, Result: protocol.SubChunkResultChunkNotFound})
			continue
		}
		resp.SubChunkEntries = append(resp.SubChunkEntries, nc.entry(off, pk.Position[1]+int32(off[1])))
	}
	return resp
}

// updateViewer runs every tick on the viewer's region; false once the
// viewer was removed.
func (r *Region) updateViewer(v *Viewer) bool {
	v.mu.Lock()
	centre, radius, placed, removed := v.centre, v.radius, v.placed, v.removed
	v.mu.Unlock()
	if removed {
		for pos := range v.held {
			r.release(pos)
		}
		clear(v.held)
		clear(v.waiting)
		return false
	}
	if !placed {
		return true
	}
	if centre != v.lastCentre && r.far(centre) {
		r.moveViewer(v, centre)
		return false
	}
	sim := min(r.d.conf.SimulationDistance, radius)
	if centre != v.lastCentre || radius != v.lastRadius || v.queue == nil {
		v.lastCentre, v.lastRadius = centre, radius
		v.netMu.Lock()
		for pos := range v.known {
			if dist2(pos, centre) > int64(radius)*int64(radius) {
				delete(v.known, pos)
				delete(v.waiting, pos)
				delete(v.nets, pos)
			}
		}
		v.netMu.Unlock()
		v.queue = chunksInRadius(v.queue, centre, radius)
		v.scratch = chunksInRadius(v.scratch, centre, sim)
		inSim := make(map[mcdb.ChunkPos]bool, len(v.scratch))
		for _, pos := range v.scratch {
			inSim[pos] = true
			if !v.held[pos] {
				v.held[pos] = true
				r.hold(pos)
			}
		}
		for pos := range v.held {
			if !inSim[pos] {
				delete(v.held, pos)
				r.release(pos)
			}
		}
	}
	sent := 0
	for n := 0; len(v.queue) > 0 && n < chunksPerTick && len(v.waiting) < maxWaiting; {
		pos := v.queue[0]
		v.queue = v.queue[1:]
		if v.known[pos] {
			continue
		}
		n++
		v.known[pos] = true
		if nc := r.netChunk(pos); nc != nil {
			v.deliver(nc)
			sent++
			continue
		}
		v.waiting[pos] = true
		r.d.want(pos, v)
	}
	if sent > 0 {
		v.unflushed = true
	}
	if !v.readyDone && simulationSent(v, centre, sim) {
		v.readyDone = true
		close(v.ready)
	}
	return true
}

// far reports whether the section of pos is more than MergeDistance
// sections from every section of the region: a viewer moving there would
// make the region claim sections that only merge it with another region
// to split again. A region with no sections yet claims its first.
func (r *Region) far(pos mcdb.ChunkPos) bool {
	if len(r.sections) == 0 {
		return false
	}
	s, m := r.d.sectionOf(pos), r.d.conf.MergeDistance
	for dx := -m; dx <= m; dx++ {
		for dz := -m; dz <= m; dz++ {
			if _, ok := r.sections[sectionPos{s[0] + dx, s[1] + dz}]; ok {
				return false
			}
		}
	}
	return true
}

// moveViewer hands a viewer that moved far away, with its player's
// entity, to the region that owns its new section, or to a new region, as
// entities are handed off: its chunks here are released, the players here
// lose its entity in the next visibility pass, and the work queued for it
// goes with it. The caller drops it from the region's viewers.
func (r *Region) moveViewer(v *Viewer, centre mcdb.ChunkPos) {
	for pos := range v.held {
		r.release(pos)
	}
	clear(v.held)
	var e *Entity
	if i := slices.IndexFunc(r.entities, func(o *Entity) bool { return o.conf.Viewer == v }); i >= 0 {
		e = r.entities[i]
		r.entities = slices.Delete(r.entities, i, i+1)
		r.unindex(e)
	}
	d := r.d
	d.mu.Lock()
	defer d.mu.Unlock()
	dst, ok := d.sections[d.sectionOf(centre)]
	if !ok {
		dst = d.newRegion(time.Now())
		d.schedule(dst)
	}
	v.region = dst
	if e != nil {
		e.region = nil
	}
	r.inMu.Lock()
	var queued []entry
	r.in = slices.DeleteFunc(r.in, func(q entry) bool {
		if q.v == v {
			queued = append(queued, q)
			return true
		}
		return false
	})
	r.inMu.Unlock()
	dst.post(entry{fn: func(o *Region) { o.arriveViewer(v, e) }, v: v})
	for _, q := range queued {
		dst.post(q)
	}
}

// arriveViewer takes a viewer, and its player's entity, handed over by
// another region.
func (r *Region) arriveViewer(v *Viewer, e *Entity) {
	i, _ := slices.BinarySearchFunc(r.viewers, v.id, func(o *Viewer, id uint64) int { return cmp.Compare(o.id, id) })
	r.viewers = slices.Insert(r.viewers, i, v)
	if e != nil {
		r.addEntity(e)
	}
}

// simulationSent reports whether every chunk of v's simulation zone around
// centre was sent.
func simulationSent(v *Viewer, centre mcdb.ChunkPos, sim int32) bool {
	for x := -sim; x <= sim; x++ {
		for z := -sim; z <= sim; z++ {
			pos := mcdb.ChunkPos{centre[0] + x, centre[1] + z}
			if x*x+z*z <= sim*sim && (!v.known[pos] || v.waiting[pos]) {
				return false
			}
		}
	}
	return true
}

// arrived gives v a chunk it may be waiting for, on its region's tick. It
// is flushed at the end of the tick.
func (v *Viewer) arrived(pos mcdb.ChunkPos, nc *NetChunk) {
	if !v.waiting[pos] {
		return
	}
	delete(v.waiting, pos)
	v.deliver(nc)
	v.unflushed = true
}

// sendEntity queues an entity packet for the client, flushed at the end of
// the tick.
func (v *Viewer) sendEntity(pk packet.Packet) {
	v.send(pk)
	v.unflushed = true
}

// deliver sends the chunk to the client.
func (v *Viewer) deliver(nc *NetChunk) {
	v.netMu.Lock()
	v.nets[nc.pos] = nc
	v.netMu.Unlock()
	v.send(nc.level)
}

// ChunkPos returns the chunk position of block coordinates x and z.
func ChunkPos(x, z int32) mcdb.ChunkPos { return mcdb.ChunkPos{x >> 4, z >> 4} }
