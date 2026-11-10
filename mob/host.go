package mob

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
)

// The Mob is its definition's Host.

// NameTag is the mob's name tag; none yet.
func (m *Mob) NameTag() string { return "" }

// NextFloat draws from the region's random.
func (m *Mob) NextFloat() float32 {
	if m.v.rnd == nil {
		return 0
	}
	return m.v.rnd.NextFloat()
}

// BeforeEvent lets every event run; the scripting event is not hooked up yet.
func (m *Mob) BeforeEvent(string, definition.Tester) bool { return true }

// Respond runs a response other than set_property. play_sound and the rest are not hooked up
// yet; they are logged once at debug.
func (m *Mob) Respond(r definition.Response, _ definition.Tester) {
	m.rt.Log.Debug("mob event response not run", "type", m.typ, "response", r.Name)
}

// Trigger runs an event on another subject. Only self is hooked up so far.
func (m *Mob) Trigger(t *definition.Trigger, ctx definition.Tester, g *definition.Guard) {
	if t.Target == definition.SubjectSelf {
		m.inst.Trigger(t.Event, ctx, g)
	}
}

// Molang is not hooked up: property defaults written in Molang keep their type's zero.
func (m *Mob) Molang(string, definition.Tester) (definition.Value, bool) {
	return definition.Value{}, false
}

// resolver is the component.Resolver of one mob.
type resolver struct{ m *Mob }

// Sound has no sound table yet: goals play no sounds.
func (resolver) Sound(string) int { return behavior.NoSound }

// DamageCause names BDS's actor damage causes the goals use.
func (resolver) DamageCause(name string) (int, bool) {
	c, ok := damageCauses[name]
	return c, ok
}

// damageCauses are BDS's ActorDamageCause numbers by JSON name.
var damageCauses = map[string]int{
	"none": -1, "override": 0, "contact": 1, "entity_attack": 2, "projectile": 3, "suffocation": 4,
	"fall": 5, "fire": 6, "fire_tick": 7, "lava": 8, "drowning": 9, "block_explosion": 10,
	"entity_explosion": 11, "void": 12, "self_destruct": 13, "magic": 14, "wither": 15,
	"starve": 16, "anvil": 17, "thorns": 18, "falling_block": 19, "piston": 20,
	"fly_into_wall": 21, "magma": 22, "fireworks": 23, "lightning": 24, "charging": 25,
	"temperature": 26, "freezing": 27, "stalactite": 28, "stalagmite": 29, "ram_attack": 30,
	"sonic_boom": 31, "campfire": 32, "soul_campfire": 33, "mace_smash": 34, "all": 35,
}

func (r resolver) Tester(self, other behavior.Actor, _ *behavior.Pos) definition.Tester {
	return tester{m: r.m, self: self, other: other}
}

// HoldsItem is not hooked up yet: tempt and the other item goals never see their item.
func (resolver) HoldsItem(behavior.Actor, []string) bool { return false }

func (resolver) Molang(string) (func(behavior.Mob) float32, bool) { return nil, false }

func (m *Mob) tester(other behavior.Actor) tester { return tester{m: m, self: m, other: other} }

// tester runs the filter tests the minimal hookup knows. A test it does not know fails, as
// if its subject could not answer; the audit will port the rest from BDS.
type tester struct {
	m           *Mob
	self, other behavior.Actor
}

func (t tester) subject(s definition.Subject) behavior.Actor {
	switch s {
	case definition.SubjectSelf:
		return t.self
	case definition.SubjectOther, definition.SubjectDamager:
		return t.other
	case definition.SubjectTarget:
		if t.m.target != behavior.NoID && t.m.v.r != nil {
			return t.m.rt.Env.Actor(t.m.v.r, t.m.target)
		}
	}
	return nil
}

// Test runs one filter test.
func (t tester) Test(test *definition.Test) (pass, resolved bool) {
	a := t.subject(test.Subject)
	if a == nil {
		return false, false
	}
	m, _ := a.(*Mob)
	switch test.Name {
	case "is_family":
		v := rawString(test.Value)
		return definition.CompareBool(test.Operator, slices.Contains(families(a), v), true), true
	case "has_component":
		if m == nil {
			return false, true
		}
		_, has := m.inst.Components()[ns(rawString(test.Value))]
		return definition.CompareBool(test.Operator, has, rawBool(test.Value, true)), true
	case "in_water", "in_water_or_rain":
		return definition.CompareBool(test.Operator, m != nil && m.InWater(), rawBool(test.Value, true)), true
	case "is_underwater":
		return definition.CompareBool(test.Operator, m != nil && m.UnderWater(), rawBool(test.Value, true)), true
	case "in_lava":
		return definition.CompareBool(test.Operator, m != nil && m.InLava(), rawBool(test.Value, true)), true
	case "on_ground":
		return definition.CompareBool(test.Operator, m != nil && m.OnGround(), rawBool(test.Value, true)), true
	case "has_target":
		return definition.CompareBool(test.Operator, m != nil && m.target != behavior.NoID, rawBool(test.Value, true)), true
	case "is_baby":
		return definition.CompareBool(test.Operator, a.Flag(behavior.FlagBaby), rawBool(test.Value, true)), true
	case "is_daytime":
		return definition.CompareBool(test.Operator, t.m.v.r != nil && t.m.rt.Env.SkyDarken(t.m.v.r) < 4, rawBool(test.Value, true)), true
	case "is_difficulty":
		want, ok := difficulties[rawString(test.Value)]
		return ok && definition.Compare(test.Operator, t.m.rt.Env.Difficulty(), want), true
	case "actor_health":
		if m == nil {
			return false, true
		}
		return definition.Compare(test.Operator, m.health, rawFloat(test.Value)), true
	case "bool_property", "enum_property", "int_property", "float_property":
		return t.property(test, m)
	}
	return false, true
}

func (t tester) property(test *definition.Test, m *Mob) (bool, bool) {
	if m == nil {
		return false, true
	}
	v, ok := m.inst.Property(rawString(test.Domain))
	if !ok {
		return false, true
	}
	switch test.Name {
	case "bool_property":
		return definition.CompareBool(test.Operator, v.Number != 0, rawBool(test.Value, true)), true
	case "enum_property":
		eq := v.String == rawString(test.Value)
		return definition.CompareBool(test.Operator, eq, true), true
	}
	return definition.Compare(test.Operator, v.Number, rawFloat(test.Value)), true
}

var difficulties = map[string]int32{"peaceful": 0, "easy": 1, "normal": 2, "hard": 3}

// families are an actor's type families: a mob's from its definition, "player" for players.
func families(a behavior.Actor) []string {
	if m, ok := a.(*Mob); ok {
		return m.families
	}
	if a.IsPlayer() {
		return []string{"player", "mob"}
	}
	return nil
}

func ns(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return "minecraft:" + name
}

func rawString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func rawBool(raw json.RawMessage, def bool) bool {
	if raw == nil {
		return def
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	return def
}

func rawFloat(raw json.RawMessage) float32 {
	var f float32
	_ = json.Unmarshal(raw, &f)
	return f
}
