package mob

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/world"
)

// filterEnv is what a definition filter is checked against.
type filterEnv struct {
	tx    *world.Tx
	self  *Mob
	other world.Entity
	// biome is the biome a spawn rule is checked in.
	biome world.Biome
}

// evalFilter checks a BDS filter: a test, a list (all must pass) or
// all_of/any_of/none_of (also AND/OR) groups. Tests Helix does not know
// fail, so a mob never does something it was not meant to.
func evalFilter(raw json.RawMessage, env filterEnv) bool {
	if len(raw) == 0 {
		return true
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		for _, f := range list {
			if !evalFilter(f, env) {
				return false
			}
		}
		return true
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	for _, key := range []string{"all_of", "AND"} {
		if v, ok := m[key]; ok {
			return evalFilter(v, env)
		}
	}
	for _, key := range []string{"any_of", "OR"} {
		if v, ok := m[key]; ok {
			var fs []json.RawMessage
			if json.Unmarshal(v, &fs) != nil {
				return evalFilter(v, env)
			}
			return slices.ContainsFunc(fs, func(f json.RawMessage) bool { return evalFilter(f, env) })
		}
	}
	if v, ok := m["none_of"]; ok {
		var fs []json.RawMessage
		if json.Unmarshal(v, &fs) != nil {
			return !evalFilter(v, env)
		}
		return !slices.ContainsFunc(fs, func(f json.RawMessage) bool { return evalFilter(f, env) })
	}
	var t struct {
		Test     string          `json:"test"`
		Subject  json.RawMessage `json:"subject"`
		Operator json.RawMessage `json:"operator"`
		Value    json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &t) != nil || t.Test == "" {
		return false
	}
	if t.Test == "has_biome_tag" {
		var tag string
		_ = json.Unmarshal(t.Value, &tag)
		b := env.biome
		if b == nil && env.self != nil {
			b = env.tx.Biome(cube.PosFromVec3(env.self.Position()))
		}
		if b == nil {
			return false
		}
		has := slices.Contains(b.Tags(), tag)
		if o := operatorOf(t.Operator); o == "!=" || o == "not" {
			return !has
		}
		return has
	}
	got, ok := testValue(t.Test, subjectOf(t.Subject, env), env, t.Value)
	if !ok {
		return false
	}
	return compare(got, t.Value, operatorOf(t.Operator))
}

// subjectOf reads the subject, which BDS's packed files write as a number:
// 0 is self, 1 is other.
func subjectOf(raw json.RawMessage, env filterEnv) world.Entity {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var n int
		if json.Unmarshal(raw, &n) == nil && n == 1 {
			s = "other"
		}
	}
	if s == "other" || s == "target" {
		if s == "target" && env.self != nil {
			e, _ := env.self.Target()
			return e
		}
		return env.other
	}
	if env.self == nil {
		return nil
	}
	return env.self
}

// operatorOf reads the operator, which BDS's packed files write as a
// number: 0 is ==, 1 is !=.
func operatorOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n int
	if json.Unmarshal(raw, &n) == nil && n == 1 {
		return "!="
	}
	return "=="
}

func familiesOf(e world.Entity) []string {
	switch v := e.(type) {
	case *Mob:
		return v.Families()
	case nil:
		return nil
	}
	if e.H().Type().EncodeEntity() == "minecraft:player" {
		return []string{"player"}
	}
	return nil
}

// testValue returns the value of a test for subject. The value passed is
// the one the filter compares against, for tests that take an argument.
func testValue(test string, subject world.Entity, env filterEnv, arg json.RawMessage) (any, bool) {
	if subject == nil {
		return nil, false
	}
	mob, _ := subject.(*Mob)
	switch test {
	case "is_family":
		var f string
		_ = json.Unmarshal(arg, &f)
		return slices.Contains(familiesOf(subject), f), true
	case "is_baby":
		return mob != nil && mob.Baby(), true
	case "on_ground":
		return mob != nil && mob.OnGround(), true
	case "in_water", "is_underwater", "in_water_or_rain":
		if mob != nil {
			return mob.s.inWater, true
		}
		_, ok := env.tx.Liquid(cube.PosFromVec3(subject.Position()))
		return ok, true
	case "in_lava":
		return mob != nil && mob.s.inLava, true
	case "is_daytime":
		t := env.tx.World().Time() % 24000
		return t >= 0 && t < 12000, true
	case "has_target":
		if mob == nil {
			return false, true
		}
		_, ok := mob.Target()
		return ok, true
	case "has_component":
		var c string
		_ = json.Unmarshal(arg, &c)
		if mob == nil {
			return false, true
		}
		_, ok := mob.s.t.def.componentsWith(mob.s.groups)[c]
		return ok, true
	case "actor_health":
		if mob == nil {
			return nil, false
		}
		return mob.Health(), true
	case "is_persistent":
		return mob != nil && mob.s.persistent, true
	case "distance_to_nearest_player":
		return nearestPlayerDist(env.tx, subject.Position()), true
	case "inactivity_timer":
		if mob == nil {
			return nil, false
		}
		// The test passes once the mob has been inactive for the seconds
		// given.
		var secs float64
		_ = json.Unmarshal(arg, &secs)
		return float64(mob.s.inactive) >= secs*20, true
	case "random_chance":
		if mob == nil {
			return nil, false
		}
		var n float64
		_ = json.Unmarshal(arg, &n)
		return n <= 1 || mob.s.rand.IntN(int(n)) == 0, true
	}
	return nil, false
}

func compare(got any, raw json.RawMessage, op string) bool {
	want := any(true)
	if len(raw) > 0 {
		if json.Unmarshal(raw, &want) != nil {
			return false
		}
	}
	switch g := got.(type) {
	case bool:
		w, ok := want.(bool)
		if !ok {
			// A test such as is_family takes its argument as the value and
			// always checks true.
			w = true
		}
		eq := g == w
		if op == "!=" || op == "not" || op == "<>" {
			return !eq
		}
		return eq
	case float64:
		w, ok := want.(float64)
		if !ok {
			return false
		}
		switch op {
		case "<":
			return g < w
		case "<=":
			return g <= w
		case ">":
			return g > w
		case ">=":
			return g >= w
		case "!=", "not", "<>":
			return g != w
		}
		return g == w
	}
	return fmt.Sprint(got) == fmt.Sprint(want)
}
