package world

import (
	"sync"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/mcdb"
)

// Viewer is a player's two zones in a dimension: the simulation zone,
// kept loaded, and the view zone, sent to the client and released. Its
// methods are safe for use by many goroutines at once.
type Viewer struct {
	d     *Dimension
	send  func(pk *packet.LevelChunk)
	flush func()

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

	// Owned by the tick goroutine.
	lastCentre mcdb.ChunkPos
	lastRadius int32
	known      map[mcdb.ChunkPos]bool // sent, or asked for (waiting)
	waiting    map[mcdb.ChunkPos]bool
	queue      []mcdb.ChunkPos
	held       map[mcdb.ChunkPos]bool
	readyDone  bool
	scratch    []mcdb.ChunkPos
}

// AddViewer adds a player's zones. send queues a LevelChunk packet for the
// player and flush sends what was queued; both are called on the tick
// goroutine and must not block. Nothing is loaded until the first Move.
func (d *Dimension) AddViewer(send func(pk *packet.LevelChunk), flush func()) *Viewer {
	v := &Viewer{
		d: d, send: send, flush: flush, ready: make(chan struct{}),
		nets: map[mcdb.ChunkPos]*NetChunk{}, known: map[mcdb.ChunkPos]bool{},
		waiting: map[mcdb.ChunkPos]bool{}, held: map[mcdb.ChunkPos]bool{},
	}
	d.post(func() { d.viewers[v] = struct{}{} })
	return v
}

// Move sets the centre of the zones, a chunk position, and the view
// radius in chunks.
func (v *Viewer) Move(centre mcdb.ChunkPos, radius int32) {
	v.mu.Lock()
	v.centre, v.radius, v.placed = centre, max(radius, 0), true
	v.mu.Unlock()
}

// Ready is closed once every chunk of the simulation zone around the first
// position was sent, when the player can be spawned.
func (v *Viewer) Ready() <-chan struct{} { return v.ready }

// Close removes the zones; the chunks they held are released.
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

// update runs every tick on the tick goroutine.
func (d *Dimension) updateViewer(v *Viewer) {
	v.mu.Lock()
	centre, radius, placed, removed := v.centre, v.radius, v.placed, v.removed
	v.mu.Unlock()
	if removed {
		for pos := range v.held {
			d.release(pos)
		}
		delete(d.viewers, v)
		return
	}
	if !placed {
		return
	}
	sim := min(d.conf.SimulationDistance, radius)
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
				d.hold(pos)
			}
		}
		for pos := range v.held {
			if !inSim[pos] {
				delete(v.held, pos)
				d.release(pos)
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
		if nc := d.netChunk(pos); nc != nil {
			v.deliver(nc)
			sent++
			continue
		}
		v.waiting[pos] = true
		if _, loaded := d.chunks[pos]; !loaded {
			d.request(pos)
		}
	}
	if sent > 0 {
		v.flush()
	}
	if !v.readyDone && d.simulationSent(v, centre, sim) {
		v.readyDone = true
		close(v.ready)
	}
}

// simulationSent reports whether every chunk of v's simulation zone around
// centre was sent.
func (d *Dimension) simulationSent(v *Viewer, centre mcdb.ChunkPos, sim int32) bool {
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

// arrived gives v a chunk it may be waiting for. Tick goroutine only.
func (v *Viewer) arrived(pos mcdb.ChunkPos, nc *NetChunk) {
	if !v.waiting[pos] {
		return
	}
	delete(v.waiting, pos)
	v.deliver(nc)
	v.flush()
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
