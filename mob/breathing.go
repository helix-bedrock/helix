package mob

import "math"

// breathRatio is where a mob's breathing point sits, as parts of its collision box: up
// is times the height, forward times the width. BDS's look control aims from this point.
type breathRatio struct{ up, forward float32 }

// breathRatioOf gives BDS 1.26.60's breathing point for a mob type. BDS sets it in the
// constructors of some actor classes (every Animal, villagers, creepers, witches,
// guardians and a few more); every other mob has none, so it looks from its feet.
func breathRatioOf(typ string) breathRatio {
	switch typ {
	case "minecraft:cow", "minecraft:chicken", "minecraft:pig", "minecraft:sheep", "minecraft:wolf",
		"minecraft:rabbit", "minecraft:ocelot", "minecraft:cat", "minecraft:llama",
		"minecraft:trader_llama", "minecraft:polar_bear", "minecraft:panda", "minecraft:bee",
		"minecraft:goat", "minecraft:camel", "minecraft:camel_husk", "minecraft:sniffer",
		"minecraft:armadillo", "minecraft:horse", "minecraft:donkey", "minecraft:mule",
		"minecraft:skeleton_horse", "minecraft:zombie_horse", "minecraft:fox", "minecraft:frog",
		"minecraft:happy_ghast", "minecraft:axolotl", "minecraft:mooshroom", "minecraft:strider",
		"minecraft:parrot", "minecraft:turtle",
		"minecraft:villager", "minecraft:villager_v2", "minecraft:creaking":
		return breathRatio{up: 0.85}
	case "minecraft:creeper", "minecraft:witch", "minecraft:guardian", "minecraft:elder_guardian",
		"minecraft:shulker":
		return breathRatio{up: 0.9}
	case "minecraft:breeze":
		return breathRatio{up: 0.76}
	case "minecraft:pufferfish":
		return breathRatio{up: 0.5}
	case "minecraft:ender_dragon":
		return breathRatio{up: 0.25}
	case "minecraft:ghast":
		return breathRatio{up: 0.2, forward: 0.5}
	case "minecraft:nautilus":
		return breathRatio{up: 0.2, forward: -0.5}
	}
	return breathRatio{}
}

// breathingPos is the mob's breathing point: its position plus the offset, turned by the
// body's yaw as BDS turns attach points.
func (m *Mob) breathingPos() (x, y, z float32) {
	fwd := m.breath.forward * m.body.Width
	a := float64(m.bodyYaw * -math.Pi / 180)
	p := m.body.Pos
	return p.X + fwd*float32(math.Sin(a)), p.Y + m.breath.up*m.body.Height, p.Z + fwd*float32(math.Cos(a))
}
