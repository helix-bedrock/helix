package player

import (
	"testing"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

func TestJukeboxPlaysAndEjects(t *testing.T) {
	p, d := machineWorld(t)
	var err error
	if p.s.discs, err = loadDiscs(); err != nil {
		t.Fatal(err)
	}
	var y int
	onRegion(d, func(r *world.Region) {
		y = surface(r, p.s.reg.Blocks.Air(), 3, 3)
		setNamed(t, p, r, cube.Pos{3, y, 3}, jukeboxBlock, nil)
	})
	at := cube.Pos{3, y, 3}
	var e *jukeboxEntity
	waitRegion(t, d, func(r *world.Region) bool {
		be, ok := r.BlockEntity(at)
		if ok {
			e, ok = be.(*jukeboxEntity)
		}
		return ok
	})
	onRegion(d, func(r *world.Region) {
		e.insert(r, at, ItemStack{Name: "minecraft:music_disc_cat", Count: 2})
		if e.record.Count != 1 || e.ComparatorSignal(r, at) != 2 {
			t.Errorf("record %+v signal %d", e.record, e.ComparatorSignal(r, at))
		}
		m := e.NetworkData(at)
		if m[finishedTag] != uint8(0) || m[recordItemTag] == nil {
			t.Errorf("network %v", m)
		}
		if _, sent := e.SaveData(at)[finishedTag]; sent {
			t.Error("finished is not saved")
		}
	})
	waitRegion(t, d, func(*world.Region) bool { return e.ticks > 0 })
	onRegion(d, func(*world.Region) {
		e.ticks = 185*20 + 1
		e.sinceNote = jukeboxNoteTicks
	})
	waitRegion(t, d, func(*world.Region) bool { return e.finished })
	onRegion(d, func(r *world.Region) {
		e.eject(r, at)
		if !e.record.Empty() || e.ComparatorSignal(r, at) != 0 {
			t.Errorf("still holds %+v", e.record)
		}
	})
}

func TestDiscTable(t *testing.T) {
	discs, err := loadDiscs()
	if err != nil {
		t.Fatal(err)
	}
	if len(discs) != 22 {
		t.Fatalf("%d discs", len(discs))
	}
	if d := discs["minecraft:music_disc_5"]; d.Signal != 15 || d.Seconds != 178 {
		t.Fatalf("disc 5 %+v", d)
	}
}
