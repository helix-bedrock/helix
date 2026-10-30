package network

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/helix-bedrock/nethernet"
	"github.com/helix-bedrock/nethernet/endpoint"
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
	// the operating system picks a port per connection.
	UDPPort uint16
	// AllowAnonymous accepts clients without a NetherNet identity, for
	// offline servers.
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
		IssueServerIdentity: func(context.Context) (*nethernet.Identity, error) { return identity, nil },
	}.Listen(handler)
	if err != nil {
		_ = tcp.Close()
		_ = closeAll()
		return nil, fmt.Errorf("network: nethernet listener: %w", err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := srv.Serve(tcp); err != nil && !errors.Is(err, http.ErrServerClosed) {
			conf.Log.Error("nethernet signaling stopped", "err", err)
		}
	}()
	// The HTTP server owns tcp from here; closing it closes tcp.
	closers = append(closers, srv.Close)
	return &netherNetTransport{Listener: l, addr: tcp.Addr(), close: closeAll}, nil
}

type netherNetTransport struct {
	*nethernet.Listener
	addr  net.Addr
	close func() error
}

func (t *netherNetTransport) Addr() net.Addr   { return t.addr }
func (t *netherNetTransport) SetPong(b []byte) { t.PongData(b) }

func (t *netherNetTransport) Close() error {
	return errors.Join(t.Listener.Close(), t.close())
}
