package player

import (
	"strings"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// Axes stripping logs, as BDS 1.26.60's log block use (0xD5609C0, LogBlock::use in 1.21.3's
// names). Every log, wood, stem, hyphae and the bamboo block is registered with the stripped
// block it turns into (the block's +936); BDS's string table names one stripped_ block for
// each, so Helix finds the pair by name: "minecraft:X" strips when "minecraft:stripped_X"
// exists. Clicked with an item tagged minecraft:is_axe (0xFD44668), the block becomes the
// stripped block's default state with the same pillar_axis (update flag 3), the axe's
// on_tool_used (0xC981EA0) plays ItemUseOn with the stripped block at the block's centre and
// wears the axe 1 (hurtAndBreak, none in creative), the held item is set back and the
// block_change game event is posted. Sneaking players use the item, not the block, so they
// do not strip (GameMode::useItemOn lets only hoes through).
const (
	strippedPrefix = "stripped_"
	statePillar    = "pillar_axis"
	// axeWear is the damage on_tool_used gives the axe.
	axeWear = 1
)

// strippedName is the block name strips into, "" for a block that does not strip.
func (s *Host) strippedName(name string) string {
	base, ok := strings.CutPrefix(name, "minecraft:")
	if !ok || strings.HasPrefix(base, strippedPrefix) {
		return ""
	}
	stripped := "minecraft:" + strippedPrefix + base
	if _, ok := s.reg.Blocks.Block(stripped); !ok {
		return ""
	}
	return stripped
}

// stripWithAxe is a log's use with an axe. False if the block does not strip or no axe is
// held, so the click goes on.
func (p *Player) stripWithAxe(r *world.Region, u *BlockUse) bool {
	if !p.holdsAxe() {
		return false
	}
	blocks := p.s.reg.Blocks
	name := p.s.strippedName(blocks.Name(u.Block))
	if name == "" {
		return false
	}
	next, ok := blocks.Block(name)
	if !ok {
		return false
	}
	if axis, has := blocks.States(u.Block)[statePillar]; has {
		states := map[string]any{}
		for k, v := range blocks.States(next) {
			states[k] = v
		}
		states[statePillar] = axis
		if id, ok := blocks.StateID(name, states); ok {
			next = id
		}
	}
	if !r.SetBlock(u.Pos, 0, next) {
		return false
	}
	r.UpdateNeighbours(u.Pos)
	playSound(r, u.Pos, p.s.blockSound(packet.SoundEventItemUseOn, blockCenter(u.Pos), next), nil)
	if !p.creative() {
		p.hurtHeld(axeWear)
	}
	p.s.postBlockGameEvent(r, gameEventBlockChange, u.Pos, p, 0, false)
	p.flush()
	return true
}
