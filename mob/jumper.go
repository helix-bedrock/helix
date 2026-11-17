package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
)

// The hooks of jump_around_target (behavior.Jumper, the breeze) and jump_to_block
// (behavior.BlockJumper, the frog and goat). BDS 1.26.60's JumpAroundTargetGoal::tick
// (0x5A06810) writes the AABBShapeComponent box directly: the movement then moves that box and
// takes the position from it, so a shrunk box lasts until the goal puts the old size back.
// Helix rebuilds the box from the mob's size after each move unless a goal set its own.

// boxSlack is how far a box's size may be off the mob's size and still count as its own.
const boxSlack = 1e-4

// DiscardFriction implements behavior.Jumper: DiscardFrictionFlagComponent.
func (m *Mob) DiscardFriction() bool { return m.body.DiscardFriction }

// SetDiscardFriction implements behavior.Jumper.
func (m *Mob) SetDiscardFriction(on bool) { m.body.DiscardFriction = on }

// SetBox implements behavior.Jumper: Actor::setAABB. The position follows at the next move.
func (m *Mob) SetBox(b behavior.Box) {
	m.body.Box = b
	w, h := b.Max.X-b.Min.X, b.Max.Y-b.Min.Y
	m.customBox = absf(w-m.body.Width) > boxSlack || absf(h-m.body.Height) > boxSlack
}

// EffectAmplifier implements behavior.Jumper: Helix's mobs have no effects yet.
func (m *Mob) EffectAmplifier(int) (int32, bool) { return 0, false }

// LastHurtTime implements behavior.Jumper: Actor::getLastHurtTimestamp, the tick of the last
// hurt that landed.
func (m *Mob) LastHurtTime() uint64 { return m.hurtAt }

// NodeType implements behavior.BlockJumper: the pathfinder's isFree for a 1x1x1 box.
func (m *Mob) NodeType(from, p behavior.Pos) path.NodeType {
	if m.finder == nil {
		return path.NodeBlocked
	}
	pf := pathfinder{m}
	return m.finder.NodeType(pathWorld{&m.v}, pf.settings(), pf.flags(), from, p, path.Pos{X: 1, Y: 1, Z: 1})
}

func absf(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
