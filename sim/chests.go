package sim

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

const (
	// chestSlots is a single chest's slots, all filled.
	chestSlots = 27
	// chestLoreLines and chestLoreLength make each item carry about 2 KB of lore, as a
	// lag machine would stuff chests with written books or named items.
	chestLoreLines  = 20
	chestLoreLength = 100
	// chestColumn is how far apart machines stand along X (redstone block, piston, chest,
	// the chest's pushed place, a gap), chestRow along Z.
	chestColumn = 5
	chestRow    = 2
	// chestPeriod is how many ticks the redstone block stays and stays away, so each
	// piston pushes and pulls its chest once every 2*chestPeriod ticks.
	chestPeriod = 4
	// pistonEast is the sticky piston's facing_direction pointing east.
	pistonEast = int32(4)
)

// chestMachine is the states a chest machine is made of, and the item it is filled with.
type chestMachine struct {
	piston, chest, power, air uint32
	item                      player.ItemStack
}

// startChests builds n machines: a sticky piston facing east with a chest in front, every
// slot filled with an item carrying a lot of lore. tickChests powers and unpowers them.
func startChests(ctx context.Context, w *World, n int) error {
	m, err := chestStates(w)
	if err != nil {
		return err
	}
	inner := int(w.size) - 2*clockMargin
	perRow := inner / chestColumn
	if n > perRow*(inner/chestRow) {
		return fmt.Errorf("sim: %d chest machines do not fit a %d block area", n, w.size)
	}
	var failed int
	err = w.Do(ctx, mcdb.ChunkPos{0, 0}, func(r *world.Region) {
		for i := range n {
			failed += placeChestMachine(r, w, w.chestMachineAt(i, perRow), m)
		}
	})
	if err == nil && failed > 0 {
		err = fmt.Errorf("sim: %d chest machine blocks could not be placed", failed)
	}
	return err
}

// chestStates looks the machine's states and item up.
func chestStates(w *World) (chestMachine, error) {
	b := w.Server.Registry().Blocks
	piston, okP := withState(b, "minecraft:sticky_piston", "facing_direction", pistonEast)
	chest, okC := b.Block("minecraft:chest")
	power, okR := b.Block("minecraft:redstone_block")
	if !okP || !okC || !okR {
		return chestMachine{}, fmt.Errorf("sim: piston, chest or redstone block states missing")
	}
	it, err := w.Item("minecraft:paper", 1)
	if err != nil {
		return chestMachine{}, err
	}
	lines := make([]string, chestLoreLines)
	for i := range lines {
		lines[i] = strings.Repeat(string(rune('a'+i%26)), chestLoreLength)
	}
	return chestMachine{piston: piston, chest: chest, power: power, air: b.Air(), item: it.WithLore(lines...)}, nil
}

// chestMachineAt is where the piston of machine i stands.
func (w *World) chestMachineAt(i, perRow int) cube.Pos {
	x0, z0, _, _ := w.Area()
	return cube.Pos{int(x0) + clockMargin + i%perRow*chestColumn + 1, int(w.Surface()), int(z0) + clockMargin + i/perRow*chestRow}
}

// placeChestMachine places the piston and the filled chest, returning how many blocks
// could not be set.
func placeChestMachine(r *world.Region, w *World, at cube.Pos, m chestMachine) int {
	chestPos := at.Add(cube.Pos{1, 0, 0})
	failed := place(r, at, m.piston) + place(r, chestPos, m.chest)
	c := w.Host.ChestAt(r, chestPos)
	if c == nil {
		return failed + 1
	}
	inv := c.Inventory(r)
	for slot := range chestSlots {
		inv.SetItem(slot, m.item)
	}
	return failed
}

// tickChests places the redstone blocks behind every piston for chestPeriod ticks, then
// takes them away for as long.
func tickChests(w *World, n, tick int) {
	if tick%chestPeriod != 0 {
		return
	}
	m, err := chestStates(w)
	if err != nil {
		return
	}
	block := m.power
	if tick/chestPeriod%2 == 1 {
		block = m.air
	}
	inner := int(w.size) - 2*clockMargin
	perRow := inner / chestColumn
	first := w.chestMachineAt(0, perRow)
	w.Post(mgl32.Vec3{float32(first[0]), float32(first[1]), float32(first[2])}, func(r *world.Region) {
		for i := range n {
			place(r, w.chestMachineAt(i, perRow).Add(cube.Pos{-1, 0, 0}), block)
		}
	})
}
