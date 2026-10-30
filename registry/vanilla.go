package registry

import (
	"fmt"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/protocol/nbt"
)

// Vanilla registers everything vanilla of BDS 1.26.60 from helix-data:
// blocks, items, biomes, entity types, the creative inventory and the
// recipes. Untouched, every registry packet a player gets is BDS's own.
func Vanilla(s *Setup) error {
	for _, step := range []func(*Setup) error{vanillaBlocks, vanillaItems, vanillaBiomes, vanillaEntityTypes, vanillaLive} {
		if err := step(s); err != nil {
			return err
		}
	}
	return nil
}

// vanillaBiomes registers every biome BDS sends, with the numeric IDs BDS
// registers them under.
func vanillaBiomes(s *Setup) error {
	ids, err := data.BiomeIDs()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	pk, err := data.BiomeDefinitions()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	str := func(i int) (string, error) {
		if i < 0 || i >= len(pk.StringList) {
			return "", fmt.Errorf("registry: vanilla: biome string %d of %d", i, len(pk.StringList))
		}
		return pk.StringList[i], nil
	}
	for _, d := range pk.BiomeDefinitions {
		name, err := str(int(d.NameIndex))
		if err != nil {
			return err
		}
		id, ok := ids[name]
		if !ok {
			return fmt.Errorf("registry: vanilla: biome %s has no ID", name)
		}
		b := Biome{
			Name: name, ID: id, Temperature: d.Temperature, Downfall: d.Downfall, FoliageSnow: d.FoliageSnow,
			Depth: d.Depth, Scale: d.Scale, MapWaterColour: d.MapWaterColour, Rain: d.Rain, gen: d.ChunkGeneration,
		}
		if tags, ok := d.Tags.Value(); ok {
			b.hasTags = true
			for _, t := range tags {
				tag, err := str(int(t))
				if err != nil {
					return err
				}
				b.Tags = append(b.Tags, tag)
			}
		}
		if err := s.RegisterBiome(b); err != nil {
			return err
		}
		s.s.vanillaBiomeNames[name] = true
	}
	s.s.vanillaBiomes = pk
	return nil
}

// vanillaEntityTypes registers every entity type BDS tells clients about,
// with its behaviour pack definition.
func vanillaEntityTypes(s *Setup) error {
	pk, err := data.ActorIdentifiers()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	defs, err := data.Entities()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	var list struct {
		IDList []identifier `nbt:"idlist"`
	}
	if err := nbt.UnmarshalEncoding(pk.SerialisedEntityIdentifiers, &list, nbt.NetworkLittleEndian); err != nil {
		return fmt.Errorf("registry: vanilla: actor identifiers: %w", err)
	}
	for _, e := range list.IDList {
		if err := s.RegisterEntityType(EntityType{
			Name: e.ID, BaseID: e.BaseID, RuntimeID: e.RuntimeID, HasSpawnEgg: e.HasSpawnEgg, Summonable: e.Summonable,
			Definition: defs[e.ID],
		}); err != nil {
			return err
		}
	}
	s.s.vanillaEntities = pk
	return nil
}

// vanillaLive sets the creative inventory and recipes BDS sends.
func vanillaLive(s *Setup) error {
	creative, err := data.CreativeContent()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	crafting, err := data.CraftingData()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	s.s.creative, s.s.crafting = creative, crafting
	return nil
}

// vanillaItems registers every item BDS tells joining players about, with
// its network ID, version and components as BDS sends them, and its stack
// size and tags as BDS reports them.
func vanillaItems(s *Setup) error {
	reg, err := data.ItemRegistry()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	list, err := data.Items()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	info := make(map[string]int, len(list))
	for i, it := range list {
		info[it.Name] = i
	}
	for _, e := range reg {
		t := ItemType{Name: e.Name, ID: e.RuntimeID, Version: e.Version, ComponentBased: e.ComponentBased, Components: e.Data}
		if i, ok := info[e.Name]; ok {
			t.MaxStack, t.Tags = uint8(min(max(list[i].MaxStack, 0), 255)), list[i].Tags //nolint:gosec // clamped
		}
		if err := s.RegisterItem(t); err != nil {
			return err
		}
	}
	return nil
}

// vanillaBlocks registers every vanilla block: each state as BDS saves
// it, its numbers as BDS holds them at runtime, and its default state.
func vanillaBlocks(s *Setup) error {
	states, err := data.BlockStates()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	props, err := data.AllBlockProperties()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	defaults, err := data.BlockDefaults()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	// block_states.nbt lists each block's states together, in the order
	// the per-state property arrays use.
	var t BlockType
	flush := func() error {
		if t.Name == "" {
			return nil
		}
		def, ok := defaults[t.Name]
		if !ok {
			return fmt.Errorf("registry: vanilla: %s has no default state", t.Name)
		}
		t.Default = -1
		for i, st := range t.States {
			if sameStates(st.States, def) {
				t.Default = i
				break
			}
		}
		if t.Default < 0 {
			return fmt.Errorf("registry: vanilla: default state of %s is not one of its states", t.Name)
		}
		return s.RegisterBlock(t)
	}
	for _, st := range states {
		if st.Name != t.Name {
			if err := flush(); err != nil {
				return err
			}
			t = BlockType{Name: st.Name}
		}
		p, ok := props[st.Name]
		if !ok {
			return fmt.Errorf("registry: vanilla: %s has no properties", st.Name)
		}
		i := len(t.States)
		t.States = append(t.States, BlockState{States: st.Properties, Version: st.Version, Properties: Properties{
			LightEmission: p.LightEmission.At(i), LightDampening: p.LightDampening.At(i),
			FlameOdds: p.FlameOdds.At(i), BurnOdds: p.BurnOdds.At(i),
			ExplosionResistance: p.ExplosionResistance.At(i), Friction: p.Friction.At(i), DestroyTime: p.DestroyTime.At(i),
		}})
	}
	return flush()
}

// sameStates compares a state with a default read from JSON, where every
// number is a float64.
func sameStates(st map[string]any, def map[string]any) bool {
	if len(st) != len(def) {
		return false
	}
	for k, v := range st {
		d, ok := def[k]
		if !ok {
			return false
		}
		switch v := v.(type) {
		case uint8:
			if f, ok := d.(float64); !ok || f != float64(v) {
				return false
			}
		case int32:
			if f, ok := d.(float64); !ok || f != float64(v) {
				return false
			}
		case string:
			if d != v {
				return false
			}
		default:
			return false
		}
	}
	return true
}
