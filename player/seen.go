package player

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// PlayerState is what anything may read about a player from any region: where it is and
// which way it looks, its game mode and level, as of the end of its last tick.
type PlayerState struct {
	// Position is the player's feet.
	Position mgl64.Vec3
	Rotation world.Rotation
	GameMode int32
	Level    int32
	// Region is the region the player was in; compare it with your own to know whether
	// the player's live fields are yours to read.
	Region *world.Region
}

// seenState is a PlayerState kept for other regions: a sequence lock over atomic words,
// so writing allocates nothing and readers never see half an update.
type seenState struct {
	seq              atomic.Uint32
	x, y, z          atomic.Uint64
	yaw, pitch, head atomic.Uint32
	gameMode, level  atomic.Int32
	region           atomic.Pointer[world.Region]
}

// store publishes s. Only the player's owner writes.
func (st *seenState) store(s PlayerState) {
	st.seq.Add(1)
	st.x.Store(math.Float64bits(s.Position[0]))
	st.y.Store(math.Float64bits(s.Position[1]))
	st.z.Store(math.Float64bits(s.Position[2]))
	st.yaw.Store(math.Float32bits(s.Rotation.Yaw))
	st.pitch.Store(math.Float32bits(s.Rotation.Pitch))
	st.head.Store(math.Float32bits(s.Rotation.HeadYaw))
	st.gameMode.Store(s.GameMode)
	st.level.Store(s.Level)
	st.region.Store(s.Region)
	st.seq.Add(1)
}

// load reads a whole update, trying again while one is being written.
func (st *seenState) load() PlayerState {
	for {
		before := st.seq.Load()
		if before%2 != 0 {
			continue
		}
		s := PlayerState{
			Position: mgl64.Vec3{math.Float64frombits(st.x.Load()), math.Float64frombits(st.y.Load()), math.Float64frombits(st.z.Load())},
			Rotation: world.Rotation{Yaw: math.Float32frombits(st.yaw.Load()), Pitch: math.Float32frombits(st.pitch.Load()), HeadYaw: math.Float32frombits(st.head.Load())},
			GameMode: st.gameMode.Load(), Level: st.level.Load(), Region: st.region.Load(),
		}
		if st.seq.Load() == before {
			return s
		}
	}
}

// State is what the player was at the end of its last tick. Unlike the player's other
// getters it may be called from any region, so commands, selectors and plugins can look
// at every player online. Runs: anywhere.
func (p *Player) State() PlayerState { return p.seen.load() }

// publishState updates State. Runs: the player's region, at the end of its tick.
func (p *Player) publishState() {
	if p.entity == nil {
		return
	}
	p.seen.store(PlayerState{
		Position: p.entity.Position().Sub(mgl64.Vec3{0, eyeHeight, 0}), Rotation: p.entity.Rotation(),
		GameMode: p.gameMode, Level: p.Level(), Region: p.entity.Region(),
	})
}

// maxQueuedRuns is how many Run calls may wait for one player's tick; more are refused,
// so a loop cannot grow the queue without end.
const maxQueuedRuns = 4096

// runQueue holds what other regions asked a player's owner to run.
type runQueue struct {
	mu  sync.Mutex
	in  []func(*Player)
	run []func(*Player)
}

// Run runs fn with the player on the player's own region, at its next tick, where fn may
// read and change everything about it: inventory, health, effects, game mode, position.
// It is how code on one region reaches a player on another; fn should hand any answer
// back the same way (Run on another player) rather than wait. It reports false if the
// queue is full. Nothing runs for a player that left before its next tick. Runs:
// anywhere.
func (p *Player) Run(fn func(p *Player)) bool {
	if fn == nil {
		return false
	}
	q := &p.runs
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.in) >= maxQueuedRuns {
		return false
	}
	q.in = append(q.in, fn)
	return true
}

// runQueued runs what Run queued. Runs: the player's region tick.
func (p *Player) runQueued() {
	q := &p.runs
	q.mu.Lock()
	q.run, q.in = q.in, q.run[:0]
	q.mu.Unlock()
	for i, fn := range q.run {
		p.s.game.safeRun(p, fn)
		q.run[i] = nil
	}
}

// safeRun runs a queued call, keeping the server up if it panics.
func (g *gameplay) safeRun(p *Player, fn func(*Player)) {
	defer func() {
		if r := recover(); r != nil {
			p.s.log.Error("Player.Run panicked", "player", p.name, "panic", r)
		}
	}()
	fn(p)
}

// FindName is the online player with a name, any case. Runs: anywhere.
func (h *Host) FindName(name string) (*Player, bool) {
	for _, p := range h.Online() {
		if strings.EqualFold(p.name, name) {
			return p, true
		}
	}
	return nil, false
}

// FindXUID is the online player with an Xbox user ID. Runs: anywhere.
func (h *Host) FindXUID(xuid string) (*Player, bool) {
	for _, p := range h.Online() {
		if p.xuid == xuid {
			return p, true
		}
	}
	return nil, false
}

// NewItem is a stack of an item by name ("apple" or "minecraft:apple"), with a block
// item's block filled in. It reports false for a name no item has, and for the
// block-only entries BDS lists without an item (double slabs, wall signs). Runs: anywhere.
func (s *Host) NewItem(name string, count int, meta int16) (ItemStack, bool) {
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	t, ok := s.reg.Items.ByName(name)
	if !ok || t.MaxStack == 0 {
		return ItemStack{}, false
	}
	it := ItemStack{Name: name, Count: uint8(min(max(count, 0), 255)), Meta: meta} //nolint:gosec // clamped
	// A block item carries its block as the creative inventory BDS sends has it; items
	// named like a block that are not block items (doors, say) carry none.
	for _, c := range s.reg.Creative.Packet().Items {
		if c.Item.NetworkID != int32(t.ID) || c.Item.BlockRuntimeID == 0 {
			continue
		}
		if id, ok := s.reg.Blocks.HashToRuntimeID(uint32(c.Item.BlockRuntimeID)); ok { //nolint:gosec // hashes are sent as int32
			it.Block = map[string]any{"name": s.reg.Blocks.Name(id), "states": s.reg.Blocks.States(id), "version": s.reg.Blocks.Version(id)}
		}
		break
	}
	return it, true
}

// MaxStack is how many of an item fit in one slot, 0 for an unknown item. Runs: anywhere.
func (s *Host) MaxStack(name string) int { return int(ItemStack{Name: name}.maxStack(s.reg)) }

// Drop throws a stack out of the player as dropping it would, or gives it back if it
// cannot be dropped, so nothing is lost. Runs: the player's region.
func (p *Player) Drop(s ItemStack) { p.dropOrGiveBack(s, false) }

// Message shows the player a line of chat text. Runs: the player's region.
func (p *Player) Message(text string) {
	p.send(&packet.Text{TextType: packet.TextTypeRaw, Message: text})
	p.flush()
}

// MessageTranslated shows the player a line in the game's words, a translation key such
// as "commands.give.successRecipient" with its parameters. Runs: the player's region.
func (p *Player) MessageTranslated(key string, params ...string) {
	p.send(&packet.Text{TextType: packet.TextTypeTranslation, NeedsTranslation: true, Message: key, Parameters: params})
	p.flush()
}

// SetPermission sets the player's permission level (PermissionVisitor, Member, Operator
// or Custom) and the command level that comes with it, as BDS does, and sends the player
// its new abilities and the commands it may now run. Runs: the player's region.
func (p *Player) SetPermission(level uint8) {
	p.abilities.SetPermission(level)
	abilities := &packet.UpdateAbilities{AbilityData: p.abilities.Data(p.uniqueID)}
	p.send(abilities, p.commandsPacket())
	p.flush()
	p.broadcast(abilities)
}
