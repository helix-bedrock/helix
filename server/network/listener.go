// Package network accepts Minecraft connections for Helix over Helix's own
// protocol, raknet and nethernet libraries, and runs the login sequence
// until a player is ready to join a world.
//
// It replaces gophertunnel's minecraft.Listener and minecraft.Conn, which
// Dragonfly used; the sequence follows them.
package network

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/helix-bedrock/helix/server/resource"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/auth"
	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/login"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/raknet"
)

// ServerStatus is what the server list shows.
type ServerStatus struct {
	ServerName    string
	ServerSubName string
	PlayerCount   int
	// MaxPlayers is shown as PlayerCount+1 when 0.
	MaxPlayers int
}

// StatusProvider gives the server list entry from the current player count
// and limit.
type StatusProvider interface {
	ServerStatus(playerCount, maxPlayers int) ServerStatus
}

// StaticStatus shows a fixed name with the real player count.
type StaticStatus struct{ Name, SubName string }

// ServerStatus implements StatusProvider.
func (s StaticStatus) ServerStatus(count, limit int) ServerStatus {
	return ServerStatus{ServerName: s.Name, ServerSubName: s.SubName, PlayerCount: count, MaxPlayers: limit}
}

// Config configures a Listener.
type Config struct {
	// Log receives connection errors. If nil, nothing is logged.
	Log *slog.Logger
	// MaximumPlayers refuses logins beyond it. 0 means no limit.
	MaximumPlayers int
	// StatusProvider fills in the server list entry.
	StatusProvider StatusProvider
	// AuthenticationDisabled lets players join without Xbox Live.
	AuthenticationDisabled bool
	// ResourcePacks are sent to every player.
	ResourcePacks []*resource.Pack
	// TexturePacksRequired makes players accept the packs to join.
	TexturePacksRequired bool
	// Compression is the batch compression. If nil, flate.
	Compression packet.Compression
	// Allow may refuse a player after login, with a message.
	Allow func(addr net.Addr, d login.IdentityData, c login.ClientData) (string, bool)
	// FlushRate sends queued packets this often. If 0, 50ms.
	FlushRate time.Duration
	// Hooks run for every connection, before its own hooks: packet logging
	// and metrics attach here.
	Hooks *conn.Hooks
}

// Transport is a listener of packet transports: raknet or nethernet.
type Transport interface {
	Accept() (net.Conn, error)
	Addr() net.Addr
	Close() error
}

// setPong sets the server list answer of transports that have one: raknet
// returns an error, nethernet does not.
func setPong(t Transport, b []byte) bool {
	switch p := t.(type) {
	case interface{ PongData([]byte) error }:
		_ = p.PongData(b)
	case interface{ PongData([]byte) }:
		p.PongData(b)
	default:
		return false
	}
	return true
}

// Listener accepts players.
type Listener struct {
	conf     Config
	t        Transport
	verifier *auth.Verifier
	count    atomic.Int32
	incoming chan *Conn
	closed   chan struct{}
	once     sync.Once
}

// Listen accepts players on t.
func Listen(t Transport, conf Config) *Listener {
	if conf.Log == nil {
		conf.Log = slog.New(slog.DiscardHandler)
	}
	if conf.StatusProvider == nil {
		conf.StatusProvider = StaticStatus{Name: "Helix"}
	}
	if conf.FlushRate == 0 {
		conf.FlushRate = 50 * time.Millisecond
	}
	l := &Listener{conf: conf, t: t, verifier: auth.New(auth.Config{}),
		incoming: make(chan *Conn), closed: make(chan struct{})}
	go l.pongLoop()
	go l.accept()
	return l
}

// ListenRakNet accepts players over RakNet on address.
func ListenRakNet(address string, conf Config) (*Listener, error) {
	rl, err := raknet.ListenConfig{ErrorLog: conf.Log}.Listen(address)
	if err != nil {
		return nil, fmt.Errorf("network: listen raknet: %w", err)
	}
	return Listen(rl, conf), nil
}

// Accept returns the next player that finished logging in.
func (l *Listener) Accept() (*Conn, error) {
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Disconnect shows the player msg and closes its connection.
func (l *Listener) Disconnect(c *Conn, msg string) error {
	c.disconnect(msg)
	return c.Close()
}

// Addr returns the address listened on.
func (l *Listener) Addr() net.Addr { return l.t.Addr() }

// PlayerCount returns the number of connections, logged in or not.
func (l *Listener) PlayerCount() int { return int(l.count.Load()) }

// Close stops accepting players.
func (l *Listener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.closed)
		err = l.t.Close()
	})
	return err
}

func (l *Listener) accept() {
	for {
		nc, err := l.t.Accept()
		if err != nil {
			_ = l.Close()
			return
		}
		t, ok := nc.(conn.Transport)
		if !ok {
			l.conf.Log.Error("transport connection cannot read packets", "type", fmt.Sprintf("%T", nc))
			_ = nc.Close()
			continue
		}
		l.count.Add(1)
		c := newConn(t, l)
		if l.conf.Hooks != nil {
			c.c = conn.New(t, conn.Config{Log: c.log, FlushInterval: l.conf.FlushRate, Shared: l.conf.Hooks})
		}
		go l.login(c)
	}
}

func (l *Listener) login(c *Conn) {
	if err := c.login(); err != nil {
		if !errors.Is(err, net.ErrClosed) {
			c.log.Info("login failed", "err", err)
		}
		_ = c.Close()
		return
	}
	select {
	case l.incoming <- c:
	case <-l.closed:
		_ = c.Close()
	}
}

func (l *Listener) status() ServerStatus {
	s := l.conf.StatusProvider.ServerStatus(l.PlayerCount(), l.conf.MaximumPlayers)
	if s.MaxPlayers == 0 {
		s.MaxPlayers = s.PlayerCount + 1
	}
	return s
}

// pongLoop keeps the server list entry up to date, as gophertunnel's
// Listener.updatePongData.
func (l *Listener) pongLoop() {
	t := time.NewTicker(4 * time.Second)
	defer t.Stop()
	for {
		s := l.status()
		var port uint16
		if a, ok := l.Addr().(interface{ AddrPort() netip.AddrPort }); ok {
			port = a.AddrPort().Port()
		}
		var id int64
		if i, ok := l.t.(interface{ ID() int64 }); ok {
			id = i.ID()
		}
		if !setPong(l.t, fmt.Appendf(nil, "MCPE;%v;%v;%v;%v;%v;%v;%v;%v;%v;%v;%v;%v;%v;",
			s.ServerName, protocol.CurrentProtocol, protocol.CurrentVersion, s.PlayerCount, s.MaxPlayers,
			id, s.ServerSubName, "Creative", 1, port, port, 0, 0)) {
			return
		}
		select {
		case <-t.C:
		case <-l.closed:
			return
		}
	}
}
