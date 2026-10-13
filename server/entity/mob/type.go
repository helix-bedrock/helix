package mob

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/world"
)

// Type is the world.EntityType of one kind of mob, such as zombies.
type Type struct {
	def *Def
}

// Types returns an entity type for every mob definition, sorted by
// identifier, leaving out the identifiers in skip (entities Helix
// implements itself, such as players and items).
func Types(defs map[string]*Def, skip func(id string) bool) []*Type {
	var out []*Type
	for id, d := range defs {
		if !d.IsMob() || skip(id) {
			continue
		}
		out = append(out, &Type{def: d})
	}
	slices.SortFunc(out, func(a, b *Type) int { return strings.Compare(a.def.ID, b.def.ID) })
	return out
}

// Def returns the definition of the mob.
func (t *Type) Def() *Def { return t.def }

// EncodeEntity returns the identifier, such as "minecraft:zombie".
func (t *Type) EncodeEntity() string { return t.def.ID }

// Open opens a mob in a transaction.
func (t *Type) Open(tx *world.Tx, handle *world.EntityHandle, data *world.EntityData) world.Entity {
	s, ok := data.Data.(*state)
	if !ok {
		s = t.newState(seedOf(data.Pos), t.spawnGroups(nil, nil))
		data.Data = s
	}
	return &Mob{tx: tx, handle: handle, data: data, s: s}
}

// BBox returns the mob's collision box, scaled with it.
func (t *Type) BBox(e world.Entity) cube.BBox {
	s := t.def.spec(nil)
	if m, ok := e.(*Mob); ok {
		s = m.s.spec
	}
	w, h := s.width*s.scale/2, s.height*s.scale
	return cube.Box(-w, 0, -w, w, h, w)
}

// Spawn returns a new mob of the type at pos, with the component groups
// its spawn event picks, ready to be added to a world.
func (t *Type) Spawn(pos mgl64.Vec3, yaw float64) *world.EntityHandle {
	id := uuid.New()
	clear(id[:8])
	seed := [2]uint64{binary.LittleEndian.Uint64(id[8:]), 0x9e3779b97f4a7c15}
	return world.EntitySpawnOpts{Position: pos, Rotation: cube.Rotation{yaw, 0}, ID: id}.New(t, spawnConfig{t: t, seed: seed})
}

type spawnConfig struct {
	t    *Type
	seed [2]uint64
}

func (c spawnConfig) Apply(data *world.EntityData) {
	r := rand.New(rand.NewPCG(c.seed[1], c.seed[0]))
	s := c.t.newState(c.seed, c.t.spawnGroups(nil, r.Float64))
	s.headYaw = data.Rot.Yaw()
	data.Data = s
}

// spawnGroups runs the minecraft:entity_spawned event on groups.
func (t *Type) spawnGroups(groups []string, rnd func() float64) []string {
	if rnd == nil {
		rnd = func() float64 { return 0.5 }
	}
	r := &eventRun{def: t.def, groups: groups, rand: rnd, filter: func(json.RawMessage) bool { return false }}
	r.fire("minecraft:entity_spawned")
	return r.groups
}

func seedOf(pos mgl64.Vec3) [2]uint64 {
	return [2]uint64{math.Float64bits(pos[0]) ^ math.Float64bits(pos[1])<<1, math.Float64bits(pos[2])}
}

// DecodeNBT reads a saved mob: its component groups, in vanilla's
// "definitions" list, and its health.
func (t *Type) DecodeNBT(m map[string]any, data *world.EntityData) {
	var groups []string
	saved := false
	if defs, ok := m["definitions"].([]any); ok {
		saved = true
		for _, d := range defs {
			name, _ := d.(string)
			g, ok := strings.CutPrefix(name, "+")
			if ok && g != t.def.ID {
				if _, known := t.def.groups[g]; known {
					groups = append(groups, g)
				}
			}
		}
	}
	seed := seedOf(data.Pos)
	if !saved {
		groups = t.spawnGroups(nil, rand.New(rand.NewPCG(seed[1], seed[0])).Float64)
	}
	s := t.newState(seed, groups)
	if h, ok := m["Health"].(float32); ok {
		s.health.AddHealth(float64(h) - s.health.Health())
	}
	s.persistent, _ = m["Persistent"].(bool)
	if b, ok := m["Persistent"].(uint8); ok {
		s.persistent = b == 1
	}
	s.headYaw = data.Rot.Yaw()
	data.Data = s
}

// EncodeNBT saves a mob.
func (t *Type) EncodeNBT(data *world.EntityData) map[string]any {
	s, ok := data.Data.(*state)
	if !ok {
		return nil
	}
	defs := []any{"+" + t.def.ID}
	for _, g := range s.groups {
		defs = append(defs, "+"+g)
	}
	persistent := uint8(0)
	if s.persistent {
		persistent = 1
	}
	return map[string]any{
		"definitions": defs,
		"Health":      float32(s.health.Health()),
		"Persistent":  persistent,
	}
}

// Registry returns reg with every vanilla mob added. Entities reg already
// has, such as items, keep their own types, and players are never mobs.
func Registry(reg world.EntityRegistry) (world.EntityRegistry, error) {
	defs, err := LoadDefs()
	if err != nil {
		return reg, err
	}
	types := reg.Types()
	for _, t := range Types(defs, func(id string) bool {
		_, ok := reg.Lookup(id)
		return ok || id == "minecraft:player"
	}) {
		types = append(types, t)
	}
	return reg.Config().New(types), nil
}
