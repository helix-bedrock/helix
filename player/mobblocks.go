package player

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// --- mob.BlockEnv ---

// ItemBlock is the block a mob's carried item places, as a player's placing finds it.
func (s *Host) ItemBlock(it behavior.Item) (string, bool) {
	id, ok := s.itemBlock(realStack(it))
	if !ok {
		return "", false
	}
	return s.reg.Blocks.Name(id), true
}

// MayPlace is a block's mayPlace at pos for a mob putting it down on the block below: what
// holds attached blocks, the plants' soil and survival, and no entity that blocks building
// in the way. A player's reach and its own box are not asked.
func (s *Host) MayPlace(r *world.Region, p behavior.Pos, block string) bool {
	id, ok := s.reg.Blocks.Block(block)
	if !ok || r == nil {
		return false
	}
	pos := cube.Pos{int(p.X), int(p.Y), int(p.Z)}
	if !s.attach.MayPlace(r, pos, id, cube.FaceUp) {
		return false
	}
	if s.farm != nil && !s.farm.MayPlace(r, pos, id) {
		return false
	}
	return !s.obstructed(r, pos, id)
}
