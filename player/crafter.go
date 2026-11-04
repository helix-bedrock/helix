package player

import (
	"maps"
	"math/bits"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// BDS's names and numbers for a crafter.
const (
	crafterBlock       = "minecraft:crafter"
	crafterEntityID    = "Crafter"
	crafterSize        = 9
	crafterGridWidth   = 3
	disabledSlotsTag   = "disabled_slots"
	stateOrientation   = "orientation"
	stateCrafting      = "crafting"
	allCrafterSlots    = 1<<crafterSize - 1
	crafterEjectHeight = 0.3
	// levelEventCrafterCraft is the crafter's smoke, with data 4 + x + 3z of the face.
	levelEventCrafterCraft = 3609
)

// crafterOrientations is BDS's orientation state, in its order: the first part is the way
// the crafter faces.
var crafterOrientations = [...]string{
	"down_east", "down_north", "down_south", "down_west", "up_east", "up_north", "up_south", "up_west",
	"west_up", "east_up", "north_up", "south_up",
}

// Which of a side's orientations a crafter is placed in: looking down at it or up at it
// from close by turns the crafter to the player, with the top as the way the player faces.
var (
	crafterDownOf = map[cube.Face]int{cube.FaceNorth: 1, cube.FaceSouth: 2, cube.FaceWest: 3, cube.FaceEast: 0}
	crafterUpOf   = map[cube.Face]int{cube.FaceNorth: 5, cube.FaceSouth: 6, cube.FaceWest: 7, cube.FaceEast: 4}
	crafterSideOf = map[cube.Face]int{cube.FaceNorth: 10, cube.FaceSouth: 11, cube.FaceWest: 8, cube.FaceEast: 9}
)

// crafter is BDS's getPlacementBlock for a crafter. Its table is by the opposite of the way
// the player looks.
func (s *placeState) crafter() {
	face := direction.FromYaw(s.yaw).Opposite().Face()
	table := crafterSideOf
	if at := s.pl.Player.entity.Position(); absInt(int(floorInt(at[0]))-s.pl.Pos[0]) <= 1 && absInt(int(floorInt(at[2]))-s.pl.Pos[2]) <= 1 {
		y := float32(s.pl.Pos[1])
		switch {
		case s.feet > y:
			table = crafterUpOf
		case y > s.feet+playerHeight:
			table = crafterDownOf
		}
	}
	s.states[stateOrientation] = crafterOrientations[table[face]]
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// crafterFace is the way a crafter faces, down if its state is not one of BDS's.
func (s *Host) crafterFace(block uint32) cube.Face {
	name, _ := s.reg.Blocks.States(block)[stateOrientation].(string)
	first, _, _ := strings.Cut(name, "_")
	switch first {
	case "up":
		return cube.FaceUp
	case "north":
		return cube.FaceNorth
	case "south":
		return cube.FaceSouth
	case "west":
		return cube.FaceWest
	case "east":
		return cube.FaceEast
	}
	return cube.FaceDown
}

// crafterTakes is BDS's canPlaceItem for a crafter: never a disabled slot, an empty one
// always, and a slot holding the same item only if it is not full and no later enabled slot
// is empty or holds less of it. That spreads items evenly over the grid.
func (c *Chest) crafterTakes(slot int, s ItemStack) bool {
	if c.slotDisabled(slot) {
		return false
	}
	cur := c.items[slot]
	if cur.Empty() {
		return true
	}
	if !cur.stacksWith(s) || cur.Count >= cur.maxStack(c.s.reg) {
		return false
	}
	for i := slot + 1; i < crafterSize; i++ {
		if c.slotDisabled(i) {
			continue
		}
		if later := c.items[i]; later.Empty() || cur.Count > later.Count && later.stacksWith(cur) {
			return false
		}
	}
	return true
}

// slotDisabled says the crafter's slot is turned off.
func (c *Chest) slotDisabled(slot int) bool { return c.disabled&(1<<slot) != 0 }

// crafterSignal is BDS's comparator reading of a crafter: disabled slots and filled ones.
func (c *Chest) crafterSignal() int32 {
	if c.disabled&allCrafterSlots == allCrafterSlots {
		return crafterSize
	}
	n := bits.OnesCount16(c.disabled & allCrafterSlots)
	for i := range crafterSize {
		if !c.items[i].Empty() {
			n++
		}
	}
	return int32(n) //nolint:gosec // at most 18
}

// setBlockState changes one state of the block at pos, telling no neighbours: BDS's flag 2.
func (c *Chest) setBlockState(r *world.Region, pos cube.Pos, key string, v any) {
	block, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	blocks := c.s.reg.Blocks
	states := maps.Clone(blocks.States(block))
	if _, has := states[key]; !has {
		return
	}
	states[key] = v
	if next, ok := blocks.StateID(blocks.Name(block), states); ok {
		r.SetBlock(pos, 0, next)
	}
}

// tickCrafter takes a crafter out of the look it has after crafting.
func (c *Chest) tickCrafter(r *world.Region, pos cube.Pos) {
	if c.crafting <= 0 {
		return
	}
	c.crafting--
	if c.crafting == 0 {
		c.setBlockState(r, pos, stateCrafting, uint8(0))
	}
}

// findCrafterRecipe is the first recipe of a crafting table that fits the grid.
func (c *Chest) findCrafterRecipe() *crafting.Recipe {
	grid := crafting.Grid{Items: make([]crafting.Item, crafterSize), Width: crafterGridWidth}
	filled := false
	for i := range crafterSize {
		grid.Items[i] = c.s.craftItem(c.items[i])
		filled = filled || !c.items[i].Empty()
	}
	if !filled {
		return nil
	}
	return c.s.Recipes().Find(craftingTableRecipes, grid)
}

// craft is BDS's crafter tick: the grid's recipe goes into the container in front, and
// what has no room flies out with smoke and a sound. Each filled slot then loses one. A grid
// with no recipe only makes the fail sound. Recipes made in code (fireworks, banners and so
// on) are not in Helix's table, and composters are not built, so neither is reached.
func (c *Chest) craft(r *world.Region, pos cube.Pos, block uint32) {
	s := c.s
	recipe := c.findCrafterRecipe()
	if recipe == nil {
		r.Broadcast(pos, &packet.LevelSoundEvent{SoundType: packet.SoundEventCrafterFail, Position: blockVec(pos), ExtraData: -1}, nil)
		return
	}
	face := s.crafterFace(block)
	to := s.chestAt(r, pos.Side(face))
	results := make([]ItemStack, 0, len(recipe.Output))
	for _, out := range recipe.Output {
		left := s.freshStack(itemFromNetwork(s.reg, out))
		for to != nil && !to.ender && !left.Empty() && to.takeOne(r, left) {
			left.Count--
		}
		if !left.Empty() {
			results = append(results, left)
		}
	}
	if len(results) != 0 {
		c.throwResults(r, pos, face, results)
	}
	for i := range crafterSize {
		if it := c.items[i]; !it.Empty() {
			it.Count--
			c.SetItem(r, i, it)
		}
	}
	c.crafting = c.s.redstone.Get().Crafter.ShowTicks
	c.setBlockState(r, pos, stateCrafting, uint8(1))
}

// throwResults throws what a crafter made out of its front.
func (c *Chest) throwResults(r *world.Region, pos cube.Pos, face cube.Face, results []ItemStack) {
	dir := faceVec(face)
	at := blockVec(pos).Add(mgl32.Vec3{0.5, crafterEjectHeight, 0.5}).Add(dir.Mul(ejectReach))
	levelEvent(r, pos, at, levelEventCrafterCraft, int32(dir[0])+3*int32(dir[2])+4)
	r.Broadcast(pos, &packet.LevelSoundEvent{SoundType: packet.SoundEventCrafterCraft, Position: blockVec(pos), ExtraData: -1}, nil)
	for _, stack := range results {
		c.s.eject(r, stack, pos, dir, at)
	}
}

// handleToggleCrafterSlot is BDS's handler for turning a crafter's slot on or off: the
// crafter must be the one this player has open, and a filled slot cannot be turned off.
func handleToggleCrafterSlot(p *Player, pk *packet.PlayerToggleCrafterSlotRequest) {
	if p.entity == nil || !p.window.open || !p.window.chest || !p.abilities.Flag(AbilityOpenContainers) {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	pos := cube.Pos{int(pk.PosX), int(pk.PosY), int(pk.PosZ)}
	c := p.windowChest(r)
	if c == nil || c.pos != pos || c.machine == nil || !c.machine.crafter || pk.Slot >= crafterSize {
		return
	}
	if pk.Disabled && !c.items[pk.Slot].Empty() {
		return
	}
	was := c.slotDisabled(int(pk.Slot))
	if was == pk.Disabled {
		return
	}
	c.disabled ^= 1 << pk.Slot
	c.changed = true
	r.SaveBlockEntity(pos)
}
