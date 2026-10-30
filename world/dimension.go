// Package world is the world in memory: the chunks of each dimension,
// loaded, generated, encoded and saved on worker pools, and the two zones
// every player has (see API_DESIGN.md, "World in memory").
//
// Until regions exist (build step 4), each dimension runs one tick
// goroutine that owns its loaded chunks; regions take that over.
package world

import (
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
}

// DefaultConfig returns BDS 1.26.60's tick-distance of 4 chunks and a
// 64 MiB chunk cache.
func DefaultConfig() Config {
	return Config{SimulationDistance: 4, CacheBytes: 64 << 20}
}

// TickDuration is the length of a tick: 20 ticks a second, as BDS.
const TickDuration = 50 * time.Millisecond

const (
	// chunksPerTick is how many chunks a player may be sent or have asked
	// for per tick.
	chunksPerTick = 16
	// maxWaiting is how many chunk loads a player may have outstanding.
	maxWaiting = 64
)

// Dimension holds the loaded chunks of one dimension. Its methods are safe
// for use by many goroutines at once.
type Dimension struct {
	conf Config
	log  *slog.Logger
	r    cube.Range
	pool *pool

	inMu sync.Mutex
	in   []func()

	stop    chan struct{}
	stopped chan struct{}
	once    sync.Once

	// Owned by the tick goroutine.
	chunks  map[mcdb.ChunkPos]*Chunk
	holds   map[mcdb.ChunkPos]int
	loading map[mcdb.ChunkPos]bool
	viewers map[*Viewer]struct{}
	cache   *netCache
}

// Chunk is a loaded chunk with what is saved alongside it. It belongs to
// the tick goroutine.
type Chunk struct {
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

// New opens a dimension and starts its tick goroutine. Call Close to save
// and stop it.
func New(conf Config) (*Dimension, error) {
	if conf.Log == nil {
		conf.Log = slog.Default()
	}
	if conf.Store == nil || conf.Blocks == nil || conf.Biomes == nil {
		return nil, errors.New("world: Store, Blocks and Biomes are required")
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
		stop: make(chan struct{}), stopped: make(chan struct{}),
		chunks: map[mcdb.ChunkPos]*Chunk{}, holds: map[mcdb.ChunkPos]int{}, loading: map[mcdb.ChunkPos]bool{},
		viewers: map[*Viewer]struct{}{}, cache: newNetCache(conf.CacheBytes),
	}
	go d.run()
	return d, nil
}

// Range is the dimension's height range.
func (d *Dimension) Range() cube.Range { return d.r }

// Close saves every changed chunk, waits for the store writes and stops
// the dimension. The store itself is left open.
func (d *Dimension) Close() error {
	d.once.Do(func() { close(d.stop) })
	<-d.stopped
	d.pool.close()
	if err := d.conf.Store.Flush(); err != nil {
		return fmt.Errorf("world: %v: flush: %w", d.conf.Dim, err)
	}
	return nil
}

// post runs fn on the tick goroutine at the start of the next tick.
func (d *Dimension) post(fn func()) {
	d.inMu.Lock()
	d.in = append(d.in, fn)
	d.inMu.Unlock()
}

func (d *Dimension) run() {
	defer close(d.stopped)
	t := time.NewTicker(TickDuration)
	defer t.Stop()
	var in []func()
	for {
		select {
		case <-d.stop:
			d.drain(in)
			d.saveChanged()
			return
		case <-t.C:
		}
		in = d.drain(in)
		for v := range d.viewers {
			d.updateViewer(v)
		}
		d.saveChanged()
	}
}

// drain runs what other goroutines posted. It reuses in's memory.
func (d *Dimension) drain(in []func()) []func() {
	d.inMu.Lock()
	in, d.in = d.in, in[:0]
	d.inMu.Unlock()
	for i, fn := range in {
		fn()
		in[i] = nil
	}
	return in
}

// netChunk returns the encoded chunk at pos if one is ready.
func (d *Dimension) netChunk(pos mcdb.ChunkPos) *NetChunk {
	if c, ok := d.chunks[pos]; ok {
		if c.net == nil && !c.encoding {
			d.encode(pos, c)
		}
		return c.net
	}
	return d.cache.get(pos)
}

// request loads the chunk at pos if it is not loaded or loading.
func (d *Dimension) request(pos mcdb.ChunkPos) {
	if _, ok := d.chunks[pos]; ok || d.loading[pos] {
		return
	}
	d.loading[pos] = true
	d.pool.do(pos, func() { d.load(pos) })
}

// load runs on a worker: it reads the chunk, or generates it, and encodes
// it for the network.
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
	d.post(func() { d.loaded(pos, col, nc, discard) })
}

// resolveBiome maps a saved biome ID to the one it loads as.
func (d *Dimension) resolveBiome(b uint32) uint32 {
	if b > math.MaxInt16 {
		return uint32(d.conf.Biomes.Resolve(-1)) //nolint:gosec // registered IDs are not negative
	}
	return uint32(d.conf.Biomes.Resolve(int16(b))) //nolint:gosec // as above
}

// loaded takes a chunk a worker loaded: it is kept while a simulation zone
// holds it, cached, and sent to the players waiting for it.
func (d *Dimension) loaded(pos mcdb.ChunkPos, col *chunk.Column, nc *NetChunk, discard bool) {
	delete(d.loading, pos)
	d.cache.put(pos, nc)
	if d.holds[pos] > 0 {
		d.chunks[pos] = &Chunk{col: col, net: nc, discard: discard}
	}
	for v := range d.viewers {
		v.arrived(pos, nc)
	}
}

// encode rebuilds the network form of a changed chunk on a worker, from a
// snapshot, so the tick never waits for it.
func (d *Dimension) encode(pos mcdb.ChunkPos, c *Chunk) {
	c.encoding = true
	snap, version := c.col.Chunk.Snapshot(), c.version
	d.pool.do(pos, func() {
		nc := encodeNetChunk(pos, d.conf.Dim, snap)
		d.post(func() {
			cur, ok := d.chunks[pos]
			switch {
			case !ok:
				// Unloaded meanwhile: load it again, after its save on the
				// same worker, for the players still waiting.
				d.request(pos)
				return
			case cur != c:
				return // reloaded meanwhile, with its own network form
			case c.version != version:
				d.encode(pos, c) // changed again: encode the newer state
				return
			}
			c.encoding, c.net = false, nc
			d.cache.put(pos, nc)
			for v := range d.viewers {
				v.arrived(pos, nc)
			}
		})
	})
}

// hold keeps the chunk at pos loaded for a simulation zone.
func (d *Dimension) hold(pos mcdb.ChunkPos) {
	d.holds[pos]++
	if d.holds[pos] == 1 {
		d.request(pos)
	}
}

// release drops a simulation zone's hold; the last one unloads the chunk,
// saving it first if it changed.
func (d *Dimension) release(pos mcdb.ChunkPos) {
	d.holds[pos]--
	if d.holds[pos] > 0 {
		return
	}
	delete(d.holds, pos)
	if c, ok := d.chunks[pos]; ok {
		d.save(pos, c)
		delete(d.chunks, pos)
	}
}

// saveChanged saves every chunk changed this tick. Only the sub chunks
// changed since the last save are copied: the snapshot shares the rest.
func (d *Dimension) saveChanged() {
	for pos, c := range d.chunks {
		d.save(pos, c)
	}
}

func (d *Dimension) save(pos mcdb.ChunkPos, c *Chunk) {
	if !c.dirty || c.discard {
		return
	}
	c.dirty = false
	snap := *c.col
	snap.Chunk = c.col.Chunk.Snapshot()
	d.pool.do(pos, func() {
		if err := d.conf.Store.StoreColumn(pos, d.conf.Dim, &snap); err != nil {
			d.log.Error("save chunk", "pos", pos, "err", err)
		}
	})
}

// setBlock changes a block of a loaded chunk; false if it is not loaded.
// Tick goroutine only. The block API comes with regions.
func (d *Dimension) setBlock(pos cube.Pos, layer uint8, block uint32) bool {
	cp := mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)} //nolint:gosec // block X and Z fit int32 chunks
	c, ok := d.chunks[cp]
	if !ok || pos[1] < d.r[0] || pos[1] > d.r[1] {
		return false
	}
	c.col.Chunk.SetBlock(uint8(pos[0]&15), int16(pos[1]), uint8(pos[2]&15), layer, block) //nolint:gosec // masked and range-checked
	c.dirty, c.net = true, nil
	c.version++
	d.cache.drop(cp)
	return true
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
