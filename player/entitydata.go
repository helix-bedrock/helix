package player

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// These are the one place the engine writes an entity's data and events, so a protocol
// change to those packets is made here.

// actorData is a change of an entity's data (flags, name, fuse...), BDS's SetActorData.
func actorData(runtimeID uint64, meta protocol.EntityMetadata) *packet.SetActorData {
	return &packet.SetActorData{EntityRuntimeID: runtimeID, EntityMetadata: meta}
}

// actorEvent is something an entity does that clients show, BDS's ActorEvent.
func actorEvent(runtimeID uint64, event uint8) *packet.ActorEvent {
	return &packet.ActorEvent{EntityRuntimeID: runtimeID, EventType: event}
}

// actorEventData is an ActorEvent with its data.
func actorEventData(runtimeID uint64, event uint8, data int32) *packet.ActorEvent {
	return &packet.ActorEvent{EntityRuntimeID: runtimeID, EventType: event, EventData: data}
}

// actorDataAt is a SetActorData stamped with the entity's tick, as BDS sends a player's
// flags.
func actorDataAt(runtimeID uint64, meta protocol.EntityMetadata, tick uint64) *packet.SetActorData {
	return &packet.SetActorData{EntityRuntimeID: runtimeID, EntityMetadata: meta, Tick: tick}
}

// nameTagFlags are the actor flags that show a name tag above an entity: can show, and
// always show, as a player has.
const nameTagFlags = int64(1<<protocol.EntityDataFlagShowName | 1<<protocol.EntityDataFlagAlwaysShowName)

// showNameTag puts a name in an entity's data, shown at all times as a player's is (the
// lead's rule for every entity: BDS leaves other entities' always show data at -1). An
// empty name changes nothing. The flags key must already be in meta.
func showNameTag(meta protocol.EntityMetadata, name string) {
	if name == "" {
		return
	}
	meta[protocol.EntityDataKeyName] = name
	meta[protocol.EntityDataKeyAlwaysShowNameTag] = uint8(1)
	if f, ok := meta[protocol.EntityDataKeyFlags].(int64); ok {
		meta[protocol.EntityDataKeyFlags] = f | nameTagFlags
	}
}
