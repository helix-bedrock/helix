package player

import (
	"maps"
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Sculk sensors, calibrated sculk sensors and sculk shriekers as BDS 1.26.60 (research note
// memory/research-sculk.md): each block actor holds a VibrationListener (0xCA54180 hearing, its
// tick moving the chosen vibration through the air) and a config: the sensor's (0xFBF08D0: hears
// every vibration but its own block's break or place, and sculk_touch only on itself, while
// inactive; activates with power by distance), the calibrated one's (0xFBFEA28: also only the
// frequency its back input gives, if any; range 16) and the shrieker's (0xFBF0918: hears only a
// sensor's tendrils clicking set off by a player, and shrieks: tryShriek 0xD3DF9F0, shriek
// 0xD3DEAF0, response 0xD3E0310). Players carry BDS's WardenSpawnTrackerComponent. The sensor's
// block and power are circuit/sculk.go.
const (
	sculkSensorBlock     = "minecraft:sculk_sensor"
	calibratedBlock      = "minecraft:calibrated_sculk_sensor"
	sculkShriekerBlock   = "minecraft:sculk_shrieker"
	amethystBlock        = "minecraft:amethyst_block"
	sculkSensorID        = "SculkSensor"
	calibratedSensorID   = "CalibratedSculkSensor"
	sculkShriekerID      = "SculkShrieker"
	vibrationListenerTag = "VibrationListener"
	vibrationSelectorTag = "selector"
	vibrationEventTag    = "event"
	queuedThreatTag      = "QueuedResponseThreatLevel"
	stateSculkPhase      = "sculk_sensor_phase"
	stateShrieking       = "active"
	stateCanSummon       = "can_summon"
	sculkPhaseActive     = 1
	sculkPhaseCooldown   = 2
	// sensorRange and calibratedRange are how far each hears; shriekerRange the shrieker.
	sensorRange     = 8
	calibratedRange = 16
	shriekerRange   = 8
	// vibrationSpeed turns distance into seconds of flight (0.05) and seconds into ticks (20).
	vibrationSpeed     = 0.05
	vibrationTicksSec  = 20
	vibrationParticleV = 20
	// maxPower is the strongest a sensor gives.
	maxPower = 15
	// shriekTicks is how long a shrieker shrieks before it responds.
	shriekTicks = 90
	// warnRange is how far a shrieker reaches players' warning levels, darknessRange how far its
	// darkness, wardenBox half the box a warden nearby stops it in.
	warnRange     = 16
	darknessRange = 40
	wardenBox     = 24
	// darknessTicks is how long the darkness lasts.
	darknessTicks = 260
	// The warden summon: tries, the spread across and the spread up and down.
	summonTries  = 20
	summonSpread = 5
	summonHeight = 6
	// replySpread is how far the warden's reply sound plays from the shrieker.
	replySpread  = 10
	wardenType   = "minecraft:warden"
	wardenEmerge = "minecraft:spawn_emerging"
	// wardenHeight is the cells a summoned warden needs free.
	wardenHeight = 3
)

// The warden spawn tracker's numbers (WardenSpawnTrackerComponent).
const (
	maxThreatLevel      = 4
	threatCooldownTicks = 200
	threatDecreaseTicks = 12000
	wardenThreatTag     = "WardenThreatLevel"
	wardenCooldownTag   = "WardenThreatLevelIncreaseCooldown"
	wardenDecreaseTag   = "WardenThreatDecreaseTimer"
)

// vibration is one heard vibration: its event, the block it came from, the distance and what
// set it off.
type vibration struct {
	event int32
	from  cube.Pos
	dist  float32
	src   *Player
}

// vibrationListener is BDS's VibrationListener with its VibrationSelector: one candidate per
// tick, the nearest (on a tie the higher frequency), sent on the next tick and travelling one
// tick a block.
type vibrationListener struct {
	rng      int32
	cand     vibration
	hasCand  bool
	candTick int64
	flight   vibration
	inFlight bool
	ticks    int32
	// latest is the event of the last vibration that arrived, 0 for none.
	latest int32
}

// consider is VibrationSelector::addCandidate.
func (l *vibrationListener) consider(r *world.Region, pos cube.Pos, ev world.GameEvent) {
	now := r.CurrentTick()
	if l.hasCand && l.candTick != now {
		return
	}
	from := floorPos(ev.Pos)
	dx, dy, dz := int64(pos[0]-from[0]), int64(pos[1]-from[1]), int64(pos[2]-from[2])
	dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
	if l.hasCand {
		if diff := l.cand.dist - dist; diff <= 0.005 && diff >= -0.005 {
			if gameEventFrequency(l.cand.event) >= gameEventFrequency(ev.ID) {
				return
			}
		} else if dist > l.cand.dist {
			return
		}
	}
	src, _ := ev.Source.(*Player)
	l.cand = vibration{event: ev.ID, from: from, dist: dist, src: src}
	l.hasCand, l.candTick = true, now
}

// tick is VibrationListener::tick: a candidate from an earlier tick takes flight, and one in
// flight arrives when its ticks run out, handed to arrive.
func (l *vibrationListener) tick(r *world.Region, pos cube.Pos, arrive func(v vibration)) {
	if !l.inFlight && l.hasCand && l.candTick != r.CurrentTick() {
		l.flight, l.inFlight, l.hasCand = l.cand, true, false
		secs := l.flight.dist * vibrationSpeed
		l.ticks = int32(secs * vibrationTicksSec)
		vibrationParticle(r, l.flight.from, pos, secs)
	}
	if !l.inFlight {
		return
	}
	l.ticks--
	if l.ticks > 0 {
		return
	}
	l.inFlight = false
	l.latest = l.flight.event
	arrive(l.flight)
}

// vibrationParticle is BDS's _requestVibrationParticle: LevelEventGeneric 2027 from the
// vibration's block to the listener, at speed 20 for its flight time.
func vibrationParticle(r *world.Region, from, to cube.Pos, secs float32) {
	tags := map[string]any{
		"origin":     vec3Source(blockCenter(from)),
		"target":     vec3Source(blockCenter(to)),
		"speed":      float32(vibrationParticleV),
		"timeToLive": secs,
	}
	if pk := levelEventGeneric(packet.LevelEventParticlesVibrationSignal, tags); pk != nil {
		r.Broadcast(to, pk, nil)
	}
}

// vec3Source is a fixed position as BDS's PositionSource::serializeTo writes it.
func vec3Source(v mgl32.Vec3) map[string]any {
	return map[string]any{"type": "vec3", "x": v[0], "y": v[1], "z": v[2]}
}

// levelEventGeneric is a LevelEventGeneric carrying tags, nil if they do not encode.
func levelEventGeneric(event int32, tags map[string]any) *packet.LevelEventGeneric {
	data, err := nbt.MarshalEncoding(tags, nbt.NetworkLittleEndian)
	if err != nil || len(data) <= nbtWrapper {
		return nil
	}
	return &packet.LevelEventGeneric{EventID: event, SerialisedEventData: data[nbtWrapper : len(data)-1]}
}

// save writes the listener as BDS saves it: an empty selector and the last event. A vibration
// in flight is not saved (BDS's pending and ticks tags were not read).
func (l *vibrationListener) save() map[string]any {
	m := map[string]any{vibrationSelectorTag: map[string]any{}}
	if l.latest != 0 {
		m[vibrationEventTag] = l.latest
	}
	return m
}

// load takes back the last event.
func (l *vibrationListener) load(saved map[string]any) {
	if m, ok := saved[vibrationListenerTag].(map[string]any); ok {
		l.latest, _ = m[vibrationEventTag].(int32)
	}
}

// SculkSensor is a sculk sensor's or calibrated sculk sensor's block entity. Runs: owner of its
// chunk.
type SculkSensor struct {
	s          *Host
	saved      map[string]any
	calibrated bool
	l          vibrationListener
	chunk      world.ChunkHandle
}

// newSculkSensor makes a sensor's block entity.
func (s *Host) newSculkSensor(calibrated bool) *SculkSensor {
	rng := int32(sensorRange)
	if calibrated {
		rng = calibratedRange
	}
	return &SculkSensor{s: s, calibrated: calibrated, l: vibrationListener{rng: rng}}
}

// loadSculkSensor brings back a saved sensor.
func (s *Host) loadSculkSensor(saved map[string]any, calibrated bool) *SculkSensor {
	e := s.newSculkSensor(calibrated)
	e.saved = maps.Clone(saved)
	delete(e.saved, vibrationListenerTag)
	e.l.load(saved)
	return e
}

// GameEventRange is how far the sensor hears.
func (e *SculkSensor) GameEventRange() int32 { return e.l.rng }

// HearGameEvent is SculkSensorVibrationConfig's checks, then the listener's.
func (e *SculkSensor) HearGameEvent(r *world.Region, pos cube.Pos, ev world.GameEvent) {
	if e.l.inFlight || gameEventFlags(ev.ID)&gameEventHeard == 0 || !e.listens(r, pos, ev) {
		return
	}
	if e.s.vibrationOccluded(r, floorPos(ev.Pos), pos) {
		return
	}
	e.l.consider(r, pos, ev)
}

// listens is the config's shouldListen: not its own block breaking or being placed, sculk_touch
// only on itself, only while inactive, and for a calibrated one only the frequency its input
// names.
func (e *SculkSensor) listens(r *world.Region, pos cube.Pos, ev world.GameEvent) bool {
	at := floorPos(ev.Pos)
	if at == pos && (ev.ID == gameEventBlockDestroy || ev.ID == gameEventBlockPlace) {
		return false
	}
	if ev.ID == gameEventSculkTouch && at != pos {
		return false
	}
	if phase := e.phase(r, pos); phase == sculkPhaseActive || phase == sculkPhaseCooldown {
		return false
	}
	if !e.calibrated {
		return true
	}
	in := int32(0)
	if fn := e.s.circuits.SculkInput; fn != nil {
		in = fn(r, pos)
	}
	return in == 0 || in == gameEventFrequency(ev.ID)
}

// phase is the sensor's sculk_sensor_phase.
func (e *SculkSensor) phase(r *world.Region, pos cube.Pos) int32 {
	b, ok := r.Block(pos, 0)
	if !ok {
		return 0
	}
	v, _ := e.s.reg.Blocks.States(b)[stateSculkPhase].(int32)
	return v
}

// TickBlockEntity moves the heard vibration along.
func (e *SculkSensor) TickBlockEntity(r *world.Region, pos cube.Pos) {
	e.l.tick(r, pos, func(v vibration) { e.receive(r, pos, v) })
}

// receive is SculkSensorVibrationConfig::onSignalReceive: an inactive sensor turns on with power
// by distance, max(1, 15 - floor(distance / range * 15)).
func (e *SculkSensor) receive(r *world.Region, pos cube.Pos, v vibration) {
	e.chunk.Changed()
	if e.phase(r, pos) == sculkPhaseActive {
		return
	}
	power := maxPower - int32(math.Floor(float64(v.dist/float32(e.l.rng)*maxPower)))
	power = max(1, power)
	e.s.activateSensor(r, pos, v, power)
}

// activateSensor is SculkSensorBlock::activate (0xD515AB0) past the block and its power: the
// power on sound unless waterlogged, amethyst blocks on any side resonating at the vibration's
// frequency, and the tendrils clicking event a shrieker hears.
func (s *Host) activateSensor(r *world.Region, pos cube.Pos, v vibration, power int32) {
	fn := s.circuits.ActivateSculk
	if fn == nil || !fn(r, pos, power) {
		return
	}
	if !s.waterlogged(r, pos) {
		playSound(r, pos, soundEvent{name: packet.SoundEventSculkSensorPowerOn, pos: blockVec(pos), extra: -1}, nil)
	}
	freq := gameEventFrequency(v.event)
	for _, f := range sculkFaces {
		at := pos.Side(f)
		if b, ok := r.Block(at, 0); ok && s.reg.Blocks.Name(b) == amethystBlock {
			s.resonate(r, at, v.src, freq)
		}
	}
	s.postBlockGameEvent(r, gameEventTendrilsClicking, pos, v.src, 0, false)
}

// resonate is an amethyst block beside an activated sensor: the resonate event of the frequency
// and its chime (LevelEvent 1067, the pitch as BDS works it out from its note table).
func (s *Host) resonate(r *world.Region, at cube.Pos, src *Player, freq int32) {
	if freq >= 1 && freq <= 15 {
		s.postBlockGameEvent(r, gameEventResonate1+freq-1, at, src, 0, false)
	}
	var pitch int32
	if freq >= 0 && freq < int32(len(resonateNotes)) {
		pitch = int32(math.Exp2(float64(resonateNotes[freq]-12)/12) * 1000)
	}
	levelEvent(r, at, blockVec(at), packet.LevelEventSoundAmethystResonate, pitch)
}

// sculkFaces are the six sides in BDS's Facing::DIRECTIONS order.
var sculkFaces = [...]cube.Face{cube.FaceDown, cube.FaceUp, cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast}

// resonateNotes are BDS's notes (0x1C645BC) by frequency.
var resonateNotes = [...]float64{0, 0, 2, 4, 6, 7, 9, 10, 12, 14, 15, 18, 19, 21, 22, 24}

// waterlogged reports water in the second layer at pos.
func (s *Host) waterlogged(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 1)
	return ok && s.reg.Blocks.Properties(b).Material == registry.MaterialWater
}

// ComparatorSignal is the last vibration's frequency while active, as BDS's.
func (e *SculkSensor) ComparatorSignal(r *world.Region, pos cube.Pos) int32 {
	if e.phase(r, pos) != sculkPhaseActive {
		return 0
	}
	return gameEventFrequency(e.l.latest)
}

// BindBlockEntity keeps the chunk the sensor is in.
func (e *SculkSensor) BindBlockEntity(h world.ChunkHandle) { e.chunk = h }

// SaveData is what BDS saves of a sensor.
func (e *SculkSensor) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = sculkSensorID
	if e.calibrated {
		m["id"] = calibratedSensorID
	}
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[vibrationListenerTag] = e.l.save()
	return m
}

// NetworkData is the same.
func (e *SculkSensor) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// SculkSensorQuiet is a sensor's cooldown ending: BDS's power off sound, unless waterlogged.
// Runs: owner of the region.
func (s *Host) SculkSensorQuiet(r *world.Region, pos cube.Pos) {
	if !s.waterlogged(r, pos) {
		playSound(r, pos, soundEvent{name: packet.SoundEventSculkSensorPowerOff, pos: blockVec(pos), extra: -1}, nil)
	}
}

// vibrationOccluded is BDS's _isVibrationOccluded: six lines from the vibration's block centre,
// each nudged 0.001 toward a face, to the listener's centre; it is blocked only when every one
// passes through wool.
func (s *Host) vibrationOccluded(r *world.Region, from, to cube.Pos) bool {
	end := blockCenter(to)
	for _, f := range sculkFaces {
		start := blockCenter(from).Add(blockVec(cube.Pos{}.Side(f)).Mul(0.001))
		if !s.lineHitsWool(r, start, end) {
			return false
		}
	}
	return true
}

// lineHitsWool walks the blocks a line passes through, as a clip does, and reports wool on it.
func (s *Host) lineHitsWool(r *world.Region, a, b mgl32.Vec3) bool {
	cell := floorPos(a)
	last := floorPos(b)
	d := b.Sub(a)
	var step [3]int
	var next, delta [3]float64
	for i := range 3 {
		switch {
		case d[i] > 0:
			step[i] = 1
			next[i] = (math.Floor(float64(a[i])) + 1 - float64(a[i])) / float64(d[i])
			delta[i] = 1 / float64(d[i])
		case d[i] < 0:
			step[i] = -1
			next[i] = (float64(a[i]) - math.Floor(float64(a[i]))) / -float64(d[i])
			delta[i] = -1 / float64(d[i])
		default:
			next[i], delta[i] = math.Inf(1), math.Inf(1)
		}
	}
	for range 64 {
		if blk, ok := r.Block(cell, 0); ok && s.occludesVibrations(blk) {
			return true
		}
		if cell == last {
			return false
		}
		axis := 0
		if next[1] < next[axis] {
			axis = 1
		}
		if next[2] < next[axis] {
			axis = 2
		}
		if next[axis] > 1 {
			return false
		}
		cell[axis] += step[axis]
		next[axis] += delta[axis]
	}
	return false
}

// SculkShrieker is a sculk shrieker's block entity. Runs: owner of its chunk.
type SculkShrieker struct {
	s     *Host
	saved map[string]any
	l     vibrationListener
	// queued is the warning level to answer when the shriek ends, if hasQueued.
	queued    int32
	hasQueued bool
	chunk     world.ChunkHandle
}

// loadShrieker brings back a saved shrieker.
func (s *Host) loadShrieker(saved map[string]any) *SculkShrieker {
	e := &SculkShrieker{s: s, l: vibrationListener{rng: shriekerRange}, saved: maps.Clone(saved)}
	e.l.load(saved)
	if v, ok := saved[queuedThreatTag].(int32); ok {
		e.queued, e.hasQueued = v, true
	}
	delete(e.saved, vibrationListenerTag)
	delete(e.saved, queuedThreatTag)
	return e
}

// GameEventRange is how far the shrieker hears.
func (e *SculkShrieker) GameEventRange() int32 { return e.l.rng }

// HearGameEvent is SculkShriekerVibrationConfig's checks: only a sensor's tendrils clicking set
// off by a player, while not shrieking.
func (e *SculkShrieker) HearGameEvent(r *world.Region, pos cube.Pos, ev world.GameEvent) {
	if e.l.inFlight || ev.ID != gameEventTendrilsClicking || e.shrieking(r, pos) {
		return
	}
	if _, ok := ev.Source.(*Player); !ok {
		return
	}
	if e.s.vibrationOccluded(r, floorPos(ev.Pos), pos) {
		return
	}
	e.l.consider(r, pos, ev)
}

// shrieking reports the shrieker's active state.
func (e *SculkShrieker) shrieking(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	v, _ := e.s.reg.Blocks.States(b)[stateShrieking].(uint8)
	return v != 0
}

// TickBlockEntity moves the heard vibration along; on arrival the player who set it off makes
// the shrieker shriek.
func (e *SculkShrieker) TickBlockEntity(r *world.Region, pos cube.Pos) {
	e.l.tick(r, pos, func(v vibration) {
		e.chunk.Changed()
		if v.src != nil {
			e.tryShriek(r, pos, v.src)
		}
	})
}

// tryShriek is SculkShriekerBlockActor::tryShriek: when it can summon, the warning level of the
// most warned player nearby goes up (none while any nearby is on cooldown or a warden is near,
// and then no shriek either) and is queued; then it shrieks.
func (e *SculkShrieker) tryShriek(r *world.Region, pos cube.Pos, p *Player) bool {
	if e.shrieking(r, pos) {
		return false
	}
	e.hasQueued = false
	if e.s.canRespond(r, pos) {
		level, ok := e.s.warn(r, pos, p)
		if !ok {
			return false
		}
		e.queued, e.hasQueued = level, true
	}
	e.s.shriek(r, pos, p)
	e.chunk.Changed()
	return true
}

// canRespond is BDS's _canRespond: the can_summon state, and a difficulty other than peaceful.
func (s *Host) canRespond(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	v, _ := s.reg.Blocks.States(b)[stateCanSummon].(uint8)
	return v != 0 && s.level.Difficulty != difficultyPeaceful
}

// warn is BDS's _tryWarn: no warden within the box around the shrieker; the players within 16
// blocks and the one who set it off; none on cooldown; the one with the highest level (the first
// on a tie) goes up and the others take its tracker.
func (s *Host) warn(r *world.Region, pos cube.Pos, by *Player) (int32, bool) {
	centre := blockCenter(pos)
	if s.wardenNear(r, centre) {
		return 0, false
	}
	players := append(s.playersNear(r, centre, warnRange, by), by)
	top := players[0]
	for _, o := range players[1:] {
		if o.warden.level > top.warden.level {
			top = o
		}
	}
	for _, o := range players {
		if o.warden.cooldown > 0 {
			return 0, false
		}
	}
	top.warden.raise()
	for _, o := range players {
		if o != top {
			o.warden = top.warden
		}
	}
	return top.warden.level, true
}

// wardenNear reports a warden in the 48 block box around centre.
func (s *Host) wardenNear(r *world.Region, centre mgl32.Vec3) bool {
	lo, hi := centre.Sub(mgl32.Vec3{wardenBox, wardenBox, wardenBox}), centre.Add(mgl32.Vec3{wardenBox, wardenBox, wardenBox})
	for _, e := range r.AppendEntitiesIn(nil, lo[0], lo[2], hi[0], hi[2]) {
		m, ok := e.Owner().(*Mob)
		if !ok || m.Type() != wardenType {
			continue
		}
		if p := e.Position(); p[0] >= lo[0] && p[0] <= hi[0] && p[1] >= lo[1] && p[1] <= hi[1] && p[2] >= lo[2] && p[2] <= hi[2] {
			return true
		}
	}
	return false
}

// playersNear is BDS's _getNearbyPlayers: living players, not spectating, closer than radius to
// centre, other than except.
func (s *Host) playersNear(r *world.Region, centre mgl32.Vec3, radius float32, except *Player) []*Player {
	var out []*Player
	for _, e := range r.AppendEntitiesIn(nil, centre[0]-radius, centre[2]-radius, centre[0]+radius, centre[2]+radius) {
		p, ok := e.Owner().(*Player)
		if !ok || p == except || p.dead() || p.spectating() {
			continue
		}
		if e.Position().Sub(centre).LenSqr() < radius*radius {
			out = append(out, p)
		}
	}
	return out
}

// shriek is BDS's _shriek: shrieking for 90 ticks, the shriek particles (LevelEvent 2035), the
// shriek sound unless waterlogged, and the shriek event.
func (s *Host) shriek(r *world.Region, pos cube.Pos, p *Player) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	on := s.withState(b, stateShrieking, uint8(1))
	r.ScheduleTick(pos, on, shriekTicks, 0)
	r.SetBlock(pos, 0, on)
	tags := map[string]any{"originX": int32(pos[0]), "originY": int32(pos[1]), "originZ": int32(pos[2])} //nolint:gosec // block coordinates fit int32
	if pk := levelEventGeneric(packet.LevelEventParticleSculkShriek, tags); pk != nil {
		r.Broadcast(pos, pk, nil)
	}
	if !s.waterlogged(r, pos) {
		playSound(r, pos, soundEvent{name: packet.SoundEventSculkShriekerShriek, pos: blockVec(pos), extra: -1}, nil)
	}
	s.postBlockGameEvent(r, gameEventShriek, pos, p, 0, false)
}

// withState returns block with one state changed, block itself if it cannot.
func (s *Host) withState(block uint32, key string, v any) uint32 {
	states := maps.Clone(s.reg.Blocks.States(block))
	if _, ok := states[key]; !ok {
		return block
	}
	states[key] = v
	if id, ok := s.reg.Blocks.StateID(s.reg.Blocks.Name(block), states); ok {
		return id
	}
	return block
}

// ShriekerTick is the shrieker's tick (SculkShriekerBlock::tick): it stops shrieking and
// responds. Runs: owner of the region.
func (s *Host) ShriekerTick(r *world.Region, pos cube.Pos, block uint32) {
	v, _ := s.reg.Blocks.States(block)[stateShrieking].(uint8)
	if v == 0 {
		return
	}
	if r.SetBlock(pos, 0, s.withState(block, stateShrieking, uint8(0))) {
		r.UpdateNeighbours(pos)
	}
	if be, ok := r.BlockEntity(pos); ok {
		if e, ok := be.(*SculkShrieker); ok {
			e.respond(r, pos)
		}
	}
}

// respond is BDS's tryRespond: at a queued level above 3 a warden is summoned; at a lower one,
// or if none could be, the warden's reply sound for the level plays near; then darkness falls
// on the players within 40 blocks.
func (e *SculkShrieker) respond(r *world.Region, pos cube.Pos) {
	if !e.hasQueued {
		return
	}
	level := e.queued
	e.hasQueued = false
	e.chunk.Changed()
	if !e.s.canRespond(r, pos) {
		return
	}
	if level <= maxThreatLevel-1 || !e.s.summonWarden(r, pos) {
		e.s.wardenReply(r, pos, level)
	}
	for _, p := range e.s.playersNear(r, blockCenter(pos), darknessRange, nil) {
		if !p.abilities.Flag(AbilityInvulnerable) {
			p.GiveEffect(ActiveEffect{ID: EffectDarkness, Duration: darknessTicks})
		}
	}
}

// wardenReplies are the warden's reply sounds by warning level.
var wardenReplies = [...]string{
	packet.SoundEventWardenNearbyClose, packet.SoundEventWardenNearbyCloser,
	packet.SoundEventWardenNearbyClosest, packet.SoundEventListeningAngry,
}

// wardenReply plays the reply sound of the level from a random place within 10 blocks.
func (s *Host) wardenReply(r *world.Region, pos cube.Pos, level int32) {
	if level <= 0 || int(level) > len(wardenReplies) {
		return
	}
	at := mgl32.Vec3{
		float32(pos[0] + rand.IntN(2*replySpread+1) - replySpread), //nolint:gosec // a sound position, not a secret
		float32(pos[1] + rand.IntN(2*replySpread+1) - replySpread), //nolint:gosec // a sound position, not a secret
		float32(pos[2] + rand.IntN(2*replySpread+1) - replySpread), //nolint:gosec // a sound position, not a secret
	}
	playSound(r, pos, soundEvent{name: wardenReplies[level-1], pos: at, extra: -1, entity: wardenType}, nil)
}

// summonWarden is BDS's _trySummonWarden through MobSpawnUtils::trySpawnMob: up to 20 tries at a
// random column within 5 blocks, each from 6 above down to 6 below for a free place on a solid
// block; the warden emerges there (minecraft:spawn_emerging) with the agitated sound. False if
// none was found.
func (s *Host) summonWarden(r *world.Region, pos cube.Pos) bool {
	for range summonTries {
		x := pos[0] + rand.IntN(2*summonSpread+1) - summonSpread //nolint:gosec // a spawn position, not a secret
		z := pos[2] + rand.IntN(2*summonSpread+1) - summonSpread //nolint:gosec // a spawn position, not a secret
		for y := pos[1] + summonHeight; y >= pos[1]-summonHeight; y-- {
			at := cube.Pos{x, y, z}
			if !s.wardenFits(r, at) {
				continue
			}
			feet := blockVec(at)
			o, ok := s.newMob(wardenType, feet, 0, definition.InitEvent, wardenEmerge)
			if !ok {
				return false
			}
			o.m.Ready(r)
			o.add(r.Dimension())
			playSound(r, at, soundEvent{name: packet.SoundEventWardenSlightlyAngry, pos: feet, extra: -1}, nil)
			return true
		}
	}
	return false
}

// wardenFits reports a solid block under at and the warden's height free above it. BDS's
// surface check and the spawned warden's own checks were not read; this is the plain rule.
func (s *Host) wardenFits(r *world.Region, at cube.Pos) bool {
	below, ok := r.Block(at.Side(cube.FaceDown), 0)
	if !ok || !s.solidBlock(below) {
		return false
	}
	for i := range wardenHeight {
		b, ok := r.Block(at.Add(cube.Pos{0, i, 0}), 0)
		if !ok || s.solidBlock(b) || s.reg.Blocks.Properties(b).Material == registry.MaterialWater {
			return false
		}
	}
	return true
}

// BindBlockEntity keeps the chunk the shrieker is in.
func (e *SculkShrieker) BindBlockEntity(h world.ChunkHandle) { e.chunk = h }

// SaveData is what BDS saves of a shrieker: the queued level only while one is queued.
func (e *SculkShrieker) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = sculkShriekerID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	if e.hasQueued {
		m[queuedThreatTag] = e.queued
	}
	m[vibrationListenerTag] = e.l.save()
	return m
}

// NetworkData is the same.
func (e *SculkShrieker) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// standOnSculk is BDS's onStandOn of the sculk blocks: a sensor hears sculk_touch at itself, a
// shrieker shrieks for the player.
func (p *Player) standOnSculk(r *world.Region, at cube.Pos) {
	b, ok := r.Block(at, 0)
	if !ok {
		return
	}
	switch p.s.reg.Blocks.Name(b) {
	case sculkSensorBlock, calibratedBlock:
		p.s.postBlockGameEvent(r, gameEventSculkTouch, at, p, 0, false)
	case sculkShriekerBlock:
		if be, ok := r.BlockEntity(at); ok {
			if e, ok := be.(*SculkShrieker); ok {
				e.tryShriek(r, at, p)
			}
		}
	}
}

// wardenTracker is BDS's WardenSpawnTrackerComponent on a player: the warning level, the
// cooldown before it may rise again, and the timer that brings it down.
type wardenTracker struct {
	level, cooldown, timer int32
}

// raise is tryIncreaseThreatLevel, called only when the cooldown is over.
func (w *wardenTracker) raise() {
	w.level = min(max(w.level, -1)+1, maxThreatLevel)
	w.cooldown, w.timer = threatCooldownTicks, threatDecreaseTicks
}

// tick counts the cooldown down and lowers the level when its timer runs out.
func (w *wardenTracker) tick() {
	if w.level > 0 {
		if w.timer > 0 {
			w.timer--
		}
		if w.timer <= 0 {
			w.level = min(max(w.level-1, 0), maxThreatLevel)
			w.timer = threatDecreaseTicks
		}
	}
	if w.cooldown > 0 {
		w.cooldown--
	}
}

// load and save use BDS's keys.
func (w *wardenTracker) load(saved map[string]any) {
	w.level, _ = saved[wardenThreatTag].(int32)
	w.cooldown, _ = saved[wardenCooldownTag].(int32)
	w.timer, _ = saved[wardenDecreaseTag].(int32)
}

func (w *wardenTracker) save(saved map[string]any) {
	saved[wardenThreatTag] = w.level
	saved[wardenCooldownTag] = w.cooldown
	saved[wardenDecreaseTag] = w.timer
}
