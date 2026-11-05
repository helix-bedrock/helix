package player

import (
	"maps"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// BDS's numbers for items a broken block throws out, and the enchantments its drop
// rules read from the tool.
const (
	dropInset        = 0.15
	dropSpan         = 0.7
	blockDropDelay   = 10
	enchantSilkTouch = int16(16)
	enchantFortune   = int16(18)
	// Not read from BDS: where its spawner sets an item's speed was not found.
	// Refine against BDS.
	dropDriftSpan = 0.2
	dropUp        = 0.2
)

// harvests reports whether the item in b is the right tool, or none is needed. Only
// the tool speed hook works this out, so it runs here.
func (g *gameplay) harvests(b *BreakInfo) bool {
	g.sys.ToolSpeed(b)
	return b.Harvest
}

// BlockDrop is one item a broken block is about to throw out, and how.
type BlockDrop struct {
	Stack ItemStack
	// Pos is where it appears, Velocity how it moves.
	Pos, Velocity mgl32.Vec3
	// PickupDelay is how many ticks pass before it can be picked up.
	PickupDelay int32
}

// NewBlockDrop returns a drop thrown from the block at pos as BDS throws them: a
// random spot inside the block, with its usual delay.
func NewBlockDrop(pos cube.Pos, s ItemStack) BlockDrop {
	return BlockDrop{
		Stack: s, Pos: blockVec(pos).Add(mgl32.Vec3{dropOffset(), dropOffset(), dropOffset()}),
		Velocity: mgl32.Vec3{dropDrift(), dropUp, dropDrift()}, PickupDelay: blockDropDelay,
	}
}

// BlockDropsEvent is a block a player is breaking, with what it is about to drop. The
// block is still there, with its block entity. It fires for every break, so listeners
// can make a block drop where it would not: the wrong tool or creative give an empty
// Drops. Change, add or remove drops as you like; to add one, use NewBlockDrop.
// Cancelling drops nothing, and a stack given to the player with Give need only be
// removed from Drops.
type BlockDropsEvent struct {
	Cancellable
	Player *Player
	Region *world.Region
	Pos    cube.Pos
	// Block is the state being broken.
	Block uint32
	// Item is the held item after this break wore it, empty if that broke it.
	Item ItemStack
	// Harvest says vanilla's rule let the block drop: not creative, and the right
	// tool or none needed.
	Harvest bool
	Drops   []BlockDrop
	// XP is the experience the block gives, as orbs from the block's corner.
	XP int32
}

// OnBlockDrops listens to what blocks broken by players are about to drop.
func OnBlockDrops(s *Setup, fn func(e *BlockDropsEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.dropsEv.add(s.owner, p, fn)
	return nil
}

// SetBlockDrops replaces what a block drops, by its name; a nil rule drops nothing.
func (s *Setup) SetBlockDrops(block string, rule loot.Rule) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.dropRules[block] = rule
	return nil
}

// DropsFunc returns the items a block drops and the experience it gives, given what
// broke it. Replace it in Systems to change every block's drops. Runs: on the region
// tick.
type DropsFunc func(s *Host, block uint32, c *loot.Context) ([]ItemStack, int32)

// blockDrops is what a player's break of the block at pos throws out: the Drops
// system if vanilla's rule lets it drop, then the event. BDS reads the item after
// wearing it, so a tool that just broke no longer harvests.
func (p *Player) blockDrops(r *world.Region, pos cube.Pos, block uint32) ([]BlockDrop, int32) {
	g := p.s.game
	var drops []BlockDrop
	var xp int32
	harvest := false
	if !p.creative() {
		if worn, ok := p.breakInfo(r, pos); ok && g.harvests(&worn) {
			harvest = true
			var items []ItemStack
			items, xp = g.sys.Drops(p.s, block, p.breakContext(r, pos))
			items = p.s.shulkerDrops(r, pos, items)
			drops = make([]BlockDrop, 0, len(items))
			for _, it := range items {
				drops = append(drops, NewBlockDrop(pos, it))
			}
		}
	}
	// A creative player's break leaves nothing, but BDS still drops a box with items in it
	// (0xD525270).
	if c := p.s.chestAt(r, pos); p.creative() && c != nil && c.shulker && !c.emptySlots(chestSize) {
		items, _ := g.sys.Drops(p.s, block, p.breakContext(r, pos))
		for _, it := range p.s.shulkerDrops(r, pos, items) {
			drops = append(drops, NewBlockDrop(pos, it))
		}
	}
	if g.dropsEv.Has() {
		e := BlockDropsEvent{Player: p, Region: r, Pos: pos, Block: block, Item: p.inventory.Held(), Harvest: harvest, Drops: drops, XP: xp}
		g.dropsEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return nil, 0
		}
		return e.Drops, e.XP
	}
	return drops, xp
}

// vanillaDrops is BDS's getResourceDrops, through its drop rule.
func vanillaDrops(s *Host, block uint32, c *loot.Context) ([]ItemStack, int32) {
	c.State = func(name string) (int32, bool) { return blockStateNumber(s.reg.Blocks.States(block), name) }
	res := s.drops.Roll(s.reg.Blocks.Name(block), c)
	items := make([]ItemStack, 0, len(res.Stacks))
	for _, d := range res.Stacks {
		if it, ok := s.dropStack(block, d); ok {
			items = append(items, it)
		}
	}
	return items, res.XP
}

// blockStateNumber reads a state value as a number, as BDS stores all of them.
func blockStateNumber(states map[string]any, name string) (int32, bool) {
	switch v := states[name].(type) {
	case int32:
		return v, true
	case uint8:
		return int32(v), true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	case string:
		// Seagrass's type is the only text state a rule reads, and only
		// against BDS's first value, "default".
		if v == "default" {
			return 0, true
		}
		return 1, true
	}
	return 0, false
}

// dropStack turns a dropped stack into an item. A block item carries its block's
// state, and an item the registry does not know does not drop.
func (s *Host) dropStack(block uint32, d loot.Stack) (ItemStack, bool) {
	if _, ok := s.reg.Items.ByName(d.Name); !ok {
		return ItemStack{}, false
	}
	blocks := s.reg.Blocks
	it := ItemStack{Name: d.Name, Count: uint8(min(d.Count, 255))} //nolint:gosec // bounded just here
	if id, ok := blocks.Block(d.Name); ok {
		if len(d.KeepStates) > 0 {
			states := maps.Clone(blocks.States(id))
			for _, k := range d.KeepStates {
				if v, ok := blocks.States(block)[k]; ok {
					states[k] = v
				}
			}
			if kept, ok := blocks.StateID(d.Name, states); ok {
				id = kept
			}
		}
		it.Block = map[string]any{"name": blocks.Name(id), "states": blocks.States(id), "version": blocks.Version(id)}
	}
	if d.CustomName != "" {
		it.Tag = map[string]any{"display": map[string]any{"Name": d.CustomName}}
	}
	return it, true
}

// breakContext is the drop context of a player's break: the held item's Silk Touch,
// Fortune, name and tags, and the name of the block entity at pos.
func (p *Player) breakContext(r *world.Region, pos cube.Pos) *loot.Context {
	held := p.inventory.Held()
	c := &loot.Context{Tool: held.Name, CustomName: customNameAt(r, pos)}
	enchantLevels(held, func(id, level int16) {
		e, ok := p.s.game.enchant(id)
		if !ok {
			return
		}
		if e.SilkTouch {
			c.Silk = level > 0
		}
		if e.Fortune {
			c.Fortune = int32(level)
		}
	})
	if t, ok := p.s.reg.Items.ByName(held.Name); ok {
		c.ToolTags = t.Tags
	}
	return c
}

// customNameAt returns the saved name of the block entity at pos.
func customNameAt(r *world.Region, pos cube.Pos) string {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return ""
	}
	name, _ := be.SaveData(pos)["CustomName"].(string)
	return name
}

// FallBlock breaks a block that lost what held it: BDS's destroyBlock with drops,
// through the Drops system. No player broke it, so no break or drops event fires.
// Runs: on the region tick.
func (s *Host) FallBlock(r *world.Region, pos cube.Pos, block uint32) {
	items, _ := s.game.sys.Drops(s, block, &loot.Context{})
	items = s.shulkerDrops(r, pos, items)
	if r.SetBlock(pos, 0, s.reg.Blocks.Air()) {
		r.UpdateNeighbours(pos)
	}
	levelEvent(r, pos, blockVec(pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}), levelEventDestroyParticles, int32(s.reg.Blocks.Hash(block))) //nolint:gosec // hashes are sent as int32
	for _, it := range items {
		d := NewBlockDrop(pos, it)
		s.spawnItem(r.Dimension(), d.Stack, d.Pos, d.Velocity, world.Rotation{}, d.PickupDelay, 0)
	}
}

// throwDrops puts the drops in the world.
func (s *Host) throwDrops(r *world.Region, drops []BlockDrop) {
	for _, d := range drops {
		if !d.Stack.Empty() {
			s.spawnItem(r.Dimension(), d.Stack, d.Pos, d.Velocity, world.Rotation{}, d.PickupDelay, 0)
		}
	}
}

func dropOffset() float32 {
	return rand.Float32()*dropSpan + dropInset //nolint:gosec // an item's scatter, not a secret
}

func dropDrift() float32 {
	return rand.Float32()*dropDriftSpan - dropDriftSpan/2 //nolint:gosec // an item's scatter, not a secret
}
