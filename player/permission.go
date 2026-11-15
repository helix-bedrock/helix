package player

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Allow and deny blocks decide where players may build, as BDS 1.26.60 checks it in
// BlockSource::checkBlockPermissions (0xCE57920, using an item on a block) and
// checkBlockDestroyPermissions (0xCE574C0, breaking): the column under the block is walked
// down from it, and the first allow block found lets the player build there, the first deny
// block stops it; with neither, the game mode decides. A column holding a border block is
// closed to everyone, and so is everything above the world's top. A creative operator skips
// all of it and only needs the build or mine ability.
const (
	allowBlockName = "minecraft:allow"
	denyBlockName  = "minecraft:deny"
)

// BlockPermissionFunc decides whether p may break the block at pos (destroy) or use what it
// holds on the block at pos, clicked on face. Vanilla is BDS's allow, deny and border block
// rules.
type BlockPermissionFunc func(p *Player, pos cube.Pos, face int32, destroy bool) bool

// vanillaBlockPermission is BDS's checkBlockPermissions and checkBlockDestroyPermissions,
// without the deny particle: GameMode and Item::_checkUseOnPermissions ask for none.
func vanillaBlockPermission(p *Player, pos cube.Pos, face int32, destroy bool) bool {
	r := p.entity.Region()
	if r == nil {
		return false
	}
	ability := AbilityBuild
	if destroy {
		ability = AbilityMine
	}
	if p.mode() == GameModeCreative && p.abilities.Flag(AbilityOperatorCommands) {
		return p.abilities.Flag(ability)
	}
	// Adventure players build only as privileged builders unless an allow block says so. The
	// item lists of adventure mode (can_place_on, can_destroy) are not read here.
	fallback := p.mode() != GameModeAdventure || p.abilities.Flag(AbilityPrivilegedBuilder)
	target := pos
	if !destroy {
		if b, ok := r.Block(pos, 0); ok && p.s.game.builtOver(p.s.reg.Blocks.Name(b), b) && borderAt(r, pos) {
			return false
		}
		if face >= int32(cube.FaceDown) && face <= int32(cube.FaceEast) {
			target = pos.Side(cube.Face(face))
		}
	}
	if !p.abilities.Flag(ability) || target[1] > r.Dimension().Range()[1] || borderAt(r, target) {
		return false
	}
	switch p.s.columnRule(r, target) {
	case ruleAllow:
		return true
	case ruleDeny:
		return false
	}
	return fallback
}

// borderAt reports whether pos is in a column walled off by a border block.
func borderAt(r *world.Region, pos cube.Pos) bool {
	border, _ := r.BorderColumn(pos[0], pos[2])
	return border
}

// builtOver reports whether b is a block placing goes into rather than next to (BDS's
// canBeBuiltOver as the permission check asks it, with no block being placed).
func (g *gameplay) builtOver(name string, b uint32) bool {
	if rb, ok := g.blocks[name].(Replaceable); ok {
		return rb.Replaceable(b)
	}
	return g.replaceables[name]
}

// columnRule is what the column decides at pos.
type columnRule uint8

const (
	ruleNone columnRule = iota
	ruleAllow
	ruleDeny
)

// columnRule walks down from pos to the world's floor and returns the first allow or deny
// block found.
func (s *Host) columnRule(r *world.Region, pos cube.Pos) columnRule {
	blocks := s.reg.Blocks
	floor := r.Dimension().Range()[0]
	for y := pos[1]; y >= floor; y-- {
		b, ok := r.Block(cube.Pos{pos[0], y, pos[2]}, 0)
		if !ok {
			continue
		}
		switch blocks.Name(b) {
		case allowBlockName:
			return ruleAllow
		case denyBlockName:
			return ruleDeny
		}
	}
	return ruleNone
}
