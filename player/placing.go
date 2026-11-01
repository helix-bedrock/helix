package player

import (
	"github.com/go-gl/mathgl/mgl32"
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

// handleInventoryTransaction handles item use on a block. Only placing is
// built: clicking air, using items on entities and interacting with
// blocks come later.
func handleInventoryTransaction(p *Player, pk *packet.InventoryTransaction) {
	d, ok := pk.TransactionData.(*protocol.UseItemTransactionData)
	if !ok || d.ActionType != protocol.UseItemActionClickBlock {
		return
	}
	if !p.useItemOn(toCube(d.BlockPosition), d.BlockFace, d.ClickedPosition) {
		p.resendPlace(toCube(d.BlockPosition), d.BlockFace)
	}
}

// useItemOn is BDS's GameMode useItemOn for a block item: the target, the
// check, the state, then the placing. False if nothing was placed.
func (p *Player) useItemOn(clicked cube.Pos, face int32, click mgl32.Vec3) bool {
	if face < int32(cube.FaceDown) || face > int32(cube.FaceEast) {
		return false
	}
	r := p.entity.Region()
	held := p.inventory.Held()
	if r == nil || held.Empty() {
		return false
	}
	block, ok := p.itemBlock(held)
	if !ok {
		return false
	}
	pl := &Placement{Player: p, Clicked: clicked, Face: face, ClickPos: click, Pos: clicked.Side(cube.Face(face)), Item: held, Block: block}
	if b, ok := r.Block(clicked, 0); ok && p.s.game.replaceable(p, b, block) {
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

// itemBlock returns the block a block item places: its saved block state,
// else the default state of the block with the item's name.
func (p *Player) itemBlock(s ItemStack) (uint32, bool) {
	blocks := p.s.reg.Blocks
	if s.Block != nil {
		name, _ := s.Block["name"].(string)
		states, _ := s.Block["states"].(map[string]any)
		if id, ok := blocks.StateID(name, states); ok {
			return id, true
		}
	}
	return blocks.Block(s.Name)
}

// replaceable reports whether placing block on b replaces b: what b's
// behaviour says, else BDS's replaceable component (helix-data's list),
// except where BDS's own rule refuses the block placed (light blocks are
// not replaced by light blocks).
func (g *gameplay) replaceable(p *Player, b, block uint32) bool {
	name := p.s.reg.Blocks.Name(b)
	if r, ok := g.blocks[name].(Replaceable); ok {
		return r.Replaceable(b)
	}
	return g.replaceables[name] && !g.notReplacedBy[name][p.s.reg.Blocks.Name(block)]
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
	if !ok || !p.s.game.replaceable(p, b, pl.Block) {
		return false
	}
	return !p.inside(pl.Pos)
}

// Player hitbox, from its eye position: 0.6 wide, 1.8 tall, eyes at 1.62.
const (
	playerHalfWidth = 0.3
	playerEyeHeight = 1.62
	playerHeight    = 1.8
)

// inside reports whether the player's box overlaps the block at pos.
func (p *Player) inside(pos cube.Pos) bool {
	e := p.entity.Position()
	minX, maxX := e[0]-playerHalfWidth, e[0]+playerHalfWidth
	minY := e[1] - playerEyeHeight
	maxY := minY + playerHeight
	minZ, maxZ := e[2]-playerHalfWidth, e[2]+playerHalfWidth
	bx, by, bz := float32(pos[0]), float32(pos[1]), float32(pos[2])
	return maxX > bx && minX < bx+1 && maxY > by && minY < by+1 && maxZ > bz && minZ < bz+1
}

// vanillaPlaceState is BDS's getPlacementBlock: the block's own rule, else
// the item's block as it is.
func vanillaPlaceState(pl *Placement) (uint32, bool) {
	p := pl.Player
	if s, ok := p.s.game.blocks[p.s.reg.Blocks.Name(pl.Block)].(PlacementStater); ok {
		return s.PlacementState(pl)
	}
	return pl.Block, true
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
	g := p.s.game
	if g.placeEv.Has() {
		e := BlockPlaceEvent{Player: p, Pos: pl.Pos, Block: pl.Block}
		g.placeEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
		pl.Block = e.Block
	}
	if !r.SetBlock(pl.Pos, 0, pl.Block) {
		return false
	}
	r.Broadcast(pl.Pos, &packet.LevelSoundEvent{
		SoundType: packet.SoundEventPlace, Position: blockVec(pl.Pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}),
		ExtraData: int32(p.s.reg.Blocks.Hash(pl.Block)), //nolint:gosec // hashes are sent as int32
	}, nil)
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
