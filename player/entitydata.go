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
