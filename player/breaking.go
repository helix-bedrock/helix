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
	levelEventUpdateCracking   = 3602
	levelEventDestroyParticles = 2001
)

const (
	// hitSoundTicks is BDS's GameMode 200 ms between hit sounds. The next
	// one plays once the clock has passed the wait, so every fifth tick.
	hitSoundTicks = 4
	// breakCooldownTicks is BDS's GameMode 250 ms after a block that did
	// not break at once, in which the next block makes no progress.
	breakCooldownTicks = 5
)

// breakState is a player's block breaking, BDS's "destroying" flag and the
// GameMode's position, face, progress and clock. As in BDS with
// server-authoritative breaking, pos stays after breaking stops and after a
// block breaks: it is where the next crack is stopped. Owned by the player's
// region.
type breakState struct {
	active   bool
	pos      cube.Pos
	face     int32
	progress float32
	// target and targetFace are the block this tick's breaking aims at;
	// breaking moves there if it is not pos.
	target     cube.Pos
	targetFace int32
	// clock counts the ticks of breaking, BDS's GameMode clock.
	clock uint64
	// cooldown is the clock when progress may go on after a block broke.
	cooldown uint64
	// nextHit is the clock the hit sound must pass to play again.
	nextHit uint64
	// crack is the speed the crack animation was last sent, as LevelEvent data.
	crack int32
	// broken counts the blocks the player broke, so a tick can tell
	// whether the BreakTick hook broke one.
	broken uint64
}

// BlockBreakEvent fires before a player's block break happens. Cancel
// keeps the block; the client is sent it back.
type BlockBreakEvent struct {
	Cancellable
	Player *Player
	Pos    cube.Pos
	// Block is the state being broken.
	Block uint32
	// Becomes is what is left in its place: air unless changed.
	Becomes uint32
	// Wear is the damage the held item takes; change it to wear the tool more,
	// less or not at all.
	Wear int32
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
	creative := p.creative()
	hadContinue, predicted, destroyed := false, false, false
	var predictPos cube.Pos
	var predictFace int32
	for _, a := range actions {
		pos := toCube(a.BlockPos)
		switch a.Action {
		case protocol.PlayerActionStartBreak:
			p.attackCircuit(r, pos)
			p.hitLectern(r, pos)
			if p.startBreak(r, pos, a.Face) {
				destroyed = true
			}
		case protocol.PlayerActionAbortBreak:
			p.stopBreak()
			levelEvent(r, pos, blockVec(pos), levelEventStopCracking, 0)
		case protocol.PlayerActionStopBreak:
			levelEvent(r, cube.Pos{}, mgl32.Vec3{}, levelEventStopCracking, 0)
		case protocol.PlayerActionPredictDestroyBlock:
			predicted, predictPos, predictFace = true, pos, a.Face
			if hadContinue && creative && p.creativeDestroy(r, pos) {
				destroyed = true
			}
		case protocol.PlayerActionContinueDestroyBlock:
			if !creative {
				levelEvent(r, p.brk.pos, blockVec(p.brk.pos), levelEventStopCracking, 0)
				p.startCrack(r, pos)
			}
			hadContinue = true
			if p.serverBreakTick(pos, a.Face) {
				destroyed = true
			}
		}
	}
	if !hadContinue && p.serverBreakTick(p.brk.pos, p.brk.face) {
		destroyed = true
	}
	if predicted && !destroyed {
		p.resendAround(r, predictPos, predictFace)
	}
}

// attackCircuit tells redstone the block at pos was hit, for blocks such as note blocks
// that answer a hit.
func (p *Player) attackCircuit(r *world.Region, pos cube.Pos) {
	attack := p.s.circuits.Attack
	if attack == nil {
		return
	}
	if b, ok := r.Block(pos, 0); ok {
		attack(r, pos, b)
	}
}

// creativeDestroy breaks the block at pos at once, as a creative player's predicted
// destroy after a continue does when the block is in reach and not air. True if it broke.
func (p *Player) creativeDestroy(r *world.Region, pos cube.Pos) bool {
	if !p.inReach(pos) {
		return false
	}
	b, ok := r.Block(pos, 0)
	return ok && b != p.s.reg.Blocks.Air() && p.s.game.sys.BreakFinish(p, pos)
}

// startBreak is BDS's StartDestroyBlock: the crack starts for everyone
// near, and in reach the breaking starts if the BreakStart hook allows.
// Progress is left as it is: only stopping or moving to another block resets
// it. True if the block broke at once (creative, or a block with no break
// time).
func (p *Player) startBreak(r *world.Region, pos cube.Pos, face int32) bool {
	if p.inReach(pos) && (p.putOutFire(r, pos, face) || p.punchFrame(r, pos)) {
		p.stopBreak()
		return false
	}
	if !p.creative() {
		p.startCrack(r, pos)
	}
	if !p.inReach(pos) {
		return false
	}
	if !p.s.game.sys.BreakStart(p, pos, face) {
		p.stopBreak()
		return false
	}
	p.brk.active, p.brk.pos, p.brk.face = true, pos, face
	// BDS's GameMode start destroys the block now in creative and when it
	// has no break time (a player-input check for touch controls that skips
	// this is not ported). The next block waits the cooldown.
	b, ok := p.breakInfo(r, pos)
	if !ok || b.Block == p.s.reg.Blocks.Air() {
		return false
	}
	if !p.creative() && (b.DestroyTime > 0 || p.s.game.sys.BreakTime(&b) < breakEnd) {
		return false
	}
	if !p.s.game.sys.BreakFinish(p, pos) {
		return false
	}
	p.brk.cooldown = p.brk.clock + breakCooldownTicks
	return true
}

// startCrack starts the crack animation at pos, at the speed of the rate now.
func (p *Player) startCrack(r *world.Region, pos cube.Pos) {
	p.brk.crack = crackData(p.breakRate(r, pos))
	levelEvent(r, pos, blockVec(pos), levelEventStartCracking, p.brk.crack)
}

// updateCrack tells clients the crack's new speed when the rate has changed.
// Clients run the animation themselves from the speed they were sent, so
// landing after starting a block in the air would leave it far behind the
// real progress. This event is an extra: from the code read so far, BDS sends
// it only without server-authoritative breaking.
func (p *Player) updateCrack(r *world.Region, rate float32) {
	if data := crackData(rate); data != p.brk.crack {
		p.brk.crack = data
		levelEvent(r, p.brk.pos, blockVec(p.brk.pos), levelEventUpdateCracking, data)
	}
}

// stopBreak stops the breaking: BDS's "destroying" flag and GameMode stop.
// The position stays.
func (p *Player) stopBreak() {
	p.brk.active, p.brk.progress, p.brk.cooldown = false, 0, 0
}

// serverBreakTick is BDS's server tick of block breaking, once per
// PlayerAuthInput, aimed at pos: only a player who started breaking goes on,
// out of reach stops it, else the clock moves and the BreakTick hook runs.
// True if a block broke.
func (p *Player) serverBreakTick(pos cube.Pos, face int32) bool {
	if !p.brk.active || p.creative() {
		return false
	}
	if !p.inReach(pos) {
		p.stopBreak()
		return false
	}
	p.brk.target, p.brk.targetFace = pos, face
	p.brk.clock++
	before := p.brk.broken
	p.s.game.sys.BreakTick(p)
	return p.brk.broken != before
}

// vanillaBreakTick is BDS's GameMode continue. Another block starts over
// from 0, and adds no progress this tick. On the same block progress grows by
// the rate each tick, unless the cooldown of the last block is still running,
// and the block breaks at 0.99999. Whether or not it really broke, progress
// starts over and the cooldown starts, unless the block took a single tick.
func vanillaBreakTick(p *Player) {
	r := p.entity.Region()
	if r == nil {
		return
	}
	brk := &p.brk
	if brk.target != brk.pos {
		if !p.s.game.sys.BreakStart(p, brk.target, brk.targetFace) {
			p.stopBreak()
			return
		}
		brk.pos, brk.progress = brk.target, 0
		return
	}
	if b, ok := r.Block(brk.pos, 0); !ok || b == p.s.reg.Blocks.Air() {
		return
	}
	rate := p.breakRate(r, brk.pos)
	p.updateCrack(r, rate)
	if brk.clock < brk.cooldown {
		return
	}
	brk.progress += rate
	p.hitSound(r)
	if brk.progress < breakEnd {
		return
	}
	p.s.game.sys.BreakFinish(p, brk.pos)
	brk.progress, brk.cooldown = 0, brk.clock
	if rate < breakEnd {
		brk.cooldown += breakCooldownTicks
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
	var wear int32
	if !p.creative() {
		wear = g.sys.Wear(&b)
	}
	becomes := p.s.reg.Blocks.Air()
	if g.breakEv.Has() {
		e := BlockBreakEvent{Player: p, Pos: pos, Block: b.Block, Becomes: becomes, Wear: wear}
		g.breakEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
		becomes, wear = e.Becomes, e.Wear
	}
	p.s.brokenTNT(r, pos, b.Block, p)
	p.hitLectern(r, pos)
	p.breakDoublePlantLower(r, pos, b.Block)
	if destroy := p.s.circuits.Destroy; destroy != nil {
		destroy(r, pos, b.Block, p.inventory.main[p.inventory.held].Name)
	}
	p.hurtHeld(wear)
	drops, xp := p.blockDrops(r, pos, b.Block)
	if !r.SetBlock(pos, 0, becomes) {
		return false
	}
	r.UpdateNeighbours(pos)
	if k := p.s.knotAt(r, pos); k != nil {
		k.Break(r)
	}
	g.sys.Exhaust(p, p.Tuning(TuneExhaustMine))
	levelEvent(r, pos, blockCenter(pos), levelEventDestroyParticles, int32(p.s.reg.Blocks.Hash(b.Block))) //nolint:gosec // hashes are sent as int32
	p.s.postBlockGameEvent(r, gameEventBlockDestroy, pos, p, b.Block, true)
	p.s.throwDrops(r, drops)
	p.s.SpawnExperience(r, blockVec(pos), xp)
	p.brk.broken++
	return true
}

// breakDoublePlantLower is a double plant's playerWillDestroy (BDS 0xCB1AEB0): a creative
// player breaking the upper half takes the lower one with it, with its break particles and no
// drops. Otherwise the other half breaks a tick later, with its drops, as it can no longer
// stand.
func (p *Player) breakDoublePlantLower(r *world.Region, pos cube.Pos, block uint32) {
	f := p.s.farm
	if f == nil || !p.creative() || !f.DoublePlantUpper(block) {
		return
	}
	at := pos.Side(cube.FaceDown)
	lower, ok := r.Block(at, 0)
	if !ok || p.s.reg.Blocks.Name(lower) != p.s.reg.Blocks.Name(block) || f.DoublePlantUpper(lower) {
		return
	}
	levelEvent(r, at, blockCenter(at), levelEventDestroyParticles, int32(p.s.reg.Blocks.Hash(lower))) //nolint:gosec // hashes are sent as int32
	if r.SetBlock(at, 0, p.s.reg.Blocks.Air()) {
		r.UpdateNeighbours(at)
	}
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

// hitSound plays BDS's hit sound of the block being broken, once the wait of
// hitSoundTicks has passed, to everyone near but the breaking player, whose
// client plays it itself (BDS's predictive synchronised sound). The wait
// carries over from block to block, as BDS's timer does.
func (p *Player) hitSound(r *world.Region) {
	if p.brk.clock <= p.brk.nextHit {
		return
	}
	p.brk.nextHit = p.brk.clock + hitSoundTicks
	b, ok := r.Block(p.brk.pos, 0)
	if !ok {
		return
	}
	playSound(r, p.brk.pos, p.s.blockSound(packet.SoundEventHit, blockCenter(p.brk.pos), b), p.viewer)
}
