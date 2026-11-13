package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/world/mcdb"

	"github.com/helix-bedrock/helix/world"
)

// StructureMobFunc spawns a mob generation placed in a structure (a village's
// villagers and iron golem, an outpost's pillagers, a mansion's illagers) on the
// first tick of its chunk, and returns it; nil to spawn nothing. Replace it to
// change, move or drop structure mobs. Runs: owner of the region.
type StructureMobFunc func(s *Host, r *world.Region, m world.GeneratedMob) *Mob

// vanillaStructureMob spawns the mob as BDS's structure pieces do (1.26.60
// sub_53704F0, sub_D80D140): with its spawn event, persistent, whether or not
// mobs may spawn naturally.
func vanillaStructureMob(s *Host, r *world.Region, m world.GeneratedMob) *Mob {
	method := definition.InitSpawned
	if m.Event != "" {
		method = definition.InitEvent
	}
	o, ok := s.newMob(m.Identifier, mgl32.Vec3{m.X, m.Y, m.Z}, m.Yaw, method, m.Event)
	if !ok {
		return nil
	}
	if m.Persistent {
		o.m.SetPersistent()
	}
	o.add(r.Dimension())
	return o
}

// spawnStructureMobs spawns the mobs the generator placed in the chunk at pos, BDS's
// structure postProcessMobsAt, which runs as the chunk is finished.
func (s *Host) spawnStructureMobs(r *world.Region, pos mcdb.ChunkPos) {
	if s.mobs == nil {
		return
	}
	for _, m := range r.Dimension().GeneratedMobs(pos) {
		s.game.sys.StructureMob(s, r, m)
	}
}
