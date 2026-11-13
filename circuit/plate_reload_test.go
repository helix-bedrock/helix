package circuit_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// A plate held down when its chunk unloads keeps looking once it is back, so it comes up when
// what held it goes.
func TestPlateComesUpAfterReload(t *testing.T) {
	w := openWorld(t)
	at := cube.Pos{4, surfaceY, 4}
	var on atomic.Bool
	on.Store(true)
	w.circuits.SetEntityBox(func(*world.Entity) (lo, hi mgl32.Vec3, mob, ok bool) {
		if !on.Load() {
			return lo, hi, false, false
		}
		return mgl32.Vec3{4.2, surfaceY, 4.2}, mgl32.Vec3{4.8, surfaceY + 1, 4.8}, true, true
	})
	spawn := func(id int64) *world.Entity {
		return w.d.AddEntity(world.EntityConfig{UniqueID: id, RuntimeID: uint64(id), Pos: mgl32.Vec3{4.5, surfaceY, 4.5}}) //nolint:gosec // small
	}
	first := spawn(900)
	w.run(func(r *world.Region) {
		place(r, cube.Pos{4, surfaceY - 1, 4}, w.state(t, "minecraft:stone", nil))
		place(r, at, w.state(t, "minecraft:wooden_pressure_plate", nil))
	})
	down := func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		return w.reg.Blocks.States(b)["redstone_signal"] == int32(1)
	}
	w.wait(t, down)

	// The player leaves: nothing holds the chunk, and it goes.
	first.Remove()
	w.leave.Move(mcdb.ChunkPos{1000, 1000}, 4)
	time.Sleep(500 * time.Millisecond)
	v := w.d.AddViewer(func(packet.Packet) {}, func() {})
	t.Cleanup(v.Close)
	w.leave.Close()
	time.Sleep(500 * time.Millisecond)

	// The player comes back, with what stands on the plate still there.
	second := spawn(901)
	t.Cleanup(second.Remove)
	v.Move(mcdb.ChunkPos{}, 4)
	select {
	case <-v.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("zone never loaded")
	}
	w.wait(t, down)
	time.Sleep(2 * time.Second)
	on.Store(false)
	w.wait(t, func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		return w.reg.Blocks.States(b)["redstone_signal"] == int32(0)
	})
}
