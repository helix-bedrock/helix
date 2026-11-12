package player

import (
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// global is the global owner (RFC 0002): one goroutine at 20 TPS that never
// splits and owns the server-wide state. So far that is the player list.
// Everything else reaches it through run, and reads the list through a
// copy published after every change.
type global struct {
	mu   sync.Mutex
	in   []func()
	stop chan struct{}
	done chan struct{}
	once sync.Once

	// Owned by the global goroutine.
	players map[uuid.UUID]*Player
	order   []*Player // in the order they joined

	list atomic.Pointer[[]*Player]

	// panicked is told of a panic in the loop, which it recovers.
	panicked func(v any, stack []byte)

	// tick runs on the global goroutine every tick after the queued work, if set before the
	// first player joins.
	tick func()
}

func newGlobal(panicked func(v any, stack []byte)) *global {
	g := &global{panicked: panicked, stop: make(chan struct{}), done: make(chan struct{}), players: map[uuid.UUID]*Player{}}
	g.list.Store(&[]*Player{})
	go g.loop()
	return g
}

// run queues fn for the global owner's next tick. Runs: anywhere.
func (g *global) run(fn func()) {
	g.mu.Lock()
	g.in = append(g.in, fn)
	g.mu.Unlock()
}

// call runs fn on the global owner and waits for it. Never from a region
// or the global owner itself.
func (g *global) call(fn func()) {
	done := make(chan struct{})
	g.run(func() { fn(); close(done) })
	select {
	case <-done:
	case <-g.done:
	}
}

// close runs what is queued and stops the global owner.
func (g *global) close() {
	g.once.Do(func() { close(g.stop) })
	<-g.done
}

func (g *global) loop() {
	defer close(g.done)
	defer func() {
		if v := recover(); v != nil {
			g.panicked(v, debug.Stack())
		}
	}()
	t := time.NewTicker(world.TickDuration)
	defer t.Stop()
	var in []func()
	for {
		stopping := false
		select {
		case <-g.stop:
			stopping = true
		case <-t.C:
		}
		g.mu.Lock()
		in, g.in = g.in, in[:0]
		g.mu.Unlock()
		for i, fn := range in {
			fn()
			in[i] = nil
		}
		if g.tick != nil && !stopping {
			g.tick()
		}
		if stopping {
			return
		}
	}
}

// add puts p on the player list. Global owner only.
func (g *global) add(p *Player) {
	g.players[p.uuid] = p
	g.order = append(g.order, p)
	g.publish()
}

// remove takes p off the player list. Global owner only.
func (g *global) remove(p *Player) {
	if g.players[p.uuid] != p {
		return
	}
	delete(g.players, p.uuid)
	for i, o := range g.order {
		if o == p {
			g.order = append(g.order[:i:i], g.order[i+1:]...)
			break
		}
	}
	g.publish()
}

// join tells every other player on the list of p, as BDS broadcasts PlayerList before
// the joining player gets StartGame. Global owner only.
func (g *global) join(p *Player) {
	pk := &packet.PlayerList{Entries: []protocol.PlayerListEntry{p.listEntry()}}
	for _, o := range g.order {
		if o != p {
			o.sendNow(pk)
		}
	}
}

// leave takes p off the player list and tells everyone left. Global owner only.
func (g *global) leave(p *Player) {
	g.remove(p)
	pk := &packet.PlayerList{Entries: []protocol.PlayerListEntry{{ActionType: protocol.PlayerListActionRemove, UUID: p.uuid}}}
	for _, o := range g.order {
		o.sendNow(pk)
	}
}

// listPacket is the whole player list as one PlayerList, for a player joining. Runs:
// anywhere.
func (g *global) listPacket() *packet.PlayerList {
	online := g.online()
	pk := &packet.PlayerList{Entries: make([]protocol.PlayerListEntry, 0, len(online))}
	for _, o := range online {
		pk.Entries = append(pk.Entries, o.listEntry())
	}
	return pk
}

func (g *global) publish() {
	list := append([]*Player(nil), g.order...)
	g.list.Store(&list)
}

// online returns the players online, as of the global owner's last change.
// Runs: anywhere.
func (g *global) online() []*Player { return *g.list.Load() }
