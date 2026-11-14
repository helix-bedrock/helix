package player

import (
	"maps"

	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Flower pots as BDS 1.26.60: FlowerPotBlockActor (type 7, saved as "FlowerPot") with its
// save 0xD40E530 (the plant as a block state compound, "PlantBlock") and load 0xD40E700.
// The block's use, place, take, removal and creative break follow FlowerPotBlock's
// use/_tryPlaceFlower/_tryTakeFlower/onRemove/playerWillDestroy, read in the 1.21.3 names
// build; onChanged sets update_bit while a plant is in.
//
// Not built: the legacy "item"/"mData" save keys (worlds from before PlantBlock).
const (
	flowerPotBlock    = "minecraft:flower_pot"
	flowerPotEntityID = "FlowerPot"
	flowerPotPlantTag = "PlantBlock"
	stateUpdateBit    = "update_bit"
)

// pottablePlants are the blocks BDS 1.26.60 gives the flower_pottable component: the 39
// BlockFlowerPottableDescription creations in its vanilla block registration (0x5BC7DE0),
// named by the FNV-1 hash of the block name each one follows. Seven (dead bush, red
// mushroom, cactus, bamboo, wither rose, torchflower, oak sapling) follow no hashed name and
// are placed by their spot in the registration order.
var pottablePlants = map[string]bool{
	"minecraft:dead_bush": true, "minecraft:dandelion": true, "minecraft:brown_mushroom": true,
	"minecraft:red_mushroom": true, "minecraft:cactus": true, "minecraft:bamboo": true,
	"minecraft:wither_rose": true, "minecraft:crimson_roots": true, "minecraft:warped_roots": true,
	"minecraft:crimson_fungus": true, "minecraft:warped_fungus": true, "minecraft:azalea": true,
	"minecraft:flowering_azalea": true, "minecraft:mangrove_propagule": true,
	"minecraft:cherry_sapling": true, "minecraft:torchflower": true, "minecraft:oak_sapling": true,
	"minecraft:spruce_sapling": true, "minecraft:birch_sapling": true, "minecraft:jungle_sapling": true,
	"minecraft:acacia_sapling": true, "minecraft:dark_oak_sapling": true, "minecraft:poppy": true,
	"minecraft:blue_orchid": true, "minecraft:allium": true, "minecraft:azure_bluet": true,
	"minecraft:red_tulip": true, "minecraft:orange_tulip": true, "minecraft:white_tulip": true,
	"minecraft:pink_tulip": true, "minecraft:oxeye_daisy": true, "minecraft:cornflower": true,
	"minecraft:lily_of_the_valley": true, "minecraft:fern": true, "minecraft:pale_oak_sapling": true,
	"minecraft:open_eyeblossom": true, "minecraft:closed_eyeblossom": true,
	"minecraft:golden_dandelion": true, "minecraft:poplar_sapling": true,
}

// FlowerPot is a flower pot's block entity: the plant in it, a block state, or 0 with
// has false. Get it with [FlowerPotAt]. Runs: owner of its chunk.
type FlowerPot struct {
	s     *Host
	saved map[string]any
	plant uint32
	has   bool
	chunk world.ChunkHandle
}

// FlowerPotAt returns the flower pot block entity at pos. Runs: owner of the chunk.
func FlowerPotAt(r *world.Region, pos cube.Pos) (*FlowerPot, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	fp, ok := be.(*FlowerPot)
	return fp, ok
}

// Plant returns the block state in the pot; false when it is empty.
func (fp *FlowerPot) Plant() (uint32, bool) { return fp.plant, fp.has }

// SetPlant puts a block state in the pot, or empties it with has false: BDS's setPlantItem
// and onChanged, which sets the pot's update_bit to whether a plant is in. Clients are told.
func (fp *FlowerPot) SetPlant(r *world.Region, pos cube.Pos, plant uint32, has bool) {
	fp.plant, fp.has = plant, has
	if !has {
		fp.plant = 0
	}
	fp.chunk.Changed()
	blocks := fp.s.reg.Blocks
	if b, ok := r.Block(pos, 0); ok && blocks.Name(b) == flowerPotBlock {
		states := maps.Clone(blocks.States(b))
		var bit uint8
		if has {
			bit = 1
		}
		states[stateUpdateBit] = bit
		if next, ok := blocks.StateID(flowerPotBlock, states); ok && next != b {
			r.SetBlock(pos, 0, next)
		}
	}
	r.SendBlockEntity(pos)
}

// BindBlockEntity keeps the chunk the pot is in.
func (fp *FlowerPot) BindBlockEntity(h world.ChunkHandle) { fp.chunk = h }

// KeepBlockEntity keeps the plant when only the pot's update_bit changes.
func (fp *FlowerPot) KeepBlockEntity(block uint32) bool {
	return fp.s.reg.Blocks.Name(block) == flowerPotBlock
}

// loadFlowerPot brings back a saved pot: PlantBlock is the plant's block state.
func (s *Host) loadFlowerPot(saved map[string]any) *FlowerPot {
	fp := &FlowerPot{s: s, saved: maps.Clone(saved)}
	if m, ok := saved[flowerPotPlantTag].(map[string]any); ok {
		name, _ := m["name"].(string)
		states, _ := m["states"].(map[string]any)
		if id, ok := s.reg.Blocks.StateID(name, states); ok && id != s.reg.Blocks.Air() {
			fp.plant, fp.has = id, true
		}
	}
	delete(fp.saved, flowerPotPlantTag)
	return fp
}

// SaveData is what BDS saves of a pot: the plant's block state, only when one is in.
func (fp *FlowerPot) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(fp.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = flowerPotEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	if fp.has {
		blocks := fp.s.reg.Blocks
		m[flowerPotPlantTag] = map[string]any{"name": blocks.Name(fp.plant), "states": blocks.States(fp.plant), "version": blocks.Version(fp.plant)}
	}
	return m
}

// NetworkData is the same: the client draws the plant from it.
func (fp *FlowerPot) NetworkData(pos cube.Pos) map[string]any { return fp.SaveData(pos) }

// BlockEntityRemoved is BDS's onRemove: the plant drops as its item.
func (fp *FlowerPot) BlockEntityRemoved(r *world.Region, pos cube.Pos) {
	if !fp.has {
		return
	}
	if it, ok := fp.s.plantItem(fp.plant); ok {
		fp.s.spillSlots(r, []ItemStack{it}, blockVec(pos), true, chestPickupDelay)
	}
	fp.has, fp.plant = false, 0
}

// plantItem is the item of a potted plant's block.
func (s *Host) plantItem(plant uint32) (ItemStack, bool) {
	name := s.reg.Blocks.Name(plant)
	return s.dropStack(plant, loot.Stack{Name: name, Count: 1})
}

// heldPlant is the pottable block a held item places, as BDS reads the item's block.
func (p *Player) heldPlant(held ItemStack) (uint32, bool) {
	if held.Empty() {
		return 0, false
	}
	blocks := p.s.reg.Blocks
	if held.Block != nil {
		name, _ := held.Block["name"].(string)
		states, _ := held.Block["states"].(map[string]any)
		if id, ok := blocks.StateID(name, states); ok && pottablePlants[name] {
			return id, true
		}
	}
	if !pottablePlants[held.Name] {
		return 0, false
	}
	return blocks.Block(held.Name)
}

// useFlowerPot is FlowerPotBlock::use: put the held plant in, or else take the plant out.
func (p *Player) useFlowerPot(r *world.Region, u *BlockUse) bool {
	fp, ok := FlowerPotAt(r, u.Pos)
	if !ok {
		return false
	}
	return p.potPlant(r, u.Pos, fp) || p.potTake(r, u.Pos, fp)
}

// potPlant is _tryPlaceFlower: a pottable plant goes into an empty pot, one used up unless
// the player builds instantly.
func (p *Player) potPlant(r *world.Region, pos cube.Pos, fp *FlowerPot) bool {
	plant, ok := p.heldPlant(p.inventory.Held())
	if !ok {
		return false
	}
	if cur, has := fp.Plant(); has && pottablePlants[p.s.reg.Blocks.Name(cur)] {
		return false
	}
	fp.SetPlant(r, pos, plant, true)
	p.s.postBlockGameEvent(r, gameEventBlockChange, pos, p, 0, false)
	if !p.abilities.Flag(AbilityInstantBuild) {
		p.useHeld()
	}
	return true
}

// potTake is _tryTakeFlower: with no pottable plant in hand, the plant in the pot goes to the
// inventory, or drops where there is no room.
func (p *Player) potTake(r *world.Region, pos cube.Pos, fp *FlowerPot) bool {
	if _, holding := p.heldPlant(p.inventory.Held()); holding {
		return false
	}
	plant, has := fp.Plant()
	if !has {
		return false
	}
	if it, ok := p.s.plantItem(plant); ok {
		if rest := p.Give(p.s.freshStack(it)); !rest.Empty() {
			p.Drop(rest)
		}
	}
	fp.SetPlant(r, pos, 0, false)
	p.s.postBlockGameEvent(r, gameEventBlockChange, pos, p, 0, false)
	p.flush()
	return true
}

// potWillDestroy is what pots do as a player breaks them (BDS's playerWillDestroy): a player
// who builds instantly empties a flower pot first, so the plant does not drop; a decorated
// pot plays its break or shatter sound.
func (p *Player) potWillDestroy(r *world.Region, pos cube.Pos, block uint32) {
	switch p.s.reg.Blocks.Name(block) {
	case flowerPotBlock:
		if fp, ok := FlowerPotAt(r, pos); ok && p.abilities.Flag(AbilityInstantBuild) {
			fp.has, fp.plant = false, 0
		}
	case decoratedPotBlock:
		p.decoratedPotBreakSound(r, pos, block)
	}
}
