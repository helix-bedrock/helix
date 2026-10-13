// Package mob implements every vanilla mob from the definitions BDS uses
// (Helix). A mob's health, speed, size, families, loot and behaviours come
// from its definition file in helix-data; the behaviours (goals) and the
// pathfinding are ported from Pumpkin (github.com/Pumpkin-MC/Pumpkin,
// GPL-3.0).
//
// A mob runs on the region it is in, like every entity: all of its methods
// must be called from a transaction of that region.
package mob

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/helix-bedrock/helix-data/data"
)

// Def is a mob definition as BDS reads it: components that every mob of
// the kind has, component groups that events add and remove, and events.
type Def struct {
	// ID is the entity identifier, such as "minecraft:zombie".
	ID string
	// Summonable and Spawnable say whether /summon and spawn eggs may make
	// the mob.
	Summonable, Spawnable bool
	// SpawnCategory is "monster", "creature" and so on.
	SpawnCategory string

	components map[string]json.RawMessage
	groups     map[string]map[string]json.RawMessage
	events     map[string]json.RawMessage
	loot       map[string]json.RawMessage

	// specs caches the parsed components per set of groups.
	specs sync.Map
}

type defFile struct {
	Entity struct {
		Description struct {
			Identifier    string `json:"identifier"`
			Summonable    bool   `json:"is_summonable"`
			Spawnable     bool   `json:"is_spawnable"`
			SpawnCategory string `json:"spawn_category"`
		} `json:"description"`
		Components map[string]json.RawMessage            `json:"components"`
		Groups     map[string]map[string]json.RawMessage `json:"component_groups"`
		Events     map[string]json.RawMessage            `json:"events"`
	} `json:"minecraft:entity"`
}

// LoadDefs reads every entity definition and loot table from helix-data.
func LoadDefs() (map[string]*Def, error) {
	ents, err := data.Entities()
	if err != nil {
		return nil, fmt.Errorf("mob: %w", err)
	}
	loot, err := data.EntityLoot()
	if err != nil {
		return nil, fmt.Errorf("mob: %w", err)
	}
	defs := make(map[string]*Def, len(ents))
	for id, raw := range ents {
		var f defFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("mob: definition of %s: %w", id, err)
		}
		e := f.Entity
		defs[id] = &Def{
			ID:            id,
			Summonable:    e.Description.Summonable,
			Spawnable:     e.Description.Spawnable,
			SpawnCategory: e.Description.SpawnCategory,
			components:    orEmpty(e.Components),
			groups:        e.Groups,
			events:        e.Events,
			loot:          loot,
		}
	}
	return defs, nil
}

func orEmpty(m map[string]json.RawMessage) map[string]json.RawMessage {
	if m == nil {
		return map[string]json.RawMessage{}
	}
	return m
}

// IsMob reports whether the definition describes a mob, rather than an
// object such as a boat: it moves on its own or has behaviours.
func (d *Def) IsMob() bool {
	has := func(c map[string]json.RawMessage) bool {
		for name := range c {
			if strings.HasPrefix(name, "minecraft:behavior.") || strings.HasPrefix(name, "minecraft:navigation.") {
				return true
			}
		}
		return false
	}
	if has(d.components) {
		return true
	}
	for _, g := range d.groups {
		if has(g) {
			return true
		}
	}
	return false
}

// componentsWith returns the components of a mob with groups added, later
// groups overriding earlier ones and the base components, as BDS does.
func (d *Def) componentsWith(groups []string) map[string]json.RawMessage {
	out := maps.Clone(d.components)
	for _, g := range groups {
		maps.Copy(out, d.groups[g])
	}
	return out
}

// spec returns the parsed components for a set of groups.
func (d *Def) spec(groups []string) *spec {
	key := strings.Join(groups, "\x00")
	if s, ok := d.specs.Load(key); ok {
		return s.(*spec)
	}
	s := parseSpec(d, d.componentsWith(groups))
	d.specs.Store(key, s)
	return s
}

// eventNode is one step of an event.
type eventNode struct {
	Add        *groupList        `json:"add"`
	Remove     *groupList        `json:"remove"`
	Randomize  []weightedNode    `json:"randomize"`
	Sequence   []json.RawMessage `json:"sequence"`
	FirstValid []json.RawMessage `json:"first_valid"`
	Trigger    json.RawMessage   `json:"trigger"`
	Filters    json.RawMessage   `json:"filters"`
}

type groupList struct {
	Groups []string `json:"component_groups"`
}

type weightedNode struct {
	Weight float64 `json:"weight"`
	json.RawMessage
}

func (w *weightedNode) UnmarshalJSON(b []byte) error {
	var v struct {
		Weight float64 `json:"weight"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	w.Weight, w.RawMessage = v.Weight, slices.Clone(b)
	return nil
}

// eventRun applies events to a mob's groups.
type eventRun struct {
	def    *Def
	groups []string
	rand   func() float64
	filter func(json.RawMessage) bool
	depth  int
}

// fire runs the event called name. Unknown events do nothing.
func (r *eventRun) fire(name string) {
	raw, ok := r.def.events[name]
	if !ok || r.depth > 16 {
		return
	}
	r.depth++
	r.node(raw)
	r.depth--
}

// node runs one event node and reports whether its filters passed.
func (r *eventRun) node(raw json.RawMessage) bool {
	var n eventNode
	if json.Unmarshal(raw, &n) != nil {
		return false
	}
	if len(n.Filters) > 0 && !r.filter(n.Filters) {
		return false
	}
	if n.Remove != nil {
		r.groups = slices.DeleteFunc(r.groups, func(g string) bool { return slices.Contains(n.Remove.Groups, g) })
	}
	if n.Add != nil {
		for _, g := range n.Add.Groups {
			if _, ok := r.def.groups[g]; ok {
				r.groups = append(slices.DeleteFunc(r.groups, func(o string) bool { return o == g }), g)
			}
		}
	}
	if len(n.Randomize) > 0 {
		var total float64
		for _, w := range n.Randomize {
			total += w.Weight
		}
		pick := r.rand() * total
		for _, w := range n.Randomize {
			if pick -= w.Weight; pick < 0 {
				r.node(w.RawMessage)
				break
			}
		}
	}
	for _, s := range n.Sequence {
		r.node(s)
	}
	for _, s := range n.FirstValid {
		if r.node(s) {
			break
		}
	}
	if len(n.Trigger) > 0 {
		var name string
		if json.Unmarshal(n.Trigger, &name) != nil {
			var t struct {
				Event  string `json:"event"`
				Target string `json:"target"`
			}
			if json.Unmarshal(n.Trigger, &t) == nil && (t.Target == "" || t.Target == "self") {
				name = t.Event
			}
		}
		r.fire(name)
	}
	return true
}
