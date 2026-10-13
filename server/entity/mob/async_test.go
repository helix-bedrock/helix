package mob

import (
	"context"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/world"
)

type moveViewer struct {
	world.NopViewer
	moves chan mgl64.Vec3
}

func (v moveViewer) ViewEntityMovement(_ world.Entity, pos mgl64.Vec3, _ cube.Rotation, _ bool) {
	select {
	case v.moves <- pos:
	default:
	}
}

func TestMobMovesInRunningWorld(t *testing.T) {
	reg, err := Registry(entity.DefaultRegistry)
	if err != nil {
		t.Fatal(err)
	}
	w := world.Config{Entities: reg}.New()
	defer w.Close()
	var h *world.EntityHandle
	v := moveViewer{moves: make(chan mgl64.Vec3, 64)}
	l := world.NewLoader(2, w, v)
	<-w.Do(func(tx *world.Tx) {
		l.Move(tx, mgl64.Vec3{0, 1, 0})
		l.Load(tx, 100)
		for x := -16; x <= 16; x++ {
			for z := -16; z <= 16; z++ {
				tx.SetBlock(cube.Pos{x, 0, z}, block.Stone{}, nil)
			}
		}
		typ, _ := tx.World().EntityRegistry().Lookup("minecraft:zombie")
		h = typ.(*Type).Spawn(mgl64.Vec3{0.5, 3, 0.5}, 0)
		tx.AddEntity(h)
	}).Done()
	select {
	case p := <-v.moves:
		t.Logf("moved to %v", p)
	case <-time.After(5 * time.Second):
		t.Fatal("viewer saw no movement in 5 seconds")
	}
	time.Sleep(time.Second / 2)
	_, _ = world.CallEntity(context.Background(), h, func(tx *world.Tx, e world.Entity) (struct{}, error) {
		m := e.(*Mob)
		// Only the order below may move it.
		m.s.goals, m.s.targets = selector{}, selector{}
		m.Navigator().MoveTo(mgl64.Vec3{8.5, 1, 0.5}, 1)
		return struct{}{}, nil
	})
	time.Sleep(2 * time.Second)
	pos, _ := world.CallEntity(context.Background(), h, func(tx *world.Tx, e world.Entity) (mgl64.Vec3, error) {
		m := e.(*Mob)
		t.Logf("pos %v idle %v ground %v forward %v", m.Position(), m.s.nav.Idle(), m.OnGround(), m.s.forward)
		return m.Position(), nil
	})
	if pos[0] < 4 {
		t.Fatalf("zombie did not walk: at %v", pos)
	}
}
