package helix

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol"
)

// playerWidth is the player's hitbox width in BDS 1.26.60's synched data.
const playerWidth = 0.6

// ownFlags are the actor flags BDS gives a player on joining.
const ownFlags = 1<<protocol.EntityDataFlagHasCollision | 1<<protocol.EntityDataFlagHasGravity

// ownMetadata is the synched actor data BDS 1.26.60 sends a joining player
// about itself, entry for entry, as captured from a real join. The client
// runs its own physics from it: without the gravity and collision flags it
// floats and walks through blocks. Keys are by number in the protocol
// library, whose names for some newer keys lag behind 1.26.60.
func (p *Player) ownMetadata() protocol.EntityMetadata {
	return protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:                            int64(ownFlags),
		protocol.EntityDataKeyStructuralIntegrity:              int32(1),
		protocol.EntityDataKeyVariant:                          int32(0),
		protocol.EntityDataKeyColorIndex:                       uint8(0),
		protocol.EntityDataKeyName:                             p.name,
		protocol.EntityDataKeyOwner:                            int64(-1),
		protocol.EntityDataKeyTarget:                           int64(0),
		protocol.EntityDataKeyAirSupply:                        int16(300),
		protocol.EntityDataKeyChargeAmount:                     uint8(0),
		protocol.EntityDataKeyPlayerFlags:                      uint8(0),
		protocol.EntityDataKeyPlayerIndex:                      int32(1),
		protocol.EntityDataKeyBedPosition:                      protocol.BlockPos{},
		protocol.EntityDataKeyLeashHolder:                      int64(-1),
		protocol.EntityDataKeyHasNPC:                           uint8(0),
		protocol.EntityDataKeyAirSupplyMax:                     int16(300),
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
		protocol.EntityDataKeyFlagsTwo:                         int64(35184405643264),
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
		protocol.EntityDataKeyCollisionBox:                     mgl32.Vec3{playerWidth, playerHeight, 0},
		protocol.EntityDataKeyVisibleMobEffects:                int64(0),
		protocol.EntityDataKeyFilteredName:                     "",
		protocol.EntityDataKeyEnterBedPosition:                 mgl32.Vec3{},
		protocol.EntityDataKeySeatThirdPersonCameraRadius:      float32(0),
		protocol.EntityDataKeySeatCameraRelaxDistanceSmoothing: float32(0),
		protocol.EntityDataKeyAimAssistPriorityPresetID:        int32(-1),
		protocol.EntityDataKeyAimAssistPriorityCategoryID:      int32(-1),
		protocol.EntityDataKeyAimAssistPriorityActorID:         int32(-1),
		protocol.EntityDataKeyFireworkDirection:                float32(64),
		protocol.EntityDataKeyFireworkShooterID:                uint8(0),
	}
}
