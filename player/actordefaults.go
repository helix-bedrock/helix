package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol"
)

// actorDefaults is the synched actor data every BDS 1.26.60 actor sends in its AddActor
// before its own values, as captured from BDS's AddActor of an area_effect_cloud. The
// vanilla client reads some of these even for entities that never change them: without
// them it draws a lingering cloud's particles unlike BDS. Keys with no name in the
// protocol library are given by number. A new map each call, for the caller to add to.
func actorDefaults() protocol.EntityMetadata {
	return protocol.EntityMetadata{
		protocol.EntityDataKeyStructuralIntegrity:              int32(1),
		protocol.EntityDataKeyVariant:                          int32(0),
		protocol.EntityDataKeyColorIndex:                       byte(0),
		protocol.EntityDataKeyName:                             "",
		protocol.EntityDataKeyOwner:                            int64(-1),
		protocol.EntityDataKeyAirSupply:                        int16(300),
		protocol.EntityDataKeyChargeAmount:                     byte(0),
		protocol.EntityDataKeyLeashHolder:                      int64(0),
		protocol.EntityDataKeyScale:                            float32(1),
		protocol.EntityDataKeyHasNPC:                           byte(0),
		protocol.EntityDataKeyAirSupplyMax:                     int16(300),
		protocol.EntityDataKeyMarkVariant:                      int32(0),
		protocol.EntityDataKeyContainerType:                    byte(0),
		protocol.EntityDataKeyContainerSize:                    int32(0),
		protocol.EntityDataKeyContainerStrengthModifier:        int32(0),
		protocol.EntityDataKeyWidth:                            float32(0.6),
		protocol.EntityDataKeyHeight:                           float32(1.8),
		protocol.EntityDataKeyFuseTime:                         int32(-1),
		protocol.EntityDataKeySeatOffset:                       mgl32.Vec3{},
		protocol.EntityDataKeySeatLockPassengerRotation:        byte(0),
		protocol.EntityDataKeySeatLockPassengerRotationDegrees: float32(0),
		protocol.EntityDataKeySeatRotationOffset:               byte(0),
		protocol.EntityDataKeySeatRotationOffsetDegrees:        float32(0),
		protocol.EntityDataKeyHasCommandBlock:                  byte(0),
		protocol.EntityDataKeyCommandName:                      "",
		protocol.EntityDataKeyLastCommandOutput:                "",
		protocol.EntityDataKeyTrackCommandOutput:               byte(1),
		protocol.EntityDataKeyControllingSeatIndex:             byte(0),
		protocol.EntityDataKeyStrength:                         int32(0),
		protocol.EntityDataKeyStrengthMax:                      int32(0),
		protocol.EntityDataKeyDataSpellCastingColor:            int32(0),
		protocol.EntityDataKeyDataLifetimeTicks:                int32(-1),
		protocol.EntityDataKeyAlwaysShowNameTag:                byte(255),
		protocol.EntityDataKeyColorTwoIndex:                    byte(0),
		protocol.EntityDataKeyFlagsTwo:                         int64(math.MinInt64),
		protocol.EntityDataKeyTradeTier:                        int32(0),
		protocol.EntityDataKeyMaxTradeTier:                     int32(0),
		protocol.EntityDataKeyTradeExperience:                  int32(0),
		protocol.EntityDataKeySkinID:                           int32(0),
		protocol.EntityDataKeyCommandBlockTickDelay:            int32(3),
		protocol.EntityDataKeyCommandBlockExecuteOnFirstTick:   byte(1),
		protocol.EntityDataKeyAmbientSoundInterval:             float32(8),
		protocol.EntityDataKeyAmbientSoundIntervalRange:        float32(16),
		protocol.EntityDataKeyAmbientSoundEventName:            "ambient",
		protocol.EntityDataKeyFallDamageMultiplier:             float32(1),
		protocol.EntityDataKeyLowTierCuredTradeDiscount:        int32(0),
		protocol.EntityDataKeyHighTierCuredTradeDiscount:       int32(0),
		protocol.EntityDataKeyNearbyCuredTradeDiscount:         int32(0),
		protocol.EntityDataKeyNearbyCuredDiscountTimeStamp:     int32(0),
		protocol.EntityDataKeyHitBox:                           map[string]any{},
		protocol.EntityDataKeyIsBuoyant:                        byte(0),
		protocol.EntityDataKeyFreezingEffectStrength:           float32(0),
		protocol.EntityDataKeyMovementSoundDistanceOffset:      float32(1),
		protocol.EntityDataKeyHeartbeatIntervalTicks:           int32(20),
		protocol.EntityDataKeyHeartbeatSoundEvent:              int32(615),
		protocol.EntityDataKeyFilteredName:                     "",
		protocol.EntityDataKeySeatThirdPersonCameraRadius:      float32(0),
		protocol.EntityDataKeySeatCameraRelaxDistanceSmoothing: float32(0),
		protocol.EntityDataKeyAimAssistPriorityPresetID:        int32(-1),
		protocol.EntityDataKeyAimAssistPriorityCategoryID:      int32(-1),
		protocol.EntityDataKeyAimAssistPriorityActorID:         int32(-1),
		protocol.EntityDataKeyArrowShooterID:                   int64(0),
		actorDataKeyNameplateDistance:                          float32(64),
	}
}

// actorDataKeyNameplateDistance is key 140, which BDS 1.26.60 sends as a float (64); the
// protocol library's name for 140 does not line up with it.
const actorDataKeyNameplateDistance = 140
