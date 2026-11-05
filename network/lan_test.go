package network

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/helix-bedrock/nethernet"
	"github.com/helix-bedrock/nethernet/discovery"
	"github.com/helix-bedrock/protocol"
)

// A client on this network finds the server with BDS's status and joins through LAN signaling.
func TestLANFindAndJoin(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := NetherNet(NetherNetConfig{Address: "127.0.0.1:0", IdentityKey: key, LAN: true, AllowAnonymous: true, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	server, ok := tr.(*netherNetTransport)
	if !ok || server.lan == nil {
		t.Fatal("no LAN signaling")
	}
	server.SetStatus(Status{Name: "a", LevelName: "b", Players: 1, MaxPlayers: 10, OnlineAuth: true})

	port := server.lan.LocalAddr().(*net.UDPAddr).Port
	client, err := discovery.ListenConfig{
		BroadcastAddress: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port},
		Log:              slog.New(slog.DiscardHandler),
	}.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var id uint64
	var data discovery.ServerData
	deadline := time.Now().Add(10 * time.Second)
	for id == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the server did not answer")
		}
		for found, b := range client.Responses() {
			if err := data.UnmarshalBinary(b); err != nil {
				t.Fatal(err)
			}
			id = found
		}
		time.Sleep(50 * time.Millisecond)
	}
	if data.ServerName != "a" || data.LevelName != "b" || data.PlayerCount != 1 || data.MaxPlayerCount != 10 ||
		data.Protocol != protocol.CurrentProtocol || !data.AcceptsOnlineAuth || data.ConnectionType != connectionNetherNet ||
		data.Nonce == "" || data.Nonce != server.handler.Nonce() {
		t.Errorf("found %+v", data)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var d nethernet.Dialer
	conn, err := d.DialContext(ctx, strconv.FormatUint(id, 10), client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	accepted, err := tr.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = accepted.Close() }()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := accepted.(*nethernet.Conn).ReadPacket()
	if err != nil || string(got) != "hello" {
		t.Errorf("%q %v", got, err)
	}
}
