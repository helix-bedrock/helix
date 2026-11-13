package generator

import (
	"sync"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/mcdb"
	"github.com/helix-bedrock/worldgen/structure"
)

// mobStash holds the mobs worldgen placed in generated chunks until the server
// takes them on the chunk's first tick (world.MobGenerator). A chunk generated
// but never ticked keeps its entry until it is ticked in this run; a chunk
// saved before its first tick and loaded after a restart has lost its mobs.
// It is safe for use by many goroutines at once.
type mobStash struct{ m sync.Map }

// put keeps the mobs of the chunk at pos, replacing what it had: a chunk
// generated again places the same mobs.
func (s *mobStash) put(pos mcdb.ChunkPos, spawns []structure.EntitySpawn) {
	if len(spawns) == 0 {
		return
	}
	mobs := make([]world.GeneratedMob, len(spawns))
	for i, e := range spawns {
		mobs[i] = world.GeneratedMob{
			Identifier: e.Identifier, Event: e.Event,
			X: float32(e.X), Y: float32(e.Y), Z: float32(e.Z), Yaw: e.Yaw,
			Persistent: e.Persistent, Source: e.Source,
		}
	}
	s.m.Store(pos, mobs)
}

// take returns and forgets the mobs of the chunk at pos.
func (s *mobStash) take(pos mcdb.ChunkPos) []world.GeneratedMob {
	v, ok := s.m.LoadAndDelete(pos)
	if !ok {
		return nil
	}
	mobs, _ := v.([]world.GeneratedMob)
	return mobs
}
