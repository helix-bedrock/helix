package player

import (
	"maps"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/ride"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// listEntry is the player's PlayerList entry.
func (p *Player) listEntry() protocol.PlayerListEntry {
	return protocol.PlayerListEntry{
		ActionType: protocol.PlayerListActionAdd, UUID: p.uuid, EntityUniqueID: p.uniqueID, Username: p.name, XUID: p.xuid,
		PlayFabID: p.conn.Client().PlayFabID, BuildPlatform: int32(p.conn.Client().DeviceOS), Skin: p.skin,
	}
}

// spawnPackets show the player to another player: AddPlayer, built on the
// player's region tick. The data is a first pass, not yet checked against
// BDS's synched actor data.
func (p *Player) spawnPackets(e *world.Entity) []packet.Packet {
	pos, rot := e.Position(), e.Rotation()
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:    p.actorFlags(),
		protocol.EntityDataKeyFlagsTwo: p.actorFlagsTwo(),
		protocol.EntityDataKeyName:     p.name,
		protocol.EntityDataKeyWidth:    float32(0.6),
		protocol.EntityDataKeyHeight:   float32(1.8),
		protocol.EntityDataKeyScale:    float32(1),

		protocol.EntityDataKeyVisibleMobEffects: p.visibleEffects(),
	}
	var links []protocol.EntityLink
	if p.vehicle != nil {
		maps.Copy(meta, p.vehicle.riders().Metadata(p))
		links = []protocol.EntityLink{ride.Link(p.vehicle.rideEntity().UniqueID(), p.uniqueID, protocol.EntityLinkRider)}
	}
	return []packet.Packet{&packet.AddPlayer{
		EntityLinks: links,
		UUID:        p.uuid, Username: p.name, EntityRuntimeID: p.runtimeID, PlatformChatID: p.conn.Client().PlatformOnlineID,
		Position: world.Vec32(pos.Sub(mgl64.Vec3{0, eyeHeight, 0})), Pitch: rot.Pitch, Yaw: rot.Yaw, HeadYaw: rot.HeadYaw,
		GameType: p.gameMode, EntityMetadata: meta,
		AbilityData: p.abilities.Data(p.uniqueID), HeldItem: p.inventory.Held().network(p.s.reg),
		DeviceID: string(p.conn.Client().DeviceID), BuildPlatform: int32(p.conn.Client().DeviceOS),
	}, p.armorEquipment(), p.offhandEquipment()}
}

// broadcast sends packets to every other player online, from the global
// owner so the caller never waits on the network. Runs: anywhere.
func (p *Player) broadcast(pks ...packet.Packet) {
	g := p.s.global
	g.run(func() {
		for _, o := range g.order {
			if o != p {
				o.sendNow(pks...)
			}
		}
	})
}

// movePacket moves the player for the players who see it: MovePlayer, as
// BDS sends another player's movement.
func (p *Player) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	return &packet.MovePlayer{
		EntityRuntimeID: p.runtimeID, Position: world.Vec32(e.Position()), Pitch: rot.Pitch, Yaw: rot.Yaw, HeadYaw: rot.HeadYaw,
		Mode: packet.MoveModeNormal, OnGround: e.OnGround(), Tick: e.Tick(),
	}
}

// playerWidth is the player's hitbox width in BDS 1.26.60's synched data.
const playerWidth = 0.6

// ownFlags are the actor flags BDS gives a player on joining.
const ownFlags = 1<<protocol.EntityDataFlagHasCollision | 1<<protocol.EntityDataFlagHasGravity

// ownFlagsTwo is the second set of actor flags BDS gives a player on joining. flagsPerSet
// is how many flags each set holds.
const (
	ownFlagsTwo = 35184405643264
	flagsPerSet = 64
)

// ownMetadata is the synched actor data BDS 1.26.60 sends a joining player
// about itself, entry for entry, as captured from a real join. The client
// runs its own physics from it: without the gravity and collision flags it
// floats and walks through blocks. Keys are by number in the protocol
// library, whose names for some newer keys lag behind 1.26.60.
func (p *Player) ownMetadata() protocol.EntityMetadata {
	b := p.Breathable()
	return protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:                            int64(ownFlags),
		protocol.EntityDataKeyStructuralIntegrity:              int32(1),
		protocol.EntityDataKeyVariant:                          int32(0),
		protocol.EntityDataKeyColorIndex:                       uint8(0),
		protocol.EntityDataKeyName:                             p.name,
		protocol.EntityDataKeyOwner:                            int64(-1),
		protocol.EntityDataKeyTarget:                           int64(0),
		protocol.EntityDataKeyAirSupply:                        b.Air(),
		protocol.EntityDataKeyChargeAmount:                     uint8(0),
		protocol.EntityDataKeyPlayerFlags:                      uint8(0),
		protocol.EntityDataKeyPlayerIndex:                      int32(1),
		protocol.EntityDataKeyBedPosition:                      protocol.BlockPos{},
		protocol.EntityDataKeyLeashHolder:                      int64(-1),
		protocol.EntityDataKeyHasNPC:                           uint8(0),
		protocol.EntityDataKeyAirSupplyMax:                     b.MaxAir(),
		protocol.EntityDataKeyMarkVariant:                      int32(0),
		protocol.EntityDataKeyContainerType:                    uint8(0),
		protocol.EntityDataKeyContainerSize:                    int32(0),
		protocol.EntityDataKeyContainerStrengthModifier:        int32(0),
		protocol.EntityDataKeyFuseTime:                         int32(-1),
		protocol.EntityDataKeySeatLockPassengerRotation:        uint8(0),
		protocol.EntityDataKeySeatLockPassengerRotationDegrees: float32(0),
		protocol.EntityDataKeySeatRotationOffset:               uint8(0),
		protocol.EntityDataKeySeatRotationOffsetDegrees:        float32(0),
		protocol.EntityDataKeyHasCommandBlock:                  uint8(0),
		protocol.EntityDataKeyCommandName:                      "",
		protocol.EntityDataKeyLastCommandOutput:                "",
		protocol.EntityDataKeyTrackCommandOutput:               uint8(1),
		protocol.EntityDataKeyStrength:                         int32(0),
		protocol.EntityDataKeyStrengthMax:                      int32(0),
		protocol.EntityDataKeyDataSpellCastingColor:            int32(0),
		protocol.EntityDataKeyDataLifetimeTicks:                int32(-1),
		protocol.EntityDataKeyAlwaysShowNameTag:                uint8(1),
		protocol.EntityDataKeyColorTwoIndex:                    uint8(0),
		protocol.EntityDataKeyScore:                            "",
		protocol.EntityDataKeyAgent:                            int64(-1),
		protocol.EntityDataKeyEatingCounter:                    int32(0),
		protocol.EntityDataKeyFlagsTwo:                         int64(ownFlagsTwo),
		protocol.EntityDataKeyInteractText:                     "",
		protocol.EntityDataKeyTradeTier:                        int32(0),
		protocol.EntityDataKeyMaxTradeTier:                     int32(0),
		protocol.EntityDataKeyTradeExperience:                  int32(0),
		protocol.EntityDataKeySkinID:                           int32(0),
		protocol.EntityDataKeyCommandBlockTickDelay:            int32(3),
		protocol.EntityDataKeyCommandBlockExecuteOnFirstTick:   uint8(1),
		protocol.EntityDataKeyAmbientSoundInterval:             float32(8),
		protocol.EntityDataKeyAmbientSoundIntervalRange:        float32(16),
		protocol.EntityDataKeyAmbientSoundEventName:            "ambient",
		protocol.EntityDataKeyFallDamageMultiplier:             float32(1),
		protocol.EntityDataKeyLowTierCuredTradeDiscount:        int32(0),
		protocol.EntityDataKeyHighTierCuredTradeDiscount:       int32(0),
		protocol.EntityDataKeyNearbyCuredTradeDiscount:         int32(0),
		protocol.EntityDataKeyNearbyCuredDiscountTimeStamp:     int32(0),
		protocol.EntityDataKeyHitBox:                           map[string]any{},
		protocol.EntityDataKeyIsBuoyant:                        uint8(0),
		protocol.EntityDataKeyFreezingEffectStrength:           float32(0),
		protocol.EntityDataKeyMovementSoundDistanceOffset:      float32(1),
		protocol.EntityDataKeyHeartbeatIntervalTicks:           int32(20),
		protocol.EntityDataKeyHeartbeatSoundEvent:              int32(615),
		protocol.EntityDataKeyPlayerLastDeathPosition:          protocol.BlockPos{},
		protocol.EntityDataKeyPlayerLastDeathDimension:         int32(0),
		protocol.EntityDataKeyPlayerHasDied:                    uint8(0),
		protocol.EntityDataKeyCollisionBox:                     mgl64.Vec3{playerWidth, playerHeight, 0},
		protocol.EntityDataKeyVisibleMobEffects:                p.visibleEffects(),
		protocol.EntityDataKeyFilteredName:                     "",
		protocol.EntityDataKeyEnterBedPosition:                 mgl64.Vec3{},
		protocol.EntityDataKeySeatThirdPersonCameraRadius:      float32(0),
		protocol.EntityDataKeySeatCameraRelaxDistanceSmoothing: float32(0),
		protocol.EntityDataKeyAimAssistPriorityPresetID:        int32(-1),
		protocol.EntityDataKeyAimAssistPriorityCategoryID:      int32(-1),
		protocol.EntityDataKeyAimAssistPriorityActorID:         int32(-1),
		protocol.EntityDataKeyFireworkDirection:                float32(64),
		protocol.EntityDataKeyFireworkShooterID:                uint8(0),
	}
}
