package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// DamageMagma is BDS's ActorDamageCause for standing on a magma block.
const DamageMagma DamageCause = 22

const (
	nameMagma = "minecraft:magma"
	// hotProbe is how far under the box's bottom BDS centres its search for hurting blocks
	// and looks for the block stood on.
	hotProbe = 0.2
	// magmaDamage is what standing on magma deals each tick, before the hurt time.
	magmaDamage = 1
)

// hotStander is what the hot-block step asks of a player or mob.
type hotStander interface {
	Living
	// skipHotBlock is BDS's skip: the entity's first tick, or riding.
	skipHotBlock() bool
	// standingOn is the block BDS says the entity stands on.
	standingOn(r *world.Region) cube.Pos
	// magmaSafe is BDS's mob check for magma: boots with Frost Walker, fire immunity or
	// sneaking.
	magmaSafe() bool
}

// hotBlockStep matches BDS 1.26.60's hot-block step of an actor's tick, for the box lo..hi:
// when a block with the hurt property lies within a block of the box's bottom (lowered by
// 0.2) and around it, the block the entity stands on picks the damage. Magma, for an entity
// that is not magma safe, marks it as on a hot block (which the pathfinder reads) and hurts
// it 1. The other cause BDS gives here, contact for a cactus or berry bush stood on, is left
// to the contact hazard. It reports the hot-block mark. Runs: the entity's region.
func (s *Host) hotBlockStep(r *world.Region, l hotStander, lo, hi mgl32.Vec3) (onHot bool) {
	if l.Dead() || l.skipHotBlock() || !s.hurtingBlockNear(r, lo, hi) {
		return false
	}
	b, ok := r.Block(l.standingOn(r), 0)
	if !ok || s.reg.Blocks.Name(b) != nameMagma || l.magmaSafe() {
		return false
	}
	l.TakeDamage(magmaDamage, DamageSource{Cause: DamageMagma, Block: nameMagma})
	return true
}

// hurtingBlockNear is BDS's search: a loaded block with the hurt property in x and z one
// block past the box and in y one block either side of its bottom lowered by 0.2, inside the
// dimension's height.
func (s *Host) hurtingBlockNear(r *world.Region, lo, hi mgl32.Vec3) bool {
	if s.mobs == nil {
		return false
	}
	y := lo[1] - hotProbe
	y0, y1 := hotFloor(y-1), hotFloor(y+1)
	rg := r.Dimension().Range()
	y0, y1 = max(y0, rg.Min()), min(y1, rg.Max())
	for x := hotFloor(lo[0] - 1); x <= hotFloor(hi[0]+1); x++ {
		for z := hotFloor(lo[2] - 1); z <= hotFloor(hi[2]+1); z++ {
			for yy := y0; yy <= y1; yy++ {
				b, ok := r.Block(cube.Pos{x, yy, z}, 0)
				if !ok {
					break
				}
				if s.mobs.Blocks.Path(b).Props&path.PropHurts != 0 {
					return true
				}
			}
		}
	}
	return false
}

func hotFloor(f float32) int { return int(math.Floor(float64(f))) }

// --- Player ---

func (p *Player) skipHotBlock() bool { return p.vehicle != nil }

func (p *Player) magmaSafe() bool {
	return p.frostWalking() || p.FireImmune() || p.Moving(MoveSneaking)
}

// standingOn is BDS's block stood on for the player: the block whose collision meets a flat
// box 0.2 under the player's box, nearest the middle when several do, else the block its
// feet are in.
func (p *Player) standingOn(r *world.Region) cube.Pos {
	feet := p.entity.Position().Sub(mgl32.Vec3{0, eyeHeight, 0})
	lo, hi := playerBox(p.entity.Position())
	y := lo[1] - hotProbe
	cx, cz := (lo[0]+hi[0])/2, (lo[2]+hi[2])/2
	best, found, at := float32(0), false, floorPos(feet)
	yb := hotFloor(y)
	for x := hotFloor(lo[0]); x <= hotFloor(hi[0]); x++ {
		for z := hotFloor(lo[2]); z <= hotFloor(hi[2]); z++ {
			pos := cube.Pos{x, yb, z}
			b, ok := r.Block(pos, 0)
			if !ok {
				continue
			}
			for _, bx := range p.s.boxesFor(r, pos, b, lo, hi) {
				minX, maxX := float32(x)+bx[0], float32(x)+bx[3]
				minZ, maxZ := float32(z)+bx[2], float32(z)+bx[5]
				if maxX <= lo[0] || minX >= hi[0] || maxZ <= lo[2] || minZ >= hi[2] ||
					y < float32(yb)+bx[1] || y > float32(yb)+bx[4] {
					continue
				}
				dx, dz := (minX+maxX)/2-cx, (minZ+maxZ)/2-cz
				if d := dx*dx + dz*dz; !found || d < best {
					best, found, at = d, true, pos
				}
			}
		}
	}
	return at
}

// --- Mob ---

func (m *Mob) skipHotBlock() bool { return m.m.FirstTick() || m.m.Vehicle() != behavior.NoID }

func (m *Mob) magmaSafe() bool {
	st, _ := m.m.Equipment(mob.EquipFeet).Data.(ItemStack)
	return hasEnchant(st, enchantFrostWalker) || m.FireImmune() || m.m.Sneaking()
}

func (m *Mob) standingOn(*world.Region) cube.Pos {
	p := m.m.StandingOn()
	return cube.Pos{int(p.X), int(p.Y), int(p.Z)}
}
