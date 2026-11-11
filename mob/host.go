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

// Molang evaluates a property default or set_property value (molang.go) for this mob.
func (m *Mob) Molang(expr string, _ definition.Tester) (definition.Value, bool) {
	e, err := parseMolang(expr)
	if err != nil {
		m.rt.Log.Debug("mob molang", "type", m.typ, "expr", expr, "err", err)
		return definition.Value{}, false
	}
	return definition.Value{Number: e.eval(m)}, true
}

// resolver is the component.Resolver of one mob.
type resolver struct{ m *Mob }

// Sound is a level sound event's number by name, as BDS 1.26.60 numbers them.
func (r resolver) Sound(name string) int {
	if n, ok := r.m.rt.sounds[strings.TrimPrefix(name, "minecraft:")]; ok {
		return n
	}
	return behavior.NoSound
}

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

func (r resolver) Tester(self, other behavior.Actor, pos *behavior.Pos) definition.Tester {
	t := tester{m: r.m, self: self, other: other}
	if pos != nil {
		t.pos, t.hasPos = *pos, true
	}
	return t
}

// HoldsItem reports a player holding one of the items in its main hand or offhand, for tempt
// and the other goals that follow an item. Mobs hold nothing yet.
func (resolver) HoldsItem(a behavior.Actor, items []string) bool {
	p, ok := a.(PlayerInfo)
	if !ok {
		return false
	}
	var buf [2]string
	held := p.Equipment("main_hand", buf[:0])
	held = p.Equipment("offhand", held)
	for _, h := range held {
		if slices.ContainsFunc(items, func(d string) bool { return ns(d) == h }) {
			return true
		}
	}
	return false
}

// Molang compiles an expression (molang.go). It is evaluated for the mob the resolver is for:
// every Molang the components hold is about the mob itself.
func (r resolver) Molang(expr string) (func(behavior.Mob) float32, bool) {
	e, err := parseMolang(expr)
	if err != nil {
		r.m.rt.Log.Debug("mob molang", "type", r.m.typ, "expr", expr, "err", err)
		return nil, false
	}
	return molangFunc{e: e, m: r.m}.eval, true
}

func (m *Mob) tester(other behavior.Actor) tester { return tester{m: m, self: m, other: other} }

// tester runs the filter tests (more in tests.go). A test it does not know fails, as if its
// subject could not answer; the audit will port the rest from BDS.
type tester struct {
	m           *Mob
	self, other behavior.Actor
	// pos is the block subject, when hasPos is set.
	pos    behavior.Pos
	hasPos bool
}

func (t tester) subject(s definition.Subject) behavior.Actor {
	switch s {
	case definition.SubjectSelf:
		return t.self
	case definition.SubjectOther, definition.SubjectDamager:
		return t.other
	case definition.SubjectPlayer:
		if t.other != nil && t.other.IsPlayer() {
			return t.other
		}
	case definition.SubjectTarget:
		if t.m.target != behavior.NoID && t.m.v.r != nil {
			return t.m.rt.Env.Actor(t.m.v.r, t.m.target)
		}
	}
	return nil
}

// Test runs one filter test.
func (t tester) Test(test *definition.Test) (pass, resolved bool) {
	if test.Subject == definition.SubjectBlock {
		return t.blockTest(test)
	}
	a := t.subject(test.Subject)
	if a == nil {
		return false, false
	}
	// State components see the mob as stateMob; its tests are about the mob itself.
	if sm, ok := a.(stateMob); ok {
		a = sm.Mob
	}
	// The mob itself is read live; another mob through its snapshot, as it may be thinking
	// on another core.
	m, _ := a.(*Mob)
	s, _ := a.(*Snapshot)
	if m != nil {
		s = nil
	}
	switch test.Name {
	case "is_family":
		v := rawString(test.Value)
		return definition.CompareBool(test.Operator, slices.Contains(families(a), v), true), true
	case "has_component":
		var comps definition.Group
		switch {
		case m != nil:
			comps = m.inst.Components()
		case s != nil:
			comps = s.comps
		default:
			return false, true
		}
		_, has := comps[ns(rawString(test.Value))]
		return definition.CompareBool(test.Operator, has, rawBool(test.Value, true)), true
	case "in_water", "in_water_or_rain":
		v := m != nil && m.InWater() || s != nil && s.inWater
		return definition.CompareBool(test.Operator, v, rawBool(test.Value, true)), true
	case "is_underwater":
		v := m != nil && m.UnderWater() || s != nil && s.underWater
		return definition.CompareBool(test.Operator, v, rawBool(test.Value, true)), true
	case "in_lava":
		v := m != nil && m.InLava() || s != nil && s.inLava
		return definition.CompareBool(test.Operator, v, rawBool(test.Value, true)), true
	case "on_ground":
		v := m != nil && m.OnGround() || s != nil && s.onGround
		return definition.CompareBool(test.Operator, v, rawBool(test.Value, true)), true
	case "has_target":
		return definition.CompareBool(test.Operator, a.Target() != behavior.NoID, rawBool(test.Value, true)), true
	case "is_baby":
		return definition.CompareBool(test.Operator, a.Flag(behavior.FlagBaby), rawBool(test.Value, true)), true
	case "is_daytime":
		return definition.CompareBool(test.Operator, t.m.v.r != nil && t.m.rt.Env.SkyDarken(t.m.v.r) < 4, rawBool(test.Value, true)), true
	case "is_difficulty":
		want, ok := difficulties[rawString(test.Value)]
		return ok && definition.Compare(test.Operator, t.m.rt.Env.Difficulty(), want), true
	case "actor_health":
		switch {
		case m != nil:
			return definition.Compare(test.Operator, m.health, rawFloat(test.Value)), true
		case s != nil:
			return definition.Compare(test.Operator, s.health, rawFloat(test.Value)), true
		}
		return false, true
	case "bool_property", "enum_property", "int_property", "float_property":
		// Another mob's properties are not in its snapshot yet: those tests fail.
		return t.property(test, m)
	}
	return t.more(test, a, m, s)
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
	if s, ok := a.(*Snapshot); ok {
		return s.families
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
