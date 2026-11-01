package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// BDS 1.26.60's reach for breaking and placing, in blocks, to the block's
// corner (GameMode pick range).
const (
	ReachSurvival float32 = 6.7
	ReachCreative float32 = 12
)

// BDS's LevelEvents for block breaking.
const (
	levelEventStartCracking    = 3600
	levelEventStopCracking     = 3601
	levelEventDestroyParticles = 2001
)

// hitSoundTicks is how often the hit sound plays while breaking: BDS's
// GameMode waits 200 ms between them.
const hitSoundTicks = 4

// breakState is a player's block breaking, BDS's "destroying" flag and the
// GameMode's position, face and progress. Owned by the player's region.
type breakState struct {
	active   bool
	pos      cube.Pos
	face     int32
	progress float32
	// broken counts the blocks the player broke, so a tick can tell
	// whether the BreakTick hook broke one.
	broken uint64
	// hitWait counts the ticks until the next hit sound.
	hitWait int
}

// BlockBreakEvent fires before a player's block break happens. Cancel
// keeps the block; the client is sent it back.
type BlockBreakEvent struct {
	Cancellable
	Player *Player
	Pos    cube.Pos
	// Block is the state being broken.
	Block uint32
}

// BreakStartFunc decides whether a player may start breaking the block at
// pos; vanilla allows it.
type BreakStartFunc func(p *Player, pos cube.Pos, face int32) bool

// BreakTimeFunc returns the progress one tick of breaking adds, 1 meaning
// the block breaks in one tick.
type BreakTimeFunc func(b *BreakInfo) float32

// BreakTickFunc advances a player's breaking by one tick and breaks the
// block when done.
type BreakTickFunc func(p *Player)

// BreakFinishFunc breaks the block at pos for a player; false if it did
// not break.
type BreakFinishFunc func(p *Player, pos cube.Pos) bool

// ToolSpeedFunc is the held item's own break speed (before enchantments
// and effects); it sets b.Harvest and b.Enchantable.
type ToolSpeedFunc func(b *BreakInfo) float32

// reach returns the player's reach for breaking and placing.
func (p *Player) reach() float32 {
	r := ReachSurvival
	if p.creative() {
		r = ReachCreative
	}
	return r * p.reachScale
}

// inReach is BDS's break range check: from the player's position to the
// block's lowest corner, below reach.
func (p *Player) inReach(pos cube.Pos) bool {
	d := p.entity.Position().Sub(mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])})
	r := p.reach()
	return d.Dot(d) < r*r
}

// underWater reports whether the player's head is in water.
func (p *Player) underWater() bool {
	pos := p.entity.Position()
	at := cube.Pos{int(math.Floor(float64(pos[0]))), int(math.Floor(float64(pos[1]))), int(math.Floor(float64(pos[2])))}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	for layer := uint8(0); layer < 2; layer++ {
		if b, ok := r.Block(at, layer); ok {
			switch p.s.reg.Blocks.Name(b) {
			case "minecraft:water", "minecraft:flowing_water":
				return true
			}
		}
	}
	return false
}

// blockPos is the block the player's position is in.
func (p *Player) blockPos() cube.Pos {
	pos := p.entity.Position()
	return cube.Pos{int(math.Floor(float64(pos[0]))), int(math.Floor(float64(pos[1]))), int(math.Floor(float64(pos[2])))}
}

// breakInfo describes breaking the block at pos with the held item.
func (p *Player) breakInfo(r *world.Region, pos cube.Pos) (BreakInfo, bool) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return BreakInfo{}, false
	}
	return BreakInfo{
		Player: p, Pos: pos, Block: b, Name: p.s.reg.Blocks.Name(b), Item: p.inventory.Held(),
		DestroyTime: p.s.reg.Blocks.Properties(b).DestroyTime,
	}, true
}

// breakRate is the progress a tick of breaking the block at pos adds.
func (p *Player) breakRate(r *world.Region, pos cube.Pos) float32 {
	b, ok := p.breakInfo(r, pos)
	if !ok {
		return 0
	}
	return p.s.game.sys.BreakTime(&b)
}

// levelEvent queues a LevelEvent for the players who have its chunk.
func levelEvent(r *world.Region, at cube.Pos, pos mgl32.Vec3, event, data int32) {
	r.Broadcast(at, &packet.LevelEvent{EventType: event, Position: pos, EventData: data}, nil)
}

func blockVec(pos cube.Pos) mgl32.Vec3 {
	return mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])}
}

func toCube(pos protocol.BlockPos) cube.Pos { return cube.Pos{int(pos[0]), int(pos[1]), int(pos[2])} }

// crackData is BDS's LevelEvent 3600 data: the rate scaled to 0-65535,
// floored.
func crackData(rate float32) int32 {
	return int32(math.Floor(float64(rate * 65535)))
}

// handleBlockActions runs PlayerAuthInput's block actions in BDS 1.26.60's
// order (server-authoritative breaking), before movement.
func (p *Player) handleBlockActions(actions []protocol.PlayerBlockAction) {
	if p.entity == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	g := p.s.game
	creative := p.creative()
	hadContinue, predicted, destroyed := false, false, false
	var predictPos cube.Pos
	var predictFace int32
	for _, a := range actions {
		pos := toCube(a.BlockPos)
		switch a.Action {
		case protocol.PlayerActionStartBreak:
			p.startBreak(r, pos, a.Face)
		case protocol.PlayerActionAbortBreak:
			p.stopBreak()
			levelEvent(r, pos, blockVec(pos), levelEventStopCracking, 0)
		case protocol.PlayerActionStopBreak:
			levelEvent(r, cube.Pos{}, mgl32.Vec3{}, levelEventStopCracking, 0)
		case protocol.PlayerActionPredictDestroyBlock:
			predicted, predictPos, predictFace = true, pos, a.Face
			if hadContinue && creative && p.inReach(pos) {
				if b, ok := r.Block(pos, 0); ok && b != p.s.reg.Blocks.Air() && g.sys.BreakFinish(p, pos) {
					destroyed = true
				}
			}
		case protocol.PlayerActionContinueDestroyBlock:
			if !creative {
				levelEvent(r, p.brk.pos, blockVec(p.brk.pos), levelEventStopCracking, 0)
				levelEvent(r, pos, blockVec(pos), levelEventStartCracking, crackData(p.breakRate(r, pos)))
			}
			if !p.brk.active || p.brk.pos != pos || p.brk.face != a.Face {
				p.startBreak(r, pos, a.Face)
			}
			hadContinue = true
			if p.serverBreakTick() {
				destroyed = true
			}
		}
	}
	if !hadContinue && p.serverBreakTick() {
		destroyed = true
	}
	if predicted && !destroyed {
		p.resendAround(r, predictPos, predictFace)
	}
}

// startBreak is BDS's StartDestroyBlock: the crack starts for everyone
// near, and in reach the breaking starts if the BreakStart hook allows.
func (p *Player) startBreak(r *world.Region, pos cube.Pos, face int32) {
	if !p.creative() {
		levelEvent(r, pos, blockVec(pos), levelEventStartCracking, crackData(p.breakRate(r, pos)))
	}
	if !p.inReach(pos) {
		return
	}
	if !p.s.game.sys.BreakStart(p, pos, face) {
		p.stopBreak()
		return
	}
	p.brk.active, p.brk.pos, p.brk.face, p.brk.progress = true, pos, face, 0
}

// stopBreak clears the breaking.
func (p *Player) stopBreak() {
	p.brk.active, p.brk.pos, p.brk.face, p.brk.progress = false, cube.Pos{}, 0, 0
}

// serverBreakTick is BDS's server tick of block breaking, once per
// PlayerAuthInput: out of reach stops it, else the BreakTick hook runs.
// True if a block broke.
func (p *Player) serverBreakTick() bool {
	if !p.brk.active || p.creative() {
		return false
	}
	if !p.inReach(p.brk.pos) {
		p.stopBreak()
		return false
	}
	before := p.brk.broken
	p.s.game.sys.BreakTick(p)
	return p.brk.broken != before
}

// vanillaBreakTick is BDS's GameMode continue: progress grows by the rate
// each tick and the block breaks at 0.99999.
func vanillaBreakTick(p *Player) {
	r := p.entity.Region()
	if r == nil {
		return
	}
	p.brk.progress += p.breakRate(r, p.brk.pos)
	p.hitSound(r)
	if p.brk.progress >= breakEnd {
		if p.s.game.sys.BreakFinish(p, p.brk.pos) {
			p.stopBreak()
		}
	}
}

// vanillaBreakFinish is BDS's GameMode destroy: a survival player must have
// finished breaking (or the block breaks at once), then the BlockBreak
// event, the block becomes air, and LevelEvent 2001 at the block's centre
// with its ID, from which clients play the break particles and sound (BDS
// sends no separate break sound).
func vanillaBreakFinish(p *Player, pos cube.Pos) bool {
	r := p.entity.Region()
	if r == nil {
		return false
	}
	b, ok := p.breakInfo(r, pos)
	if !ok || b.Block == p.s.reg.Blocks.Air() {
		return false
	}
	if !p.creative() && p.brk.progress < breakEnd && !(b.DestroyTime <= 0 && p.s.game.sys.BreakTime(&b) >= breakEnd) {
		return false
	}
	g := p.s.game
	if g.breakEv.Has() {
		e := BlockBreakEvent{Player: p, Pos: pos, Block: b.Block}
		g.breakEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	if !p.creative() {
		p.hurtHeld(g.sys.Wear(&b))
	}
	if !r.SetBlock(pos, 0, p.s.reg.Blocks.Air()) {
		return false
	}
	levelEvent(r, pos, blockVec(pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}), levelEventDestroyParticles, int32(p.s.reg.Blocks.Hash(b.Block))) //nolint:gosec // hashes are sent as int32
	p.brk.broken++
	return true
}

// resendAround is BDS's answer to a destroy the client predicted but the
// server did not do: the block, the block above it, the neighbour on the
// face and the block above that, sent to this player only.
func (p *Player) resendAround(r *world.Region, pos cube.Pos, face int32) {
	side := pos.Side(cube.Face(face))
	for _, at := range [...]cube.Pos{pos, pos.Side(cube.FaceUp), side, side.Side(cube.FaceUp)} {
		p.sendBlock(r, at)
	}
	p.flush()
}

// hitSound plays BDS's hit sound of the block being broken, at most every
// hitSoundTicks, to everyone near but the breaking player, whose client
// plays it itself (BDS's predictive synchronised sound). The wait carries
// over from block to block, as BDS's timer does.
func (p *Player) hitSound(r *world.Region) {
	if p.brk.hitWait > 0 {
		p.brk.hitWait--
		return
	}
	p.brk.hitWait = hitSoundTicks - 1
	b, ok := r.Block(p.brk.pos, 0)
	if !ok {
		return
	}
	r.Broadcast(p.brk.pos, &packet.LevelSoundEvent{
		SoundType: packet.SoundEventHit, Position: blockVec(p.brk.pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}),
		ExtraData: int32(p.s.reg.Blocks.Hash(b)), //nolint:gosec // hashes are sent as int32
	}, p.viewer)
}
