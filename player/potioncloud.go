package player

import (
	"maps"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/mcdb"
)

// Area effect clouds, the lingering potion's cloud, BDS 1.26.60's AreaEffectCloud (vtable
// 0xFAF9BD0, constructor 0xC07F480, tick 0xC07FE50, save 0xC081340, load 0xC082560). A cloud
// lasts its duration from its spawn tick and is gone sooner once its radius is below half a
// block. Every fifth tick of its life it gives its effects to the players within its radius
// of its centre that it has not touched for its reapplication delay, at a quarter of their
// duration (instant ones at half strength), and its radius and duration change by their
// on use amounts for each. It is saved with the chunk, so it stays through a restart.

const (
	nameAreaEffectCloud = "minecraft:area_effect_cloud"

	// cloudMinRadius is the radius below which a cloud is gone.
	cloudMinRadius = 0.5
	// cloudEvery is how often a cloud looks for players, in ticks of its life.
	cloudEvery = 5
	// cloudDurationScale is what a cloud gives of an effect's duration, and cloudInstantScale
	// the strength of an instant one.
	cloudDurationScale = 0.25
	cloudInstantScale  = 0.5
	// cloudTicksPerSecond turns spawn_aoe_cloud's duration, in seconds, into ticks.
	cloudTicksPerSecond = 20
	// cloudParticleMobSpellAmbient is BDS's particle ID for mobspellambient, the
	// definition's default and the constructor's.
	cloudParticleMobSpellAmbient = 35
	// cloudChangeOnPickup is the constructor's radius change when a bottle takes from it.
	cloudChangeOnPickup = -0.5

	cloudTagDuration       = "Duration"
	cloudTagReapplication  = "ReapplicationDelay"
	cloudTagDurationOnUse  = "DurationOnUse"
	cloudTagInitialRadius  = "InitialRadius"
	cloudTagRadiusOnUse    = "RadiusOnUse"
	cloudTagRadiusPerTick  = "RadiusPerTick"
	cloudTagRadius         = "Radius"
	cloudTagOwner          = "OwnerId"
	cloudTagPotion         = "PotionId"
	cloudTagParticle       = "ParticleId"
	cloudTagEffects        = "mobEffects"
	cloudTagColour         = "ParticleColor"
	cloudTagSpawnTick      = "SpawnTick"
	cloudTagChangeOnPickup = "RadiusChangeOnPickup"
	cloudTagPickupCount    = "PickupCount"
)

// CloudSpec is a definition's spawn_aoe_cloud on hit subcomponent (parsed as 0x714E6A0).
type CloudSpec struct {
	// Duration is in ticks; Radius is where it starts, RadiusOnUse what each player it
	// touches takes from it.
	Duration            int32
	Radius, RadiusOnUse float32
	// ReapplicationDelay is how many ticks before it touches a player again.
	ReapplicationDelay int32
	// Particle is the particle it shows, AffectOwner whether it touches who threw it.
	Particle    int32
	AffectOwner bool
}

// AreaEffectCloud is a lingering potion's cloud. Runs: owner of its entity.
type AreaEffectCloud struct {
	s      *Host
	entity *world.Entity
	saved  map[string]any
	gone   bool

	potion  int16
	effects []ActiveEffect
	colour  uint32
	// duration is its life in ticks (EffectInfinite for endless); durationOnUse what each
	// player touched adds to it (0 for nothing).
	duration, durationOnUse int32
	// radius grows by perTick each tick of its life; radiusOnUse is added for each player
	// touched.
	radius, radiusOnUse, perTick float32
	reapplication                int32
	particle                     int32
	changeOnPickup               float32
	pickupCount                  int32
	owner                        int64
	affectOwner                  bool
	// spawnTick is the tick it began, 0 until its first tick.
	spawnTick int64
	// victims holds the players touched, by unique ID, until the tick of its life they may
	// be touched again.
	victims map[int64]int64
	found   []*world.Entity
}

// Entity returns its entity.
func (c *AreaEffectCloud) Entity() *world.Entity { return c.entity }

// Effects returns the effects it gives.
func (c *AreaEffectCloud) Effects() []ActiveEffect { return append([]ActiveEffect(nil), c.effects...) }

// spawnCloud is BDS's spawn_aoe_cloud (0x714EA60) where a lingering potion lands: a potion with
// no effects other than water shows only its splash; any other leaves a cloud of its effects,
// owned by the thrower, which waits its reapplication delay between touches of a player
// unless its first effect is instant.
func (t *Projectile) spawnCloud(r *world.Region, at mgl32.Vec3) {
	spec := t.spec.Cloud
	pot, _ := t.s.Potion(int32(t.potion))
	if len(pot.Effects) == 0 && t.potion != potionWater {
		t.splashShow(r, at, waterColour, false)
		return
	}
	c := &AreaEffectCloud{
		s: t.s, potion: t.potion, effects: append([]ActiveEffect(nil), pot.Effects...), colour: t.s.potionColour(pot.Effects),
		duration: spec.Duration, radius: max(spec.Radius, 0), radiusOnUse: spec.RadiusOnUse,
		particle: spec.Particle, changeOnPickup: cloudChangeOnPickup, affectOwner: spec.AffectOwner,
		reapplication: spec.ReapplicationDelay, victims: map[int64]int64{},
	}
	if t.owner != nil && t.owner.entity != nil {
		c.owner = t.owner.entity.UniqueID()
	}
	if len(pot.Effects) > 0 {
		if def, ok := t.s.game.effects[pot.Effects[0].ID]; ok && def.Instant {
			c.reapplication = 0
		}
	}
	// BDS's setup (0xC07FC60): the radius grows by the on use amount over the cloud's life.
	if c.duration > 0 {
		c.perTick = -c.radiusOnUse / float32(c.duration)
	}
	c.entity = r.Dimension().AddEntity(c.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), t.s.runtimeIDs.Add(1), at)) //nolint:gosec // an ID, not a secret
	t.splashShow(r, at, c.colour, false)
}

func (c *AreaEffectCloud) config(uid int64, rid uint64, pos mgl32.Vec3) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uid, RuntimeID: rid, Pos: pos,
		Owner: c, Spawn: c.spawnPackets, Input: c.tick, Save: c.saveEntity,
	}
}

// metadata is its actor data, as BDS syncs it.
func (c *AreaEffectCloud) metadata() protocol.EntityMetadata {
	return protocol.EntityMetadata{
		protocol.EntityDataKeyEffectColor:  int32(c.colour), //nolint:gosec // ARGB, as BDS sends it
		protocol.EntityDataKeyAuxValueData: c.potion, protocol.EntityDataKeyDataRadius: c.radius,
		protocol.EntityDataKeyDataParticle: c.particle, protocol.EntityDataKeyDataDuration: c.duration,
		protocol.EntityDataKeyDataChangeRate: c.perTick, protocol.EntityDataKeyDataChangeOnPickup: c.changeOnPickup,
		protocol.EntityDataKeyDataPickupCount: c.pickupCount,
	}
}

func (c *AreaEffectCloud) spawnPackets(e *world.Entity) []packet.Packet {
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: nameAreaEffectCloud,
		Position: e.Position(), EntityMetadata: c.metadata(),
	}}
}

// currentRadius is BDS's radius (0xC07F7F0): the radius it was given, grown by its change a
// tick for each tick of its life.
func (c *AreaEffectCloud) currentRadius(age int64) float32 {
	return max(c.radius+float32(age)*c.perTick, 0)
}

// remove takes it out of the world.
func (c *AreaEffectCloud) remove() {
	c.gone = true
	c.entity.Remove()
}

// tick is BDS's AreaEffectCloud tick.
func (c *AreaEffectCloud) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || c.gone {
		return
	}
	now := r.CurrentTick()
	if c.spawnTick <= 0 {
		c.spawnTick = now
		return
	}
	age := max(now-c.spawnTick, 0)
	if c.duration != EffectInfinite && int64(c.duration) < age || c.currentRadius(age) < cloudMinRadius {
		c.remove()
		return
	}
	if age%cloudEvery != 0 {
		return
	}
	for id, until := range c.victims {
		if age >= until {
			delete(c.victims, id)
		}
	}
	if len(c.effects) == 0 {
		clear(c.victims)
		return
	}
	c.touch(r, e, age)
}

// touch gives its effects to the players in its reach, as BDS's tick does once every
// cloudEvery ticks. Passing the owner when it may not touch it ends the whole pass, as in BDS.
func (c *AreaEffectCloud) touch(r *world.Region, e *world.Entity, age int64) {
	radius := c.currentRadius(age)
	pos := e.Position()
	lo := mgl32.Vec3{pos[0] - radius, pos[1] - radius/2, pos[2] - radius}
	hi := mgl32.Vec3{pos[0] + radius, pos[1] + radius/2, pos[2] + radius}
	c.found = r.AppendEntitiesIn(c.found[:0], lo[0], lo[2], hi[0], hi[2])
	g := c.s.game
	for _, o := range c.found {
		target, ok := o.Owner().(*Player)
		if !ok || target.dead() || target.gameMode == GameModeSpectator {
			continue
		}
		boxLo, boxHi, has := target.BlastBox()
		if !has || !overlaps(lo, hi, boxLo, boxHi) {
			continue
		}
		uid := o.UniqueID()
		if !c.affectOwner && uid == c.owner {
			return
		}
		if _, touched := c.victims[uid]; touched {
			continue
		}
		if o.Position().Sub(pos).LenSqr() > radius*radius {
			continue
		}
		c.victims[uid] = age + int64(c.reapplication)
		for _, eff := range c.effects {
			def, known := g.effects[eff.ID]
			if !known {
				continue
			}
			if def.Instant {
				target.ApplyInstantEffect(eff.ID, eff.Amplifier, cloudInstantScale)
				continue
			}
			ticks := eff.Duration
			if ticks != EffectInfinite {
				ticks = max(int32(float32(ticks)*cloudDurationScale), 0)
			}
			target.GiveEffect(ActiveEffect{ID: eff.ID, Amplifier: eff.Amplifier, Duration: ticks})
		}
		if c.radiusOnUse != 0 {
			c.radius = max(c.radius+c.radiusOnUse, 0)
			if c.currentRadius(age) < cloudMinRadius {
				c.remove()
				return
			}
			e.Send(actorData(e.RuntimeID(), protocol.EntityMetadata{protocol.EntityDataKeyDataRadius: c.radius}))
		}
		if c.durationOnUse != EffectInfinite && c.durationOnUse != 0 {
			if c.duration != EffectInfinite {
				c.duration += c.durationOnUse
				if c.duration < EffectInfinite || c.duration == 0 {
					c.remove()
					return
				}
			}
			e.Send(actorData(e.RuntimeID(), protocol.EntityMetadata{protocol.EntityDataKeyDataDuration: c.duration}))
		}
	}
}

func (c *AreaEffectCloud) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if c.gone {
		return chunk.Entity{}, false
	}
	data := maps.Clone(c.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos := e.Position()
	data["identifier"] = nameAreaEffectCloud
	data["UniqueID"] = e.UniqueID()
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Rotation"] = []float32{0, 0}
	data["Motion"] = []float32{0, 0, 0}
	data[cloudTagDuration] = c.duration
	data[cloudTagReapplication] = c.reapplication
	data[cloudTagDurationOnUse] = c.durationOnUse
	data[cloudTagInitialRadius] = c.radius
	data[cloudTagRadiusOnUse] = c.radiusOnUse
	data[cloudTagRadiusPerTick] = c.perTick
	// BDS writes half the larger side of the cloud's box here; Helix gives the cloud no box,
	// so this is its radius now.
	radius := c.radius
	if r := e.Region(); r != nil && c.spawnTick > 0 {
		radius = c.currentRadius(max(r.CurrentTick()-c.spawnTick, 0))
	}
	data[cloudTagRadius] = radius
	data[cloudTagOwner] = c.owner
	data[cloudTagPotion] = c.potion
	data[cloudTagParticle] = c.particle
	list := make([]any, 0, len(c.effects))
	for i := range c.effects {
		list = append(list, effectNBT(&c.effects[i]))
	}
	data[cloudTagEffects] = list
	data[cloudTagColour] = int32(c.colour) //nolint:gosec // ARGB, as BDS saves it
	data[cloudTagSpawnTick] = max(c.spawnTick, 0)
	data[cloudTagChangeOnPickup] = c.changeOnPickup
	data[cloudTagPickupCount] = c.pickupCount
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadCloud brings a saved cloud back, with the time already passed in its life: its spawn
// tick is the world's tick, which goes on across restarts.
func (s *Host) loadCloud(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	d := saved.Data
	c := &AreaEffectCloud{s: s, saved: d, victims: map[int64]int64{}, affectOwner: true}
	c.duration, _ = d[cloudTagDuration].(int32)
	c.reapplication, _ = d[cloudTagReapplication].(int32)
	c.durationOnUse, _ = d[cloudTagDurationOnUse].(int32)
	c.radius, _ = d[cloudTagInitialRadius].(float32)
	c.radiusOnUse, _ = d[cloudTagRadiusOnUse].(float32)
	c.perTick, _ = d[cloudTagRadiusPerTick].(float32)
	c.owner, _ = d[cloudTagOwner].(int64)
	c.potion, _ = d[cloudTagPotion].(int16)
	c.particle, _ = d[cloudTagParticle].(int32)
	colour, _ := d[cloudTagColour].(int32)
	c.colour = uint32(colour) //nolint:gosec // ARGB, as BDS saves it
	c.spawnTick, _ = d[cloudTagSpawnTick].(int64)
	c.changeOnPickup, _ = d[cloudTagChangeOnPickup].(float32)
	c.pickupCount, _ = d[cloudTagPickupCount].(int32)
	if list, isList := d[cloudTagEffects].([]any); isList {
		for _, v := range list {
			if m, isMap := v.(map[string]any); isMap {
				if eff, valid := effectFromNBT(m); valid {
					c.effects = append(c.effects, eff)
				}
			}
		}
	}
	c.entity = r.AddLoadedEntity(at, c.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}))
	return true
}
