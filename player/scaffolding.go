package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/fall"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// scaffoldingItem is the scaffolding block's item.
const scaffoldingItem = "minecraft:scaffolding"

// placeScaffolding is BDS 1.26.60's ScaffoldingBlockItem::useOn (0xC8DB170): using it on
// scaffolding builds the column up. If the block on the clicked side is scaffolding and the
// top or bottom was clicked, or the clicked block itself is scaffolding, the item goes on
// the first block above that column that is not scaffolding, inside the world's height;
// otherwise it places like any block. ScaffoldingBlock's mayPlace (0xD511640) and
// getPlacementBlock (0xD5114D0) come after: never into lava, a loose one only over air or
// water, and the stability where it lands. The falling package runs the rest.
func (p *Player) placeScaffolding(r *world.Region, clicked cube.Pos, face int32, click mgl32.Vec3, held ItemStack) bool {
	f := p.s.falling
	block, ok := p.itemBlock(held)
	if !ok || f == nil {
		return false
	}
	pl := &Placement{Player: p, Clicked: clicked, Face: face, ClickPos: click, Pos: clicked.Side(cube.Face(face)), Item: held, Block: block}
	if from, column := p.scaffoldColumn(r, clicked, cube.Face(face)); column {
		top, ok := scaffoldTop(r, f, from)
		if !ok {
			return false
		}
		pl.Pos, pl.Face = top, int32(cube.FaceUp)
	} else if b, ok := r.Block(clicked, 0); ok && p.s.game.replaceable(p, r, clicked, b, block) {
		pl.Pos = clicked
	}
	sys := p.s.game.sys
	if !f.ScaffoldMayPlace(r, pl.Pos) || !sys.PlaceCheck(pl) {
		return false
	}
	pl.Block = f.ScaffoldState(r, pl.Pos)
	return sys.Place(pl)
}

// scaffoldColumn reports whether a use on clicked's face builds a scaffolding column up, and
// from which block of it the column is walked.
func (p *Player) scaffoldColumn(r *world.Region, clicked cube.Pos, face cube.Face) (cube.Pos, bool) {
	f := p.s.falling
	side := clicked.Side(face)
	if rng := r.Dimension().Range(); side[1] >= rng[0] && side[1] <= rng[1] {
		if b, ok := r.Block(side, 0); ok && f.Kind(b) == fall.Scaffold {
			return side, face == cube.FaceDown || face == cube.FaceUp
		}
	}
	b, ok := r.Block(clicked, 0)
	return clicked, ok && f.Kind(b) == fall.Scaffold
}

// scaffoldTop is the first block above from that is not scaffolding, false if it is above
// the world's height.
func scaffoldTop(r *world.Region, f *fall.Table, from cube.Pos) (cube.Pos, bool) {
	top := r.Dimension().Range()[1]
	at := from
	for at[1] <= top {
		b, ok := r.Block(at, 0)
		if !ok || f.Kind(b) != fall.Scaffold {
			break
		}
		at[1]++
	}
	return at, at[1] <= top
}
