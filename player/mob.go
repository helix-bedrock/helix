package player

import (
	"math"
	"math/rand/v2"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// The engine side of mobs: the Host answers the mob runtime's questions (mob.Env), and Mob
// is a mob's entity, its packets and the hits between mobs and players. What a mob does is
// all in its definition, run by package mob.
//
// First hookup only: mobs are not saved (one whose chunk unloads is gone; natural
// spawning is in naturalspawn.go, despawning in mob/despawn.go), drop no loot or experience, and are hurt outside the Hit pipeline (no armour,
// no HitEvent).

// setUpMobs reads every entity definition the mob runtime can run, at boot.
func (s *Host) setUpMobs() {
	s.mobs = mob.NewRuntime(mob.NewBlocks(s.reg.Blocks), s, s.log)
	for _, t := range s.reg.EntityTypes.All() {
		if t.Name == registry.Player || len(t.Definition) == 0 {
			continue
		}
		if err := s.mobs.Parse(t.Name, t.Definition); err != nil {
			s.log.Debug("entity definition not read", "type", t.Name, "err", err)
		}
	}
	if err := s.setUpSpawning(); err != nil {
		s.log.Error("natural mob spawning off", "err", err)
	}
}

// CanSpawnMob reports whether typ has a definition the mob runtime runs.
func (s *Host) CanSpawnMob(typ string) bool { return s.mobs != nil && s.mobs.Has(typ) }

// Mob is a mob in the world: its entity and the mob runtime's state. Runs: owner of its
// entity.
type Mob struct {
	s      *Host
	entity *world.Entity
	m      *mob.Mob
	moves  actorMoves
	// surface is set for a mob the natural spawner put on the surface; the spawner counts
	// surface and underground mobs apart.
	surface bool
	// died is set once the death event went out.
	died bool
	gone bool
	// ended is set by think when the mob's death has run its course; apply removes it.
	ended bool
	// items is the mob's real equipment (mobequip.go); needEquip is set until a created
	// mob has rolled its equipment table. lastPlayer is the last player to hurt it.
	items      mobItems
	needEquip  bool
	lastPlayer *Player
}

// Entity returns the mob's entity.
func (m *Mob) Entity() *world.Entity { return m.entity }

// Type is the mob's entity type.
func (m *Mob) Type() string { return m.m.Type() }

// Health is the mob's health.
func (m *Mob) Health() float32 { return m.m.HealthValue() }

// SpawnMob adds a mob of the type with its feet at pos, as a spawned mob (its definition's
// minecraft:entity_spawned event runs). False if the type has no definition. Runs:
// anywhere.
func (s *Host) SpawnMob(d *world.Dimension, typ string, pos mgl32.Vec3, yaw float32) (*Mob, bool) {
	if s.mobs == nil {
		return nil, false
	}
	o, ok := s.newMob(typ, pos, yaw, definition.InitSpawned, "")
	if !ok {
		return nil, false
	}
	o.add(d)
	return o, true
}

// newMob makes a mob that is not in the world yet: its definition runs once add puts it
// there. initEvent is the event to start with when method is definition.InitEvent.
func (s *Host) newMob(typ string, pos mgl32.Vec3, yaw float32, method definition.InitMethod, initEvent string) (*Mob, bool) {
	if s.mobs == nil {
		return nil, false
	}
	o := &Mob{s: s, needEquip: method != definition.InitLoaded}
	uid := itemUniqueIDBase | rand.Int64N(itemUniqueIDBase) //nolint:gosec // an entity ID, not a secret
	rid := s.runtimeIDs.Add(1)
	m, err := s.mobs.NewWithEvent(typ, uid, rid, vec(pos), yaw, o, method, initEvent)
	if err != nil {
		return nil, false
	}
	o.m = m
	return o, true
}

// add puts a mob from newMob in the dimension at its current position.
func (o *Mob) add(d *world.Dimension) {
	pos := mglVec(o.m.Pos())
	_, yaw, head, _ := o.m.Rotation()
	rot := world.Rotation{Yaw: yaw, HeadYaw: head}
	o.moves.start(pos, rot, actorMoveDropped, actorMoveFar)
	o.moves.head = true
	o.entity = d.AddEntity(world.EntityConfig{
		UniqueID: int64(o.m.ID()), RuntimeID: o.m.RuntimeID(), Pos: pos, Rot: rot,
		Owner: o, Spawn: o.spawnPackets, Move: o.movePacket, Think: o.think, Input: o.apply,
	})
}

func vec(p mgl32.Vec3) behavior.Vec3 { return behavior.Vec3{X: p[0], Y: p[1], Z: p[2]} }

func mglVec(p behavior.Vec3) mgl32.Vec3 { return mgl32.Vec3{p.X, p.Y, p.Z} }

// metadata is the mob's actor data: flags, size and air.
func (m *Mob) metadata() protocol.EntityMetadata {
	f := m.m.Flags()
	w, h := m.m.Size()
	air, maxAir := m.m.Air()
	return protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:    int64(f[0]), //nolint:gosec // a bit set sent as int64
		protocol.EntityDataKeyFlagsTwo: int64(f[1]), //nolint:gosec // a bit set sent as int64
		protocol.EntityDataKeyWidth:    w, protocol.EntityDataKeyHeight: h,
		protocol.EntityDataKeyScale:     m.m.Scale(),
		protocol.EntityDataKeyAirSupply: air, protocol.EntityDataKeyAirSupplyMax: maxAir,
	}
}

// spawnPackets show the mob. Not yet compared with what BDS sends.
func (m *Mob) spawnPackets(e *world.Entity) []packet.Packet {
	pitch, yaw, head, body := m.m.Rotation()
	return m.spawnEquipment([]packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: m.m.Type(),
		Position: e.Position(), Velocity: mglVec(m.m.Body().Vel),
		Pitch: pitch, Yaw: yaw, HeadYaw: head, BodyYaw: body,
		Attributes: []protocol.AttributeValue{
			{Name: "minecraft:health", Value: m.m.HealthValue(), Max: m.m.MaxHealth()},
			{Name: "minecraft:movement", Value: m.m.MovementSpeed(), Max: math.MaxFloat32},
		},
		EntityMetadata: m.metadata(),
	}})
}

// movePacket sends its move with BDS's actor movement sync, head yaw included.
func (m *Mob) movePacket(e *world.Entity) packet.Packet {
	m.moves.update(e, mglVec(m.m.Body().Vel))
	return nil
}

// think runs the mob's AI and movement: the think phase, maybe on another core while other
// mobs think too. It changes only the mob; the rest waits for apply.
func (m *Mob) think(e *world.Entity) {
	r := e.Region()
	if r == nil || m.gone {
		return
	}
	m.ended = m.m.Tick(r)
}

// apply does what the mob decided this tick, in the region's runtime ID order: its queued
// effects, the void check, the shared hazards (fire, lava, contact blocks, landing) and the
// move of its entity.
func (m *Mob) apply(e *world.Entity) {
	if e.Region() == nil || m.gone {
		return
	}
	m.m.Apply()
	if m.ended {
		if r := e.Region(); r != nil {
			m.dropExperience(r)
		}
		m.remove(e)
		return
	}
	if m.needEquip {
		m.equipFromTable(e.Region())
	}
	if m.s.checkVoid(e); m.gone || e.Region() == nil || m.m.Dead() {
		return
	}
	if m.tickHazards(e.Region()); m.gone || e.Region() == nil {
		return
	}
	b := m.m.Body()
	pitch, yaw, head, _ := m.m.Rotation()
	if !e.Apply(world.Movement{Pos: mglVec(b.Pos), Rot: world.Rotation{Pitch: pitch, Yaw: yaw, HeadYaw: head}, OnGround: b.OnGround, Tick: e.Tick()}) {
		m.m.SetPos(vec(e.Position()))
		b.Vel = behavior.Vec3{}
	}
}

func (m *Mob) remove(e *world.Entity) {
	m.gone = true
	e.Remove()
}

// OutOfWorld removes the mob in the void.
func (m *Mob) OutOfWorld(_ *Host, e *world.Entity) { m.remove(e) }

// hurt takes damage from an attacker (nil for none), shows the hurt or death and pushes
// the mob away from the attacker. It reports the damage taken.
func (m *Mob) hurt(amount float32, by *Player, cause DamageCause) float32 {
	r := m.entity.Region()
	if r == nil || m.gone {
		return 0
	}
	id := behavior.NoID
	if by != nil && by.entity != nil {
		id = behavior.ID(by.entity.UniqueID())
		m.lastPlayer = by
	}
	dealt := m.m.Hurt(amount, id, int(cause), uint64(max(r.CurrentTick(), 0))) //nolint:gosec // clamped
	if dealt <= 0 {
		return 0
	}
	m.entity.Send(actorEvent(m.entity.RuntimeID(), packet.ActorEventHurt))
	if by != nil && by.entity != nil {
		away := m.entity.Position().Sub(by.feet())
		v := by.knockbackStep(mglVec(m.m.Body().Vel), away[0], away[2], by.Tuning(TuneKnockbackPush), by.Tuning(TuneKnockbackLift))
		m.m.Knock(vec(v))
		m.entity.Send(&packet.SetActorMotion{EntityRuntimeID: m.entity.RuntimeID(), Velocity: v, Tick: m.entity.Tick()})
	}
	if m.m.Dead() && !m.died {
		m.died = true
		m.entity.Send(actorEvent(m.entity.RuntimeID(), packet.ActorEventDeath))
		m.dropOnDeath(r)
	}
	return dealt
}

// feet is where the player stands: its entity's position is its eyes.
func (p *Player) feet() mgl32.Vec3 {
	return p.entity.Position().Sub(mgl32.Vec3{0, playerEyeHeight, 0})
}

// --- mob.Hooks ---

// Attack is the mob's melee hit on a player: its attack damage through the TakeHit
// system, then a push away from the mob. BDS's difficulty scaling of mob damage and hits
// on other mobs are not built yet.
func (m *Mob) Attack(_ *mob.Mob, target behavior.ID) (hit, killed bool) {
	r := m.entity.Region()
	if r == nil {
		return false, false
	}
	p, ok := m.s.entityByID(r, target).(*Player)
	if !ok || p.entity == nil || p.dead() {
		return false, false
	}
	h := NewHit(p, m.m.AttackDamage(), DamageSource{Cause: DamageEntityAttack})
	if !p.s.game.sys.TakeHit(h) {
		return false, false
	}
	if !p.dead() {
		away := p.feet().Sub(m.entity.Position())
		resist := max(1-p.attributes.get(attrKnockbackResistance), 0) * p.Tuning(TuneKnockbackTakenScale)
		vel := p.knockbackStep(p.move.velocity, away[0], away[2], p.Tuning(TuneKnockbackPush)*resist, p.Tuning(TuneKnockbackLift)*resist)
		p.move.velocity = vel
		p.send(&packet.SetActorMotion{EntityRuntimeID: p.runtimeID, Velocity: vel, Tick: p.entity.Tick()})
		p.flush()
	}
	return true, p.dead()
}

// Swing shows the mob's arm swing. Not compared with BDS: it may send an ActorEvent
// instead.
func (m *Mob) Swing(*mob.Mob) {
	m.entity.Send(&packet.Animate{ActionType: packet.AnimateActionSwingArm, EntityRuntimeID: m.entity.RuntimeID()})
}

// Sound plays a level sound event the mob made, for those who see it.
func (m *Mob) Sound(_ *mob.Mob, n int, at behavior.Vec3) {
	name := mob.SoundName(n)
	if name == "" || m.entity == nil || m.entity.Region() == nil {
		return
	}
	m.entity.Send(soundEvent{name: name, pos: mglVec(at), extra: -1, entity: m.m.Type()}.packet())
}

// ActorEvent sends an actor event about the mob to those who see it.
func (m *Mob) ActorEvent(_ *mob.Mob, id int) {
	if m.entity == nil || m.entity.Region() == nil || id < 0 || id > math.MaxUint8 {
		return
	}
	m.entity.Send(actorEvent(m.entity.RuntimeID(), uint8(id)))
}

// Hurt is damage the mob's own state deals it (drowning, burning, hurt_on_condition).
func (m *Mob) Hurt(_ *mob.Mob, amount float32, cause int) {
	if m.entity == nil {
		return
	}
	m.hurt(amount, nil, DamageCause(cause))
}

// FlagsChanged sends the mob's new actor data.
func (m *Mob) FlagsChanged(*mob.Mob) {
	if m.entity == nil || m.entity.Region() == nil {
		return
	}
	m.entity.Send(actorData(m.entity.RuntimeID(), m.metadata()))
}

// --- player hits mob ---

// useOnMob is a use on actor transaction aimed at a mob: an attack hits it with BDS's
// player attack damage (attribute, weapon, enchantments, effects, critical hit), a click
// does nothing yet. False if it was not aimed at a mob.
func (p *Player) useOnMob(d *protocol.UseItemOnEntityTransactionData) bool {
	o := p.mobNear(d.TargetEntityRuntimeID)
	if o == nil {
		return false
	}
	if d.ActionType != protocol.UseItemOnEntityActionAttack {
		return true
	}
	if !p.selectSlot(d.HotBarSlot, d.HeldItem.Stack.NetworkID) {
		p.sendHeld()
		p.flush()
		return true
	}
	pos := p.entity.Position()
	limit := float32(attackRange)
	if p.creative() {
		limit = attackRangeCreative
	}
	if pos.Sub(d.Position).Len() > attackFromRange || pos.Sub(o.entity.Position()).Len() > limit {
		return true
	}
	if !p.abilities.Flag(AbilityAttackMobs) {
		return true
	}
	r := p.entity.Region()
	if r == nil {
		return true
	}
	h := p.s.game.sys.AttackParts(p, nil)
	sound := packet.SoundEventAttackNoDamage
	if h != nil {
		critical := h.Critical()
		if o.hurt(h.addBonuses(h.Base), p, DamageEntityAttack) > 0 {
			sound = packet.SoundEventAttackStrong
			if critical {
				sound = packet.SoundEventAttackCritical
				o.entity.Send(&packet.Animate{ActionType: packet.AnimateActionCriticalHit, EntityRuntimeID: o.entity.RuntimeID(), Data: critEventData})
			}
			h.attackerAfter()
		}
	}
	playSound(r, p.blockPos(), soundEvent{name: sound, pos: pos, extra: -1, entity: "minecraft:player"}, p.viewer)
	p.flush()
	return true
}

// mobNear finds a mob near the player by its runtime ID, in the player's region.
func (p *Player) mobNear(id uint64) *Mob {
	r := p.entity.Region()
	if r == nil {
		return nil
	}
	pos := p.entity.Position()
	p.touching = r.AppendEntitiesIn(p.touching[:0], pos[0]-attackScan, pos[2]-attackScan, pos[0]+attackScan, pos[2]+attackScan)
	defer clear(p.touching)
	for _, e := range p.touching {
		if e.RuntimeID() != id {
			continue
		}
		if o, ok := e.Owner().(*Mob); ok && !o.gone && !o.m.Dead() {
			return o
		}
	}
	return nil
}

// --- mob.Env ---

// mobScratch is a buffer for entity scans of the mob runtime. Scans run while many mobs
// think at once, so each takes its own from the Host's pool and gives it back.
type mobScratch struct {
	list []*world.Entity
	pool *sync.Pool
}

func (s *Host) scratch() *mobScratch {
	if sc, ok := s.mobScratch.Get().(*mobScratch); ok {
		return sc
	}
	return &mobScratch{pool: &s.mobScratch}
}

func (sc *mobScratch) done() {
	clear(sc.list)
	sc.list = sc.list[:0]
	sc.pool.Put(sc)
}

// actorOf is the entity as the mob runtime sees it: players and mobs, nil for the rest. A
// mob is its snapshot, since it may be thinking on another core; players do not change
// while mobs think.
func actorOf(e *world.Entity) behavior.Actor {
	switch o := e.Owner().(type) {
	case *Player:
		if o.entity != nil {
			return playerActor{o}
		}
	case *Mob:
		if !o.gone {
			return o.m.Snapshot()
		}
	}
	return nil
}

// entityByID is the owner of the entity with the unique ID in r, nil when there is none. It
// scans the region: a per-region index is still to come.
func (s *Host) entityByID(r *world.Region, id behavior.ID) any {
	if id == behavior.NoID {
		return nil
	}
	sc := s.scratch()
	defer sc.done()
	sc.list = r.AppendEntities(sc.list)
	for _, e := range sc.list {
		if behavior.ID(e.UniqueID()) == id {
			return e.Owner()
		}
	}
	return nil
}

// Mob is the mob with the unique ID in r, for the changes mobs make to each other.
func (s *Host) Mob(r *world.Region, id behavior.ID) *mob.Mob {
	if o, ok := s.entityByID(r, id).(*Mob); ok && !o.gone {
		return o.m
	}
	return nil
}

// Boxes are the block's collision boxes at pos.
func (s *Host) Boxes(r *world.Region, pos cube.Pos, b uint32) []registry.Box {
	return s.boxesAt(r, pos, b)
}

// Actor finds an entity by unique ID in r. It scans the region: a per-region index is
// still to come.
func (s *Host) Actor(r *world.Region, id behavior.ID) behavior.Actor {
	if id == behavior.NoID {
		return nil
	}
	sc := s.scratch()
	defer sc.done()
	sc.list = r.AppendEntities(sc.list)
	for _, e := range sc.list {
		if behavior.ID(e.UniqueID()) == id {
			return actorOf(e)
		}
	}
	return nil
}

// Entities appends the players and mobs whose boxes touch b.
func (s *Host) Entities(r *world.Region, dst []behavior.Actor, self behavior.ID, b behavior.Box) []behavior.Actor {
	sc := s.scratch()
	defer sc.done()
	// Look a little wider than b: entity positions are their feet or eyes, not their box.
	sc.list = r.AppendEntitiesIn(sc.list, b.Min.X-2, b.Min.Z-2, b.Max.X+2, b.Max.Z+2)
	for _, e := range sc.list {
		a := actorOf(e)
		if a == nil || a.ID() == self {
			continue
		}
		bx := a.Box()
		if bx.Min.X < b.Max.X && bx.Max.X > b.Min.X && bx.Min.Y < b.Max.Y && bx.Max.Y > b.Min.Y && bx.Min.Z < b.Max.Z && bx.Max.Z > b.Min.Z {
			dst = append(dst, a)
		}
	}
	return dst
}

// Players appends the players in r. Players in other regions of the dimension are too far
// for any goal to reach.
func (s *Host) Players(r *world.Region, dst []behavior.Actor) []behavior.Actor {
	sc := s.scratch()
	defer sc.done()
	sc.list = r.AppendEntities(sc.list)
	for _, e := range sc.list {
		if p, ok := e.Owner().(*Player); ok && p.entity != nil {
			dst = append(dst, playerActor{p})
		}
	}
	return dst
}

// DimensionID is the dimension's BDS number.
func (s *Host) DimensionID(d *world.Dimension) int32 { return s.dimensionID(d) }

// DefaultGameMode is the level's game mode.
func (s *Host) DefaultGameMode() int { return int(s.level.GameMode) }

// Difficulty is the level's difficulty.
func (s *Host) Difficulty() int32 { return s.level.Difficulty }

// MobGriefing is the mobgriefing game rule.
func (s *Host) MobGriefing() bool { return s.level.MobGriefing }

// SkyDarken is the sky darkening from the clock and the overworld's weather.
func (s *Host) SkyDarken(r *world.Region) int32 {
	if s.clock == nil {
		return 0
	}
	return daytime.SkyDarken(s.clock.Time(), s.clock.Cycle(), s.Rain(r), s.Thunder(r))
}

// Rain is the overworld's rain level; other dimensions have none.
func (s *Host) Rain(r *world.Region) float32 {
	if s.weather == nil || r.Dimension() != s.overworld {
		return 0
	}
	return s.weather.Rain()
}

// Thunder is the overworld's thunder level.
func (s *Host) Thunder(r *world.Region) float32 {
	if s.weather == nil || r.Dimension() != s.overworld {
		return 0
	}
	return s.weather.Thunder()
}

// LevelEvent sends a level event with the block's network hash as data.
func (s *Host) LevelEvent(r *world.Region, id int32, p behavior.Vec3, block string) {
	var data int32
	if block != "" {
		if b, ok := s.reg.Blocks.StateID(block, nil); ok {
			data = int32(s.reg.Blocks.Hash(b)) //nolint:gosec // hashes are sent as int32
		}
	}
	pos := mglVec(p)
	levelEvent(r, vecPos(pos), pos, id, data)
}

// BreakBlock breaks the block as a mob does (a zombie through a door): the block goes
// with its break particles. Its drops are not built yet.
func (s *Host) BreakBlock(r *world.Region, p behavior.Pos, _ behavior.ID) {
	at := cube.Pos{int(p.X), int(p.Y), int(p.Z)}
	b, ok := r.Block(at, 0)
	if !ok || b == s.reg.Blocks.Air() {
		return
	}
	r.SetBlock(at, 0, s.reg.Blocks.Air())
	r.UpdateNeighbours(at)
	levelEvent(r, at, blockCenter(at), levelEventDestroyParticles, int32(s.reg.Blocks.Hash(b))) //nolint:gosec // hashes are sent as int32
}

// SetBlock places the block a goal chose (an enderman's held block, a sheep's grass).
func (s *Host) SetBlock(r *world.Region, p behavior.Pos, b behavior.PlacedBlock, _ behavior.ID) {
	states := map[string]any(nil)
	if b.State != "" {
		states = map[string]any{b.State: b.Value}
	}
	id, ok := s.reg.Blocks.StateID(b.Name, states)
	if !ok {
		if id, ok = s.reg.Blocks.StateID(b.Name, nil); !ok {
			return
		}
	}
	at := cube.Pos{int(p.X), int(p.Y), int(p.Z)}
	r.SetBlock(at, 0, id)
	r.UpdateNeighbours(at)
}

// Particle spawns a named particle effect for the players near it.
func (s *Host) Particle(r *world.Region, name string, p behavior.Vec3) {
	pos := mglVec(p)
	r.Broadcast(vecPos(pos), &packet.SpawnParticleEffect{Dimension: byte(s.dimensionID(r.Dimension())), EntityUniqueID: -1, Position: pos, ParticleName: name}, nil) //nolint:gosec // dimension IDs fit a byte
}

// --- players as the mob runtime sees them ---

// playerActor is a player as a behavior.Actor. Runs: owner of the player's region, which
// is the region of the mobs asking.
type playerActor struct{ p *Player }

func (a playerActor) ID() behavior.ID    { return behavior.ID(a.p.entity.UniqueID()) }
func (a playerActor) Pos() behavior.Vec3 { return vec(a.p.feet()) }
func (a playerActor) Box() behavior.Box {
	f := a.p.feet()
	return behavior.Box{
		Min: behavior.Vec3{X: f[0] - playerWidth/2, Y: f[1], Z: f[2] - playerWidth/2},
		Max: behavior.Vec3{X: f[0] + playerWidth/2, Y: f[1] + playerHeight, Z: f[2] + playerWidth/2},
	}
}
func (a playerActor) Eyes() behavior.Vec3 { return vec(a.p.entity.Position()) }
func (a playerActor) Alive() bool         { return !a.p.dead() }
func (a playerActor) Removed() bool       { return false }
func (a playerActor) Pickable() bool      { return !a.p.dead() && a.p.gameMode != GameModeSpectator }
func (a playerActor) IsMob() bool         { return true }
func (a playerActor) IsPlayer() bool      { return true }

// Flag answers no actor flag yet; goals read only baby, sitting and the like from players.
func (a playerActor) Flag(int) bool         { return false }
func (a playerActor) Type() string          { return registry.Player }
func (a playerActor) GameMode() int         { return int(a.p.gameMode) }
func (a playerActor) Riders() []behavior.ID { return nil }
func (a playerActor) Vehicle() behavior.ID  { return behavior.NoID }
func (a playerActor) Owner() behavior.ID    { return behavior.NoID }
func (a playerActor) Team() int             { return 0 }
func (a playerActor) Dimension() int        { return int(a.p.s.dimensionID(a.p.dim)) }
func (a playerActor) HeadRotation() (pitch, yaw float32) {
	r := a.p.entity.Rotation()
	return r.Pitch, r.HeadYaw
}
func (a playerActor) BodyYaw() float32                  { return a.p.entity.Rotation().Yaw }
func (a playerActor) Invulnerable() bool                { return a.p.invulnerable() }
func (a playerActor) HasHealth() bool                   { return true }
func (a playerActor) Targetable() bool                  { return true }
func (a playerActor) Monster() bool                     { return false }
func (a playerActor) ArmourCoverage() float32           { return 0 }
func (a playerActor) WearsHeadOf(string) bool           { return false }
func (a playerActor) Target() behavior.ID               { return behavior.NoID }
func (a playerActor) HijacksMount() bool                { return false }
func (a playerActor) LastHurtByMob() behavior.ID        { return behavior.NoID }
func (a playerActor) LastHurtMob() (behavior.ID, int32) { return behavior.NoID, 0 }
func (a playerActor) BlocksSpace() bool                 { return false }
func (a playerActor) AttachPos(int) behavior.Vec3       { return a.Eyes() }

var (
	_ mob.Env        = (*Host)(nil)
	_ mob.Hooks      = (*Mob)(nil)
	_ behavior.Actor = playerActor{}
)
