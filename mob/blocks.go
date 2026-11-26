package mob

import (
	"strings"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/physics"
	"github.com/helix-bedrock/helix-data/data"
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
	// perch is the log groups random_fly and parrots perch on (with the leaves property).
	perch map[string]bool
	// rules is each state's path-through check.
	rules []pathRule
	// full is set for states whose collision is one whole block.
	full []bool
	// wall is what a cell of a border column reads as: the barrier, a solid block of one
	// whole box. BDS walls the column off for actors without IgnoresBorderCollisions.
	wall uint32
	// topSupport matches BDS(block, up, Any) as the engine answers it
	// (SetTopSupport); nil falls back to blocking motion.
	topSupport func(id uint32) bool
}

// SetTopSupport gives the engine's answer to whether a block holds things on its top face
// (BDS's canProvideSupport, face up, support type Any), which a teleport stands on. Set it
// before any mob ticks; it is read from any region.
func (b *Blocks) SetTopSupport(fn func(id uint32) bool) { b.topSupport = fn }

// TopSupport reports that the block holds things on its top face.
func (b *Blocks) TopSupport(id uint32) bool {
	if b.topSupport != nil {
		return b.topSupport(id)
	}
	return b.Goal(id).BlocksMotion
}

// The BDS material types the tables test (MaterialType numbers, as the registry keeps them).
const (
	materialAir    = 0
	materialWater  = registry.MaterialWater
	materialLava   = registry.MaterialLava
	materialPortal = 16
)

// NewBlocks builds the tables for every state in reg from BDS 1.26.60's own block facts, as the
// registry carries them from helix-data: the BlockProperty bits of the block type (BlockType;
// the pathfinder's Props are the same bits), the Material's flag byte (never buildable,
// liquid, blocks motion, solid) and the Block solid flag. Only Kind, the blocks
// BDS tells apart by type, still comes from the name.
func NewBlocks(reg *registry.Blocks) *Blocks {
	n := reg.Count()
	b := &Blocks{reg: reg, perch: map[string]bool{}, path: make([]path.BlockInfo, n), phys: make([]physics.Block, n), goals: make([]behavior.Block, n), full: make([]bool, n)}
	for _, name := range behavior.DefaultWalkBlocks().Perch {
		b.perch[name] = true
	}
	// A missing table leaves every block on BDS's default check.
	rules, _ := data.BlockPathRules()
	b.rules = make([]pathRule, n)
	for id := range n {
		b.fill(uint32(id))                               //nolint:gosec // n is a state count
		if kind, ok := rules[reg.Name(uint32(id))]; ok { //nolint:gosec // n is a state count
			b.rules[id] = readPathRule(kind, reg.Name(uint32(id)), reg.States(uint32(id))) //nolint:gosec // n is a state count
		}
	}
	b.wall, _ = reg.Block("minecraft:barrier")
	return b
}

func (b *Blocks) fill(id uint32) {
	props := b.reg.Properties(id)
	name := strings.TrimPrefix(b.reg.Name(id), "minecraft:")
	states := b.reg.States(id)
	full := fullBox(props.Collision)
	b.full[id] = full
	mat := path.Material(props.Material)
	liquid := props.MaterialFlags&data.MaterialLiquid != 0
	solid := props.MaterialFlags&data.MaterialSolid != 0
	blocksMotion := props.MaterialFlags&data.MaterialBlocksMotion != 0
	top, height := shapeTop(props.Collision)

	info := path.BlockInfo{
		Kind: kindOf(name), Props: path.Props(props.TypeFlags), Material: mat,
		Solid: props.BlockSolid, SolidBlocking: solidBlocking(props.MaterialFlags),
		Open:         stateTrue(states, "open_bit"),
		Extinguished: stateTrue(states, "extinguished"),
		Arms:         armsOf(states),
		Flowing:      name == "flowing_water",
		VisualTop:    visualTop(name, states, top),
	}
	b.path[id] = info

	b.phys[id] = physics.Block{
		Air: props.Material == materialAir, Material: mat, Friction: props.Friction,
		SoulSand: name == "soul_sand", PowderSnow: name == "powder_snow",
		Climbable: props.Climbable, StopsDescent: name == "scaffolding",
		LowJump: name == "honey_block", Restitution: restitution(name),
		// BDS's stand-on slowdown is slime's and honey's; the inside rules are honey's.
		StandSlow: name == "slime" || name == "honey_block", Honey: name == "honey_block",
	}

	b.goals[id] = behavior.Block{
		Name: b.reg.Name(id), Material: mat,
		Ground: groundOf(props),
		Solid:  solid, Liquid: liquid,
		HasCollision: len(props.Collision) > 0, BlocksMotion: blocksMotion,
		Tree:        props.TypeFlags&data.BlockPropertyLeaves != 0 || b.perch[b.reg.Name(id)],
		NoJumpStart: props.TypeFlags&data.BlockPropertyPreventsJumping != 0, NoJumpLanding: props.TypeFlags&propCausesDamage != 0,
		BlockSolid: props.BlockSolid,
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

// propCausesDamage is BDS's BlockProperty CausesDamage (1<<22), which keeps jump_around_target
// from landing in the block.
const propCausesDamage = 1 << 22

// solidBlocking matches BDS: the material blocks motion and is not never
// buildable, the same bits as the ground test below.
func solidBlocking(flags uint8) bool {
	return flags&(data.MaterialNeverBuildable|data.MaterialBlocksMotion) == data.MaterialBlocksMotion
}

// groundOf is BDS's ground test of the random position search and strolling: the material
// blocks motion and is not never-buildable, and the block type is cube shaped. Glass,
// leaves, slabs, fences and walls are not ground.
func groundOf(p registry.Properties) bool {
	return solidBlocking(p.MaterialFlags) &&
		p.TypeFlags&data.BlockPropertyCubeShaped != 0
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

// armsOf reads a fence, pane or bar's arms from its connection states, which since 1.26
// are part of the block state.
func armsOf(s map[string]any) uint8 {
	var arms uint8
	for i, state := range armStates {
		if stateTrue(s, state) {
			arms |= 1 << i
		}
	}
	return arms
}

// armStates are the connection states in the order of the pathfinder's Arm bits.
var armStates = [...]string{
	"minecraft:connection_north", "minecraft:connection_east",
	"minecraft:connection_south", "minecraft:connection_west",
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
	if name == "honey_block" {
		// BDS's honey bounciness: -1, no bounce at all.
		return -1
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
