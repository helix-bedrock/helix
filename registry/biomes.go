package registry

import (
	"fmt"
	"slices"
	"strings"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Plains replaces deleted biomes, as the engine design decides.
const Plains = "minecraft:plains"

// Biome is a biome as the client learns it at join. The numbers are what
// BDS sends in BiomeDefinitionList.
type Biome struct {
	// Name is the namespaced name, such as "minecraft:plains".
	Name string
	// ID is the numeric ID chunks store. Vanilla biomes keep BDS's; a
	// negative ID asks Freeze to pick a free one.
	ID int16
	// Temperature drives snow, rain and sky colour.
	Temperature float32
	// Downfall is how strongly rain affects colours and blocks.
	Downfall float32
	// FoliageSnow is how far foliage turns white with snow.
	FoliageSnow float32
	// Depth and Scale are the biome's legacy terrain shape values.
	Depth, Scale float32
	// MapWaterColour is the ARGB water colour on maps.
	MapWaterColour int32
	// Rain is false for dry biomes.
	Rain bool
	// Tags group biomes, such as "overworld" or "monster".
	Tags []string

	// gen is BDS's client-side generation data, kept for vanilla biomes
	// only: its strings point into the vanilla string list.
	gen protocol.Optional[protocol.BiomeChunkGeneration]
	// hasTags records whether BDS sent a tag list at all.
	hasTags bool
}

// RegisterBiome adds a biome. Registering a name that exists replaces it
// (keeping its ID when the new one has none), and the log says who
// replaced whom.
func (s *Setup) RegisterBiome(b Biome) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if !strings.Contains(b.Name, ":") {
		return fmt.Errorf("registry: biome %q: name needs a namespace", b.Name)
	}
	if old, ok := s.s.biomes[b.Name]; ok {
		s.log.Info("biome replaced", "biome", b.Name, "by", s.owner, "was", s.s.biomeOwners[b.Name])
		// A plugin's version of a vanilla biome has no generation data
		// (the field is unexported), so clients stop generating it
		// themselves.
		if b.ID < 0 {
			b.ID = old.ID
		}
	} else {
		s.s.biomeOrder = append(s.s.biomeOrder, b.Name)
	}
	if len(b.Tags) > 0 {
		b.hasTags = true
	}
	if s.owner != s.s.vanillaOwner {
		s.s.biomesChanged = true
	}
	delete(s.s.deletedBiomes, b.Name)
	s.s.biomes[b.Name] = b
	s.s.biomeOwners[b.Name] = s.owner
	return nil
}

// DeleteBiome removes a biome from the whole server. Saved chunks that
// hold it load replacement; an empty replacement means plains. Plains
// cannot be deleted.
func (s *Setup) DeleteBiome(name, replacement string) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if name == Plains {
		return fmt.Errorf("registry: delete biome %s: it cannot be deleted", name)
	}
	old, ok := s.s.biomes[name]
	if !ok {
		return fmt.Errorf("registry: delete biome %s: not registered", name)
	}
	if replacement == "" {
		replacement = Plains
	}
	delete(s.s.biomes, name)
	s.s.deletedBiomes[name] = deletedBiome{id: old.ID, replacement: replacement}
	s.s.biomesChanged = true
	s.log.Info("biome deleted", "biome", name, "by", s.owner, "replacement", replacement)
	return nil
}

type deletedBiome struct {
	id          int16
	replacement string
}

// Biomes is the frozen biome table. Its methods are safe for use by many
// goroutines at once.
type Biomes struct {
	biomes   []Biome
	byName   map[string]int
	byID     map[int16]int
	replaced map[int16]int16
	plains   int16
	pk       *packet.BiomeDefinitionList
}

// freezeBiomes builds the biome table and its packet. Unchanged vanilla
// biomes send exactly the packet BDS sends, client-side generation data
// included.
func (s *Setup) freezeBiomes() (*Biomes, error) {
	bs := &Biomes{byName: map[string]int{}, byID: map[int16]int{}, replaced: map[int16]int16{}}
	used := map[int16]bool{}
	for _, name := range s.s.biomeOrder {
		if b, ok := s.s.biomes[name]; ok && b.ID >= 0 {
			used[b.ID] = true
		}
	}
	for _, d := range s.s.deletedBiomes {
		used[d.id] = true // never reuse a deleted biome's ID: saved chunks hold it
	}
	var next int16
	for _, name := range s.s.biomeOrder {
		b, ok := s.s.biomes[name]
		if !ok {
			continue
		}
		if b.ID < 0 {
			for used[next] {
				next++
				if next < 0 {
					return nil, fmt.Errorf("registry: freeze: no free biome ID for %s", name)
				}
			}
			b.ID = next
			used[next] = true
		}
		if other, dup := bs.byID[b.ID]; dup {
			return nil, fmt.Errorf("registry: freeze: biomes %s and %s share ID %d", bs.biomes[other].Name, name, b.ID)
		}
		b.Tags = slices.Clone(b.Tags)
		bs.byID[b.ID] = len(bs.biomes)
		bs.byName[name] = len(bs.biomes)
		bs.biomes = append(bs.biomes, b)
	}
	i, ok := bs.byName[Plains]
	if !ok {
		return nil, fmt.Errorf("registry: freeze: %s is not registered", Plains)
	}
	bs.plains = bs.biomes[i].ID
	for name, d := range s.s.deletedBiomes {
		r, seen := d.replacement, map[string]bool{name: true}
		for {
			next, gone := s.s.deletedBiomes[r]
			if !gone || seen[r] {
				break
			}
			seen[r] = true
			r = next.replacement
		}
		j, ok := bs.byName[r]
		if !ok {
			return nil, fmt.Errorf("registry: freeze: replacement %s of deleted biome %s is not registered", r, name)
		}
		bs.replaced[d.id] = bs.biomes[j].ID
	}
	if !s.s.biomesChanged && s.s.vanillaBiomes != nil {
		bs.pk = s.s.vanillaBiomes
		return bs, nil
	}
	bs.pk = s.buildBiomePacket(bs.biomes)
	return bs, nil
}

// buildBiomePacket builds BiomeDefinitionList on top of the vanilla string
// list, so kept vanilla generation data still points at the right strings.
// Vanilla biomes are sent with ID -1, as BDS sends them: the client knows
// their IDs.
func (s *Setup) buildBiomePacket(biomes []Biome) *packet.BiomeDefinitionList {
	pk := &packet.BiomeDefinitionList{}
	index := map[string]uint16{}
	if v := s.s.vanillaBiomes; v != nil {
		pk.StringList = slices.Clone(v.StringList)
		for i, str := range pk.StringList {
			if _, ok := index[str]; !ok {
				index[str] = uint16(i) //nolint:gosec // the list is far below 65536 strings
			}
		}
	}
	intern := func(str string) uint16 {
		if i, ok := index[str]; ok {
			return i
		}
		i := uint16(len(pk.StringList)) //nolint:gosec // as above
		index[str] = i
		pk.StringList = append(pk.StringList, str)
		return i
	}
	for _, b := range biomes {
		id := b.ID
		if s.s.vanillaBiomeNames[b.Name] {
			id = -1
		}
		d := protocol.BiomeDefinition{
			NameIndex: int16(intern(b.Name)), //nolint:gosec // as above
			BiomeID:   id, Temperature: b.Temperature, Downfall: b.Downfall, FoliageSnow: b.FoliageSnow,
			Depth: b.Depth, Scale: b.Scale, MapWaterColour: b.MapWaterColour, Rain: b.Rain, ChunkGeneration: b.gen,
		}
		if b.hasTags {
			tags := make([]uint16, len(b.Tags))
			for i, t := range b.Tags {
				tags[i] = intern(t)
			}
			d.Tags = protocol.Option(tags)
		}
		pk.BiomeDefinitions = append(pk.BiomeDefinitions, d)
	}
	return pk
}

// Count returns the number of biomes.
func (bs *Biomes) Count() int { return len(bs.biomes) }

// ByName returns a biome by name.
func (bs *Biomes) ByName(name string) (Biome, bool) {
	i, ok := bs.byName[name]
	if !ok {
		return Biome{}, false
	}
	return bs.biomes[i], true
}

// ByID returns a biome by numeric ID.
func (bs *Biomes) ByID(id int16) (Biome, bool) {
	i, ok := bs.byID[id]
	if !ok {
		return Biome{}, false
	}
	return bs.biomes[i], true
}

// Resolve returns the ID a saved biome ID loads as: itself if registered,
// the replacement of a deleted biome, or plains for any other.
func (bs *Biomes) Resolve(id int16) int16 {
	if _, ok := bs.byID[id]; ok {
		return id
	}
	if r, ok := bs.replaced[id]; ok {
		return r
	}
	return bs.plains
}

// DefinitionPacket returns the BiomeDefinitionList packet every joining
// player gets. It is shared: do not change it.
func (bs *Biomes) DefinitionPacket() *packet.BiomeDefinitionList { return bs.pk }
