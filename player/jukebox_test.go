package player

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
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
		if e.handle == 0 || e.hearing == nil {
			t.Errorf("song has no handle or listeners: %v handle %d", e.hearing, e.handle)
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
		if e.hearing != nil {
			t.Errorf("song over but still heard by %v", e.hearing)
		}
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

// One who comes late is sent the song from where it has got to, and one too far away is not.
func TestJukeboxSongForLateAndFar(t *testing.T) {
	e := &jukeboxEntity{handle: 7, ticks: 200}
	centre := mgl32.Vec3{0.5, 0.5, 0.5}
	late := e.songPacket("record.cat", centre, false)
	if v, ok := late.PlaybackPositionSeconds.Value(); !ok || v != 10 {
		t.Errorf("late start %v %v", v, ok)
	}
	if h, ok := late.Handle.Value(); !ok || h != 7 {
		t.Errorf("handle %v %v", h, ok)
	}
	if _, ok := e.songPacket("record.cat", centre, true).PlaybackPositionSeconds.Value(); ok {
		t.Error("from the start has a start position")
	}
}

// A player in reach is sent the song once, and it stops with the disc.
func TestJukeboxReachesPlayer(t *testing.T) {
	p, d := machineWorld(t)
	var err error
	if p.s.discs, err = loadDiscs(); err != nil {
		t.Fatal(err)
	}
	at := cube.Pos{3, 0, 3}
	p.flush = func() {}
	p.entity = d.AddEntity(world.EntityConfig{UniqueID: 2, RuntimeID: p.s.runtimeIDs.Add(1), Pos: mgl32.Vec3{4, 0, 5}, Owner: p})
	waitRegion(t, d, func(*world.Region) bool { return p.entity.Region() != nil })
	onRegion(d, func(r *world.Region) {
		e := &jukeboxEntity{s: p.s}
		e.insert(r, at, ItemStack{Name: "minecraft:music_disc_cat", Count: 1})
		if _, heard := e.hearing[p]; !heard {
			t.Errorf("player in reach not sent the song: %v", e.hearing)
		}
		e.ticks = 40
		e.updateHearing(r, at)
		if len(e.hearing) != 1 {
			t.Errorf("listeners %v", e.hearing)
		}
		e.stopSong(r)
		if e.hearing != nil {
			t.Errorf("still hearing: %v", e.hearing)
		}
	})
}
