package player

import (
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// bellBlock rings when clicked from the right side.
const bellBlock = "minecraft:bell"

// What a player's click did first, as BDS keeps it for the repeats of a held
// click.
const (
	useNothing = iota
	useBlock
	useItem
)

// BlockUse is a player clicking a block.
type BlockUse struct {
	Player *Player
	// Pos and Block are the block clicked, Face its side, ClickPos where on it
	// (0 to 1 on each axis).
	Pos      cube.Pos
	Block    uint32
	Face     int32
	ClickPos mgl32.Vec3
	// Item is what the player holds.
	Item ItemStack
}

// BlockUseEvent fires before a player's click on a block does anything. Cancel
// stops both the block's use and the held item's.
type BlockUseEvent struct {
	Cancellable
	Player *Player
	Pos    cube.Pos
	Block  uint32
}

// UseBlockFunc does what clicking the block does. False if the block did not
// take the click, so the held item is used instead.
type UseBlockFunc func(u *BlockUse) bool

// Usable is a block type's own answer to being clicked (BDS's use). False if it
// did not take the click.
type Usable interface {
	Use(u *BlockUse) bool
}

// vanillaUseBlock is the clicked block's behaviour, else what the blockuse
// package gives it. Without the doors and switches ability a vanilla block still
// takes the click but does nothing, as in BDS.
func vanillaUseBlock(u *BlockUse) bool {
	p := u.Player
	r := p.entity.Region()
	if r == nil {
		return false
	}
	blocks := p.s.reg.Blocks
	if b, ok := p.s.game.blocks[blocks.Name(u.Block)].(Usable); ok {
		return b.Use(u)
	}
	if p.tillWithHoe(r, u) || p.pickBerries(r, u) {
		return true
	}
	if lightLevel(blocks.Name(u.Block)) >= 0 && lightLevel(u.Item.Name) >= 0 {
		return p.useLightBlock(r, u)
	}
	if p.s.chests.kind[u.Block] != chestNone {
		return p.useChest(r, u)
	}
	switch blocks.Name(u.Block) {
	case blockuse.Bed, blockuse.StrawBed:
		return p.useBed(r, u)
	case jukeboxBlock:
		return p.useJukebox(r, u)
	case craftingTable:
		return p.useCraftingTable(r, u)
	case smithingTable:
		return p.useSmithingTable(r, u)
	case enchantingTable:
		return p.useEnchantingTable(r, u)
	case grindstone:
		return p.useGrindstone(r, u)
	case loomBlock:
		return p.useLoom(r, u)
	case anvilBlock, anvilChipped, anvilDamaged, anvilDeprecated:
		return p.useAnvil(r, u)
	}
	if isSignBlock(blocks.Name(u.Block)) {
		return p.useSign(r, u)
	}
	if !p.abilities.Flag(AbilityDoorsAndSwitches) {
		return blockuse.Usable(blocks, u.Block)
	}
	if ring := p.s.circuits.RingBell; ring != nil && blocks.Name(u.Block) == bellBlock {
		return ring(r, u.Pos, u.Block, p.entity.Rotation().Yaw)
	}
	if use := p.s.circuits.Use; use != nil && use(r, u.Pos, u.Block) {
		return true
	}
	return blockuse.Use(blocks, r, u.Pos, u.Block, p.entity.Rotation().Yaw)
}

// lightBlockPrefix starts the names of the sixteen light blocks, light_block_0 to 15.
const lightBlockPrefix = "minecraft:light_block_"

// lightLevel is the level in a light block's name, -1 if name is no light block.
func lightLevel(name string) int {
	n, ok := strings.CutPrefix(name, lightBlockPrefix)
	if !ok {
		return -1
	}
	level, err := strconv.Atoi(n)
	if err != nil || level < 0 || level > 15 {
		return -1
	}
	return level
}

// useLightBlock is BDS 1.26.60's light block click (sub_D557A40): a light block held on a
// light block always takes the click, so nothing is placed, and in creative the block
// becomes the next level, 15 going round to 0 (the chain its registration builds).
func (p *Player) useLightBlock(r *world.Region, u *BlockUse) bool {
	if !p.creative() {
		return true
	}
	next := lightBlockPrefix + strconv.Itoa((lightLevel(p.s.reg.Blocks.Name(u.Block))+1)%16)
	if id, ok := p.s.reg.Blocks.Block(next); ok && r.SetBlock(u.Pos, 0, id) {
		r.UpdateNeighbours(u.Pos)
	}
	return true
}
