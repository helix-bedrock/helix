package player

import "image/color"

// Player indexes, as BDS 1.26.60 hands them out when a player is added to the level
// (0xCE31BB0): the lowest index no user on the list holds, counting the joining player's
// own unset index 0, so the first player gets 1. The index is synched actor data 27;
// clients colour the player on maps and the locator bar by it.

// freeIndex is the lowest index from 1 that no other player on the list holds. Global
// owner only.
func (g *global) freeIndex(p *Player) int32 {
	for i := int32(1); ; i++ {
		taken := false
		for _, o := range g.order {
			if o != p && o.index == i {
				taken = true
				break
			}
		}
		if !taken {
			return i
		}
	}
}

// playerColours is BDS's 13 player colours (0x1B5C378), as its float colours turn into
// bytes (mce::Color::toARGB truncates).
var playerColours = [13]color.RGBA{
	{237, 237, 237, 255}, {84, 255, 255, 255}, {255, 169, 0, 255}, {189, 236, 159, 255},
	{253, 253, 174, 255}, {255, 84, 255, 255}, {169, 169, 169, 255}, {166, 195, 255, 255},
	{84, 255, 84, 255}, {230, 123, 120, 255}, {129, 165, 255, 255}, {255, 255, 84, 255},
	{25, 189, 159, 255},
}

// playerColour is the colour BDS gives a player index (Level 0xCE360C0): index-1 taken
// as an unsigned number, modulo 13, so index 0 gives the third colour.
func playerColour(index int32) color.RGBA {
	return playerColours[(uint64(int64(index))-1)%uint64(len(playerColours))]
}
