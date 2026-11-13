package player

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// cartWorld is a world with a straight run of rails along z at x=10, on a stone floor,
// out of reach of the test's player.
func cartWorld(t *testing.T, name string, states map[string]any) (*Player, *world.Dimension, int) {
	t.Helper()
	p, d := machineWorld(t)
	p.s.level.TNTExplodes = true
	ticket := d.KeepLoaded(world.Radius(0, 0, 4), true)
	t.Cleanup(ticket.Release)
	// The chunks load in the background, and a test can only give up off the region.
	waitRegion(t, d, func(r *world.Region) bool {
		_, ok := r.Block(cube.Pos{10, 0, 15}, 0)
		return ok
	})
	var y int
	onRegion(d, func(r *world.Region) {
		y = surface(r, p.s.reg.Blocks.Air(), 10, 10)
		for z := 0; z < 16; z++ {
			setNamed(t, p, r, cube.Pos{10, y, z}, name, states)
		}
	})
	return p, d, y
}

// railStates is a rail along z.
func railStates(dir int32) map[string]any {
	return map[string]any{stateRailDirection: dir}
}

func TestCartRollsAlongRailAndSlowsDown(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	m := p.s.SpawnMinecart(d, nameMinecart, mgl64.Vec3{10.5, float64(y) + 0.5, 5.5})
	onRegion(d, func(*world.Region) { m.SetVelocity(mgl64.Vec3{0, 0, 0.3}) })
	waitRegion(t, d, func(*world.Region) bool { return m.entity.Position()[2] > 7 })
	var far float32
	onRegion(d, func(*world.Region) {
		at := m.entity.Position()
		far = float32(at[2])
		if at[0] != 10.5 {
			t.Errorf("left the line: %v", at)
		}
		if at[1] < float64(y) || at[1] > float64(y)+1 {
			t.Errorf("not on the rail: %v, rail at %d", at, y)
		}
		if v := m.Velocity(); v[2] <= 0 || v[2] >= 0.3 {
			t.Errorf("speed %v", v)
		}
	})
	waitRegion(t, d, func(*world.Region) bool { return m.entity.Position()[2] > float64(far) })
}

func TestPoweredRailSpeedsUpAndBrakes(t *testing.T) {
	p, d, y := cartWorld(t, nameGoldenRail, map[string]any{stateRailDirection: int32(0), stateRailData: uint8(1)})
	m := p.s.SpawnMinecart(d, nameMinecart, mgl64.Vec3{10.5, float64(y) + 0.5, 2.5})
	onRegion(d, func(*world.Region) { m.SetVelocity(mgl64.Vec3{0, 0, 0.1}) })
	waitRegion(t, d, func(*world.Region) bool { return m.Velocity()[2] > 0.3 })
	onRegion(d, func(r *world.Region) {
		for z := 0; z < 16; z++ {
			setNamed(t, p, r, cube.Pos{10, y, z}, nameGoldenRail, map[string]any{stateRailDirection: int32(0), stateRailData: uint8(0)})
		}
	})
	waitRegion(t, d, func(*world.Region) bool { return m.Velocity().Len() == 0 })
}

func TestPoweredRailStartsCartAgainstBlock(t *testing.T) {
	p, d, y := cartWorld(t, nameGoldenRail, map[string]any{stateRailDirection: int32(0), stateRailData: uint8(1)})
	onRegion(d, func(r *world.Region) { setNamed(t, p, r, cube.Pos{10, y, 4}, "minecraft:stone", nil) })
	m := p.s.SpawnMinecart(d, nameMinecart, mgl64.Vec3{10.5, float64(y) + 0.5, 5.5})
	waitRegion(t, d, func(*world.Region) bool { return m.Velocity()[2] > 0 && m.entity.Position()[2] > 6 })
}

func TestCartRollsDownSlope(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	onRegion(d, func(r *world.Region) {
		// Up to the south: the slope stands in the low block.
		setNamed(t, p, r, cube.Pos{10, y, 10}, nameRail, railStates(5))
		setNamed(t, p, r, cube.Pos{10, y, 11}, "minecraft:stone", nil)
		setNamed(t, p, r, cube.Pos{10, y + 1, 11}, nameRail, railStates(0))
	})
	m := p.s.SpawnMinecart(d, nameMinecart, mgl64.Vec3{10.5, float64(y) + 0.5, 10.75})
	waitRegion(t, d, func(*world.Region) bool { return m.entity.Position()[2] < 8 })
	onRegion(d, func(*world.Region) {
		if v := m.Velocity(); v[2] >= 0 {
			t.Errorf("speed %v", v)
		}
		if at := m.entity.Position(); at[1] >= float64(y)+1 {
			t.Errorf("still high: %v", at)
		}
	})
}

// hopperUnder puts a hopper on the rail's line with a rail above it.
func hopperUnder(t *testing.T, p *Player, d *world.Dimension, y int) (*Chest, cube.Pos) {
	t.Helper()
	at := cube.Pos{3, y, 8}
	var c *Chest
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at, "minecraft:hopper", nil)
		c = p.s.chestAt(r, at)
	})
	return c, at
}

func TestHopperTakesFromChestCartAbove(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	c, at := hopperUnder(t, p, d, y)
	onRegion(d, func(r *world.Region) { setNamed(t, p, r, at.Side(cube.FaceUp), nameRail, railStates(0)) })
	m := p.s.SpawnMinecart(d, nameChestMinecart, mgl64.Vec3{3.5, float64(y) + 1.5, 8.5})
	onRegion(d, func(r *world.Region) {
		m.Chest().Inventory(r).SetItem(3, ItemStack{Name: "minecraft:emerald", Count: 2})
	})
	waitRegion(t, d, func(r *world.Region) bool {
		return c.Inventory(r).Item(0).Count == 2 && m.Chest().Inventory(r).Item(3).Empty()
	})
}

func TestHopperTakesFromMovingChestCart(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	c, at := hopperUnder(t, p, d, y)
	onRegion(d, func(r *world.Region) {
		for z := 2; z < 16; z++ {
			setNamed(t, p, r, cube.Pos{at[0], y + 1, z}, nameRail, railStates(0))
			if z != at[2] {
				setNamed(t, p, r, cube.Pos{at[0], y, z}, "minecraft:stone", nil)
			}
		}
	})
	m := p.s.SpawnMinecart(d, nameChestMinecart, mgl64.Vec3{3.5, float64(y) + 1.5, 7})
	onRegion(d, func(r *world.Region) {
		m.Chest().Inventory(r).SetItem(0, ItemStack{Name: "minecraft:emerald", Count: 5})
		m.SetVelocity(mgl64.Vec3{0, 0, 0.1})
	})
	waitRegion(t, d, func(r *world.Region) bool { return c.Inventory(r).Item(0).Count > 0 })
}

func TestHopperDoesNotReachCartOnBlockAbove(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	c, at := hopperUnder(t, p, d, y)
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at.Side(cube.FaceUp), "minecraft:stone", nil)
		setNamed(t, p, r, cube.Pos{at[0], at[1] + 2, at[2]}, nameRail, railStates(0))
	})
	m := p.s.SpawnMinecart(d, nameChestMinecart, mgl64.Vec3{3.5, float64(y) + 2.5, 8.5})
	onRegion(d, func(r *world.Region) {
		m.Chest().Inventory(r).SetItem(0, ItemStack{Name: "minecraft:emerald", Count: 2})
	})
	time.Sleep(500 * time.Millisecond)
	onRegion(d, func(r *world.Region) {
		if !c.Inventory(r).Item(0).Empty() {
			t.Error("the hopper reached through a block")
		}
		if m.Chest().Inventory(r).Item(0).Count != 2 {
			t.Error("the cart lost its items")
		}
	})
}

func TestHopperFillsCartBelow(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	at := cube.Pos{3, y + 1, 8}
	var c *Chest
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, cube.Pos{3, y, 8}, nameRail, railStates(0))
		setNamed(t, p, r, at, "minecraft:hopper", map[string]any{"facing_direction": int32(0)})
		c = p.s.chestAt(r, at)
		c.Inventory(r).SetItem(0, ItemStack{Name: "minecraft:emerald", Count: 1})
	})
	m := p.s.SpawnMinecart(d, nameChestMinecart, mgl64.Vec3{3.5, float64(y) + 0.5, 8.5})
	waitRegion(t, d, func(r *world.Region) bool {
		return m.Chest().Inventory(r).Item(0).Count == 1 && c.Inventory(r).Item(0).Empty()
	})
}

func TestHopperCartTakesFromChestAboveUntilStopped(t *testing.T) {
	p, d, y := cartWorld(t, nameActivatorRail, map[string]any{stateRailDirection: int32(0), stateRailData: uint8(0)})
	above := cube.Pos{3, y + 1, 8}
	var c *Chest
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, cube.Pos{3, y, 8}, nameActivatorRail, map[string]any{stateRailDirection: int32(0), stateRailData: uint8(1)})
		setNamed(t, p, r, above, "minecraft:chest", nil)
		c = p.s.chestAt(r, above)
		c.Inventory(r).SetItem(0, ItemStack{Name: "minecraft:emerald", Count: 2})
	})
	m := p.s.SpawnMinecart(d, nameHopperMinecart, mgl64.Vec3{3.5, float64(y) + 0.5, 8.5})
	time.Sleep(400 * time.Millisecond)
	onRegion(d, func(r *world.Region) {
		if c.Inventory(r).Item(0).Count != 2 {
			t.Error("a stopped hopper cart took items")
		}
		setNamed(t, p, r, cube.Pos{3, y, 8}, nameActivatorRail, map[string]any{stateRailDirection: int32(0), stateRailData: uint8(0)})
	})
	waitRegion(t, d, func(r *world.Region) bool { return m.Chest().Inventory(r).Item(0).Count > 0 })
}

func TestTNTCartLightsOnPoweredActivatorRail(t *testing.T) {
	p, d, y := cartWorld(t, nameActivatorRail, map[string]any{stateRailDirection: int32(0), stateRailData: uint8(1)})
	m := p.s.SpawnMinecart(d, nameTNTMinecart, mgl64.Vec3{10.5, float64(y) + 0.5, 10.5})
	waitRegion(t, d, func(*world.Region) bool { return m.lit })
	onRegion(d, func(*world.Region) { m.fuse = 1 })
	waitRegion(t, d, func(*world.Region) bool { return m.gone })
}

func TestActivatorRailThrowsOutRider(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	m := p.s.SpawnMinecart(d, nameMinecart, mgl64.Vec3{10.5, float64(y) + 0.5, 10.5})
	onRegion(d, func(*world.Region) {
		mount(m, p)
		if m.Rider() != p || p.vehicle != m {
			t.Fatal("did not mount")
		}
	})
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, cube.Pos{10, y, 10}, nameActivatorRail, map[string]any{stateRailDirection: int32(0), stateRailData: uint8(1)})
	})
	waitRegion(t, d, func(*world.Region) bool { return m.Rider() == nil && p.vehicle == nil })
}

func TestCartBreaksAfterHitsAndDrops(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	m := p.s.SpawnMinecart(d, nameChestMinecart, mgl64.Vec3{10.5, float64(y) + 0.5, 10.5})
	onRegion(d, func(r *world.Region) {
		m.Chest().Inventory(r).SetItem(0, ItemStack{Name: "minecraft:emerald", Count: 2})
		if m.Hurt(r, 1, nil) || m.Hurt(r, 1, nil) {
			t.Error("broke too soon")
		}
		if !m.Hurt(r, 1, nil) {
			t.Error("did not break on the third hit")
		}
	})
	waitRegion(t, d, func(r *world.Region) bool {
		var names []string
		var buf [16]*world.Entity
		for _, e := range r.AppendEntitiesIn(buf[:0], 8, 8, 13, 13) {
			if i, ok := e.Owner().(*ItemActor); ok {
				names = append(names, i.stack.Name)
			}
		}
		return len(names) == 3
	})
}

func TestCartSavesAndLoads(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	m := p.s.SpawnMinecart(d, nameHopperMinecart, mgl64.Vec3{10.5, float64(y) + 0.5, 10.5})
	onRegion(d, func(r *world.Region) {
		m.Chest().Inventory(r).SetItem(2, ItemStack{Name: "minecraft:emerald", Count: 3})
		saved, ok := m.saveEntity(m.entity)
		if !ok || saved.Data["identifier"] != nameHopperMinecart {
			t.Fatalf("saved %v", saved)
		}
		saved.ID = 77
		if !p.s.loadEntity(r, mcdb.ChunkPos{}, chunk.Entity{ID: saved.ID, Data: saved.Data}) {
			t.Fatal("did not load")
		}
		var buf [16]*world.Entity
		for _, e := range r.AppendEntitiesIn(buf[:0], 0, 0, 16, 16) {
			if b, ok := e.Owner().(*Minecart); ok && e.UniqueID() == 77 {
				if s := b.Chest().Inventory(r).Item(2); s.Count != 3 {
					t.Errorf("came back with %+v", s)
				}
				return
			}
		}
		t.Error("the loaded cart is not there")
	})
}

func TestDispenserPutsCartOnRail(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(1))
	at := cube.Pos{10, y, 15}
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, cube.Pos{10, y, 14}, nameRail, railStates(0))
		setNamed(t, p, r, at, "minecraft:dispenser", map[string]any{"facing_direction": int32(2)})
		p.s.chestAt(r, at).Inventory(r).SetItem(0, ItemStack{Name: nameMinecart, Count: 2})
		b, _ := r.Block(at, 0)
		p.s.Dispense(r, at, b)
		if n := p.s.chestAt(r, at).Inventory(r).Item(0).Count; n != 1 {
			t.Errorf("%d left", n)
		}
	})
	// The cart joins the region after this task.
	waitRegion(t, d, func(r *world.Region) bool {
		var buf [16]*world.Entity
		for _, e := range r.AppendEntitiesIn(buf[:0], 9, 12, 12, 16) {
			if _, ok := e.Owner().(*Minecart); ok {
				return true
			}
		}
		return false
	})
}

func TestRiderSitsInTheCartAndLandsBeside(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	m := p.s.SpawnMinecart(d, nameMinecart, mgl64.Vec3{10.5, float64(y) + 0.5, 8.5})
	onRegion(d, func(*world.Region) {
		mount(m, p)
		at := m.entity.Position()
		want := at.Add(mgl64.Vec3{0, eyeHeight + playerRidingOffset + cartSeatY, 0})
		if got := p.seatPosition(); !got.ApproxEqualThreshold(want, 1e-4) {
			t.Errorf("seat at %v, want %v", got, want)
		}
		letOff(m, p)
		if m.Rider() != nil || p.vehicle != nil || p.riding {
			t.Error("still riding")
		}
		got := p.entity.Position()
		if feet := got[1] - eyeHeight; feet < float64(y) || feet > float64(y)+0.01 {
			t.Errorf("landed with feet at %v, floor at %d", feet, y)
		}
		if got[0] == at[0] && got[2] == at[2] {
			t.Errorf("landed in the cart at %v", got)
		}
	})
}
