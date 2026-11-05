package player

import (
	"math"
	"testing"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
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

// TestArmorShown notices a change of what is worn, and puts each piece in its place.
func TestArmorShown(t *testing.T) {
	p := testPlayer(t)
	if p.armorChanged() {
		t.Fatal("nothing worn, yet changed")
	}
	p.inventory.armor[0] = ItemStack{Name: "minecraft:diamond_helmet", Count: 1, netID: 7}
	p.inventory.armor[3] = ItemStack{Name: "minecraft:diamond_boots", Count: 1, netID: 8}
	if !p.armorChanged() {
		t.Fatal("armour put on, not noticed")
	}
	pk := p.armorEquipment()
	if pk.Helmet.Stack.Count != 1 || pk.Boots.Stack.Count != 1 || pk.Chestplate.Stack.Count != 0 || pk.Leggings.Stack.Count != 0 {
		t.Errorf("packet = %+v", pk)
	}
	p.showArmor()
	if p.armorChanged() {
		t.Error("shown, still changed")
	}
	p.inventory.armor[0] = ItemStack{}
	if !p.armorChanged() {
		t.Error("armour taken off, not noticed")
	}
}

// TestOffhandShown notices a new item and a new count, and sends BDS's window.
func TestOffhandShown(t *testing.T) {
	p := testPlayer(t)
	if p.offhandChanged() {
		t.Fatal("nothing held, yet changed")
	}
	p.inventory.offhand[0] = ItemStack{Name: "minecraft:totem_of_undying", Count: 1, netID: 9}
	if !p.offhandChanged() {
		t.Fatal("new item not noticed")
	}
	pk := p.offhandEquipment()
	if pk.WindowID != protocol.WindowIDOffHand || pk.InventorySlot != 0 || pk.HotBarSlot != 0 || pk.NewItem.Stack.Count != 1 {
		t.Errorf("packet = %+v", pk)
	}
	p.showOffhand()
	if p.offhandChanged() {
		t.Error("shown, still changed")
	}
	p.inventory.offhand[0].Count = 2
	if !p.offhandChanged() {
		t.Error("new count not noticed")
	}
}

// TestShieldRaise lifts a shield by BDS's rule: it blocks once it has been up long
// enough, not while swinging or on cooldown, and starts over when it comes down.
func TestShieldRaise(t *testing.T) {
	p := testPlayer(t)
	p.s.game = &gameplay{tuning: vanillaTuning(exhaustionValues{})}
	p.s.game.sys.ShieldUp = vanillaShieldUp
	p.attributes = DefaultAttributes()
	p.inventory.offhand[0] = ItemStack{Name: itemShield, Count: 1, netID: 3}
	p.move.flags = 1 << MoveSneaking
	if got := p.blocksAfter(shieldRaiseTicks); got != shieldRaiseTicks+1 {
		t.Errorf("raised after %d ticks", got)
	}
	p.move.flags = 0
	p.tickShield()
	if p.Blocking() || p.shield.raisedAt != 0 {
		t.Error("still up after standing up")
	}
	p.move.flags = 1 << MoveSneaking
	p.shield.cooldown = 3
	p.tickShield()
	if p.shield.blocking {
		t.Error("up during its cooldown")
	}
	p.shield.cooldown = 0
	p.startSwing()
	p.tickShield()
	if p.shield.blocking {
		t.Error("up during a swing")
	}
	p.inventory.offhand[0] = ItemStack{}
	p.inventory.main[p.inventory.held] = ItemStack{Name: itemShield, Count: 1, netID: 4}
	if p.heldShield() != &p.inventory.main[p.inventory.held] {
		t.Error("a held shield is not found")
	}
}

// blocksAfter counts the ticks until the player blocks, up to limit+2.
func (p *Player) blocksAfter(limit int) int {
	for i := 1; i <= limit+2; i++ {
		p.tickShield()
		if p.Blocking() {
			return i
		}
	}
	return limit + 3
}

// TestShieldFlags keeps the shield's flags in the second set, where BDS's numbers put them.
func TestShieldFlags(t *testing.T) {
	p := testPlayer(t)
	p.shield.blocking, p.shield.blocked = true, true
	want := int64(ownFlagsTwo | 1<<(protocol.EntityDataFlagBlocking-64) | 1<<(protocol.EntityDataFlagBlockedUsingShield-64))
	if got := p.actorFlagsTwo(); got != want {
		t.Errorf("flags = %x, want %x", got, want)
	}
}

// TestEffectNumbers pins Regeneration's interval to BDS's.
func TestEffectNumbers(t *testing.T) {
	for _, c := range []struct{ amplifier, every int32 }{{0, 50}, {1, 25}, {5, 1}, {40, 1}} {
		if got := max(int32(regenerationBase)>>min(c.amplifier, 31), 1); got != c.every {
			t.Errorf("level %d heals every %d", c.amplifier, got)
		}
	}
}

// TestEquipRefused leaves what may not go on where it is; putting armour on sends
// packets, so it is tested in game.
func TestEquipRefused(t *testing.T) {
	p := testPlayer(t)
	p.s.game = &gameplay{combat: &combatTable{armor: map[string]data.ArmorValue{"minecraft:iron_helmet": {}}}}
	if vanillaEquipArmor(p, ItemStack{Name: "minecraft:stick", Count: 1}) {
		t.Error("a stick went on")
	}
	if vanillaEquipArmor(p, ItemStack{Name: "minecraft:skull", Count: 1}) {
		t.Error("a head went on")
	}
	p.s.game.enchants.Store(newEnchantTable(map[int16]*Enchantment{enchantBinding: {ID: enchantBinding, StaysWorn: true}}))
	cursed := ItemStack{Name: "minecraft:iron_helmet", Count: 1, Tag: map[string]any{"ench": []any{map[string]any{"id": enchantBinding, "lvl": int16(1)}}}}
	p.inventory.armor[0] = cursed
	if vanillaEquipArmor(p, ItemStack{Name: "minecraft:iron_helmet", Count: 1}) {
		t.Error("a cursed helmet came off")
	}
	locked := ItemStack{Name: "minecraft:iron_helmet", Count: 1, Tag: map[string]any{"minecraft:item_lock": uint8(itemLockInSlot)}}
	p.inventory.armor[0] = ItemStack{}
	if vanillaEquipArmor(p, locked) {
		t.Error("a locked helmet went on")
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
