package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/helix/world"
)

// raid_garden's and lay_egg's hooks (behavior.GardenRaider, behavior.EggLayer). BDS 1.26.60's
// RaidGardenGoal::tick (0x550C5C0) fires the block event minecraft:on_raided on the block
// above the target as the mob, then posts the eat game event at the mob's attach point 8.
// Only crops, sweet berry bushes and cave vines with berries answer it (GardenEnv).

// GardenEnv is what an engine whose blocks answer raid_garden implements on its Env.
type GardenEnv interface {
	// RaidBlock is minecraft:on_raided on the block at p, raided by the mob with the ID.
	RaidBlock(r *world.Region, p behavior.Pos, by behavior.ID)
}

// MatchesBlock implements behavior.BlockMatcher.
func (m *Mob) MatchesBlock(p behavior.Pos, descriptors []string) bool {
	return m.blockMatches(p, descriptors)
}

// RaidBlock implements behavior.GardenRaider: the block event runs at Apply. The eat game
// event is not posted: Helix's game events come from players only.
func (m *Mob) RaidBlock(p behavior.Pos) { m.do(op{kind: opRaidBlock, pos: p}) }

// ProbePath implements behavior.PathProber: the navigation's createPath to the point
// (NavigationComponent vt+40), made and not followed.
func (m *Mob) ProbePath(to behavior.Vec3) (behavior.PathProbe, bool) {
	if m.nv == nil {
		return behavior.PathProbe{}, false
	}
	nm := m.navMob()
	p := m.nv.CreatePath(&nm, pathfinder{m}, path.Pos{X: floor32(to.X), Y: floor32(to.Y), Z: floor32(to.Z)})
	if p == nil {
		return behavior.PathProbe{}, false
	}
	pr := behavior.PathProbe{Nodes: len(p.Points), Status: p.Status}
	if n := len(p.Points); n > 0 {
		pr.End = p.Points[n-1].Pos
	}
	return pr, true
}
