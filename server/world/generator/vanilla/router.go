package vanilla

import (
	"encoding/json"
	"fmt"

	"github.com/helix-bedrock/helix-data/data"
)

// settings are a dimension's noise settings.
type settings struct {
	MinY, Height int32
	SeaLevel     int32
	DefaultBlock string
	DefaultFluid string
	Aquifers     bool
}

// router holds the density functions terrain is made of, for one world
// seed and dimension.
type router struct {
	prog *program
	set  settings

	finalDensity, preliminarySurface                            node
	temperature, vegetation, continents, erosion, depth, ridges node
	barrier, floodedness, spread, lava, aquiferErosion          node
	aquiferDepth, aquiferExclusion                              node
	materialRule                                                json.RawMessage
}

// newRouter reads a dimension's noise settings, such as "overworld", and
// builds its density functions for seed.
func newRouter(seed uint64, dimension string) (*router, error) {
	wg, err := data.Worldgen()
	if err != nil {
		return nil, err
	}
	raw, ok := wg["noise_settings/"+dimension]
	if !ok {
		return nil, fmt.Errorf("vanilla: no noise settings for %q", dimension)
	}
	var ns struct {
		Noise struct {
			MinY   int32 `json:"min_y"`
			Height int32 `json:"height"`
		} `json:"noise"`
		SeaLevel     int32                      `json:"sea_level"`
		DefaultBlock json.RawMessage            `json:"default_block"`
		DefaultFluid json.RawMessage            `json:"default_fluid"`
		Router       map[string]json.RawMessage `json:"noise_router"`
		Aquifers     map[string]json.RawMessage `json:"aquifers"`
		MaterialRule json.RawMessage            `json:"material_rule"`
	}
	if err := json.Unmarshal(raw, &ns); err != nil {
		return nil, fmt.Errorf("vanilla: noise settings %s: %w", dimension, err)
	}
	r := &router{
		prog: &program{data: wg, named: map[string]node{}, noises: map[string]*normalNoise{},
			split: xoroshiroFromSeed(seed).splitter(), seed: seed},
		set: settings{MinY: ns.Noise.MinY, Height: ns.Noise.Height, SeaLevel: ns.SeaLevel,
			DefaultBlock: blockName(ns.DefaultBlock), DefaultFluid: blockName(ns.DefaultFluid), Aquifers: len(ns.Aquifers) > 0},
		materialRule: ns.MaterialRule,
	}
	fields := []struct {
		m    map[string]json.RawMessage
		key  string
		dest *node
	}{
		{ns.Router, "final_density", &r.finalDensity},
		{ns.Router, "temperature", &r.temperature},
		{ns.Router, "vegetation", &r.vegetation},
		{ns.Router, "continents", &r.continents},
		{ns.Router, "erosion", &r.erosion},
		{ns.Router, "depth", &r.depth},
		{ns.Router, "ridges", &r.ridges},
		{ns.Aquifers, "barrier", &r.barrier},
		{ns.Aquifers, "fluid_level_floodedness", &r.floodedness},
		{ns.Aquifers, "fluid_level_spread", &r.spread},
		{ns.Aquifers, "lava", &r.lava},
		{ns.Aquifers, "exclusion", &r.aquiferExclusion},
		{ns.Aquifers, "surface_level", &r.preliminarySurface},
	}
	for _, f := range fields {
		v, ok := f.m[f.key]
		if !ok {
			*f.dest = constant(0)
			continue
		}
		n, err := r.prog.parse(v)
		if err != nil {
			return nil, fmt.Errorf("vanilla: %s %s: %w", dimension, f.key, err)
		}
		*f.dest = n
	}
	// The aquifer reads erosion and depth from the noise router.
	r.aquiferErosion, r.aquiferDepth = r.erosion, r.depth
	return r, nil
}

// blockName reads a block in a settings file: a name or {"Name": ...}.
func blockName(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var b struct {
		Name string `json:"Name"`
	}
	_ = json.Unmarshal(raw, &b)
	return b.Name
}

// climate is a position's six climate values, quantized as Java does.
type climate [7]int64

func quantize(v float64) int64 { return int64(float32(v) * 10000) }

// climateAt samples the climate at a block position.
func (r *router) climateAt(c *evalCtx, x, y, z int32) climate {
	return climate{
		quantize(r.temperature.eval(c, x, y, z)),
		quantize(r.vegetation.eval(c, x, y, z)),
		quantize(r.continents.eval(c, x, y, z)),
		quantize(r.erosion.eval(c, x, y, z)),
		quantize(r.depth.eval(c, x, y, z)),
		quantize(r.ridges.eval(c, x, y, z)),
		0,
	}
}
