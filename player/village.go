package player

import (
	"math/rand/v2"
	"sync"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/village"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Villages (package village) as the Host runs them: one manager per dimension, ticked with
// the chunks, the POI table read per block state, and the mobs as dwellers. Villages are
// not saved yet: after a restart dwellers find their POIs again within seconds, and a
// villager keeps its profession through its saved component groups.

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
	m, _ := v.managers.LoadOrStore(d, village.NewManager(villageEnv{v}, v.table, func(n int32) int32 {
		if n <= 0 {
			return 0
		}
		return rand.Int32N(n) //nolint:gosec // gameplay randomness
	}))
	mgr, _ := m.(*village.Manager)
	return mgr
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
