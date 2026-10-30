// Package world is the world in memory: the chunks of each dimension,
// loaded, generated, encoded and saved on worker pools, and the two zones
// every player has (see API_DESIGN.md, "World in memory").
//
// Each dimension is split into regions (RFC 0002) that tick in parallel on
// a shared Scheduler; every loaded chunk belongs to exactly one region.
package world

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Config configures a Dimension. Start from [DefaultConfig].
type Config struct {
	// Log receives the dimension's log. If nil, slog.Default().
	Log *slog.Logger
	// Dim is which dimension this is.
	Dim mcdb.Dimension
	// Store loads and saves the chunks.
	Store mcdb.WorldStore
	// Blocks and Biomes are the registry's tables.
	Blocks *registry.Blocks
	Biomes *registry.Biomes
	// Generator makes chunks the store does not have. If nil, Empty.
	Generator Generator
	// DiscardGenerated keeps generated chunks out of the store, even when
	// changed: for a stand-in generator whose chunks must never end up in
	// a world that will get real ones.
	DiscardGenerated bool
	// SimulationDistance is the radius in chunks of each player's
	// simulation zone, BDS's tick-distance.
	SimulationDistance int32
	// Workers is how many goroutines load, generate, encode and save
	// chunks. 0 means one per CPU.
	Workers int
	// CacheBytes is the memory budget of encoded chunks kept after they
	// were sent, so players nearby get them without another load.
	CacheBytes int
	// Scheduler ticks the dimension's regions. If nil, the dimension starts
	// its own with one goroutine per CPU and stops it on Close.
	Scheduler *Scheduler
	// SectionShift sets the size of a section, 1<<SectionShift chunks a
	// side, at most 10.
	SectionShift uint
	// MergeDistance is how close, in sections, two regions may come before
	// they merge, at least 1. Regions are always at least that many empty
	// sections apart, so nothing in one reaches the other within a tick.
	MergeDistance int32
}

// DefaultConfig returns BDS 1.26.60's tick-distance of 4 chunks, a 64 MiB
// chunk cache, sections of 8 by 8 chunks and a merge distance of one
// section. The section size and merge distance are placeholders until
// helix-bench picks them.
func DefaultConfig() Config {
	return Config{SimulationDistance: 4, CacheBytes: 64 << 20, SectionShift: 3, MergeDistance: 1}
}

// TickDuration is the length of a tick: 20 ticks a second, as BDS.
const TickDuration = 50 * time.Millisecond

const (
	// chunksPerTick is how many chunks a player may be sent or have asked
	// for per tick.
	chunksPerTick = 16
	// maxWaiting is how many chunk loads a player may have outstanding.
	maxWaiting = 64
	// maxSectionShift keeps sections within a sane size.
	maxSectionShift = 10
)

// Dimension holds the regions of one dimension and the chunk work shared
// by them. Its methods are safe for use by many goroutines at once.
type Dimension struct {
	conf     Config
	log      *slog.Logger
	r        cube.Range
	pool     *pool
	cache    *netCache
	sched    *Scheduler
	ownSched bool

	// mu guards the layout of the regions (which region owns each section)
	// and every region's state. It is held between ticks, or for a moment
	// inside one when a region claims or gives up a section.
	mu        sync.Mutex
	idle      *sync.Cond
	sections  map[sectionPos]*Region
	regions   []*Region
	parked    []*Region
	nextID    uint64
	viewerSeq uint64
	ticking   int
	closing   bool

	// loadMu guards the players waiting for encoded chunks.
	loadMu  sync.Mutex
	pending map[mcdb.ChunkPos]bool
	waiters map[mcdb.ChunkPos][]*Viewer

	closeOnce sync.Once
	closeErr  error
}

// Chunk is a loaded chunk with what is saved alongside it. It belongs to
// its region and moves between regions by pointer.
type Chunk struct {
	pos mcdb.ChunkPos
	col *chunk.Column
	net *NetChunk // nil while the network form is out of date
	// dirty means changed since it was last saved.
	dirty bool
	// discard keeps it out of the store.
	discard  bool
	encoding bool
	// version counts changes, so a stale encode is recognised.
	version uint64
}

// New opens a dimension. Call Close to save and stop it.
func New(conf Config) (*Dimension, error) {
	if conf.Log == nil {
		conf.Log = slog.Default()
	}
	if conf.Store == nil || conf.Blocks == nil || conf.Biomes == nil {
		return nil, errors.New("world: Store, Blocks and Biomes are required")
	}
	if conf.SectionShift > maxSectionShift || conf.MergeDistance < 1 {
		return nil, fmt.Errorf("world: SectionShift must be at most %d and MergeDistance at least 1", maxSectionShift)
	}
	if conf.Generator == nil {
		conf.Generator = Empty{}
	}
	if conf.Workers <= 0 {
		conf.Workers = runtime.GOMAXPROCS(0)
	}
	r, ok := conf.Dim.Range()
	if !ok {
		return nil, fmt.Errorf("world: unknown dimension %v", conf.Dim)
	}
	d := &Dimension{
		conf: conf, log: conf.Log.With("dimension", conf.Dim.String()), r: r, pool: newPool(conf.Workers),
		cache: newNetCache(conf.CacheBytes), sched: conf.Scheduler,
		sections: map[sectionPos]*Region{}, pending: map[mcdb.ChunkPos]bool{}, waiters: map[mcdb.ChunkPos][]*Viewer{},
	}
	d.idle = sync.NewCond(&d.mu)
	if d.sched == nil {
		d.sched, d.ownSched = NewScheduler(0), true
	}
	return d, nil
}

// Range is the dimension's height range.
func (d *Dimension) Range() cube.Range { return d.r }

// Close stops the regions after their current tick, runs the work queued
// for them, saves every changed chunk and waits for the store writes. The
// store itself is left open.
func (d *Dimension) Close() error {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closing = true
		for d.ticking > 0 {
			d.idle.Wait()
		}
		regions := slices.Clone(d.regions)
		d.mu.Unlock()
		for _, r := range regions {
			r.ticking.Store(true)
			r.drain()
			r.saveChanged()
			r.ticking.Store(false)
		}
		d.pool.close()
		if d.ownSched {
			d.sched.Close()
		}
		if err := d.conf.Store.Flush(); err != nil {
			d.closeErr = fmt.Errorf("world: %v: flush: %w", d.conf.Dim, err)
		}
	})
	return d.closeErr
}

// RunAt runs fn on the region that owns the chunk at pos, on its next tick
// once the chunk is loaded, loading it if needed. After Close it does
// nothing. Runs: anywhere.
func (d *Dimension) RunAt(pos mcdb.ChunkPos, fn func(*Region)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closing {
		return
	}
	o, ok := d.sections[d.sectionOf(pos)]
	if !ok {
		o = d.newRegion(time.Now())
		d.schedule(o)
	}
	o.post(entry{fn: func(r *Region) { r.runAt(pos, fn) }, pos: pos, at: true})
}

// Regions describes the dimension's regions, ordered by ID. Runs: anywhere.
func (d *Dimension) Regions() []RegionInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]RegionInfo, 0, len(d.regions))
	for _, r := range d.regions {
		info := r.info
		info.ID, info.Sections = r.id, len(r.sections)
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b RegionInfo) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (d *Dimension) sectionOf(pos mcdb.ChunkPos) sectionPos {
	return sectionPos{pos[0] >> d.conf.SectionShift, pos[1] >> d.conf.SectionShift}
}

// postAt queues fn for the region that owns the section of pos; false if
// none does.
func (d *Dimension) postAt(pos mcdb.ChunkPos, fn func(*Region)) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	o, ok := d.sections[d.sectionOf(pos)]
	if !ok {
		return false
	}
	o.post(entry{fn: fn, pos: pos, at: true})
	return true
}

// postViewer queues fn for the region v is in.
func (d *Dimension) postViewer(v *Viewer, fn func(*Region)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r := v.region; r != nil && r.state != stateDead {
		r.post(entry{fn: fn, v: v})
	}
}

// want registers v as waiting for the encoded chunk at pos and starts
// making it if nobody has.
func (d *Dimension) want(pos mcdb.ChunkPos, v *Viewer) {
	d.loadMu.Lock()
	d.waiters[pos] = append(d.waiters[pos], v)
	start := !d.pending[pos]
	d.pending[pos] = true
	d.loadMu.Unlock()
	if start && !d.postAt(pos, func(o *Region) { o.produce(pos) }) {
		d.poolLoad(pos)
	}
}

// publish caches an encoded chunk and gives it to the players waiting.
func (d *Dimension) publish(pos mcdb.ChunkPos, nc *NetChunk) {
	d.cache.put(pos, nc)
	d.loadMu.Lock()
	vs := d.waiters[pos]
	delete(d.waiters, pos)
	delete(d.pending, pos)
	d.loadMu.Unlock()
	for _, v := range vs {
		d.postViewer(v, func(*Region) { v.arrived(pos, nc) })
	}
}

// encodedUnloaded takes an encode of a chunk unloaded meanwhile: still
// right if it did not change after the snapshot, else loaded again, after
// its save on the same worker.
func (d *Dimension) encodedUnloaded(c *Chunk, nc *NetChunk, version uint64) {
	if c.version == version {
		d.publish(c.pos, nc)
		return
	}
	d.poolLoad(c.pos)
}

func (d *Dimension) poolLoad(pos mcdb.ChunkPos) {
	d.pool.do(pos, func() { d.load(pos) })
}

// load runs on a worker: it reads the chunk, or generates it, encodes it
// for the network, and hands it to the region that owns it.
func (d *Dimension) load(pos mcdb.ChunkPos) {
	col, err := d.conf.Store.LoadColumn(pos, d.conf.Dim)
	discard := false
	if err != nil {
		if !errors.Is(err, mcdb.ErrNotFound) {
			// Never save over data that failed to load: it may be readable
			// by a later version.
			d.log.Error("load chunk", "pos", pos, "err", err)
		}
		discard = !errors.Is(err, mcdb.ErrNotFound) || d.conf.DiscardGenerated
		c := chunk.New(d.conf.Blocks, d.r)
		if err := d.conf.Generator.Generate(pos, c); err != nil {
			d.log.Error("generate chunk", "pos", pos, "err", err)
		}
		col = &chunk.Column{Chunk: c, Finalisation: chunk.DefaultFinalisation()}
	} else {
		col.Chunk.ReplaceBiomes(d.resolveBiome)
	}
	nc := encodeNetChunk(pos, d.conf.Dim, col.Chunk)
	d.publish(pos, nc)
	d.postAt(pos, func(o *Region) { o.arrive(pos, col, nc, discard) })
}

// resolveBiome maps a saved biome ID to the one it loads as.
func (d *Dimension) resolveBiome(b uint32) uint32 {
	if b > math.MaxInt16 {
		return uint32(d.conf.Biomes.Resolve(-1)) //nolint:gosec // registered IDs are not negative
	}
	return uint32(d.conf.Biomes.Resolve(int16(b))) //nolint:gosec // as above
}

// chunksInRadius returns the chunk positions within r of centre, nearest
// first: BDS's view is a disc.
func chunksInRadius(dst []mcdb.ChunkPos, centre mcdb.ChunkPos, r int32) []mcdb.ChunkPos {
	dst = dst[:0]
	for x := -r; x <= r; x++ {
		for z := -r; z <= r; z++ {
			if x*x+z*z <= r*r {
				dst = append(dst, mcdb.ChunkPos{centre[0] + x, centre[1] + z})
			}
		}
	}
	slices.SortStableFunc(dst, func(a, b mcdb.ChunkPos) int {
		return int(dist2(a, centre) - dist2(b, centre))
	})
	return dst
}

func dist2(a, b mcdb.ChunkPos) int64 {
	dx, dz := int64(a[0])-int64(b[0]), int64(a[1])-int64(b[1])
	return dx*dx + dz*dz
}
