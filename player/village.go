package player

import (
	"math/rand/v2"
	"sync"

	"github.com/google/uuid"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/village"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Villages (package village) as the Host runs them: one manager per dimension, ticked with
// the chunks, the POI table read per block state, and the mobs as dwellers. A dimension's
// villages are read from the world when its manager is made, and saved every 600 ticks, as
// BDS saves each village, and when the server closes. A dweller saves its village's UUID,
// RewardPlayersOnFirstFounding and its preferred profession (DwellerComponent, 1.26.60
// 0x723F7E0).

// villages is the Host's village system.
type villages struct {
	s     *Host
	table *village.Table
	// byState is the POI of each block state, nil for none: beds count by their head.
	byState []*village.Def
	// managers are the managers by dimension, made on first use.
	managers sync.Map
}

// setUpVillages builds the village system from the POI table. Runs: boot.
func (s *Host) setUpVillages(table *village.Table) {
	v := &villages{s: s, table: table}
	blocks := s.reg.Blocks
	v.byState = make([]*village.Def, blocks.Count())
	for id := range v.byState {
		b := uint32(id) //nolint:gosec // state IDs fit
		d, ok := table.Lookup(blocks.Name(b))
		if !ok || blockuse.IsBed(blocks, b) && !blockuse.BedIsHead(blocks, b) {
			continue
		}
		v.byState[id] = d
	}
	s.villages = v
	s.mobs.Villages = v
}

// Manager is the village manager of r's dimension (mob.Villages). Safe from any goroutine.
func (v *villages) Manager(r *world.Region) *village.Manager {
	if r == nil {
		return nil
	}
	d := r.Dimension()
	if m, ok := v.managers.Load(d); ok {
		mgr, _ := m.(*village.Manager)
		return mgr
	}
	m, loaded := v.managers.LoadOrStore(d, village.NewManager(villageEnv{v}, v.table, func(n int32) int32 {
		if n <= 0 {
			return 0
		}
		return rand.Int32N(n) //nolint:gosec // gameplay randomness
	}))
	mgr, _ := m.(*village.Manager)
	if !loaded && mgr != nil {
		if st := v.store(); st != nil {
			if err := mgr.Load(st, mcdb.Dimension(v.s.dimensionID(d))); err != nil {
				v.s.log.Error("load villages", "err", err)
			}
		}
	}
	return mgr
}

// store is the world's level store, nil when the world keeps none.
func (v *villages) store() village.Store {
	if v.s.maps == nil || v.s.maps.store == nil {
		return nil
	}
	return v.s.maps.store
}

// saveVillages saves the villages of every dimension. Runs: anywhere.
func (s *Host) saveVillages() {
	if s.villages == nil {
		return
	}
	st := s.villages.store()
	if st == nil {
		return
	}
	s.villages.managers.Range(func(k, m any) bool {
		d, _ := k.(*world.Dimension)
		if mgr, ok := m.(*village.Manager); ok && d != nil {
			if err := mgr.Save(st, mcdb.Dimension(s.dimensionID(d))); err != nil {
				s.log.Error("save villages", "err", err)
			}
		}
		return true
	})
}

// The dweller's own save keys (DwellerComponent::addAdditionalSaveData).
const (
	dwellerKeyVillage    = "DwellingUniqueID"
	dwellerKeyReward     = "RewardPlayersOnFirstFounding"
	dwellerKeyProfession = "PreferredProfession"
)

// saveDweller writes a dweller's village UUID, its founding reward flag (kept as loaded)
// and its preferred profession when it has one.
func (o *Mob) saveDweller(data map[string]any) {
	if !o.m.IsDweller() {
		return
	}
	data[dwellerKeyVillage] = o.m.VillageID().String()
	if _, ok := data[dwellerKeyReward].(uint8); !ok {
		data[dwellerKeyReward] = uint8(0)
	}
	if p := o.m.Profession(); p != "" {
		data[dwellerKeyProfession] = p
	} else {
		delete(data, dwellerKeyProfession)
	}
}

// loadDweller reads them back: the mob joins its village again on its next dweller update.
func (o *Mob) loadDweller(data map[string]any) {
	if s, ok := data[dwellerKeyVillage].(string); ok {
		if id, err := uuid.Parse(s); err == nil {
			o.m.SetVillageID(id)
		}
	}
	if p, ok := data[dwellerKeyProfession].(string); ok {
		o.m.SetProfession(p)
	}
}

// chunkTick runs the village work of a ticking chunk. Runs: owner of the region.
func (v *villages) chunkTick(r *world.Region, pos mcdb.ChunkPos) {
	if m, ok := v.managers.Load(r.Dimension()); ok {
		if mgr, ok := m.(*village.Manager); ok {
			mgr.ChunkTick(r, pos)
		}
	}
}

// villageEnv is village.Env.
type villageEnv struct{ v *villages }

// POIAt is the POI of the block at pos.
func (e villageEnv) POIAt(r *world.Region, pos cube.Pos) (*village.Def, bool) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return nil, false
	}
	if int(b) >= len(e.v.byState) {
		return nil, true
	}
	return e.v.byState[b], true
}

// Dweller is the mob with the unique ID in r.
func (e villageEnv) Dweller(r *world.Region, id behavior.ID) (village.Dweller, bool) {
	m := e.v.s.Mob(r, id)
	if m == nil || m.Dead() || !m.IsDweller() {
		return nil, false
	}
	m.SetRegion(r)
	return m, true
}

// LevelEvent shows a level event with no data.
func (e villageEnv) LevelEvent(r *world.Region, id int32, p behavior.Vec3) {
	pos := mglVec(p)
	levelEvent(r, vecPos(pos), pos, id, 0)
}

// --- the mob runtime's village and door questions (mob.Env) ---

// RainingAt reports rain falling on pos.
func (s *Host) RainingAt(r *world.Region, pos cube.Pos) bool {
	if s.chunkWeather == nil || r.Dimension() != s.overworld {
		return false
	}
	return s.chunkWeather.RainingAt(r, pos)
}

// SetDoorOpen opens or closes a door as a villager does, with the door's sound.
func (s *Host) SetDoorOpen(r *world.Region, p behavior.Pos, open bool) {
	at := cube.Pos{int(p.X), int(p.Y), int(p.Z)}
	if b, ok := r.Block(at, 0); ok && blockuse.IsDoor(s.reg.Blocks, b) {
		blockuse.SetPowered(s.reg.Blocks, r, at, b, open)
	}
}

// LevelEventData sends a level event with a number as its data.
func (s *Host) LevelEventData(r *world.Region, id int32, p behavior.Vec3, data int32) {
	pos := mglVec(p)
	levelEvent(r, vecPos(pos), pos, id, data)
}

var _ mob.Villages = (*villages)(nil)
