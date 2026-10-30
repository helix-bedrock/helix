package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/protocol/packet"
)

// EntityType is an entity type: what the client learns at join, and its
// behaviour pack definition (components, component groups, events), which
// the entity library runs. The engine's own hooks (spawn, hurt, death and
// drops, interact, tick) come with players and entities.
type EntityType struct {
	// Name is the namespaced identifier, such as "minecraft:zombie".
	Name string
	// BaseID is the identifier the client falls back to ("bid"), usually
	// "minecraft:" for vanilla types.
	BaseID string
	// RuntimeID is the numeric type ID ("rid"). Vanilla types keep BDS's;
	// 0 asks Freeze to pick a free one.
	RuntimeID int32
	// HasSpawnEgg reports whether the type has a spawn egg.
	HasSpawnEgg bool
	// Summonable reports whether /summon may create it.
	Summonable bool
	// Definition is the behaviour pack entity file, as JSON. The player is
	// the minecraft:player type.
	Definition json.RawMessage
}

// RegisterEntityType adds an entity type. Registering a name that exists
// replaces it (keeping its runtime ID when the new one has none), and the
// log says who replaced whom.
func (s *Setup) RegisterEntityType(t EntityType) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if !strings.Contains(t.Name, ":") {
		return fmt.Errorf("registry: entity type %q: name needs a namespace", t.Name)
	}
	if old, ok := s.s.entities[t.Name]; ok {
		s.log.Info("entity type replaced", "entity", t.Name, "by", s.owner, "was", s.s.entityOwners[t.Name])
		if t.RuntimeID == 0 {
			t.RuntimeID = old.RuntimeID
		}
	} else {
		s.s.entityOrder = append(s.s.entityOrder, t.Name)
	}
	if s.owner != s.s.vanillaOwner {
		s.s.entitiesChanged = true
	}
	delete(s.s.deletedEntities, t.Name)
	s.s.entities[t.Name] = t
	s.s.entityOwners[t.Name] = s.owner
	return nil
}

// DeleteEntityType removes an entity type from the whole server. The
// player cannot be deleted.
func (s *Setup) DeleteEntityType(name string) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if name == Player {
		return fmt.Errorf("registry: delete entity type %s: it cannot be deleted", name)
	}
	if _, ok := s.s.entities[name]; !ok {
		return fmt.Errorf("registry: delete entity type %s: not registered", name)
	}
	delete(s.s.entities, name)
	s.s.deletedEntities[name] = true
	s.s.entitiesChanged = true
	s.log.Info("entity type deleted", "entity", name, "by", s.owner)
	return nil
}

// Player is the player's entity type.
const Player = "minecraft:player"

// EntityTypes is the frozen entity type table. Its methods are safe for
// use by many goroutines at once.
type EntityTypes struct {
	types  []EntityType
	byName map[string]int
	pk     *packet.AvailableActorIdentifiers
}

// identifier is one entry of AvailableActorIdentifiers, in the key order
// BDS writes (sorted).
type identifier struct {
	BaseID      string `nbt:"bid"`
	HasSpawnEgg bool   `nbt:"hasspawnegg"`
	ID          string `nbt:"id"`
	RuntimeID   int32  `nbt:"rid"`
	Summonable  bool   `nbt:"summonable"`
}

// freezeEntityTypes builds the entity type table and its packet. When no
// plugin changed entity types, the packet is BDS's own.
func (s *Setup) freezeEntityTypes() (*EntityTypes, error) {
	et := &EntityTypes{byName: map[string]int{}}
	var next int32 = 1
	for _, name := range s.s.entityOrder {
		if t, ok := s.s.entities[name]; ok && t.RuntimeID >= next {
			next = t.RuntimeID + 1
		}
	}
	ids := map[int32]string{}
	for _, name := range s.s.entityOrder {
		t, ok := s.s.entities[name]
		if !ok {
			continue
		}
		if t.RuntimeID == 0 {
			t.RuntimeID, next = next, next+1
		}
		if other, dup := ids[t.RuntimeID]; dup {
			return nil, fmt.Errorf("registry: freeze: entity types %s and %s share runtime ID %d", other, name, t.RuntimeID)
		}
		ids[t.RuntimeID] = name
		t.Definition = bytes.Clone(t.Definition)
		et.byName[name] = len(et.types)
		et.types = append(et.types, t)
	}
	if _, ok := et.byName[Player]; !ok {
		return nil, fmt.Errorf("registry: freeze: %s is not registered", Player)
	}
	if !s.s.entitiesChanged && s.s.vanillaEntities != nil {
		et.pk = s.s.vanillaEntities
		return et, nil
	}
	list := make([]identifier, len(et.types))
	for i, t := range et.types {
		list[i] = identifier{BaseID: t.BaseID, HasSpawnEgg: t.HasSpawnEgg, ID: t.Name, RuntimeID: t.RuntimeID, Summonable: t.Summonable}
	}
	b, err := nbt.MarshalEncoding(struct {
		IDList []identifier `nbt:"idlist"`
	}{list}, nbt.NetworkLittleEndian)
	if err != nil {
		return nil, fmt.Errorf("registry: freeze: actor identifiers: %w", err)
	}
	et.pk = &packet.AvailableActorIdentifiers{SerialisedEntityIdentifiers: b}
	return et, nil
}

// Count returns the number of entity types.
func (et *EntityTypes) Count() int { return len(et.types) }

// ByName returns an entity type by identifier.
func (et *EntityTypes) ByName(name string) (EntityType, bool) {
	i, ok := et.byName[name]
	if !ok {
		return EntityType{}, false
	}
	return et.types[i], true
}

// All returns every entity type in registration order. The slice is
// shared: do not change it.
func (et *EntityTypes) All() []EntityType { return et.types }

// IdentifiersPacket returns the AvailableActorIdentifiers packet every
// joining player gets. It is shared: do not change it.
func (et *EntityTypes) IdentifiersPacket() *packet.AvailableActorIdentifiers { return et.pk }
