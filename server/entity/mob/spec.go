package mob

import (
	"cmp"
	"encoding/json"
	"github.com/helix-bedrock/helix/server/molang"
	"math"
	"slices"
	"strings"
)

// spec is what a mob's active components say, parsed once per set of
// component groups.
type spec struct {
	health, maxHealth float64
	speed             float64
	width, height     float64
	scale             float64
	baby              bool
	families          []string
	attack            [2]float64
	followRange       float64
	loot              string
	burnsInDaylight   bool
	knockbackResist   float64
	jumpPower         float64
	gravity           bool

	// walks is set for mobs that walk with navigation.walk or .generic;
	// other navigations (flying, swimming) are not ported yet, so such mobs
	// do not pathfind.
	walks bool
	// swims and flies are set for mobs that move by steering straight at a
	// point, at directSpeed blocks per tick.
	swims, flies                            bool
	directSpeed                             float64
	canFloat, canOpenDoors, canPassDoors    bool
	avoidSun, avoidWater, canWalkOverFences bool

	behaviours []behaviourSpec
	despawn    *despawnSpec
	sensor     *sensorSpec
	explode    *explodeSpec
	ageable    *ageableSpec
	breed      *breedSpec
	// shooter is the projectile the mob shoots, such as "minecraft:arrow".
	shooter   string
	equipment string
	ambient   *ambientSpec
	// xp is the experience_reward on_death expression.
	xp *molang.Expr
}

type behaviourSpec struct {
	name     string
	priority int
	raw      json.RawMessage
}

// number reads a JSON number, a [min, max] pair or a {range_min, range_max}
// object as its low and high values.
func number(raw json.RawMessage) (lo, hi float64, ok bool) {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f, f, true
	}
	var pair []float64
	if json.Unmarshal(raw, &pair) == nil && len(pair) == 2 {
		return pair[0], pair[1], true
	}
	var r struct {
		Min      *float64 `json:"range_min"`
		Max      *float64 `json:"range_max"`
		MinShort *float64 `json:"min"`
		MaxShort *float64 `json:"max"`
	}
	if json.Unmarshal(raw, &r) == nil {
		if r.Min != nil && r.Max != nil {
			return *r.Min, *r.Max, true
		}
		if r.MinShort != nil && r.MaxShort != nil {
			return *r.MinShort, *r.MaxShort, true
		}
	}
	return 0, 0, false
}

// field reads one field of a component object.
func field(raw json.RawMessage, name string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m[name]
}

func numberField(raw json.RawMessage, name string, def float64) float64 {
	if f := field(raw, name); f != nil {
		if lo, _, ok := number(f); ok {
			return lo
		}
	}
	return def
}

func boolField(raw json.RawMessage, name string, def bool) bool {
	var b bool
	if f := field(raw, name); f != nil && json.Unmarshal(f, &b) == nil {
		return b
	}
	return def
}

func parseSpec(d *Def, c map[string]json.RawMessage) *spec {
	s := &spec{
		health: 10, maxHealth: 10, speed: 0.25, width: 0.6, height: 1.8, scale: 1,
		followRange: 16, jumpPower: 0.42, gravity: true, canPassDoors: true,
	}
	if raw, ok := c["minecraft:health"]; ok {
		s.maxHealth = numberField(raw, "max", numberField(raw, "value", s.maxHealth))
		s.health = numberField(raw, "value", s.maxHealth)
		if s.maxHealth < s.health {
			s.maxHealth = s.health
		}
	}
	if raw, ok := c["minecraft:movement"]; ok {
		s.speed = numberField(raw, "value", s.speed)
	}
	if raw, ok := c["minecraft:collision_box"]; ok {
		s.width = numberField(raw, "width", s.width)
		s.height = numberField(raw, "height", s.height)
	}
	if raw, ok := c["minecraft:scale"]; ok {
		s.scale = numberField(raw, "value", 1)
	}
	_, s.baby = c["minecraft:is_baby"]
	if raw, ok := c["minecraft:type_family"]; ok {
		var f struct {
			Family []string `json:"family"`
		}
		_ = json.Unmarshal(raw, &f)
		s.families = f.Family
	}
	if raw, ok := c["minecraft:attack"]; ok {
		if lo, hi, ok := number(field(raw, "damage")); ok {
			s.attack = [2]float64{lo, hi}
		}
	}
	if raw, ok := c["minecraft:follow_range"]; ok {
		s.followRange = numberField(raw, "value", s.followRange)
	}
	if raw, ok := c["minecraft:loot"]; ok {
		var l struct {
			Table string `json:"table"`
		}
		_ = json.Unmarshal(raw, &l)
		s.loot = l.Table
	}
	_, s.burnsInDaylight = c["minecraft:burns_in_daylight"]
	if raw, ok := c["minecraft:knockback_resistance"]; ok {
		s.knockbackResist = numberField(raw, "value", 0)
	}
	if raw, ok := c["minecraft:jump.static"]; ok {
		s.jumpPower = numberField(raw, "jump_power", s.jumpPower)
	}
	if raw, ok := c["minecraft:physics"]; ok {
		s.gravity = boolField(raw, "has_gravity", true)
	}
	for _, nav := range []string{"minecraft:navigation.walk", "minecraft:navigation.generic", "minecraft:navigation.climb"} {
		raw, ok := c[nav]
		if !ok {
			continue
		}
		s.walks = boolField(raw, "can_walk", true)
		s.canFloat = boolField(raw, "can_swim", false) || boolField(raw, "can_float", false)
		s.canOpenDoors = boolField(raw, "can_open_doors", false)
		s.canPassDoors = boolField(raw, "can_pass_doors", true)
		s.avoidSun = boolField(raw, "avoid_sun", false)
		s.avoidWater = boolField(raw, "avoid_water", false)
		s.canWalkOverFences = boolField(raw, "can_walk_over_fences", false)
	}
	if raw, ok := c["minecraft:navigation.generic"]; ok && boolField(raw, "can_swim", false) && !boolField(raw, "can_walk", true) {
		s.swims, s.walks = true, false
		s.directSpeed = s.speed
		if u, ok := c["minecraft:underwater_movement"]; ok {
			s.directSpeed = numberField(u, "value", s.speed)
		}
	}
	for _, nav := range []string{"minecraft:navigation.float", "minecraft:navigation.hover", "minecraft:navigation.fly"} {
		if _, ok := c[nav]; ok {
			s.flies, s.walks, s.gravity = true, false, false
			s.directSpeed = s.speed
			if f, ok := c["minecraft:flying_speed"]; ok {
				s.directSpeed = numberField(f, "value", s.speed)
			}
			s.directSpeed = math.Min(s.directSpeed, 0.3)
		}
	}
	if raw, ok := c["minecraft:target_nearby_sensor"]; ok {
		s.sensor = parseSensor(raw)
	}
	if raw, ok := c["minecraft:explode"]; ok {
		s.explode = parseExplode(raw)
	}
	if raw, ok := c["minecraft:ageable"]; ok {
		s.ageable = parseAgeable(raw)
	}
	if raw, ok := c["minecraft:breedable"]; ok {
		s.breed = parseBreedable(raw, d.ID)
	}
	if raw, ok := c["minecraft:shooter"]; ok {
		var sh struct {
			Def string `json:"def"`
		}
		_ = json.Unmarshal(raw, &sh)
		s.shooter = sh.Def
	}
	if raw, ok := c["minecraft:equipment"]; ok {
		var e struct {
			Table string `json:"table"`
		}
		_ = json.Unmarshal(raw, &e)
		s.equipment = e.Table
	}
	if raw, ok := c["minecraft:ambient_sound_interval"]; ok {
		s.ambient = parseAmbient(raw)
	} else {
		// Most mobs (cows, zombies) have no such component yet make idle
		// sounds in BDS: assume the component's documented defaults.
		s.ambient = parseAmbient(nil)
	}
	if raw, ok := c["minecraft:experience_reward"]; ok {
		if f := field(raw, "on_death"); f != nil {
			var src string
			if json.Unmarshal(f, &src) != nil {
				src = string(f) // a plain number
			}
			if e, err := molang.Parse(src); err == nil {
				s.xp = e
			}
		}
	}
	if raw, ok := c["minecraft:despawn"]; ok {
		s.despawn = parseDespawn(raw)
	}
	if _, ok := c["minecraft:behavior.float"]; ok {
		s.canFloat = true
	}
	for name, raw := range c {
		b, ok := strings.CutPrefix(name, "minecraft:behavior.")
		if !ok {
			continue
		}
		s.behaviours = append(s.behaviours, behaviourSpec{name: b, priority: int(numberField(raw, "priority", 0)), raw: raw})
	}
	// Lower priority runs first; ties keep a fixed order so every server
	// builds the same goals.
	slices.SortFunc(s.behaviours, func(a, b behaviourSpec) int {
		return cmp.Or(cmp.Compare(a.priority, b.priority), strings.Compare(a.name, b.name))
	})
	return s
}

func (s *spec) hasFamily(f string) bool { return slices.Contains(s.families, f) }
