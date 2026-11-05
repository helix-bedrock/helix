package player

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// cartViewers makes a cart holding 10 emeralds in slot 0 and two players with it open, as
// openCart leaves them: the test players have no connection to be told on.
func cartViewers(t *testing.T, id string) (p, q *Player, m *Minecart, d *world.Dimension) {
	t.Helper()
	p, d, y := cartWorld(t, nameRail, railStates(0))
	at := mgl32.Vec3{10.5, float32(y) + 0.5, 10.5}
	m = p.s.SpawnMinecart(d, id, at)
	q = &Player{s: p.s, gameMode: GameModeSurvival}
	q.entity = d.AddEntity(world.EntityConfig{UniqueID: 2, RuntimeID: 2, Pos: at})
	p.gameMode = GameModeSurvival
	waitRegion(t, d, func(*world.Region) bool {
		return m.entity.Region() != nil && q.entity.Region() != nil && p.entity.Region() != nil
	})
	onRegion(d, func(r *world.Region) {
		m.Chest().Inventory(r).SetItem(0, ItemStack{Name: "minecraft:emerald", Count: 10})
		for _, v := range []*Player{p, q} {
			v.window.open, v.window.inv, v.window.kind, v.window.cart, v.window.dim = true, m.chest.Inventory(r), m.chest.Kind(), m, r.Dimension()
			m.chest.addViewer(r, v)
		}
	})
	return p, q, m, d
}

func takeFromCart(v *Player, count, slot byte, id, requestID int32) uint8 {
	a := take(count, slotInfo(containerLevelEntity, slot, id), slotInfo(containerCursor, cursorSlot, 0))
	return v.stackRequest(protocol.ItemStackRequest{RequestID: requestID, Actions: []protocol.StackRequestAction{a}}).Status
}

func TestCartOpensAsBDSSays(t *testing.T) {
	if got := cartSpecs[nameChestMinecart].opens; got != byte(protocol.ContainerTypeContainer) {
		t.Errorf("chest cart opens window %d", got)
	}
	if got := cartSpecs[nameHopperMinecart].opens; got != byte(protocol.ContainerTypeHopper) {
		t.Errorf("hopper cart opens window %d", got)
	}
}

// What a client says about a cart's slots is checked against the server's.
func TestCartClientCannotLie(t *testing.T) {
	p, _, m, d := cartViewers(t, nameHopperMinecart)
	onRegion(d, func(r *world.Region) {
		s := m.Chest().Inventory(r).Item(0)
		for name, status := range map[string]uint8{
			"wrong item ID":      takeFromCart(p, 1, 0, s.netID+1000, 1),
			"more than it holds": takeFromCart(p, 11, 0, s.netID, 2),
			"nothing":            takeFromCart(p, 0, 0, s.netID, 3),
			"an empty slot":      takeFromCart(p, 1, 1, s.netID, 4),
			// A hopper cart has five slots; its chest type's array has more.
			"past its slots": takeFromCart(p, 1, 5, s.netID, 5),
		} {
			if status == protocol.ItemStackResponseStatusOK {
				t.Errorf("%s was allowed", name)
			}
		}
		if got := m.Chest().Inventory(r).Item(0); got.Count != 10 || !p.inventory.ui[cursorSlot].Empty() {
			t.Errorf("refused takes changed the cart to %+v, cursor %+v", got, p.inventory.ui[cursorSlot])
		}
	})
}

// Two players on one stack: the second is held to what the first left.
func TestTwoPlayersCannotDoubleTakeFromCart(t *testing.T) {
	p, q, m, d := cartViewers(t, nameChestMinecart)
	onRegion(d, func(r *world.Region) {
		id := m.Chest().Inventory(r).Item(0).netID
		if status := takeFromCart(p, 10, 0, id, 1); status != protocol.ItemStackResponseStatusOK {
			t.Fatalf("first take: status %d", status)
		}
		if status := takeFromCart(q, 10, 0, id, 1); status == protocol.ItemStackResponseStatusOK {
			t.Error("the second player took the same stack")
		}
		if got, none := p.inventory.ui[cursorSlot].Count, q.inventory.ui[cursorSlot]; got != 10 || !none.Empty() {
			t.Errorf("cursors hold %d and %+v", got, none)
		}
		if !m.Chest().Inventory(r).Item(0).Empty() {
			t.Errorf("cart still holds %+v", m.Chest().Inventory(r).Item(0))
		}
	})
}

func TestTwoPlayersShareACartStack(t *testing.T) {
	p, q, m, d := cartViewers(t, nameChestMinecart)
	onRegion(d, func(r *world.Region) {
		id := m.Chest().Inventory(r).Item(0).netID
		if status := takeFromCart(p, 6, 0, id, 1); status != protocol.ItemStackResponseStatusOK {
			t.Fatalf("first take: status %d", status)
		}
		if status := takeFromCart(q, 6, 0, id, 1); status == protocol.ItemStackResponseStatusOK {
			t.Error("the second player took more than was left")
		}
		if status := takeFromCart(q, 4, 0, id, 2); status != protocol.ItemStackResponseStatusOK {
			t.Errorf("the second player could not take what was left: status %d", status)
		}
		total := int(p.inventory.ui[cursorSlot].Count) + int(q.inventory.ui[cursorSlot].Count) + int(m.Chest().Inventory(r).Item(0).Count)
		if total != 10 {
			t.Errorf("10 emeralds became %d", total)
		}
	})
}

// A broken cart spills its items once, and its window takes nothing more.
func TestBrokenCartWindowTakesNothing(t *testing.T) {
	p, _, m, d := cartViewers(t, nameChestMinecart)
	onRegion(d, func(r *world.Region) {
		id := m.Chest().Inventory(r).Item(0).netID
		m.Break(r, false)
		if status := takeFromCart(p, 10, 0, id, 1); status == protocol.ItemStackResponseStatusOK {
			t.Error("took from a broken cart")
		}
		if !p.inventory.ui[cursorSlot].Empty() || p.window.inv != nil {
			t.Errorf("cursor %+v, window open %v", p.inventory.ui[cursorSlot], p.window.inv != nil)
		}
	})
}

// A cart in another region is another core's, and one unloaded may already be saved: its
// slots in memory are out of reach, whatever window the player still has.
func TestCartInOtherRegionTakesNothing(t *testing.T) {
	p, d, y := cartWorld(t, nameRail, railStates(0))
	far := mcdb.ChunkPos{100, 100}
	ticket := d.KeepLoaded(world.Chunks(far), true)
	t.Cleanup(ticket.Release)
	m := p.s.SpawnMinecart(d, nameChestMinecart, mgl32.Vec3{1600.5, float32(y) + 0.5, 1600.5})
	done := make(chan struct{})
	d.RunAt(far, func(r *world.Region) {
		m.chest.items[0] = ItemStack{Name: "minecraft:emerald", Count: 10, netID: 77}
		close(done)
	})
	<-done
	waitRegion(t, d, func(r *world.Region) bool {
		owner := m.entity.Region()
		return owner != nil && owner != r
	})
	onRegion(d, func(r *world.Region) {
		p.window.open, p.window.inv, p.window.kind, p.window.cart, p.window.dim = true, m.chest.Inventory(r), m.chest.Kind(), m, r.Dimension()
		if status := takeFromCart(p, 10, 0, 77, 1); status == protocol.ItemStackResponseStatusOK {
			t.Error("took from a cart in another region")
		}
		if !p.inventory.ui[cursorSlot].Empty() {
			t.Errorf("cursor %+v", p.inventory.ui[cursorSlot])
		}
		p.flush = func() {}
		p.tickChest()
		if p.window.inv != nil {
			t.Error("the window stayed open")
		}
	})
}

// A hopper taking from a cart that is open: what the window then takes is what is left.
func TestHopperAndPlayerShareACart(t *testing.T) {
	p, _, m, d := cartViewers(t, nameChestMinecart)
	onRegion(d, func(r *world.Region) {
		s := m.Chest().Inventory(r).Item(0)
		s.Count = 1
		m.Chest().Inventory(r).SetItem(0, s)
		if status := takeFromCart(p, 2, 0, m.Chest().Inventory(r).Item(0).netID, 1); status == protocol.ItemStackResponseStatusOK {
			t.Error("took more than the hopper left")
		}
	})
}

func TestPushDirection(t *testing.T) {
	at := cube.Pos{0, 0, 0}
	var open, top [pushNowhere]bool
	for _, n := range []int{pushDown, pushNorth, pushSouth, pushWest, pushEast} {
		top[n] = true
	}
	var all [pushNowhere]bool
	for n := range all {
		all[n] = true
	}
	var eastOnly = all
	eastOnly[pushEast] = false
	for _, c := range []struct {
		name   string
		pos    mgl32.Vec3
		closed *[pushNowhere]bool
		want   int
	}{
		{"nearest is down", mgl32.Vec3{0.5, 0.1, 0.5}, &open, pushDown},
		{"nearest is up", mgl32.Vec3{0.5, 0.9, 0.5}, &open, pushUp},
		{"nearest is west", mgl32.Vec3{0.1, 0.5, 0.5}, &open, pushWest},
		{"nearest is north", mgl32.Vec3{0.5, 0.5, 0.1}, &open, pushNorth},
		{"nearest is south", mgl32.Vec3{0.5, 0.5, 0.9}, &open, pushSouth},
		// BDS counts east as a whole block, so it goes down instead though east is nearer.
		{"east loses to down", mgl32.Vec3{0.9, 0.5, 0.5}, &open, pushDown},
		{"only the top is open", mgl32.Vec3{0.1, 0.1, 0.1}, &top, pushUp},
		{"only east is open", mgl32.Vec3{0.1, 0.1, 0.1}, &eastOnly, pushEast},
		{"nothing is open", mgl32.Vec3{0.5, 0.5, 0.5}, &all, pushNowhere},
	} {
		if got := pushDirection(at, c.pos, c.closed); got != c.want {
			t.Errorf("%s: went %d, want %d", c.name, got, c.want)
		}
	}
}

// inStone reports whether the item's box is in the block at its bottom.
func inStone(i *ItemActor, r *world.Region, stone uint32) bool {
	pos := i.entity.Position()
	b, ok := r.Block(floorPos(mgl32.Vec3{pos[0], pos[1] - itemHalfSize, pos[2]}), 0)
	return ok && b == stone
}

func TestItemPushedOutOfBlockPlacedOnIt(t *testing.T) {
	p, d, y := cartWorld(t, "minecraft:air", nil)
	stone, _ := p.s.reg.Blocks.StateID("minecraft:stone", nil)
	var i *ItemActor
	onRegion(d, func(r *world.Region) {
		i = p.s.spawnItem(r.Dimension(), ItemStack{Name: "minecraft:emerald", Count: 1},
			mgl32.Vec3{4.3, float32(y) + itemHalfSize, 4.5}, mgl32.Vec3{}, world.Rotation{}, 0, 0)
	})
	waitRegion(t, d, func(*world.Region) bool { return i.entity.Region() != nil })
	at := cube.Pos{4, y, 4}
	onRegion(d, func(r *world.Region) { r.SetBlock(at, 0, stone) })
	waitRegion(t, d, func(r *world.Region) bool { return !inStone(i, r, stone) })
	onRegion(d, func(r *world.Region) {
		if got := floorPos(i.entity.Position()); got == at {
			t.Errorf("still at %v", got)
		}
	})
}

// With every side closed it rises through the blocks with its collision off.
func TestItemWalledInRisesOut(t *testing.T) {
	p, d, y := cartWorld(t, "minecraft:air", nil)
	stone, _ := p.s.reg.Blocks.StateID("minecraft:stone", nil)
	at := cube.Pos{4, y + 1, 4}
	var i *ItemActor
	onRegion(d, func(r *world.Region) {
		for _, f := range []cube.Face{cube.FaceDown, cube.FaceUp, cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast} {
			r.SetBlock(at.Side(f), 0, stone)
		}
		i = p.s.spawnItem(r.Dimension(), ItemStack{Name: "minecraft:emerald", Count: 1},
			mgl32.Vec3{4.5, float32(at[1]) + 0.5, 4.5}, mgl32.Vec3{}, world.Rotation{}, 0, 0)
	})
	waitRegion(t, d, func(*world.Region) bool { return i.entity.Region() != nil })
	onRegion(d, func(r *world.Region) { r.SetBlock(at, 0, stone) })
	waitRegion(t, d, func(*world.Region) bool { return i.noClip })
	waitRegion(t, d, func(r *world.Region) bool { return !inStone(i, r, stone) && !i.noClip })
	onRegion(d, func(r *world.Region) {
		if got := i.entity.Position()[1]; got <= float32(at[1])+1 {
			t.Errorf("came out at y %v, under the block over it at %d", got, at[1]+1)
		}
	})
}
