package mob

import (
	"context"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/world"
	"github.com/helix-bedrock/helix/server/world/biome"
	"github.com/helix-bedrock/helix/server/world/generator"
)

// TestZombieWandersInFlatWorld runs a zombie in a running superflat world,
// as the server makes by default, and waits for it to wander off alone.
func TestZombieWandersInFlatWorld(t *testing.T) {
	reg, err := Registry(entity.DefaultRegistry)
	if err != nil {
		t.Fatal(err)
	}
	world.DefaultBlockRegistry.Finalize()
	gen := generator.NewFlat(biome.Plains{}, []world.Block{block.Grass{}, block.Dirt{}, block.Dirt{}, block.Bedrock{}})
	w := world.Config{Entities: reg, Generator: gen}.New()
	defer w.Close()
	l := world.NewLoader(4, w, world.NopViewer{})
	var h *world.EntityHandle
	start := mgl64.Vec3{0.5, -60, 0.5}
	<-w.Do(func(tx *world.Tx) {
		l.Move(tx, start)
		l.Load(tx, 200)
		t.Logf("ground: %T at y=-61, %T at -60", tx.Block([3]int{0, -61, 0}), tx.Block([3]int{0, -60, 0}))
		typ, _ := tx.World().EntityRegistry().Lookup("minecraft:zombie")
		h = typ.(*Type).Spawn(start, 0)
		tx.AddEntity(h)
	}).Done()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		pos, _ := world.CallEntity(context.Background(), h, func(tx *world.Tx, e world.Entity) (mgl64.Vec3, error) {
			m := e.(*Mob)
			t.Logf("pos %v idle %v forward %v ground %v", m.Position(), m.s.nav.Idle(), m.s.forward, m.OnGround())
			return m.Position(), nil
		})
		if pos.Sub(start).Len() > 2 {
			return
		}
	}
	t.Fatal("zombie never wandered off in 20 seconds")
}
