package world

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/world/cube"
)

// Game events as BDS 1.26.60's GameEventDispatcher::post (0xCA517E0 and its block form): an
// event at a position reaches the listeners kept by the chunks within 16 blocks of it, each
// that is within its own range and 16 blocks, in chunk then registration order. Listeners are
// block entities (sculk sensors and shriekers); what an event means is the caller's.
const (
	// gameEventReach is the farthest any listener hears, in blocks: BDS walks the chunks
	// within it and drops listeners further than its square.
	gameEventReach = 16
)

// GameEvent is something that happened that listeners may hear, such as a block breaking or
// a player stepping. ID numbers it as the poster's table does (BDS's GameEvent IDs for
// vanilla). It is passed by value, so posting one allocates nothing.
type GameEvent struct {
	ID  int32
	Pos mgl32.Vec3
	// Block is the block the event is about, when HasBlock is set.
	Block    uint32
	HasBlock bool
	// Source is what caused it (a player, a mob), nil for nothing.
	Source any
}

// GameEventListener is a block entity that hears game events near it.
type GameEventListener interface {
	// GameEventRange is how far it hears, in blocks.
	GameEventRange() int32
	// HearGameEvent is an event in range of the listener at pos.
	HearGameEvent(r *Region, pos cube.Pos, ev GameEvent)
}

// PostGameEvent tells every listener in range about ev. Listeners in chunks of other regions
// do not hear it. Runs: owner.
func (r *Region) PostGameEvent(ev GameEvent) {
	lo := chunkOf(ev.Pos.Sub(mgl32.Vec3{gameEventReach, gameEventReach, gameEventReach}))
	hi := chunkOf(ev.Pos.Add(mgl32.Vec3{gameEventReach, gameEventReach, gameEventReach}))
	for x := lo[0]; x <= hi[0]; x++ {
		for z := lo[1]; z <= hi[1]; z++ {
			c, ok := r.chunkAt(cube.Pos{int(x) << 4, r.d.r[0], int(z) << 4})
			if !ok {
				continue
			}
			r.postInChunk(c, ev)
		}
	}
}

// postInChunk tells the chunk's listeners in range about ev. A listener may change the
// chunk's block entities, so each position is looked up as it comes.
func (r *Region) postInChunk(c *Chunk, ev GameEvent) {
	for i := 0; i < len(c.listeners); i++ {
		pos := c.listeners[i]
		l, ok := c.blockEntities[pos].(GameEventListener)
		if !ok || !gameEventHeard(pos, ev.Pos, l.GameEventRange()) {
			continue
		}
		l.HearGameEvent(r, pos, ev)
	}
}

// gameEventHeard is BDS's distance test: the square distance from the listener's centre,
// truncated to a whole number, within its range squared and within 16 blocks.
func gameEventHeard(pos cube.Pos, at mgl32.Vec3, rng int32) bool {
	dx := at[0] - (float32(pos[0]) + 0.5)
	dy := at[1] - (float32(pos[1]) + 0.5)
	dz := at[2] - (float32(pos[2]) + 0.5)
	d2 := dy*dy + dx*dx + dz*dz
	if d2 > gameEventReach*gameEventReach || math.IsNaN(float64(d2)) {
		return false
	}
	return uint64(d2) <= uint64(rng)*uint64(rng) //nolint:gosec // rng is a listener's range, never negative in use
}
