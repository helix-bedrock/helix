package player

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// The client asks to open a chest or hopper cart with an Interact aimed at the cart: one
// in reach is found by its ID, one too far away is not.
func TestInteractFindsCartInReach(t *testing.T) {
	for _, kind := range []string{nameChestMinecart, nameHopperMinecart} {
		t.Run(kind, func(t *testing.T) {
			p, d, y := cartWorld(t, nameRail, railStates(0))
			far := p.s.SpawnMinecart(d, kind, mgl32.Vec3{3.5, float32(y) + 0.5, 8.5})
			near := p.s.SpawnMinecart(d, kind, p.entity.Position().Add(mgl32.Vec3{1, 1, 0}))
			waitRegion(t, d, func(*world.Region) bool {
				return p.cartNear(far.entity.RuntimeID()) != nil && p.cartNear(near.entity.RuntimeID()) != nil
			})
			onRegion(d, func(*world.Region) {
				if p.cartInReach(far.entity.RuntimeID()) != nil {
					t.Error("found a cart out of reach")
				}
				if p.cartInReach(near.entity.RuntimeID()) != near {
					t.Error("did not find the cart in reach")
				}
			})
		})
	}
}

// A hopper's cooldown after a move and after a pick up are its own numbers.
func TestHopperCooldownsFromConfig(t *testing.T) {
	p, d := machineWorld(t)
	conf := *p.s.redstone.Get()
	conf.Hopper.MoveCooldown, conf.Hopper.PickupCooldown = 300, 700
	p.s.redstone.Set(conf)
	var c *Chest
	var at cube.Pos
	onRegion(d, func(r *world.Region) {
		y := surface(r, p.s.reg.Blocks.Air(), 3, 8)
		at = cube.Pos{3, y, 8}
		setNamed(t, p, r, at.Side(cube.FaceDown), "minecraft:chest", nil)
		setNamed(t, p, r, at, "minecraft:hopper", map[string]any{"facing_direction": int32(0)})
		c = p.s.chestAt(r, at)
		c.SetItem(r, 0, ItemStack{Name: "minecraft:emerald", Count: 2})
	})
	waitRegion(t, d, func(*world.Region) bool { return c.cooldown > 200 && c.cooldown <= 300 })
	onRegion(d, func(r *world.Region) {
		c.SetItem(r, 0, ItemStack{})
		c.cooldown = 0
		p.s.spawnItem(d, ItemStack{Name: "minecraft:emerald", Count: 1}, mgl32.Vec3{3.5, float32(at[1]) + 1.2, 8.5}, mgl32.Vec3{}, world.Rotation{}, 0, 0)
	})
	waitRegion(t, d, func(*world.Region) bool { return c.cooldown > 300 })
}

// More items per move moves that many at once.
func TestHopperItemsPerMove(t *testing.T) {
	p, d := machineWorld(t)
	conf := *p.s.redstone.Get()
	conf.Hopper.ItemsPerMove, conf.Hopper.MoveCooldown = 3, 1000
	p.s.redstone.Set(conf)
	var c *Chest
	onRegion(d, func(r *world.Region) {
		at := cube.Pos{3, surface(r, p.s.reg.Blocks.Air(), 3, 8), 8}
		setNamed(t, p, r, at.Side(cube.FaceDown), "minecraft:chest", nil)
		setNamed(t, p, r, at, "minecraft:hopper", map[string]any{"facing_direction": int32(0)})
		c = p.s.chestAt(r, at)
		c.SetItem(r, 0, ItemStack{Name: "minecraft:emerald", Count: 10})
	})
	waitRegion(t, d, func(r *world.Region) bool { return c.Item(r, 0).Count == 7 })
}
