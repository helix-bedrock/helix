package connect_test

import (
	"testing"
	"time"

	"github.com/helix-bedrock/helix/connect"
	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

const surfaceY = -60

// TestFences places a fence by another fence, a nether brick fence and stone,
// and takes the stone away again.
func TestFences(t *testing.T) {
	s := registry.NewSetup(nil, "minecraft")
	if err := registry.Vanilla(s); err != nil {
		t.Fatal(err)
	}
	reg, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	table, err := connect.New(reg.Blocks)
	if err != nil {
		t.Fatal(err)
	}
	db, err := mcdb.Config{Blocks: reg.Blocks}.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	r, _ := mcdb.Overworld.Range()
	flat, err := world.ParseFlat("", reg.Blocks, reg.Biomes, r)
	if err != nil {
		t.Fatal(err)
	}
	conf := world.DefaultConfig()
	conf.Store, conf.Blocks, conf.Biomes, conf.Generator = db, reg.Blocks, reg.Biomes, flat
	conf.Ticker = connect.NewTicker(table, liquid.New(reg.Blocks))
	d, err := world.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	v := d.AddViewer(func(packet.Packet) {}, func() {})
	defer v.Close()
	v.Move(mcdb.ChunkPos{}, 2)
	select {
	case <-v.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("zone never loaded")
	}
	fence, _ := reg.Blocks.Block("minecraft:oak_fence")
	nether, _ := reg.Blocks.Block("minecraft:nether_brick_fence")
	stone, _ := reg.Blocks.Block("minecraft:stone")
	at := cube.Pos{8, surfaceY, 8}
	done := make(chan struct{})
	d.RunAt(mcdb.ChunkPos{}, func(r *world.Region) {
		defer close(done)
		arm := func(pos cube.Pos, key string) uint8 {
			b, _ := r.Block(pos, 0)
			v, _ := reg.Blocks.States(b)["minecraft:connection_"+key].(uint8)
			return v
		}
		place := func(pos cube.Pos, block uint32) {
			r.SetBlock(pos, 0, table.Connected(r, pos, block))
			r.UpdateNeighbours(pos)
		}
		place(at, fence)
		place(at.Side(cube.FaceNorth), fence)
		place(at.Side(cube.FaceEast), nether)
		place(at.Side(cube.FaceWest), stone)
		if arm(at, "north") != 1 || arm(at.Side(cube.FaceNorth), "south") != 1 {
			t.Error("two oak fences did not join")
		}
		if arm(at, "east") != 0 {
			t.Error("an oak fence joined a nether brick fence")
		}
		if arm(at, "west") != 1 {
			t.Error("a fence did not join stone")
		}
		r.SetBlock(at.Side(cube.FaceWest), 0, reg.Blocks.Air())
		r.UpdateNeighbours(at.Side(cube.FaceWest))
		if arm(at, "west") != 0 {
			t.Error("a fence kept its arm when the stone went")
		}
		// A wire joins the hook that faces it and wire, not stone or a hook facing away.
		wire, _ := reg.Blocks.Block("minecraft:trip_wire")
		hook := func(direction int32) uint32 {
			id, ok := reg.Blocks.StateID("minecraft:tripwire_hook", map[string]any{"attached_bit": uint8(0), "direction": direction, "powered_bit": uint8(0)})
			if !ok {
				t.Error("no hook state")
			}
			return id
		}
		mid := cube.Pos{12, surfaceY, 8}
		place(mid.Side(cube.FaceWest), hook(3))
		place(mid.Side(cube.FaceEast), hook(3))
		place(mid.Side(cube.FaceNorth), stone)
		place(mid, wire)
		if arm(mid, "west") != 1 || arm(mid, "east") != 0 || arm(mid, "north") != 0 {
			t.Errorf("wire arms: west %d east %d north %d", arm(mid, "west"), arm(mid, "east"), arm(mid, "north"))
		}
		place(mid.Side(cube.FaceEast), hook(1))
		if arm(mid, "east") != 1 {
			t.Error("a wire did not join a hook placed after it")
		}
		place(mid.Side(cube.FaceSouth), wire)
		if arm(mid, "south") != 1 || arm(mid.Side(cube.FaceSouth), "north") != 1 {
			t.Error("two wires did not join")
		}
	})
	<-done
}
