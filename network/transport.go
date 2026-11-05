package network

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/helix-bedrock/nethernet"
	"github.com/helix-bedrock/nethernet/discovery"
	"github.com/helix-bedrock/nethernet/endpoint"
	"github.com/helix-bedrock/nethernet/signaling"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/raknet"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// RakNet listens for RakNet connections on a UDP address.
func RakNet(address string, log *slog.Logger) (Transport, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	l, err := raknet.ListenConfig{ErrorLog: log}.Listen(address)
	if err != nil {
		return nil, fmt.Errorf("network: listen raknet: %w", err)
	}
	return rakNetTransport{l}, nil
}

type rakNetTransport struct{ *raknet.Listener }

func (t rakNetTransport) SetPong(b []byte) { _ = t.PongData(b) }

// NetherNetConfig configures a NetherNet transport: HTTP signaling on a TCP
// address, then WebRTC over UDP, as BDS 1.26.60 does by default.
type NetherNetConfig struct {
	// Address is the TCP address of the HTTP signaling endpoint.
	Address string
	// IdentityKey is the server identity key (see nethernet.LoadIdentityKey).
	// Players are asked to trust it on their first join and again whenever
	// it changes.
	IdentityKey *ecdsa.PrivateKey
	// Domain is shown in the trust prompt. If empty, "self", as BDS.
	Domain string
	// UDPPort, when not 0, carries every connection over one UDP port. Else
	// each connection has its own port, from UDPPorts or from the operating
	// system.
	UDPPort uint16
	// UDPPorts is BDS's server-udp-ports: ports or ranges, each maybe
	// public:local, with maybe an address first. Connections take a local port
	// from them, unless UDPPort is set, and offer the public one too.
	UDPPorts string
	// PortMappings is BDS's server-port-mappings: public:local single ports,
	// without saying which local ports to take.
	PortMappings string
	// LAN answers players looking for games on this network, BDS's LAN
	// signaling, which is on by default.
	LAN bool
	// SignInID is BDS's nethernet-id, a UUID. When set, the server signs in to
	// Microsoft's signaling service as BDS does, and takes players and ICE
	// servers from it too.
	SignInID string
	// ApplicationID and ApplicationTenantID are BDS's application-id and
	// application-tenant-id: the server owner's Azure application, which signs
	// in with the certificate at CertificatePath. Microsoft decides whether it
	// may use the service.
	ApplicationID, ApplicationTenantID string
	// CertificatePath is BDS's AppCertificate.pfx. If empty, that file.
	CertificatePath string
	// DiscoveryURL replaces Minecraft's service discovery, for tests.
	DiscoveryURL string
	// PublicIP is BDS's server-public-ip: the address players outside this
	// network reach the server at, offered next to the machine's own as a
	// server-reflexive candidate. The router must forward UDP to the same port.
	PublicIP string
	// AllowAnonymous accepts clients without a NetherNet identity. BDS
	// refuses them, online or offline.
	AllowAnonymous bool
	// Log receives transport events. If nil, nothing is logged.
	Log *slog.Logger
}

// NetherNet listens for NetherNet connections.
func NetherNet(conf NetherNetConfig) (Transport, error) {
	if conf.IdentityKey == nil {
		return nil, errors.New("network: nethernet needs an identity key")
	}
	if conf.Domain == "" {
		conf.Domain = "self"
	}
	if conf.Log == nil {
		conf.Log = slog.New(slog.DiscardHandler)
	}
	identity, err := nethernet.ServerIdentity(conf.IdentityKey, conf.Domain)
	if err != nil {
		return nil, fmt.Errorf("network: nethernet identity: %w", err)
	}
	var closers []func() error
	closeAll := func() error {
		var err error
		for i := len(closers) - 1; i >= 0; i-- {
			err = errors.Join(err, closers[i]())
		}
		return err
	}
	var se webrtc.SettingEngine
	nethernet.ApplySettings(&se)
	se.SetICETimeouts(iceDisconnectedTimeout, iceFailedTimeout, iceKeepalive)
	mappings, err := portMappings(conf)
	if err != nil {
		return nil, err
	}
	if conf.UDPPort == 0 && conf.UDPPorts != "" {
		ports, err := nethernet.ParsePortMappings(conf.UDPPorts, netip.Addr{})
		if err != nil {
			return nil, fmt.Errorf("network: nethernet udp ports %q: %w", conf.UDPPorts, err)
		}
		lo, hi := nethernet.PortRange(ports)
		if err := se.SetEphemeralUDPPortRange(lo, hi); err != nil {
			return nil, fmt.Errorf("network: nethernet udp ports %q: %w", conf.UDPPorts, err)
		}
	}
	if conf.UDPPort != 0 {
		mux, err := ice.NewMultiUDPMuxFromPort(int(conf.UDPPort))
		if err != nil {
			return nil, fmt.Errorf("network: nethernet udp port: %w", err)
		}
		closers = append(closers, mux.Close)
		se.SetICEUDPMux(mux)
	}
	tcp, err := net.Listen("tcp", conf.Address)
	if err != nil {
		_ = closeAll()
		return nil, fmt.Errorf("network: listen nethernet signaling: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	issue := func(context.Context) (*nethernet.Identity, error) { return identity, nil }
	handler := endpoint.HandlerConfig{Logger: conf.Log}.New()
	l, err := nethernet.ListenConfig{
		API:                 api,
		Log:                 conf.Log,
		AllowAnonymous:      conf.AllowAnonymous,
		PortMappings:        mappings,
		IssueServerIdentity: issue,
	}.Listen(handler)
	if err != nil {
		_ = tcp.Close()
		_ = closeAll()
		return nil, fmt.Errorf("network: nethernet listener: %w", err)
	}
	go func() {
		if err := handler.ServeListener(tcp); err != nil && !errors.Is(err, net.ErrClosed) {
			conf.Log.Error("nethernet signaling stopped", "err", err)
		}
	}()
	closers = append(closers, tcp.Close, handler.Close)
	t := &netherNetTransport{Listener: l, handler: handler, addr: tcp.Addr(), close: closeAll}
	listen := nethernet.ListenConfig{
		API:                 api,
		Log:                 conf.Log,
		AllowAnonymous:      conf.AllowAnonymous,
		PortMappings:        mappings,
		IssueServerIdentity: issue,
	}
	if !conf.LAN && conf.SignInID == "" {
		return t, nil
	}
	t.accepted = make(chan accepted)
	t.stop = make(chan struct{})
	go t.acceptMain(l)
	if conf.LAN {
		// BDS goes on without it when it has no viable network.
		if lan, err := listenLAN(conf); err != nil {
			conf.Log.Warn("no LAN signaling", "err", err)
		} else if lanListener, err := listen.Listen(lan); err != nil {
			_ = lan.Close()
			conf.Log.Warn("no LAN signaling", "err", err)
		} else {
			closers = append(closers, lan.Close, lanListener.Close)
			t.lan = lan
			go t.acceptOther(lanListener)
		}
	}
	if conf.SignInID != "" {
		if !validSignInID(conf.SignInID) {
			_ = closeAll()
			return nil, fmt.Errorf("network: nethernet-id %q is invalid: should be in UUID format xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", conf.SignInID)
		}
		go t.signIn(conf, listen)
	}
	return t, nil
}

// The ICE timings of libwebrtc, which BDS uses: a connection is weak after 2.5 s without
// hearing from the other side, and dead after 30 s. pion counts failure from the weak point.
const (
	iceDisconnectedTimeout = 2500 * time.Millisecond
	iceFailedTimeout       = 27500 * time.Millisecond
	iceKeepalive           = 2500 * time.Millisecond
)

// portMappings is what conf says about ports seen from outside. A public address on Helix's one
// fixed port maps that port to itself. BDS has no fixed port: there a public address only applies
// to the ports it is told about.
func portMappings(conf NetherNetConfig) ([]nethernet.PortMapping, error) {
	var public netip.Addr
	if conf.PublicIP != "" {
		var err error
		if public, err = netip.ParseAddr(conf.PublicIP); err != nil {
			return nil, fmt.Errorf("network: nethernet public ip %q: %w", conf.PublicIP, err)
		}
	}
	mappings, err := nethernet.ParsePortMappings(conf.UDPPorts, public)
	if err != nil {
		return nil, fmt.Errorf("network: nethernet udp ports %q: %w", conf.UDPPorts, err)
	}
	fixed, err := nethernet.ParseFixedPortMappings(conf.PortMappings, public)
	if err != nil {
		return nil, fmt.Errorf("network: nethernet port mappings %q: %w", conf.PortMappings, err)
	}
	mappings = append(mappings, fixed...)
	if len(mappings) == 0 && public.IsValid() && conf.UDPPort != 0 {
		mappings = []nethernet.PortMapping{{Min: conf.UDPPort, Max: conf.UDPPort, PublicIP: public}}
	}
	return mappings, nil
}

// listenLAN opens BDS's LAN port, or any free one if it is taken, as BDS does.
func listenLAN(conf NetherNetConfig) (*discovery.Listener, error) {
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("network: lan network id: %w", err)
	}
	lc := discovery.ListenConfig{NetworkID: binary.LittleEndian.Uint64(id), DisableBroadcast: true, Log: conf.Log}
	l, err := lc.Listen(fmt.Sprintf(":%d", discovery.DefaultPort))
	if err != nil {
		conf.Log.Info("LAN port already occupied, trying ephemeral port", "port", discovery.DefaultPort)
		return lc.Listen("")
	}
	return l, nil
}

// validSignInID says whether an ID has the form of a UUID.
func validSignInID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
	}
	return true
}

// signalingRetry is how long to wait before signing in again after a failure. BDS's own retry
// timing was not read.
const signalingRetry = 30 * time.Second

// signIn keeps the server signed in to the signaling service until the transport closes. A failure
// only logs: the server goes on without it, as BDS does.
func (t *netherNetTransport) signIn(conf NetherNetConfig, listen nethernet.ListenConfig) {
	token := signalingToken(conf)
	for {
		err := t.signedIn(conf, listen, token)
		if err != nil {
			conf.Log.Warn("failed to sign in to signaling service", "err", err)
		}
		select {
		case <-t.stop:
			return
		case <-time.After(signalingRetry):
		}
	}
}

// signedIn signs in once, and returns when the connection ends.
func (t *netherNetTransport) signedIn(conf NetherNetConfig, listen nethernet.ListenConfig, token func(context.Context) (string, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), signingInTimeout)
	defer cancel()
	base := conf.DiscoveryURL
	if base == "" {
		base = signaling.DefaultDiscoveryURL
	}
	uri, err := signaling.Discover(ctx, nil, base, protocol.CurrentVersion, signalingEnvironment)
	if err != nil {
		return err
	}
	c, err := signaling.Dial(ctx, signaling.Config{URL: uri, SignInID: conf.SignInID, Token: token, Log: conf.Log})
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	l, err := listen.Listen(c)
	if err != nil {
		return fmt.Errorf("network: signaling listener: %w", err)
	}
	defer func() { _ = l.Close() }()
	conf.Log.Info("signed in to signaling service", "id", conf.SignInID)
	t.setSignedIn(true)
	defer t.setSignedIn(false)
	done := make(chan struct{})
	go func() {
		t.acceptOther(l)
		close(done)
	}()
	select {
	case <-c.Context().Done():
		return context.Cause(c.Context())
	case <-t.stop:
		return nil
	case <-done:
		return nil
	}
}

// signalingToken gets tokens with the owner's certificate. Without one BDS has no token, and tries
// anyway.
func signalingToken(conf NetherNetConfig) func(context.Context) (string, error) {
	path := conf.CertificatePath
	if path == "" {
		path = signaling.CertificateFile
	}
	key, cert, err := signaling.LoadCertificate(path)
	if err != nil {
		conf.Log.Warn("no token for signaling service", "err", err)
		return nil
	}
	a := &signaling.AzureAuth{ApplicationID: conf.ApplicationID, TenantID: conf.ApplicationTenantID, Key: key, Certificate: cert}
	return a.Token
}

// setSignedIn says whether the server is signed in, which the server list shows.
func (t *netherNetTransport) setSignedIn(in bool) {
	t.statusMu.Lock()
	t.signed = in
	s, ok := t.status, t.hasStatus
	t.statusMu.Unlock()
	if ok {
		t.SetStatus(s)
	}
}

// accepted is a connection or error from one of a transport's listeners.
type accepted struct {
	conn net.Conn
	err  error
}

type netherNetTransport struct {
	*nethernet.Listener
	handler *endpoint.Handler
	// lan answers players on this network, if on.
	lan *discovery.Listener
	// accepted joins what all listeners accept, when there is more than one.
	accepted chan accepted
	stop     chan struct{}
	stopOnce sync.Once
	// statusMu guards the last status and whether the server is signed in.
	statusMu  sync.Mutex
	status    Status
	hasStatus bool
	signed    bool
	addr      net.Addr
	close     func() error
}

func (t *netherNetTransport) Addr() net.Addr   { return t.addr }
func (t *netherNetTransport) SetPong(b []byte) { t.PongData(b) }

// Accept implements [Transport] over both listeners.
func (t *netherNetTransport) Accept() (net.Conn, error) {
	if t.accepted == nil {
		return t.Listener.Accept()
	}
	select {
	case a := <-t.accepted:
		return a.conn, a.err
	case <-t.stop:
		return nil, net.ErrClosed
	}
}

// acceptMain passes on what the main listener accepts. When it fails, the transport has failed.
func (t *netherNetTransport) acceptMain(l *nethernet.Listener) {
	for {
		c, err := l.Accept()
		if !t.pass(accepted{c, err}) || err != nil {
			return
		}
	}
}

// acceptOther passes on what another listener accepts. When it fails, the others go on.
func (t *netherNetTransport) acceptOther(l *nethernet.Listener) {
	for {
		c, err := l.Accept()
		if err != nil || !t.pass(accepted{c, nil}) {
			return
		}
	}
}

// pass hands on a connection, and says false if the transport stopped first.
func (t *netherNetTransport) pass(a accepted) bool {
	select {
	case t.accepted <- a:
		return true
	case <-t.stop:
		if a.conn != nil {
			_ = a.conn.Close()
		}
		return false
	}
}

// SetStatus gives the server list all of BDS's fields, which a pong has not.
func (t *netherNetTransport) SetStatus(s Status) {
	t.statusMu.Lock()
	t.status, t.hasStatus = s, true
	connection := connectionNetherNet
	if t.signed {
		connection = connectionSignedIn
	}
	t.statusMu.Unlock()
	t.handler.Status(endpoint.Status{
		Connection: connection,
		ServerName: s.Name, Protocol: int(protocol.CurrentProtocol), Version: protocol.CurrentVersion, LevelName: s.LevelName,
		PlayerCount: s.Players, MaxPlayerCount: s.MaxPlayers, GameType: s.GameModeID, Hardcore: s.Hardcore,
		OnlineAuth: s.OnlineAuth, SelfSignedAuth: s.SelfSignedAuth,
	})
	if t.lan != nil {
		t.lan.ServerData(&discovery.ServerData{
			ServerName: s.Name, Protocol: protocol.CurrentProtocol, Version: protocol.CurrentVersion, LevelName: s.LevelName,
			GameType: int32(s.GameModeID), PlayerCount: int32(s.Players), MaxPlayerCount: int32(s.MaxPlayers), //nolint:gosec // small counts
			Hardcore: s.Hardcore, AcceptsOnlineAuth: s.OnlineAuth, AcceptsSelfSignedAuth: s.SelfSignedAuth,
			Nonce: t.handler.Nonce(), ConnectionType: int32(connection),
		})
	}
}

const (
	// connectionNetherNet and connectionSignedIn are BDS's connection kinds, without and with
	// Microsoft's signaling service.
	connectionNetherNet = 4
	connectionSignedIn  = 7
	// signingInTimeout limits one try at signing in.
	signingInTimeout = time.Minute
	// signalingEnvironment is the environment BDS uses by default.
	signalingEnvironment = "prod"
)

func (t *netherNetTransport) Close() error {
	if t.stop != nil {
		t.stopOnce.Do(func() { close(t.stop) })
	}
	return errors.Join(t.Listener.Close(), t.close())
}
