// Package mob runs mobs from their entity definitions with the entity library: the
// definition's components become the mob's goals, navigation, move control and physics,
// ticked in BDS's order. Nothing here knows any mob by name: a zombie and a cow differ only
// in their definition, as in BDS.
//
// Sensors and event components run (sensors.go), and the mob state components (state.go);
// loot and saving are not hooked up yet. The player package's Mob actor owns the entity, packets and damage.
package mob

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/component"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/goal"
	"github.com/helix-bedrock/entity/move"
	"github.com/helix-bedrock/entity/nav"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/physics"
	"github.com/helix-bedrock/entity/sensor"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/entity/trace"
	"github.com/helix-bedrock/helix/effect"
	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// BDS's actor flag numbers the runtime sets itself.
const (
	FlagNoAI       = 16
	FlagCanClimb   = 19
	FlagCanWalk    = 22
	FlagBreathing  = 35
	FlagHasGravity = 49
	FlagCollision  = 48
)

// BDS's defaults for a mob without the components that set them.
const (
	defaultMaxAutoStep = 0.5625
	defaultAirSpeed    = 0.02
	// defaultLiquidSpeed is the underwater and lava movement every BDS mob
	// registers (Mob attribute setup 0xBFEF1F0); definitions may override it.
	defaultLiquidSpeed = 0.02
	defaultFollowRange = 16
	defaultHealth      = 10
	// hurtTicks is how long a mob only takes damage above its last hurt.
	hurtTicks = 10
	// deathTicks is how long a dead mob stays before it is removed.
	deathTicks = 20
	// lookYaw is the body control's turn limit when the head looks away.
	bodyMaxTurn = 75
)

// Runtime is what every mob of a server shares: the block tables, the engine's answers and
// the goal registry. Runs: read from any region once built.
type Runtime struct {
	Blocks *Blocks
	Env    Env
	// Goals maps goal components to factories; vanilla's by default.
	Goals *component.Registry
	// Villages is the village system dwellers belong to, nil for none (dweller.go).
	Villages Villages
	// Walk is the blocks the walk target scores look for (behavior.WalkTarget);
	// BDS's by default. Set it before mobs tick.
	Walk behavior.WalkBlocks
	// ItemRules answers the item components' item questions (items.go); nil gives mobs no
	// item components. Set it before mobs tick.
	ItemRules behavior.ItemRules
	Log       *slog.Logger
	// parser reads definitions; parsed definitions are cached by type.
	parser *definition.Parser
	defs   map[string]*definition.Definition
	// sounds are the level sound events' numbers by name (soundevents_gen.go).
	sounds map[string]int
}

// NewRuntime returns a runtime with vanilla's goals.
func NewRuntime(b *Blocks, env Env, log *slog.Logger) *Runtime {
	rt := &Runtime{Blocks: b, Env: env, Goals: component.Vanilla(), Walk: behavior.DefaultWalkBlocks(), Log: log, parser: definition.NewParser(), defs: map[string]*definition.Definition{}, sounds: map[string]int{}}
	for n, name := range soundEvents {
		if name != "" {
			rt.sounds[name] = n
		}
	}
	return rt
}

// SoundName is the name of a level sound event number, "" for none.
func SoundName(n int) string {
	if n < 0 || n >= len(soundEvents) || n == behavior.NoSound {
		return ""
	}
	return soundEvents[n]
}

// SoundKnown reports whether name is a level sound event. BDS reads a definition's sound
// names into these numbers, so a name that is not one plays nothing.
func SoundKnown(name string) bool {
	for _, s := range soundEvents {
		if s == name && s != "" {
			return true
		}
	}
	return false
}

// Parse reads and caches the definition of an entity type. Call it at boot for every type,
// so regions only read the cache.
func (rt *Runtime) Parse(typ string, raw json.RawMessage) error {
	def, warnings, err := rt.parser.Parse(raw)
	if err != nil {
		return fmt.Errorf("mob: parse %s: %w", typ, err)
	}
	for _, w := range warnings {
		rt.Log.Debug("entity definition", "type", typ, "warning", w)
	}
	rt.defs[typ] = def
	return nil
}

// Has reports a parsed definition for typ.
func (rt *Runtime) Has(typ string) bool { _, ok := rt.defs[typ]; return ok }

// ArmorHooks is what an engine that knows the mob's armour implements on its Hooks: what
// the armour leaves of damage amount of cause, as BDS's Mob hurt (armour, protection
// enchantments, Resistance, then absorption), after the hurt time has taken its part.
type ArmorHooks interface {
	AfterArmor(m *Mob, amount float32, cause int) float32
}

// Hooks are what the engine does for one mob: its packets and its effects on others. They
// run on the region's goroutine, never while mobs think.
type Hooks interface {
	// Attack is a melee hit on the entity with the ID; it reports a hit and whether the
	// entity died. It runs in the mob's Apply.
	Attack(m *Mob, target behavior.ID) (hit, killed bool)
	// Swing swings the mob's arm for those who see it.
	Swing(m *Mob)
	// Sound plays a level sound event at p.
	Sound(m *Mob, sound int, p behavior.Vec3)
	// FlagsChanged tells those who see the mob that its actor flags or air changed.
	FlagsChanged(m *Mob)
	// Shoot fires the projectile def (aux value aux) at the target as minecraft:shooter
	// does, at power when above 0, playing sound (a level sound event, NoSound for none).
	Shoot(m *Mob, target behavior.ID, def string, aux int32, power float32, sound int)
	// ActorEvent sends an actor event about the mob.
	ActorEvent(m *Mob, id int)
	// Hurt hurts the mob through the engine's hurt (its packets included) with no attacker.
	Hurt(m *Mob, amount float32, cause int)
	// Born makes the baby of a mating beside the parent m (BDS's
	// ActorFactory::createBornActor) and returns it as the mob runtime sees it, nil when
	// none was made. It runs in m's Apply.
	Born(m *Mob, b state.Birth) behavior.Actor
	// Experience drops experience orbs worth amount at the mob.
	Experience(m *Mob, amount int32)
	// Ignite sets the entity with the ID on fire for at least seconds: a melee hit's
	// can_spread_on_fire. It runs in the mob's Apply.
	Ignite(m *Mob, target behavior.ID, seconds int32)
	// LeashDropped is the mob's leash coming off (Actor::dropLeash); dropLead drops a lead
	// item at the mob.
	LeashDropped(m *Mob, dropLead bool)
}

// Mob is one mob: its definition state, goals, navigation and body. It is used only from its
// entity's tick: Tick in the think phase, the rest on the region's goroutine.
type Mob struct {
	rt    *Runtime
	hooks Hooks
	typ   string
	// walk is the C++ class's walk target kind (behavior.WalkKindOf).
	walk behavior.WalkKind
	id   behavior.ID
	rid  uint64

	inst *definition.Instance
	// geneEvents is set until a new (not loaded) mob's genetics have run their birth events.
	geneEvents bool
	// savedHealth is a restored health the first rebuild puts in place of the default, 0 for none.
	savedHealth float32
	families    []string
	sel         goal.Selector
	nv          *nav.Navigation
	navigate    navigator
	finder      *path.Finder
	movement    component.Movement
	hasMove     bool
	ctl         move.Control
	// trace records what the mob does each tick while someone watches it (trace.go);
	// tracePathAt is the path length and node last recorded.
	trace       *trace.Log
	tracePathAt [2]int
	// traceGround and traceFeet are the blocks last recorded as stood on and stood in.
	traceGround, traceFeet path.Pos
	look                   move.Look
	jump                   move.Jump
	// jumpCtl is BDS's JumpControlComponent (jump.static or jump.dynamic).
	jumpCtl bool
	// slimer is movement.jump's slime move control state.
	slimer move.Slimer
	// swimDir is a squid's swim direction (Squid +1172) and squidPitch its body pitch
	// (Squid +1156); see squid.go.
	swimDir    behavior.Vec3
	squidPitch float32
	lookSet    bool
	body       physics.Body
	// ride is the mob's riding (ride.go).
	ride riding
	// boost is minecraft:boostable's state.
	boost boosting
	flags path.ActorFlags
	// panicking is BDS's IsPanickingFlagComponent: set while the panic
	// goal runs, read by the is_panicking filter, never sent.
	panicking bool
	// doors is minecraft:annotation.open_door's state, nil without the component.
	doors *nav.OpenDoors
	// teleported is set by Teleport until the engine sends the move (TakeTeleport).
	teleported bool

	pitch, yaw, headYaw, bodyYaw float32
	prevPitch, prevYaw           float32

	health, maxHealth float32
	// baseMaxHealth is the health attribute's maximum before healthBoost, Health Boost's
	// modifier on it.
	baseMaxHealth, healthBoost          float32
	speed, underwaterSpeed, followRange float32
	// jumpStrength is minecraft:horse.jump_strength, which the client of a rider steering
	// the horse jumps by; hasJumpStrength is false without the component.
	jumpStrength, jumpStrengthMax float32
	hasJumpStrength               bool
	// speedBase is the movement attribute before its modifiers, speedMods those (sprint.go).
	speedBase               float32
	speedMods               []attrModifier
	lavaSpeed               float32
	attackDamage, attackMin float32
	hasAttack               bool
	// attackEffect is minecraft:attack's effect_name, effect_duration (in ticks) and
	// effect_amplifier, given by the melee goals' hit.
	attackEffect attackEffect

	target, lastHurtBy, lastHurtMob behavior.ID
	// lastHurtByPlayer is BDS's mLastHurtByPlayerId: the last player that
	// hurt the mob.
	lastHurtByPlayer behavior.ID
	lastHurtTick     uint64
	lastHurtStamp    int32
	lastHurtAmount   float32
	hurtTime         int32
	// playerHitTime counts down from playerHitTicks after a player hurt the mob: Molang's
	// query.last_hit_by_player.
	playerHitTime int32
	// equip is what the mob holds and wears (equipment.go); charged and chargeAmount its
	// crossbow's state.
	equip [EquipSlots]behavior.Item
	// items are the item components (items.go): hasItems is set once they are built, inv is
	// minecraft:inventory's slots, equipState minecraft:equip_item's waiting stack, and
	// canPickUp the spawn roll pickup_based_on_chance reads.
	items    behavior.Items
	hasItems bool
	inv      inventory
	// love is the make_love state (love.go).
	love                    love
	equipState              behavior.EquipState
	canPickUp, canPickUpSet bool
	charged                 bool
	chargeAmount            int8
	// pushNear is scratch space for the actors pushActors finds.
	pushNear []behavior.Actor
	// standBoxes is scratch space for StandingOn.
	standBoxes []path.Box
	lastCause  int
	deathTime  int32
	persistent bool
	// nameTag is the mob's name (BDS's setNameTag), "" for none.
	nameTag string
	// suspect is the suspect_tracking location; sonicCooldown the warden's sonic boom
	// cooldown, set when hasSonicCooldown is (warden.go).
	suspect          suspicion
	sonicCooldown    uint64
	hasSonicCooldown bool
	// trusted are the IDs the trust component holds (trust.go).
	trusted []behavior.ID
	// chasing, targetLeader, caravanHead and caravanTail are play's, follow_target_captain's
	// and follow_caravan's state (pack.go).
	chasing                                bool
	targetLeader, caravanHead, caravanTail behavior.ID
	// hurtThisTick and wasHurt are Actor +784 and +785 (wasHurt is +785, copied from +784 by
	// Actor::baseTick, 1.26.60); pushedBy is PushedByComponent; rollCount is the panda's
	// RollCounterComponent (flyer.go, golem.go, panda.go).
	hurtThisTick, wasHurt bool
	pushedBy              behavior.ID
	hasPushedBy           bool
	rollCount             int32
	// customBox is set while a goal's Actor::setAABB box stands in for the mob's size;
	// hurtAt is the tick of the last hurt that landed (Actor::getLastHurtTimestamp);
	// fallImmune is CurrentlyImmuneToFallDamageComponent; ramHorn is the ram's horn dropper
	// flag; mingle is MingleComponent, set when hasMingle is (jumper.go, ram.go, mingle.go).
	customBox, fallImmune, ramHorn bool
	hurtAt                         uint64
	mingle                         behavior.MingleState
	hasMingle                      bool
	// effects are the mob's effects (effects.go).
	effects effect.List
	// leashHolder is the entity holding the mob's leash, NoID for none (breed.go).
	leashHolder behavior.ID
	// owner is the entity that tamed the mob (Actor::setOwner), NoID for none; madTime
	// counts the ticks of a refused horse's rear-up (tame.go).
	owner   behavior.ID
	madTime int32
	// strength and strengthMax are minecraft:strength's (horse.go).
	strength, strengthMax int32
	// saved is the age and breeding state a save holds, put back once the components are
	// built; births is scratch space for a mating's babies.
	saved  *SavedState
	births []state.Birth
	// states are the definition's state components (state.go), stateEnv what they run with,
	// stateComps the components they were read from. despawned is set when the mob despawned.
	states     *state.Set
	stateEnv   state.Env
	stateComps definition.Group
	despawned  bool
	// fireTicks is how long the mob still burns (the engine counts it down and burns it, as
	// it does a player); air and maxAir its air supply.
	fireTicks   int32
	air, maxAir int16
	// sensors are the definition's sensors and event components (sensors.go), sensorEnv what
	// they run with, sensorComps the components they were read from.
	sensors     *sensor.Set
	sensorEnv   sensor.Env
	sensorComps definition.Group
	swellDir    int32
	// swell is the creeper's swell, 0 to 30 (creeper.go); blast the explosion its
	// minecraft:explode queued for Apply.
	swell int32
	// transforming is set while a due transformation waits for Apply.
	transforming bool
	blast        state.Explosion
	noActionTime int32
	// hijackable is CanBeHijackedByPassengerComponent, which mount_pathing adds while it
	// runs; persistSit is PersistSitComponent, which look_at_player's sit variant adds
	// (actions.go). Neither is read by Helix yet.
	hijackable, persistSit bool
	// aggroedByPlayer is EnderMan's mAggroedByPlayer: a player's hit made it angry.
	aggroedByPlayer bool
	// data is the actor data the components wrote (data.go).
	data []DataValue
	// scale is minecraft:scale's value, the size the client draws the mob at (a rabbit 0.6,
	// babies their group's own).
	scale float32
	// dw is the DwellerComponent's state and the trade window's player (dweller.go);
	// trade the trade components' state (trade.go); pathNodes scratch for PathNodes.
	dw        dweller
	trade     tradeState
	pathNodes []behavior.Pos
	// moveSpeed is the speed the move control last chose (BDS's Mob::mSpeed), kept between
	// ticks so walking speeds up a tenth at a time.
	moveSpeed float32

	// rnd is the mob's own random, seeded from its ID, so what it draws does not depend on
	// which mobs think on which core.
	rnd *Random
	// thinking is set during Tick: ops queue instead of running, flag changes wait for Apply.
	thinking   bool
	flagsDirty bool
	ops        []op
	snap       Snapshot

	v view
}

// New makes a mob of the type at pos. method is how it came to be (spawned, born, loaded),
// which picks the event its definition runs first.
func (rt *Runtime) New(typ string, uniqueID int64, runtimeID uint64, pos behavior.Vec3, yaw float32, hooks Hooks, method definition.InitMethod) (*Mob, error) {
	return rt.NewWithEvent(typ, uniqueID, runtimeID, pos, yaw, hooks, method, "")
}

// NewWithEvent is New with the event a mob starts with when method is
// definition.InitEvent, as an identifier such as minecraft:pillager<event> asks.
func (rt *Runtime) NewWithEvent(typ string, uniqueID int64, runtimeID uint64, pos behavior.Vec3, yaw float32, hooks Hooks, method definition.InitMethod, initEvent string) (*Mob, error) {
	def, ok := rt.defs[typ]
	if !ok {
		return nil, fmt.Errorf("mob: no definition for %s", typ)
	}
	m := &Mob{
		rt: rt, hooks: hooks, typ: typ, walk: behavior.WalkKindOf(typ), id: behavior.ID(uniqueID), rid: runtimeID,
		target: behavior.NoID, lastHurtBy: behavior.NoID, lastHurtMob: behavior.NoID, lastHurtByPlayer: behavior.NoID, lastCause: -1, leashHolder: behavior.NoID, owner: behavior.NoID, dw: dweller{trading: behavior.NoID}, love: love{partner: behavior.NoID},
		targetLeader: behavior.NoID, caravanHead: behavior.NoID, caravanTail: behavior.NoID, ride: riding{vehicle: behavior.NoID},
		yaw: yaw, headYaw: yaw, bodyYaw: yaw, prevYaw: yaw,
		finder: path.NewFinder(),
		health: defaultHealth, maxHealth: defaultHealth, baseMaxHealth: defaultHealth, followRange: defaultFollowRange,
		underwaterSpeed: defaultLiquidSpeed, lavaSpeed: defaultLiquidSpeed,
	}
	m.rnd = NewRandom(uint64(uniqueID)) //nolint:gosec // the bits of the ID seed the random
	m.v = view{blocks: rt.Blocks, env: rt.Env, rnd: m.rnd, m: m}
	m.navigate.m = m
	m.body = physics.Body{Pos: pos, PrevPos: pos, Yaw: yaw, SwimSpeed: 1, MaxAutoStep: defaultMaxAutoStep, AirSpeed: defaultAirSpeed, HasPhysics: true, JumpBoost: -1, Levitation: -1}
	m.flags.Set(FlagHasGravity, true)
	m.flags.Set(FlagCollision, true)
	m.flags.Set(FlagBreathing, true)
	m.flags.Set(FlagCanClimb, true)
	m.inst = definition.New(def, m, method, initEvent)
	m.geneEvents = method != definition.InitLoaded
	m.ride.addRiders = method != definition.InitLoaded
	m.setSize(0.6, 1.8)
	m.markElder()
	m.snapshot()
	return m, nil
}

// Type is the mob's entity type.
func (m *Mob) Type() string { return m.typ }

// Families is the mob's type_family list.
func (m *Mob) Families() []string { return m.families }

// Body is the mob's movement state.
func (m *Mob) Body() *physics.Body { return &m.body }

// Flags are the mob's actor flags.
func (m *Mob) Flags() *path.ActorFlags { return &m.flags }

// Rotation is the mob's pitch, yaw, head yaw and body yaw.
func (m *Mob) Rotation() (pitch, yaw, headYaw, bodyYaw float32) {
	return m.pitch, m.yaw, m.headYaw, m.bodyYaw
}

// Scale is the size the client draws the mob at, from minecraft:scale (1 without it).
func (m *Mob) Scale() float32 {
	if m.scale <= 0 {
		return 1
	}
	return m.scale
}

// Size is the mob's collision width and height.
func (m *Mob) Size() (w, h float32) { return m.body.Width, m.body.Height }

// MaxHealth is the health attribute's maximum.
func (m *Mob) MaxHealth() float32 { return m.maxHealth }

// SetHealthBoost puts Health Boost's modifier (an addition) on the health maximum, 0 to take
// it off. As BDS's attribute instance does when its maximum drops, health above the new
// maximum is cut to it; a rising maximum leaves health as it is.
func (m *Mob) SetHealthBoost(v float32) {
	m.healthBoost = v
	m.maxHealth = m.baseMaxHealth + v
	m.health = min(m.health, m.maxHealth)
}

// HealthValue is the mob's health.
func (m *Mob) HealthValue() float32 { return m.health }

// SetHealth sets the health straight, between 0 and the maximum, with no hurt or death
// handling: for actors that run their own damage, as BDS's armour stand burns its health
// attribute down.
func (m *Mob) SetHealth(h float32) { m.health = min(max(h, 0), m.maxHealth) }

// AttackDamage is the attack component's damage.

// JumpStrength is the horse.jump_strength attribute's value and maximum; ok is false
// without it.
func (m *Mob) JumpStrength() (v, mx float32, ok bool) {
	return m.jumpStrength, m.jumpStrengthMax, m.hasJumpStrength
}

// MovementSpeed is the movement attribute.
func (m *Mob) MovementSpeed() float32 { return m.speed }

// Dead reports a mob with no health left.
func (m *Mob) Dead() bool { return m.health <= 0 }

func (m *Mob) setSize(w, h float32) {
	m.body.Width, m.body.Height = w, h
	m.body.Box = boxAt(m.body.Pos, w, h)
	m.customBox = false
}

func boxAt(p path.Vec3, w, h float32) path.Box {
	return path.Box{Min: path.Vec3{X: p.X - w/2, Y: p.Y, Z: p.Z - w/2}, Max: path.Vec3{X: p.X + w/2, Y: p.Y + h, Z: p.Z + w/2}}
}

// Tick runs the thinking half of one tick of the mob on region r, in BDS's system order: the
// definition's pending changes, the goal selector, navigation, the move control, the jump and
// look controls, the movement pipeline and the body control. It may run at the same time as
// other mobs' Tick (see think.go); call Apply after it. It reports whether the mob's death has
// ended and it should be removed.
func (m *Mob) Tick(r *world.Region) (remove bool) {
	m.v.r = r
	if !m.trace.Step() {
		return false
	}
	m.traceTick()
	m.thinking = true
	remove = m.think()
	m.thinking = false
	return remove
}

func (m *Mob) think() (remove bool) {
	if m.inst.Changed() || m.inst.Components() == nil {
		m.refresh()
	}
	if m.hurtTime > 0 {
		m.hurtTime--
	}
	m.wasHurt, m.hurtThisTick = m.hurtThisTick, false
	if m.playerHitTime > 0 {
		m.playerHitTime--
	}
	if m.Dead() {
		m.deathTime++
		return m.deathTime >= deathTicks
	}
	m.creeperTick()
	m.endermanTick()
	m.rollTick()
	if m.stateTick() {
		return true
	}
	m.boostTick()
	m.madTick()
	m.sensorTick()
	m.dwellerTick()
	m.prevPitch, m.prevYaw = m.pitch, m.yaw
	m.lookSet = false
	m.noActionTime++

	noAI := m.flags.Has(FlagNoAI)
	m.squidPreStep()
	m.sel.Tick(noAI)
	m.hasPushedBy = false
	if m.nv != nil {
		nm := m.navMob()
		if nav.ShouldTick(false, noAI, m.health) {
			m.nv.Tick(pathWorld{&m.v}, &nm)
		}
		m.openDoorsTick(&nm)
		m.traceWanted()
	}
	still := m.ridingStill()
	if !still {
		m.moveControl()
	}
	m.lookControl()
	if !still {
		m.physicsTick()
		m.squidStep()
	}
	m.bodyControl()
	return false
}

// refresh rebuilds the mob from its definition's components, as BDS's reload: goals,
// navigation, movement, attributes and size.
func (m *Mob) refresh() {
	m.inst.Refresh(m.tester(nil), m.rebuild)
	m.flagsChanged()
}

// rebuild reads the mob's parts from its active components comps: attributes,
// navigation, movement, states, sensors and goals.
func (m *Mob) rebuild(comps definition.Group) {
	order := m.inst.Order()
	m.readAttributes(comps)
	m.readStrength(comps)
	m.readNavigation(order, comps)
	m.readMovement(order, comps)
	m.readJump(comps)
	ctx := component.Context{World: goalWorld{&m.v}, Mob: m, Resolver: resolver{m}}
	m.readStates(ctx, comps)
	m.readOpenDoors()
	m.scale = 1
	if m.states != nil && m.states.Scale != nil {
		m.scale = m.states.Scale.Value
	}
	m.readRideable()
	m.readSensors(ctx, comps)
	goals, warnings := m.rt.Goals.Build(ctx, order, comps)
	for _, w := range warnings {
		m.rt.Log.Debug("mob goal", "type", m.typ, "warning", w)
	}
	goals.Apply(&m.sel)
}

// readNavigation sets up the mob's navigation from its components; none leaves it
// without.
func (m *Mob) readNavigation(order []string, comps definition.Group) {
	n, _, ok, err := component.Navigation(order, comps)
	if !ok {
		m.nv = nil
		return
	}
	if err != nil {
		m.rt.Log.Debug("mob navigation", "type", m.typ, "err", err)
	}
	m.nv = n
	nm := m.navMob()
	m.nv.Init(&nm)
}

// readMovement reads the mob's movement component; none leaves it unable to move.
func (m *Mob) readMovement(order []string, comps definition.Group) {
	mv, ok, err := component.ReadMovement(order, comps)
	if !ok {
		m.hasMove = false
		return
	}
	if err != nil {
		m.rt.Log.Debug("mob movement", "type", m.typ, "err", err)
	}
	m.movement, m.hasMove = mv, true
	m.ctl.MaxTurn = mv.MaxTurn
}

// readJump reads whether the mob has a jump control, and gives squids their flag.
func (m *Mob) readJump(comps definition.Group) {
	_, static := comps[jumpStatic]
	_, dynamic := comps[jumpDynamic]
	m.jumpCtl = static || dynamic
	m.body.Squid = isSquid(m.typ)
}

// readAttributes reads the attribute components the minimal hookup uses.
func (m *Mob) readAttributes(comps definition.Group) {
	if v, mx, ok := attribute(comps["minecraft:health"]); ok {
		if m.baseMaxHealth != mx || m.inst.Components() == nil {
			m.health = v
		}
		m.baseMaxHealth = mx
		m.maxHealth = mx + m.healthBoost
	}
	if m.savedHealth > 0 {
		// A save's health replaces the definition's once its components are read.
		m.health = min(m.savedHealth, m.maxHealth)
		m.savedHealth = 0
	}
	if v, _, ok := attribute(comps["minecraft:movement"]); ok {
		m.speedBase = v
		m.applySpeed()
	}
	if v, _, ok := attribute(comps["minecraft:underwater_movement"]); ok {
		m.underwaterSpeed = v
	}
	if v, _, ok := attribute(comps["minecraft:lava_movement"]); ok {
		m.lavaSpeed = v
	}
	m.body.AirSpeed = defaultAirSpeed
	if v, _, ok := attribute(comps["minecraft:flying_speed"]); ok {
		m.body.AirSpeed = v
	}
	if v, _, ok := attribute(comps["minecraft:follow_range"]); ok {
		m.followRange = v
	}
	m.jumpStrength, m.jumpStrengthMax, m.hasJumpStrength = attribute(comps["minecraft:horse.jump_strength"])
	m.attackMin, m.attackDamage = 0, 0
	m.attackEffect = attackEffect{}
	raw, hasAttack := comps["minecraft:attack"]
	m.hasAttack = hasAttack
	if hasAttack {
		var a struct {
			Damage json.RawMessage `json:"damage"`
		}
		if json.Unmarshal(raw, &a) == nil {
			var rg behavior.Range[float32]
			if rg.UnmarshalJSON(a.Damage) == nil {
				m.attackMin, m.attackDamage = rg.Min, rg.Max
			}
		}
		m.attackEffect = parseAttackEffect(raw)
	}
	if raw, ok := comps["minecraft:collision_box"]; ok {
		var b struct{ Width, Height float32 }
		if json.Unmarshal(raw, &b) == nil {
			m.setSize(b.Width, b.Height)
		}
	}
	m.families = m.families[:0]
	if raw, ok := comps["minecraft:type_family"]; ok {
		var f struct {
			Family []string `json:"family"`
		}
		if json.Unmarshal(raw, &f) == nil {
			m.families = append(m.families, f.Family...)
		}
	}
	m.flags.Set(behavior.FlagFireImmune, comps["minecraft:fire_immune"] != nil)
	if raw, ok := comps["minecraft:physics"]; ok {
		var p struct {
			HasGravity   *bool `json:"has_gravity"`
			HasCollision *bool `json:"has_collision"`
		}
		if json.Unmarshal(raw, &p) == nil {
			m.flags.Set(FlagHasGravity, p.HasGravity == nil || *p.HasGravity)
			m.flags.Set(FlagCollision, p.HasCollision == nil || *p.HasCollision)
		}
	}
	m.persistent = m.persistent || comps["minecraft:persistent"] != nil
}

// attribute reads an attribute component: value and max, value as a number or a range.
func attribute(raw json.RawMessage) (v, mx float32, ok bool) {
	if raw == nil {
		return 0, 0, false
	}
	var a struct {
		Value json.RawMessage `json:"value"`
		Max   *float32        `json:"max"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Value == nil {
		return 0, 0, false
	}
	var rg behavior.Range[float32]
	if rg.UnmarshalJSON(a.Value) != nil {
		return 0, 0, false
	}
	v = rg.Max
	mx = v
	if a.Max != nil {
		mx = *a.Max
	}
	return v, mx, true
}

// navMob is the mob as navigation sees it.
func (m *Mob) navMob() nav.Mob {
	nm := nav.Mob{
		Pos: m.body.Pos, Vel: m.body.Vel, Box: m.body.Box, Width: m.body.Width, Height: m.body.Height,
		CanFly: m.flags.Has(nav.ActorCanFly), CanSwim: m.flags.Has(nav.ActorCanSwim), CanWalk: m.flags.Has(nav.ActorCanWalk),
		OnGround: m.body.OnGround, WasInWater: m.body.WasInWater, InLava: m.body.Travel() == physics.TravelLava,
		HorizontalCollision: m.body.HorizontalCollision[0] || m.body.HorizontalCollision[1],
		Actor:               flagActor{m}, Difficulty: m.rt.Env.Difficulty(), MobGriefing: m.rt.Env.MobGriefing(), Trace: m.trace,
		FireImmune: m.flags.Has(behavior.FlagFireImmune), OpensDoors: m.flags.Has(behavior.FlagDoorOpener),
	}
	if m.hasMove {
		nm.MoveControl = &m.ctl
	}
	return nm
}

type flagActor struct{ m *Mob }

func (a flagActor) Flag(n int) bool        { return a.m.flags.Has(n) }
func (a flagActor) SetFlag(n int, on bool) { a.m.SetFlag(n, on) }

// moveControl runs the movement component's control on the wanted position navigation set.
func (m *Mob) moveControl() {
	if !m.hasMove {
		return
	}
	b := move.Body{
		Pos: m.body.Pos, Vel: m.body.Vel, Box: m.body.Box, Width: m.body.Width,
		Pitch: m.pitch, Yaw: m.yaw, BodyYaw: m.bodyYaw, HeadYaw: m.headYaw, Flags: m.flags,
		OnGround: m.body.OnGround, WasInWater: m.body.WasInWater, InWater: m.body.WasInWater,
		MovementSpeed: m.speed, AirSpeed: m.body.AirSpeed, MaxAutoStep: m.body.MaxAutoStep,
		Speed: m.moveSpeed, MoveSpeed: m.moveSpeed,
		LocalMove: m.body.Input, Jump: &m.jump, Look: &m.look,
		Eye: path.Vec3{X: m.body.Pos.X, Y: m.body.Pos.Y + m.eyeHeight(), Z: m.body.Pos.Z},
	}
	// The walking controls speed up towards the end of the path the mob follows (BDS's
	// Mob::mSpeed and the navigation's last node); without them the speed stays 0.
	var end path.Pos
	if m.nv != nil {
		if p, _ := m.nv.Path(); p != nil && len(p.Points) > 0 {
			end = p.Points[len(p.Points)-1].Pos
			b.PathEnd = &end
		}
	}
	var target path.Vec3
	if t := m.rt.Env.Actor(m.v.r, m.target); t != nil {
		target = t.Pos()
		b.Target = &target
	}
	switch m.movement.Name {
	case "minecraft:movement.generic", "minecraft:movement.amphibious":
		move.Generic(pathWorld{&m.v}, &b, &m.ctl)
	case "minecraft:movement.fly":
		move.Fly(&b, &m.ctl)
	case "minecraft:movement.hover":
		move.Hover(&b, &m.ctl)
	case "minecraft:movement.jump":
		m.slimeMove(&b)
	default:
		// Basic, skip, sway, glide and dolphin fall back to basic walking in this
		// first hookup.
		move.Basic(&b, &m.ctl)
	}
	m.yaw, m.bodyYaw, m.pitch = b.Yaw, b.BodyYaw, b.Pitch
	// SetSpeed keeps the new speed in Speed; a control that stops clears MoveSpeed.
	if m.moveSpeed != 0 && b.MoveSpeed == 0 {
		m.moveSpeed = 0
	} else {
		m.moveSpeed = b.Speed
	}
	m.body.Input = b.LocalMove
	m.body.Vel = b.Vel
	m.flags = b.Flags
}

// lookControl is LookControl::tick (BDS 1.26.60 0xBFBF1E0): the head turns toward what a
// goal looks at, or to the rotation a control asked for; with neither, the pitch goes back
// to level and the head to the body, 10 degrees a tick. The head then stays within 75
// degrees of the body.
func (m *Mob) lookControl() {
	switch {
	case m.look.Has:
		d := path.Vec3{X: m.look.Pos.X - m.body.Pos.X, Y: m.look.Pos.Y - (m.body.Pos.Y + m.eyeHeight()), Z: m.look.Pos.Z - m.body.Pos.Z}
		yaw := float32(math.Atan2(float64(d.Z), float64(d.X)))*180/math.Pi - 90
		h := float32(math.Sqrt(float64(d.X*d.X + d.Z*d.Z)))
		pitch := -float32(math.Atan2(float64(d.Y), float64(h))) * 180 / math.Pi
		m.pitch = approach(m.pitch, pitch, m.look.PitchSpeed)
		m.headYaw = approach(m.headYaw, yaw, m.look.YawSpeed)
		m.look.Has = false
	case m.look.HasYaw:
		m.pitch = approach(m.pitch, m.look.Pitch, m.look.PitchSpeed)
		m.headYaw = approach(m.headYaw, m.look.Yaw, m.look.YawSpeed)
		m.look.HasYaw = false
	default:
		m.pitch = approach(m.pitch, 0, lookReset)
		m.headYaw = approach(m.headYaw, m.bodyYaw, lookReset)
	}
	m.headYaw = clampAround(m.headYaw, m.bodyYaw, bodyMaxTurn)
}

// lookReset is how far, in degrees a tick, the look control turns the head back when
// nothing is looked at.
const lookReset = 10

// bodyControl turns the body: with the movement while it moves, else within reach of the
// head.
func (m *Mob) bodyControl() {
	dx, dz := m.body.Pos.X-m.body.PrevPos.X, m.body.Pos.Z-m.body.PrevPos.Z
	if dx*dx+dz*dz > 2.5e-7 {
		m.bodyYaw = m.yaw
		m.headYaw = clampAround(m.headYaw, m.bodyYaw, bodyMaxTurn)
		return
	}
	m.bodyYaw = clampAround(m.bodyYaw, m.headYaw, bodyMaxTurn)
}

func (m *Mob) physicsTick() {
	m.body.PrevPos = m.body.Pos
	m.body.Yaw = m.yaw
	m.body.Flags = m.flags
	m.body.MovementSpeed = m.speed
	m.body.Attrs.Underwater, m.body.Attrs.Lava = &m.underwaterSpeed, &m.lavaSpeed
	m.body.Jumping = m.jump.Jumping
	m.jump.Jumping = false
	m.body.Nav = physics.Nav{Present: m.nv != nil, Following: m.nv != nil && !m.nv.Done()}
	if m.nv != nil {
		m.body.Nav.CanSink = m.nv.Flags&nav.FlagCanSink != 0
		m.body.Nav.CanWalkInLava = m.nv.Flags&nav.FlagCanWalkInLava != 0
	}
	if t := m.rt.Env.Actor(m.v.r, m.target); t != nil {
		m.body.HasTarget, m.body.Target = true, t.Pos()
	} else {
		m.body.HasTarget = false
	}
	m.blockClimb()
	m.beforeMove()
	m.applyBody()
	m.liquidPush()
	m.body.Tick(physWorld{&m.v})
	if !m.customBox {
		m.body.Box = boxAt(m.body.Pos, m.body.Width, m.body.Height)
	}
	m.body.UpdateLiquid(physWorld{&m.v}, m.ridingBoat())
	m.body.HeadInWater = m.headInWater()
	m.blockSlowdown()
	m.traceBlocks()
}

// liquidPush is BDS 1.26.60's LiquidPhysicsSystem push (sub_77DCDE0) before the mob moves: the
// current of the lava or water in the body's liquid boxes, the same push items get.
func (m *Mob) liquidPush() {
	lava, water := m.body.LiquidBoxes()
	push := liquid.Push(m.v.blocks.reg, m.v.r, blockArea(lava), blockArea(water))
	m.body.Vel.X += push[0]
	m.body.Vel.Y += push[1]
	m.body.Vel.Z += push[2]
	if push != [3]float32{} {
		m.traceVel("liquid current", behavior.Vec3{X: push[0], Y: push[1], Z: push[2]})
	}
}

// blockArea is the blocks a box covers.
func blockArea(b path.Box) liquid.Area {
	return liquid.Area{
		Min: cube.Pos{int(floor32(b.Min.X)), int(floor32(b.Min.Y)), int(floor32(b.Min.Z))},
		Max: cube.Pos{int(floor32(b.Max.X)), int(floor32(b.Max.Y)), int(floor32(b.Max.Z))},
	}
}

// ridingBoat reports a vehicle that is a boat, whose riders BDS never counts as in water.
func (m *Mob) ridingBoat() bool {
	v := m.Vehicle()
	if v == behavior.NoID {
		return false
	}
	a := m.rt.Env.Actor(m.v.r, v)
	if a == nil {
		return false
	}
	t := a.Type()
	return t == nameBoat || t == nameChestBoat
}

// The boats, whose riders are never in water.
const (
	nameBoat      = "minecraft:boat"
	nameChestBoat = "minecraft:chest_boat"
)

// headInWater is BDS 1.26.60's Actor::isHeadInLiquid with water (sub_B39C240): the block at
// the mob's eyes is water whose surface, by BlockUtils::getLiquidBlockHeight (0xB656EF0), is
// above them.
func (m *Mob) headInWater() bool {
	eyes := m.Eyes()
	p := path.Pos{X: floor32(m.body.Pos.X), Y: floor32(eyes.Y), Z: floor32(m.body.Pos.Z)}
	b := m.v.block(p, 0)
	if m.v.blocks.Path(b).Material != path.MaterialWater {
		return false
	}
	depth, _ := m.v.blocks.reg.States(b)["liquid_depth"].(int32)
	if depth >= liquidFalling {
		depth = 0
	}
	return float32(p.Y)+1-float32(depth)/liquidLevels > eyes.Y
}

// The liquid depths: liquidFalling and above is falling liquid, a full block; liquidLevels
// divides the depth into the block's height.
const (
	liquidFalling = 8
	liquidLevels  = 9
)

func (m *Mob) eyeHeight() float32 { return m.body.Height * 0.85 }

func wrap(d float32) float32 {
	d = float32(math.Mod(float64(d), 360))
	if d >= 180 {
		d -= 360
	}
	if d < -180 {
		d += 360
	}
	return d
}

func approach(from, to, step float32) float32 {
	d := wrap(to - from)
	return from + max(-step, min(step, d))
}

func clampAround(v, centre, limit float32) float32 {
	d := wrap(v - centre)
	return centre + max(-limit, min(limit, d))
}

// Hurt takes damage from the attacker (NoID for none) with a cause. It applies BDS's hurt
// time: within it only damage above the last hurt counts. It reports the damage dealt.
func (m *Mob) Hurt(amount float32, by behavior.ID, cause int, tick uint64) float32 {
	return m.hurt(amount, by, cause, tick, false)
}

// HurtIgnoringCooldown is Hurt with the hurt time skipped, as BDS's hurt cooldown bypass
// (Actor +849) does for the guardian's magic hit: the damage counts in full and a new hurt
// time starts.
func (m *Mob) HurtIgnoringCooldown(amount float32, by behavior.ID, cause int, tick uint64) float32 {
	return m.hurt(amount, by, cause, tick, true)
}

func (m *Mob) hurt(amount float32, by behavior.ID, cause int, tick uint64, ignoreCooldown bool) float32 {
	if m.Dead() || amount <= 0 {
		return 0
	}
	attacker := m.actor(by)
	amount, ok := m.damageSensed(amount, cause, attacker)
	if !ok || amount <= 0 {
		m.traceHurt("hurt stopped by damage sensor", amount, cause)
		return 0
	}
	m.endermanHurt(amount, attacker)
	dealt := amount
	if m.hurtTime > 0 && !ignoreCooldown {
		if amount <= m.lastHurtAmount {
			m.traceHurt("hurt ignored in hurt time", amount, cause)
			return 0
		}
		dealt = amount - m.lastHurtAmount
	} else {
		m.hurtTime = hurtTicks
	}
	m.hurtThisTick, m.hurtAt = true, tick
	m.lastHurtAmount = amount
	if h, ok := m.hooks.(ArmorHooks); ok {
		dealt = h.AfterArmor(m, dealt, cause)
	}
	m.traceHurt("hurt", dealt, cause)
	m.health = max(m.health-dealt, 0)
	m.endermanHurtEffects(dealt)
	m.lastCause = cause
	if by != behavior.NoID {
		m.lastHurtBy, m.lastHurtTick = by, tick
	}
	m.noActionTime = 0
	m.fire(onHurt, attacker)
	if attacker != nil && attacker.IsPlayer() {
		m.lastHurtByPlayer = attacker.ID()
		m.playerHitTime = playerHitTicks
		m.fire(onHurtByPlayer, attacker)
	}
	if m.Dead() {
		m.fire(onDeath, attacker)
	}
	m.snapshot()
	return dealt
}

// Knock pushes the mob as a hit does.
func (m *Mob) Knock(v behavior.Vec3) {
	m.traceVel("knocked", v)
	m.body.Vel = v
}

// SetPos moves the mob, as a teleport does.
func (m *Mob) SetPos(p behavior.Vec3) {
	m.body.Pos, m.body.PrevPos = p, p
	m.body.Box = boxAt(p, m.body.Width, m.body.Height)
	m.customBox = false
	if !m.thinking {
		m.snapshot()
	}
}

// Ready builds the mob from its definition before it is in the world, so its size,
// attributes and flags are known: the natural spawner checks them before adding it. r is the
// region it will join. Runs: owner of r.
func (m *Mob) Ready(r *world.Region) {
	m.v.r = r
	if m.inst.Components() == nil {
		m.refresh()
	}
	m.snapshot()
}

// Event runs a definition event on the mob.
func (m *Mob) RunEvent(name string) { m.inst.Execute(name, m.tester(nil)) }
