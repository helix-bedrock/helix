package player

import (
	"maps"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/loot"
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

// DropsFunc returns the items a block drops, given what broke it. Replace it in
// Systems to change drops. Runs: on the region tick.
type DropsFunc func(s *Host, block uint32, c *loot.Context) []ItemStack

// vanillaDrops is BDS's getResourceDrops, through its drop rule. Experience is
// worked out but no orbs exist yet.
func vanillaDrops(s *Host, block uint32, c *loot.Context) []ItemStack {
	c.State = func(name string) (int32, bool) { return blockStateNumber(s.reg.Blocks.States(block), name) }
	res := s.drops.Roll(s.reg.Blocks.Name(block), c)
	items := make([]ItemStack, 0, len(res.Stacks))
	for _, d := range res.Stacks {
		if it, ok := s.dropStack(block, d); ok {
			items = append(items, it)
		}
	}
	return items
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
		switch id {
		case enchantSilkTouch:
			c.Silk = level > 0
		case enchantFortune:
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

// throwDrops is BDS's spawnResources: each item at a random spot in the block.
func (s *Host) throwDrops(r *world.Region, pos cube.Pos, items []ItemStack) {
	for _, it := range items {
		at := blockVec(pos).Add(mgl32.Vec3{dropOffset(), dropOffset(), dropOffset()})
		vel := mgl32.Vec3{dropDrift(), dropUp, dropDrift()}
		s.spawnItem(r.Dimension(), it, at, vel, world.Rotation{}, blockDropDelay, 0)
	}
}

func dropOffset() float32 {
	return rand.Float32()*dropSpan + dropInset //nolint:gosec // an item's scatter, not a secret
}

func dropDrift() float32 {
	return rand.Float32()*dropDriftSpan - dropDriftSpan/2 //nolint:gosec // an item's scatter, not a secret
}
