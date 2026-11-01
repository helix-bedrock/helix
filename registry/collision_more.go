package registry

// More of BDS 1.26.60's block shape rules (see collision.go). Each box is
// min x, y, z, then max x, y, z inside the block.

// moreRule returns the rules of collision_more.go by name.
func moreRule(rule string) func(name string, s map[string]any) []Box {
	switch rule {
	case "trapdoor":
		return trapdoorShape
	case "candle":
		return candleShape
	case "shelf":
		return shelfShape
	case "hanging_sign":
		return hangingSignShape
	case "lantern":
		return lanternShape
	case "chain":
		return chainShape
	case "rod":
		return rodShape
	case "skull":
		return skullShape
	case "amethyst":
		return amethystShape
	case "anvil":
		return anvilShape
	case "dripstone":
		return dripstoneShape
	case "snow_layer":
		return snowLayerShape
	case "pitcher_crop":
		return pitcherCropShape
	case "bamboo":
		return bambooShape
	case "composter":
		return composterShape
	case "chorus_plant":
		return chorusPlantShape
	case "big_dripleaf":
		return bigDripleafShape
	case "pale_moss_carpet":
		return paleMossCarpetShape
	case "end_portal_frame":
		return endPortalFrameShape
	case "cocoa":
		return cocoaShape
	case "hopper":
		return hopperShape
	case "bell":
		return bellShape
	case "cake":
		return cakeShape
	case "cauldron":
		return cauldronShape
	case "brewing_stand":
		return brewingStandShape
	case "grindstone":
		return grindstoneShape
	case "ladder":
		return ladderShape
	}
	return nil
}

// trapdoorThin is the thickness of trapdoors and door panels in BDS.
const trapdoorThin = 0.1825

// trapdoorShape: closed, a thin slab at the bottom or, upside down, the
// top; open, a panel at the side its direction says.
func trapdoorShape(_ string, s map[string]any) []Box {
	if num(s, "open_bit") == 0 {
		if num(s, "upside_down_bit") != 0 {
			return []Box{{0, 1 - trapdoorThin, 0, 1, 1, 1}}
		}
		return []Box{{0, 0, 0, 1, trapdoorThin, 1}}
	}
	switch num(s, "direction") {
	case 1:
		return []Box{{1 - trapdoorThin, 0, 0, 1, 1, 1}}
	case 2:
		return []Box{{0, 0, 0, 1, 1, trapdoorThin}}
	case 3:
		return []Box{{0, 0, 1 - trapdoorThin, 1, 1, 1}}
	}
	return []Box{{0, 0, 0, trapdoorThin, 1, 1}}
}

// candleShape is the box around one to four candles.
func candleShape(_ string, s map[string]any) []Box {
	boxes := [4]Box{
		{0.4375, 0, 0.4375, 0.5625, 0.375, 0.5625},
		{0.3125, 0, 0.4375, 0.6875, 0.375, 0.625},
		{0.3125, 0, 0.375, 0.625, 0.375, 0.6875},
		{0.3125, 0, 0.3125, 0.6875, 0.375, 0.625},
	}
	return []Box{boxes[num(s, "candles")&3]}
}

// shelfShape is a board 5/16 deep against the side opposite its facing.
func shelfShape(_ string, s map[string]any) []Box {
	const depth = 0.3125
	switch enum(s, "minecraft:cardinal_direction", cardinalOrder) {
	case 1:
		return []Box{{1 - depth, 0, 0, 1, 1, 1}}
	case 2:
		return []Box{{0, 0, 1 - depth, 1, 1, 1}}
	case 3:
		return []Box{{0, 0, 0, depth, 1, 1}}
	}
	return []Box{{0, 0, 0, 1, 1, depth}}
}

// hangingSignShape: nothing for a sign hanging under a block, else the bar
// along the top of the sign.
func hangingSignShape(_ string, s map[string]any) []Box {
	if num(s, "hanging") != 0 {
		return nil
	}
	if f := num(s, "facing_direction"); f == 2 || f == 3 {
		return []Box{{0, 0.875, 0.375, 1, 1, 0.625}}
	}
	return []Box{{0.375, 0.875, 0, 0.625, 1, 1}}
}

// lanternShape: standing, or hanging an eighth lower from the block above.
func lanternShape(_ string, s map[string]any) []Box {
	if num(s, "hanging") != 0 {
		return []Box{{0.3125, 0.125, 0.3125, 0.6875, 0.625, 0.6875}}
	}
	return []Box{{0.3125, 0, 0.3125, 0.6875, 0.5, 0.6875}}
}

// chainShape is a 3/16 thick bar along its axis.
func chainShape(_ string, s map[string]any) []Box {
	const lo, hi = 0.40625, 0.59375
	switch enum(s, "pillar_axis", axisOrder) {
	case 1:
		return []Box{{0, lo, lo, 1, hi, hi}}
	case 2:
		return []Box{{lo, lo, 0, hi, hi, 1}}
	}
	return []Box{{lo, 0, lo, hi, 1, hi}}
}

// rodShape is a quarter-block thick rod along its facing's axis (end rods
// and lightning rods).
func rodShape(_ string, s map[string]any) []Box {
	switch num(s, "facing_direction") {
	case 2, 3:
		return []Box{{0.375, 0.375, 0, 0.625, 0.625, 1}}
	case 4, 5:
		return []Box{{0, 0.375, 0.375, 1, 0.625, 0.625}}
	}
	return []Box{{0.375, 0, 0.375, 0.625, 1, 0.625}}
}

// skullShape: on the floor, or on a wall at the side its facing says.
func skullShape(_ string, s map[string]any) []Box {
	switch num(s, "facing_direction") {
	case 2:
		return []Box{{0.25, 0.25, 0.5, 0.75, 0.75, 1}}
	case 3:
		return []Box{{0.25, 0.25, 0, 0.75, 0.75, 0.5}}
	case 4:
		return []Box{{0.5, 0.25, 0.25, 1, 0.75, 0.75}}
	case 5:
		return []Box{{0, 0.25, 0.25, 0.5, 0.75, 0.75}}
	}
	return []Box{{0.25, 0, 0.25, 0.75, 0.5, 0.75}}
}

// amethystSize is each amethyst bud's height and inset, in sixteenths, as
// BDS sets them on the block type.
func amethystSize(name string) (h, w float32) {
	switch name {
	case "minecraft:small_amethyst_bud":
		return 3, 4
	case "minecraft:medium_amethyst_bud":
		return 4, 3
	case "minecraft:large_amethyst_bud":
		return 5, 3
	}
	return 7, 3 // amethyst_cluster, ice_crystal
}

// amethystShape grows from the face it is attached to.
func amethystShape(name string, s map[string]any) []Box {
	h, w := amethystSize(name)
	h, w = h/16, w/16
	lo, hi := w, 1-w
	switch enum(s, "minecraft:block_face", faceOrder) {
	case 1:
		return []Box{{lo, 0, lo, hi, h, hi}}
	case 2:
		return []Box{{lo, lo, 1 - h, hi, hi, 1}}
	case 3:
		return []Box{{lo, lo, 0, hi, hi, h}}
	case 4:
		return []Box{{1 - h, lo, lo, 1, hi, hi}}
	case 5:
		return []Box{{0, lo, lo, h, hi, hi}}
	}
	return []Box{{lo, 1 - h, lo, hi, 1, hi}}
}

// anvilShape is the anvil's full-height body along its facing.
func anvilShape(_ string, s map[string]any) []Box {
	if d := enum(s, "minecraft:cardinal_direction", cardinalOrder); d == 1 || d == 3 {
		return []Box{{0, 0, 0.125, 1, 1, 0.875}}
	}
	return []Box{{0.125, 0, 0, 0.875, 1, 1}}
}

// dripstoneShape is a column, thinner toward the tip; a tip is shorter.
// BDS also moves it by the block's random offset, which is not ported.
func dripstoneShape(_ string, s map[string]any) []Box {
	switch enum(s, "dripstone_thickness", dripOrder) {
	case 0:
		if num(s, "hanging") != 0 {
			return []Box{{0.3125, 0.3125, 0.3125, 0.6875, 1, 0.6875}}
		}
		return []Box{{0.3125, 0, 0.3125, 0.6875, 0.6875, 0.6875}}
	case 1:
		return []Box{{0.25, 0, 0.25, 0.75, 1, 0.75}}
	case 2:
		return []Box{{0.1875, 0, 0.1875, 0.8125, 1, 0.8125}}
	case 3:
		return []Box{{0.125, 0, 0.125, 0.875, 1, 0.875}}
	}
	return []Box{{0.3125, 0, 0.3125, 0.6875, 1, 0.6875}}
}

// snowLayerShape is the shape players get: nothing under three layers'
// worth, half a block from there, a full block when complete. BDS gives
// items, falling blocks and some other entities other heights.
func snowLayerShape(_ string, s map[string]any) []Box {
	switch h := num(s, "height"); {
	case h >= 7:
		return []Box{{0, 0, 0, 1, 1, 1}}
	case h >= 3:
		return []Box{{0, 0, 0, 1, 0.5, 1}}
	}
	return nil
}

// pitcherCropShape: the lower half only, bigger once it grows.
func pitcherCropShape(_ string, s map[string]any) []Box {
	if num(s, "upper_block_bit") != 0 {
		return nil
	}
	if num(s, "growth") <= 0 {
		return []Box{{0.3125, -0.0625, 0.3125, 0.6875, 0.1875, 0.6875}}
	}
	return []Box{{0.1875, -0.0625, 0.1875, 0.8125, 0.3125, 0.8125}}
}

// bambooShape is the stalk. BDS also moves it by the block's random
// offset, which is not ported.
func bambooShape(_ string, s map[string]any) []Box {
	if s["bamboo_stalk_thickness"] == "thick" {
		return []Box{{0.5, 0, 0.5, 0.6875, 1, 0.6875}}
	}
	return []Box{{0.5, 0, 0.5, 0.625, 1, 0.625}}
}

// walls returns the four walls of an open-topped block, an eighth thick.
func walls() []Box {
	return []Box{
		{0, 0, 0, 0.125, 1, 1},
		{0, 0, 0, 1, 1, 0.125},
		{0.875, 0, 0, 1, 1, 1},
		{0, 0, 0.875, 1, 1, 1},
	}
}

// composterShape is the floor, at the height of the compost, and walls.
func composterShape(_ string, s map[string]any) []Box {
	var floor float32 = 0.125
	switch l := num(s, "composter_fill_level"); {
	case l >= 8:
		floor = 0.9375
	case l > 0:
		floor = float32(2*l+1) / 16
	}
	return append([]Box{{0, 0, 0, 1, floor, 1}}, walls()...)
}

// cauldronShape is the floor and the walls.
func cauldronShape(string, map[string]any) []Box {
	return append([]Box{{0, 0, 0, 1, 0.3125, 1}}, walls()...)
}

// hopperShape is the bowl's floor and rim, then the funnel.
func hopperShape(string, map[string]any) []Box {
	return []Box{
		{0, 0.625, 0, 1, 0.6875, 1},
		{0, 0.6875, 0, 0.125, 1, 1},
		{0.875, 0.6875, 0, 1, 1, 1},
		{0.125, 0.6875, 0, 0.875, 1, 0.125},
		{0.125, 0.6875, 0.875, 0.875, 1, 1},
		{0.25, 0.25, 0.25, 0.75, 0.625, 0.75},
	}
}

// brewingStandShape is the rod, then the base.
func brewingStandShape(string, map[string]any) []Box {
	return []Box{{0.4375, 0, 0.4375, 0.5625, 0.875, 0.5625}, {0, 0, 0, 1, 0.125, 1}}
}

// endPortalFrameShape is the frame and, with an eye, the eye.
func endPortalFrameShape(_ string, s map[string]any) []Box {
	boxes := []Box{{0, 0, 0, 1, 0.8125, 1}}
	if num(s, "end_portal_eye_bit") != 0 {
		boxes = append(boxes, Box{0.3125, 0.8125, 0.3125, 0.6875, 1, 0.6875})
	}
	return boxes
}

// chorusPlantShape is the middle, reaching each connected side.
func chorusPlantShape(_ string, s map[string]any) []Box {
	b := Box{0.125, 0.125, 0.125, 0.875, 0.875, 0.875}
	if num(s, "minecraft:connection_west") != 0 {
		b[0] = 0
	}
	if num(s, "minecraft:connection_down") != 0 {
		b[1] = 0
	}
	if num(s, "minecraft:connection_north") != 0 {
		b[2] = 0
	}
	if num(s, "minecraft:connection_east") != 0 {
		b[3] = 1
	}
	if num(s, "minecraft:connection_up") != 0 {
		b[4] = 1
	}
	if num(s, "minecraft:connection_south") != 0 {
		b[5] = 1
	}
	return []Box{b}
}

// bigDripleafShape is the leaf of a head, lower as it tilts, and nothing
// once fully tilted or for the stem. BDS gives projectiles a full block.
func bigDripleafShape(_ string, s map[string]any) []Box {
	if num(s, "big_dripleaf_head") == 0 {
		return nil
	}
	switch enum(s, "big_dripleaf_tilt", tiltOrder) {
	case 2:
		return []Box{{0, 0.6875, 0, 1, 0.8125, 1}}
	case 3:
		return nil
	}
	return []Box{{0, 0.6875, 0, 1, 0.9375, 1}}
}

// paleMossCarpetShape: the bottom carpet is a sixteenth high, the upper
// part has nothing.
func paleMossCarpetShape(_ string, s map[string]any) []Box {
	if num(s, "upper_block_bit") != 0 {
		return nil
	}
	return []Box{{0, 0, 0, 1, 0.0625, 1}}
}

// cocoaShape is the pod, bigger with age, on the side of the log its
// direction faces.
func cocoaShape(_ string, s map[string]any) []Box {
	age := float32(num(s, "age"))
	size, height := 2*age+4, 2*age+5
	half := size / 2
	y0 := (12 - height) / 16
	mid0, mid1 := (8-half)/16, (8+half)/16
	switch num(s, "direction") {
	case 1:
		return []Box{{0.0625, y0, mid0, (size + 1) / 16, 0.75, mid1}}
	case 2:
		return []Box{{mid0, y0, 0.0625, mid1, 0.75, (size + 1) / 16}}
	case 3:
		return []Box{{(15 - size) / 16, y0, mid0, 0.9375, 0.75, mid1}}
	}
	return []Box{{mid0, y0, (15 - size) / 16, mid1, 0.75, 0.9375}}
}

// bellShape: on the floor, hanging, on one wall or between two.
func bellShape(_ string, s map[string]any) []Box {
	d := num(s, "direction")
	across := d == 0 || d == 2
	switch enum(s, "attachment", attachOrder) {
	case 1:
		return []Box{{0.25, 0.25, 0.25, 0.75, 1, 0.75}}
	case 2:
		minX := [4]float32{0.25, 0.1875, 0.25, 0}
		minZ := [4]float32{0, 0.25, 0.1875, 0.25}
		maxX := [4]float32{0.75, 1, 0.75, 0.8125}
		maxZ := [4]float32{0.8125, 0.75, 1, 0.75}
		i := d & 3
		return []Box{{minX[i], 0.25, minZ[i], maxX[i], 0.9375, maxZ[i]}}
	case 3:
		if across {
			return []Box{{0.25, 0.25, 0, 0.75, 1, 1}}
		}
		return []Box{{0, 0.25, 0.25, 1, 1, 0.75}}
	}
	if across {
		return []Box{{0, 0, 0.25, 1, 0.8125, 0.75}}
	}
	return []Box{{0.25, 0, 0, 0.75, 0.8125, 1}}
}

// cakeShape is the cake less its bites.
func cakeShape(_ string, s map[string]any) []Box {
	return []Box{{float32(2*num(s, "bite_counter")+1) / 16, 0, 0.0625, 0.9375, 0.5, 0.9375}}
}

// grindstoneShape: on a wall, along its direction; otherwise standing or
// hanging, the same upright shape.
func grindstoneShape(_ string, s map[string]any) []Box {
	if enum(s, "attachment", attachOrder) != 2 {
		return []Box{{0.125, 0, 0.125, 0.875, 1, 0.875}}
	}
	if d := num(s, "direction"); d == 0 || d == 2 {
		return []Box{{0.125, 0.125, 0, 0.875, 0.875, 1}}
	}
	return []Box{{0, 0.125, 0.125, 1, 0.875, 0.875}}
}

// ladderShape is a thin panel against the wall its facing points away
// from; ladders cannot face up or down, which has no box.
func ladderShape(_ string, s map[string]any) []Box {
	switch num(s, "facing_direction") {
	case 2:
		return []Box{{0, 0, 0.8125, 1, 1, 1}}
	case 3:
		return []Box{{0, 0, 0, 1, 1, 0.1875}}
	case 4:
		return []Box{{0.8125, 0, 0, 1, 1, 1}}
	case 5:
		return []Box{{0, 0, 0, 0.1875, 1, 1}}
	}
	return nil
}
