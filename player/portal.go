package player

import (
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/portal"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// nameEnderEye is the eye of ender, which fills end portal frames.
const nameEnderEye = "minecraft:ender_eye"

// BDS 1.26.60's portal timing for players (PortalCooldownDurationComponent, the actor
// portal tick sub_B39FF00, Player sub_BCC9950, setPlayerGameType sub_BCCDF90,
// ServerPlayer::changeDimension sub_A1490F0).
const (
	// playerPortalCooldown is the player's cooldown duration: standing in a portal
	// keeps the cooldown one below it, and it runs down a tick at a time outside.
	playerPortalCooldown = 10
	// portalTravelCooldown is the cooldown a trip through a nether portal leaves.
	portalTravelCooldown = 300
	// survivalPortalTime is how many ticks a survival or adventure player stands in a
	// nether portal before it takes them; creative players go at once.
	survivalPortalTime = 80
	// portalTimeDecay is how fast the time in a portal falls once out of it.
	portalTimeDecay = 4
	// netherScale is how much smaller the Nether is than the overworld.
	netherScale = 8
	// worldLimit is the furthest x or z a portal trip lands at.
	worldLimit = 32_000_000
	// arrivalWaitTicks is how long an arrival waits for the chunks around it before it
	// builds or finds the portal with what is loaded.
	arrivalWaitTicks = 200
	// dimensionTrace is how many packets after a dimension change -debug logs.
	dimensionTrace = 400
)

// portalState is the player's PortalCooldownDurationComponent and its arrival.
type portalState struct {
	cooldown, time int32
	// arrival runs on the player's region each tick after a trip until it reports
	// done; waited counts its ticks.
	arrival func(r *world.Region) bool
	waited  int
	// loadingScreen numbers the ChangeDimension loading screens.
	loadingScreen uint32
	// changing is set from ChangeDimension until the client says the change is done:
	// its movement until then is from before the change. changingTicks bounds it.
	changing      bool
	changingTicks int
	rng           *rand.Rand
}

// dimMove is a move to another dimension waiting for the old region to take the
// entity out.
type dimMove struct {
	to     *world.Dimension
	eyes   mgl32.Vec3
	rot    world.Rotation
	arrive func(r *world.Region) bool
	// respawn marks a move made by respawning, not a portal.
	respawn bool
}

// dimension is the open dimension with BDS's number id, nil if it is not open.
func (s *Host) dimension(id mcdb.Dimension) *world.Dimension {
	switch id {
	case mcdb.Overworld:
		return s.overworld
	case mcdb.Nether:
		return s.nether
	case mcdb.End:
		return s.end
	}
	return nil
}

// dimensionID is BDS's number of an open dimension.
func (s *Host) dimensionID(d *world.Dimension) int32 {
	switch {
	case d != nil && d == s.nether:
		return int32(mcdb.Nether)
	case d != nil && d == s.end:
		return int32(mcdb.End)
	}
	return int32(mcdb.Overworld)
}

// maxPortalTime is the ticks the player stands in a nether portal before it goes.
func (p *Player) maxPortalTime() int32 {
	if p.creative() {
		return 0
	}
	return survivalPortalTime
}

// tickPortal is the player's part of BDS's actor portal tick and the portal blocks'
// entityInside: the time in a nether portal, the cooldown, and the trips. It runs on
// the player's region each tick.
func (p *Player) tickPortal() {
	st := &p.portal
	if st.changing {
		if st.changingTicks++; st.changingTicks > arrivalWaitTicks {
			st.changing = false
		}
	}
	if st.arrival != nil {
		if r := p.entity.Region(); r != nil {
			st.waited++
			if st.arrival(r) {
				st.arrival, st.waited = nil, 0
				p.finishArrival()
			}
		}
		return
	}
	if p.s.portals == nil || !p.initialised.Load() || p.dead() || p.moving != nil || p.vehicle != nil || p.gameMode == GameModeSpectator {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	inNether, inEnd := p.portalBlocks(r)
	if inEnd && st.cooldown == 0 {
		p.enterEndPortal()
		st.cooldown = playerPortalCooldown
		return
	}
	if !inNether {
		st.time = max(st.time-portalTimeDecay, 0)
		if st.cooldown > 0 {
			st.cooldown--
		}
		return
	}
	if st.cooldown > 0 {
		st.cooldown = playerPortalCooldown - 1
		return
	}
	old := st.time
	st.time++
	if limit := p.maxPortalTime(); old >= limit {
		st.time, st.cooldown = limit, portalTravelCooldown
		p.enterNetherPortal()
	}
}

// portalBlocks reports whether the player's box is in a nether portal or end portal
// block.
func (p *Player) portalBlocks(r *world.Region) (nether, end bool) {
	feet := p.entity.Position().Sub(mgl32.Vec3{0, eyeHeight, 0})
	lo := vecPos(feet.Sub(mgl32.Vec3{playerWidth / 2, 0, playerWidth / 2}))
	hi := vecPos(feet.Add(mgl32.Vec3{playerWidth / 2, playerHeight, playerWidth / 2}))
	for x := lo[0]; x <= hi[0]; x++ {
		for y := lo[1]; y <= hi[1]; y++ {
			for z := lo[2]; z <= hi[2]; z++ {
				b, ok := r.Block(cube.Pos{x, y, z}, 0)
				if !ok {
					continue
				}
				nether = nether || p.s.portals.IsPortal(b)
				end = end || p.s.portals.IsEndPortal(b)
			}
		}
	}
	return nether, end
}

// enterNetherPortal sends the player through a nether portal: from the overworld to
// the Nether, from anywhere else to the overworld, at the position BDS's dimensions
// translate (x and z divided or multiplied by 8, y kept, within 32 million), then to
// the nearest portal there within 128 blocks or a new one.
func (p *Player) enterNetherPortal() {
	from := p.s.dimensionID(p.dim)
	to := mcdb.Nether
	if from != int32(mcdb.Overworld) {
		to = mcdb.Overworld
	}
	dest := p.s.dimension(to)
	if dest == nil {
		return
	}
	eyes := p.entity.Position()
	feet := eyes.Sub(mgl32.Vec3{0, eyeHeight, 0})
	along := p.s.portals.Along(mcdb.Dimension(from), vecPos(feet), feet)
	target := feet
	switch {
	case to == mcdb.Nether:
		target[0], target[2] = target[0]/netherScale, target[2]/netherScale
	case from == int32(mcdb.Nether):
		target[0], target[2] = target[0]*netherScale, target[2]*netherScale
	}
	target[0], target[2] = clampWorld(target[0]), clampWorld(target[2])
	rg := dest.Range()
	target[1] = min(max(target[1], float32(rg[0])), float32(rg[1]))
	p.changeDimension(&dimMove{to: dest, eyes: target.Add(mgl32.Vec3{0, eyeHeight, 0}), rot: p.entity.Rotation(), arrive: func(r *world.Region) bool {
		return p.arriveAtPortal(r, to, target, along)
	}})
}

// clampWorld keeps a portal trip's x or z within BDS's limit, whole as BDS makes it.
func clampWorld(v float32) float32 {
	return float32(min(max(math.Trunc(float64(v)), -worldLimit), worldLimit))
}

// arriveAtPortal is BDS's PortalForcer::force at the end of a nether portal trip, once
// the chunks around the target are loaded (or the wait ran out): the nearest recorded
// portal, else a new one.
func (p *Player) arriveAtPortal(r *world.Region, dim mcdb.Dimension, target mgl32.Vec3, along float32) bool {
	at := vecPos(target)
	if !p.areaLoaded(r, at) && p.portal.waited < arrivalWaitTicks {
		return false
	}
	a, ok := p.s.portals.Find(r, dim, at, along)
	if !ok {
		a = p.s.portals.Make(r, dim, target, p.portalRand())
	}
	p.teleportFeet(a.Pos, p.entity.Rotation())
	return true
}

// areaLoaded reports whether the portal search area around at is loaded in r.
func (p *Player) areaLoaded(r *world.Region, at cube.Pos) bool {
	const reach = 16
	for _, d := range [...]cube.Pos{{0, 0, 0}, {-reach, 0, -reach}, {reach, 0, -reach}, {-reach, 0, reach}, {reach, 0, reach}} {
		if _, ok := r.Block(at.Add(d), 0); !ok {
			return false
		}
	}
	return true
}

// portalRand is the player's random source for building portals.
func (p *Player) portalRand() *rand.Rand {
	if p.portal.rng == nil {
		p.portal.rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) //nolint:gosec // gameplay randomness
	}
	return p.portal.rng
}

// enterEndPortal sends the player through an end portal (sub_CAF0E00): into the End
// onto its arrival platform, or out of the End to its respawn point in the overworld.
// BDS shows the credits on the way out; Helix does not yet.
func (p *Player) enterEndPortal() {
	if p.dim == p.s.end {
		if p.s.overworld == nil {
			return
		}
		p.leaveEnd()
		return
	}
	if p.s.end == nil {
		return
	}
	arrival := portal.EndArrival
	feet := mgl32.Vec3{float32(arrival[0]), float32(arrival[1]), float32(arrival[2])}
	rot := p.entity.Rotation()
	rot.Pitch = 0
	p.changeDimension(&dimMove{to: p.s.end, eyes: feet.Add(mgl32.Vec3{0, eyeHeight, 0}), rot: rot, arrive: func(r *world.Region) bool {
		if _, ok := r.Block(arrival, 0); !ok && p.portal.waited < arrivalWaitTicks {
			return false
		}
		p.s.portals.EndPlatform(r)
		p.teleportFeet(feet, rot)
		if p.s.onEnterEnd != nil {
			p.s.onEnterEnd()
		}
		return true
	}})
}

// leaveEnd sends the player out of the End to its respawn point in the overworld: it
// lands at the bed (or world spawn), and once the bed's chunk is loaded it moves to the
// spot beside the bed BDS's respawn picks.
func (p *Player) leaveEnd() { p.toOverworldSpawn(false) }

// toOverworldSpawn moves the player to its respawn point in the overworld, by a
// respawn or a portal.
func (p *Player) toOverworldSpawn(respawn bool) {
	spawn, _ := p.worldSpawn()
	if p.spawn.set {
		bed := p.spawn.pos
		spawn = mgl32.Vec3{float32(bed[0]) + 0.5, float32(bed[1]) + 1 + eyeHeight, float32(bed[2]) + 0.5}
	}
	p.changeDimension(&dimMove{to: p.s.overworld, eyes: spawn, rot: p.entity.Rotation(), respawn: respawn, arrive: func(r *world.Region) bool {
		if !p.spawn.set {
			return true
		}
		if _, ok := r.Block(p.spawn.pos, 0); !ok && p.portal.waited < arrivalWaitTicks {
			return false
		}
		if at, ok := p.bedRespawn(r); ok {
			p.teleportFeet(at.Sub(mgl32.Vec3{0, eyeHeight, 0}), p.entity.Rotation())
		}
		return true
	}})
}

// changeDimension starts a move to another dimension: the region takes the entity
// out on its next tick, then arrive puts it in the new dimension. Region tick.
func (p *Player) changeDimension(mv *dimMove) {
	if p.moving != nil || p.entity == nil || mv.to == nil || mv.to == p.dim {
		return
	}
	p.moving = mv
	p.entity.Remove()
}

// arrive puts the player in the dimension of its move, as BDS 1.26.60's
// PlayerDimensionTransferer does: ChangeDimension with a new loading screen
// (sub_CDEA5D0), then the chunks of the new dimension around it; the dimension change
// done action follows once the player is placed there (sub_CDEB1F0, see
// finishArrival). BDS sends PlayStatus PlayerSpawn only on the first spawn
// (sub_A13F8E0), never on a dimension change. It runs on the old region when it took
// the entity out.
func (p *Player) arrive(mv *dimMove) {
	z := &p.zone
	z.mu.Lock()
	old := p.viewer
	p.dim = mv.to
	p.viewer = mv.to.AddViewer(func(pk packet.Packet) { p.send(pk) }, p.flush)
	z.pos, z.published = mv.eyes, false
	z.mu.Unlock()
	old.Close()
	p.portal.loadingScreen++
	p.traceSends.Store(dimensionTrace)
	p.send(&packet.ChangeDimension{Dimension: p.s.dimensionID(mv.to), Position: mv.eyes, Respawn: mv.respawn, LoadingScreenID: protocol.Option(p.portal.loadingScreen)})
	p.portal.arrival, p.portal.waited = mv.arrive, 0
	p.portal.changing, p.portal.changingTicks = true, 0
	p.move = moveState{}
	p.entity = mv.to.AddEntity(p.entityConfig(mv.eyes, mv.rot))
	p.moveZones(mv.eyes)
	p.flush()
}

// finishArrival tells the client the dimension change is done, once the player is
// placed in the new dimension, as BDS's transferer sends PlayerAction 14 last.
func (p *Player) finishArrival() {
	p.send(&packet.PlayerAction{EntityRuntimeID: p.runtimeID, ActionType: protocol.PlayerActionDimensionChangeDone})
	p.flush()
}

// teleportFeet moves the player to feet and tells its client, as a teleport.
func (p *Player) teleportFeet(feet mgl32.Vec3, rot world.Rotation) {
	eyes := feet.Add(mgl32.Vec3{0, eyeHeight, 0})
	if !p.entity.Apply(world.Movement{Pos: eyes, Rot: rot, Tick: p.entity.Tick()}) {
		return
	}
	p.move.fall = 0
	p.moveZones(eyes)
	p.send(&packet.MovePlayer{
		EntityRuntimeID: p.runtimeID, Position: eyes, Pitch: rot.Pitch, Yaw: rot.Yaw, HeadYaw: rot.HeadYaw,
		Mode: packet.MoveModeTeleport, Tick: p.entity.Tick(),
		// A teleport always carries its cause: the client (1.26.60, sub_DC17774) rejects one
		// without and disconnects with BadPacket. BDS's dimension transfer builds it with
		// cause and source 0 (sub_CDE8520 into sub_85B0F00).
		TeleportData: protocol.Option(protocol.TeleportData{TeleportCause: packet.TeleportCauseUnknown}),
	})
	p.flush()
}

// lightPortal is flint and steel's portal lighting (FlintAndSteelItem useOn calls
// PortalBlock::trySpawnPortal before setting fire): true if the fire at pos made a
// portal instead.
func (p *Player) lightPortal(r *world.Region, pos cube.Pos) bool {
	if p.s.portals == nil {
		return false
	}
	return p.s.portals.Light(r, mcdb.Dimension(p.s.dimensionID(r.Dimension())), pos)
}

// useEnderEye puts an eye of ender into the end portal frame at pos, if it has none:
// the frame fills (sound), the eye is used up outside creative, and a finished ring
// opens the end portal (sound). It reports whether the eye went in.
func (p *Player) useEnderEye(r *world.Region, pos cube.Pos) bool {
	if p.s.portals == nil {
		return false
	}
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	frame, eye := p.s.portals.IsFrame(b)
	if !frame || eye {
		return false
	}
	filled, ok := p.s.portals.WithEye(b)
	if !ok || !r.SetBlock(pos, 0, filled) {
		return false
	}
	r.UpdateNeighbours(pos)
	playSound(r, pos, soundEvent{name: packet.SoundEventEnderEyePlaced, pos: blockCenter(pos), extra: -1}, nil)
	p.useUpHeld()
	if mid, opened := p.s.portals.OpenEndPortal(r, pos); opened {
		playSound(r, mid, soundEvent{name: packet.SoundEventEndPortalCreated, pos: blockCenter(mid), extra: -1}, nil)
	}
	return true
}
