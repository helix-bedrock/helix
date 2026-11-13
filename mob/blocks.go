package mob

import (
	"strings"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/physics"
	"github.com/helix-bedrock/helix/registry"
)

// Blocks is what the entity library knows about every block state, built once from the
// registry. The library never reads block names itself; Helix answers with these facts.
// Runs: read from any goroutine once built.
type Blocks struct {
	reg   *registry.Blocks
	path  []path.BlockInfo
	phys  []physics.Block
	goals []behavior.Block
	// full is set for states whose collision is one whole block.
	full []bool
}

// The BDS material types the tables test (MaterialType numbers, as the registry keeps them).
const (
	materialAir    = 0
	materialWater  = registry.MaterialWater
	materialLava   = registry.MaterialLava
	materialPortal = 16
)

// NewBlocks builds the tables for every state in reg. Kinds and property bits come from the
// block names, the way BDS gives them to its block classes (stairs, slabs, doors...).
//
// Not compared with BDS state by state: Solid and SolidBlocking come from the registry's
// solid material flag and a full collision box, not from BDS's Material table, which helix-data
// does not have yet.
func NewBlocks(reg *registry.Blocks) *Blocks {
	n := reg.Count()
	b := &Blocks{reg: reg, path: make([]path.BlockInfo, n), phys: make([]physics.Block, n), goals: make([]behavior.Block, n), full: make([]bool, n)}
	for id := range n {
		b.fill(uint32(id)) //nolint:gosec // n is a state count
	}
	return b
}

func (b *Blocks) fill(id uint32) {
	props := b.reg.Properties(id)
	name := strings.TrimPrefix(b.reg.Name(id), "minecraft:")
	states := b.reg.States(id)
	full := fullBox(props.Collision)
	b.full[id] = full
	mat := path.Material(props.Material)
	liquid := props.Material == materialWater || props.Material == materialLava
	solid := props.SolidMaterial && !liquid
	top, height := shapeTop(props.Collision)

	info := path.BlockInfo{
		Kind: kindOf(name), Props: propsOf(name, props), Material: mat,
		Solid: solid && full, SolidBlocking: solid && full,
		Open:         stateTrue(states, "open_bit"),
		Extinguished: stateTrue(states, "extinguished"),
		Flowing:      name == "flowing_water",
		VisualTop:    visualTop(name, states, top),
	}
	b.path[id] = info

	b.phys[id] = physics.Block{
		Air: props.Material == materialAir, Material: mat, Friction: props.Friction,
		SoulSand: name == "soul_sand", PowderSnow: name == "powder_snow",
		Climbable: props.Climbable, StopsDescent: name == "scaffolding",
		LowJump: name == "honey_block", Restitution: restitution(name),
	}

	b.goals[id] = behavior.Block{
		Name: b.reg.Name(id), Material: mat,
		Ground: solid && !liquid,
		Solid:  solid, Liquid: liquid,
		HasCollision: len(props.Collision) > 0, BlocksMotion: solid,
		Tree:       strings.HasSuffix(name, "_log") || strings.HasSuffix(name, "_leaves") || strings.HasSuffix(name, "_stem") || strings.HasSuffix(name, "_wood"),
		BlockSolid: solid && full,
		ShapeTop:   top, ShapeHeight: height,
		Stairs: info.Props&path.PropStairs != 0, Slab: info.Props&path.PropSlab != 0, Trapdoor: info.Props&path.PropTrapdoor != 0,
		Fence: info.Props&path.PropFence != 0 && !strings.HasSuffix(name, "_wall"),
	}
	if host, ok := infestedHosts[name]; ok {
		b.goals[id].Host = "minecraft:" + host
	}
	if inf, ok := hostInfested[name]; ok {
		b.goals[id].Infested = "minecraft:" + inf
	}
}

// Path returns the pathfinder's facts for a state.
func (b *Blocks) Path(id uint32) *path.BlockInfo {
	if int(id) >= len(b.path) {
		return &b.path[0]
	}
	return &b.path[id]
}

// Physics returns physics' facts for a state.
func (b *Blocks) Physics(id uint32) physics.Block {
	if int(id) >= len(b.phys) {
		return physics.Block{Air: true}
	}
	return b.phys[id]
}

// Goal returns the goals' facts for a state.
func (b *Blocks) Goal(id uint32) behavior.Block {
	if int(id) >= len(b.goals) {
		return behavior.Block{}
	}
	return b.goals[id]
}

// Full reports a state whose collision is one whole block.
func (b *Blocks) Full(id uint32) bool { return int(id) < len(b.full) && b.full[id] }

func fullBox(boxes []registry.Box) bool {
	return len(boxes) == 1 && boxes[0] == registry.Box{0, 0, 0, 1, 1, 1}
}

func shapeTop(boxes []registry.Box) (top, height float32) {
	if len(boxes) == 0 {
		return 0, 0
	}
	lo := float32(1)
	for _, bx := range boxes {
		top = max(top, bx[4])
		lo = min(lo, bx[1])
	}
	return top, top - lo
}

func stateTrue(s map[string]any, key string) bool {
	switch v := s[key].(type) {
	case uint8:
		return v != 0
	case int32:
		return v != 0
	}
	return false
}

// visualTop is how high the block looks inside its cell: water by its depth, otherwise the
// top of its collision, or a whole block.
func visualTop(name string, s map[string]any, top float32) float32 {
	if name == "water" || name == "flowing_water" || name == "lava" || name == "flowing_lava" {
		depth, _ := s["liquid_depth"].(int32)
		if depth >= 8 {
			depth = 0
		}
		return 1 - float32(depth+1)/9
	}
	if top > 0 {
		return top
	}
	return 1
}

func restitution(name string) float32 {
	if name == "slime" {
		return 1
	}
	if strings.HasSuffix(name, "_bed") || name == "bed" {
		return 0.66
	}
	return 0
}

// kindOf is the pathfinder's special blocks.
func kindOf(name string) path.Kind {
	switch name {
	case "air", "cave_air", "void_air":
		return path.KindAir
	case "magma":
		return path.KindMagma
	case "campfire", "soul_campfire":
		return path.KindCampfire
	case "honey_block":
		return path.KindHoney
	case "iron_door":
		return path.KindIronDoor
	case "powder_snow":
		return path.KindPowderSnow
	case "scaffolding":
		return path.KindScaffolding
	case "water", "flowing_water":
		return path.KindWater
	case "chest", "trapped_chest":
		return path.KindChest
	}
	if strings.HasSuffix(name, "_shelf") {
		return path.KindShelf
	}
	return path.KindOther
}

// propsOf sets the BDS block class bits the pathfinder reads.
func propsOf(name string, p registry.Properties) path.Props {
	var out path.Props
	has := func(suffix string) bool { return strings.HasSuffix(name, suffix) }
	switch {
	case has("_stairs"):
		out |= path.PropStairs
	case has("_slab") && !has("double_slab"):
		out |= path.PropSlab
	case name == "hopper":
		out |= path.PropHopper
	case name == "snow_layer":
		out |= path.PropTopSnow
	case has("fence_gate"):
		out |= path.PropFenceGate
	case has("_leaves") || has("leaves2") || name == "leaves":
		out |= path.PropLeaves
	case has("_fence") || has("_wall") && !has("_sign") && !has("_banner") && !has("_torch") && !has("_fan"):
		out |= path.PropFence
	case has("carpet"):
		out |= path.PropCarpet | path.PropSmallSolid
	case has("_button"):
		out |= path.PropButton
	case has("_door") && !has("trapdoor"):
		out |= path.PropDoor
	case has("trapdoor"):
		out |= path.PropTrapdoor
	case has("_sign") || name == "standing_sign" || name == "wall_sign":
		out |= path.PropSign
	case has("shulker_box") || name == "ender_chest":
		out |= path.PropSmallSolid
	case has("pressure_plate"):
		out |= path.PropPressurePlate
	}
	switch name {
	case "fire", "soul_fire", "campfire", "soul_campfire", "magma", "sweet_berry_bush", "cactus", "wither_rose", "lava", "flowing_lava":
		out |= path.PropHurts
	}
	if name == "cactus" || name == "sweet_berry_bush" {
		out |= path.PropHurtsOnContact
	}
	if name == "cauldron" || name == "composter" {
		out |= path.PropInnerFloor
	}
	_ = p
	return out
}

// Silverfish blocks: the block an infested block hides and the reverse.
var (
	infestedHosts = map[string]string{
		"infested_stone": "stone", "infested_cobblestone": "cobblestone", "infested_stone_bricks": "stone_bricks",
		"infested_mossy_stone_bricks": "mossy_stone_bricks", "infested_cracked_stone_bricks": "cracked_stone_bricks",
		"infested_chiseled_stone_bricks": "chiseled_stone_bricks", "infested_deepslate": "deepslate",
	}
	hostInfested = func() map[string]string {
		m := make(map[string]string, len(infestedHosts))
		for k, v := range infestedHosts {
			m[v] = k
		}
		return m
	}()
)
