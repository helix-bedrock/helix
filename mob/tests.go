package mob

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/world/cube"
)

// The filter tests beyond the core ones in host.go, by what answers them: the mob itself (or
// another mob's snapshot), a player (PlayerInfo), or the world around the mob (Env). Written
// from the test names and vanilla's use of them, not yet checked against BDS 1.26.60's filter
// test classes one by one. Mobs have no equipment, riders, owners, effects or name tags yet,
// so those tests answer as BDS would for a mob without them.

// PlayerInfo is what the filter tests ask about a player beyond behavior.Actor. The player
// package's actor implements it. Its methods only read and may run while mobs think.
type PlayerInfo interface {
	// Sneaking reports the player sneaking.
	Sneaking() bool
	// Sprinting reports the player sprinting.
	Sprinting() bool
	// HasAbility reports one of the player's abilities by its filter name (instabuild,
	// invulnerable, flying, mayfly and the rest).
	HasAbility(name string) bool
	// Equipment appends the items in the slots a filter domain names ("hand", "main_hand",
	// "offhand", "head", "torso", "leg", "feet", "armor", "inventory", "any").
	Equipment(domain string, dst []string) []string
	// ItemTags are an item's tags.
	ItemTags(item string) []string
}

// BDS's actor flag numbers only the filter tests read.
const (
	flagOnFire   = 0
	flagSleeping = 76
)

const (
	// dayTicks is a day's length, for hourly_clock_time.
	dayTicks = 24000
	// ticksPerSecond turns the seconds in filter values into ticks.
	ticksPerSecond = 20
)

// moving reports the mob moving this tick.
func (m *Mob) moving() bool {
	dx, dy, dz := m.body.Pos.X-m.body.PrevPos.X, m.body.Pos.Y-m.body.PrevPos.Y, m.body.Pos.Z-m.body.PrevPos.Z
	return dx*dx+dy*dy+dz*dz > 1e-7
}

// more runs the tests that are not in host.go. a is the subject, m or s the mob behind it.
func (t tester) more(test *definition.Test, a behavior.Actor, m *Mob, s *Snapshot) (pass, resolved bool) {
	op := test.Operator
	p, _ := a.(PlayerInfo)
	env, r := t.m.rt.Env, t.m.v.r
	switch test.Name {
	case "has_equipment":
		if eq := mobEquip(m, s); eq != nil {
			has := slices.Contains(equipmentNames(eq, domainOr(test, "any"), nil), ns(rawString(test.Value)))
			return definition.CompareBool(op, has, true), true
		}
		return definition.CompareBool(op, t.equipped(p, test, false), true), true
	case "has_equipment_tag":
		// Item tags of a mob's equipment are not looked up yet: only players answer.
		return definition.CompareBool(op, t.equipped(p, test, true), true), true
	case "all_slots_empty":
		var empty bool
		if eq := mobEquip(m, s); eq != nil {
			empty = len(equipmentNames(eq, domainOr(test, "any"), nil)) == 0
		} else {
			empty = p == nil || len(p.Equipment(domainOr(test, "any"), nil)) == 0
		}
		return definition.CompareBool(op, empty, rawBool(test.Value, true)), true
	case "is_sneak_held", "is_sneaking":
		return definition.CompareBool(op, p != nil && p.Sneaking(), rawBool(test.Value, true)), true
	case "is_sprinting":
		return definition.CompareBool(op, p != nil && p.Sprinting(), rawBool(test.Value, true)), true
	case "has_ability":
		return definition.CompareBool(op, p != nil && p.HasAbility(rawString(test.Value)), true), true
	case "rider_count":
		return definition.Compare(op, int32(len(a.Riders())), rawInt(test.Value)), true //nolint:gosec // a rider count
	case "is_riding":
		return definition.CompareBool(op, a.Vehicle() != behavior.NoID, rawBool(test.Value, true)), true
	case "has_ranged_weapon":
		// ActorHasRangedWeaponTest::evaluate (1.21.3 names): a bow or crossbow in the main hand.
		var hand []string
		if eq := mobEquip(m, s); eq != nil {
			hand = equipmentNames(eq, "hand", nil)
		} else if p != nil {
			hand = p.Equipment("hand", nil)
		}
		ranged := slices.ContainsFunc(hand, func(n string) bool { return n == itemBow || n == itemCrossbow })
		return definition.CompareBool(op, ranged, rawBool(test.Value, true)), true
	case "is_riding_self", "is_vehicle_family", "is_controlling_passenger_family", "in_caravan", "is_leashed_to",
		"has_nametag", "has_mob_effect", "is_avoiding_mobs", "is_owner", "trusts",
		"has_damaged_equipment", "has_same_equipment_in_slot_as", "has_trade_supply", "has_container_open":
		// Nothing a mob or player can have yet: riding, leashes, owners, effects, name tags,
		// and trades are not hooked up.
		return definition.CompareBool(op, false, rawBool(test.Value, true)), true
	case "is_variant", "is_mark_variant", "is_skin_id", "is_color":
		v, ok := intComponent(comps(m, s), variantComponents[test.Name])
		return ok && definition.Compare(op, v, rawInt(test.Value)), true
	case "y_rotation":
		yaw := float32(math.Remainder(float64(a.BodyYaw()), 360))
		return definition.Compare(op, yaw, rawFloat(test.Value)), true
	case "is_moving":
		moving := m != nil && m.moving()
		return definition.CompareBool(op, moving, rawBool(test.Value, true)), true
	case "on_fire":
		return definition.CompareBool(op, a.Flag(flagOnFire), rawBool(test.Value, true)), true
	case "is_sitting":
		return definition.CompareBool(op, a.Flag(behavior.FlagSitting), rawBool(test.Value, true)), true
	case "is_sleeping":
		return definition.CompareBool(op, a.Flag(flagSleeping), rawBool(test.Value, true)), true
	case "is_leashed":
		return definition.CompareBool(op, a.Flag(behavior.FlagLeashed), rawBool(test.Value, true)), true
	case "is_tamed":
		return definition.CompareBool(op, a.Flag(behavior.FlagTamed), rawBool(test.Value, true)), true
	case "is_persistent":
		return definition.CompareBool(op, m != nil && m.persistent, rawBool(test.Value, true)), true
	case "is_missing_health":
		missing := m != nil && m.health < m.maxHealth
		return definition.CompareBool(op, missing, rawBool(test.Value, true)), true
	case "in_contact_with_water":
		wet := m != nil && m.InWater() || s != nil && s.inWater
		return definition.CompareBool(op, wet, rawBool(test.Value, true)), true
	case "in_nether":
		return definition.CompareBool(op, a.Dimension() == 1, rawBool(test.Value, true)), true
	case "was_last_hurt_by":
		by := t.other != nil && t.other.ID() == a.LastHurtByMob()
		return definition.CompareBool(op, by, rawBool(test.Value, true)), true
	case "random_chance":
		n := max(rawInt(test.Value), 1)
		return definition.CompareBool(op, t.m.rnd.NextInt(n) == 0, true), true
	case "inactivity_timer":
		idle := m != nil && m.noActionTime >= rawInt(test.Value)*ticksPerSecond
		return definition.CompareBool(op, idle, true), true
	case "distance_to_nearest_player":
		d, ok := t.nearestPlayer(a.Pos())
		return ok && definition.Compare(op, d, rawFloat(test.Value)), true
	case "target_distance":
		tg := t.m.actor(a.Target())
		return tg != nil && definition.Compare(op, dist(a.Pos(), tg.Pos()), rawFloat(test.Value)), true
	}
	if r == nil {
		return false, true
	}
	pos := a.Pos()
	at := cube.Pos{int(math.Floor(float64(pos.X))), int(math.Floor(float64(pos.Y))), int(math.Floor(float64(pos.Z)))}
	switch test.Name {
	case "has_biome_tag":
		tags, ok := env.BiomeTags(r, at)
		return ok && definition.CompareBool(op, slices.Contains(tags, rawString(test.Value)), true), true
	case "is_biome":
		name, ok := env.BiomeName(r, at)
		return ok && definition.CompareBool(op, strings.TrimPrefix(name, "minecraft:") == strings.TrimPrefix(rawString(test.Value), "minecraft:"), true), true
	case "hourly_clock_time":
		return definition.Compare(op, env.DayTime()%dayTicks, rawInt(test.Value)), true
	case "is_game_rule":
		v, ok := env.GameRule(strings.ToLower(rawString(test.Domain)))
		return ok && definition.CompareBool(op, v, rawBool(test.Value, true)), true
	case "is_underground":
		// BDS's canSeeSky reads the height map: below the highest block is underground.
		h, ok := r.Height(at[0], at[2])
		return ok && definition.CompareBool(op, at[1]+1 < h, rawBool(test.Value, true)), true
	case "is_brightness":
		b := float32(r.Brightness(at.Add(cube.Pos{0, 1, 0}), uint8(min(max(env.SkyDarken(r), 0), 15)))) / 15 //nolint:gosec // clamped
		return definition.Compare(op, b, rawFloat(test.Value)), true
	case "in_block":
		id, _ := r.Block(at, 0)
		return definition.CompareBool(op, t.m.rt.Blocks.reg.Name(id) == ns(rawString(test.Value)), true), true
	case "weather", "weather_at_position":
		return definition.CompareBool(op, weatherIs(rawString(test.Value), env.Rain(r), env.Thunder(r)), true), true
	}
	return false, true
}

// blockTest runs a test on the block subject.
func (t tester) blockTest(test *definition.Test) (pass, resolved bool) {
	r := t.m.v.r
	if !t.hasPos || r == nil {
		return false, false
	}
	if test.Name != "is_block" {
		return false, true
	}
	id, _ := r.Block(cube.Pos{int(t.pos.X), int(t.pos.Y), int(t.pos.Z)}, 0)
	return definition.CompareBool(test.Operator, t.m.rt.Blocks.reg.Name(id) == ns(rawString(test.Value)), true), true
}

// equipped is has_equipment and has_equipment_tag: an item (or an item with the tag) in the
// slots the domain names. Mobs carry nothing yet.
func (t tester) equipped(p PlayerInfo, test *definition.Test, tag bool) bool {
	if p == nil {
		return false
	}
	want := rawString(test.Value)
	if !tag {
		want = ns(want)
	}
	for _, item := range p.Equipment(domainOr(test, "any"), nil) {
		if !tag && item == want || tag && slices.Contains(p.ItemTags(item), want) {
			return true
		}
	}
	return false
}

// nearestPlayer is the distance to the nearest player of the region.
func (t tester) nearestPlayer(p behavior.Vec3) (float32, bool) {
	r := t.m.v.r
	if r == nil {
		return 0, false
	}
	best, found := float32(math.MaxFloat32), false
	for _, pl := range t.m.rt.Env.Players(r, nil) {
		if d := dist(p, pl.Pos()); d < best {
			best, found = d, true
		}
	}
	return best, found
}

// mobEquip is the equipment of the mob behind a test's subject, nil for a player.
func mobEquip(m *Mob, s *Snapshot) *[EquipSlots]behavior.Item {
	switch {
	case m != nil:
		return &m.equip
	case s != nil:
		return &s.equip
	}
	return nil
}

// variantComponents are the components the variant tests read their value from.
var variantComponents = map[string]string{
	"is_variant": "minecraft:variant", "is_mark_variant": "minecraft:mark_variant",
	"is_skin_id": "minecraft:skin_id", "is_color": "minecraft:color",
}

func comps(m *Mob, s *Snapshot) definition.Group {
	switch {
	case m != nil:
		return m.inst.Components()
	case s != nil:
		return s.comps
	}
	return nil
}

// intComponent reads the "value" of a component such as minecraft:variant.
func intComponent(c definition.Group, name string) (int32, bool) {
	raw, ok := c[name]
	if !ok {
		return 0, false
	}
	var v struct {
		Value *float32 `json:"value"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Value == nil {
		return 0, false
	}
	return int32(*v.Value), true
}

// weatherIs is the weather tests' values: clear, precipitation (rain or snow), rain, snow
// and thunderstorm. Rain and snow are not told apart by biome yet.
func weatherIs(v string, rain, thunder float32) bool {
	switch v {
	case "clear":
		return rain <= 0
	case "precipitation", "rain", "snow":
		return rain > 0
	case "thunderstorm":
		return thunder > 0
	}
	return false
}

func domainOr(test *definition.Test, def string) string {
	if d := rawString(test.Domain); d != "" {
		return d
	}
	return def
}

func dist(a, b behavior.Vec3) float32 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

// rawInt reads a number or a numeric string as an integer.
func rawInt(raw []byte) int32 {
	f := rawFloat(raw)
	if f == 0 {
		if n, err := strconv.Atoi(strings.Trim(string(raw), `"`)); err == nil {
			return int32(n) //nolint:gosec // a filter value
		}
	}
	return int32(f)
}
