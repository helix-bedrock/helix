package player

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Projectiles, from BDS 1.26.60's ProjectileSystem (0x7745da0), ProjectileComponent and the
// on hit subcomponents. What differs between them is read from each one's definition.
const (
	nameSnowball   = "minecraft:snowball"
	nameEgg        = "minecraft:egg"
	nameEnderPearl = "minecraft:ender_pearl"
	nameArrow      = "minecraft:arrow"
	nameBow        = "minecraft:bow"

	nameWater        = "minecraft:water"
	nameFlowingWater = "minecraft:flowing_water"

	// arrowGroupPlayer is the part of the arrow's definition a player's bow gives it.
	arrowGroupPlayer = "minecraft:player_arrow"

	// projectileLife is how many ticks one stuck in a block lasts.
	projectileLife = 1200
	// projectileSpread is what a gaussian on each axis counts for, times its uncertainty.
	projectileSpread = 0.0075
	// projectileReachMax and projectileReachStep make a target's box grow with the
	// projectile's age: nothing for projectileReachFrom ticks, then up to the most.
	projectileReachMax  = 0.3
	projectileReachStep = 0.05
	projectileReachFrom = 2
	// projectileScan is how far around its path to look for what it may hit.
	projectileScan = 3
	// projectileClipLimit is how many blocks one tick's flight walks at most, BDS's 200.
	projectileClipLimit = 200
	// projectileMoveDropped and projectileMoveFar are BDS's defaults for a definition's
	// conditional bandwidth optimization.
	projectileMoveDropped = actorMoveDropped
	projectileMoveFar     = actorMoveFar
	// projectileTurn is how much of a turn its rotation makes each tick.
	projectileTurn = 0.2
	// projectileBounce is what is left of its speed, turned round, when it does no damage.
	projectileBounce = -0.1
	// pearlDamage is the fall damage of arriving by ender pearl, 0x714f4b0.
	pearlDamage = 5
	// pearlCooldown is how many ticks a player must wait between pearls.
	pearlCooldown = 20

	// critFlat and critShare make up what a critical arrow may add at most.
	critFlat  = 2
	critShare = 0.5
	// powerBase and powerPerLevel multiply an arrow's damage by its bow's Power.
	powerBase     = 1.25
	powerPerLevel = 0.25
	// punchPerLevel is what each level of its bow's Punch adds to its knockback.
	punchPerLevel = 1.6
	// bowUseTicks is how long a bow can be held drawn, its use duration. bowMinPull is the
	// least draw that shoots, and bowPullSpan how many ticks are a second in the draw's sum.
	bowUseTicks = 72000
	bowMinPull  = 0.1
	bowPullSpan = 20

	// The enchantments a bow reads, by BDS's IDs.
	enchantPower    = int16(19)
	enchantPunch    = int16(20)
	enchantInfinity = int16(22)

	// actorEventArrowShake is the event BDS sends when one sticks, with how long it shakes.
	actorEventArrowShake = 39
	// levelEventPop is BDS's sound of a picked up arrow.
	levelEventPop = 1030
	// levelEventDispenseProjectile is a dispenser's sound for what it shoots.
	levelEventDispenseProjectile = 1002
	// dispenseProjectilePitch is the data of that sound, BDS's.
	dispenseProjectilePitch = 78642
	// dispenseLift is how far up a dispenser aims above its front.
	dispenseLift = 0.1
	// popPitchSpread and popPitchScale make the random pitch of levelEventPop.
	popPitchSpread = 0.7
	popPitchScale  = 2000

	projectileFlags = 1<<protocol.EntityDataFlagHasGravity | 1<<protocol.EntityDataFlagHasCollision

	projectileKeyInGround = "inGround"
	projectileKeyPickup   = "pickup"
	projectileKeyStuckAt  = "stuckAt"
	projectileKeyPotion   = "PotionId"
)

// arrowPickup says who may pick up a stuck arrow.
type arrowPickup uint8

const (
	// arrowPickupAll gives an arrow to whoever touches it.
	arrowPickupAll arrowPickup = iota
	// arrowPickupCreative only lets players in creative clear it, for nothing.
	arrowPickupCreative
)

// ProjectileSpec is what a kind of projectile does, from the minecraft:projectile component
// of its definition. A plugin may change one for a single projectile: see [Projectile.Spec].
type ProjectileSpec struct {
	// ID is the entity's identifier.
	ID string
	// Width and Height are its collision box.
	Width, Height float32
	// Power is its speed when shot, in blocks a tick. Gravity is what it loses each tick;
	// Inertia what is left of its speed each tick, LiquidInertia the same in water.
	Power, Gravity, Inertia, LiquidInertia float32
	// UncertaintyBase less UncertaintyPerDifficulty for each level of difficulty is how far
	// off it may fly.
	UncertaintyBase, UncertaintyPerDifficulty float32
	// DamageMin and DamageMax bound its base damage, and PowerMultiplier adds its speed
	// times this. DamageOnly names the only family it damages, empty for all.
	DamageMin, DamageMax, PowerMultiplier float32
	DamageOnly                            string
	// CeilDamage rounds its damage up before a critical hit adds to it.
	CeilDamage bool
	// Knockback pushes what it hits.
	Knockback bool
	// RemoveOnHit removes it after any hit, and DestroyOnHit after hitting an entity.
	RemoveOnHit, DestroyOnHit bool
	// MoveDroppedTicks and MoveFarDistance say how seldom far players get its moves.
	MoveDroppedTicks int32
	MoveFarDistance  float32
	// Stick leaves it in the block it hits, shaking for ShakeTicks.
	Stick      bool
	ShakeTicks int32
	// TeleportOwner moves who threw it to where it lands.
	TeleportOwner bool
	// Bounce turns it back when it hits what it cannot damage.
	Bounce bool
	// HitSound is the sound it makes on hitting something, empty for none.
	HitSound string
	// StopOnHurt stops it when it hurts what it hits (stop_on_hurt).
	StopOnHurt bool
	// Offset is how far from its shooter's eyes it starts, turned with the shooter.
	Offset mgl32.Vec3
	// ReflectOnHurt turns it back along its attacker's view when hit, once it has flown
	// ReflectTicks (reflect_on_hurt, reflect_immunity).
	ReflectOnHurt bool
	ReflectTicks  int32
	// WindBurst explodes it with Burst where it hits, then removes it (wind_burst_on_hit).
	WindBurst bool
	// Burst is its minecraft:explode component.
	Burst ExplodeSpec
	// Particle is the particle_on_hit particle, empty for none, shown when it hits an
	// entity (ParticleOnEntity) or anything else (ParticleOnOther).
	Particle                          string
	ParticleCount                     int32
	ParticleOnEntity, ParticleOnOther bool
	// AngleOffset turns its thrower's aim up (negative) or down, in degrees (angle_offset).
	AngleOffset float32
	// SplashPotion splashes its potion's effects within SplashRange where it hits
	// (thrown_potion_effect); Cloud leaves its potion's cloud there (spawn_aoe_cloud).
	SplashPotion bool
	SplashRange  float32
	Cloud        *CloudSpec
	// DouseFire puts fires out where a water potion hits a block (douse_fire).
	DouseFire bool
	// GrantXP leaves experience worth XPMin up to XPMax less one where it hits (grant_xp).
	GrantXP      bool
	XPMin, XPMax int32
}

// ExplodeSpec is a definition's minecraft:explode component.
type ExplodeSpec struct {
	Power, DamageScale, KnockbackScale        float32
	BreaksBlocks, CausesFire, AllowUnderwater bool
	TogglesBlocks, NegatesFallDamage          bool
	ParticleEffect, SoundEffect               string
}

// explodeComponent is the minecraft:explode component, with BDS's defaults (0x7258540).
type explodeComponent struct {
	Power           *float32 `json:"power"`
	DamageScale     *float32 `json:"damage_scaling"`
	KnockbackScale  *float32 `json:"knockback_scaling"`
	BreaksBlocks    *bool    `json:"breaks_blocks"`
	CausesFire      bool     `json:"causes_fire"`
	AllowUnderwater bool     `json:"allow_underwater"`
	TogglesBlocks   bool     `json:"toggles_blocks"`
	NegatesFall     bool     `json:"negates_fall_damage"`
	Particle        string   `json:"particle_effect"`
	Sound           string   `json:"sound_effect"`
}

// BDS's defaults of minecraft:explode where a definition does not say.
const (
	explodeDefaultPower = 3
	explodeDefaultScale = 1
)

// spec is the component read with its defaults.
func (c explodeComponent) spec() ExplodeSpec {
	s := ExplodeSpec{
		Power: explodeDefaultPower, DamageScale: explodeDefaultScale, KnockbackScale: explodeDefaultScale, BreaksBlocks: true,
		CausesFire: c.CausesFire, AllowUnderwater: c.AllowUnderwater, TogglesBlocks: c.TogglesBlocks,
		NegatesFallDamage: c.NegatesFall, ParticleEffect: c.Particle, SoundEffect: c.Sound,
	}
	if c.Power != nil {
		s.Power = *c.Power
	}
	if c.DamageScale != nil {
		s.DamageScale = *c.DamageScale
	}
	if c.KnockbackScale != nil {
		s.KnockbackScale = *c.KnockbackScale
	}
	if c.BreaksBlocks != nil {
		s.BreaksBlocks = *c.BreaksBlocks
	}
	return s
}

// projectileDefinition is the part of a definition this reads.
type projectileDefinition struct {
	Entity struct {
		Components map[string]json.RawMessage            `json:"components"`
		Groups     map[string]map[string]json.RawMessage `json:"component_groups"`
	} `json:"minecraft:entity"`
}

// projectileComponent is the minecraft:projectile component.
type projectileComponent struct {
	Power           float32   `json:"power"`
	Gravity         float32   `json:"gravity"`
	Inertia         *float32  `json:"inertia"`
	LiquidInertia   *float32  `json:"liquid_inertia"`
	UncertaintyBase float32   `json:"uncertainty_base"`
	UncertaintyMult float32   `json:"uncertainty_multiplier"`
	HitSound        string    `json:"hit_sound"`
	ShouldBounce    string    `json:"should_bounce"`
	StopOnHurt      bool      `json:"stop_on_hurt"`
	ReflectOnHurt   bool      `json:"reflect_on_hurt"`
	ReflectImmunity float32   `json:"reflect_immunity"`
	Offset          []float32 `json:"offset"`
	AngleOffset     float32   `json:"angle_offset"`
	OnHit           struct {
		Impact *struct {
			Damage struct {
				Min float32 `json:"min"`
				Max float32 `json:"max"`
			} `json:"damage"`
			Filter          string  `json:"filter"`
			PowerMultiplier float32 `json:"power_multiplier"`
			Ceil            bool    `json:"ceil_pre_critical_damage"`
			Knockback       bool    `json:"knockback"`
			Destroy         bool    `json:"destroy_on_hit"`
		} `json:"impact_damage"`
		Remove *struct{} `json:"remove_on_hit"`
		Stick  *struct {
			ShakeTime float32 `json:"shake_time"`
		} `json:"stick_in_ground"`
		Teleport *struct{} `json:"teleport_owner"`
		Thrown   *struct{} `json:"thrown_potion_effect"`
		Douse    *struct{} `json:"douse_fire"`
		Cloud    *struct {
			Duration    int32    `json:"duration"`
			Radius      float32  `json:"radius"`
			RadiusOnUse *float32 `json:"radius_on_use"`
			Reapply     int32    `json:"reapplication_delay"`
			AffectOwner *bool    `json:"affect_owner"`
		} `json:"spawn_aoe_cloud"`
		WindBurst *struct{}       `json:"wind_burst_on_hit"`
		GrantXP   json.RawMessage `json:"grant_xp"`
		Particle  *struct {
			Type     string `json:"particle_type"`
			Count    int32  `json:"num_particles"`
			OnEntity bool   `json:"on_entity_hit"`
			OnOther  bool   `json:"on_other_hit"`
		} `json:"particle_on_hit"`
	} `json:"on_hit"`
}

// projectileBox is the minecraft:collision_box component.
type projectileBox struct {
	Width  float32 `json:"width"`
	Height float32 `json:"height"`
}

// Defaults of the component when a definition does not say, read from its constructor
// (0x7143370).
const (
	projectileInertia       = 0.99
	projectileLiquidInertia = 0.6
	// projectileSplashRange is its splash_range (ProjectileComponent +424).
	projectileSplashRange = 4
	// cloudRadiusOnUse is spawn_aoe_cloud's default radius_on_use (0x714E6E0).
	cloudRadiusOnUse = -1
)

// projectileBandwidth is the part of a definition that says how often far players get moves.
type projectileBandwidth struct {
	Default struct {
		Dropped int32   `json:"max_dropped_ticks"`
		Far     float32 `json:"max_optimized_distance"`
	} `json:"default_values"`
}

// ticksPerShakeSecond turns a definition's shake time into ticks.
const ticksPerShakeSecond = 20

// projectileBounceAlways names should_bounce's value that turns it back.
const projectileBounceNoDamage = "if_no_damage_dealt"

// parseProjectileSpec reads a spec from a definition. group is the component group that
// holds the component, empty for the definition's own components.
func parseProjectileSpec(id, group string, definition []byte) (ProjectileSpec, error) {
	var d projectileDefinition
	if err := json.Unmarshal(definition, &d); err != nil {
		return ProjectileSpec{}, fmt.Errorf("player: projectile %s: %w", id, err)
	}
	raw, ok := d.Entity.Components["minecraft:projectile"]
	if group != "" {
		raw, ok = d.Entity.Groups[group]["minecraft:projectile"]
	}
	if !ok {
		return ProjectileSpec{}, fmt.Errorf("player: projectile %s: no component in group %q", id, group)
	}
	var c projectileComponent
	if err := json.Unmarshal(raw, &c); err != nil {
		return ProjectileSpec{}, fmt.Errorf("player: projectile %s: %w", id, err)
	}
	var box projectileBox
	if rawBox, has := d.Entity.Components["minecraft:collision_box"]; has {
		if err := json.Unmarshal(rawBox, &box); err != nil {
			return ProjectileSpec{}, fmt.Errorf("player: projectile %s: %w", id, err)
		}
	}
	band := projectileBandwidth{}
	band.Default.Dropped, band.Default.Far = projectileMoveDropped, projectileMoveFar
	if rawBand, has := d.Entity.Components["minecraft:conditional_bandwidth_optimization"]; has {
		if err := json.Unmarshal(rawBand, &band); err != nil {
			return ProjectileSpec{}, fmt.Errorf("player: projectile %s: %w", id, err)
		}
	}
	s := ProjectileSpec{
		MoveDroppedTicks: band.Default.Dropped, MoveFarDistance: band.Default.Far,
		ID: id, Width: box.Width, Height: box.Height, Power: c.Power, Gravity: c.Gravity,
		Inertia: projectileInertia, LiquidInertia: projectileLiquidInertia,
		UncertaintyBase: c.UncertaintyBase, UncertaintyPerDifficulty: c.UncertaintyMult,
		HitSound: c.HitSound, Bounce: c.ShouldBounce == projectileBounceNoDamage, StopOnHurt: c.StopOnHurt,
		RemoveOnHit: c.OnHit.Remove != nil, TeleportOwner: c.OnHit.Teleport != nil,
		AngleOffset: c.AngleOffset, SplashPotion: c.OnHit.Thrown != nil, SplashRange: projectileSplashRange,
		DouseFire: c.OnHit.Douse != nil,
	}
	if a := c.OnHit.Cloud; a != nil {
		cs := &CloudSpec{
			Duration: max(a.Duration, 0) * cloudTicksPerSecond, Radius: a.Radius, RadiusOnUse: cloudRadiusOnUse,
			ReapplicationDelay: a.Reapply, Particle: cloudParticleMobSpellAmbient, AffectOwner: true,
		}
		if a.RadiusOnUse != nil {
			cs.RadiusOnUse = *a.RadiusOnUse
		}
		if a.AffectOwner != nil {
			cs.AffectOwner = *a.AffectOwner
		}
		s.Cloud = cs
	}
	if len(c.Offset) == 3 {
		s.Offset = mgl32.Vec3{c.Offset[0], c.Offset[1], c.Offset[2]}
	}
	if c.Inertia != nil {
		s.Inertia = *c.Inertia
	}
	if c.LiquidInertia != nil {
		s.LiquidInertia = *c.LiquidInertia
	}
	if i := c.OnHit.Impact; i != nil {
		s.DamageMin, s.DamageMax, s.PowerMultiplier = i.Damage.Min, i.Damage.Max, i.PowerMultiplier
		s.DamageOnly, s.CeilDamage, s.Knockback, s.DestroyOnHit = i.Filter, i.Ceil, i.Knockback, i.Destroy
	}
	if st := c.OnHit.Stick; st != nil {
		s.Stick, s.ShakeTicks = true, int32(st.ShakeTime*ticksPerShakeSecond)
	}
	s.ReflectOnHurt = c.ReflectOnHurt
	s.ReflectTicks = int32(math.Floor(float64(c.ReflectImmunity * reflectTicksPerSecond)))
	if p := c.OnHit.Particle; p != nil {
		s.Particle, s.ParticleCount, s.ParticleOnEntity, s.ParticleOnOther = p.Type, p.Count, p.OnEntity, p.OnOther
	}
	if len(c.OnHit.GrantXP) > 0 {
		lo, hi, err := parseGrantXP(c.OnHit.GrantXP)
		if err != nil {
			return ProjectileSpec{}, fmt.Errorf("player: projectile %s: grant_xp: %w", id, err)
		}
		s.GrantXP, s.XPMin, s.XPMax = true, lo, hi
	}
	if c.OnHit.WindBurst != nil {
		s.WindBurst = true
		var ex explodeComponent
		if raw, has := d.Entity.Components["minecraft:explode"]; has {
			if err := json.Unmarshal(raw, &ex); err != nil {
				return ProjectileSpec{}, fmt.Errorf("player: projectile %s: %w", id, err)
			}
		}
		s.Burst = ex.spec()
	}
	return s, nil
}

// ProjectileSpec is the spec of the entity type id's projectile, read from its definition,
// for SpawnProjectile; false if the type has none. Runs: anywhere.
func (s *Host) ProjectileSpec(id string) (ProjectileSpec, bool) { return s.projectileSpec(id, "") }

// projectileSpecKey names a spec in the Host's cache.
type projectileSpecKey struct{ id, group string }

// projectileSpec is the spec of a kind of projectile, read once. False if the kind has none.
func (s *Host) projectileSpec(id, group string) (ProjectileSpec, bool) {
	key := projectileSpecKey{id, group}
	if v, ok := s.projectileSpecs.Load(key); ok {
		spec, isSpec := v.(ProjectileSpec)
		return spec, isSpec
	}
	t, ok := s.reg.EntityTypes.ByName(id)
	if !ok {
		return ProjectileSpec{}, false
	}
	spec, err := parseProjectileSpec(id, group, t.Definition)
	if err != nil {
		s.log.Warn("no projectile", "err", err)
		return ProjectileSpec{}, false
	}
	s.projectileSpecs.Store(key, spec)
	return spec, true
}

// Projectile is anything flying as a projectile. Runs: owner of its entity.
type Projectile struct {
	s      *Host
	spec   ProjectileSpec
	entity *world.Entity
	vel    mgl32.Vec3
	rot    world.Rotation

	// plan is what its think step found along this tick's flight, when planned.
	plan     projectileHit
	planKind projectileHitKind
	planned  bool

	// waiting holds a new one still for its first tick. The players who see it are told
	// after entities tick, so it would first show a whole tick's flight from where it started.
	waiting bool
	// owner is who shot it, if anyone. separated is that it has left its owner's box: until
	// then it passes through its owner.
	owner     *Player
	separated bool

	// age is how many ticks it has flown; stuckTicks how long it has been stuck.
	age, stuckTicks int32
	stuck           bool
	stuckAt         cube.Pos
	shake           int32

	// critical, power, punch and pickup are from the bow that shot it.
	critical bool
	power    int32
	punch    float32
	pickup   arrowPickup
	item     ItemStack
	// pierce is how many more entities it may fly through (a crossbow's Piercing), and
	// pierced those it already went through, which it does not hit again.
	pierce  int32
	pierced []*world.Entity
	// trident is set for a thrown trident.
	trident *tridentState
	// potion is a thrown potion's ID, the item's meta.
	potion int16

	// moves is what those who see it were last told of its movement.
	moves actorMoves
	// stuckMoveDue is the move BDS always sends on the tick after it sticks.
	stuckMoveDue bool
	// ticked and shown count off its start: BDS makes every projectile invisible when it is
	// made and visible at the end of its second tick.
	ticked, shown bool

	gone  bool
	saved map[string]any
	found []*world.Entity
}

// Entity returns its entity.
func (t *Projectile) Entity() *world.Entity { return t.entity }

// Spec returns what it does.
func (t *Projectile) Spec() ProjectileSpec { return t.spec }

// SetSpec changes what it does from now on.
func (t *Projectile) SetSpec(spec ProjectileSpec) { t.spec = spec }

// Velocity returns its speed in blocks a tick.
func (t *Projectile) Velocity() mgl32.Vec3 { return t.vel }

// SetVelocity changes its speed.
func (t *Projectile) SetVelocity(vel mgl32.Vec3) { t.vel = vel }

// aim is BDS's shoot (0x71480f0): dir made one long, off by a gaussian on each axis, times
// the speed.
func aim(dir mgl32.Vec3, speed, uncertainty float32) mgl32.Vec3 {
	if dir.Len() < 0.001 {
		dir = mgl32.Vec3{}
	} else {
		dir = dir.Normalize()
	}
	for n := range 3 {
		dir[n] += float32(rand.NormFloat64()) * projectileSpread * uncertainty //nolint:gosec // an aim, not a secret
	}
	return dir.Mul(speed)
}

// flightRotation is the rotation that points along vel.
func flightRotation(vel mgl32.Vec3) world.Rotation {
	flat := math.Sqrt(float64(vel[0]*vel[0] + vel[2]*vel[2]))
	yaw := float32(math.Atan2(float64(vel[0]), float64(vel[2])) * 180 / math.Pi)
	pitch := float32(math.Atan2(float64(vel[1]), flat) * 180 / math.Pi)
	return world.Rotation{Pitch: pitch, Yaw: yaw, HeadYaw: yaw}
}

// SpawnProjectile adds spec's projectile at pos, flying along dir at speed. owner is who
// shot it, or nil.
func (s *Host) SpawnProjectile(d *world.Dimension, spec ProjectileSpec, pos, dir mgl32.Vec3, speed float32, owner *Player) *Projectile {
	uncertainty := max(spec.UncertaintyBase-spec.UncertaintyPerDifficulty*float32(s.level.Difficulty), 0)
	t := &Projectile{s: s, spec: spec, owner: owner, vel: aim(dir, speed, uncertainty), separated: owner == nil, waiting: true}
	t.rot = flightRotation(t.vel)
	t.entity = d.AddEntity(t.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), pos, t.rot)) //nolint:gosec // an ID, not a secret
	return t
}

func (t *Projectile) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	t.moves.start(pos, rot, t.spec.MoveDroppedTicks, t.spec.MoveFarDistance)
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: t, Spawn: t.spawnPackets, Move: t.movePacket, Think: t.think, Input: t.tick, Save: t.saveEntity,
	}
}

// spawnPackets show it to a player. Not yet compared with what BDS sends.
func (t *Projectile) spawnPackets(e *world.Entity) []packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags: t.flags(), protocol.EntityDataKeyWidth: t.spec.Width,
		protocol.EntityDataKeyHeight: t.spec.Height, protocol.EntityDataKeyScale: float32(1),
	}
	if t.owner != nil && t.owner.entity != nil {
		meta[protocol.EntityDataKeyOwner] = t.owner.entity.UniqueID()
	}
	if t.isPotion() {
		meta[protocol.EntityDataKeyAuxValueData] = t.potion
	}
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: t.spec.ID,
		Position: e.Position(), Velocity: t.vel, Pitch: t.rot.Pitch, Yaw: t.rot.Yaw, HeadYaw: t.rot.HeadYaw,
		EntityMetadata: meta,
	}}
}

func (t *Projectile) flags() int64 {
	f := int64(projectileFlags)
	if t.shown {
		f |= 1 << protocol.EntityDataFlagShowName
	} else {
		f |= 1 << protocol.EntityDataFlagInvisible
	}
	if t.critical {
		f |= 1 << protocol.EntityDataFlagCritical
	}
	if t.trident != nil && t.trident.returning {
		f |= 1 << protocol.EntityDataFlagReturnTrident
	}
	if t.spec.ID == nameLingeringPotion {
		f |= 1 << protocol.EntityDataFlagLingering
	}
	return f
}

// rotationByte is a rotation as BDS keeps it for a move: a signed byte, 256 to the turn.
func rotationByte(deg float32) int8 {
	wrapped := math.Mod(float64(deg)+180, 360)
	if wrapped < 0 {
		wrapped += 360
	}
	return int8(int32((wrapped - 180) / 360 * 256))
}

// movePacket sends its move as BDS's actor movement sync does. Nil: the sync sends its own.
func (t *Projectile) movePacket(e *world.Entity) packet.Packet {
	t.moves.update(e, t.vel)
	return nil
}

// moveTicks is BDS's count for a player dist away.
func (s ProjectileSpec) moveTicks(dist float32) int64 {
	return moveTicks(s.MoveDroppedTicks, s.MoveFarDistance, dist)
}

// tick is BDS's projectile tick: if stuck, whether the block still holds it; if not, what
// lies along this tick's flight, in order, then the move, the turn, slowing and gravity.
func (t *Projectile) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || t.gone {
		return
	}
	if t.s.checkVoid(e); t.gone {
		return
	}
	if t.waiting {
		t.waiting = false
		return
	}
	t.fly(r, e)
	if t.gone {
		return
	}
	if !t.ticked {
		t.ticked = true
	} else if !t.shown {
		t.shown = true
		t.sendFlags(e)
	}
}

// fly is one tick of what it does. BDS tells clients only where it is: they fly it themselves
// from the velocity in AddActor, and BDS never sends one its velocity after that.
func (t *Projectile) fly(r *world.Region, e *world.Entity) {
	if t.stuckMoveDue {
		t.stuckMoveDue = false
		t.moves.send(e)
	}
	if t.shake > 0 {
		e.Send(actorEventData(e.RuntimeID(), actorEventArrowShake, t.shake))
		t.shake = 0
	}
	if t.tridentReturns(r, e) {
		t.planned = false
		return
	}
	if t.stuck {
		t.planned = false
		t.tickStuck(r, e)
		return
	}
	t.age++
	from := e.Position()
	to := from.Add(t.vel)
	hit, kind := t.plan, t.planKind
	if !t.planned {
		t.checkSeparated(r, from, to)
		hit, kind = t.firstHit(r, from, to)
	}
	t.plan, t.planned = projectileHit{}, false
	pos := to
	if kind != projectileHitNone {
		pos = hit.pos
		t.apply(r, e, hit, kind)
		if t.gone || t.stuck {
			return
		}
		if kind == projectileHitEntity && t.wasPierced(hit.entity) {
			pos = to
		}
	}
	rot := t.rot
	t.turn()
	// The next chunk is not loaded yet: it waits with its speed, and flies on once it is.
	if !e.Apply(world.Movement{Pos: pos, Rot: t.rot, Tick: e.Tick()}) {
		t.rot = rot
		t.age--
		return
	}
	inertia := t.spec.Inertia
	if t.s.inWater(r, pos) {
		inertia = t.spec.LiquidInertia
	}
	t.vel = t.vel.Mul(inertia)
	t.vel[1] -= t.spec.Gravity
}

// sendFlags tells those who see it its flags.
func (t *Projectile) sendFlags(e *world.Entity) {
	e.Send(actorData(e.RuntimeID(), protocol.EntityMetadata{protocol.EntityDataKeyFlags: t.flags()}))
}

// projectileEyes is where a shooter's projectile starts: its eyes and the definition's
// offset, turned with the shooter.
func projectileEyes(spec ProjectileSpec, eyes mgl32.Vec3, rot world.Rotation) mgl32.Vec3 {
	yaw := float64(rot.Yaw) * -math.Pi / 180
	c, s := float32(math.Cos(yaw)), float32(math.Sin(yaw))
	o := spec.Offset
	return eyes.Add(mgl32.Vec3{o[0]*c + o[2]*s, o[1], o[2]*c - o[0]*s})
}

// turn moves its rotation a part of the way to where it now flies.
func (t *Projectile) turn() {
	to := flightRotation(t.vel)
	t.rot.Pitch += wrapDegrees(to.Pitch-t.rot.Pitch) * projectileTurn
	t.rot.Yaw += wrapDegrees(to.Yaw-t.rot.Yaw) * projectileTurn
	t.rot.HeadYaw = t.rot.Yaw
}

// inWater reports whether the block at pos holds water.
func (s *Host) inWater(r *world.Region, pos mgl32.Vec3) bool {
	b, ok := r.Block(floorPos(pos), 0)
	return ok && (s.reg.Blocks.Name(b) == nameWater || s.reg.Blocks.Name(b) == nameFlowingWater)
}

// checkSeparated is BDS's look for its owner: once its path grown by a block holds no part
// of its owner, it may hit its owner.
// think finds what this tick's flight hits first, the costly search through blocks and
// players (see think.go). The hit itself is applied in Input.
func (t *Projectile) think(e *world.Entity) {
	r := e.Region()
	if r == nil || t.gone || t.waiting || t.stuck {
		return
	}
	from := e.Position()
	to := from.Add(t.vel)
	t.checkSeparated(r, from, to)
	// Input counts the age up before the search, which reads it.
	t.age++
	t.plan, t.planKind = t.firstHit(r, from, to)
	t.age--
	t.planned = true
}

func (t *Projectile) checkSeparated(r *world.Region, from, to mgl32.Vec3) {
	if t.separated {
		return
	}
	if t.owner == nil || t.owner.entity == nil || t.owner.entity.Region() != r {
		t.separated = true
		return
	}
	lo, hi, ok := t.owner.BlastBox()
	if !ok {
		t.separated = true
		return
	}
	pathLo, pathHi := minVec(from, to), maxVec(from, to)
	grow := mgl32.Vec3{1, 1, 1}
	t.separated = !overlaps(pathLo.Sub(grow), pathHi.Add(grow), lo, hi)
}

func minVec(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{min(a[0], b[0]), min(a[1], b[1]), min(a[2], b[2])}
}

func maxVec(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{max(a[0], b[0]), max(a[1], b[1]), max(a[2], b[2])}
}

// projectileHitKind is what a projectile hit.
type projectileHitKind uint8

const (
	projectileHitNone projectileHitKind = iota
	projectileHitBlock
	projectileHitEntity
)

// projectileHit is where a flight hit something.
type projectileHit struct {
	pos    mgl32.Vec3
	block  cube.Pos
	player *Player
	// mob is the mob hit, when a mob and not a player was hit.
	mob *Mob
	// entity is the entity hit.
	entity *world.Entity
	// normal is the hit block face's outward direction, zero if not worked out.
	normal mgl32.Vec3
}

// faceNormal is the face of the box lo to hi that a flight along vel entered at pos.
func faceNormal(pos, lo, hi, vel mgl32.Vec3) mgl32.Vec3 {
	const edge = 1e-4
	var n mgl32.Vec3
	for a := range 3 {
		switch {
		case vel[a] > 0 && pos[a]-lo[a] < edge:
			n[a] = -1
			return n
		case vel[a] < 0 && hi[a]-pos[a] < edge:
			n[a] = 1
			return n
		}
	}
	return n
}

// firstHit is the nearest thing along from to to.
func (t *Projectile) firstHit(r *world.Region, from, to mgl32.Vec3) (projectileHit, projectileHitKind) {
	best, kind := projectileHit{}, projectileHitNone
	limit := float64(2)
	if at, block, lo, hi, ok := t.s.clipBlocks(r, from, to); ok {
		pos := clampToBox(from.Add(to.Sub(from).Mul(float32(at))), lo, hi)
		best, kind, limit = projectileHit{pos: pos, block: block, normal: faceNormal(pos, lo, hi, to.Sub(from))}, projectileHitBlock, at
	}
	reach := min(max((float32(t.age)-projectileReachFrom)*projectileReachStep, 0), projectileReachMax)
	lo, hi := minVec(from, to), maxVec(from, to)
	t.found = r.AppendEntitiesIn(t.found[:0], lo[0]-projectileScan, lo[2]-projectileScan, hi[0]+projectileScan, hi[2]+projectileScan)
	grow := mgl32.Vec3{reach, reach, reach}
	for _, o := range t.found {
		if t.wasPierced(o) {
			continue
		}
		hit := projectileHit{entity: o}
		switch target := o.Owner().(type) {
		case *Player:
			if target.dead() || target == t.owner && !t.separated || target.gameMode == GameModeSpectator {
				continue
			}
			hit.player = target
		case *Mob:
			if target.Dead() {
				continue
			}
			hit.mob = target
		default:
			continue
		}
		boxLo, boxHi, has := o.Owner().(Living).BlastBox()
		if !has {
			continue
		}
		at, hits := segmentEntersBox(from, to, boxLo.Sub(grow), boxHi.Add(grow))
		if hits && at < limit {
			limit, kind = at, projectileHitEntity
			hit.pos = from.Add(to.Sub(from).Mul(float32(at)))
			best = hit
		}
	}
	return best, kind
}

// segmentEntersBox is how far along from to to it enters the box, 0 to 1.
func segmentEntersBox(from, to, lo, hi mgl32.Vec3) (float64, bool) {
	t0, t1 := 0.0, 1.0
	for n := range 3 {
		a, d := float64(from[n]), float64(to[n]-from[n])
		l, h := float64(lo[n]), float64(hi[n])
		if d == 0 {
			if a < l || a > h {
				return 0, false
			}
			continue
		}
		e0, e1 := (l-a)/d, (h-a)/d
		if e0 > e1 {
			e0, e1 = e1, e0
		}
		t0, t1 = max(t0, e0), min(t1, e1)
		if t0 > t1 {
			return 0, false
		}
	}
	return t0, true
}

// clampToBox keeps a point on the box it hit: rounding can leave it just outside, and a stuck
// projectile must still count as in its block.
func clampToBox(p, lo, hi mgl32.Vec3) mgl32.Vec3 {
	for n := range 3 {
		p[n] = min(max(p[n], lo[n]), hi[n])
	}
	return p
}

// insideBox is BDS's test of whether a stuck projectile's block still holds it (0x7745da0): the
// faces count, since that is where it hit.
func insideBox(p, lo, hi mgl32.Vec3) bool {
	for n := range 3 {
		if p[n] < lo[n] || p[n] > hi[n] {
			return false
		}
	}
	return true
}

// clipBlocks walks the blocks from from to to, and says how far along it first enters a
// block's collision box, which block and which box. A block not loaded stops it.
func (s *Host) clipBlocks(r *world.Region, from, to mgl32.Vec3) (float64, cube.Pos, mgl32.Vec3, mgl32.Vec3, bool) {
	var pos, end, step [3]int
	var next, delta [3]float64
	for n := range 3 {
		a, d := float64(from[n]), float64(to[n]-from[n])
		pos[n], end[n] = itemFloor(from[n]), itemFloor(to[n])
		switch {
		case d > 0:
			step[n], delta[n], next[n] = 1, 1/d, (float64(pos[n]+1)-a)/d
		case d < 0:
			step[n], delta[n], next[n] = -1, -1/d, (float64(pos[n])-a)/d
		default:
			next[n] = math.Inf(1)
		}
	}
	for range projectileClipLimit {
		at := cube.Pos{pos[0], pos[1], pos[2]}
		b, ok := r.Block(at, 0)
		if !ok {
			return 0, at, mgl32.Vec3{}, mgl32.Vec3{}, false
		}
		best, found := 2.0, false
		var bestLo, bestHi mgl32.Vec3
		for _, box := range s.boxesAt(r, at, b) {
			lo := mgl32.Vec3{float32(at[0]) + box[0], float32(at[1]) + box[1], float32(at[2]) + box[2]}
			hi := mgl32.Vec3{float32(at[0]) + box[3], float32(at[1]) + box[4], float32(at[2]) + box[5]}
			if v, hits := segmentEntersBox(from, to, lo, hi); hits && v < best {
				best, found, bestLo, bestHi = v, true, lo, hi
			}
		}
		if found {
			return best, at, bestLo, bestHi, true
		}
		if pos == end {
			return 0, at, mgl32.Vec3{}, mgl32.Vec3{}, false
		}
		n := 0
		if next[1] < next[n] {
			n = 1
		}
		if next[2] < next[n] {
			n = 2
		}
		if next[n] > 1 {
			return 0, at, mgl32.Vec3{}, mgl32.Vec3{}, false
		}
		pos[n] += step[n]
		next[n] += delta[n]
	}
	return 0, cube.Pos{}, mgl32.Vec3{}, mgl32.Vec3{}, false
}

// apply is what a hit does, BDS's on hit subcomponents as far as built.
func (t *Projectile) apply(r *world.Region, e *world.Entity, hit projectileHit, kind projectileHitKind) {
	t.hitSound(r, hit.pos)
	if kind == projectileHitEntity {
		if hit.mob != nil {
			t.hitMob(hit.mob)
		} else {
			t.hitPlayer(hit.player)
		}
		// BDS's Piercing (0xC9AF5C0 gives the arrow the level and level + 1 hits): it flies
		// on through the entity.
		if t.pierce > 0 {
			t.pierce--
			t.pierced = append(t.pierced, hit.entity)
			return
		}
		if t.spec.DestroyOnHit {
			t.remove(e)
		}
	} else {
		t.s.HitTarget(r, hit.block, hit.pos, t.spec.Stick)
		t.tridentOnRod(r, hit.block)
		if t.spec.Stick {
			t.stickIn(e, hit)
			return
		}
	}
	t.hitParticles(r, hit.pos, kind)
	if t.isPotion() {
		t.potionHit(r, e, hit, kind)
	}
	if t.spec.GrantXP {
		t.grantXP(r, hit.pos)
	}
	if t.spec.WindBurst {
		t.windBurst(r, hit, kind)
		t.remove(e)
		return
	}
	if t.spec.TeleportOwner {
		t.teleportOwner(r, hit.pos)
	}
	if t.spec.RemoveOnHit {
		t.remove(e)
	}
}

// hitSound plays its sound of hitting something.
func (t *Projectile) hitSound(r *world.Region, at mgl32.Vec3) {
	if t.spec.HitSound != "" {
		playSound(r, floorPos(at), soundEvent{name: t.spec.HitSound, pos: at, extra: -1, entity: t.spec.ID}, nil)
	}
}

// remove takes it out of the world.
func (t *Projectile) remove(e *world.Entity) {
	t.gone = true
	e.Remove()
}

// stickIn leaves it where it hit.
func (t *Projectile) stickIn(e *world.Entity, hit projectileHit) {
	t.vel = mgl32.Vec3{}
	t.stuck, t.stuckAt, t.stuckTicks, t.shake, t.critical = true, hit.block, 0, t.spec.ShakeTicks, false
	t.stuckMoveDue = true
	// BDS's on ground flag is what stuck is; its moves carry it to the client.
	e.Apply(world.Movement{Pos: hit.pos, Rot: t.rot, OnGround: true, Tick: e.Tick()})
	t.sendFlags(e)
}

// tickStuck is a stuck one's tick: it falls again when its block no longer holds it, is
// gone after projectileLife, and a player touching it may pick it up.
func (t *Projectile) tickStuck(r *world.Region, e *world.Entity) {
	pos := e.Position()
	if !t.heldBy(r, pos) {
		t.stuck, t.stuckTicks = false, 0
		e.Apply(world.Movement{Pos: pos, Rot: t.rot, Tick: e.Tick()})
		return
	}
	t.stuckTicks++
	if t.stuckTicks >= projectileLife {
		t.remove(e)
		return
	}
	if t.stuckTicks < t.spec.ShakeTicks {
		return
	}
	t.found = r.AppendEntitiesIn(t.found[:0], pos[0]-projectileScan, pos[2]-projectileScan, pos[0]+projectileScan, pos[2]+projectileScan)
	for _, o := range t.found {
		p, ok := o.Owner().(*Player)
		if !ok || p.dead() {
			continue
		}
		lo, hi, has := p.BlastBox()
		if has && overlaps(pos, pos, lo.Sub(arrowTouchReach), hi.Add(arrowTouchReach)) && t.pickedUpBy(r, e, p) {
			return
		}
	}
}

// arrowTouchReach is how far around a player's box it picks up arrows.
var arrowTouchReach = mgl32.Vec3{1, 0.5, 1}

// heldBy reports whether its block still has a collision box where it sits.
func (t *Projectile) heldBy(r *world.Region, pos mgl32.Vec3) bool {
	b, ok := r.Block(t.stuckAt, 0)
	if !ok {
		return true
	}
	for _, box := range t.s.boxesAt(r, t.stuckAt, b) {
		at := t.stuckAt
		lo := mgl32.Vec3{float32(at[0]) + box[0], float32(at[1]) + box[1], float32(at[2]) + box[2]}
		hi := mgl32.Vec3{float32(at[0]) + box[3], float32(at[1]) + box[4], float32(at[2]) + box[5]}
		if insideBox(pos, lo, hi) {
			return true
		}
	}
	return false
}

// pickedUpBy is a player touching a stuck one; true if it is gone.
func (t *Projectile) pickedUpBy(r *world.Region, e *world.Entity, p *Player) bool {
	switch t.pickup {
	case arrowPickupCreative:
		if !p.creative() {
			return false
		}
		pitch := int32((((rand.Float32()-rand.Float32())*popPitchSpread + 1) * popPitchScale)) //nolint:gosec // a pitch, not a secret
		levelEvent(r, floorPos(e.Position()), e.Position(), levelEventPop, pitch)
	default:
		item := t.item
		if item.Empty() {
			item = ItemStack{Name: nameArrow, Count: 1}
		}
		if left := p.Give(t.s.freshStack(item)); !left.Empty() {
			return false
		}
		e.Send(&packet.TakeItemActor{ItemEntityRuntimeID: e.RuntimeID(), TakerEntityRuntimeID: p.runtimeID})
	}
	t.remove(e)
	return true
}

// projectileDamage is BDS's impact damage (0x714a880) on what it may damage: the base, its
// speed times the multiplier, the bow's Power, rounded up if the definition says, then a
// critical hit's random extra.
func projectileDamage(spec ProjectileSpec, speed float32, power int32, critical bool) float32 {
	damage := spec.DamageMin + rand.Float32()*(spec.DamageMax-spec.DamageMin) + speed*spec.PowerMultiplier //nolint:gosec // damage, not a secret
	if power > 0 {
		damage *= float32(power)*powerPerLevel + powerBase
	}
	if spec.CeilDamage {
		damage = float32(math.Ceil(float64(damage)))
	}
	if critical {
		damage += rand.Float32() * (critShare*damage + critFlat) //nolint:gosec // damage, not a secret
	}
	return damage
}

// hitPlayer hurts and pushes a player. A definition that damages only one family, as a
// snowball does blazes, does no damage to a player but still pushes. The push is the first
// push of a melee hit along its flight, with Punch's extra; BDS's knockback for this was
// not read line by line. A hit in a game with no pvp does nothing to a player shot by one.
func (t *Projectile) hitPlayer(target *Player) {
	if t.owner != nil && t.owner != target && !t.s.level.PVP {
		return
	}
	var damage float32
	if t.spec.DamageOnly == "" {
		damage = projectileDamage(t.spec, t.vel.Len(), t.power, t.critical)
	}
	if r := target.entity.Region(); r != nil && damage > 0 {
		damage += t.tridentHit(r, target, target.entity.Position())
	}
	g := t.s.game
	hurt := damage > 0 && g.sys.Hurt(target, damage, DamageSource{Cause: causeProjectile, Attacker: t.owner})
	if hurt && t.spec.StopOnHurt {
		t.tridentHurt()
	}
	if damage > 0 && !hurt && t.spec.Bounce {
		t.vel = t.vel.Mul(projectileBounce)
		t.spec.DestroyOnHit = false
		return
	}
	if (hurt || damage <= 0) && t.spec.Knockback {
		target.projectileKnockback(t.vel, t.punch)
	}
}

// hitMob hurts a mob as hitPlayer hurts a player: the projectile's damage (a definition that
// damages only one family damages only mobs of that family), with its shooter as the
// attacker, which also pushes the mob away from the shooter. BDS's knockback along the flight
// and Punch on mobs were not read; the hit's push stands in.
func (t *Projectile) hitMob(target *Mob) {
	var damage float32
	if t.spec.DamageOnly == "" || target.hasFamily(t.spec.DamageOnly) {
		damage = projectileDamage(t.spec, t.vel.Len(), t.power, t.critical)
	}
	if r := target.entity.Region(); r != nil && damage > 0 {
		damage += t.tridentHit(r, target, target.entity.Position())
	}
	hurt := damage > 0 && target.TakeDamage(damage, DamageSource{Cause: causeProjectile, Attacker: t.owner})
	if hurt && t.spec.StopOnHurt {
		t.tridentHurt()
	}
	if damage > 0 && !hurt && t.spec.Bounce {
		t.vel = t.vel.Mul(projectileBounce)
		t.spec.DestroyOnHit = false
	}
}

// projectileKnockback pushes the player along a flight.
func (p *Player) projectileKnockback(flight mgl32.Vec3, punch float32) {
	if p.entity == nil {
		return
	}
	resist := max(1-p.attributes.get(attrKnockbackResistance), 0) * p.Tuning(TuneKnockbackTakenScale)
	vel := p.knockbackStep(p.move.velocity, flight[0], flight[2], p.Tuning(TuneKnockbackPush)*resist, p.Tuning(TuneKnockbackLift)*resist)
	if punch > 0 {
		vel = p.knockbackStep(vel, flight[0], flight[2], punch*p.Tuning(TuneKnockbackExtraPush)*resist, punch*p.Tuning(TuneKnockbackExtraLift)*resist)
	}
	p.move.velocity = vel
	p.send(&packet.SetActorMotion{EntityRuntimeID: p.runtimeID, Velocity: vel, Tick: p.entity.Tick()})
	p.flush()
}

// teleportOwner moves who threw it to where it landed, with BDS's fall damage. A thrower in
// another region by now stays where it is.
func (t *Projectile) teleportOwner(r *world.Region, at mgl32.Vec3) {
	p := t.owner
	if p == nil || p.dead() || p.entity == nil || p.entity.Region() != r {
		return
	}
	eyes := at.Add(mgl32.Vec3{0, eyeHeight, 0})
	rot := p.entity.Rotation()
	if !p.entity.Apply(world.Movement{Pos: eyes, Rot: rot, Tick: p.entity.Tick()}) {
		return
	}
	p.move.fall = 0
	p.send(&packet.MovePlayer{
		EntityRuntimeID: p.runtimeID, Position: eyes, Pitch: rot.Pitch, Yaw: rot.Yaw, HeadYaw: rot.HeadYaw,
		Mode: packet.MoveModeTeleport, Tick: p.entity.Tick(),
		// The client disconnects on a teleport without its cause (see teleportFeet). BDS's
		// projectile teleport (sub_714F4B0) sends cause Projectile with the projectile's
		// actor type as the source; that number is not looked up yet, so the source is 0.
		TeleportData: protocol.Option(protocol.TeleportData{TeleportCause: packet.TeleportCauseProjectile}),
	})
	p.flush()
	if p.s.fallHurts() {
		p.s.game.sys.Hurt(p, pearlDamage, DamageSource{Cause: DamageFall})
	}
}

func (t *Projectile) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if t.gone {
		return chunk.Entity{}, false
	}
	data := maps.Clone(t.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos := e.Position()
	data["identifier"] = t.spec.ID
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Motion"] = []float32{t.vel[0], t.vel[1], t.vel[2]}
	data["Rotation"] = []float32{t.rot.Yaw, t.rot.Pitch}
	var stuck uint8
	if t.stuck {
		stuck = 1
	}
	data[projectileKeyInGround] = stuck
	data[projectileKeyPickup] = uint8(t.pickup)
	t.saveTrident(data)
	if t.isPotion() {
		data[projectileKeyPotion] = t.potion
	}
	data[projectileKeyStuckAt] = []int32{int32(t.stuckAt[0]), int32(t.stuckAt[1]), int32(t.stuckAt[2])} //nolint:gosec // block positions fit
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// isProjectile reports whether a saved entity's identifier is one this runs.
func isProjectile(id string) bool {
	return id == nameSnowball || id == nameEgg || id == nameEnderPearl || id == nameArrow || id == nameThrownTrident || id == nameWindChargeProjectile ||
		id == nameSplashPotion || id == nameLingeringPotion || id == nameXPBottle
}

// isPotion reports a thrown potion.
func (t *Projectile) isPotion() bool {
	return t.spec.ID == nameSplashPotion || t.spec.ID == nameLingeringPotion
}

// loadProjectile brings back a saved one. Who shot it is not kept, so a bow's enchantments
// and owner are gone, as they are for a pearl that lands after a restart.
func (s *Host) loadProjectile(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity, id string) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	group := ""
	if id == nameArrow {
		group = arrowGroupPlayer
	}
	spec, ok := s.projectileSpec(id, group)
	if !ok {
		return false
	}
	t := &Projectile{s: s, spec: spec, saved: saved.Data, separated: true}
	if v, isList := floats(saved.Data["Motion"]); isList && len(v) == 3 {
		t.vel = mgl32.Vec3{v[0], v[1], v[2]}
	}
	if v, isList := floats(saved.Data["Rotation"]); isList && len(v) == 2 {
		t.rot = world.Rotation{Yaw: v[0], HeadYaw: v[0], Pitch: v[1]}
	}
	if v, isByte := saved.Data[projectileKeyInGround].(uint8); isByte {
		t.stuck = v != 0
	}
	if v, isByte := saved.Data[projectileKeyPickup].(uint8); isByte {
		t.pickup = arrowPickup(v)
	}
	if v, isList := saved.Data[projectileKeyStuckAt].([]int32); isList && len(v) == 3 {
		t.stuckAt = cube.Pos{int(v[0]), int(v[1]), int(v[2])}
	}
	t.loadTrident(saved.Data)
	if v, isShort := saved.Data[projectileKeyPotion].(int16); isShort {
		t.potion = v
	}
	t.entity = r.AddLoadedEntity(at, t.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}, t.rot))
	return true
}

// lookDirection is where a rotation looks.
func lookDirection(rot world.Rotation) mgl32.Vec3 {
	yaw, pitch := float64(rot.Yaw)*math.Pi/180, float64(rot.Pitch)*math.Pi/180
	return mgl32.Vec3{float32(-math.Sin(yaw) * math.Cos(pitch)), float32(-math.Sin(pitch)), float32(math.Cos(yaw) * math.Cos(pitch))}
}

// throwable is the projectile a held item throws, empty if it is none.
func throwable(item string) string {
	switch item {
	case nameSnowball, nameEgg, nameEnderPearl:
		return item
	case nameWindCharge:
		return nameWindChargeProjectile
	case nameXPBottleItem:
		return nameXPBottle
	case nameSplashPotion, nameLingeringPotion:
		return item
	}
	return ""
}

// throwItem is throwing what is held: the projectile from the player's eyes, the sound and
// one item gone unless in creative. The eggs' chicks wait for mobs. False if the item is
// not one, or a pearl too soon after the last.
func (p *Player) throwItem(held ItemStack) bool {
	id := throwable(held.Name)
	if id == "" || p.entity == nil {
		return false
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	now := r.CurrentTick()
	// A click on a block and a click on the air may both arrive for one throw.
	if now == p.thrownAt {
		return true
	}
	if id == nameEnderPearl {
		if now < p.pearlReady {
			return false
		}
		p.pearlReady = now + pearlCooldown
	}
	if category, ticks, has := throwableCooldown(held.Name); has {
		if p.Cooldown(category) > 0 {
			return false
		}
		p.StartCooldown(category, ticks)
	}
	spec, ok := p.s.projectileSpec(id, "")
	if !ok {
		return false
	}
	p.thrownAt = now
	eyes, rot := p.entity.Position(), p.entity.Rotation()
	t := p.s.SpawnProjectile(r.Dimension(), spec, projectileEyes(spec, eyes, rot), lookDirection(aimRotation(rot, spec.AngleOffset)), spec.Power, p)
	t.potion = held.Meta
	// BDS's throwable items (0xC985510 and the like) play it at the drop attach point.
	playSound(r, p.blockPos(), soundEvent{name: packet.SoundEventThrow, pos: p.AttachPos(AttachDrop), extra: -1, entity: "minecraft:player"}, nil)
	if !p.creative() {
		p.useHeld()
	}
	return true
}

// bowPull is how far a bow is drawn after ticks, BDS's 0xc8d71c0: 0 to 1.
func bowPull(ticks int32) float32 {
	s := float32(ticks) / bowPullSpan
	return min((s*s+s+s)/3, 1)
}

// arrowSlot is where the arrows a bow would use are: the offhand first, then the inventory
// in order. -1 for the offhand, and false with none.
func (inv *playerItems) arrowSlot() (int, bool) {
	if inv.offhand[0].Name == nameArrow && inv.offhand[0].Count > 0 {
		return -1, true
	}
	for i := range inv.main {
		if inv.main[i].Name == nameArrow && inv.main[i].Count > 0 {
			return i, true
		}
	}
	return 0, false
}

// mayDrawBow is BDS's rule: anyone in creative, with Infinity, or with arrows.
func (p *Player) mayDrawBow(held ItemStack) bool {
	if p.creative() || heldEnchant(held, enchantInfinity) > 0 {
		return true
	}
	_, ok := p.inventory.arrowSlot()
	return ok
}

// heldEnchant is the level of an enchantment on a stack.
func heldEnchant(s ItemStack, id int16) int32 {
	var level int32
	enchantLevels(s, func(e, l int16) {
		if e == id {
			level = int32(l)
		}
	})
	return level
}

// releaseBow is letting go of a drawn bow, BDS's 0xc8d71c0: too short a draw does nothing;
// else the arrow takes the draw's speed and the bow's enchantments, the bow wears, and one
// arrow goes, unless the shooter has Infinity or is in creative.
func (p *Player) releaseBow(held ItemStack, pulled int32) {
	pull := bowPull(pulled)
	if pull < bowMinPull || p.entity == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	spec, ok := p.s.projectileSpec(nameArrow, arrowGroupPlayer)
	if !ok {
		return
	}
	infinity := heldEnchant(held, enchantInfinity) > 0
	creative := p.creative()
	slot, hasArrow := p.inventory.arrowSlot()
	if !hasArrow && !creative && !infinity {
		return
	}
	eyes, rot := p.entity.Position(), p.entity.Rotation()
	t := p.s.SpawnProjectile(r.Dimension(), spec, projectileEyes(spec, eyes, rot), lookDirection(rot), spec.Power*pull, p)
	t.critical = pull == 1
	t.power = heldEnchant(held, enchantPower)
	t.punch = float32(heldEnchant(held, enchantPunch)) * punchPerLevel
	if infinity || creative {
		t.pickup = arrowPickupCreative
	}
	// BDS's bow (0xC8D71C0) plays it at the weapon attach point.
	playSound(r, p.blockPos(), soundEvent{name: packet.SoundEventBow, pos: p.AttachPos(AttachWeapon), extra: -1, entity: "minecraft:player"}, nil)
	if creative {
		return
	}
	p.hurtHeld(1)
	if !hasArrow || infinity {
		return
	}
	if slot < 0 {
		p.inventory.offhand[0].Count--
		if p.inventory.offhand[0].Count <= 0 {
			p.inventory.offhand[0] = ItemStack{}
		}
		p.sendOffhand()
		p.sendEquipment()
	} else {
		s := &p.inventory.main[slot]
		s.Count--
		if s.Count <= 0 {
			*s = ItemStack{}
		}
		p.sendSlot(slot)
	}
	p.flush()
}

// dispenseProjectile is a dispenser shooting what a dispenser shoots, 0xc8e4c50: from at,
// along its face and a little up, with what the item's definition says. False if the item
// is not one. Who shot it is no one, so an arrow may be picked up.
func (s *Host) dispenseProjectile(r *world.Region, pos cube.Pos, at, dir mgl32.Vec3, item string) bool {
	id := throwable(item)
	if item == nameArrow {
		id = nameArrow
	}
	// Dispensed potions wait: this is not given the item's meta, the potion's ID.
	if id == "" || id == nameEnderPearl || id == nameSplashPotion || id == nameLingeringPotion {
		return false
	}
	spec, ok := s.projectileSpec(id, "")
	if !ok {
		return false
	}
	s.SpawnProjectile(r.Dimension(), spec, at, dir.Add(mgl32.Vec3{0, dispenseLift, 0}), spec.Power, nil)
	levelEvent(r, pos, at, levelEventDispenseProjectile, dispenseProjectilePitch)
	return true
}

// wasPierced is whether it already flew through o.
func (t *Projectile) wasPierced(o *world.Entity) bool {
	for _, p := range t.pierced {
		if p == o {
			return true
		}
	}
	return false
}
