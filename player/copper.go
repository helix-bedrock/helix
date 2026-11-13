package player

import (
	"strings"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Copper's oxidation stages by name, as BDS 1.26.60's copper blocks hold them (CopperBlock's
// data at +944: the chain of stages, the stage, waxed). Only what lightning needs is here:
// reading a block's stage and stepping it back one. Ageing, scraping with an axe and waxing are
// not built.
const (
	copperExposed   = "exposed_"
	copperWeathered = "weathered_"
	copperOxidized  = "oxidized_"
	copperWaxed     = "minecraft:waxed_"
	// copperBlockName and copperExposedBlock are the one pair that does not follow the prefix
	// rule: the first stage of the plain block is copper_block, the next exposed_copper.
	copperBlockName    = "minecraft:copper_block"
	copperExposedBlock = "minecraft:exposed_copper"
	// copperUpperBit marks a door's upper half, which changes with the lower.
	copperUpperBit = "upper_block_bit"
)

// copperStages are the prefixes of oxidation stages 1 to 3.
var copperStages = [...]string{copperExposed, copperWeathered, copperOxidized}

// copperStage is the oxidation stage of the block called name, 0 to 3, and whether it is a
// copper block that is not waxed at all. A waxed one reports false.
func (s *Host) copperStage(name string) (int, bool) {
	if strings.HasPrefix(name, copperWaxed) {
		return 0, false
	}
	base, ok := strings.CutPrefix(name, "minecraft:")
	if !ok {
		return 0, false
	}
	for i, prefix := range copperStages {
		if strings.HasPrefix(base, prefix) {
			return i + 1, true
		}
	}
	if name == copperBlockName {
		return 0, true
	}
	_, ok = s.reg.Blocks.Block("minecraft:" + copperExposed + base)
	return 0, ok
}

// lessOxidized is the name of the stage before name's, "" for stage 0 or no copper.
func (s *Host) lessOxidized(name string) string {
	stage, ok := s.copperStage(name)
	if !ok || stage == 0 {
		return ""
	}
	if name == copperExposedBlock {
		return copperBlockName
	}
	base := strings.TrimPrefix(name, "minecraft:")
	base = strings.TrimPrefix(base, copperStages[stage-1])
	if stage == 1 {
		return "minecraft:" + base
	}
	return "minecraft:" + copperStages[stage-2] + base
}

// deoxidize steps the copper block at pos back one oxidation stage, keeping its states, as
// BDS's sub_CB2C940 does (a door's two halves together). False if it is no copper that can.
// Runs: owner of the region.
func (s *Host) deoxidize(r *world.Region, pos cube.Pos) bool {
	block, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	blocks := s.reg.Blocks
	name := s.lessOxidized(blocks.Name(block))
	if name == "" {
		return false
	}
	states := blocks.States(block)
	next, ok := blocks.StateID(name, states)
	if !ok || !r.SetBlock(pos, 0, next) {
		return false
	}
	if upper, isDoor := states[copperUpperBit].(uint8); isDoor {
		other := pos.Side(cube.FaceUp)
		if upper != 0 {
			other = pos.Side(cube.FaceDown)
		}
		if b, ok := r.Block(other, 0); ok && blocks.Name(b) == blocks.Name(block) {
			if id, ok := blocks.StateID(name, blocks.States(b)); ok && r.SetBlock(other, 0, id) {
				r.UpdateNeighbours(other)
			}
		}
	}
	r.UpdateNeighbours(pos)
	return true
}
