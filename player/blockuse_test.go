package player

import (
	"testing"

	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// A player's click opens and shuts wooden doors (from either half), trapdoors and
// fence gates, through the same path a click from the client takes; iron ones
// leave the click to the held item.
func TestClickOpensDoors(t *testing.T) {
	p, d := machineWorld(t)
	p.abilities = DefaultAbilities()
	blocks := p.s.reg.Blocks
	waitRegion(t, d, func(*world.Region) bool { return p.entity.Region() != nil })
	open := func(r *world.Region, pos cube.Pos) bool {
		b, _ := r.Block(pos, 0)
		v, _ := blocks.States(b)["open_bit"].(uint8)
		return v != 0
	}
	click := func(r *world.Region, pos cube.Pos) bool {
		b, _ := r.Block(pos, 0)
		return p.s.game.sys.UseBlock(&BlockUse{Player: p, Pos: pos, Block: b, Face: int32(cube.FaceUp)})
	}
	door, gate, trap := cube.Pos{1, -60, 1}, cube.Pos{3, -60, 1}, cube.Pos{5, -60, 1}
	iron := cube.Pos{7, -60, 1}
	onRegion(d, func(r *world.Region) {
		for _, c := range []struct {
			pos  cube.Pos
			name string
		}{{door, "minecraft:wooden_door"}, {iron, "minecraft:iron_door"}} {
			b, _ := blocks.Block(c.name)
			if !blockuse.PlaceDoor(blocks, r, c.pos, b) {
				t.Fatalf("%s not placed", c.name)
			}
		}
		setNamed(t, p, r, gate, "minecraft:fence_gate", nil)
		setNamed(t, p, r, trap, "minecraft:trapdoor", nil)

		// Lower half then upper half: the door opens, then shuts.
		for _, pos := range []cube.Pos{door, gate, trap} {
			if !click(r, pos) || !open(r, pos) {
				t.Errorf("%v not opened", pos)
			}
		}
		for _, pos := range []cube.Pos{door.Side(cube.FaceUp), gate, trap} {
			if !click(r, pos) {
				t.Errorf("click on %v not taken", pos)
			}
		}
		for _, pos := range []cube.Pos{door, gate, trap} {
			if open(r, pos) {
				t.Errorf("%v still open", pos)
			}
		}
		if click(r, iron) || open(r, iron) {
			t.Error("an iron door opened by hand")
		}
	})
}
