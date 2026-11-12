package sim

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/login"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/protocol/version"
)

const (
	// eyeHeight is how far above its feet a player's position is on the wire.
	eyeHeight = 1.62
	// joinTimeout is how long a bot may take to get into the world.
	joinTimeout = 2 * time.Minute
	// botViewRadius is the chunk radius bots ask for: a low client setting, so the server's
	// chunk work is real but bots do not spend the machine's CPU on decoding chunks.
	botViewRadius = 4
	// headerIDMask takes the packet ID out of a packet header.
	headerIDMask = 0x3ff
)

// keptPackets are the packets a bot decodes; everything else it only counts or drops
// unread, so 400 bots in one process cost the machine as little as they can.
func keptPackets() map[uint32]bool {
	return map[uint32]bool{
		packet.IDNetworkSettings: true, packet.IDServerToClientHandshake: true, packet.IDPlayStatus: true,
		packet.IDResourcePacksInfo: true, packet.IDResourcePackStack: true, packet.IDStartGame: true,
		packet.IDUpdateBlock: true, packet.IDDisconnect: true,
	}
}

// Bot is a client in the same process: it logs in like a vanilla client over a memory
// connection, then sends PlayerAuthInput every tick as a client does, and whatever
// actions its scenario gives it.
type Bot struct {
	Name string
	// RuntimeID and UniqueID are the bot's player entity's, from StartGame.
	RuntimeID uint64
	UniqueID  int64

	c    *conn.Conn
	key  *ecdsa.PrivateKey
	id   login.IdentityData
	kept map[uint32]bool
	// events gets the decoded packets the reader keeps.
	events chan packet.Packet
	// readErr is why the reader stopped.
	readErr atomic.Pointer[error]
	// moves counts movement packets about others received.
	moves atomic.Int64
	lat   *latencies

	// Rand is the bot's own random source, for its scenario's choices.
	Rand *mrand.Rand

	// The rest is the bot's own, used by its driver goroutine only: where it is, its
	// client tick and input, and its scenario's state (heading, blocks dug, mining now).
	pos     mgl32.Vec3
	tick    uint64
	input   packet.PlayerAuthInput
	heading float64
	dug     int
	mining  bool

	// mu guards breaks: the block actions sent and not yet answered, with when.
	mu     sync.Mutex
	breaks map[protocol.BlockPos]time.Time
}

// newBot dials the memory transport and logs in, returning once the bot is in the world.
func newBot(ctx context.Context, tr *network.Memory, name string, seed uint64, lat *latencies) (*Bot, error) {
	mc, err := tr.Dial()
	if err != nil {
		return nil, fmt.Errorf("sim: dial: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, err
	}
	b := &Bot{
		Name: name, key: key, kept: keptPackets(), lat: lat, Rand: mrand.New(mrand.NewPCG(seed, 3)), //nolint:gosec // a scenario's choices
		c:      conn.New(mc, conn.Config{Pool: version.Latest.Packets(false), FromServer: true}),
		id:     login.IdentityData{Identity: uuid.NewString(), DisplayName: name},
		events: make(chan packet.Packet, 64), breaks: map[protocol.BlockPos]time.Time{},
	}
	b.c.Hooks().OnRawIn(b.filter)
	go b.read()
	ctx, cancel := context.WithTimeout(ctx, joinTimeout)
	defer cancel()
	if err := b.join(ctx); err != nil {
		_ = b.c.Close()
		return nil, fmt.Errorf("sim: %s join: %w", name, err)
	}
	return b, nil
}

// filter drops every packet the bot does not decode, counting movement of others.
func (b *Bot) filter(e *conn.RawEvent) conn.Verdict {
	id := headerID(e.Data)
	if id == packet.IDMovePlayer || id == packet.IDMoveActorDelta || id == packet.IDMoveActorAbsolute {
		b.moves.Add(1)
	}
	if b.kept[id] {
		return conn.Allow
	}
	return conn.Drop
}

// headerID reads the packet ID from the start of a packet's varuint32 header.
func headerID(data []byte) uint32 {
	var v uint32
	for i := 0; i < len(data) && i < 5; i++ {
		v |= uint32(data[i]&0x7f) << (7 * i)
		if data[i]&0x80 == 0 {
			break
		}
	}
	return v & headerIDMask
}

// read decodes the kept packets until the connection closes: block updates answer the
// bot's breaks here; the rest goes to the join.
func (b *Bot) read() {
	defer close(b.events)
	for {
		pk, err := b.c.ReadPacket()
		if err != nil {
			b.readErr.Store(&err)
			return
		}
		if u, ok := pk.(*packet.UpdateBlock); ok {
			b.answered(u.Position)
			continue
		}
		// Once in the world nothing waits for these; a full queue drops them rather than
		// stop reading, which would back up the server's writes.
		select {
		case b.events <- pk:
		default:
		}
	}
}

// answered records the delay of the break at pos, if the bot sent one.
func (b *Bot) answered(pos protocol.BlockPos) {
	b.mu.Lock()
	sent, ok := b.breaks[pos]
	delete(b.breaks, pos)
	b.mu.Unlock()
	if ok {
		b.lat.add(time.Since(sent))
	}
}

// expect waits for the next kept packet, which must be a T.
func expect[T packet.Packet](ctx context.Context, b *Bot) (T, error) {
	var zero T
	for {
		select {
		case pk, ok := <-b.events:
			if !ok {
				if err := b.readErr.Load(); err != nil {
					return zero, fmt.Errorf("connection closed: %w", *err)
				}
				return zero, errors.New("connection closed")
			}
			if d, isDisconnect := pk.(*packet.Disconnect); isDisconnect {
				return zero, fmt.Errorf("disconnected: %s", d.Message)
			}
			if v, isT := pk.(T); isT {
				return v, nil
			}
		case <-ctx.Done():
			return zero, fmt.Errorf("waiting for %T: %w", zero, ctx.Err())
		}
	}
}

// send writes packets and flushes them.
func (b *Bot) send(pks ...packet.Packet) error {
	for _, pk := range pks {
		if err := b.c.WritePacket(pk); err != nil {
			return err
		}
	}
	return b.c.Flush()
}

// join runs the client side of the login and spawn, as a vanilla client does.
func (b *Bot) join(ctx context.Context) error {
	steps := []func(context.Context) error{b.settings, b.login, b.encrypt, b.packs, b.spawn}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// settings asks for the network settings and turns compression on.
func (b *Bot) settings(ctx context.Context) error {
	if err := b.send(&packet.RequestNetworkSettings{ClientProtocol: protocol.CurrentProtocol}); err != nil {
		return err
	}
	ns, err := expect[*packet.NetworkSettings](ctx, b)
	if err != nil {
		return err
	}
	b.c.EnableCompression(packet.FlateCompression, int(ns.CompressionThreshold))
	return nil
}

// login sends an offline login.
func (b *Bot) login(context.Context) error {
	cd := login.ClientData{
		DeviceOS: 7, GameVersion: protocol.CurrentVersion, LanguageCode: "en_US", ServerAddress: "127.0.0.1:19132", SkinID: "steve", SelfSignedID: uuid.NewString(),
		SkinResourcePatch: base64.StdEncoding.EncodeToString([]byte(`{"geometry":{"default":"geometry.humanoid.custom"}}`)),
	}
	return b.send(&packet.Login{ClientProtocol: protocol.CurrentProtocol, ConnectionRequest: login.EncodeOffline(b.id, cd, b.key, false)})
}

// handshakeToken is the part of the server's handshake token the client needs.
type handshakeToken struct {
	header struct{ X5U string }
	claims struct{ Salt string }
}

// encrypt reads the server's handshake and turns encryption on as the client does.
func (b *Bot) encrypt(ctx context.Context) error {
	hs, err := expect[*packet.ServerToClientHandshake](ctx, b)
	if err != nil {
		return err
	}
	var tok handshakeToken
	if err := parseHandshake(hs.JWT, &tok); err != nil {
		return err
	}
	pub := &ecdsa.PublicKey{}
	if err := login.ParsePublicKey(tok.header.X5U, pub); err != nil {
		return err
	}
	salt, err := base64.StdEncoding.DecodeString(tok.claims.Salt)
	if err != nil {
		return err
	}
	secret, err := sharedSecret(b.key, pub)
	if err != nil {
		return err
	}
	b.c.EnableEncryption(sha256.Sum256(append(salt, secret...)))
	if err := b.send(&packet.ClientToServerHandshake{}); err != nil {
		return err
	}
	ps, err := expect[*packet.PlayStatus](ctx, b)
	if err != nil {
		return err
	}
	if ps.Status != packet.PlayStatusLoginSuccess {
		return fmt.Errorf("play status %d", ps.Status)
	}
	return nil
}

// parseHandshake reads the header and claims of the handshake JWT.
func parseHandshake(jwt []byte, tok *handshakeToken) error {
	parts := strings.Split(string(jwt), ".")
	if len(parts) != 3 {
		return fmt.Errorf("handshake token has %d parts", len(parts))
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	if err := json.Unmarshal(hb, &tok.header); err != nil {
		return err
	}
	return json.Unmarshal(cb, &tok.claims)
}

// sharedSecret is the ECDH secret of the bot's key and the server's.
func sharedSecret(key *ecdsa.PrivateKey, server *ecdsa.PublicKey) ([]byte, error) {
	priv, err := key.ECDH()
	if err != nil {
		return nil, err
	}
	pub, err := server.ECDH()
	if err != nil {
		return nil, err
	}
	return priv.ECDH(pub)
}

// packs accepts the server's resource packs, having none to download.
func (b *Bot) packs(ctx context.Context) error {
	if _, err := expect[*packet.ResourcePacksInfo](ctx, b); err != nil {
		return err
	}
	if err := b.send(&packet.ResourcePackClientResponse{Response: packet.PackResponseAllPacksDownloaded}); err != nil {
		return err
	}
	if _, err := expect[*packet.ResourcePackStack](ctx, b); err != nil {
		return err
	}
	return b.send(&packet.ResourcePackClientResponse{Response: packet.PackResponseCompleted})
}

// spawn reads StartGame, asks for chunks, waits to be let in and says it is in.
func (b *Bot) spawn(ctx context.Context) error {
	sg, err := expect[*packet.StartGame](ctx, b)
	if err != nil {
		return err
	}
	b.RuntimeID, b.UniqueID, b.pos = sg.EntityRuntimeID, sg.EntityUniqueID, sg.PlayerPosition
	if err := b.send(&packet.RequestChunkRadius{ChunkRadius: botViewRadius, MaxChunkRadius: botViewRadius}); err != nil {
		return err
	}
	for {
		ps, err := expect[*packet.PlayStatus](ctx, b)
		if err != nil {
			return err
		}
		if ps.Status == packet.PlayStatusPlayerSpawn {
			break
		}
	}
	b.input.InputData = protocol.NewInputFlags(packet.InputFlagCount)
	// Bots say they stand on something, as a client on the ground does: BDS breaks five
	// times slower in the air, and lands every move without it.
	b.input.InputData.Set(packet.InputFlagVerticalCollision)
	return b.send(&packet.SetLocalPlayerAsInitialised{EntityRuntimeID: b.RuntimeID})
}

// Feet is where the bot stands.
func (b *Bot) Feet() mgl32.Vec3 { return b.pos.Sub(mgl32.Vec3{0, eyeHeight, 0}) }

// MoveTo puts the bot's feet at feet on its next input.
func (b *Bot) MoveTo(feet mgl32.Vec3) { b.pos = feet.Add(mgl32.Vec3{0, eyeHeight, 0}) }

// Input sends this tick's PlayerAuthInput with actions, if any, and the delta from the
// last position.
func (b *Bot) Input(last mgl32.Vec3, actions []protocol.PlayerBlockAction) error {
	b.tick++
	in := &b.input
	in.Position, in.Delta, in.Tick = b.pos, b.pos.Sub(last), b.tick
	in.BlockActions = protocol.Optional[[]protocol.PlayerBlockAction]{}
	if len(actions) > 0 {
		in.BlockActions = protocol.Option(actions)
		now := time.Now()
		b.mu.Lock()
		for _, a := range actions {
			// The first action at a block starts its clock; the ones a survival break
			// sends every tick after it do not.
			if _, waiting := b.breaks[a.BlockPos]; !waiting {
				b.breaks[a.BlockPos] = now
			}
		}
		b.mu.Unlock()
	}
	return b.send(in)
}

// Waiting reports whether a block action at pos was sent and the server has not answered
// it with a block update yet.
func (b *Bot) Waiting(pos protocol.BlockPos) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.breaks[pos]
	return ok
}

// Close disconnects the bot.
func (b *Bot) Close() error { return b.c.Close() }
