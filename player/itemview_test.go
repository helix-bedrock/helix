package player

import (
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/mcdb"
)

// packetLog keeps what a player's viewer was sent.
type packetLog struct {
	mu  sync.Mutex
	pks []packet.Packet
}

func (l *packetLog) add(pk packet.Packet) {
	l.mu.Lock()
	l.pks = append(l.pks, pk)
	l.mu.Unlock()
}

// aboutItem returns the packets that name the item.
func (l *packetLog) aboutItem(runtimeID uint64, uniqueID int64) []packet.Packet {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []packet.Packet
	for _, pk := range l.pks {
		switch v := pk.(type) {
		case *packet.AddItemActor:
			if v.EntityRuntimeID == runtimeID {
				out = append(out, pk)
			}
		case *packet.MoveActorAbsolute:
			if v.EntityRuntimeID == runtimeID {
				out = append(out, pk)
			}
		case *packet.ActorEvent:
			if v.EntityRuntimeID == runtimeID {
				out = append(out, pk)
			}
		case *packet.SetActorData:
			if v.EntityRuntimeID == runtimeID {
				out = append(out, pk)
			}
		case *packet.RemoveActor:
			if v.EntityUniqueID == uniqueID {
				out = append(out, pk)
			}
		}
	}
	return out
}

// TestItemHiddenFromOthers runs a real region with two players standing by an
// item only the first may see: the second is sent nothing about it at all, not
// even when the stack changes, and may not pick it up.
func TestItemHiddenFromOthers(t *testing.T) {
	seer, hidden := testPlayer(t), testPlayer(t)
	hidden.s = seer.s
	reg := seer.s.reg
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
	d, err := world.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	logs := map[*Player]*packetLog{seer: {}, hidden: {}}
	pos := mgl64.Vec3{8, -58, 8}
	for p, l := range logs {
		p.viewer = d.AddViewer(l.add, func() {})
		defer p.viewer.Close()
		p.viewer.Move(world.ChunkPos(8, 8), 2)
		p.entity = d.AddEntity(world.EntityConfig{UniqueID: int64(p.s.runtimeIDs.Add(1)), RuntimeID: p.s.runtimeIDs.Add(1), Pos: pos, Viewer: p.viewer, Owner: p})
	}

	it := seer.s.newItem(ItemStack{Name: "minecraft:emerald", Count: 3}, mgl64.Vec3{0, 0.2, 0}, dropPickupDelay, dropThrowTime)
	it.SetVisibility(func(p *Player) bool { return p == seer })
	it.addTo(d, mgl64.Vec3{9, -55, 9}, world.Rotation{})

	id, unique := it.entity.RuntimeID(), it.entity.UniqueID()
	deadline := time.Now().Add(15 * time.Second)
	for len(logs[seer].aboutItem(id, unique)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the player allowed to see the item was never shown it")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Enough ticks for moves, then a stack change through the world's sends.
	time.Sleep(300 * time.Millisecond)
	d.RunAt(world.ChunkPos(9, 9), func(*world.Region) { it.SetStack(ItemStack{Name: "minecraft:emerald", Count: 5}) })
	time.Sleep(300 * time.Millisecond)

	if got := logs[hidden].aboutItem(id, unique); len(got) != 0 {
		t.Fatalf("hidden player was sent %d packets about the item: %T", len(got), got[0])
	}
	if it.CanSee(hidden) || !it.CanSee(seer) {
		t.Fatal("CanSee does not follow the rule")
	}
	var sawCount bool
	for _, pk := range logs[seer].aboutItem(id, unique) {
		if e, ok := pk.(*packet.ActorEvent); ok && e.EventData == 5 {
			sawCount = true
		}
	}
	if !sawCount {
		t.Fatal("the player who sees the item was not told its new count")
	}
}

// TestItemLifetime checks an item's own lifetime wins over the server's, which
// wins over vanilla's.
func TestItemLifetime(t *testing.T) {
	p := testPlayer(t)
	i := p.s.newItem(ItemStack{Name: "minecraft:emerald", Count: 1}, mgl64.Vec3{}, 0, 0)
	i.age = 100
	if got := i.TicksLeft(); got != itemLifetime-100 {
		t.Fatalf("vanilla: %d", got)
	}
	p.s.SetItemLifetime(200)
	if got := i.TicksLeft(); got != 100 {
		t.Fatalf("server: %d", got)
	}
	i.SetLifetime(ItemLifetimeForever)
	if got := i.TicksLeft(); got != ItemLifetimeForever {
		t.Fatalf("own: %d", got)
	}
	o := p.s.newItem(ItemStack{Name: "minecraft:emerald", Count: 1}, mgl64.Vec3{}, 0, 0)
	if i.merge(o) {
		t.Fatal("an item with its own lifetime merged")
	}
}

// TestItemsSetApartDoNotMerge checks the merge rule.
func TestItemsSetApartDoNotMerge(t *testing.T) {
	p := testPlayer(t)
	stack := ItemStack{Name: "minecraft:emerald", Count: 3}
	a := p.s.newItem(stack, mgl64.Vec3{}, 0, 0)
	b := p.s.newItem(stack, mgl64.Vec3{}, 0, 0)
	b.SetVisibility(func(*Player) bool { return true })
	if a.merge(b) || b.merge(a) {
		t.Fatal("an item with a visibility rule merged")
	}
	c := p.s.newItem(stack, mgl64.Vec3{}, 0, 0)
	c.SetView(func(*Player, *ItemView) {})
	if a.setApart(c) || c.setApart(a) {
		t.Fatal("an item with only a view was kept apart")
	}
}
