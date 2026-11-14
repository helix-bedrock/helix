package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/fall"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Placement is a player placing a block: where it clicked, where the block
// goes and which state it is placed in.
type Placement struct {
	Player *Player
	// Clicked is the block clicked, Face its side, ClickPos where on it
	// (0 to 1 on each axis).
	Clicked  cube.Pos
	Face     int32
	ClickPos mgl32.Vec3
	// Pos is where the block goes: the clicked block if it can be built
	// over, else its neighbour on Face.
	Pos cube.Pos
	// Item is the item placed from, Block the block state to place.
	Item  ItemStack
	Block uint32
}

// BlockPlaceEvent fires before a player's block is placed. Block may be
// changed; Cancel keeps the world as it was and the client is sent it
// back.
type BlockPlaceEvent struct {
	Cancellable
	Player *Player
	Pos    cube.Pos
	Block  uint32
}

// PlaceStateFunc picks the state placed (BDS's getPlacementBlock); false
// places nothing.
type PlaceStateFunc func(pl *Placement) (uint32, bool)

// PlaceCheckFunc decides whether the block may go at pl.Pos (BDS's
// mayPlace).
type PlaceCheckFunc func(pl *Placement) bool

// PlaceFunc places the block: the event, the block set, the sound and the
// item used up. False if nothing was placed.
type PlaceFunc func(pl *Placement) bool

// handleInventoryTransaction handles the legacy drop, item use on a block or in
// the air, using an item on an entity and letting an item go.
func handleInventoryTransaction(p *Player, pk *packet.InventoryTransaction) {
	switch d := pk.TransactionData.(type) {
	case *protocol.NormalTransactionData:
		p.handleNormalTransaction(pk.Actions)
	case *protocol.UseItemOnEntityTransactionData:
		p.handleUseOnEntity(d)
	case *protocol.ReleaseItemTransactionData:
		p.letGo()
	case *protocol.UseItemTransactionData:
		if p.dead() {
			return
		}
		switch d.ActionType {
		case protocol.UseItemActionClickBlock:
			p.handleClickBlock(d)
		case protocol.UseItemActionClickAir:
			p.handleUseAir(d)
		}
	}
}

// handleClickBlock is an item used on a block.
func (p *Player) handleClickBlock(d *protocol.UseItemTransactionData) {
	if !p.selectUsed(d) {
		p.sendHeld()
		p.resendPlace(toCube(d.BlockPosition), d.BlockFace)
		return
	}
	first := d.TriggerType != protocol.TriggerTypeSimulationTick
	if !p.useItemOn(toCube(d.BlockPosition), d.BlockFace, d.ClickedPosition, first) {
		// The client took what it predicted from its hand; the server kept it, so the slot
		// goes back too or the client shows it gone until the inventory is next sent.
		if held := p.inventory.Held(); held.Count > 0 {
			p.sendHeld()
		}
		p.resendPlace(toCube(d.BlockPosition), d.BlockFace)
		// What is thrown or drawn does not need the air: aimed at a block it still goes.
		if held := p.inventory.Held(); first && (throwable(held.Name) != "" || held.Name == nameBow || held.Name == nameCrossbow || held.Name == nameFishingRod) {
			p.s.game.sys.UseItem(p, held)
		}
	}
}

// useItemOn is BDS's GameMode useItemOn: the event, the clicked block's use,
// then the held item. first is false for the repeats sent while the button is
// held, which stay with whichever of the two the first click did, so a held
// click does not flap a door. False if nothing happened.
func (p *Player) useItemOn(clicked cube.Pos, face int32, click mgl32.Vec3, first bool) bool {
	if face < int32(cube.FaceDown) || face > int32(cube.FaceEast) {
		return false
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	if first {
		p.lastUse = useNothing
	}
	target, ok := r.Block(clicked, 0)
	if !ok || !p.inReach(clicked) {
		return false
	}
	g, held := p.s.game, p.inventory.Held()
	if g.useEv.Has() {
		e := BlockUseEvent{Player: p, Pos: clicked, Block: target}
		g.useEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	// A player sneaking with something in hand uses the item, not the block, except a hoe:
	// BDS's GameMode::useItemOn (0xCA46800) lets an item tagged minecraft:is_hoe use the
	// block while sneaking, so it tills.
	if (held.Empty() || !p.Moving(MoveSneaking) || p.holdsHoe(held)) && p.lastUse != useItem {
		u := &BlockUse{Player: p, Pos: clicked, Block: target, Face: face, ClickPos: click, Item: held}
		if g.sys.UseBlock(u) {
			if p.lastUse == useNothing {
				p.lastUse = useBlock
			}
			return true
		}
	}
	if p.lastUse == useBlock {
		return true
	}
	if held.Empty() {
		return false
	}
	if !p.useHeldOn(r, clicked, face, click, held) {
		return false
	}
	if p.lastUse == useNothing {
		p.lastUse = useItem
	}
	return true
}

// useHeldOn is the held item's part of useItemOn: an item with a use of its own on
// blocks, else a block item's target, check, state and placing. False if nothing
// was placed.
func (p *Player) useHeldOn(r *world.Region, clicked cube.Pos, face int32, click mgl32.Vec3, held ItemStack) bool {
	if used, ok := p.useSpecialOn(r, clicked, face, click, held); ok {
		return used
	}
	return p.placeHeld(r, clicked, face, click, held)
}

// useSpecialOn runs the use on a block of an item that is not simply placed: fire,
// buckets, spawn eggs, entities placed as items, signs, beds and the like. ok is false
// if held has no such use; used is whether it did something.
func (p *Player) useSpecialOn(r *world.Region, clicked cube.Pos, face int32, click mgl32.Vec3, held ItemStack) (used, ok bool) {
	if held.Name == nameFlintSteel {
		return p.lightFire(r, clicked, face, held), true
	}
	if held.Name == nameFireworkRocket {
		return p.useFireworkOn(r, clicked, click, held), true
	}
	if held.Name == brushItem {
		return p.useBrushOn(r, clicked, face), true
	}
	if held.Name == nameShears {
		return p.useShearsOn(r, clicked, face), true
	}
	if spawnEggMob(held.Name) != "" {
		return p.useSpawnEgg(r, clicked, face, held), true
	}
	if held.Name == nameEnderEye {
		return p.useEnderEye(r, clicked), true
	}
	if held.Name == nameBoneMeal {
		return p.useBoneMeal(r, clicked, click, held), true
	}
	if _, ok := bucketLiquids[held.Name]; ok {
		return p.pourBucket(clicked, face, held), true
	}
	if held.Name == emptyBucket {
		return p.fillBucket(clicked), true
	}
	if b, ok := mobBucketItem(held.Name); ok {
		return p.pourMobBucket(clicked, face, held, b), true
	}
	if held.Name == namePowderSnowBucket {
		return p.pourPowderSnow(clicked, face), true
	}
	if held.Name == nameEndCrystalItem {
		return p.placeEndCrystal(r, clicked), true
	}
	if held.Name == paintingID {
		return p.placePainting(r, clicked, face), true
	}
	if held.Name == armorStandID {
		return p.placeArmorStand(r, clicked, face), true
	}
	if isCushionItem(held.Name) {
		return p.placeCushion(r, clicked, face, click, held), true
	}
	if isBedItem(held.Name) {
		return p.placeBed(r, clicked, face, held), true
	}
	if held.Name == bannerItem {
		return p.placeBanner(r, clicked, face, held), true
	}
	if isHangingSignName(held.Name) {
		return p.placeHangingSign(r, clicked, face, held), true
	}
	if isSignItem(held.Name) {
		return p.placeSign(r, clicked, face, held), true
	}
	if IsCartItem(held.Name) {
		return p.placeCart(r, clicked, held.Name), true
	}
	if IsBoatItem(held.Name) {
		return p.placeBoat(r, clicked, face, click, held), true
	}
	if held.Name == scaffoldingItem {
		return p.placeScaffolding(r, clicked, face, click, held), true
	}
	return false, false
}

// placeHeld places the block item held: the target (the clicked block if it can be built
// over, else the side clicked), the check and the state. False if nothing was placed.
func (p *Player) placeHeld(r *world.Region, clicked cube.Pos, face int32, click mgl32.Vec3, held ItemStack) bool {
	block, ok := p.itemBlock(held)
	if !ok {
		return false
	}
	pl := &Placement{Player: p, Clicked: clicked, Face: face, ClickPos: click, Pos: clicked.Side(cube.Face(face)), Item: held, Block: block}
	if b, ok := r.Block(clicked, 0); ok && p.s.game.replaceable(p, r, clicked, b, block) {
		pl.Pos = clicked
	}
	sys := p.s.game.sys
	if !sys.PlaceCheck(pl) {
		return false
	}
	state, ok := sys.PlaceState(pl)
	if !ok {
		return false
	}
	pl.Block = state
	return sys.Place(pl)
}

// plantedBlocks is the block of each item named differently from what it places.
// Signs are not here: their block depends on the face clicked and their block
// entities are not built. Banners place in banner.go.
var plantedBlocks = map[string]string{
	"minecraft:redstone":          "minecraft:redstone_wire",
	"minecraft:repeater":          "minecraft:unpowered_repeater",
	"minecraft:comparator":        "minecraft:unpowered_comparator",
	"minecraft:string":            "minecraft:trip_wire",
	"minecraft:sugar_cane":        "minecraft:reeds",
	"minecraft:wheat_seeds":       "minecraft:wheat",
	"minecraft:beetroot_seeds":    "minecraft:beetroot",
	"minecraft:carrot":            "minecraft:carrots",
	"minecraft:potato":            "minecraft:potatoes",
	"minecraft:melon_seeds":       "minecraft:melon_stem",
	"minecraft:pumpkin_seeds":     "minecraft:pumpkin_stem",
	"minecraft:torchflower_seeds": "minecraft:torchflower_crop",
	"minecraft:pitcher_pod":       "minecraft:pitcher_crop",
	"minecraft:sweet_berries":     "minecraft:sweet_berry_bush",
	"minecraft:glow_berries":      "minecraft:cave_vines",
	"minecraft:cocoa_beans":       "minecraft:cocoa",
}

// itemBlock returns the block a block item places: its saved block state, else
// the default state of the block it plants or of the block with its name.
func (p *Player) itemBlock(s ItemStack) (uint32, bool) {
	blocks := p.s.reg.Blocks
	if s.Block != nil {
		name, _ := s.Block["name"].(string)
		states, _ := s.Block["states"].(map[string]any)
		if id, ok := blocks.StateID(name, states); ok {
			return id, true
		}
	}
	if name, ok := plantedBlocks[s.Name]; ok {
		return blocks.Block(name)
	}
	return blocks.Block(s.Name)
}

// replaceable reports whether placing block on b, at pos, replaces b: what b's
// behaviour says, else BDS's replaceable component (helix-data's list),
// except where BDS's own rule refuses the block placed (light blocks are
// not replaced by light blocks, full snow layers are not built over).
func (g *gameplay) replaceable(p *Player, r *world.Region, pos cube.Pos, b, block uint32) bool {
	blocks := p.s.reg.Blocks
	name := blocks.Name(b)
	if rb, ok := g.blocks[name].(Replaceable); ok {
		return rb.Replaceable(b)
	}
	if flowerBeds[name] {
		return flowerBedBuiltOver(blocks, b, block)
	}
	if isCandleBlock(name) {
		return candleBuiltOver(blocks, b, block)
	}
	if !g.replaceables[name] || g.notReplacedBy[name][blocks.Name(block)] {
		return false
	}
	if name == nameSnowLayer {
		return g.snowReplaceable(p, r, pos, b, block)
	}
	return true
}

// Snow layer states read by snowReplaceable.
const (
	nameSnowLayer    = "minecraft:snow_layer"
	nameAir          = "minecraft:air"
	stateSnowHeight  = "height"
	snowHeightFilled = 7
)

// snowReplaceable is BDS 1.26.60's TopSnowBlock::canBeBuiltOver (sub_D4EB2B0): unless a snow
// layer is placed, a block in the snow's second layer (a plant it covers) decides; otherwise
// only snow below its full height (8 layers, which looks like a snow block) is built over.
func (g *gameplay) snowReplaceable(p *Player, r *world.Region, pos cube.Pos, b, block uint32) bool {
	blocks := p.s.reg.Blocks
	if blocks.Name(block) != nameSnowLayer && r != nil {
		if covered, ok := r.Block(pos, 1); ok && blocks.Name(covered) != nameAir {
			return g.replaceable(p, r, pos, covered, block)
		}
	}
	h, _ := blocks.States(b)[stateSnowHeight].(int32)
	return h < snowHeightFilled
}

// vanillaPlaceCheck is BDS's mayPlace as far as built: the target is in the
// dimension and loaded, can be built over, is in reach, and the placing
// player is not in the way.
func vanillaPlaceCheck(pl *Placement) bool {
	p := pl.Player
	r := p.entity.Region()
	if r == nil || !p.inReach(pl.Clicked) {
		return false
	}
	b, ok := r.Block(pl.Pos, 0)
	if !ok || !p.s.game.replaceable(p, r, pl.Pos, b, pl.Block) {
		return false
	}
	blocks := p.s.reg.Blocks
	if blockuse.IsDoor(blocks, pl.Block) && !blockuse.DoorFits(blocks, r, pl.Pos, func(above uint32) bool {
		return p.s.game.replaceable(p, r, pl.Pos.Side(cube.FaceUp), above, pl.Block)
	}) {
		return false
	}
	if !p.s.attach.MayPlace(r, pl.Pos, pl.Block, cube.Face(pl.Face)) {
		return false
	}
	if f := p.s.farm; f != nil && !f.MayPlace(r, pl.Pos, pl.Block) {
		return false
	}
	// BDS tests the state it would place, not the item's: only a block with a
	// collision box can be in the player's way, so a torch goes where it stands.
	state, ok := p.s.game.sys.PlaceState(pl)
	return ok && !p.inside(pl.Pos, state) && !p.s.obstructed(r, pl.Pos, state)
}

// BuildBlocker is an entity owner that keeps blocks from being placed where it is: BDS's
// actor flag that mayPlace (sub_CE5DEE0) reads (Actor+620), set by falling blocks.
type BuildBlocker interface {
	// BuildBox is its box, false if it does not block now.
	BuildBox() (lo, hi mgl32.Vec3, ok bool)
}

// obstructed reports whether an entity that blocks building is inside a collision box block
// would have at pos, as BDS's mayPlace checks before it places. Runs: owner of the region.
func (s *Host) obstructed(r *world.Region, pos cube.Pos, block uint32) bool {
	boxes := s.reg.Blocks.Properties(block).Collision
	if len(boxes) == 0 {
		return false
	}
	bx, by, bz := float32(pos[0]), float32(pos[1]), float32(pos[2])
	for _, e := range r.AppendEntitiesIn(nil, bx-1, bz-1, bx+2, bz+2) {
		bb, ok := e.Owner().(BuildBlocker)
		if !ok {
			continue
		}
		lo, hi, ok := bb.BuildBox()
		if !ok {
			continue
		}
		for _, b := range boxes {
			if hi[0] > bx+b[0] && lo[0] < bx+b[3] && hi[1] > by+b[1] && lo[1] < by+b[4] && hi[2] > bz+b[2] && lo[2] < bz+b[5] {
				return true
			}
		}
	}
	return false
}

// Player hitbox, from its eye position: 0.6 wide, 1.8 tall, eyes at 1.62.
const (
	playerHalfWidth = 0.3
	playerEyeHeight = 1.62
	playerHeight    = 1.8
)

// inside reports whether the player's box overlaps the collision boxes block
// would have at pos.
func (p *Player) inside(pos cube.Pos, block uint32) bool {
	e := p.entity.Position()
	minX, maxX := e[0]-playerHalfWidth, e[0]+playerHalfWidth
	minY := e[1] - playerEyeHeight
	maxY := minY + playerHeight
	minZ, maxZ := e[2]-playerHalfWidth, e[2]+playerHalfWidth
	bx, by, bz := float32(pos[0]), float32(pos[1]), float32(pos[2])
	for _, b := range p.s.reg.Blocks.Properties(block).Collision {
		if maxX > bx+b[0] && minX < bx+b[3] && maxY > by+b[1] && minY < by+b[4] && maxZ > bz+b[2] && minZ < bz+b[5] {
			return true
		}
	}
	return false
}

// vanillaPlace is BDS's BlockItem placing after the checks: the BlockPlace
// event, the block set, the place sound (LevelSoundEvent "place" with the
// block, at its centre, to everyone near, the placer too), and one item used
// up outside creative.
func vanillaPlace(pl *Placement) bool {
	p := pl.Player
	r := p.entity.Region()
	if r == nil {
		return false
	}
	pl.Block = p.s.connections.Connected(r, pl.Pos, pl.Block)
	g := p.s.game
	if g.placeEv.Has() {
		e := BlockPlaceEvent{Player: p, Pos: pl.Pos, Block: pl.Block}
		g.placeEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
		pl.Block = e.Block
	}
	blocks := p.s.reg.Blocks
	if f := p.s.falling; f != nil && f.Kind(pl.Block) == fall.Snow {
		// TopSnowBlock::tryToPlace (slot 70, 0xD4EA610), the same one landing snow runs:
		// layers add to snow already there up to a full block, the rest goes on top.
		if !f.Land(r, pl.Pos, pl.Block) {
			return false
		}
	} else if blockuse.IsDoor(blocks, pl.Block) {
		if !blockuse.PlaceDoor(blocks, r, pl.Pos, pl.Block) {
			return false
		}
	} else {
		if !r.SetBlock(pl.Pos, 0, pl.Block) {
			return false
		}
		if c := p.s.chestAt(r, pl.Pos); c != nil && c.shulker {
			c.placedFrom(r, pl)
		}
		if dp, ok := DecoratedPotAt(r, pl.Pos); ok {
			dp.placedFrom(pl)
		}
		r.UpdateNeighbours(pl.Pos)
	}
	playSound(r, pl.Pos, p.s.blockSound(packet.SoundEventPlace, blockCenter(pl.Pos), pl.Block), nil)
	p.s.postBlockGameEvent(r, gameEventBlockPlace, pl.Pos, p, pl.Block, true)
	if !p.creative() {
		p.useHeld()
	}
	return true
}

// useHeld takes one item from the held stack and tells the client.
func (p *Player) useHeld() {
	inv := &p.inventory
	s := &inv.main[inv.held]
	if s.Count > 0 {
		s.Count--
	}
	if s.Count == 0 {
		*s = ItemStack{}
	}
	p.sendHeld()
	p.sendEquipment()
	p.flush()
}

// resendPlace sends the clicked block and its neighbour back to the player
// after a placing that did not happen, so its client drops what it
// predicted.
func (p *Player) resendPlace(clicked cube.Pos, face int32) {
	r := p.entity.Region()
	if r == nil {
		return
	}
	for _, at := range [...]cube.Pos{clicked, clicked.Side(cube.Face(face))} {
		p.sendBlock(r, at)
	}
	p.flush()
}

// selectUsed applies item use's slot, as BDS selects the slot the client
// names, and checks the client thinks it holds the item the server has there.
// What is used is the server's stack either way; a client that names another
// item gets its slot back.
func (p *Player) selectUsed(d *protocol.UseItemTransactionData) bool {
	return p.selectSlot(d.HotBarSlot, d.HeldItem.Stack.NetworkID)
}

// selectSlot applies the slot a client names and compares the item it thinks it
// holds with the server's.
func (p *Player) selectSlot(slot, networkID int32) bool {
	if slot < 0 || slot >= hotbarSize {
		return false
	}
	p.inventory.held = uint8(slot) //nolint:gosec // checked above
	p.sendEquipment()
	held := p.inventory.Held()
	if held.Empty() {
		return networkID == 0
	}
	t, ok := p.s.reg.Items.ByName(held.Name)
	return ok && int32(t.ID) == networkID
}
