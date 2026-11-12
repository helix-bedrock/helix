// Package sim runs the real server under heavy, made-up load and measures what it costs:
// how long each region tick takes and where the time goes, what the process allocates and
// how much CPU the garbage collector takes. It is the load test harness of PRIORITIES.md
// item L, step 1: scenarios with no network. A Scenario adds one kind of load (dropped
// items, lit TNT, falling blocks, fireworks, mobs, lingering clouds); Run starts a fresh
// server in a temporary folder, adds the load, lets it settle, measures and stops.
//
// Every number comes from the engine's own code paths: the server is helix.Server with the
// vanilla registry and systems, only with no players and no network traffic. Bots come
// later (item L step 2).
//
// cmd/sim runs scenarios from the command line:
//
//	go run ./cmd/sim -scenario tnt -n 0,100,1000
package sim

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/generator"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/mcdb"
)

const (
	// DefaultSize is the side in blocks of the square kept loaded and ticking when the
	// scenario names none: 8 by 8 chunks, one region.
	DefaultSize = 128
	// startTimeout is how long the server may take to open its world.
	startTimeout = time.Minute
	// pollEvery is how often Start checks whether the server is up.
	pollEvery = 10 * time.Millisecond
	// levelName is the world folder's name inside the temporary folder.
	levelName = "sim"
	// worldSeed keeps every run on the same world.
	worldSeed int64 = 1
)

// Config configures the server a run starts. The zero value works: a flat world, every
// core, errors only in the log.
type Config struct {
	// Log receives the server's log. Nil means errors only, to stderr: the over-budget
	// warnings a load test causes on purpose would drown the report.
	Log *slog.Logger
	// Cores is the server's TickCores: 0 means one per CPU.
	Cores int
	// WorldType is the world type by name; empty means flat, so the ground is the same
	// everywhere and generation costs nothing.
	WorldType string
	// Dir is the folder the world is made in. Empty means a new temporary folder, removed
	// when the world closes.
	Dir string
	// Warmup is how long the load runs before measuring, Measure how long it is measured.
	// Zero means DefaultWarmup and DefaultMeasure.
	Warmup, Measure time.Duration
}

// World is a running server a scenario adds its load to.
type World struct {
	// Server is the running server, Host its players and entities, Dim the overworld.
	Server *helix.Server
	Host   *player.Host
	Dim    *world.Dimension
	// Rand places the load; it is seeded the same on every run.
	Rand *rand.Rand

	size    int32
	surface float32
	ticket  *world.Ticket
	cancel  context.CancelFunc
	done    chan error
	tempDir string
}

// Start starts a server with a size by size block square around 0, 0 kept loaded and
// ticking, and waits until it is up and the square is loaded. Close stops it.
func Start(ctx context.Context, conf Config, size int32) (*World, error) {
	if size <= 0 {
		size = DefaultSize
	}
	w := &World{size: size, Rand: rand.New(rand.NewPCG(uint64(worldSeed), 0)), done: make(chan error, 1)} //nolint:gosec // a fixed layout
	dir, err := w.worldDir(conf.Dir)
	if err != nil {
		return nil, err
	}
	s, err := helix.New(serverConfig(conf, dir))
	if err != nil {
		w.removeTemp()
		return nil, fmt.Errorf("sim: new server: %w", err)
	}
	w.Server = s
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go func() { w.done <- s.Run(runCtx) }()
	if err := w.waitUp(ctx); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.holdArea(ctx); err != nil {
		_ = w.Close()
		return nil, err
	}
	return w, nil
}

// worldDir is the folder to make the world in: the configured one, or a new temporary
// folder that Close removes.
func (w *World) worldDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	tmp, err := os.MkdirTemp("", "helix-sim-")
	if err != nil {
		return "", fmt.Errorf("sim: world folder: %w", err)
	}
	w.tempDir = tmp
	return tmp, nil
}

// serverConfig is a server with no network a player could reach: offline, RakNet on a
// free loopback port (a server needs one transport), no NetherNet.
func serverConfig(conf Config, dir string) helix.Config {
	c := helix.DefaultConfig()
	c.Log = conf.Log
	if c.Log == nil {
		c.Log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	c.WorldsDir, c.LevelName = dir, levelName
	c.WorldType = conf.WorldType
	if c.WorldType == "" {
		c.WorldType = generator.NameFlat
	}
	seed := worldSeed
	c.Seed = &seed
	c.TickCores = conf.Cores
	c.OnlineMode = false
	c.NetherNet.Enabled = false
	c.RakNet = helix.RakNetConfig{Enabled: true, Address: "127.0.0.1:0"}
	return c
}

// waitUp waits until the server has opened its world and built its player host.
func (w *World) waitUp(ctx context.Context) error {
	deadline := time.After(startTimeout)
	poll := time.NewTicker(pollEvery)
	defer poll.Stop()
	for {
		if h := w.Server.Host(); h != nil && h.Overworld() != nil {
			w.Host, w.Dim = h, h.Overworld()
			return nil
		}
		select {
		case err := <-w.done:
			w.done <- err
			return fmt.Errorf("sim: server stopped while starting: %w", err)
		case <-deadline:
			return errors.New("sim: server took too long to start")
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		}
	}
}

// holdArea keeps the square loaded and ticking, as a player's simulation zone does, and
// waits for every chunk of it, then finds the ground.
func (w *World) holdArea(ctx context.Context) error {
	x0, z0, x1, z1 := w.Area()
	w.ticket = w.Dim.KeepLoaded(world.Box(x0, z0, x1, z1), true)
	for pos := range world.Box(x0, z0, x1, z1) {
		if err := w.Do(ctx, pos, func(*world.Region) {}); err != nil {
			return err
		}
	}
	return w.Do(ctx, mcdb.ChunkPos{0, 0}, func(r *world.Region) {
		if y, ok := r.Height(0, 0); ok {
			w.surface = float32(y)
		}
	})
}

// Close stops the server, saves and closes the world, and removes the temporary folder.
func (w *World) Close() error {
	if w.ticket != nil {
		w.ticket.Release()
	}
	w.cancel()
	err := <-w.done
	w.done <- err
	w.removeTemp()
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("sim: server: %w", err)
	}
	return nil
}

// removeTemp removes the temporary world folder, if Start made one.
func (w *World) removeTemp() {
	if w.tempDir != "" {
		_ = os.RemoveAll(w.tempDir)
	}
}

// Area is the square kept loaded and ticking, as block corners, both included.
func (w *World) Area() (x0, z0, x1, z1 int32) {
	half := w.size / 2
	return -half, -half, w.size - half - 1, w.size - half - 1
}

// Surface is the Y of the air just above the ground at 0, 0.
func (w *World) Surface() float32 { return w.surface }

// RandomSpot is a random point in the area, dy blocks above the ground at 0, 0.
func (w *World) RandomSpot(dy float32) mgl32.Vec3 {
	x0, z0, _, _ := w.Area()
	return mgl32.Vec3{
		float32(x0) + w.Rand.Float32()*float32(w.size),
		w.surface + dy,
		float32(z0) + w.Rand.Float32()*float32(w.size),
	}
}

// Do runs fn on the region that owns the chunk at pos, loading it, and waits for it.
func (w *World) Do(ctx context.Context, pos mcdb.ChunkPos, fn func(r *world.Region)) error {
	ran := make(chan struct{})
	w.Dim.RunAt(pos, func(r *world.Region) {
		fn(r)
		close(ran)
	})
	select {
	case <-ran:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Post runs fn on the region that owns the chunk holding pos on its next tick, without
// waiting: for spawning many things at once.
func (w *World) Post(pos mgl32.Vec3, fn func(r *world.Region)) {
	w.Dim.RunAt(chunkOf(pos), fn)
}

// Item is a stack of an item by name, for scenarios; an unknown name is an error.
func (w *World) Item(name string, count int) (player.ItemStack, error) {
	it, ok := w.Host.NewItem(name, count, 0)
	if !ok {
		return player.ItemStack{}, fmt.Errorf("sim: no item %q", name)
	}
	return it, nil
}

// chunkOf is the chunk holding pos.
func chunkOf(pos mgl32.Vec3) mcdb.ChunkPos {
	return world.ChunkPos(int32(math.Floor(float64(pos[0]))), int32(math.Floor(float64(pos[2]))))
}
