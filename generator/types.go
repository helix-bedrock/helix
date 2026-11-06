package generator

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
	"github.com/helix-bedrock/worldgen/1.26.60/overworld"
)

// Names of the built-in world types.
const (
	// NameVanilla is BDS's own terrain ([Vanilla]).
	NameVanilla = "vanilla"
	// NameFlat is BDS's flat world: level.dat's layers, or the options'.
	NameFlat = "flat"
	// NameVoid is BDS's void world: nothing at all.
	NameVoid = "void"
)

// Generator numbers level.dat stores and StartGame sends, as BDS numbers them.
// The client reads them for the horizon and fog, not to make terrain.
const (
	LevelInfinite int32 = 1
	LevelFlat     int32 = 2
	LevelVoid     int32 = 5
)

// Context is what a world type builds its generator from.
type Context struct {
	// Registry is the frozen registry: blocks and biomes to place.
	Registry *registry.Registry
	// Seed is the world's seed, level.dat's RandomSeed.
	Seed int64
	// Dimension is the dimension the generator is for, and Range its heights.
	Dimension mcdb.Dimension
	Range     cube.Range
	// Options is the type's own setting from the server config, "" for none:
	// for vanilla the path of a worldgen JSON file (overworld.ParseConfig's
	// format), for flat a FlatWorldLayers JSON.
	Options string
	// FlatLayers is level.dat's FlatWorldLayers JSON, "" for BDS's default.
	FlatLayers string
	// Log is the server's log.
	Log *slog.Logger
}

// Type is a world type a server can be set to by name.
type Type struct {
	// New builds the generator. It runs once, when the world opens.
	New func(ctx Context) (world.Generator, error)
	// Level is the generator number kept in level.dat and sent to players:
	// [LevelInfinite], [LevelFlat] or [LevelVoid]. 0 means [LevelInfinite].
	Level int32
}

// Types are the world types to pick from, by name. Plugins add their own
// before the server starts:
//
//	conf := helix.DefaultConfig()
//	conf.Generators["islands"] = generator.Type{New: newIslands}
//	conf.WorldType = "islands"
type Types map[string]Type

// Default returns the built-in types: vanilla, flat and void.
func Default() Types {
	return Types{
		NameVanilla: {New: newVanilla, Level: LevelInfinite},
		NameFlat:    {New: newFlat, Level: LevelFlat},
		NameVoid:    {New: newVoid, Level: LevelVoid},
	}
}

// Names returns the type names, sorted.
func (t Types) Names() []string {
	names := make([]string, 0, len(t))
	for n := range t {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// ForLevel returns the built-in type of a level.dat generator number: flat
// for 2, void for 5, vanilla for the rest (1, and BDS's old 0 limited worlds).
func ForLevel(level int32) string {
	switch level {
	case LevelFlat:
		return NameFlat
	case LevelVoid:
		return NameVoid
	}
	return NameVanilla
}

// newVanilla builds [Vanilla] for the overworld, with the worldgen JSON file
// named by the options. Its packs are found next to the file.
func newVanilla(ctx Context) (world.Generator, error) {
	if ctx.Dimension != mcdb.Overworld {
		return nil, fmt.Errorf("generator: vanilla: only the overworld is built so far, not %v", ctx.Dimension)
	}
	var conf *overworld.Config
	if ctx.Options != "" {
		data, err := os.ReadFile(ctx.Options)
		if err != nil {
			return nil, fmt.Errorf("generator: vanilla: %w", err)
		}
		conf, err = overworld.ParseConfig(data, os.DirFS(filepath.Dir(ctx.Options)))
		if err != nil {
			return nil, fmt.Errorf("generator: vanilla: %s: %w", ctx.Options, err)
		}
	}
	return NewVanilla(ctx.Registry, ctx.Seed, conf)
}

// newFlat builds BDS's flat world from the options' layers, else level.dat's.
func newFlat(ctx Context) (world.Generator, error) {
	layers := ctx.FlatLayers
	if o := bytes.TrimSpace([]byte(ctx.Options)); len(o) > 0 {
		layers = string(o)
	}
	f, err := world.ParseFlat(layers, ctx.Registry.Blocks, ctx.Registry.Biomes, ctx.Range)
	if err != nil {
		return nil, fmt.Errorf("generator: flat: %w", err)
	}
	return f, nil
}

// newVoid builds the void.
func newVoid(Context) (world.Generator, error) { return world.Empty{}, nil }
