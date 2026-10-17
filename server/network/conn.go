package network

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/server/resource"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/login"
	"github.com/helix-bedrock/protocol/packet"
)

// Conn is one player's connection after login. It implements session.Conn.
//
// The login sequence (network settings, login, encryption, resource packs)
// runs in the listener before Accept returns the Conn. StartGameContext then
// runs the spawn sequence. The steps follow gophertunnel's minecraft.Conn
// (MIT, v1.62.0), which Dragonfly used, on top of Helix's protocol/conn.
type Conn struct {
	c   *conn.Conn
	l   *Listener
	log *slog.Logger

	id            login.IdentityData
	cd            login.ClientData
	authenticated bool
	cacheEnabled  atomic.Bool
	radius        atomic.Int32
	gameData      GameData

	// pending holds packets read during the spawn sequence that the
	// session should still see. Only the reading goroutine touches it.
	mu      sync.Mutex
	pending []packet.Packet

	closeOnce sync.Once
}

// Messages shown on the client's disconnect screen.
const (
	msgLoginFailed  = "Login failed. Try again."
	msgAuthDown     = "Could not check your Xbox Live sign-in right now. Try again later."
	msgXboxRequired = "You must be signed in to Xbox Live to join this server."
	msgServerFull   = "The server is full."
)

// compressionThreshold is the smallest batch that is compressed.
const compressionThreshold = 256

// packChunkSize is the size of one resource pack chunk, as gophertunnel's
// DefaultResourcePackChunkSize.
const packChunkSize = 128 * 1024

// verifyTimeout bounds fetching Microsoft's signing keys during one login.
const verifyTimeout = 10 * time.Second

func newConn(t conn.Transport, l *Listener) *Conn {
	log := l.conf.Log.With("addr", t.RemoteAddr())
	return &Conn{
		l: l, log: log,
		// DisableLimits: see accept.
		c: conn.New(t, conn.Config{Log: log, FlushInterval: l.conf.FlushRate, DisableLimits: true}),
	}
}

// login runs the login sequence until the client has its resource packs.
func (c *Conn) login() error {
	var req *packet.RequestNetworkSettings
	if err := expect(c, &req); err != nil {
		return err
	}
	if req.ClientProtocol != protocol.CurrentProtocol {
		status := packet.PlayStatusLoginFailedClient
		if req.ClientProtocol > protocol.CurrentProtocol {
			status = packet.PlayStatusLoginFailedServer
		}
		_ = c.c.WritePacket(&packet.PlayStatus{Status: status})
		_ = c.c.Flush()
		return fmt.Errorf("unsupported protocol %d (server speaks %d)", req.ClientProtocol, protocol.CurrentProtocol)
	}
	compression := c.l.conf.Compression
	if compression == nil {
		compression = packet.FlateCompression
	}
	if err := c.write(&packet.NetworkSettings{
		CompressionThreshold: compressionThreshold,
		CompressionAlgorithm: compression.EncodeCompression(),
	}); err != nil {
		return err
	}
	c.c.EnableCompression(compression, compressionThreshold)

	var lg *packet.Login
	if err := expect(c, &lg); err != nil {
		return err
	}
	pub, err := c.verify(lg)
	if err != nil {
		return err
	}

	if d, ok := c.c.Transport().(packet.EncryptionDisabler); !ok || !d.DisableEncryption() {
		if err := c.encrypt(pub); err != nil {
			return fmt.Errorf("enable encryption: %w", err)
		}
		var hs *packet.ClientToServerHandshake
		if err := expect(c, &hs); err != nil {
			return err
		}
	}
	return c.sendPacks()
}

// verify checks the Login packet: Xbox Live sign-in when authentication is
// on, the allow list and the player limit.
func (c *Conn) verify(lg *packet.Login) (*ecdsa.PublicKey, error) {
	conf := login.Config{AllowLegacyChain: true}
	online := !c.l.conf.AuthenticationDisabled
	if online {
		ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
		ver, err := c.l.verifier.Get(ctx)
		cancel()
		if err != nil {
			c.disconnect(msgAuthDown)
			return nil, fmt.Errorf("token verifier: %w", err)
		}
		conf.Verifier = ver
	}
	id, cd, res, err := login.ParseWithConfig(lg.ConnectionRequest, conf)
	if err != nil {
		c.disconnect(msgLoginFailed)
		return nil, fmt.Errorf("verify login: %w", err)
	}
	if online && !res.XBOXLiveAuthenticated {
		c.disconnect(msgXboxRequired)
		return nil, errors.New("not signed in to Xbox Live")
	}
	c.id, c.cd, c.authenticated = id, cd, res.XBOXLiveAuthenticated
	c.c.SetLoginData(id, cd)
	c.log = c.log.With("name", id.DisplayName)
	if allow := c.l.conf.Allow; allow != nil {
		if msg, ok := allow(c.RemoteAddr(), id, cd); !ok {
			c.disconnect(msg)
			return nil, errors.New("not allowed: " + msg)
		}
	}
	if m := c.l.conf.MaximumPlayers; m != 0 && int(c.l.count.Load()) > m {
		_ = c.c.WritePacket(&packet.PlayStatus{Status: packet.PlayStatusLoginFailedServerFull})
		_ = c.c.Flush()
		return nil, errors.New(msgServerFull)
	}
	return res.PublicKey, nil
}

// encrypt sends the server's handshake and enables encryption, as
// gophertunnel's Conn.enableEncryption.
func (c *Conn) encrypt(client *ecdsa.PublicKey) error {
	if client == nil {
		return errors.New("no client public key")
	}
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return err
	}
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Key: key, Algorithm: jose.ES384}, &jose.SignerOptions{
		ExtraHeaders: map[jose.HeaderKey]any{"x5u": login.MarshalPublicKey(&key.PublicKey)},
	})
	if err != nil {
		return err
	}
	token, err := jwt.Signed(signer).Claims(struct {
		Salt string `json:"salt"`
	}{base64.RawStdEncoding.EncodeToString(salt[:])}).Serialize()
	if err != nil {
		return err
	}
	if err := c.write(&packet.ServerToClientHandshake{JWT: []byte(token)}); err != nil {
		return err
	}
	priv, err := key.ECDH()
	if err != nil {
		return err
	}
	pub, err := client.ECDH()
	if err != nil {
		return err
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		return err
	}
	c.c.EnableEncryption(sha256.Sum256(append(salt[:], shared...)))
	return nil
}

// sendPacks sends the resource pack list and the packs the client asks
// for, until the client is done.
func (c *Conn) sendPacks() error {
	_ = c.c.WritePacket(&packet.PlayStatus{Status: packet.PlayStatusLoginSuccess})
	packs := c.l.conf.ResourcePacks
	info := &packet.ResourcePacksInfo{TexturePackRequired: c.l.conf.TexturePacksRequired}
	for _, p := range packs {
		tp := protocol.TexturePackInfo{UUID: p.UUID(), Version: p.Version(), Size: uint64(p.Len()), DownloadURL: p.DownloadURL()}
		if p.Encrypted() {
			tp.ContentKey = p.ContentKey()
			tp.ContentIdentity = p.Manifest().Header.UUID.String()
		}
		info.TexturePacks = append(info.TexturePacks, tp)
	}
	if err := c.write(info); err != nil {
		return err
	}
	var queue []*resource.Pack
	for {
		pk, err := c.c.ReadPacket()
		if err != nil {
			return err
		}
		switch pk := pk.(type) {
		case *packet.ClientCacheStatus:
			c.cacheEnabled.Store(pk.Enabled)
		case *packet.ResourcePackChunkRequest:
			if err := c.sendChunk(queue, pk); err != nil {
				return err
			}
		case *packet.ResourcePackClientResponse:
			switch pk.Response {
			case packet.PackResponseRefused:
				return errors.New("client refused resource packs")
			case packet.PackResponseSendPacks:
				queue = queue[:0]
				for _, want := range pk.PacksToDownload {
					p, ok := findPack(packs, want)
					if !ok {
						return fmt.Errorf("client asked for unknown pack %q", want)
					}
					queue = append(queue, p)
					if err := c.c.WritePacket(dataInfo(p)); err != nil {
						return err
					}
				}
				if err := c.c.Flush(); err != nil {
					return err
				}
			case packet.PackResponseAllPacksDownloaded:
				stack := &packet.ResourcePackStack{BaseGameVersion: protocol.CurrentVersion,
					Experiments: []protocol.ExperimentData{{Name: "cameras", Enabled: true}}}
				for _, p := range packs {
					stack.TexturePacks = append(stack.TexturePacks, protocol.StackResourcePack{UUID: p.UUID().String(), Version: p.Version()})
				}
				if err := c.write(stack); err != nil {
					return err
				}
			case packet.PackResponseCompleted:
				return nil
			default:
				return fmt.Errorf("unknown resource pack response %d", pk.Response)
			}
		}
	}
}

func findPack(packs []*resource.Pack, id string) (*resource.Pack, bool) {
	u, v, hasV := strings.Cut(id, "_")
	for _, p := range packs {
		if p.UUID().String() == u && (!hasV || v == "" || v == p.Version()) {
			return p, true
		}
	}
	return nil, false
}

// dataInfo describes a pack before it is sent, as gophertunnel's
// resourcePackQueue.NextPack.
func dataInfo(p *resource.Pack) *packet.ResourcePackDataInfo {
	var t byte
	switch {
	case p.HasWorldTemplate():
		t = packet.ResourcePackTypeWorldTemplate
	case p.HasTextures() && (p.HasBehaviours() || p.HasScripts()):
		t = packet.ResourcePackTypeAddon
	case !p.HasTextures() && (p.HasBehaviours() || p.HasScripts()):
		t = packet.ResourcePackTypeBehaviour
	case p.HasTextures():
		t = packet.ResourcePackTypeResources
	default:
		t = packet.ResourcePackTypeSkins
	}
	sum := p.Checksum()
	return &packet.ResourcePackDataInfo{
		UUID: p.UUID().String() + "_" + p.Version(), DataChunkSize: packChunkSize,
		ChunkCount: uint32(p.DataChunkCount(packChunkSize)), Size: uint64(p.Len()),
		Hash: sum[:], PackType: t,
	}
}

func (c *Conn) sendChunk(queue []*resource.Pack, pk *packet.ResourcePackChunkRequest) error {
	u, _, _ := strings.Cut(pk.UUID, "_")
	for _, p := range queue {
		if p.UUID().String() != u {
			continue
		}
		off := uint64(pk.ChunkIndex) * packChunkSize
		if off >= uint64(p.Len()) {
			return fmt.Errorf("pack %s: chunk %d out of range", u, pk.ChunkIndex)
		}
		data := make([]byte, packChunkSize)
		n, err := p.ReadAt(data, int64(off))
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read pack chunk: %w", err)
		}
		return c.write(&packet.ResourcePackChunkData{UUID: pk.UUID, ChunkIndex: uint32(pk.ChunkIndex), DataOffset: off, Data: data[:n]})
	}
	return fmt.Errorf("chunk asked for pack %q that is not being sent", pk.UUID)
}

// StartGameContext sends the world to the client and waits until it has
// spawned. Packets the client sends meanwhile are kept for ReadPacket.
func (c *Conn) StartGameContext(ctx context.Context, data GameData) error {
	if data.WorldName == "" {
		data.WorldName = c.l.status().ServerName
	}
	c.gameData = data
	if len(data.Dimensions) > 0 {
		_ = c.c.WritePacket(&packet.DimensionData{Definitions: data.Dimensions})
	}
	_ = c.c.WritePacket(&packet.JigsawStructureData{StructureData: map[string]any{
		"processors": []map[string]any{}, "template_pools": []map[string]any{},
		"jigsaws": []map[string]any{}, "structure_sets": []map[string]any{},
	}})
	_ = c.c.WritePacket(&packet.VoxelShapes{})
	_ = c.c.WritePacket(startGame(data))
	if err := c.write(&packet.ItemRegistry{Items: data.Items}); err != nil {
		return err
	}

	type result struct{ err error }
	done := make(chan result, 1)
	go func() { done <- result{c.spawn()} }()
	select {
	case r := <-done:
		return r.err
	case <-ctx.Done():
		_ = c.Close()
		<-done
		return fmt.Errorf("start game: %w", ctx.Err())
	}
}

// spawn reads until the client has spawned, answering its chunk radius
// request.
func (c *Conn) spawn() error {
	for {
		pk, err := c.c.ReadPacket()
		if err != nil {
			return err
		}
		switch pk := pk.(type) {
		case *packet.RequestChunkRadius:
			if pk.ChunkRadius < 1 {
				return fmt.Errorf("chunk radius %d", pk.ChunkRadius)
			}
			r := pk.ChunkRadius
			if gr := c.gameData.ChunkRadius; gr != 0 {
				r = gr
			}
			c.radius.Store(pk.ChunkRadius)
			_ = c.c.WritePacket(&packet.ChunkRadiusUpdated{ChunkRadius: r})
			_ = c.c.WritePacket(&packet.PlayStatus{Status: packet.PlayStatusPlayerSpawn})
			if err := c.c.Flush(); err != nil {
				return err
			}
		case *packet.SetLocalPlayerAsInitialised:
			if pk.EntityRuntimeID != c.gameData.EntityRuntimeID {
				return fmt.Errorf("spawned with runtime ID %d, want %d", pk.EntityRuntimeID, c.gameData.EntityRuntimeID)
			}
			return nil
		case *packet.ClientCacheStatus:
			c.cacheEnabled.Store(pk.Enabled)
		default:
			c.mu.Lock()
			c.pending = append(c.pending, pk)
			c.mu.Unlock()
		}
	}
}

func startGame(d GameData) *packet.StartGame {
	return &packet.StartGame{
		Difficulty: d.Difficulty, EntityUniqueID: d.EntityUniqueID, EntityRuntimeID: d.EntityRuntimeID,
		PlayerGameMode: d.PlayerGameMode, PlayerPosition: d.PlayerPosition, Pitch: d.Pitch, Yaw: d.Yaw,
		WorldSeed: d.WorldSeed, Dimension: d.Dimension, WorldSpawn: d.WorldSpawn,
		EditorWorldType: d.EditorWorldType, CreatedInEditor: d.CreatedInEditor, ExportedFromEditor: d.ExportedFromEditor,
		PersonaDisabled: d.PersonaDisabled, CustomSkinsDisabled: d.CustomSkinsDisabled, EmoteChatMuted: d.EmoteChatMuted,
		GameRules: d.GameRules, Time: d.Time, DayCycleLockTime: d.DayCycleLockTime, Blocks: d.CustomBlocks,
		AchievementsDisabled: true, Generator: 1, EducationFeaturesEnabled: true, MultiPlayerGame: true,
		MultiPlayerCorrelationID: uuid.NewString(), CommandsEnabled: true, WorldName: d.WorldName,
		LANBroadcastEnabled: true, PlayerMovementSettings: d.PlayerMovementSettings, WorldGameMode: d.WorldGameMode,
		Hardcore: d.Hardcore, XBLBroadcastMode: d.XBLBroadcastMode,
		ServerAuthoritativeInventory: d.ServerAuthoritativeInventory, PlayerPermissions: d.PlayerPermissions,
		Experiments: d.Experiments, ClientSideGeneration: d.ClientSideGeneration,
		ChatRestrictionLevel: d.ChatRestrictionLevel, DisablePlayerInteractions: d.DisablePlayerInteractions,
		BaseGameVersion: d.BaseGameVersion, GameVersion: protocol.CurrentVersion,
		UseBlockNetworkIDHashes: d.UseBlockNetworkIDHashes, PropertyData: d.PropertyData,
	}
}

// ReadPacket returns the next packet from the client.
func (c *Conn) ReadPacket() (packet.Packet, error) {
	c.mu.Lock()
	if len(c.pending) > 0 {
		pk := c.pending[0]
		c.pending = c.pending[1:]
		c.mu.Unlock()
		return pk, nil
	}
	c.mu.Unlock()
	return c.c.ReadPacket()
}

// WritePacket queues a packet; it is sent at the next flush.
func (c *Conn) WritePacket(pk packet.Packet) error { return c.c.WritePacket(pk) }

// Flush sends the queued packets.
func (c *Conn) Flush() error { return c.c.Flush() }

// Close closes the connection.
func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.c.Close()
		c.l.count.Add(-1)
	})
	return err
}

// IdentityData returns who the player is, from the Login packet.
func (c *Conn) IdentityData() login.IdentityData { return c.id }

// ClientData returns what the client said about itself at login.
func (c *Conn) ClientData() login.ClientData { return c.cd }

// Authenticated reports whether Xbox Live vouched for the player.
func (c *Conn) Authenticated() bool { return c.authenticated }

// ClientCacheEnabled reports whether the client caches chunk blobs.
func (c *Conn) ClientCacheEnabled() bool { return c.cacheEnabled.Load() }

// ChunkRadius returns the chunk radius the client asked for at spawn.
func (c *Conn) ChunkRadius() int { return int(c.radius.Load()) }

// RemoteAddr returns the client's address.
func (c *Conn) RemoteAddr() net.Addr { return c.c.RemoteAddr() }

// Latency returns the transport's measured latency, or 0 if it has none.
func (c *Conn) Latency() time.Duration {
	if l, ok := c.c.Transport().(interface{ Latency() time.Duration }); ok {
		return l.Latency()
	}
	return 0
}

// Hooks returns the connection's packet hooks, for tools that watch or
// change packets.
func (c *Conn) Hooks() *conn.Hooks { return c.c.Hooks() }

func (c *Conn) disconnect(msg string) {
	_ = c.c.WritePacket(&packet.Disconnect{Message: msg})
	_ = c.c.Flush()
}

func (c *Conn) write(pk packet.Packet) error {
	if err := c.c.WritePacket(pk); err != nil {
		return fmt.Errorf("write %T: %w", pk, err)
	}
	return c.c.Flush()
}

// expect reads the next packet into *dst, failing if it has another type.
func expect[T packet.Packet](c *Conn, dst *T) error {
	pk, err := c.c.ReadPacket()
	if err != nil {
		return err
	}
	v, ok := pk.(T)
	if !ok {
		return fmt.Errorf("expected %T, got %T", *dst, pk)
	}
	*dst = v
	return nil
}
