package registry

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/helix-bedrock/helix-data/data"
	files "github.com/helix-bedrock/helix-data/data/1.26.60"
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
		b.Humid = vanillaHumid(name, d.Downfall)
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
	trims, err := data.TrimData()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	s.s.creative, s.s.crafting, s.s.trims = creative, crafting, trims
	if s.s.voxelShapes, err = data.VoxelShapes(); err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	if s.s.jigsaw, err = data.JigsawStructureData(); err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	return nil
}

// vanillaItems registers every item BDS tells joining players about, with
// its network ID, version and components as BDS sends them, its stack
// size and tags as BDS reports them, and where BDS lets players wear or
// hold it.
func vanillaItems(s *Setup) error {
	reg, err := data.ItemRegistry()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	list, err := data.Items()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	armorSlots, offhandList, err := data.ItemEquipment()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	offhand := make(map[string]bool, len(offhandList))
	for _, name := range offhandList {
		offhand[name] = true
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
		t.ArmorSlot, t.Wearable = armorSlots[e.Name]
		t.Offhand = offhand[e.Name]
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
	defs, err := data.BlockDefinitions()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	coll, err := data.CollisionData()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	push, err := data.BlockPushData()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	pushes, stairs := make(map[string]bool, len(push.Solid)), make(map[string]bool, len(push.Stairs))
	for _, n := range push.Solid {
		pushes[n] = true
	}
	for _, n := range push.Stairs {
		stairs[n] = true
	}
	climb, err := data.Climbable()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	climbable := make(map[string]bool, len(climb))
	for _, n := range climb {
		climbable[n] = true
	}
	materials, err := data.BlockMaterials()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	liq, err := vanillaLiquids()
	if err != nil {
		return err
	}
	transl, err := data.BlockTranslucency()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	mining, err := data.MiningData()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	definitions := make(map[string]map[string]any, len(defs))
	for _, d := range defs {
		definitions[d.Name] = d.Properties
		s.s.definitionOrder = append(s.s.definitionOrder, d.Name)
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
		t.Definition = definitions[t.Name]
		return s.RegisterBlock(t)
	}
	for _, st := range states {
		if st.Name != t.Name {
			if err := flush(); err != nil {
				return err
			}
			t = BlockType{Name: st.Name, Tags: mining.Blocks[st.Name].Tags}
		}
		p, ok := props[st.Name]
		if !ok {
			return fmt.Errorf("registry: vanilla: %s has no properties", st.Name)
		}
		i := len(t.States)
		t.States = append(t.States, BlockState{States: st.Properties, Version: st.Version, Properties: Properties{
			LightEmission: p.LightEmission.At(i), LightDampening: p.LightDampening.At(i),
			FlameOdds: p.FlameOdds.At(i), BurnOdds: p.BurnOdds.At(i), LavaFlammable: p.LavaFlammable.At(i) == 1, Translucency: transl[st.Name],
			ExplosionResistance: p.ExplosionResistance.At(i), Friction: p.Friction.At(i), DestroyTime: p.DestroyTime.At(i),
			Climbable: climbable[st.Name], Material: materials[st.Name], PushesOut: pushes[st.Name], Stairs: stairs[st.Name],
			SolidMaterial: liq.solid[st.Name], LiquidReaction: liq.reaction[st.Name], CanContainLiquid: liq.contain[st.Name], LavaBlocking: liq.lavaBlocking[st.Name],
		}})
		boxes, computed, err := collisionOf(coll, st.Name, st.Properties)
		if err != nil {
			return err
		}
		t.States[i].Properties.Collision, t.States[i].Properties.CollisionComputed = boxes, computed
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

// collisionOf returns a state's collision boxes from helix-data's
// bds/collision.json, and whether BDS computes them in code instead.
func collisionOf(c *data.Collision, name string, props map[string]any) ([]Box, bool, error) {
	if comp, ok := c.Computed[name]; ok {
		if rule := stateRule(comp.Rule); rule != nil {
			return rule(name, props), false, nil
		}
		return nil, true, nil
	}
	b, ok := c.Blocks[name]
	if !ok {
		return nil, false, fmt.Errorf("registry: vanilla: %s has no collision", name)
	}
	src := b.Boxes
	if len(b.States) > 0 {
		src = nil
		for _, s := range b.States {
			if sameValues(s.Properties, props) {
				src = s.Boxes
				break
			}
		}
		if src == nil {
			return nil, false, fmt.Errorf("registry: vanilla: %s: no collision for state %v", name, props)
		}
	}
	boxes := make([]Box, len(src))
	for i, bx := range src {
		boxes[i] = Box(bx)
	}
	return boxes, false, nil
}

// sameValues compares state values written with different integer types.
func sameValues(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if fmt.Sprint(v) != fmt.Sprint(b[k]) {
			return false
		}
	}
	return true
}

// liquidFacts are helix-data's liquid facts by block name.
type liquidFacts struct {
	reaction       map[string]LiquidReaction
	contain, solid map[string]bool
	lavaBlocking   map[string]bool
}

// vanillaLiquids reads the liquid facts of every block.
func vanillaLiquids() (liquidFacts, error) {
	d, err := data.Liquids()
	if err != nil {
		return liquidFacts{}, fmt.Errorf("registry: vanilla: %w", err)
	}
	f := liquidFacts{reaction: map[string]LiquidReaction{}, contain: map[string]bool{}, solid: map[string]bool{}, lavaBlocking: map[string]bool{}}
	for name, blocks := range d.Reaction {
		r, ok := liquidReaction(name)
		if !ok {
			return f, fmt.Errorf("registry: vanilla: unknown liquid reaction %q", name)
		}
		for _, b := range blocks {
			f.reaction[b] = r
		}
	}
	for _, b := range d.CanContain {
		f.contain[b] = true
	}
	for _, b := range d.Solid {
		f.solid[b] = true
	}
	for _, b := range d.LavaBlocking {
		f.lavaBlocking[b] = true
	}
	return f, nil
}

// biomeFile is the part of a vanilla biome file vanillaHumid reads.
type biomeFile struct {
	Biome struct {
		Components struct {
			Humidity *biomeHumidity `json:"minecraft:humidity"`
		} `json:"components"`
	} `json:"minecraft:biome"`
}

// biomeHumidity is a biome file's minecraft:humidity component.
type biomeHumidity struct {
	IsHumid bool `json:"is_humid"`
}

// humidDownfall is the downfall above which a biome without a minecraft:humidity component is
// humid, BDS's rule.
const humidDownfall = 0.85

// vanillaHumid is whether a vanilla biome is humid: its file's minecraft:humidity component,
// else downfall above humidDownfall.
func vanillaHumid(name string, downfall float32) bool {
	raw, err := files.Files().ReadFile("vanilla/biomes/" + strings.TrimPrefix(name, "minecraft:") + ".biome.json")
	if err == nil {
		var doc biomeFile
		if json.Unmarshal(raw, &doc) == nil && doc.Biome.Components.Humidity != nil {
			return doc.Biome.Components.Humidity.IsHumid
		}
	}
	return downfall > humidDownfall
}
