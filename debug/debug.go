// Package debug shows what the server is doing: every packet sent and
// received, how long each world tick and each part of it takes, and which
// blocks the scheduled updates (water, lava, redstone) spend that time on.
//
// A nil *Monitor is valid and does nothing, so code calls it unconditionally
// and pays almost nothing when debugging is off. Serve exposes a dashboard
// and JSON on a local HTTP address, plus Go's pprof profiler.
package debug

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/packet"
)

// Config configures a Monitor.
type Config struct {
	// Packets is how many recent packets are kept. If 0, 20000.
	Packets int
	// Ticks is how many recent ticks are kept per world. If 0, 400.
	Ticks int
	// SlowTick marks ticks that take longer. If 0, 50ms.
	SlowTick time.Duration
}

// Monitor collects packets and tick timings. It is safe for concurrent use.
type Monitor struct {
	conf  Config
	start time.Time

	pmu     sync.Mutex
	packets []Packet // ring
	pnext   int
	pfull   bool
	counts  map[string]*Count
	subs    map[chan Packet]struct{}

	tmu    sync.Mutex
	worlds map[string]*worldTicks

	regions sync.Mutex
	regionF func() any
}

// SetRegions sets the function that describes the world's regions for the
// region map. It is called on each request, from the HTTP goroutine.
func (m *Monitor) SetRegions(f func() any) {
	if m == nil {
		return
	}
	m.regions.Lock()
	m.regionF = f
	m.regions.Unlock()
}

// Regions returns the region description, or nil.
func (m *Monitor) Regions() any {
	if m == nil {
		return nil
	}
	m.regions.Lock()
	f := m.regionF
	m.regions.Unlock()
	if f == nil {
		return nil
	}
	return f()
}

// New returns a Monitor.
func New(conf Config) *Monitor {
	if conf.Packets <= 0 {
		conf.Packets = 20000
	}
	if conf.Ticks <= 0 {
		conf.Ticks = 400
	}
	if conf.SlowTick <= 0 {
		conf.SlowTick = 50 * time.Millisecond
	}
	return &Monitor{
		conf: conf, start: time.Now(), packets: make([]Packet, conf.Packets),
		counts: map[string]*Count{}, subs: map[chan Packet]struct{}{}, worlds: map[string]*worldTicks{},
	}
}

// Packet is one packet sent or received.
type Packet struct {
	Time   time.Time `json:"time"`
	In     bool      `json:"in"`
	Player string    `json:"player"`
	Addr   string    `json:"addr"`
	Name   string    `json:"name"`
	ID     uint32    `json:"id"`
	// Body is the packet's fields, cut to a few hundred characters.
	Body string `json:"body"`
	// pk is the packet itself, for full recordings.
	pk packet.Packet
}

// Count is the number of packets of one type.
type Count struct {
	Name string `json:"name"`
	In   int64  `json:"in"`
	Out  int64  `json:"out"`
}

const bodyLimit = 300

// Hooks returns connection hooks that record every packet. Pass them as
// network.Config.Hooks. A nil Monitor returns nil.
func (m *Monitor) Hooks() *conn.Hooks {
	if m == nil {
		return nil
	}
	h := &conn.Hooks{}
	h.OnPacketIn(func(e *conn.PacketEvent) conn.Verdict { m.record(e, true); return conn.Allow })
	h.OnPacketOut(func(e *conn.PacketEvent) conn.Verdict { m.record(e, false); return conn.Allow })
	return h
}

func (m *Monitor) record(e *conn.PacketEvent, in bool) {
	if e.Packet == nil {
		return
	}
	name := strings.TrimPrefix(fmt.Sprintf("%T", e.Packet), "*packet.")
	p := Packet{Time: time.Now(), In: in, Name: name, ID: e.Packet.ID(), pk: e.Packet}
	if e.Conn != nil {
		p.Player = e.Conn.IdentityData().DisplayName
		p.Addr = e.Conn.RemoteAddr().String()
	}
	body := fmt.Sprintf("%+v", e.Packet)
	if len(body) > bodyLimit {
		body = body[:bodyLimit] + "…"
	}
	p.Body = body

	m.pmu.Lock()
	m.packets[m.pnext] = p
	m.pnext++
	if m.pnext == len(m.packets) {
		m.pnext, m.pfull = 0, true
	}
	c := m.counts[name]
	if c == nil {
		c = &Count{Name: name}
		m.counts[name] = c
	}
	if in {
		c.In++
	} else {
		c.Out++
	}
	for ch := range m.subs {
		select {
		case ch <- p:
		default: // a slow recorder misses packets rather than slowing the game
		}
	}
	m.pmu.Unlock()
}

// Packets returns recent packets, newest last, that match f.
func (m *Monitor) Packets(f func(Packet) bool, limit int) []Packet {
	if m == nil {
		return nil
	}
	m.pmu.Lock()
	defer m.pmu.Unlock()
	var all []Packet
	if m.pfull {
		all = append(all, m.packets[m.pnext:]...)
	}
	all = append(all, m.packets[:m.pnext]...)
	out := make([]Packet, 0, min(limit, len(all)))
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if f == nil || f(all[i]) {
			out = append(out, all[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Counts returns how many packets of each type were sent and received.
func (m *Monitor) Counts() []Count {
	if m == nil {
		return nil
	}
	m.pmu.Lock()
	defer m.pmu.Unlock()
	out := make([]Count, 0, len(m.counts))
	for _, c := range m.counts {
		out = append(out, *c)
	}
	return out
}

// subscribe returns a channel receiving every packet until cancel.
func (m *Monitor) subscribe() (<-chan Packet, func()) {
	ch := make(chan Packet, 4096)
	m.pmu.Lock()
	m.subs[ch] = struct{}{}
	m.pmu.Unlock()
	return ch, func() {
		m.pmu.Lock()
		delete(m.subs, ch)
		m.pmu.Unlock()
	}
}
