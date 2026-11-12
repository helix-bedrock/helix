package network

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/login"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/protocol/version"
)

// dialMemory connects a client to a memory transport.
func dialMemory(t *testing.T, tr *Memory) *conn.Conn {
	t.Helper()
	c, err := tr.Dial()
	if err != nil {
		t.Fatal(err)
	}
	return conn.New(c, conn.Config{Pool: version.Latest.Packets(false)})
}

func listen(t *testing.T, conf Config) (*Listener, *Memory) {
	t.Helper()
	conf.ReadTimeout = 2 * time.Second
	l, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	tr := NewMemory()
	l.Serve(tr)
	t.Cleanup(func() { _ = l.Close() })
	return l, tr
}

// client is the client side of the login, as a vanilla client does it.
type client struct {
	t   *testing.T
	c   *conn.Conn
	key *ecdsa.PrivateKey
	id  login.IdentityData
}

func newClient(t *testing.T, c *conn.Conn) *client {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, c: c, key: key, id: login.IdentityData{Identity: uuid.NewString(), DisplayName: "Steve"}}
}

func (cl *client) send(pk packet.Packet) {
	cl.t.Helper()
	if err := cl.c.WritePacket(pk); err != nil {
		cl.t.Fatal(err)
	}
	if err := cl.c.Flush(); err != nil {
		cl.t.Fatal(err)
	}
}

func read[T packet.Packet](cl *client) T {
	cl.t.Helper()
	pk, err := cl.c.ReadPacket()
	if err != nil {
		cl.t.Fatalf("read %T: %v", *new(T), err)
	}
	v, ok := pk.(T)
	if !ok {
		cl.t.Fatalf("got %#v, want %T", pk, *new(T))
	}
	return v
}

func (cl *client) requestSettings(p int32) {
	cl.send(&packet.RequestNetworkSettings{ClientProtocol: p})
}

func (cl *client) login() {
	cl.t.Helper()
	ns := read[*packet.NetworkSettings](cl)
	cl.c.EnableCompression(packet.FlateCompression, int(ns.CompressionThreshold))
	cd := login.ClientData{DeviceOS: 7, GameVersion: protocol.CurrentVersion, LanguageCode: "en_US", ServerAddress: "127.0.0.1:19132", SkinID: "steve",
		SkinResourcePatch: base64.StdEncoding.EncodeToString([]byte(`{"geometry":{"default":"geometry.humanoid.custom"}}`))}
	cl.send(&packet.Login{ClientProtocol: protocol.CurrentProtocol, ConnectionRequest: login.EncodeOffline(cl.id, cd, cl.key, false)})
}

// encrypt reads the server's handshake and turns encryption on the way the
// client does.
func (cl *client) encrypt() {
	cl.t.Helper()
	hs := read[*packet.ServerToClientHandshake](cl)
	parts := strings.Split(string(hs.JWT), ".")
	if len(parts) != 3 {
		cl.t.Fatalf("handshake token has %d parts", len(parts))
	}
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	pb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var h struct{ X5U string }
	var p struct{ Salt string }
	if json.Unmarshal(hb, &h) != nil || json.Unmarshal(pb, &p) != nil {
		cl.t.Fatal("handshake token does not decode")
	}
	pub := &ecdsa.PublicKey{}
	if err := login.ParsePublicKey(h.X5U, pub); err != nil {
		cl.t.Fatal(err)
	}
	salt, _ := base64.StdEncoding.DecodeString(p.Salt)
	priv, _ := cl.key.ECDH()
	spub, _ := pub.ECDH()
	secret, _ := priv.ECDH(spub)
	cl.c.EnableEncryption(sha256.Sum256(append(salt, secret...)))
	cl.send(&packet.ClientToServerHandshake{})
}

func (cl *client) packs() {
	cl.t.Helper()
	read[*packet.ResourcePacksInfo](cl)
	cl.send(&packet.ResourcePackClientResponse{Response: packet.PackResponseAllPacksDownloaded})
	if st := read[*packet.ResourcePackStack](cl); st.BaseGameVersion != protocol.CurrentVersion {
		cl.t.Fatalf("stack base game version %q", st.BaseGameVersion)
	}
	cl.send(&packet.ResourcePackClientResponse{Response: packet.PackResponseCompleted})
}

func accept(t *testing.T, l *Listener) *Conn {
	t.Helper()
	got := make(chan *Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			got <- c
		}
	}()
	select {
	case c := <-got:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("login never finished")
		return nil
	}
}

func TestLoginOffline(t *testing.T) {
	l, tr := listen(t, DefaultConfig())
	cl := newClient(t, dialMemory(t, tr))
	cl.requestSettings(protocol.CurrentProtocol)
	cl.login()
	cl.encrypt()
	if ps := read[*packet.PlayStatus](cl); ps.Status != packet.PlayStatusLoginSuccess {
		t.Fatalf("play status %d", ps.Status)
	}
	cl.packs()
	c := accept(t, l)
	if c.Identity().Identity != cl.id.Identity || c.Authenticated() {
		t.Fatalf("identity %+v, authenticated %v", c.Identity(), c.Authenticated())
	}
}

func TestOutdatedClients(t *testing.T) {
	for _, tc := range []struct {
		protocol       int32
		status, reason int32
	}{
		{protocol.CurrentProtocol - 1, packet.PlayStatusLoginFailedClient, packet.DisconnectReasonOutdatedClient},
		{protocol.CurrentProtocol + 1, packet.PlayStatusLoginFailedServer, packet.DisconnectReasonOutdatedServer},
	} {
		_, tr := listen(t, DefaultConfig())
		cl := newClient(t, dialMemory(t, tr))
		cl.requestSettings(tc.protocol)
		if ps := read[*packet.PlayStatus](cl); ps.Status != tc.status {
			t.Fatalf("protocol %d: play status %d, want %d", tc.protocol, ps.Status, tc.status)
		}
		if d := read[*packet.Disconnect](cl); d.Reason != tc.reason {
			t.Fatalf("protocol %d: reason %d, want %d", tc.protocol, d.Reason, tc.reason)
		}
	}
}

// TestServerStep checks that a server's own step runs in its place and its
// refusal reaches the client.
func TestServerStep(t *testing.T) {
	conf := DefaultConfig()
	var saw string
	conf.Steps = DefaultSteps().After(StepLogin, Step{Name: "allow_list", Run: func(_ context.Context, lg *Login) error {
		saw = lg.Identity.DisplayName
		return Refuse(packet.DisconnectReasonDenyListed, "", errors.New("not on the list"))
	}})
	_, tr := listen(t, conf)
	cl := newClient(t, dialMemory(t, tr))
	cl.requestSettings(protocol.CurrentProtocol)
	cl.login()
	if d := read[*packet.Disconnect](cl); d.Reason != packet.DisconnectReasonDenyListed {
		t.Fatalf("reason %d", d.Reason)
	}
	if saw != "Steve" {
		t.Fatalf("step saw %q", saw)
	}
}

func TestSilentClientTimesOut(t *testing.T) {
	conf := DefaultConfig()
	l, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	l.conf.ReadTimeout = 50 * time.Millisecond
	tr := NewMemory()
	l.Serve(tr)
	defer func() { _ = l.Close() }()
	c := dialMemory(t, tr)
	done := make(chan error, 1)
	go func() { _, err := c.ReadPacket(); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("silent client got a packet")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("silent client was never dropped")
	}
}

func TestStepsEdits(t *testing.T) {
	s := DefaultSteps()
	noop := func(context.Context, *Login) error { return nil }
	s = s.Before(StepEncryption, Step{Name: "a", Run: noop}).After(StepEncryption, Step{Name: "b", Run: noop}).Remove(StepResourcePacks)
	names := make([]string, 0, len(s))
	for _, st := range s {
		names = append(names, st.Name)
	}
	want := "network_settings login a encryption b login_success"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("steps %q, want %q", got, want)
	}
}
