package sim

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
)

const (
	// joinAtOnce is how many bots log in at the same time.
	joinAtOnce = 32
	// botSeed seeds bot i's random source with botSeed + i.
	botSeed = 1000
)

// BotFunc is what bot i does on its tick: it may move the bot (Bot.MoveTo) and returns the
// block actions to send with this tick's input. It runs on the bot's own goroutine.
type BotFunc func(w *World, b *Bot, i, tick int) []protocol.PlayerBlockAction

// bots are the bots of a run and the goroutines driving them.
type bots struct {
	list []*Bot
	lat  latencies
	stop chan struct{}
	wg   sync.WaitGroup
	// joined is how long the logins took, all bots together.
	joined time.Duration
}

// joinBots logs n bots in, joinAtOnce at a time, and gives them the game mode.
func (w *World) joinBots(ctx context.Context, n int, mode int32) (*bots, error) {
	bs := &bots{list: make([]*Bot, n), stop: make(chan struct{})}
	start := time.Now()
	sem := make(chan struct{}, joinAtOnce)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			b, err := newBot(ctx, w.memory, fmt.Sprintf("bot%d", i), uint64(botSeed+i), &bs.lat) //nolint:gosec // small
			if err != nil {
				errs <- err
				return
			}
			bs.list[i] = b
		}()
	}
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		bs.close()
		return nil, err
	}
	bs.joined = time.Since(start)
	return bs, w.setGameMode(ctx, bs.list, mode)
}

// setGameMode gives every bot's player the game mode, on the owner of its entity.
func (w *World) setGameMode(ctx context.Context, list []*Bot, mode int32) error {
	for _, b := range list {
		p, ok := w.Host.FindName(b.Name)
		if !ok {
			return fmt.Errorf("sim: %s is not online", b.Name)
		}
		if err := w.Do(ctx, chunkOf(p.State().Position), func(*world.Region) { p.SetGameMode(mode) }); err != nil {
			return err
		}
	}
	return nil
}

// drive starts one goroutine per bot sending its input every 50 ms, as a client does.
func (bs *bots) drive(w *World, fn BotFunc) {
	for i, b := range bs.list {
		bs.wg.Add(1)
		go bs.run(w, b, i, fn)
	}
}

// run is bot i's client tick loop.
func (bs *bots) run(w *World, b *Bot, i int, fn BotFunc) {
	defer bs.wg.Done()
	t := time.NewTicker(world.TickDuration)
	defer t.Stop()
	for tick := 0; ; tick++ {
		last := b.pos
		var actions []protocol.PlayerBlockAction
		if fn != nil {
			actions = fn(w, b, i, tick)
		}
		if b.Input(last, actions) != nil {
			return
		}
		select {
		case <-bs.stop:
			return
		case <-t.C:
		}
	}
}

// moves is the movement packets all bots received so far.
func (bs *bots) moves() int64 {
	var n int64
	for _, b := range bs.list {
		n += b.moves.Load()
	}
	return n
}

// close stops the bots and disconnects them.
func (bs *bots) close() {
	select {
	case <-bs.stop:
	default:
		close(bs.stop)
	}
	bs.wg.Wait()
	for _, b := range bs.list {
		if b != nil {
			_ = b.Close()
		}
	}
}

// spotOf is bot i's place on a grid over the area, cell blocks apart, at the ground.
func (w *World) spotOf(i, cell int) mgl32.Vec3 {
	x0, z0, _, _ := w.Area()
	perRow := max(int(w.size)/cell, 1)
	x, z := i%perRow, i/perRow%perRow
	return mgl32.Vec3{float32(x0) + float32(x*cell) + float32(cell)/2, w.Surface(), float32(z0) + float32(z*cell) + float32(cell)/2}
}

// creative is the game mode bots get when a scenario asks for it.
const creative = player.GameModeCreative

// The methods below take a nil *bots, for runs without bots.

// movesSoFar is moves, 0 without bots.
func (bs *bots) movesSoFar() int64 {
	if bs == nil {
		return 0
	}
	return bs.moves()
}

// measure starts or stops keeping action delays.
func (bs *bots) measure(on bool) {
	if bs != nil {
		bs.lat.setOn(on)
	}
}

// closeIfAny closes the bots, if there are any.
func (bs *bots) closeIfAny() {
	if bs != nil {
		bs.close()
	}
}

// addTo fills in the bot numbers of a report: moves were received while measuring.
func (bs *bots) addTo(r *Report, moves int64) {
	if bs == nil {
		return
	}
	r.Bots, r.JoinTime = len(bs.list), bs.joined
	r.ActionMedian, r.ActionP99, r.ActionWorst, r.Actions = bs.lat.spread()
	r.MovesPerBot = float64(moves) / float64(len(bs.list)) / r.Measured.Seconds()
}
