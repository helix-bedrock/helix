package player

import (
	"maps"
	"math"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Beds and straw beds as BDS 1.26.60 runs them (BedBlock, StrawBed, BedItem, StrawBedItem,
// Player::startSleepInBed and stopSleepInBed, the sleep counter and ServerPlayerSleepManager).
// The research note is private.
const (
	bedEntityID = "Bed"
	bedColorTag = "color"
	// endPortal and endPortalFrame keep a bed from going within bedPortalReach blocks above or
	// below them.
	endPortal      = "minecraft:end_portal"
	endPortalFrame = "minecraft:end_portal_frame"
	bedPortalReach = 3
	// A player may sleep in a bed within bedReachSide blocks on x and z, bedReachUp on y.
	bedReachSide = 3
	bedReachUp   = 4
	// bedBlast is the explosion of a bed used outside the overworld.
	bedBlast = 5
	// The height a sleeper lies at above the bed's block, a bed and a straw bed.
	bedSleepHeight   = 0.90625
	strawSleepHeight = 0.59375
	// sleepFull is the sleep counter of a player who has slept long enough; a woken player's
	// counter runs on to sleepWake and then clears.
	sleepFull = 100
	sleepWake = 110
	// sleepSize is the sleeper's box, wide and high.
	sleepSize = 0.2
	// playerFlagSleep is the player flags bit of a sleeper.
	playerFlagSleep = 1
	// minSkyDarken is how dark the sky must be to sleep: night, or a thunderstorm.
	minSkyDarken = 4
	// dayTicks is the length of a day.
	dayTicks = 24000
	// enchantRespiration on what it wears lets a player sleep in a bed under water, as do
	// Water Breathing and Conduit Power.
	enchantRespiration = int16(6)
	// levelEventDestroyStrawBed is the break particles a used straw bed leaves.
	levelEventDestroyStrawBed = 2021
)

// The answers of startSleepInBed, as BDS numbers them.
const (
	sleepOK = iota
	sleepNotPossible
	sleepNotNow
	sleepTooFar
	sleepOther
	sleepNotSafe
	sleepObstructed
)

// sleepMessages are what a refused sleeper is told.
var sleepMessages = map[int]string{
	sleepNotNow: "tile.bed.noSleep", sleepTooFar: "tile.bed.tooFar", sleepNotSafe: "tile.bed.notSafe",
	sleepObstructed: "tile.bed.obstructed",
}

// standUpSpots are where BDS looks for somewhere to get up, from the bed's head, for a bed
// facing south: one side, then round the foot to the other, then on top. standUpOther is
// the same starting on the other side, for a sleeper lying that way.
var (
	standUpSpots = [12]cube.Pos{
		{1, 0, 0}, {1, 0, 1}, {1, 0, 2}, {0, 0, 2}, {-1, 0, 2}, {-1, 0, 1},
		{-1, 0, 0}, {-1, 0, -1}, {0, 0, -1}, {1, 0, -1}, {0, 1, 0}, {0, 1, 1},
	}
	standUpOther = [12]cube.Pos{
		{-1, 0, 0}, {-1, 0, 1}, {-1, 0, 2}, {0, 0, 2}, {1, 0, 2}, {1, 0, 1},
		{1, 0, 0}, {1, 0, -1}, {0, 0, -1}, {-1, 0, -1}, {0, 1, 0}, {0, 1, 1},
	}
	// standUpTurn is how BDS turns the spots for the bed's facing.
	standUpTurn = [4]uint8{2, 3, 0, 1}
	// bedYaw is the way a sleeper's body lies for each facing (BDS's table 0x13E7460).
	bedYaw = [4]float32{0, 90, -180, -90}
	// dangerousToStand are blocks a sleeper does not choose to get up on or in when anywhere
	// else will do.
	dangerousToStand = map[string]bool{
		"minecraft:wither_rose": true, "minecraft:fire": true, "minecraft:soul_fire": true, "minecraft:magma": true,
		"minecraft:campfire": true, "minecraft:soul_campfire": true, "minecraft:cactus": true,
		"minecraft:sweet_berry_bush": true,
	}
)

// bedEntity is a bed's block entity: its colour. The client draws nothing for a bed
// without one.
type bedEntity struct {
	saved map[string]any
	color uint8
}

// SaveData is what BDS saves of a bed.
func (e *bedEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = bedEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[bedColorTag] = e.color
	return m
}

// NetworkData is the same: BDS sends a bed as it saves it.
func (e *bedEntity) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// loadBed brings back a saved bed.
func loadBed(saved map[string]any) *bedEntity {
	e := &bedEntity{saved: maps.Clone(saved)}
	e.color, _ = saved[bedColorTag].(uint8)
	delete(e.saved, bedColorTag)
	return e
}

// BedColor is the colour of the bed at pos, 0 (white) if there is none. Runs: owner of the
// region.
func BedColor(r *world.Region, pos cube.Pos) uint8 {
	if be, ok := r.BlockEntity(pos); ok {
		if b, isBed := be.(*bedEntity); isBed {
			return b.color
		}
	}
	return 0
}

// bedDrops gives a dropped bed the colour of the bed it came from.
func bedDrops(r *world.Region, pos cube.Pos, items []ItemStack) []ItemStack {
	for i := range items {
		if items[i].Name == blockuse.Bed {
			items[i].Meta = int16(BedColor(r, pos))
		}
	}
	return items
}

// blockEntityDrops adds what a block's block entity gives what it drops: a shulker box's
// items, a bed's colour, a banner's patterns. Call it before the block goes.
func (s *Host) blockEntityDrops(r *world.Region, pos cube.Pos, items []ItemStack) []ItemStack {
	return s.composterDrops(r, pos, bannerDrops(r, pos, bedDrops(r, pos, s.shulkerDrops(r, pos, items))))
}

// isBedItem reports whether an item places a bed.
func isBedItem(name string) bool { return name == blockuse.Bed || name == blockuse.StrawBed }

// placeBed is BedItem's useOn: the bed goes where a block would, else once more a block
// further for a click on a side.
func (p *Player) placeBed(r *world.Region, clicked cube.Pos, face int32, held ItemStack) bool {
	block, ok := p.s.reg.Blocks.Block(held.Name)
	if !ok {
		return false
	}
	pos := clicked.Side(cube.Face(face))
	if b, isB := r.Block(clicked, 0); isB && p.s.game.replaceable(p, r, clicked, b, block) {
		pos = clicked
	}
	if p.tryBed(r, clicked, pos, block, held) {
		return true
	}
	if f := cube.Face(face); f != cube.FaceUp && f != cube.FaceDown {
		return p.tryBed(r, clicked, pos.Side(f), block, held)
	}
	return false
}

// tryBed is BedItem's placing at one foot position (sub_C9D2BC0): the head goes the way the
// player looks, both halves must be free and held from below, and no end portal may be near.
// Unlike a block, nothing checks for entities in the way: BDS places a bed over the placer.
func (p *Player) tryBed(r *world.Region, clicked, foot cube.Pos, block uint32, held ItemStack) bool {
	if !p.inReach(clicked) {
		return false
	}
	blocks := p.s.reg.Blocks
	facing := direction.FromYaw(p.entity.Rotation().Yaw)
	head := foot.Side(facing.Face())
	footState := blockuse.BedSet(blocks, blockuse.BedWithFacing(blocks, block, facing), blockuse.BedHead, false)
	headState := blockuse.BedSet(blocks, footState, blockuse.BedHead, true)
	if !p.bedFits(r, foot, head, block) || p.endPortalNear(r, foot) {
		return false
	}
	if g := p.s.game; g.placeEv.Has() {
		e := BlockPlaceEvent{Player: p, Pos: foot, Block: footState}
		g.placeEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	if !r.SetBlock(foot, 0, footState) {
		return false
	}
	r.SetBlock(head, 0, headState)
	if blocks.Name(block) == blockuse.Bed {
		colourBed(r, uint8(held.Meta&0xf), foot, head) //nolint:gosec // masked to a colour
	}
	r.UpdateNeighbours(foot)
	r.UpdateNeighbours(head)
	playSound(r, head, p.s.blockSound(packet.SoundEventPlace, blockCenter(head), headState), nil)
	if !p.creative() {
		p.useHeld()
	}
	return true
}

// bedFits reports whether both halves of a bed may go at foot and head: each can be built
// over, holds no lava, and stands on a block that supports it.
func (p *Player) bedFits(r *world.Region, foot, head cube.Pos, block uint32) bool {
	blocks := p.s.reg.Blocks
	for _, at := range [...]cube.Pos{foot, head} {
		b, ok := r.Block(at, 0)
		if !ok || !p.s.game.replaceable(p, r, at, b, block) || blocks.Properties(b).Material == registry.MaterialLava {
			return false
		}
		under, okUnder := r.Block(at.Side(cube.FaceDown), 0)
		if !okUnder || !p.s.attach.Supports(under, cube.FaceUp) {
			return false
		}
	}
	return true
}

// endPortalNear reports an end portal or frame in the column within bedPortalReach of foot.
func (p *Player) endPortalNear(r *world.Region, foot cube.Pos) bool {
	blocks := p.s.reg.Blocks
	for y := foot[1] - bedPortalReach; y <= foot[1]+bedPortalReach; y++ {
		if b, ok := r.Block(cube.Pos{foot[0], y, foot[2]}, 0); ok {
			if n := blocks.Name(b); n == endPortal || n == endPortalFrame {
				return true
			}
		}
	}
	return false
}

// colourBed sets the colour of the bed block entities at both halves and sends them.
func colourBed(r *world.Region, color uint8, halves ...cube.Pos) {
	for _, at := range halves {
		if be, ok := r.BlockEntity(at); ok {
			if b, isBed := be.(*bedEntity); isBed {
				b.color = color
				r.SaveBlockEntity(at)
				r.SendBlockEntity(at)
			}
		}
	}
}

// sleepState is where a player is with sleeping. The region owns it; info is a copy for
// the global owner's sleep count.
type sleepState struct {
	// asleep says the player lies in the bed whose head is at bed.
	asleep bool
	bed    cube.Pos
	// counter is BDS's SleepCounterComponent.
	counter int32
	// info is sleepInfo's bits, for the global owner.
	info atomic.Uint32
}

// The bits of sleepState.info.
const (
	// sleepCounts is a player the sleep count looks at: alive and not a spectator.
	sleepCounts = 1 << iota
	sleepAsleep
	// sleepRested is a sleeper whose counter reached sleepFull.
	sleepRested
)

// Sleeping reports whether the player lies in a bed. Runs: owner of its region.
func (p *Player) Sleeping() bool { return p.sleep.asleep }

// useBed is BDS's BedBlock use (sub_CCD0990, sub_D4DD380): sleep in the overworld; anywhere
// else a bed explodes and a straw bed falls apart, if respawn blocks explode.
func (p *Player) useBed(r *world.Region, u *BlockUse) bool {
	blocks := p.s.reg.Blocks
	head, ok := blockuse.BedHeadPos(blocks, r, u.Pos, u.Block)
	if !ok {
		return true
	}
	if r.Dimension() == p.s.overworld {
		p.trySleep(r, head, blocks.Name(u.Block) == blockuse.Bed)
		return true
	}
	if !p.s.level.RespawnBlocksExplode {
		return true
	}
	headBlock, _ := r.Block(head, 0)
	foot := blockuse.BedOther(blocks, head, headBlock)
	if blocks.Name(u.Block) == blockuse.StrawBed {
		p.s.FallBlock(r, head, headBlock)
		if b, isB := r.Block(foot, 0); isB && blocks.Name(b) == blockuse.StrawBed {
			p.s.FallBlock(r, foot, b)
		}
		return true
	}
	air := blocks.Air()
	r.SetBlock(head, 0, air)
	at := blockVec(head).Add(mgl32.Vec3{0.5, 0.5, 0.5})
	if b, isB := r.Block(foot, 0); isB && blocks.Name(b) == blockuse.Bed {
		r.SetBlock(foot, 0, air)
		at = at.Add(blockVec(foot).Add(mgl32.Vec3{0.5, 0.5, 0.5})).Mul(0.5)
	}
	x := NewExplosion(at, bedBlast)
	x.Fire, x.MaxResistance = true, math.MaxFloat32
	x.Kind, x.FromBlock, x.Block, x.BlockPos, x.Attacker = ExplosionBed, true, headBlock, head, p
	p.s.Explode(r, x)
	return true
}

// trySleep is BDS's sub_CCD0F00: a bed someone else sleeps in is occupied; a stale occupied
// bit is cleared; then the player tries to sleep and is told why not.
func (p *Player) trySleep(r *world.Region, head cube.Pos, setSpawn bool) {
	blocks := p.s.reg.Blocks
	b, _ := r.Block(head, 0)
	if blockuse.BedIsOccupied(blocks, b) {
		if p.s.sleeperAt(r, head, p) {
			p.tell("tile.bed.occupied")
			return
		}
		p.setOccupied(r, head, false)
	}
	res := p.startSleep(r, head, setSpawn)
	if res == sleepOK {
		p.setOccupied(r, head, true)
		return
	}
	if key, ok := sleepMessages[res]; ok {
		p.tell(key)
	}
}

// sleeperAt reports whether a player other than p sleeps in the bed with its head at head.
func (s *Host) sleeperAt(r *world.Region, head cube.Pos, p *Player) bool {
	at := blockVec(head)
	for _, e := range r.AppendEntitiesIn(nil, at[0]-1, at[2]-1, at[0]+2, at[2]+2) {
		if o, ok := e.Owner().(*Player); ok && o != p && o.sleep.asleep && o.sleep.bed == head {
			return true
		}
	}
	return false
}

// setOccupied sets the occupied bit of both halves of the bed with its head at head.
func (p *Player) setOccupied(r *world.Region, head cube.Pos, on bool) {
	blocks := p.s.reg.Blocks
	b, ok := r.Block(head, 0)
	if !ok || !blockuse.IsBed(blocks, b) {
		return
	}
	r.SetBlock(head, 0, blockuse.BedSet(blocks, b, blockuse.BedOccupied, on))
	foot := blockuse.BedOther(blocks, head, b)
	if f, isF := r.Block(foot, 0); isF && blocks.Name(f) == blocks.Name(b) {
		r.SetBlock(foot, 0, blockuse.BedSet(blocks, f, blockuse.BedOccupied, on))
	}
}

// tell sends the player a message to translate.
func (p *Player) tell(key string) {
	p.send(&packet.Text{TextType: packet.TextTypeTranslation, NeedsTranslation: true, Message: key})
	p.flush()
}

// startSleep is BDS's Player::startSleepInBed (sub_BCD20B0) as far as Helix has its parts:
// the bed under water, distance, a place to get up, the spawn point, the time of day, then
// lying down. Monsters nearby are not looked for: Helix has none yet.
func (p *Player) startSleep(r *world.Region, head cube.Pos, setSpawn bool) int {
	if extra, ok := r.Block(head, 1); ok && p.s.reg.Blocks.Properties(extra).Material == registry.MaterialWater && !p.breathesWater() {
		return sleepOther
	}
	if p.sleep.asleep || p.dead() || p.vehicle != nil {
		return sleepOther
	}
	feet := p.entity.Position()
	feet[1] -= playerEyeHeight
	if abs32(feet[0]-float32(head[0])) > bedReachSide || abs32(feet[1]-float32(head[1])) > bedReachUp || abs32(feet[2]-float32(head[2])) > bedReachSide {
		return sleepTooFar
	}
	if _, ok := p.s.standUpSpot(r, head, nil); !ok {
		return sleepObstructed
	}
	if setSpawn && p.setBedSpawn(head) {
		p.tell("tile.bed.respawnSet")
	}
	if p.s.clock != nil {
		if daytime.SkyDarken(p.s.clock.Time(), p.s.clock.Cycle(), p.s.weather.Rain(), p.s.weather.Thunder()) < minSkyDarken {
			return sleepNotNow
		}
	}
	p.lieDown(r, head)
	return sleepOK
}

// breathesWater reports whether the player may sleep in a bed under water.
func (p *Player) breathesWater() bool {
	if _, ok := p.Effect(EffectWaterBreathing); ok {
		return true
	}
	if _, ok := p.Effect(EffectConduitPower); ok {
		return true
	}
	for _, it := range p.Armour().Items() {
		for _, e := range it.Enchants() {
			if e.ID == enchantRespiration && e.Level > 0 {
				return true
			}
		}
	}
	return false
}

// lieDown puts the player in the bed: its small box, its place on the bed and the sleeping
// flags, as BDS's success path does.
func (p *Player) lieDown(r *world.Region, head cube.Pos) {
	blocks := p.s.reg.Blocks
	b, _ := r.Block(head, 0)
	height := float32(bedSleepHeight)
	if blocks.Name(b) == blockuse.StrawBed {
		height = strawSleepHeight
	}
	p.sleep.asleep, p.sleep.bed, p.sleep.counter = true, head, 0
	facing := blockuse.BedFacing(blocks, b)
	rot := p.entity.Rotation()
	rot.Yaw = bedYaw[facing]
	rot.HeadYaw = rot.Yaw
	at := mgl32.Vec3{float32(head[0]) + 0.5, float32(head[1]) + height + playerEyeHeight, float32(head[2]) + 0.5}
	applyMovement(p, moveTo(at, rot, p.entity.Tick()))
	p.showSleep()
}

// showSleep tells everyone, the player too, whether it sleeps.
func (p *Player) showSleep() {
	var playerFlags byte
	width, height := float32(playerHalfWidth*2), float32(playerHeight)
	if p.sleep.asleep {
		playerFlags = 1 << playerFlagSleep
		width, height = sleepSize, sleepSize
	}
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags: p.actorFlags(), protocol.EntityDataKeyPlayerFlags: playerFlags,
		protocol.EntityDataKeyBedPosition: blockPos(p.sleep.bed),
		protocol.EntityDataKeyWidth:       width, protocol.EntityDataKeyHeight: height,
	}
	pk := actorDataAt(p.runtimeID, meta, p.entity.Tick())
	p.entity.Send(pk)
	p.send(pk)
	p.flush()
}

// StopSleeping gets the player out of bed, as BDS's stopSleepInBed: the bed is free again,
// the player stands up beside it and a straw bed is used up. Runs: owner of its region.
func (p *Player) StopSleeping() {
	if !p.sleep.asleep || p.entity == nil {
		return
	}
	p.sleep.asleep = false
	r := p.entity.Region()
	head := p.sleep.bed
	p.send(&packet.Animate{ActionType: packet.AnimateActionStopSleep, EntityRuntimeID: p.runtimeID})
	if r != nil {
		blocks := p.s.reg.Blocks
		b, _ := r.Block(head, 0)
		if blockuse.IsBed(blocks, b) {
			p.setOccupied(r, head, false)
			pos := p.entity.Position()
			at, ok := p.s.standUpSpot(r, head, &pos)
			if !ok {
				at = head.Side(cube.FaceUp)
			}
			stand := mgl32.Vec3{float32(at[0]) + 0.5, float32(at[1]) + playerEyeHeight, float32(at[2]) + 0.5}
			applyMovement(p, moveTo(stand, p.entity.Rotation(), p.entity.Tick()))
			if blocks.Name(b) == blockuse.StrawBed {
				p.s.useUpStrawBed(r, head, b)
			}
		}
	}
	p.showSleep()
}

// useUpStrawBed takes away a straw bed slept in: both halves, with BDS's break particles and
// sound and no drop.
func (s *Host) useUpStrawBed(r *world.Region, head cube.Pos, b uint32) {
	blocks := s.reg.Blocks
	foot := blockuse.BedOther(blocks, head, b)
	air := blocks.Air()
	levelEvent(r, head, blockVec(head), levelEventDestroyStrawBed, int32(blocks.Hash(b))) //nolint:gosec // hashes are sent as int32
	r.SetBlock(head, 0, air)
	if f, ok := r.Block(foot, 0); ok && blocks.Name(f) == blockuse.StrawBed {
		r.SetBlock(foot, 0, air)
		r.UpdateNeighbours(foot)
	}
	r.UpdateNeighbours(head)
	playSound(r, head, s.blockSound(packet.SoundEventStrawBedBreakLeave, blockVec(head), b), nil)
}

// standUpSpot is BDS's sub_CCD1AB0: the first of standUpSpots, turned for the bed, with
// something to stand on and room to stand, the safe ones first. from is where the sleeper
// is, which picks the side to look on first; nil starts on the usual side.
func (s *Host) standUpSpot(r *world.Region, head cube.Pos, from *mgl32.Vec3) (cube.Pos, bool) {
	blocks := s.reg.Blocks
	b, ok := r.Block(head, 0)
	if !ok || !blockuse.IsBed(blocks, b) {
		return cube.Pos{}, false
	}
	facing := blockuse.BedFacing(blocks, b)
	spots := &standUpSpots
	if from != nil {
		cx, cz := float32(head[0])+0.5, float32(head[2])+0.5
		switch facing {
		case direction.South:
			if from[0] > cx {
				spots = &standUpOther
			}
		case direction.West:
			if from[2] > cz {
				spots = &standUpOther
			}
		case direction.North:
			if from[0] < cx {
				spots = &standUpOther
			}
		case direction.East:
			if from[2] < cz {
				spots = &standUpOther
			}
		}
	}
	air := blocks.Air()
	var fallback cube.Pos
	found := false
	for _, o := range spots {
		at := head.Add(turnSpot(o, standUpTurn[facing]))
		if !s.standable(r, at.Side(cube.FaceDown)) {
			continue
		}
		in, _ := r.Block(at, 0)
		inBed := blockuse.IsBed(blocks, in)
		if n := blocks.Name(in); !inBed && (s.solidBlock(in) || n == endPortal || n == endPortalFrame) {
			continue
		}
		if above, _ := r.Block(at.Side(cube.FaceUp), 0); above != air {
			continue
		}
		if inBed {
			if above2, _ := r.Block(at.Add(cube.Pos{0, 2, 0}), 0); above2 != air {
				continue
			}
		}
		under, _ := r.Block(at.Side(cube.FaceDown), 0)
		if !s.dangerous(in) && !s.dangerous(under) {
			return at, true
		}
		if !found {
			fallback, found = at, true
		}
	}
	return fallback, found
}

// turnSpot turns a stand-up spot as BDS does for each facing.
func turnSpot(o cube.Pos, turn uint8) cube.Pos {
	switch turn {
	case 1:
		return cube.Pos{-o[2], o[1], o[0]}
	case 2:
		return cube.Pos{-o[0], o[1], -o[2]}
	case 3:
		return cube.Pos{o[2], o[1], -o[0]}
	}
	return o
}

// standable reports whether the block at pos has a collision box to stand on.
func (s *Host) standable(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && len(s.reg.Blocks.Properties(b).Collision) > 0
}

// dangerous reports whether a block hurts whoever stands on or in it.
func (s *Host) dangerous(b uint32) bool {
	return dangerousToStand[s.reg.Blocks.Name(b)] || s.reg.Blocks.Properties(b).Material == registry.MaterialLava
}

// tickSleep is BDS's sleep counter and the end of a sleep whose bed went, then the copy the
// global owner counts with.
func (p *Player) tickSleep() {
	if p.sleep.asleep {
		if r := p.entity.Region(); r != nil {
			if b, ok := r.Block(p.sleep.bed, 0); !ok || !blockuse.IsBed(p.s.reg.Blocks, b) {
				p.StopSleeping()
			}
		}
	}
	switch {
	case p.sleep.asleep:
		p.sleep.counter = min(p.sleep.counter+1, sleepFull)
	case p.sleep.counter > 0:
		p.sleep.counter++
		if p.sleep.counter >= sleepWake {
			p.sleep.counter = 0
		}
	}
	var info uint32
	if !p.dead() && p.mode() != GameModeSpectator {
		info |= sleepCounts
	}
	if p.sleep.asleep {
		info |= sleepAsleep
		if p.sleep.counter >= sleepFull {
			info |= sleepRested
		}
	}
	p.sleep.info.Store(info)
}

// mode is the player's game mode with the world's put in for the default.
func (p *Player) mode() int32 {
	if p.gameMode == GameModeDefault {
		return p.s.level.GameMode
	}
	return p.gameMode
}

// sleepTick is ServerPlayerSleepManager on the global owner: once enough of those who count
// sleep (playerssleepingpercentage, at least one) and have slept long enough, the night is
// skipped and every sleeper wakes. Global owner only.
func (s *Host) sleepTick() {
	g := s.global
	var total, rested, asleep int
	for _, p := range g.order {
		info := p.sleep.info.Load()
		if info&sleepCounts == 0 {
			continue
		}
		total++
		if info&sleepAsleep != 0 {
			asleep++
		}
		if info&sleepRested != 0 {
			rested++
		}
	}
	if total == 0 || asleep == 0 {
		return
	}
	need := max(1, int(math.Ceil(float64(total)*float64(s.level.PlayersSleepingPercentage)/100)))
	if rested < need {
		return
	}
	if s.clock != nil && s.level.DoDayLightCycle {
		t := s.clock.Time()
		s.clock.Set(dayTicks * ((t + dayTicks) / dayTicks))
		s.SyncClocks()
	}
	if s.weather != nil {
		s.weather.Stop()
	}
	for _, p := range g.order {
		if p.sleep.info.Load()&sleepAsleep == 0 {
			continue
		}
		p.sleep.info.Store(p.sleep.info.Load() &^ (sleepAsleep | sleepRested))
		p.runOnRegion(func() { p.StopSleeping() })
	}
}

// runOnRegion runs fn on the owner of the player's region, if it is still in the world.
func (p *Player) runOnRegion(fn func()) {
	e := p.entity
	if e == nil {
		return
	}
	r := e.Region()
	if r == nil {
		return
	}
	pos := e.Position()
	r.Dimension().RunAt(world.ChunkPos(int32(math.Floor(float64(pos[0]))), int32(math.Floor(float64(pos[2])))), func(r *world.Region) {
		if p.entity == e && e.Region() == r {
			fn()
		}
	})
}

// The keys BDS saves a player's respawn point under.
const (
	spawnX, spawnY, spawnZ         = "SpawnX", "SpawnY", "SpawnZ"
	spawnBlockX, spawnBlockY       = "SpawnBlockPositionX", "SpawnBlockPositionY"
	spawnBlockZ, spawnDimTag       = "SpawnBlockPositionZ", "SpawnDimension"
	spawnForcedTag                 = "SpawnForced"
	unsetSpawn               int32 = math.MinInt32
)

// setBedSpawn makes the bed with its head at head the player's respawn point. False if it
// already was. Where the player stands is kept too (floored, feet rounded up), as BDS's
// setSpawnPoint (sub_BCF6300) keeps it in SpawnX/Y/Z: a respawn first shows the top of
// that column, so a tower where the player stood shows it on the tower.
func (p *Player) setBedSpawn(head cube.Pos) bool {
	if p.spawn.set && p.spawn.dim == spawnOverworld && p.spawn.pos == head {
		return false
	}
	feet := p.entity.Position().Sub(mgl32.Vec3{0, playerEyeHeight, 0})
	stand := cube.Pos{int(math.Floor(float64(feet[0]))), int(math.Ceil(float64(feet[1]))), int(math.Floor(float64(feet[2])))}
	p.spawn = spawnPointState{set: true, pos: head, stand: stand, dim: spawnOverworld}
	return true
}

// The dimensions a respawn point is kept in (BDS's SpawnDimension).
const (
	spawnOverworld int32 = 0
	spawnNether    int32 = 1
)

// spawnPointState is a player's own respawn point: a bed in the overworld or a respawn
// anchor in the Nether. pos is the block (SpawnBlockPosition), stand where the player stood
// when it was set (SpawnX/Y/Z).
type spawnPointState struct {
	set        bool
	pos, stand cube.Pos
	dim        int32
}

// loadSpawn reads the respawn point BDS saved.
func (p *Player) loadSpawn(data map[string]any) {
	x, okX := data[spawnBlockX].(int32)
	y, okY := data[spawnBlockY].(int32)
	z, okZ := data[spawnBlockZ].(int32)
	if !okX || !okY || !okZ || x == unsetSpawn {
		return
	}
	p.spawn = spawnPointState{set: true, pos: cube.Pos{int(x), int(y), int(z)}}
	p.spawn.stand = p.spawn.pos
	sx, okX := data[spawnX].(int32)
	sy, okY := data[spawnY].(int32)
	sz, okZ := data[spawnZ].(int32)
	if okX && okY && okZ && sx != unsetSpawn {
		p.spawn.stand = cube.Pos{int(sx), int(sy), int(sz)}
	}
	if d, ok := data[spawnDimTag].(int32); ok {
		p.spawn.dim = d
	}
}

// saveSpawn writes the respawn point where BDS keeps it.
func (p *Player) saveSpawn(data map[string]any) {
	if !p.spawn.set {
		for _, k := range [...]string{spawnX, spawnY, spawnZ, spawnBlockX, spawnBlockY, spawnBlockZ} {
			data[k] = unsetSpawn
		}
		return
	}
	x, y, z := int32(p.spawn.pos[0]), int32(p.spawn.pos[1]), int32(p.spawn.pos[2])          //nolint:gosec // block coordinates fit int32
	sx, sy, sz := int32(p.spawn.stand[0]), int32(p.spawn.stand[1]), int32(p.spawn.stand[2]) //nolint:gosec // block coordinates fit int32
	data[spawnX], data[spawnY], data[spawnZ] = sx, sy, sz
	data[spawnBlockX], data[spawnBlockY], data[spawnBlockZ] = x, y, z
	data[spawnDimTag] = p.spawn.dim
	data[spawnForcedTag] = uint8(0)
}

// bedRespawn is where a dead player comes back at its bed: a place to get up beside it. False
// if it has no bed or the bed is gone, which forgets it.
func (p *Player) bedRespawn(r *world.Region) (mgl32.Vec3, bool) {
	if !p.spawn.set || p.spawn.dim != spawnOverworld || r == nil {
		return mgl32.Vec3{}, false
	}
	b, ok := r.Block(p.spawn.pos, 0)
	if !ok || !blockuse.IsBed(p.s.reg.Blocks, b) {
		if ok {
			p.spawn.set = false
		}
		return mgl32.Vec3{}, false
	}
	at, found := p.s.standUpSpot(r, p.spawn.pos, nil)
	if !found {
		return mgl32.Vec3{}, false
	}
	return mgl32.Vec3{float32(at[0]) + 0.5, float32(at[1]) + playerEyeHeight, float32(at[2]) + 0.5}, true
}
