package village

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Villages are saved as BDS 1.26.60 saves them: five level keys per village,
// VILLAGE_<dimension>_<uuid>_ and then INFO (0xBEECA90), DWELLERS (0xBEEE570), POI
// (0xBEEF2F0), PLAYERS (0xBEEDDE0) and RAID (0xBEEFF30, deleted while there is no raid).
// The dwellers keep the village's UUID in their own save (DwellerComponent, 0x723F7E0).
//
// Only claimed POIs are saved, as in BDS: free ones are found again by the scans. Not
// modelled, so saved as BDS's defaults: the village's MTick, PDTick, BDTime and GDTime (0),
// the raid box (the POI box), player standings (none) and last_worked.

// Store is where villages are saved: a world's level keys. mcdb.WorldStore is one.
type Store interface {
	LoadLevel(key string) ([]byte, error)
	StoreLevel(key string, value []byte) error
	DeleteLevel(key string) error
	ForEachLevel(prefix string, fn func(key string, value []byte) bool) error
}

// The key suffixes.
const (
	keyInfo     = "INFO"
	keyDwellers = "DWELLERS"
	keyPOI      = "POI"
	keyPlayers  = "PLAYERS"
	keyRaid     = "RAID"
)

// saveVersion is INFO's Version byte.
const saveVersion = 1

// The dweller roles, in BDS's order (DwellerRole; the role names are registered in that
// order at 0x734EA00): DWELLERS holds one list per role.
const (
	roleInhabitant = iota
	roleDefender
	roleHostile
	rolePassive
	roles
)

// roleIndex is a dweller_role name's index, inhabitant for an unknown one.
func roleIndex(name string) uint8 {
	switch name {
	case "defender":
		return roleDefender
	case "hostile":
		return roleHostile
	case "passive":
		return rolePassive
	}
	return roleInhabitant
}

func villageKey(dim mcdb.Dimension, id uuid.UUID, suffix string) string {
	return mcdb.LevelPrefixVillageIn(dim) + id.String() + "_" + suffix
}

// Save writes every village of the manager to the store and deletes the keys of villages
// removed since the last save. Runs: anywhere; it takes the lock while it encodes.
func (m *Manager) Save(store Store, dim mcdb.Dimension) error {
	type record struct {
		key   string
		value []byte
	}
	m.mu.Lock()
	var recs []record
	var errs []error
	for _, v := range m.villages {
		for _, part := range []struct {
			suffix string
			tag    map[string]any
		}{
			{keyInfo, m.infoTag(v)},
			{keyDwellers, dwellersTag(v)},
			{keyPOI, poiTag(v)},
			{keyPlayers, map[string]any{"Players": []any{}}},
		} {
			raw, err := nbt.MarshalEncoding(part.tag, nbt.LittleEndian)
			if err != nil {
				errs = append(errs, fmt.Errorf("village %v %s: %w", v.ID, part.suffix, err))
				continue
			}
			recs = append(recs, record{villageKey(dim, v.ID, part.suffix), raw})
		}
	}
	gone := m.removed
	m.removed = nil
	ids := make([]uuid.UUID, 0, len(m.villages))
	for _, v := range m.villages {
		ids = append(ids, v.ID)
	}
	m.mu.Unlock()
	for _, r := range recs {
		if err := store.StoreLevel(r.key, r.value); err != nil {
			errs = append(errs, err)
		}
	}
	for _, id := range ids {
		if err := store.DeleteLevel(villageKey(dim, id, keyRaid)); err != nil && !errors.Is(err, mcdb.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	for _, id := range gone {
		for _, s := range []string{keyInfo, keyDwellers, keyPOI, keyPlayers, keyRaid} {
			if err := store.DeleteLevel(villageKey(dim, id, s)); err != nil && !errors.Is(err, mcdb.ErrNotFound) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// infoTag is INFO: the ticks, the POI box and the raid box as ints, Initialized and
// Version.
func (m *Manager) infoTag(v *Village) map[string]any {
	x0, y0, z0 := int32(v.min[0]), int32(v.min[1]), int32(v.min[2])       //nolint:gosec // world positions fit
	x1, y1, z1 := int32(v.max[0]+1), int32(v.max[1]+1), int32(v.max[2]+1) //nolint:gosec // world positions fit
	return map[string]any{
		"Tick": m.now, "MTick": int64(0), "PDTick": int64(0), "BDTime": int64(0), "GDTime": int64(0),
		"Initialized": uint8(1),
		"X0":          x0, "Y0": y0, "Z0": z0, "X1": x1, "Y1": y1, "Z1": z1,
		"RX0": x0, "RY0": y0, "RZ0": z0, "RX1": x1, "RY1": y1, "RZ1": z1,
		"Version": uint8(saveVersion),
	}
}

// dwellersTag is DWELLERS: one {actors} compound per role, each actor its ID, the tick it
// was last near (TS) and where it last was.
func dwellersTag(v *Village) map[string]any {
	lists := make([]any, roles)
	for r := range roles {
		actors := []any{}
		for _, mem := range v.members {
			if int(mem.role) != r {
				continue
			}
			actors = append(actors, map[string]any{
				"ID": int64(mem.id), "TS": mem.seen,
				"last_saved_pos": []int32{int32(mem.pos[0]), int32(mem.pos[1]), int32(mem.pos[2])}, //nolint:gosec // world positions fit
			})
		}
		lists[r] = map[string]any{"actors": actors}
	}
	return map[string]any{"Dwellers": lists}
}

// poiTag is POI: for each dweller with a claim, its bed, bell and job site, a {Skip: 1}
// for a kind it has none of.
func poiTag(v *Village) map[string]any {
	list := []any{}
	for _, mem := range v.members {
		if mem.pois == [kinds]*POI{} {
			continue
		}
		inst := make([]any, kinds)
		for k, p := range mem.pois {
			if p == nil {
				inst[k] = map[string]any{"Skip": uint8(1)}
				continue
			}
			inst[k] = poiInstanceTag(p)
		}
		list = append(list, map[string]any{"VillagerID": int64(mem.id), "instances": inst})
	}
	return map[string]any{"POI": list}
}

// poiInstanceTag is POIInstance::save (0xBEDE800).
func poiInstanceTag(p *POI) map[string]any {
	return map[string]any{
		"X": int32(p.Pos[0]), "Y": int32(p.Pos[1]), "Z": int32(p.Pos[2]), //nolint:gosec // world positions fit
		"Name": p.Def.Name, "InitEvent": p.Def.InitEvent, "Type": int32(p.Def.Kind),
		"UseAABB": boolByte(p.Def.UseBox), "Radius": p.Def.Radius,
		"OwnerCount": int64(len(p.owners)), "Capacity": int64(p.Def.Capacity), "Weight": int64(p.Def.Weight),
		"SoundEvent": p.Def.Sound, "Skip": uint8(0),
	}
}

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// saved is one village's keys as read.
type saved struct {
	info, dwellers, poi map[string]any
}

// Load reads the dimension's villages from the store (BDS scans the VILLAGE_ prefix when a
// dimension opens). Villages the manager already has are kept. Runs: before the dimension
// ticks.
func (m *Manager) Load(store Store, dim mcdb.Dimension) error {
	prefix := mcdb.LevelPrefixVillageIn(dim)
	byID := map[uuid.UUID]*saved{}
	var order []uuid.UUID
	var errs []error
	err := store.ForEachLevel(prefix, func(key string, value []byte) bool {
		id, suffix, ok := splitKey(strings.TrimPrefix(key, prefix))
		if !ok {
			return true
		}
		var tag map[string]any
		if err := nbt.UnmarshalEncoding(value, &tag, nbt.LittleEndian); err != nil {
			errs = append(errs, fmt.Errorf("village key %q: %w", key, err))
			return true
		}
		s := byID[id]
		if s == nil {
			s = &saved{}
			byID[id] = s
			order = append(order, id)
		}
		switch suffix {
		case keyInfo:
			s.info = tag
		case keyDwellers:
			s.dwellers = tag
		case keyPOI:
			s.poi = tag
		}
		return true
	})
	if err != nil {
		errs = append(errs, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range order {
		s := byID[id]
		if s.info == nil || m.village(id) != nil {
			continue
		}
		m.villages = append(m.villages, m.restore(id, s))
	}
	m.reindex()
	return errors.Join(errs...)
}

// splitKey splits "<uuid>_<suffix>".
func splitKey(rest string) (uuid.UUID, string, bool) {
	const n = 36
	if len(rest) < n+2 || rest[n] != '_' {
		return uuid.UUID{}, "", false
	}
	id, err := uuid.Parse(rest[:n])
	if err != nil {
		return uuid.UUID{}, "", false
	}
	return id, rest[n+1:], true
}

// village is the village with the ID. Call with the lock held.
func (m *Manager) village(id uuid.UUID) *Village {
	for _, v := range m.villages {
		if v.ID == id {
			return v
		}
	}
	return nil
}

// restore builds a village from its saved keys. Call with the lock held.
func (m *Manager) restore(id uuid.UUID, s *saved) *Village {
	v := &Village{ID: id}
	v.min = cube.Pos{int(tagInt(s.info["X0"])), int(tagInt(s.info["Y0"])), int(tagInt(s.info["Z0"]))}
	v.max = cube.Pos{int(tagInt(s.info["X1"])) - 1, int(tagInt(s.info["Y1"])) - 1, int(tagInt(s.info["Z1"])) - 1}
	lists, _ := s.dwellers["Dwellers"].([]any)
	for r, l := range lists {
		c, _ := l.(map[string]any)
		actors, _ := c["actors"].([]any)
		for _, a := range actors {
			t, ok := a.(map[string]any)
			if !ok {
				continue
			}
			mem := &member{id: behavior.ID(tagInt(t["ID"])), seen: tagInt(t["TS"]), role: uint8(min(r, roles-1))} //nolint:gosec // 0 to 3
			if p := tagInts(t["last_saved_pos"]); len(p) == 3 {
				mem.pos = cube.Pos{int(p[0]), int(p[1]), int(p[2])}
			}
			if v.member(mem.id) == nil {
				v.members = append(v.members, mem)
				m.byDweller[mem.id] = v
			}
		}
	}
	claims, _ := s.poi["POI"].([]any)
	for _, c := range claims {
		t, ok := c.(map[string]any)
		if !ok {
			continue
		}
		m.restoreClaims(v, behavior.ID(tagInt(t["VillagerID"])), t["instances"])
	}
	v.grow()
	v.publish()
	return v
}

// restoreClaims gives a saved dweller back its POIs, making each POI once.
func (m *Manager) restoreClaims(v *Village, id behavior.ID, raw any) {
	mem := v.member(id)
	if mem == nil {
		mem = &member{id: id}
		v.members = append(v.members, mem)
		m.byDweller[id] = v
	}
	inst, _ := raw.([]any)
	for k, i := range inst {
		t, ok := i.(map[string]any)
		if !ok || k >= kinds || tagInt(t["Skip"]) != 0 {
			continue
		}
		pos := cube.Pos{int(tagInt(t["X"])), int(tagInt(t["Y"])), int(tagInt(t["Z"]))}
		p := m.known[pos]
		if p == nil {
			d := m.savedDef(t)
			if d == nil {
				continue
			}
			p = &POI{Pos: pos, Def: d}
			m.known[pos] = p
			v.pois[d.Kind] = append(v.pois[d.Kind], p)
		}
		if mem.pois[p.Def.Kind] == nil && !p.full() {
			mem.pois[p.Def.Kind] = p
			p.owners = append(p.owners, id)
		}
	}
}

// savedDef is the table's POI with a saved POI's name and type, else one made from what
// was saved.
func (m *Manager) savedDef(t map[string]any) *Def {
	name, _ := t["Name"].(string)
	kind := Kind(tagInt(t["Type"]))
	if kind < 0 || int(kind) >= kinds {
		return nil
	}
	if d := m.table.find(name, kind); d != nil {
		return d
	}
	d := &Def{Name: name, Kind: kind, Capacity: int32(tagInt(t["Capacity"])), Weight: int32(tagInt(t["Weight"])), UseBox: tagInt(t["UseAABB"]) != 0} //nolint:gosec // small saved numbers
	d.Radius, _ = t["Radius"].(float32)
	d.Sound, _ = t["SoundEvent"].(string)
	d.InitEvent, _ = t["InitEvent"].(string)
	if kind == Job {
		d.EndEvent = unskilled
	}
	return d
}

// tagInt reads any NBT integer.
func tagInt(v any) int64 {
	switch n := v.(type) {
	case uint8:
		return int64(n)
	case int16:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

// tagInts reads a list or array of ints.
func tagInts(v any) []int64 {
	switch l := v.(type) {
	case []int32:
		out := make([]int64, len(l))
		for i, n := range l {
			out[i] = int64(n)
		}
		return out
	case []any:
		out := make([]int64, len(l))
		for i, n := range l {
			out[i] = tagInt(n)
		}
		return out
	}
	return nil
}
