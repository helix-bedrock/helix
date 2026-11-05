package network

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/helix-bedrock/nethernet"
	"github.com/helix-bedrock/nethernet/endpoint"
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
	se.SetICETimeouts(iceDisconnectedTimeout, iceFailedTimeout, iceKeepalive)
	mappings, err := portMappings(conf)
	if err != nil {
		return nil, err
	}
	if conf.UDPPort == 0 && conf.UDPPorts != "" {
		lo, hi := nethernet.PortRange(mappings)
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
	handler := endpoint.HandlerConfig{Logger: conf.Log}.New()
	l, err := nethernet.ListenConfig{
		API:                 webrtc.NewAPI(webrtc.WithSettingEngine(se)),
		Log:                 conf.Log,
		AllowAnonymous:      conf.AllowAnonymous,
		PortMappings:        mappings,
		IssueServerIdentity: func(context.Context) (*nethernet.Identity, error) { return identity, nil },
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
	return &netherNetTransport{Listener: l, handler: handler, addr: tcp.Addr(), close: closeAll}, nil
}

// The ICE timings of libwebrtc, which BDS uses: a connection is weak after 2.5 s without
// hearing from the other side, and dead after 30 s. pion counts failure from the weak point.
const (
	iceDisconnectedTimeout = 2500 * time.Millisecond
	iceFailedTimeout       = 27500 * time.Millisecond
	iceKeepalive           = 2500 * time.Millisecond
)

// portMappings is what conf says about ports seen from outside. A public address with one fixed
// port and no UDPPorts maps that port to itself.
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
	if len(mappings) == 0 && public.IsValid() {
		high := conf.UDPPort
		if high == 0 {
			high = 65535
		}
		mappings = []nethernet.PortMapping{{Min: conf.UDPPort, Max: high, PublicIP: public}}
	}
	return mappings, nil
}

type netherNetTransport struct {
	*nethernet.Listener
	handler *endpoint.Handler
	addr    net.Addr
	close   func() error
}

func (t *netherNetTransport) Addr() net.Addr   { return t.addr }
func (t *netherNetTransport) SetPong(b []byte) { t.PongData(b) }

// SetStatus gives the server list all of BDS's fields, which a pong has not.
func (t *netherNetTransport) SetStatus(s Status) {
	t.handler.Status(endpoint.Status{
		ServerName: s.Name, Protocol: int(protocol.CurrentProtocol), Version: protocol.CurrentVersion, LevelName: s.LevelName,
		PlayerCount: s.Players, MaxPlayerCount: s.MaxPlayers, GameType: s.GameModeID, Hardcore: s.Hardcore,
		OnlineAuth: s.OnlineAuth, SelfSignedAuth: s.SelfSignedAuth,
	})
}

func (t *netherNetTransport) Close() error {
	return errors.Join(t.Listener.Close(), t.close())
}
