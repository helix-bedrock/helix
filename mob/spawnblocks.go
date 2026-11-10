package mob

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/helix-bedrock/entity/spawn"
	files "github.com/helix-bedrock/helix-data/data/files"
	"github.com/helix-bedrock/helix/registry"
)

// SpawnBlocks is what the natural spawner knows about every block state, built once from
// the registry and BDS's material table. Runs: read from any goroutine once built.
type SpawnBlocks struct {
	blocks []spawn.Block
}

// materialFlags are the flag bytes of one BDS MaterialType (helix-data's
// bds/block_materials.json, read from BDS at runtime).
type materialFlags struct {
	Type         uint8 `json:"type"`
	Translucent  bool  `json:"translucent"`
	BlocksMotion bool  `json:"blocks_motion"`
	Solid        bool  `json:"solid"`
}

// NewSpawnBlocks builds the table for every state in reg.
//
// FlagSolid and FlagSolidBlocking stand for BDS's per-block isSolid and BlockSource's
// solid-blocking test. helix-data has only the material's solid byte, so both are the
// material's solid flag on a state whose collision is one whole block, as the mob tables
// do; blocks whose own class changes isSolid are not compared with BDS.
func NewSpawnBlocks(reg *registry.Blocks) (*SpawnBlocks, error) {
	raw, err := fs.ReadFile(files.Files(), "bds/block_materials.json")
	if err != nil {
		return nil, fmt.Errorf("mob: read block materials: %w", err)
	}
	var doc struct {
		Materials []materialFlags `json:"materials"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("mob: decode block materials: %w", err)
	}
	var mats [256]materialFlags
	for _, m := range doc.Materials {
		mats[m.Type] = m
	}
	n := reg.Count()
	b := &SpawnBlocks{blocks: make([]spawn.Block, n)}
	for id := range n {
		b.blocks[id] = spawnBlock(reg, uint32(id), &mats) //nolint:gosec // n is a state count
	}
	return b, nil
}

func spawnBlock(reg *registry.Blocks, id uint32, mats *[256]materialFlags) spawn.Block {
	props := reg.Properties(id)
	full := reg.Name(id)
	name := strings.TrimPrefix(full, "minecraft:")
	states := reg.States(id)
	m := mats[props.Material]
	var f spawn.BlockFlags
	if m.BlocksMotion {
		f |= spawn.FlagBlocksMotion
	}
	if m.Translucent {
		f |= spawn.FlagTranslucent
	}
	if m.Solid && fullBox(props.Collision) {
		f |= spawn.FlagSolid | spawn.FlagSolidBlocking
	}
	if strings.HasSuffix(name, "carpet") {
		f |= spawn.FlagCarpet
	}
	if props.HalfSlab {
		f |= spawn.FlagSlab
		if half, _ := states["minecraft:vertical_half"].(string); half == "bottom" {
			f |= spawn.FlagBottom
		}
	}
	if props.Material == materialWater || props.Material == materialLava {
		if depth, _ := states["liquid_depth"].(int32); depth == 0 {
			f |= spawn.FlagSource
		}
	}
	switch name {
	case "grass_block":
		f |= spawn.FlagGrass
	case "mycelium":
		f |= spawn.FlagMycelium
	case "magma":
		f |= spawn.FlagMagma
	case "campfire", "soul_campfire":
		f |= spawn.FlagCampfire
	case "bedrock", "invisible_bedrock", "barrier":
		f |= spawn.FlagNoSpawnBelow
	}
	return spawn.Block{Name: full, Material: spawn.Material(props.Material), Flags: f}
}

// Block returns the spawner's facts for a state.
func (b *SpawnBlocks) Block(id uint32) spawn.Block {
	if int(id) >= len(b.blocks) {
		return spawn.Block{Name: "minecraft:air"}
	}
	return b.blocks[id]
}
