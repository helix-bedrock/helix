package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/state"
)

// The state components' actions that reach past the mob, as BDS 1.26.60 does them:
// minecraft:teleport's move (TeleportComponent::teleport 0x7170A80), minecraft:spawn_entity
// (SpawnActorSystem::tick 0x76CBF10), minecraft:trail's blocks, and the game events the
// components post. Each is checked while the mob thinks, from what the mob may read, and
// done at Apply.

// TeleportHooks is what an engine that shows mob teleports implements on its Hooks: the mob
// moved from from to its new place by minecraft:teleport. BDS sends the teleport trail
// (LevelEvent 2023 with the start, end, the box's size, DirScale 0.2 and Count 128), plays
// LevelEvent 1018 where it left, posts the teleport game event there, and plays 1018 again
// where it arrived unless the mob is silent. It runs in the mob's Apply, after the move.
type TeleportHooks interface {
	Teleported(m *Mob, from behavior.Vec3)
}

// SpawnHooks is what an engine that runs minecraft:spawn_entity implements on its Hooks. Both
// run in the spawning mob's Apply.
type SpawnHooks interface {
	// SpawnItem spawns one of the item at the mob as Spawner::spawnItem, with a pick-up delay
	// of 10 ticks; false when the item does not exist.
	SpawnItem(m *Mob, item string) bool
	// SpawnEntity spawns a mob of the type from m by the spawn method (born, spawned,
	// summoned or transformed), starting with the event (none when empty), as BDS's
	// ActorSpawnHelper; nil when it could not.
	SpawnEntity(m *Mob, typ, event, method string) *Mob
}

// GameEventHooks is what an engine that runs game events (vibrations) implements on its
// Hooks: the mob posted the game event with the BDS name at p. It runs in the mob's Apply.
type GameEventHooks interface {
	GameEvent(m *Mob, name string, p behavior.Vec3)
}

// ArmorEnchantHooks is what an engine that keeps the enchantments of a mob's items
// implements on its Hooks. It runs while the mob thinks and reads only the mob's own items.
type ArmorEnchantHooks interface {
	// Respiration is the highest Respiration level of the armour the mob wears.
	Respiration(m *Mob) int32
}

// The spawn methods BDS's ActorSpawnHelper knows (0xB33BD60), and what born does to a
// rideable newborn.
const (
	spawnBorn     = "born"
	spawnSpawned  = "spawned"
	spawnSummoned = "summoned"
	spawnChanged  = "transformed"
)

// The game events the mob runtime posts itself.
const (
	gameEventEat         = "eat"
	gameEventEntityPlace = "entity_place"
	gameEventRoar        = "entity_roar"
)

// Teleport is TeleportComponent::teleport's search: from the block at p it walks down to the
// first block that holds things on its top face (above the world's bottom), then the mob's
// box moved there must hit no block and hold no liquid. The move, the trail, the sounds and
// the game event happen at Apply; the answer is given now.
func (a stateActions) Teleport(p behavior.Vec3) bool {
	to, ok := a.m.teleportSpot(p)
	if ok {
		a.m.do(op{kind: opTeleport, at: to})
	}
	return ok
}

// teleportSpot is where a teleport toward p would stand, false when there is no room.
func (m *Mob) teleportSpot(p behavior.Vec3) (behavior.Vec3, bool) {
	if m.v.r == nil {
		return behavior.Vec3{}, false
	}
	b := blockOf(p)
	w := goalWorld{&m.v}
	if !m.v.loaded(b) {
		return behavior.Vec3{}, false
	}
	minY := w.MinHeight()
	for {
		if b.Y <= minY {
			return behavior.Vec3{}, false
		}
		if m.rt.Blocks.TopSupport(m.v.block(behavior.Pos{X: b.X, Y: b.Y - 1, Z: b.Z}, 0)) {
			break
		}
		b.Y--
	}
	to := behavior.Vec3{X: p.X, Y: float32(b.Y), Z: p.Z}
	at := m.body.Pos
	shift := behavior.Vec3{X: to.X - at.X, Y: to.Y - at.Y, Z: to.Z - at.Z}
	box := behavior.Box{
		Min: behavior.Vec3{X: m.body.Box.Min.X + shift.X, Y: m.body.Box.Min.Y + shift.Y, Z: m.body.Box.Min.Z + shift.Z},
		Max: behavior.Vec3{X: m.body.Box.Max.X + shift.X, Y: m.body.Box.Max.Y + shift.Y, Z: m.body.Box.Max.Z + shift.Z},
	}
	if w.BoxCollides(box) || w.BoxHasLiquid(box) {
		return behavior.Vec3{}, false
	}
	return to, true
}

// teleportTo moves the mob as Actor::teleportTo with its stop riding flag, then shows it.
// Runs: Apply.
func (m *Mob) teleportTo(to behavior.Vec3) {
	from := m.body.Pos
	m.stopRiding(false)
	m.Teleport(to)
	if h, ok := m.hooks.(TeleportHooks); ok {
		h.Teleported(m, from)
	}
}

// PlaceBlock is minecraft:trail's placing: the block goes only into air and only where it
// may be placed, set and its neighbours told (update flags 3) at Apply.
func (a stateActions) PlaceBlock(p behavior.Pos, block string) bool {
	m := a.m
	if m.v.r == nil || m.v.block(p, 0) != m.rt.Blocks.reg.Air() || !m.MayPlace(block, p) {
		return false
	}
	m.do(op{kind: opPlaceBlock, pos: p, placed: behavior.PlacedBlock{Name: ns(block)}, id: m.id})
	return true
}

// Spawn queues a minecraft:spawn_entity entry whose timer ran out and whose filters passed.
func (a stateActions) Spawn(e state.SpawnEntry) {
	a.m.do(op{kind: opSpawnEntry, spawn: &e})
}

// spawnEntry is the rest of SpawnActorSystem::tick for one entry: the spawn sound at the
// mob's body, then num_to_spawn items or entities. An item that spawns runs
// spawn_item_event on the mob; an entity is spawned by the entry's method with its event,
// posts entity_place at the mob, takes the mob's leash when should_leash is set, and a
// rideable one born from the mob carries it. The first entity that cannot spawn ends the
// entry. Runs: Apply.
func (m *Mob) spawnEntry(e *state.SpawnEntry) {
	if n := (resolver{m}).Sound(e.Sound); n != behavior.NoSound && m.hooks != nil {
		m.hooks.Sound(m, n, m.bodyCentre())
	}
	h, ok := m.hooks.(SpawnHooks)
	if !ok {
		return
	}
	for range e.Count {
		if e.Entity == "" {
			if e.Item != "" && h.SpawnItem(m, ns(e.Item)) && e.ItemEvent != nil {
				m.Trigger(e.ItemEvent, m.tester(nil), nil)
			}
			continue
		}
		c := h.SpawnEntity(m, ns(e.Entity), e.Event, e.Method)
		if c == nil {
			return
		}
		m.postGameEvent(gameEventEntityPlace, m.body.Pos)
		if e.ShouldLeash {
			m.leashSpawned(c)
		}
		if e.Method == spawnBorn {
			m.rideNewborn(c)
		}
	}
}

// leashSpawned ties a spawned mob's leash to the mob, as should_leash does; one without
// minecraft:leashable is refused as BDS logs it.
func (m *Mob) leashSpawned(c *Mob) {
	if !c.Leashable() {
		m.rt.Log.Debug("spawn_entity: should_leash on a mob without minecraft:leashable", "type", c.typ)
		return
	}
	c.SetLeashHolder(m.id)
}

// rideNewborn is SpawnActorSystem's born rule: a rideable newborn that can take the mob as a
// passenger makes it leave its vehicle, and the mob gets on (a drowned on the zombie
// nautilus it spawns).
func (m *Mob) rideNewborn(c *Mob) {
	if c.states == nil || c.states.Rideable == nil || c.stateEnv.Self == nil {
		return
	}
	if c.states.Rideable.CanAddPassenger(&c.stateEnv, m) {
		m.stopRiding(true)
	}
	m.startRiding(c)
}

// bodyCentre is attach point 1, the body: the middle of the mob's box.
func (m *Mob) bodyCentre() behavior.Vec3 {
	b := m.body.Box
	return behavior.Vec3{X: (b.Min.X + b.Max.X) / 2, Y: (b.Min.Y + b.Max.Y) / 2, Z: (b.Min.Z + b.Max.Z) / 2}
}

// postGameEvent posts a game event by its BDS name at p, at Apply.
func (m *Mob) postGameEvent(name string, p behavior.Vec3) {
	m.do(op{kind: opGameEvent, name: name, at: p})
}

// EatGameEvent posts the eat game event at p (Actor::feed's, at attach point 8).
func (m *Mob) EatGameEvent(p behavior.Vec3) { m.postGameEvent(gameEventEat, p) }

// RoarGameEvent implements behavior.Roarer: entity_roar at p.
func (m *Mob) RoarGameEvent(p behavior.Vec3) { m.postGameEvent(gameEventRoar, p) }

// runActionOp does the queued actions of this file. Runs: Apply.
func (m *Mob) runActionOp(o *op) {
	switch o.kind {
	case opTeleport:
		if !m.Dead() {
			m.teleportTo(o.at)
		}
	case opSpawnEntry:
		if o.spawn != nil {
			m.spawnEntry(o.spawn)
		}
	case opGameEvent:
		if h, ok := m.hooks.(GameEventHooks); ok {
			h.GameEvent(m, o.name, o.at)
		}
	case opPlaceBlock:
		// Checked again: another mob's Apply may have filled the block this tick.
		if m.v.r != nil && m.v.block(o.pos, 0) == m.rt.Blocks.reg.Air() {
			m.rt.Env.SetBlock(m.v.r, o.pos, o.placed, o.id)
		}
	}
}

// spawnInit is the init method and event a spawn_entity entity starts with: its event when
// the entry has one, else the method's own.
func spawnInit(method, event string) (definition.InitMethod, string) {
	if event != "" {
		return definition.InitEvent, event
	}
	if method == spawnBorn {
		return definition.InitBorn, ""
	}
	if method == spawnChanged {
		return definition.InitTransformed, ""
	}
	return definition.InitSpawned, ""
}

// SpawnInit is spawnInit for the engine's SpawnEntity, false for a method BDS's spawn
// helper does not know (nothing spawns).
func SpawnInit(method, event string) (definition.InitMethod, string, bool) {
	switch method {
	case spawnBorn, spawnSpawned, spawnSummoned, spawnChanged:
		im, ev := spawnInit(method, event)
		return im, ev, true
	}
	return 0, "", false
}

// --- small state hooks ---

// CanSit implements behavior.Mob: the mob has minecraft:sittable.
func (m *Mob) CanSit() bool { return m.states != nil && m.states.Sittable != nil }

// SetHijackable implements behavior.Mob: CanBeHijackedByPassengerComponent on or off.
func (m *Mob) SetHijackable(on bool) { m.hijackable = on }

// SetPersistSit implements behavior.Mob: PersistSitComponent on or off.
func (m *Mob) SetPersistSit(on bool) { m.persistSit = on }

// ResetFlock implements behavior.Mob. minecraft:flocking's state is not ported in the
// entity library (only its settings), so there is nothing to reset.
func (m *Mob) ResetFlock() {}
