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
	"github.com/helix-bedrock/helix/village"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// The engine side of mobs: the Host answers the mob runtime's questions (mob.Env), and Mob
// is a mob's entity, its packets and the hits between mobs and players. What a mob does is
// all in its definition, run by package mob.
//
// Mobs are saved with their chunk in BDS's actor format (mobsave.go); natural spawning is
// in naturalspawn.go, monster spawners in spawner.go, despawning in mob/despawn.go. Mobs are
// still hurt outside the Hit pipeline (no armour, no HitEvent).

// topSupport is canProvideSupport on a block's top face, which a teleporting mob stands
// on: the attach table's answer, or blocking motion before it is loaded.
func (s *Host) topSupport(id uint32) bool {
	if s.attach == nil {
		return s.reg.Blocks.Properties(id).SolidMaterial
	}
	return s.attach.Supports(id, cube.FaceUp)
}

// setUpMobs reads every entity definition the mob runtime can run, at boot.
func (s *Host) setUpMobs() {
	blocks := mob.NewBlocks(s.reg.Blocks)
	blocks.SetTopSupport(s.topSupport)
	s.mobs = mob.NewRuntime(blocks, s, s.log)
	s.mobs.ItemRules = itemRules{s}
	for _, t := range s.reg.EntityTypes.All() {
		if t.Name == registry.Player || len(t.Definition) == 0 {
			continue
		}
		if err := s.mobs.Parse(t.Name, t.Definition); err != nil {
			s.log.Debug("entity definition not read", "type", t.Name, "err", err)
		}
	}
	s.setUpVillages(village.Vanilla())
	if err := s.loadTradeTables(); err != nil {
		s.log.Error("trade tables not read", "err", err)
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
	// hazards are the hazard blocks its box touched when it thought (thinkHazards).
	hazards thoughtHazards
	// surface is set for a mob the natural spawner put on the surface; the spawner counts
	// surface and underground mobs apart.
	surface bool
	// sentAttrs are the attributes viewers were last sent (mobattrs.go).
	sentAttrs mobAttrs
	// sentBody is the body armour viewers were last sent (mobattrs.go).
	sentBody ItemStack
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
	// saved is the data the mob was loaded from: fields Helix does not run yet are written
	// back unchanged (mobsave.go).
	saved map[string]any
	// naturalSpawn is set for a mob the natural spawner or a monster spawner made (BDS's
	// NaturalSpawn).
	naturalSpawn bool
	// stand is an armour stand's own state, nil for any other mob (armorstand.go).
	stand *armorStand
	// absorption is the mob's absorption attribute (Absorption's extra health); absorbed is
	// what the hurt being run took off it (mobeffects.go).
	absorption, absorbed float32
	// trades are a villager's offers (trade.go), nil without a trade table.
	trades *villagerTrades
	// horseInv and horseView are the window slots of a horse container and who has them
	// open (mobhorse.go).
	horseInv  *Inventory
	horseView *horseHolder
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
	uid := itemUniqueIDBase | rand.Int64N(itemUniqueIDBase) //nolint:gosec // an entity ID, not a secret
	return s.newMobID(uid, typ, pos, yaw, method, initEvent)
}

// newMobID is newMob with the unique ID given, as a saved mob keeps its own.
func (s *Host) newMobID(uid int64, typ string, pos mgl32.Vec3, yaw float32, method definition.InitMethod, initEvent string) (*Mob, bool) {
	if s.mobs == nil {
		return nil, false
	}
	o := &Mob{s: s, needEquip: method != definition.InitLoaded}
	rid := s.runtimeIDs.Add(1)
	m, err := s.mobs.NewWithEvent(typ, uid, rid, vec(pos), yaw, o, method, initEvent)
	if err != nil {
		return nil, false
	}
	o.m = m
	if typ == armorStandID {
		o.stand = &armorStand{}
	}
	return o, true
}

// add puts a mob from newMob in the dimension at its current position.
func (o *Mob) add(d *world.Dimension) {
	pos := mglVec(o.m.Pos())
	_, yaw, head, _ := o.m.Rotation()
	rot := world.Rotation{Yaw: yaw, HeadYaw: head}
	o.moves.start(pos, rot, actorMoveDropped, actorMoveFar)
	o.moves.head = true
	o.entity = d.AddEntity(o.config(pos, rot))
	e := o.entity
	o.m.SetBoxListener(func(b behavior.Box) { e.SetActorBox(mglVec(b.Min), mglVec(b.Max)) })
}

// config is the mob's entity, saved with its chunk.
func (o *Mob) config(pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: int64(o.m.ID()), RuntimeID: o.m.RuntimeID(), Pos: pos, Rot: rot,
		Owner: o, Actor: true, Spawn: o.spawnPackets, Move: o.movePacket, Think: o.think, Input: o.apply, Save: o.saveEntity,
	}
}

func vec(p mgl32.Vec3) behavior.Vec3 { return behavior.Vec3{X: p[0], Y: p[1], Z: p[2]} }

func mglVec(p behavior.Vec3) mgl32.Vec3 { return mgl32.Vec3{p.X, p.Y, p.Z} }

// metadata is the mob's actor data: flags, size, air, its target's unique ID (0 for none,
// as Mob::setTarget writes it) and what its components wrote.
func (m *Mob) metadata() protocol.EntityMetadata {
	f := m.m.Flags()
	w, h := m.m.Size()
	air, maxAir := m.m.Air()
	md := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:    int64(f[0]), //nolint:gosec // a bit set sent as int64
		protocol.EntityDataKeyFlagsTwo: int64(f[1]), //nolint:gosec // a bit set sent as int64
		protocol.EntityDataKeyWidth:    w, protocol.EntityDataKeyHeight: h,
		protocol.EntityDataKeyScale:     m.m.Scale(),
		protocol.EntityDataKeyAirSupply: air, protocol.EntityDataKeyAirSupplyMax: maxAir,
		protocol.EntityDataKeyTarget:            targetData(m.m.Target()),
		protocol.EntityDataKeyVisibleMobEffects: m.visibleEffects(),
	}
	for _, d := range m.m.ActorData() {
		md[uint32(d.Key)] = dataWireValue(uint32(d.Key), d.Value) //nolint:gosec // actor data keys are small
	}
	if m.stand != nil {
		md[protocol.EntityDataKeyPoseIndex] = m.stand.pose
	}
	return md
}

// targetData is a target as actor data 6: its unique ID, or 0 for none as BDS writes it.
// The client raises a skeleton's bow for any other value, so NoID (-1) must not go out.
func targetData(id behavior.ID) int64 {
	if id == behavior.NoID {
		return 0
	}
	return int64(id)
}

// spawnPackets show the mob. Not yet compared with what BDS sends.
func (m *Mob) spawnPackets(e *world.Entity) []packet.Packet {
	pitch, yaw, head, body := m.m.Rotation()
	a := m.currentAttrs()
	m.sentAttrs, m.sentBody = a, m.bodyItem()
	attrs := []protocol.AttributeValue{
		{Name: "minecraft:health", Value: m.m.HealthValue(), Max: a.maxHealth},
		{Name: attrMovementName, Value: a.speed, Max: math.MaxFloat32},
	}
	// A rider's client jumps a horse it steers by the horse's jump strength.
	if a.hasJump {
		attrs = append(attrs, protocol.AttributeValue{Name: attrJumpStrengthName, Value: a.jump, Max: max(a.jumpMax, a.jump)})
	}
	return m.spawnEquipment([]packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: m.m.Type(),
		Position: e.Position(), Velocity: mglVec(m.m.Body().Vel),
		Pitch: pitch, Yaw: yaw, HeadYaw: head, BodyYaw: body,
		Attributes: attrs, EntityMetadata: m.metadata(), EntityLinks: m.rideLinks(e),
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
	m.thinkHazards(r)
}

// apply does what the mob decided this tick, in the region's runtime ID order: its queued
// effects, the void check, the shared hazards (fire, lava, contact blocks, landing) and the
// move of its entity.
func (m *Mob) apply(e *world.Entity) {
	if e.Region() == nil || m.gone {
		return
	}
	m.m.Apply()
	m.syncAttrs()
	if m.m.Seats() != nil {
		settleRiders(m)
	}
	if !m.m.Dead() && len(*m.m.Effects()) > 0 {
		m.tickEffects()
	}
	if m.m.Dead() && m.m.LeashHolder() != behavior.NoID {
		m.dropLeashOnDeath(e.Region())
	}
	if m.m.Dead() {
		m.dropInventoryOnDeath(e.Region())
	}
	if m.ended {
		// A despawned mob (a creeper that blew up) leaves no experience, as BDS's
		// Actor::despawn.
		if r := e.Region(); r != nil && m.m.Dead() {
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
	if m.stand != nil {
		m.tickArmorStand(e.Region())
	}
	m.tickTrades(e.Region())
	b := m.m.Body()
	pitch, yaw, head, _ := m.m.Rotation()
	if !e.Apply(world.Movement{Pos: mglVec(b.Pos), Rot: world.Rotation{Pitch: pitch, Yaw: yaw, HeadYaw: head}, OnGround: b.OnGround, Tick: e.Tick()}) {
		m.m.SetPos(vec(e.Position()))
		b.Vel = behavior.Vec3{}
	}
	if m.m.TakeTeleport() {
		m.sendTeleport(e)
	}
}

// sendTeleport tells the mob's viewers it teleported, as BDS's Actor::teleportTo does with a
// MoveActorAbsolute marked as a teleport: the client snaps it there with its rotation, body
// included, instead of walking it over. The delta sync starts again from there. It is sent
// only when the place or rotation changed, so a mob held in place (a sleeping villager is
// teleported every tick) costs nothing.
func (m *Mob) sendTeleport(e *world.Entity) {
	pos, rot := e.Position(), e.Rotation()
	if pos == m.moves.pos && rotationByte(rot.Pitch) == rotationByte(m.moves.rot.Pitch) &&
		rotationByte(rot.Yaw) == rotationByte(m.moves.rot.Yaw) && rotationByte(rot.HeadYaw) == rotationByte(m.moves.rot.HeadYaw) {
		return
	}
	m.moves.pos, m.moves.rot, m.moves.ground = pos, rot, e.OnGround()
	flags := byte(packet.MoveFlagTeleport)
	if e.OnGround() {
		flags |= packet.MoveFlagOnGround
	}
	pk := &packet.MoveActorAbsolute{EntityRuntimeID: e.RuntimeID(), Flags: flags, Position: pos, Rotation: mgl32.Vec3{rot.Pitch, rot.Yaw, rot.HeadYaw}}
	e.EachSeer(func(p *world.Entity) { p.SendTo(pk) })
}

func (m *Mob) remove(e *world.Entity) {
	m.closeTrade()
	m.m.Unlink()
	letOffAll(m)
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
	if m.stand != nil {
		return m.standHurt(by, cause)
	}
	id := behavior.NoID
	if by != nil && by.entity != nil {
		id = behavior.ID(by.entity.UniqueID())
		m.lastPlayer = by
	}
	dealt := m.hurtBy(amount, id, cause)
	if dealt <= 0 {
		return 0
	}
	if by != nil {
		by.hurtMob(m.m.ID())
	}
	if by != nil && by.entity != nil {
		away := m.entity.Position().Sub(by.feet())
		v := by.knockbackStep(mglVec(m.m.Body().Vel), away[0], away[2], by.Tuning(TuneKnockbackPush), by.Tuning(TuneKnockbackLift))
		m.m.Knock(vec(v))
		m.entity.Send(&packet.SetActorMotion{EntityRuntimeID: m.entity.RuntimeID(), Velocity: v, Tick: m.entity.Tick()})
	}
	return dealt
}

// hurtBy hurts the mob by the entity with ID by (behavior.NoID for none) and shows the
// hurt, or the death and its drops. It reports the damage taken, absorption's share too.
func (m *Mob) hurtBy(amount float32, by behavior.ID, cause DamageCause) float32 {
	return m.hurtWith(amount, by, cause, false)
}

// hurtWith is hurtBy, with the mob's hurt time skipped when ignoreCooldown is set.
func (m *Mob) hurtWith(amount float32, by behavior.ID, cause DamageCause, ignoreCooldown bool) float32 {
	r := m.entity.Region()
	if r == nil || m.gone {
		return 0
	}
	tick := uint64(max(r.CurrentTick(), 0)) //nolint:gosec // clamped
	var dealt float32
	m.absorbed = 0
	if ignoreCooldown {
		dealt = m.m.HurtIgnoringCooldown(amount, by, int(cause), tick)
	} else {
		dealt = m.m.Hurt(amount, by, int(cause), tick)
	}
	// A hurt that absorption took whole still landed.
	dealt += m.absorbed
	if dealt <= 0 {
		return 0
	}
	// Mob::_hurt (0xBFDC98F in BDS 1.26.60) takes Absorption away once its health is used up.
	if m.absorption <= 0 {
		m.removeEffect(EffectAbsorption)
	}
	m.entity.Send(actorEvent(m.entity.RuntimeID(), packet.ActorEventHurt))
	m.effectsHurt(cause)
	if m.m.Dead() && !m.died {
		m.died = true
		m.effectsDied()
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

// Attack is the mob's melee hit: on a player its attack damage through the TakeHit system,
// then a push away from the mob; on another mob, attackMob. BDS's difficulty scaling of
// mob damage is not built yet.
func (m *Mob) Attack(_ *mob.Mob, target behavior.ID) (hit, killed bool) {
	// Mob::doHurtTarget (0xBFEE420, 1.26.60) swings and broadcasts ActorEvent 4 before it
	// looks at the target.
	m.swingEvent()
	r := m.entity.Region()
	if r == nil {
		return false, false
	}
	if o, ok := m.s.entityByID(r, target).(*Mob); ok {
		return m.attackMob(o)
	}
	p, ok := m.s.entityByID(r, target).(*Player)
	if !ok || p.entity == nil || p.dead() {
		return false, false
	}
	h := NewHit(p, m.meleeDamage(), DamageSource{Cause: DamageEntityAttack})
	if !p.s.game.sys.TakeHit(h) {
		return false, false
	}
	p.hurtByMob(m.m.ID())
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

// attackMob is the mob's melee hit on another mob (a zombie on a villager): its attack
// damage as an entity attack from the mob, with the server's knockback tuning, as a
// player's hit on a mob pushes it. Mob difficulty scaling is not built.
func (m *Mob) attackMob(o *Mob) (hit, killed bool) {
	r := m.entity.Region()
	if o == m || o.entity == nil || o.Dead() || o.stand != nil || r == nil {
		return false, false
	}
	dealt := o.hurtBy(m.meleeDamage(), behavior.ID(m.entity.UniqueID()), DamageEntityAttack)
	if dealt <= 0 {
		return false, false
	}
	if !o.Dead() {
		s := m.s
		away := o.entity.Position().Sub(m.entity.Position())
		v := knockbackOf(mglVec(o.m.Body().Vel), away[0], away[2], s.Tuning(TuneKnockbackPush), s.Tuning(TuneKnockbackLift),
			s.Tuning(TuneKnockbackRetain), s.Tuning(TuneKnockbackLiftScale), s.Tuning(TuneKnockbackCap))
		o.m.Knock(vec(v))
		o.entity.Send(&packet.SetActorMotion{EntityRuntimeID: o.entity.RuntimeID(), Velocity: v, Tick: o.entity.Tick()})
	}
	return true, o.Dead()
}

// Ignite is a melee hit's can_spread_on_fire: the target, a player or a mob, burns for at
// least seconds (Actor::setOnFire).
func (m *Mob) Ignite(_ *mob.Mob, target behavior.ID, seconds int32) {
	r := m.entity.Region()
	if r == nil {
		return
	}
	switch e := m.s.entityByID(r, target).(type) {
	case *Mob:
		e.SetOnFire(seconds)
	case *Player:
		e.SetOnFire(seconds)
	}
}

// Swing is the attack goals' swing. BDS's melee goal swings and sends ActorEvent 4 (data
// 0) only while the mob holds an item (MeleeAttackBaseGoal::_attemptAttackTarget,
// 0x5431880 in 1.26.60); the hit itself sends one more (swingEvent).
func (m *Mob) Swing(*mob.Mob) {
	if !m.m.Equipment(mob.EquipMainhand).Empty() {
		m.swingEvent()
	}
}

// swingEvent shows the mob's arm swing: ActorEvent 4 with data 0, which BDS broadcasts from
// Mob::swing's callers. Mobs ignore the player's Animate swing.
func (m *Mob) swingEvent() {
	if m.entity == nil || m.entity.Region() == nil {
		return
	}
	m.entity.Send(&packet.ActorEvent{EntityRuntimeID: m.entity.RuntimeID(), EventType: packet.ActorEventStartAttacking})
}

// Sound plays a level sound event the mob made, for those who see it.
func (m *Mob) Sound(_ *mob.Mob, n int, at behavior.Vec3) {
	name := mob.SoundName(n)
	if name == "" || m.entity == nil || m.entity.Region() == nil {
		return
	}
	m.entity.Send(soundEvent{name: name, pos: mglVec(at), extra: -1, entity: m.m.Type(), actor: m.entity.UniqueID()}.packet())
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
	if d.ActionType == protocol.UseItemOnEntityActionInteract && o.stand != nil {
		p.interactArmorStand(o, d)
		return true
	}
	if d.ActionType == protocol.UseItemOnEntityActionInteract {
		p.interactMob(o, d)
		return true
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
	p.attackMob(o)
	return true
}

// attackMob is the player's melee attack on a mob, BDS's player attack: the abilities rule,
// the hit's parts, the mob's hurt, then the mace's smash and what the hit costs the attacker
// and its item, and the sound.
func (p *Player) attackMob(o *Mob) {
	if !p.abilities.Flag(AbilityAttackMobs) {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	pos := p.entity.Position()
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
			p.maceSmash(r, o.entity)
			h.attackerAfter()
		}
	}
	playSound(r, p.blockPos(), soundEvent{name: sound, pos: pos, extra: -1, entity: "minecraft:player"}, p.viewer)
	p.flush()
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
	case *LeashKnot:
		if !o.gone {
			return knotActor{id: behavior.ID(e.UniqueID()), pos: vec(e.Position())}
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
			if i, ok := e.Owner().(*ItemActor); ok && !i.taken {
				return itemEntity{i}
			}
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
	// Mobs whose box misses b are left out by the world from the box their snapshot set.
	sc.list = r.AppendActorsTouching(sc.list, mglVec(b.Min), mglVec(b.Max), 2)
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
	sc.list = r.AppendPlayers(sc.list)
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

// TNTExplodes is the tntexplodes game rule.
func (s *Host) TNTExplodes() bool { return s.level.TNTExplodes }

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

// Interacts is Player::canInteractWithOtherEntitiesInGame: not a spectator,
// dead or not.
func (a playerActor) Interacts() bool { return a.p.gameMode != GameModeSpectator }

// HasFamily is the is_family test on the player's type_family (player,
// mob), the same families mob filters give players.
func (a playerActor) HasFamily(f string) bool { return f == "player" || f == "mob" }
func (a playerActor) IsMob() bool             { return true }
func (a playerActor) IsPlayer() bool          { return true }

// Flag answers no actor flag yet; goals read only baby, sitting and the like from players.
func (a playerActor) Flag(int) bool         { return false }
func (a playerActor) Type() string          { return registry.Player }
func (a playerActor) GameMode() int         { return int(a.p.gameMode) }
func (a playerActor) Riders() []behavior.ID { return nil }
func (a playerActor) Owner() behavior.ID    { return behavior.NoID }
func (a playerActor) Team() int             { return 0 }
func (a playerActor) Dimension() int        { return int(a.p.s.dimensionID(a.p.dim)) }
func (a playerActor) HeadRotation() (pitch, yaw float32) {
	r := a.p.entity.Rotation()
	return r.Pitch, r.HeadYaw
}
func (a playerActor) BodyYaw() float32 { return a.p.entity.Rotation().Yaw }

// Yaw is the player's ActorRotationComponent yaw, which controlled_by_player turns a
// ridden mob towards.
func (a playerActor) Yaw() float32 { return a.p.entity.Rotation().Yaw }

// Vehicle is the mob the player rides, NoID for none or another vehicle.
func (a playerActor) Vehicle() behavior.ID {
	if o, ok := a.p.vehicle.(*Mob); ok {
		return o.m.ID()
	}
	return behavior.NoID
}
func (a playerActor) Invulnerable() bool                { return a.p.invulnerable() }
func (a playerActor) HasHealth() bool                   { return true }
func (a playerActor) Targetable() bool                  { return true }
func (a playerActor) Monster() bool                     { return false }
func (a playerActor) ArmourCoverage() float32           { return 0 }
func (a playerActor) WearsHeadOf(string) bool           { return false }
func (a playerActor) Target() behavior.ID               { return behavior.NoID }
func (a playerActor) HijacksMount() bool                { return false }
func (a playerActor) LastHurtByMob() behavior.ID        { return a.p.hurts.by }
func (a playerActor) LastHurtMob() (behavior.ID, int32) { return a.p.hurts.mob, a.p.hurts.stamp }
func (a playerActor) BlocksSpace() bool                 { return false }
func (a playerActor) AttachPos(int) behavior.Vec3       { return a.Eyes() }

// PosDelta implements mob.MovingActor: the player's last movement, as BDS sets posDelta from
// the client's input.
func (a playerActor) PosDelta() behavior.Vec3 { return vec(a.p.move.velocity) }

// PrevPos implements mob.MovingActor: where the player's feet were before its last movement.
func (a playerActor) PrevPos() behavior.Vec3 { return vec(a.p.feet().Sub(a.p.move.moved)) }

var (
	_ mob.Env         = (*Host)(nil)
	_ mob.Hooks       = (*Mob)(nil)
	_ mob.MovingActor = playerActor{}
	_ behavior.Actor  = playerActor{}
)

// dataWireValue is an actor data value as the client reads its key: the colours are bytes
// (BDS's Color and Color2), which a client given an int drops, so a tamed wolf showed no
// collar.
func dataWireValue(key uint32, v any) any {
	if n, ok := v.(int32); ok && (key == protocol.EntityDataKeyColorIndex || key == protocol.EntityDataKeyColorTwoIndex) {
		return uint8(n) //nolint:gosec // colours are 0 to 15
	}
	return v
}
