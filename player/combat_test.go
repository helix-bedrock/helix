package player

import (
	"math"
	"testing"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 0.0001 }

// TestFoodFromItemData reads foods out of the items' own data, in both shapes BDS
// uses, and leaves other items alone.
func TestFoodFromItemData(t *testing.T) {
	foods := foodTable(testPlayer(t).s.reg.Items)
	apple, ok := foods["minecraft:apple"]
	if !ok || apple.Nutrition != 4 || !near(apple.SaturationModifier, 0.3) || apple.Duration != 32 || apple.CanAlwaysEat {
		t.Errorf("apple = %+v", apple)
	}
	golden := foods["minecraft:golden_apple"]
	if !golden.CanAlwaysEat || len(golden.Effects) != 2 || golden.Effects[0].Duration != 100 {
		t.Errorf("golden apple = %+v", golden)
	}
	if stew := foods["minecraft:mushroom_stew"]; stew.Becomes != "bowl" {
		t.Errorf("stew = %+v", stew)
	}
	if _, ok := foods["minecraft:stick"]; ok {
		t.Error("a stick is food")
	}
}

// TestAttackEffects pins Strength and Weakness to BDS's sums.
func TestAttackEffects(t *testing.T) {
	if got := strengthAttack(1, 8); !near(got, (8*1.3+1)*1.3+1) {
		t.Errorf("strength II = %v", got)
	}
	if got := weaknessAttack(0, 1); !near(got, 0.3) {
		t.Errorf("weakness I = %v", got)
	}
	if got := weaknessAttack(4, 1); got != 0 {
		t.Errorf("weakness never goes below 0: %v", got)
	}
}

// TestProtection pins each kind of armour enchantment to BDS's multiplier.
func TestProtection(t *testing.T) {
	for _, c := range []struct {
		kind  int
		cause DamageCause
		want  int32
	}{
		{protectAll, DamageEntityAttack, 4}, {protectAll, DamageStarve, 0}, {protectAll, DamageVoid, 0},
		{protectFall, DamageFall, 12}, {protectFall, DamageEntityAttack, 0},
		{protectFire, 6, 8}, {protectFire, DamageFall, 0}, {protectBlast, 10, 8}, {protectProjectile, 3, 8},
	} {
		if got := protectionKind(c.kind)(4, c.cause); got != c.want {
			t.Errorf("kind %d cause %d = %d, want %d", c.kind, c.cause, got, c.want)
		}
	}
}

// TestKnockbackStep pins one push: away along the direction, kept to the cap upward.
func TestKnockbackStep(t *testing.T) {
	p := testPlayer(t)
	v := p.knockbackStep([3]float32{}, 3, 4, knockbackPush, knockbackPush)
	if !near(v[0], 0.24) || !near(v[2], 0.32) || !near(v[1], knockbackCap) {
		t.Errorf("push = %v", v)
	}
	if v := p.knockbackStep([3]float32{}, 0, 0, knockbackPush, 0); v[0] != 0 || v[2] != 0 {
		t.Errorf("no direction = %v", v)
	}
}

// TestDamageCauses pins the sets BDS tests causes against.
func TestDamageCauses(t *testing.T) {
	if armorBypassed(DamageEntityAttack) || !armorBypassed(DamageFall) || !armorBypassed(DamageStarve) || !armorBypassed(DamageVoid) {
		t.Error("armour causes")
	}
	if !resisted(DamageEntityAttack) || !resisted(DamageFall) || resisted(DamageVoid) || resisted(DamageStarve) {
		t.Error("resistance causes")
	}
}

// TestWaterlogging runs a real region: a block that can hold water keeps a source it
// is placed in, and gives it back when it breaks; another block does not, and flowing
// water is not kept.
func TestWaterlogging(t *testing.T) {
	reg := testPlayer(t).s.reg
	db, err := mcdb.Config{Blocks: reg.Blocks}.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rng, _ := mcdb.Overworld.Range()
	flat, err := world.ParseFlat("", reg.Blocks, reg.Biomes, rng)
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

	water, _ := reg.Blocks.Block("minecraft:water")
	flowing, ok := reg.Blocks.StateID("minecraft:water", map[string]any{"liquid_depth": int32(3)})
	if !ok {
		t.Fatal("no flowing water")
	}
	fence, _ := reg.Blocks.Block("minecraft:oak_fence")
	stone, _ := reg.Blocks.Block("minecraft:stone")
	if !reg.Blocks.Properties(fence).CanContainLiquid {
		t.Fatal("a fence should hold water")
	}
	air := reg.Blocks.Air()
	at, other := cube.Pos{4, 0, 4}, cube.Pos{5, 0, 4}

	ticket := d.KeepLoaded(world.Chunks(mcdb.ChunkPos{}), true)
	defer ticket.Release()
	runIn(t, d, func(r *world.Region) {
		r.SetBlock(at, 0, water)
		r.SetBlock(at, 0, fence)
		if b, _ := r.Block(at, 1); b != water {
			t.Errorf("fence in water holds %d", b)
		}
		r.SetBlock(at, 0, air)
		if b, _ := r.Block(at, 0); b != water {
			t.Errorf("broken fence leaves %d", b)
		}
		if b, _ := r.Block(at, 1); b != air {
			t.Errorf("broken fence's second layer holds %d", b)
		}
		r.SetBlock(at, 0, fence)
		r.SetBlock(at, 0, stone)
		if b, _ := r.Block(at, 1); b != air {
			t.Errorf("stone keeps water: %d", b)
		}
		r.SetBlock(other, 0, flowing)
		r.SetBlock(other, 0, fence)
		if b, _ := r.Block(other, 1); b != air {
			t.Errorf("fence keeps flowing water: %d", b)
		}
	})
}
