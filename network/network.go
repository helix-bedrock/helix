// Package network accepts Bedrock players over RakNet and NetherNet and runs
// the login sequence the way the Bedrock Dedicated Server 1.26.60 does, up to
// the point where the player is ready to be sent a world.
//
// The login is a list of named steps (see [DefaultSteps]). A server may
// replace any step, remove it, or add its own before or after it, such as
// an allow list or a server-full check. A step refuses a player by returning
// a [Refusal], which sends the player the disconnect reason BDS sends, so the
// client shows its own translated message.
package network

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/login"
	"github.com/helix-bedrock/protocol/packet"
)

// Transport is a listener of one kind of connection, such as RakNet or
// NetherNet. [RakNet] and [NetherNet] make the built-in ones.
type Transport interface {
	// Accept waits for the next connection. Connections must implement
	// conn.Transport (whole batches per read).
	Accept() (net.Conn, error)
	// Addr is the address listened on.
	Addr() net.Addr
	// SetPong sets the answer to server list pings, in the RakNet pong
	// format.
	SetPong(b []byte)
	// Close stops the transport.
	Close() error
}

// Config configures a [Listener]. Start from [DefaultConfig].
type Config struct {
	// Log receives connection events. If nil, nothing is logged.
	Log *slog.Logger
	// Steps is the login sequence. If nil, DefaultSteps().
	Steps Steps
	// Verifier checks Xbox Live tokens (an *auth.Service). If nil, the server
	// is offline: identities are read without verification.
	Verifier login.TokenVerifier
	// AllowLegacyChain accepts the old certificate chain login.
	AllowLegacyChain bool
	// HandshakeKey is the P-384 key that signs every encryption handshake;
	// BDS holds one key for the whole server too. If nil, one is generated.
	HandshakeKey *ecdsa.PrivateKey
	// Compression is the batch compression. If nil, flate (BDS's "zlib").
	Compression packet.Compression
	// CompressionThreshold is the smallest batch compressed. BDS's default
	// is 1.
	CompressionThreshold uint16
	// MaxBatchSize is the largest decompressed batch a client may send.
	// BDS allows 10 MiB.
	MaxBatchSize int
	// ShieldID is the shield's item network ID. Item stacks of the shield
	// carry an extra field, so packets with items cannot be read or written
	// right without it. The server sets it from its item registry.
	ShieldID int32
	// ReadTimeout is how long the login waits for each packet from the
	// client before giving up. 0 waits forever.
	ReadTimeout time.Duration
	// MaxLogins is how many logins may run at once; more wait. It bounds
	// the CPU and memory a flood of connections can take. 0 means no limit.
	MaxLogins int
	// FlushInterval sends queued packets this often. 0 sends only on Flush.
	FlushInterval time.Duration
	// Hooks run for every connection: packet logging and metrics attach
	// here. May be nil.
	Hooks *conn.Hooks
	// Packs are the resource packs sent to every player.
	Packs []Pack
	// PacksRequired makes players accept the packs to join.
	PacksRequired bool
	// Experiments are sent in the resource pack stack.
	Experiments []protocol.ExperimentData
}

// DefaultConfig returns the configuration BDS 1.26.60 uses by default, where
// it is known: zlib compression from 1 byte, batches up to 10 MiB.
func DefaultConfig() Config {
	return Config{
		CompressionThreshold: 1,
		MaxBatchSize:         10 << 20,
		ReadTimeout:          30 * time.Second,
		MaxLogins:            128,
		FlushInterval:        50 * time.Millisecond,
	}
}

// Listener runs the login sequence for players from any number of
// transports and hands over the ones that finish it. It is safe for use by
// many goroutines at once.
type Listener struct {
	conf  Config
	log   *slog.Logger
	steps Steps
	sem   chan struct{}

	incoming chan *Conn
	closed   chan struct{}
	// ctx is cancelled on Close; logins in progress stop with it.
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	wg     sync.WaitGroup

	mu         sync.Mutex
	transports []Transport
	pong       []byte
}

// New returns a Listener. It accepts nothing until [Listener.Serve].
func New(conf Config) (*Listener, error) {
	if conf.Log == nil {
		conf.Log = slog.New(slog.DiscardHandler)
	}
	if conf.Compression == nil {
		conf.Compression = packet.FlateCompression
	}
	if conf.HandshakeKey == nil {
		k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("network: handshake key: %w", err)
		}
		conf.HandshakeKey = k
	}
	if conf.HandshakeKey.Curve != elliptic.P384() {
		return nil, errors.New("network: handshake key must be P-384")
	}
	steps := conf.Steps
	if steps == nil {
		steps = DefaultSteps()
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &Listener{conf: conf, log: conf.Log, steps: steps, incoming: make(chan *Conn), closed: make(chan struct{}), ctx: ctx, cancel: cancel}
	if conf.MaxLogins > 0 {
		l.sem = make(chan struct{}, conf.MaxLogins)
	}
	return l, nil
}

// Serve accepts connections from t until t or the Listener is closed. The
// Listener closes t when it closes.
func (l *Listener) Serve(t Transport) {
	l.mu.Lock()
	select {
	case <-l.closed:
		l.mu.Unlock()
		_ = t.Close()
		return
	default:
	}
	l.transports = append(l.transports, t)
	if l.pong != nil {
		t.SetPong(l.pong)
	}
	l.mu.Unlock()
	l.wg.Add(1)
	go l.accept(t)
}

// SetStatus sets what the server list shows on every transport.
func (l *Listener) SetStatus(s Status) {
	b := s.pong()
	l.mu.Lock()
	l.pong = b
	ts := append([]Transport(nil), l.transports...)
	l.mu.Unlock()
	for _, t := range ts {
		t.SetPong(b)
	}
}

// Accept returns the next player that finished the login sequence.
func (l *Listener) Accept() (*Conn, error) {
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close stops every transport and waits for their accept loops. Players
// still logging in are disconnected; players already accepted are not.
func (l *Listener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.closed)
		l.cancel()
		l.mu.Lock()
		ts := l.transports
		l.mu.Unlock()
		for _, t := range ts {
			err = errors.Join(err, t.Close())
		}
		l.wg.Wait()
	})
	return err
}

func (l *Listener) accept(t Transport) {
	defer l.wg.Done()
	for {
		nc, err := t.Accept()
		if err != nil {
			select {
			case <-l.closed:
			default:
				l.log.Error("transport stopped", "addr", t.Addr(), "err", err)
			}
			return
		}
		ct, ok := nc.(conn.Transport)
		if !ok {
			l.log.Error("connection does not carry whole batches", "type", fmt.Sprintf("%T", nc))
			_ = nc.Close()
			continue
		}
		l.wg.Add(1)
		go l.login(ct)
	}
}

// login runs the steps for one connection and hands it over.
func (l *Listener) login(t conn.Transport) {
	defer l.wg.Done()
	if l.sem != nil {
		select {
		case l.sem <- struct{}{}:
			defer func() { <-l.sem }()
		case <-l.closed:
			_ = t.Close()
			return
		}
	}
	log := l.log.With("addr", t.RemoteAddr())
	c := conn.New(t, conn.Config{Log: log, FlushInterval: l.conf.FlushInterval, Shared: l.conf.Hooks, MaxBatchSize: l.conf.MaxBatchSize, ShieldID: l.conf.ShieldID})
	// Closing the Listener stops a read the login is blocked in.
	stop := context.AfterFunc(l.ctx, func() { _ = t.SetReadDeadline(time.Now()) })
	defer stop()

	lg := &Login{l: l, c: c, log: log}
	for _, st := range l.steps {
		if err := st.Run(l.ctx, lg); err != nil {
			lg.fail(st.Name, err)
			_ = c.Close()
			return
		}
	}
	_ = c.Transport().SetReadDeadline(time.Time{})
	pc := &Conn{c: c, login: lg}
	select {
	case l.incoming <- pc:
	case <-l.closed:
		pc.Disconnect(packet.DisconnectReasonShutdown, "")
		_ = c.Close()
	}
}
