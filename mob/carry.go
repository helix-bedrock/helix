package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/helix/world"
)

// BlockEnv is what an engine whose mobs carry and place blocks (take_block, place_block)
// implements on its Env.
type BlockEnv interface {
	// ItemBlock is the block an item places; ok is false for an item that places none.
	ItemBlock(it behavior.Item) (block string, ok bool)
	// MayPlace is the block's mayPlace at p.
	MayPlace(r *world.Region, p behavior.Pos, block string) bool
}

// attachCarry is the attach point CanSeeBlock looks from.
const attachCarry = 3

// BlockItem implements behavior.BlockCarrier: one of the block, ItemStack(block, 1). The
// item takes the block's name; its state is not kept, so a placed block takes its default.
func (m *Mob) BlockItem(b behavior.Block) behavior.Item {
	return behavior.Item{Name: b.Name, Count: 1}
}

// CarriedBlock implements behavior.BlockCarrier: the block the main hand item places.
func (m *Mob) CarriedBlock() (string, bool) {
	e, ok := m.rt.Env.(BlockEnv)
	it := m.Slot(behavior.SlotMainhand)
	if !ok || it.Empty() {
		return "", false
	}
	return e.ItemBlock(it)
}

// MayPlace implements behavior.BlockCarrier.
func (m *Mob) MayPlace(block string, p behavior.Pos) bool {
	e, ok := m.rt.Env.(BlockEnv)
	return ok && m.v.r != nil && e.MayPlace(m.v.r, p, block)
}

// CanSeeBlock implements behavior.BlockCarrier: the line from the mob's attach
// point 3 to the block's centre hits nothing, or hits that block.
func (m *Mob) CanSeeBlock(p behavior.Pos) bool {
	if m.v.r == nil {
		return false
	}
	from := m.AttachPos(attachCarry)
	to := path.Vec3{X: float32(p.X) + 0.5, Y: float32(p.Y) + 0.5, Z: float32(p.Z) + 0.5}
	hit, ok := m.v.clip(path.Vec3{X: from.X, Y: from.Y, Z: from.Z}, to)
	return !ok || floor32(hit.X) == p.X && floor32(hit.Y) == p.Y && floor32(hit.Z) == p.Z
}
