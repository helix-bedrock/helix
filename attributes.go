package helix

import (
	"math"

	"github.com/helix-bedrock/protocol"
)

// attributeDef is one of a player's attributes as BDS 1.26.60 defines it.
type attributeDef struct {
	name          string
	min, max, def float32
	saved         bool
}

// playerAttributes are a player's attributes in the order BDS 1.26.60
// sends them at join; saved marks the ones it keeps in the player's data.
var playerAttributes = [...]attributeDef{
	{"minecraft:health", 0, 20, 20, true},
	{"minecraft:follow_range", 0, 2048, 16, false},
	{"minecraft:knockback_resistance", -2, 1, 0, false},
	{"minecraft:movement", 0, math.MaxFloat32, 0.1, true},
	{"minecraft:underwater_movement", 0, math.MaxFloat32, 0.02, false},
	{"minecraft:lava_movement", 0, math.MaxFloat32, 0.02, false},
	{"minecraft:attack_damage", 1, 1, 1, true},
	{"minecraft:absorption", 0, 16, 0, false},
	{"minecraft:luck", -1024, 1024, 0, false},
	{"minecraft:friction_modifier", 0, 256, 1, false},
	{"minecraft:bounciness", 0, 1, 0, false},
	{"minecraft:air_drag_modifier", 0, 256, 1, false},
	{"minecraft:player.hunger", 0, 20, 20, true},
	{"minecraft:player.saturation", 0, 20, 5, true},
	{"minecraft:player.exhaustion", 0, 20, 0, true},
	{"minecraft:player.level", 0, 24791, 0, true},
	{"minecraft:player.experience", 0, 1, 0, true},
}

// attributeValue is an attribute's base and current value.
type attributeValue struct {
	base, current float32
}

// Attributes are a player's attribute values, in playerAttributes' order.
// Runs: owner of the player's entity.
type Attributes [len(playerAttributes)]attributeValue

// DefaultAttributes returns BDS's attributes of a new player.
func DefaultAttributes() Attributes {
	var a Attributes
	for i, d := range playerAttributes {
		a[i] = attributeValue{base: d.def, current: d.def}
	}
	return a
}

// load reads the values BDS saved in the player's "Attributes" list. A
// saved attribute without "Current" is at its base.
func (a *Attributes) load(data map[string]any) {
	list, ok := data["Attributes"].([]any)
	if !ok {
		return
	}
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["Name"].(string)
		base, ok := m["Base"].(float32)
		if !ok {
			continue
		}
		for i, d := range playerAttributes {
			if d.name != name {
				continue
			}
			cur, ok := m["Current"].(float32)
			if !ok {
				cur = base
			}
			a[i] = attributeValue{base: base, current: cur}
		}
	}
}

// save writes the attributes BDS saves, as it writes them, into the
// player's data. A saved list is kept as it is (with any modifiers) while
// Helix changes no attribute.
func (a *Attributes) save(data map[string]any) {
	if _, ok := data["Attributes"]; ok {
		return
	}
	list := make([]any, 0, len(playerAttributes))
	for i, d := range playerAttributes {
		if !d.saved {
			continue
		}
		m := map[string]any{"Name": d.name, "Base": a[i].base, "DefaultMin": d.min, "DefaultMax": d.max}
		if a[i].current != a[i].base {
			m["Current"] = a[i].current
		}
		list = append(list, m)
	}
	data["Attributes"] = list
}

// list returns the attributes as UpdateAttributes carries them.
func (a *Attributes) list() []protocol.Attribute {
	out := make([]protocol.Attribute, len(playerAttributes))
	for i, d := range playerAttributes {
		out[i] = protocol.Attribute{
			AttributeValue: protocol.AttributeValue{Name: d.name, Value: a[i].current, Min: d.min, Max: d.max},
			DefaultMin:     d.min, DefaultMax: d.max, Default: d.def,
		}
	}
	return out
}
