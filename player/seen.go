package player

import (
	"math"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
)

// PlayerState is what anything may read about a player from any region: where it is and
// which way it looks, its game mode and level, as of the end of its last tick.
type PlayerState struct {
	// Position is the player's feet.
	Position mgl32.Vec3
	Rotation world.Rotation
	GameMode int32
	Level    int32
	// Region is the region the player was in; compare it with your own to know whether
	// the player's live fields are yours to read.
	Region *world.Region
}

// seenState is a PlayerState kept for other regions: a sequence lock over atomic words,
// so writing allocates nothing and readers never see half an update.
type seenState struct {
	seq                       atomic.Uint32
	x, y, z, yaw, pitch, head atomic.Uint32
	gameMode, level           atomic.Int32
	region                    atomic.Pointer[world.Region]
}

// store publishes s. Only the player's owner writes.
func (st *seenState) store(s PlayerState) {
	st.seq.Add(1)
	st.x.Store(math.Float32bits(s.Position[0]))
	st.y.Store(math.Float32bits(s.Position[1]))
	st.z.Store(math.Float32bits(s.Position[2]))
	st.yaw.Store(math.Float32bits(s.Rotation.Yaw))
	st.pitch.Store(math.Float32bits(s.Rotation.Pitch))
	st.head.Store(math.Float32bits(s.Rotation.HeadYaw))
	st.gameMode.Store(s.GameMode)
	st.level.Store(s.Level)
	st.region.Store(s.Region)
	st.seq.Add(1)
}

// load reads a whole update, trying again while one is being written.
func (st *seenState) load() PlayerState {
	for {
		before := st.seq.Load()
		if before%2 != 0 {
			continue
		}
		s := PlayerState{
			Position: mgl32.Vec3{math.Float32frombits(st.x.Load()), math.Float32frombits(st.y.Load()), math.Float32frombits(st.z.Load())},
			Rotation: world.Rotation{Yaw: math.Float32frombits(st.yaw.Load()), Pitch: math.Float32frombits(st.pitch.Load()), HeadYaw: math.Float32frombits(st.head.Load())},
			GameMode: st.gameMode.Load(), Level: st.level.Load(), Region: st.region.Load(),
		}
		if st.seq.Load() == before {
			return s
		}
	}
}

// State is what the player was at the end of its last tick. Unlike the player's other
// getters it may be called from any region, so commands, selectors and plugins can look
// at every player online. Runs: anywhere.
func (p *Player) State() PlayerState { return p.seen.load() }

// publishState updates State. Runs: the player's region, at the end of its tick.
func (p *Player) publishState() {
	if p.entity == nil {
		return
	}
	p.seen.store(PlayerState{
		Position: p.entity.Position().Sub(mgl32.Vec3{0, eyeHeight, 0}), Rotation: p.entity.Rotation(),
		GameMode: p.gameMode, Level: p.Level(), Region: p.entity.Region(),
	})
}
