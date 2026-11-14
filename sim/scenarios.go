package sim

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/world"
)

// How long the loads that end by themselves live, in ticks, so Tick replaces them at the
// rate that keeps about n alive.
const (
	// tntFuse is BDS's fuse for lit TNT.
	tntFuse = 80
	// fallHeight is how far above the ground falling blocks start; fallTicks about how
	// long they take to land from there.
	fallHeight = 40
	fallTicks  = 45
	// rocketTicks is about how long a firework rocket with no Flight flies.
	rocketTicks = 30
	// regenerationPotion is the potion ID of Regeneration, the clouds' potion.
	regenerationPotion = 28
)

// itemKinds are the items the item scenario drops, taken in turn so neighbours differ and
// do not merge: the scenario is about many items, not one big stack.
func itemKinds() []string {
	return []string{
		"minecraft:dirt", "minecraft:cobblestone", "minecraft:oak_log", "minecraft:stone",
		"minecraft:sand", "minecraft:apple", "minecraft:stick", "minecraft:iron_ingot",
	}
}

// Scenarios are the built-in scenarios, a new list each call.
func Scenarios() []Scenario {
	return append(loadScenarios(), botScenarios()...)
}

// loadScenarios are the scenarios without bots.
func loadScenarios() []Scenario {
	return []Scenario{
		{Name: "idle", About: "the loaded area alone, nothing added: the baseline", Unit: "-"},
		{Name: "items", About: "dropped items lying on the ground, kinds mixed so they never merge", Unit: "items", Start: startItems},
		{Name: "tnt", About: "lit TNT exploding all the time, about n lit at once", Unit: "TNT", Tick: tickTNT},
		{Name: "falling", About: "sand falling from 40 blocks up all the time, about n in the air", Unit: "blocks", Tick: tickFalling},
		{Name: "fireworks", About: "firework rockets launched all the time, about n in the air", Unit: "rockets", Tick: tickFireworks},
		{Name: "mobs", About: "cows wandering the area", Unit: "cows", Start: startMobs},
		{Name: "clouds", About: "lingering Regeneration clouds (no players to touch, so only their own tick)", Unit: "clouds", Start: startClouds},
		{Name: "redstone", About: "observer clocks (two observers facing each other) each driving a line of redstone dust", Unit: "clocks", Size: redstoneSize, Start: startClocks},
		{Name: "chests", About: "sticky pistons pushing and pulling chests full of items with 2 KB of lore each", Unit: "chests", Size: redstoneSize, Start: startChests, Tick: tickChests},
	}
}

// Find is the built-in scenario named name.
func Find(name string) (Scenario, bool) {
	for _, sc := range Scenarios() {
		if sc.Name == name {
			return sc, true
		}
	}
	return Scenario{}, false
}

// spawnsThisTick is how many to spawn on tick so that, each living life ticks, about n
// are alive once the first have died: n/life a tick, the remainder spread evenly.
func spawnsThisTick(n, life, tick int) int {
	return n*(tick+1)/life - n*tick/life
}

// startItems drops n items on a one block grid over the area, in layers when n is larger
// than the area, each cell's kind differing from its neighbours'.
func startItems(_ context.Context, w *World, n int) error {
	kinds := itemKinds()
	stacks := make([]player.ItemStack, len(kinds))
	for i, name := range kinds {
		it, err := w.Item(name, 1)
		if err != nil {
			return err
		}
		stacks[i] = it
	}
	x0, z0, _, _ := w.Area()
	side := int(w.size)
	for i := range n {
		x, z, layer := i%side, i/side%side, i/(side*side)
		pos := mgl32.Vec3{float32(x0) + float32(x) + 0.5, w.Surface() + float32(layer) + 0.5, float32(z0) + float32(z) + 0.5}
		w.Host.SpawnItem(w.Dim, stacks[(x*3+z*5+layer)%len(stacks)], pos, mgl32.Vec3{})
	}
	return nil
}

// tickTNT lights TNT at random spots on BDS's fuse.
func tickTNT(w *World, n, tick int) {
	for range spawnsThisTick(n, tntFuse, tick) {
		w.Host.SpawnTNT(w.Dim, w.RandomSpot(0), tntFuse, nil)
	}
}

// tickFalling drops sand from fallHeight over random spots.
func tickFalling(w *World, n, tick int) {
	sand, ok := w.Server.Registry().Blocks.Block("minecraft:sand")
	if !ok {
		return
	}
	for range spawnsThisTick(n, fallTicks, tick) {
		w.Host.SpawnFallingBlock(w.Dim, w.RandomSpot(fallHeight), sand)
	}
}

// tickFireworks launches plain rockets straight up from random spots, on the owner of
// each spot's chunk as LaunchFirework needs.
func tickFireworks(w *World, n, tick int) {
	rocket, err := w.Item("minecraft:firework_rocket", 1)
	if err != nil {
		return
	}
	up := mgl32.Vec3{0, 1, 0}
	for range spawnsThisTick(n, rocketTicks, tick) {
		pos := w.RandomSpot(0)
		w.Post(pos, func(r *world.Region) { w.Host.LaunchFirework(r.Dimension(), pos, up, rocket, nil) })
	}
}

// startMobs spawns n cows at random spots.
func startMobs(_ context.Context, w *World, n int) error {
	for range n {
		if _, ok := w.Host.SpawnMob(w.Dim, "minecraft:cow", w.RandomSpot(0), w.Rand.Float32()*360); !ok {
			return errors.New("sim: cows cannot be spawned")
		}
	}
	return nil
}

// startClouds leaves n lingering Regeneration clouds at random spots, shaped as a thrown
// lingering potion's.
func startClouds(_ context.Context, w *World, n int) error {
	spec, ok := w.Host.ProjectileSpec("minecraft:lingering_potion")
	if !ok || spec.Cloud == nil {
		return fmt.Errorf("sim: lingering potions leave no cloud")
	}
	for range n {
		w.Host.SpawnCloud(w.Dim, w.RandomSpot(0), regenerationPotion, *spec.Cloud, nil)
	}
	return nil
}
