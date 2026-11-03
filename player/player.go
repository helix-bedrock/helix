// Package player is everything a player is and does on a Helix server: the
// Player (session, entity, saved data), joining and the packets it reads
// and is sent, its inventory, abilities, attributes and effects, and its
// gameplay (block breaking and placing, containers), each step a hook in
// the Setup. The Host runs the players of one server and owns the player
// list.
//
// Files by job: player.go (the Player), host.go (admitting players, the
// Host), join.go (joining and the read loop), send.go (sending packets),
// data.go (saved data), view.go (what other players see), zones.go (the
// chunks around it), list.go (the player list), setup.go (handlers),
// input.go (the input queue), and the gameplay files.
package player

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/mcdb"
)

// Player is a player online. It is three parts (API_DESIGN.md, "Players and
// entities"): the session (the connection, identity and skin, safe from
// anywhere), the entity in the world (owned by its region) and the profile
// (server-wide facts, owned by the global owner).
type Player struct {
	s    *Host
	conn *network.Conn

	// recipeMu keeps recipe rule edits and sends in order. recipeRules and
	// recipeCache are this player's recipes. Runs: anywhere.
	recipeMu    sync.Mutex
	recipeRules atomic.Pointer[recipeRules]
	recipeCache atomic.Pointer[recipeView]

	// Session: fixed once the player joined. Runs: anywhere.
	uuid      uuid.UUID
	name      string
	xuid      string
	skin      protocol.Skin
	ids       mcdb.PlayerIDs
	uniqueID  int64
	runtimeID uint64
	// sendErrLogged is set by the first failed send, so a dead connection
	// is logged once.
	sendErrLogged atomic.Bool

	// Entity: the player's body in the world.
	entity *world.Entity
	viewer *world.Viewer
	// input carries world packets from the network goroutine to the
	// region's tick.
	input inputQueue
	// flush sends what was queued for the client. Runs: anywhere.
	flush func()
	zone  zoneState

	// saved is the player's data as BDS saved it, written back with what
	// Helix changed so nothing Helix does not use yet is lost.
	saved map[string]any

	// Gameplay: owned by the player's region once it joined.
	gameMode   int32
	abilities  Abilities
	attributes Attributes
	inventory  Inventory
	requests   requestState
	effects    []ActiveEffect
	brk        breakState
	move       moveState
	window     windowState
	using      usingState
	food       foodState
	hurt       hurtState
	// respawn is how far the respawn sequence got (respawnNone when alive).
	respawn int
	// lastUse is what the first click of the held button did, a use constant.
	lastUse int
	// touching is the pick-up scan's buffer; shownHeld and shownSlot are what
	// other players were last told it holds.
	touching  []*world.Entity
	shownHeld ItemStack
	shownSlot uint8
	// breakSpeed multiplies the player's break speed; reachScale its
	// reach. Both 1 by default.
	breakSpeed float32
	// tuned is the numbers the player has of its own; nil when it has none.
	tuned *playerTuning
	// lastLevelSound is the tick of the last level sound.
	lastLevelSound int64
	reachScale     float32
	// rng is the player's random source (item wear).
	rng *rand.Rand
}

// eyeHeight is how far above its feet a player's position is: the client
// sends and is sent eye positions.
const eyeHeight = 1.62

// newPlayer builds the session of a connection that finished the login.
func (s *Host) newPlayer(c *network.Conn, id uuid.UUID) (*Player, error) {
	skin, err := c.Client().Skin()
	if err != nil {
		return nil, fmt.Errorf("skin: %w", err)
	}
	ident := c.Identity()
	p := &Player{
		s: s, conn: c, uuid: id, name: ident.DisplayName, xuid: ident.XUID, skin: skin,
		ids: playerIDs(ident.Identity, c.Client().SelfSignedID, ident.XUID), runtimeID: s.runtimeIDs.Add(1),
		gameMode: s.level.GameMode, abilities: DefaultAbilities(), attributes: DefaultAttributes(),
		breakSpeed: 1, reachScale: 1, rng: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())), //nolint:gosec // gameplay randomness
	}
	p.uniqueID = int64(p.runtimeID) //nolint:gosec // runtime IDs are small
	return p, nil
}

// Name returns the player's display name. Runs: anywhere.
func (p *Player) Name() string { return p.name }

// Host returns the Host the player is on.
func (p *Player) Host() *Host { return p.s }

// UUID returns the player's identity UUID. Runs: anywhere.
func (p *Player) UUID() uuid.UUID { return p.uuid }

// XUID returns the player's Xbox user ID, empty for offline players. Runs:
// anywhere.
func (p *Player) XUID() string { return p.xuid }

// Conn returns the player's connection, for packets. Runs: anywhere.
func (p *Player) Conn() *network.Conn { return p.conn }

// Entity returns the player's entity in the world. Runs: anywhere; its
// position and rotation are read on its region.
func (p *Player) Entity() *world.Entity { return p.entity }

// SetGameMode changes the player's game mode and the abilities that come
// with it, and tells the player and everyone online, as BDS's
// setPlayerGameType: UpdatePlayerGameType to all (BDS never sends
// SetPlayerGameType itself), then the abilities. Runs: owner of the player's
// entity.
func (p *Player) SetGameMode(mode int32) {
	p.gameMode = mode
	p.abilities.SetGameMode(mode, p.s.level.GameMode)
	abilities := &packet.UpdateAbilities{AbilityData: p.abilities.Data(p.uniqueID)}
	update := &packet.UpdatePlayerGameType{GameType: mode, PlayerUniqueID: p.uniqueID}
	if p.entity != nil {
		update.Tick = p.entity.Tick()
	}
	p.send(update, abilities)
	p.flush()
	p.broadcast(update, abilities)
}

// SetBreakSpeed sets the multiplier on the player's break speed (1 by
// default). Runs: the player's region.
func (p *Player) SetBreakSpeed(m float32) { p.breakSpeed = m }

// SetReachScale sets the multiplier on the player's break and place reach
// (1 by default). Runs: the player's region.
func (p *Player) SetReachScale(m float32) { p.reachScale = m }
