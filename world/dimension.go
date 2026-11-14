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
	"sync/atomic"
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
	// ShieldID is the shield's item runtime ID, which encoding packets with items needs
	// (the movement fan-out encodes each packet once for every player).
	ShieldID int32
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
	// Light says how blocks give and take light. If nil, the registry's values,
	// which are BDS's (RegistryLight). Read when the dimension is made.
	Light LightRules
	// Sky says whether the dimension has sky light: by default the overworld only.
	Sky SkyMode
	// SectionShift sets the size of a section, 1<<SectionShift chunks a
	// side, at most 10.
	SectionShift uint
	// MergeDistance is how close, in sections, two regions may come before
	// they merge, at least 1. Regions are always at least that many empty
	// sections apart, so nothing in one reaches the other within a tick.
	MergeDistance int32
	// EntityViewRadius2 is the square of the radius, in chunks, within which
	// a player sees entities: every chunk with dx*dx+dz*dz at most this.
	// It is separate from the view and tick distances, as in BDS. It must
	// stay within the gap regions keep between them (MergeDistance
	// sections), so no player misses an entity in another region.
	EntityViewRadius2 int32
	// RandomTickSpeed is BDS's randomtickspeed game rule: how many random
	// ticks each sub chunk gets per tick, 2.5 at speed 1. 0 turns random
	// ticks off.
	RandomTickSpeed int32
	// Ticker gives blocks their random and queued ticks. Nil ticks nothing,
	// though scheduled ticks are still kept and saved.
	Ticker BlockTicker
	// AutoSaveTicks is how often every loaded chunk also saves its entities and
	// tick queues, which a tick's block changes do not save, so a crash or a
	// kill loses at most this much of them. Each chunk takes its turn at its
	// own point in the interval, so the saves spread over the ticks. 0 is
	// DefaultAutoSaveTicks; below 0 they are saved only when a chunk unloads
	// and on Close. Helix's own: BDS does not save like this.
	AutoSaveTicks int32
	// SaveDelayTicks is the least time between two saves of a chunk whose blocks
	// keep changing: the first change saves at the end of its tick, later ones wait
	// until this many ticks have passed, so a machine changing a chunk every tick
	// does not save it, block entities and all, 20 times a second. A crash loses at
	// most this much of such a chunk. Unloading, Close and the auto-save always
	// save. 0 is DefaultSaveDelayTicks; below 0 saves every tick. Helix's own: BDS
	// does not save like this.
	SaveDelayTicks int32
	// Weather runs on every ticking chunk before its random ticks, as BDS's chunk tick runs
	// lightning, freezing and snow. Nil has no weather.
	Weather WeatherTicker
	// Spawner runs on every ticking chunk after its block ticks: BDS's natural mob spawner,
	// which BDS 1.26.60 runs once per ticking chunk per tick. Nil spawns nothing.
	Spawner ChunkSpawner
	// Settler queues the ticks a chunk needs to settle when it loads, generated or saved,
	// such as liquid generation left beside open space. Nil queues none.
	Settler Settler
	// ParallelMin is how many items a phase of Region.Parallel (mob AI and the like) needs
	// before a crowded region spreads it over idle cores; 0 means DefaultParallelMin.
	ParallelMin int
	// ParallelHelpers is how many goroutines at most help one region's phase besides its
	// own; 0 means DefaultParallelHelpers and below 0 keeps every region on one core.
	ParallelHelpers int
}

// DefaultConfig returns BDS 1.26.60's tick-distance of 4 chunks and its
// entity view disc (squared radius 17, 57 chunks), a 64 MiB chunk cache,
// sections of 8 by 8 chunks, a merge distance of one section and BDS's
// random tick speed of 1. The section size and merge distance were picked
// with BenchmarkRegionLayout: of the layouts the entity view disc allows,
// it had the fewest slow ticks overall, split towns into the most regions
// and made no regions by merges and splits.
func DefaultConfig() Config {
	return Config{SimulationDistance: 4, CacheBytes: 64 << 20, SectionShift: 3, MergeDistance: 1, EntityViewRadius2: 17, RandomTickSpeed: DefaultRandomTickSpeed}
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
	light    *lightTable
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
	// viewers are every player's zones in the dimension, so a block change
	// reaches the players in other regions who were sent its chunk.
	viewers   map[*Viewer]struct{}
	ticketSeq uint64
	ticking   int
	closing   bool

	// parallelMin and parallelHelpers are SetParallel's settings.
	parallelMin     atomic.Int64
	parallelHelpers atomic.Int64

	// loadMu guards the players waiting for encoded chunks.
	loadMu  sync.Mutex
	pending map[mcdb.ChunkPos]bool
	waiters map[mcdb.ChunkPos][]*Viewer

	// entityView is the live EntityViewRadius2.
	entityView atomic.Int32
	// randomTickSpeed is the live RandomTickSpeed.
	randomTickSpeed atomic.Int32
	// entityLoader turns saved entities into live ones.
	entityLoader atomic.Pointer[EntityLoader]
	// blockEntities makes block entities; listenerStates marks the states whose block entity
	// hears game events, made with it.
	blockEntities  atomic.Pointer[BlockEntities]
	listenerStates atomic.Pointer[[]bool]
	// tickObserver is SetTickObserver's function.
	tickObserver atomic.Pointer[TickObserver]

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
	// saveWait is how many more ticks a changed chunk waits before its next save
	// (Config.SaveDelayTicks).
	saveWait int32
	// discard keeps it out of the store.
	discard  bool
	encoding bool
	// version counts changes, so a stale encode is recognised.
	version uint64
	// ticks and randomTicks are the chunk's tick queues; ticksDirty means
	// they changed since the chunk was last saved, saved with its next
	// save or when it unloads.
	ticks, randomTicks tickQueue
	ticksDirty         bool
	// savedLive means the store holds live entities for this chunk, so it
	// must be saved when it unloads even if none are left in it.
	savedLive bool
	// blockEntities are the chunk's block entities, tickingEntities the
	// positions of those that tick, in the order they came.
	blockEntities   map[cube.Pos]BlockEntity
	tickingEntities []cube.Pos
	// listeners are the positions of those that hear game events, in the order they came.
	listeners []cube.Pos
	// tickedAt is the region pass (Region.ticks) the chunk last ticked in, neverTicked if it
	// has not ticked since it loaded. It counts the region's own ticks, not the world tick:
	// the world tick follows the wall clock, so a region tick that runs late would skip a
	// number and every chunk would look unticked. Rebased when the chunk changes region.
	tickedAt int64
	// plannedAt is the region pass its random ticks were last planned for, plan their index
	// in Region.plans (randomplan.go).
	plannedAt int64
	plan      int
	// light is the chunk's sky and block light and its height map, worked out on load.
	light *chunkLight
	// settle is the edge blocks the Settler kept for the chunk's ticks, nil once decided;
	// settleTries counts the ticks they were asked about.
	settle      []chunk.ScheduledBlockUpdate
	settleTries int
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
		viewers: map[*Viewer]struct{}{},
	}
	d.light = newLightTable(conf)
	if err := d.SetEntityViewRadius2(conf.EntityViewRadius2); err != nil {
		return nil, err
	}
	d.SetRandomTickSpeed(conf.RandomTickSpeed)
	pmin, helpers := conf.ParallelMin, conf.ParallelHelpers
	if pmin <= 0 {
		pmin = DefaultParallelMin
	}
	if helpers == 0 {
		helpers = DefaultParallelHelpers
	}
	d.SetParallel(pmin, helpers)
	d.idle = sync.NewCond(&d.mu)
	if d.sched == nil {
		d.sched, d.ownSched = NewScheduler(0), true
	}
	return d, nil
}

// SetEntityViewRadius2 changes how far players see entities (see
// Config.EntityViewRadius2), from the next tick on. Safe from any
// goroutine.
func (d *Dimension) SetEntityViewRadius2(r2 int32) error {
	gap := int64(d.conf.MergeDistance) << d.conf.SectionShift
	if r2 < 0 || int64(r2) > gap*gap {
		return fmt.Errorf("world: EntityViewRadius2 must be between 0 and %d, the gap between regions squared", gap*gap)
	}
	d.entityView.Store(r2)
	return nil
}

// SetRandomTickSpeed changes the random tick speed (see
// Config.RandomTickSpeed) from the next tick on; negative is taken as 0.
// Safe from any goroutine.
func (d *Dimension) SetRandomTickSpeed(speed int32) {
	d.randomTickSpeed.Store(max(speed, 0))
}

// CurrentTick returns the world's tick, from the dimension's scheduler.
// Runs: anywhere.
func (d *Dimension) CurrentTick() int64 { return d.sched.CurrentTick() }

// Range is the dimension's height range.
func (d *Dimension) Range() cube.Range { return d.r }

// SimulationDistance is the radius in chunks of each player's simulation zone (BDS's
// tick-distance). Safe from any goroutine.
func (d *Dimension) SimulationDistance() int32 { return d.conf.SimulationDistance }

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
			r.saveChanged(true)
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
	now := time.Now()
	for _, r := range d.regions {
		info := r.info
		info.ID, info.Sections = r.id, len(r.sections)
		info.TPS, info.MSPT = r.meter.TPS(now), r.meter.MSPT()
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

// postArrival is postAt for a loaded chunk joining its region.
func (d *Dimension) postArrival(pos mcdb.ChunkPos, fn func(*Region)) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	o, ok := d.sections[d.sectionOf(pos)]
	if !ok {
		return false
	}
	o.post(entry{fn: fn, pos: pos, at: true, arrival: true})
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
	d.pool.do(pos, func() {
		defer d.sched.recovered(d.log)
		d.load(pos)
	})
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
		// A generated chunk still needs its population (BDS's finalisation 1): its first tick
		// gives it its first mobs, then marks it done.
		col = &chunk.Column{Chunk: chunk.New(d.conf.Blocks, d.r), Finalisation: chunk.FinalisationNeedsPopulation}
		if err := d.generate(pos, col); err != nil {
			d.log.Error("generate chunk", "pos", pos, "err", err)
		}
	} else {
		col.Chunk.ReplaceBiomes(d.resolveBiome)
	}
	settle := d.settleChunk(pos, col)
	entities := make([]chunk.BlockEntity, 0, len(col.BlockEntities))
	for _, be := range col.BlockEntities {
		entities = append(entities, chunk.BlockEntity{Pos: be.Pos, Data: savedNetworkData(be.Data)})
	}
	nc := encodeNetChunk(pos, d.conf.Dim, col.Chunk, entities)
	d.publish(pos, nc)
	light := d.lightColumn(col.Chunk)
	d.postArrival(pos, func(o *Region) { o.arrive(pos, col, nc, discard, light, settle) })
}

// tickSafely is runTick with a panic recovered and passed to the scheduler's handler.
func (d *Dimension) tickSafely(r *Region) {
	defer d.sched.recovered(d.log)
	d.runTick(r)
}

// generate fills a new column with the dimension's generator, recovering a panic in it so one
// bad chunk does not stop the server.
func (d *Dimension) generate(pos mcdb.ChunkPos, col *chunk.Column) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("generator panicked: %v", r)
		}
	}()
	if g, ok := d.conf.Generator.(ColumnGenerator); ok {
		return g.GenerateColumn(pos, col)
	}
	return d.conf.Generator.Generate(pos, col.Chunk)
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
