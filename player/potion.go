package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Potions, from BDS 1.26.60. The table is the one Potion's static setup (0xC8C1380) builds and
// registers in ID order (0xC6DDD70, Potion::addPotion 0xC6DD230): a potion item's meta is its
// ID. Drinking is PotionItem (vtable 0xFB51820): 32 ticks of use, then the finish 0xC90ABD0.
// Splash and lingering potions are thrown as snowballs are (0xC8E58C0, 0xC8F9B90) and do
// their work in their projectile's on hit subcomponents: thrown_potion_effect (0x7153490),
// spawn_aoe_cloud (0x714EA60) and douse_fire (0x7151E10).

const (
	namePotion          = "minecraft:potion"
	nameSplashPotion    = "minecraft:splash_potion"
	nameLingeringPotion = "minecraft:lingering_potion"

	// potionUseTicks is how long drinking takes, the potion item's use duration.
	potionUseTicks = 32
	// potionWater is the water bottle's ID.
	potionWater = 0

	// splashDurationScale is what a splash keeps of an effect's duration, BDS's 0.75
	// (dword_FD33A28), before the distance scale.
	splashDurationScale = 0.75
	// splashMinTicks is the shortest splashed effect given: BDS gives none of 20 ticks or less.
	splashMinTicks = 20
	// splashItemParticles is how many item crack particles a splash shows.
	splashItemParticles = 5
	// particleIconCrack is BDS's item crack particle, the data's low bits with the item ID.
	particleIconCrack = 14
)

// Potion is a kind of potion: its ID is the potion item's meta, and its effects are given at
// these durations when drunk.
type Potion struct {
	ID int32
	// Name is BDS's name, as in its translation keys.
	Name    string
	Effects []ActiveEffect
}

// RegisterPotion adds a potion or replaces the one with its ID.
func (s *Setup) RegisterPotion(p Potion) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	if old, ok := s.h.game.potions[p.ID]; ok {
		s.h.log.Info("potion replaced", "name", p.Name, "was", old.Name, "by", s.owner)
	}
	s.h.game.potions[p.ID] = &p
	return nil
}

// Potion returns the registered potion with id, to change its fields in place.
func (s *Setup) Potion(id int32) (*Potion, bool) {
	p, ok := s.h.game.potions[id]
	return p, ok && !s.h.frozen
}

// Potion returns the potion with id, false if there is none.
func (s *Host) Potion(id int32) (Potion, bool) {
	p, ok := s.game.potions[id]
	if !ok {
		return Potion{}, false
	}
	return *p, true
}

// potionEffect is one effect of a potion.
func potionEffect(id, ticks, amplifier int32) ActiveEffect {
	return ActiveEffect{ID: id, Duration: ticks, Amplifier: amplifier}
}

// vanillaPotions are BDS 1.26.60's potions, in ID order, with their effects as its static
// setup gives them.
func vanillaPotions() []Potion {
	one := func(id int32, name string, effect, ticks, amplifier int32) Potion {
		return Potion{ID: id, Name: name, Effects: []ActiveEffect{potionEffect(effect, ticks, amplifier)}}
	}
	turtle := func(id int32, name string, ticks, slow, resist int32) Potion {
		return Potion{ID: id, Name: name, Effects: []ActiveEffect{
			potionEffect(EffectSlowness, ticks, slow), potionEffect(EffectResistance, ticks, resist),
		}}
	}
	return []Potion{
		{ID: potionWater, Name: "water"},
		{ID: 1, Name: "mundane"},
		{ID: 2, Name: "long_mundane"},
		{ID: 3, Name: "thick"},
		{ID: 4, Name: "awkward"},
		one(5, "nightvision", EffectNightVision, 3600, 0),
		one(6, "long_nightvision", EffectNightVision, 9600, 0),
		one(7, "invisibility", EffectInvisibility, 3600, 0),
		one(8, "long_invisibility", EffectInvisibility, 9600, 0),
		one(9, "leaping", EffectJumpBoost, 3600, 0),
		one(10, "long_leaping", EffectJumpBoost, 9600, 0),
		one(11, "strong_leaping", EffectJumpBoost, 1800, 1),
		one(12, "fire_resistance", EffectFireResistance, 3600, 0),
		one(13, "long_fire_resistance", EffectFireResistance, 9600, 0),
		one(14, "swiftness", EffectSpeed, 3600, 0),
		one(15, "long_swiftness", EffectSpeed, 9600, 0),
		one(16, "strong_swiftness", EffectSpeed, 1800, 1),
		one(17, "slowness", EffectSlowness, 1800, 0),
		one(18, "long_slowness", EffectSlowness, 4800, 0),
		one(19, "water_breathing", EffectWaterBreathing, 3600, 0),
		one(20, "long_water_breathing", EffectWaterBreathing, 9600, 0),
		one(21, "healing", EffectInstantHealth, 1, 0),
		one(22, "strong_healing", EffectInstantHealth, 1, 1),
		one(23, "harming", EffectInstantDamage, 1, 0),
		one(24, "strong_harming", EffectInstantDamage, 1, 1),
		one(25, "poison", EffectPoison, 900, 0),
		one(26, "long_poison", EffectPoison, 2400, 0),
		one(27, "strong_poison", EffectPoison, 450, 1),
		one(28, "regeneration", EffectRegeneration, 900, 0),
		one(29, "long_regeneration", EffectRegeneration, 2400, 0),
		one(30, "strong_regeneration", EffectRegeneration, 450, 1),
		one(31, "strength", EffectStrength, 3600, 0),
		one(32, "long_strength", EffectStrength, 9600, 0),
		one(33, "strong_strength", EffectStrength, 1800, 1),
		one(34, "weakness", EffectWeakness, 1800, 0),
		one(35, "long_weakness", EffectWeakness, 4800, 0),
		one(36, "wither", EffectWither, 800, 1),
		turtle(37, "turtle_master", 400, 3, 2),
		turtle(38, "long_turtle_master", 800, 3, 2),
		turtle(39, "strong_turtle_master", 400, 5, 3),
		one(40, "slow_falling", EffectSlowFalling, 1800, 0),
		one(41, "long_slow_falling", EffectSlowFalling, 4800, 0),
		one(42, "strong_slowness", EffectSlowness, 400, 3),
		one(43, "wind_charged", EffectWindCharged, 3600, 0),
		one(44, "weaving", EffectWeaving, 3600, 0),
		one(45, "oozing", EffectOozing, 3600, 0),
		one(46, "infested", EffectInfested, 3600, 0),
		one(47, "freezing", EffectFreezing, 900, 0),
		one(48, "long_freezing", EffectFreezing, 1800, 0),
	}
}

// waterColour is the colour of a potion with no effects that show, BDS's 0x385DC6.
const waterColour uint32 = 0xFF385DC6

// potionColour is BDS's mix of a potion's colour (0xCABAEC0), as ARGB: each effect that shows
// counts its colour once per level, and the mix is their average. A potion with no effects
// is the colour of water; one whose effects all hide is black.
func (s *Host) potionColour(effects []ActiveEffect) uint32 {
	if len(effects) == 0 {
		return waterColour
	}
	var r, g, b, n float64
	for _, e := range effects {
		def, ok := s.game.effects[e.ID]
		if !ok || e.Hidden || def.NoParticles || e.Amplifier < 0 {
			continue
		}
		c := float64(e.Amplifier + 1)
		r += c * float64(def.Color>>16&0xFF)
		g += c * float64(def.Color>>8&0xFF)
		b += c * float64(def.Color&0xFF)
		n += c
	}
	if n == 0 {
		return 0
	}
	channel := func(v float64) uint32 { return uint32(min(max(v/n/0xFF, 0), 1) * 0xFF) }
	return 0xFF000000 | channel(r)<<16 | channel(g)<<8 | channel(b)
}

// startPotion starts drinking a potion, which any player may.
func (p *Player) startPotion(held ItemStack) {
	u := &p.using
	slot := p.inventory.held
	if u.active && u.slot == slot && u.item.Name == held.Name && u.item.Meta == held.Meta {
		return
	}
	*u = usingState{active: true, slot: slot, item: held, left: potionUseTicks, potion: true}
	p.sendActorFlags()
}

// drinkPotion is BDS's finished drink (0xC90ABD0): every effect of the potion, at its full
// duration, then one potion used, the bottle.empty sound at the mouth and a glass bottle,
// which takes the potion's place when it was the last one and goes into the inventory (or
// drops) otherwise.
func (p *Player) drinkPotion(held ItemStack) {
	if pot, ok := p.s.Potion(int32(held.Meta)); ok {
		for _, e := range pot.Effects {
			if e.ID != 0 {
				p.GiveEffect(ActiveEffect{ID: e.ID, Amplifier: e.Amplifier, Duration: e.Duration})
			}
		}
	}
	if !p.creative() {
		p.useHeld()
	}
	if r := p.entity.Region(); r != nil {
		playSound(r, p.blockPos(), soundEvent{name: soundBottleEmpty, pos: p.AttachPos(AttachMouth), extra: -1, entity: "minecraft:player"}, nil)
	}
	bottle := ItemStack{Name: nameGlassBottle, Count: 1, netID: p.s.stackIDs.Add(1)}
	inv := &p.inventory
	if inv.main[inv.held].Empty() {
		inv.main[inv.held] = bottle
		p.sendHeld()
	} else if rest := p.Give(bottle); !rest.Empty() {
		p.s.game.sys.DropItem(p, rest, false)
	}
	p.flush()
}

// soundBottleEmpty is BDS's sound 478, played when a potion is finished.
const soundBottleEmpty = "bottle.empty"

// aimRotation is BDS's throw aim (0x7147BC0): the pitch turned by the definition's angle
// offset, less of it the further up or down the thrower looks.
func aimRotation(rot world.Rotation, offset float32) world.Rotation {
	if offset != 0 {
		rot.Pitch += offset * float32(math.Cos(float64(rot.Pitch)*math.Pi/180))
	}
	return rot
}

// splashReach is BDS's growth of the boxes a splash reaches with the potion's age, as its
// projectile's hit boxes grow.
func (t *Projectile) splashReach() float32 {
	return min(max((float32(t.age)-projectileReachFrom)*projectileReachStep, 0), projectileReachMax)
}

// splashShow is the splash's colour and item crack particles at the hit, BDS's LevelEvent
// 2002 with the potion's colour and five of 2009 with the item.
func (t *Projectile) splashShow(r *world.Region, at mgl32.Vec3, colour uint32, cracks bool) {
	block := floorPos(at)
	levelEvent(r, block, at, packet.LevelEventParticlesPotionSplash, int32(colour)) //nolint:gosec // ARGB, as BDS sends it
	if !cracks {
		return
	}
	if it, ok := t.s.reg.Items.ByName(t.spec.ID); ok {
		for range splashItemParticles {
			levelEvent(r, block, at, packet.LevelEventParticleGenericSpawn, int32(it.ID)<<16|particleIconCrack)
		}
	}
}

// splash is BDS's thrown_potion_effect: the potion's effects on every player within its
// splash range of where it hit, measured to the player's box, at full strength on the one it
// hit and less the further off. Harmful effects from a player's potion pass other players by
// while pvp is off. Mobs take no effects in Helix yet.
func (t *Projectile) splash(r *world.Region, e *world.Entity, hit projectileHit) {
	pot, _ := t.s.Potion(int32(t.potion))
	t.splashShow(r, hit.pos, t.s.potionColour(pot.Effects), true)
	if len(pot.Effects) == 0 {
		return
	}
	reach, rng := t.splashReach(), t.spec.SplashRange
	pos := e.Position()
	half := t.spec.Width / 2
	lo := mgl32.Vec3{pos[0] - half - rng - reach, pos[1] - rng/2 - reach, pos[2] - half - rng - reach}
	hi := mgl32.Vec3{pos[0] + half + rng + reach, pos[1] + t.spec.Height + rng/2 + reach, pos[2] + half + rng + reach}
	grow := mgl32.Vec3{reach, reach, reach}
	t.found = r.AppendEntitiesIn(t.found[:0], lo[0], lo[2], hi[0], hi[2])
	g := t.s.game
	for _, o := range t.found {
		target, ok := o.Owner().(*Player)
		if !ok || target.dead() || target.gameMode == GameModeSpectator {
			continue
		}
		boxLo, boxHi, has := target.BlastBox()
		if !has || !overlaps(lo, hi, boxLo, boxHi) {
			continue
		}
		near := clampToBox(hit.pos, boxLo.Sub(grow), boxHi.Add(grow))
		dist := near.Sub(hit.pos).LenSqr()
		if dist >= rng*rng {
			continue
		}
		scale := float32(1)
		if o != hit.entity {
			scale = 1 - float32(math.Sqrt(float64(dist)))/rng
		}
		for _, eff := range pot.Effects {
			def, known := g.effects[eff.ID]
			if !known {
				continue
			}
			if def.Harmful && t.owner != nil && !t.s.level.PVP {
				continue
			}
			if def.Instant {
				target.ApplyInstantEffect(eff.ID, eff.Amplifier, scale)
				continue
			}
			ticks := eff.Duration
			if ticks != EffectInfinite {
				ticks = max(int32(splashDurationScale*float32(ticks)), 0)
				ticks = int32(scale*float32(ticks) + 0.5)
				if ticks <= splashMinTicks {
					continue
				}
			}
			target.GiveEffect(ActiveEffect{ID: eff.ID, Amplifier: eff.Amplifier, Duration: ticks})
		}
	}
}

// douseFire is BDS's douse_fire for a water potion hitting a block: the block beside the face
// it hit (the block itself when that is a campfire) and the four around it at its height lose
// their fire, and campfires there go out.
func (t *Projectile) douseFire(r *world.Region, hit projectileHit) {
	if t.potion != potionWater {
		return
	}
	base := hit.block
	if b, ok := r.Block(base, 0); !ok || !isCampfireName(t.s.reg.Blocks.Name(b)) {
		base = base.Add(cube.Pos{int(hit.normal[0]), int(hit.normal[1]), int(hit.normal[2])})
	}
	for _, at := range [...]cube.Pos{base, base.Add(cube.Pos{0, 0, 1}), base.Add(cube.Pos{-1, 0, 0}), base.Add(cube.Pos{0, 0, -1}), base.Add(cube.Pos{1, 0, 0})} {
		b, ok := r.Block(at, 0)
		if !ok {
			continue
		}
		switch name := t.s.reg.Blocks.Name(b); {
		case name == nameFire || name == nameSoulFire:
			if r.SetBlock(at, 0, t.s.reg.Blocks.Air()) {
				r.UpdateNeighbours(at)
			}
		case isCampfireName(name):
			if t.s.fire != nil {
				t.s.fire.DouseCampfire(r, at, false)
			}
		}
	}
}

// potionHit is what a potion's on hit subcomponents do where it lands.
func (t *Projectile) potionHit(r *world.Region, e *world.Entity, hit projectileHit, kind projectileHitKind) {
	if t.spec.DouseFire && kind == projectileHitBlock {
		t.douseFire(r, hit)
	}
	if t.spec.SplashPotion {
		t.splash(r, e, hit)
	}
	if t.spec.Cloud != nil {
		t.spawnCloud(r, hit.pos)
	}
}
