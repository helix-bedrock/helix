package sim

import (
	"context"
	"fmt"
	"maps"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

const (
	// redstoneSize is the redstone scenario's area: room for about 10,000 clocks.
	redstoneSize = 512
	// clockDust is how much redstone dust each clock drives, in a line out of its back.
	clockDust = 8
	// clockColumn is how far apart clocks are along X (two observers, the dust and a
	// gap), clockRow along Z (a gap, so lines of dust do not join).
	clockColumn = clockDust + 4
	clockRow    = 2
	// observerFacing is the observer state naming the side it watches.
	observerFacing = "minecraft:facing_direction"
	// clockMargin keeps clocks off the area's edge: queued ticks wait while any chunk
	// within 8 blocks is not loaded, as BDS's do, so a clock on the edge never runs.
	clockMargin = 16
)

// clockBlocks are the states a clock is made of.
type clockBlocks struct {
	east, west, dust uint32
}

// startClocks builds n observer clocks: observer A watching east and observer B watching
// west, side by side, so each sees the other change and both pulse forever, with a line of
// dust west of A, which A's back powers. B goes first, so placing A starts the clock.
func startClocks(ctx context.Context, w *World, n int) error {
	blocks, err := clockStates(w.Server.Registry().Blocks)
	if err != nil {
		return err
	}
	inner := int(w.size) - 2*clockMargin
	perRow := inner / clockColumn
	if n > perRow*(inner/clockRow) {
		return fmt.Errorf("sim: %d clocks do not fit a %d block area", n, w.size)
	}
	var failed int
	// The ticket keeps the whole area loaded and in one region, so one region places all.
	// A goes in a tick after B, once the circuit has made B's observer, as a player would
	// build it: an observer placed in the same tick does not hear its neighbour yet.
	err = w.Do(ctx, mcdb.ChunkPos{0, 0}, func(r *world.Region) {
		for i := range n {
			failed += placeClockBase(r, w.clockAt(i, perRow), blocks)
		}
	})
	if err != nil {
		return err
	}
	err = w.Do(ctx, mcdb.ChunkPos{0, 0}, func(r *world.Region) {
		for i := range n {
			failed += place(r, w.clockAt(i, perRow), blocks.east)
		}
	})
	if err == nil && failed > 0 {
		err = fmt.Errorf("sim: %d clock blocks could not be placed (outside the region)", failed)
	}
	return err
}

// clockStates looks the clock's states up.
func clockStates(b *registry.Blocks) (clockBlocks, error) {
	east, okE := withState(b, "minecraft:observer", observerFacing, "east")
	west, okW := withState(b, "minecraft:observer", observerFacing, "west")
	dust, okD := b.Block("minecraft:redstone_wire")
	if !okE || !okW || !okD {
		return clockBlocks{}, fmt.Errorf("sim: observer or redstone states missing")
	}
	return clockBlocks{east: east, west: west, dust: dust}, nil
}

// withState is the state of block name with its default states but key set to value.
func withState(b *registry.Blocks, name, key string, value any) (uint32, bool) {
	def, ok := b.Block(name)
	if !ok {
		return 0, false
	}
	states := maps.Clone(b.States(def))
	states[key] = value
	return b.StateID(name, states)
}

// clockAt is where observer A of clock i stands.
func (w *World) clockAt(i, perRow int) cube.Pos {
	x0, z0, _, _ := w.Area()
	return cube.Pos{int(x0) + clockMargin + i%perRow*clockColumn + clockDust + 1, int(w.Surface()), int(z0) + clockMargin + i/perRow*clockRow}
}

// placeClockBase places a clock's dust and observer B, A being at a, returning how many
// blocks could not be set.
func placeClockBase(r *world.Region, a cube.Pos, blocks clockBlocks) int {
	failed := 0
	for d := 1; d <= clockDust; d++ {
		failed += place(r, a.Add(cube.Pos{-d, 0, 0}), blocks.dust)
	}
	return failed + place(r, a.Add(cube.Pos{1, 0, 0}), blocks.west)
}

// place sets a block as gameplay does, telling its neighbours; 1 if it could not be set.
func place(r *world.Region, pos cube.Pos, block uint32) int {
	if !r.SetBlock(pos, 0, block) {
		return 1
	}
	r.UpdateNeighbours(pos)
	return 0
}
