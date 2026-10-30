package network

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/auth"
	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/login"
	"github.com/helix-bedrock/protocol/packet"
)

// Login is one connection going through the login sequence. Steps read and
// fill it; a server's own steps may read everything a step before them set.
// A Login is used by one goroutine only.
type Login struct {
	l   *Listener
	c   *conn.Conn
	log *slog.Logger

	// Protocol is the client's protocol version, from
	// RequestNetworkSettings.
	Protocol int32
	// Identity is who the player is, from the login token.
	Identity login.IdentityData
	// Client is what the client says about itself, such as its skin.
	Client login.ClientData
	// Authenticated reports whether Xbox Live vouched for Identity.
	Authenticated bool
	// ClientKey is the client's public key, from the login token.
	ClientKey *ecdsa.PublicKey
	// CacheEnabled reports whether the client caches chunk blobs.
	CacheEnabled bool
}

// Conn is the packet connection. Steps write to it directly when they need
// more than [Login.Write].
func (lg *Login) Conn() *conn.Conn { return lg.c }

// RemoteAddr is the client's address.
func (lg *Login) RemoteAddr() net.Addr { return lg.c.RemoteAddr() }

// Read returns the next packet, waiting at most Config.ReadTimeout.
func (lg *Login) Read(ctx context.Context) (packet.Packet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d := lg.l.conf.ReadTimeout; d > 0 {
		_ = lg.c.Transport().SetReadDeadline(time.Now().Add(d))
	}
	return lg.c.ReadPacket()
}

// Write sends packets at once: the login waits on every answer.
func (lg *Login) Write(pks ...packet.Packet) error {
	for _, pk := range pks {
		if err := lg.c.WritePacket(pk); err != nil {
			return fmt.Errorf("write %T: %w", pk, err)
		}
	}
	return lg.c.Flush()
}

// Expect reads the next packet and fails unless it is a T.
func Expect[T packet.Packet](ctx context.Context, lg *Login) (T, error) {
	var zero T
	pk, err := lg.Read(ctx)
	if err != nil {
		return zero, err
	}
	v, ok := pk.(T)
	if !ok {
		return zero, Refuse(packet.DisconnectReasonUnexpectedPacket, "", fmt.Errorf("expected %T, got %T", zero, pk))
	}
	return v, nil
}

// fail tells the player why, when a step refused them, and logs it.
func (lg *Login) fail(step string, err error) {
	var r *Refusal
	if errors.As(err, &r) {
		_ = lg.Write(&packet.Disconnect{Reason: r.Reason, Message: r.Message})
		lg.log.Info("login refused", "step", step, "reason", r.Reason, "err", r.Err)
		return
	}
	if !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled) {
		lg.log.Debug("login stopped", "step", step, "err", err)
	}
}

// Refusal refuses a player: the client is sent Reason (a
// packet.DisconnectReason value) and shows its translated message for it,
// or Message when it is not empty.
type Refusal struct {
	Reason  int32
	Message string
	// Err is why, for the server's log. It is not sent.
	Err error
}

// Refuse returns a Refusal.
func Refuse(reason int32, message string, err error) error {
	return &Refusal{Reason: reason, Message: message, Err: err}
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("refused with reason %d: %v", r.Reason, r.Err)
}

func (r *Refusal) Unwrap() error { return r.Err }

// Step is one step of the login sequence.
type Step struct {
	// Name identifies the step for Steps.Before, After, Replace and Remove.
	Name string
	// Run does the step. An error stops the login: a *Refusal is shown to
	// the player, any other error just closes the connection.
	Run func(ctx context.Context, lg *Login) error
}

// Names of the default steps, in order.
const (
	StepNetworkSettings = "network_settings"
	StepLogin           = "login"
	StepEncryption      = "encryption"
	StepLoginSuccess    = "login_success"
	StepResourcePacks   = "resource_packs"
)

// DefaultSteps returns the login sequence of BDS 1.26.60. Server checks go
// between them where BDS does them: identity checks (allow list, a player
// already online) after StepLogin, and the server-full check before
// StepLoginSuccess, which BDS does after encryption.
func DefaultSteps() Steps {
	return Steps{
		{StepNetworkSettings, networkSettings},
		{StepLogin, readLogin},
		{StepEncryption, encryption},
		{StepLoginSuccess, loginSuccess},
		{StepResourcePacks, resourcePacks},
	}
}

// Steps is a login sequence.
type Steps []Step

// Index returns the position of the named step, or -1.
func (s Steps) Index(name string) int {
	for i, st := range s {
		if st.Name == name {
			return i
		}
	}
	return -1
}

// Before returns the steps with st added before the named one, or at the end
// when there is no such step.
func (s Steps) Before(name string, st Step) Steps {
	i := s.Index(name)
	if i < 0 {
		i = len(s)
	}
	return s.insert(i, st)
}

// After returns the steps with st added after the named one, or at the end
// when there is no such step.
func (s Steps) After(name string, st Step) Steps {
	i := s.Index(name)
	if i < 0 {
		i = len(s) - 1
	}
	return s.insert(i+1, st)
}

// Replace returns the steps with the named one's Run replaced.
func (s Steps) Replace(name string, run func(ctx context.Context, lg *Login) error) Steps {
	out := append(Steps(nil), s...)
	if i := out.Index(name); i >= 0 {
		out[i].Run = run
	}
	return out
}

// Remove returns the steps without the named one.
func (s Steps) Remove(name string) Steps {
	out := make(Steps, 0, len(s))
	for _, st := range s {
		if st.Name != name {
			out = append(out, st)
		}
	}
	return out
}

func (s Steps) insert(i int, st Step) Steps {
	out := make(Steps, 0, len(s)+1)
	out = append(out, s[:i]...)
	out = append(out, st)
	return append(out, s[i:]...)
}

// checkProtocol refuses clients of another protocol the way BDS does: a
// PlayStatus saying which side is outdated, then the disconnect reason.
func checkProtocol(lg *Login, p int32) error {
	if p == protocol.CurrentProtocol {
		return nil
	}
	status, reason := packet.PlayStatusLoginFailedServer, packet.DisconnectReasonOutdatedServer
	if p < protocol.CurrentProtocol {
		status, reason = packet.PlayStatusLoginFailedClient, packet.DisconnectReasonOutdatedClient
	}
	_ = lg.Write(&packet.PlayStatus{Status: status})
	return Refuse(reason, "", fmt.Errorf("client protocol %d, server %d", p, protocol.CurrentProtocol))
}

// networkSettings answers RequestNetworkSettings with the compression
// settings and turns compression on (BDS: ServerNetworkHandler's
// RequestNetworkSettings handler).
func networkSettings(ctx context.Context, lg *Login) error {
	req, err := Expect[*packet.RequestNetworkSettings](ctx, lg)
	if err != nil {
		return err
	}
	lg.Protocol = req.ClientProtocol
	if err := checkProtocol(lg, req.ClientProtocol); err != nil {
		return err
	}
	conf := lg.l.conf
	if err := lg.Write(&packet.NetworkSettings{
		CompressionThreshold: conf.CompressionThreshold,
		CompressionAlgorithm: conf.Compression.EncodeCompression(),
	}); err != nil {
		return err
	}
	lg.c.EnableCompression(conf.Compression, int(conf.CompressionThreshold))
	return nil
}

// readLogin reads and verifies the Login packet, in BDS's order: protocol,
// a connection request at all, a token at all, then the token itself.
func readLogin(ctx context.Context, lg *Login) error {
	pk, err := Expect[*packet.Login](ctx, lg)
	if err != nil {
		return err
	}
	if err := checkProtocol(lg, pk.ClientProtocol); err != nil {
		return err
	}
	if len(pk.ConnectionRequest) == 0 {
		return Refuse(packet.DisconnectReasonLoginPacketNoRequest, "", errors.New("no connection request"))
	}
	conf := lg.l.conf
	id, cd, res, err := login.ParseWithConfig(pk.ConnectionRequest, login.Config{Verifier: conf.Verifier, AllowLegacyChain: conf.AllowLegacyChain})
	if err != nil {
		if errors.Is(err, login.ErrLegacyChain) {
			return Refuse(packet.DisconnectReasonLoginPacketNoCert, "", err)
		}
		return Refuse(auth.Reason(err), "", err)
	}
	if conf.Verifier != nil && !res.XBOXLiveAuthenticated {
		return Refuse(packet.DisconnectReasonNotAuthenticated, "", errors.New("not signed in to Xbox Live"))
	}
	lg.Identity, lg.Client, lg.Authenticated, lg.ClientKey = id, cd, res.XBOXLiveAuthenticated, res.PublicKey
	lg.c.SetLoginData(id, cd)
	lg.log = lg.log.With("name", id.DisplayName, "xuid", id.XUID)
	return nil
}

// encryption sends the server's handshake and encrypts the connection.
// Transports that encrypt by themselves (NetherNet) skip it, as BDS does.
func encryption(ctx context.Context, lg *Login) error {
	if d, ok := lg.c.Transport().(packet.EncryptionDisabler); ok && d.DisableEncryption() {
		// NetherNet bound the connection to the key the client signed its
		// WebRTC offer with but did not check that key's token; the login
		// token did. They must name the same key, or the login was taken
		// from another connection.
		if k, ok := lg.c.Transport().(interface{ PublicKey() *ecdsa.PublicKey }); ok {
			if pub := k.PublicKey(); pub != nil && !pub.Equal(lg.ClientKey) {
				return Refuse(packet.DisconnectReasonNotAuthenticated, "", errors.New("login key differs from the NetherNet identity key"))
			}
		}
		return nil
	}
	token, key, err := login.ServerHandshake(lg.l.conf.HandshakeKey, lg.ClientKey)
	if err != nil {
		return Refuse(packet.DisconnectReasonUnrecoverableError, "", err)
	}
	if err := lg.Write(&packet.ServerToClientHandshake{JWT: token}); err != nil {
		return err
	}
	lg.c.EnableEncryption(key)
	_, err = Expect[*packet.ClientToServerHandshake](ctx, lg)
	return err
}

// loginSuccess tells the client its login is accepted.
func loginSuccess(_ context.Context, lg *Login) error {
	return lg.Write(&packet.PlayStatus{Status: packet.PlayStatusLoginSuccess})
}
