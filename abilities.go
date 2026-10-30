package helix

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Ability is one of a player's abilities, in BDS's order (the bit of each
// in protocol's Ability masks).
type Ability uint8

// The abilities, in BDS's order.
const (
	AbilityBuild Ability = iota
	AbilityMine
	AbilityDoorsAndSwitches
	AbilityOpenContainers
	AbilityAttackPlayers
	AbilityAttackMobs
	AbilityOperatorCommands
	AbilityTeleport
	AbilityInvulnerable
	AbilityFlying
	AbilityMayFly
	AbilityInstantBuild
	AbilityLightning
	AbilityFlySpeed
	AbilityWalkSpeed
	AbilityMuted
	AbilityWorldBuilder
	AbilityNoClip
	AbilityPrivilegedBuilder
	AbilityVerticalFlySpeed
	abilityCount
)

// isFloat reports whether the ability holds a speed rather than a flag.
func (a Ability) isFloat() bool {
	return a == AbilityFlySpeed || a == AbilityWalkSpeed || a == AbilityVerticalFlySpeed
}

// AbilityLayer is one of BDS's ability layers. A layer above the base
// overrides the base for the abilities it sets.
type AbilityLayer uint8

// The layers Helix uses; the others exist on the client.
const (
	AbilityLayerBase      AbilityLayer = protocol.AbilityLayerTypeBase
	AbilityLayerSpectator AbilityLayer = protocol.AbilityLayerTypeSpectator
	abilityLayerCount                  = protocol.AbilityLayerTypeLoadingScreen + 1
)

// Permission levels, as BDS has them: what a player may do in the world.
const (
	PermissionVisitor  = packet.PermissionLevelVisitor
	PermissionMember   = packet.PermissionLevelMember
	PermissionOperator = packet.PermissionLevelOperator
	PermissionCustom   = packet.PermissionLevelCustom
)

// Command permission levels, as BDS has them: which commands a player may
// run.
const (
	CommandPermissionAny uint8 = iota
	CommandPermissionGameDirectors
	CommandPermissionAdmin
	CommandPermissionHost
	CommandPermissionOwner
)

// Game modes, as BDS numbers them. GameModeDefault follows the world's game
// mode.
const (
	GameModeSurvival  int32 = 0
	GameModeCreative  int32 = 1
	GameModeAdventure int32 = 2
	GameModeDefault   int32 = 5
	GameModeSpectator int32 = 6
)

// abilityValue is an ability in one layer: unset, or a flag or speed.
type abilityValue struct {
	set  bool
	flag bool
	f    float32
}

// Abilities are a player's layered abilities and permission levels, kept
// as BDS keeps them. Runs: owner of the player's entity.
type Abilities struct {
	layers [abilityLayerCount][abilityCount]abilityValue
	// Permission is the player's permission level (Permission constants).
	Permission uint8
	// CommandPermission is the player's command permission level.
	CommandPermission uint8
}

// Default speeds, as BDS's base layer has them.
const (
	defaultFlySpeed         = 0.05
	defaultWalkSpeed        = 0.1
	defaultVerticalFlySpeed = 1
)

// DefaultAbilities returns BDS's abilities of a new player: every base
// ability set, build and mine allowed, the rest off, member permissions.
func DefaultAbilities() Abilities {
	var a Abilities
	for i := range abilityCount {
		a.layers[AbilityLayerBase][i].set = true
	}
	a.SetFlag(AbilityLayerBase, AbilityBuild, true)
	a.SetFlag(AbilityLayerBase, AbilityMine, true)
	a.SetSpeed(AbilityLayerBase, AbilityFlySpeed, defaultFlySpeed)
	a.SetSpeed(AbilityLayerBase, AbilityWalkSpeed, defaultWalkSpeed)
	a.SetSpeed(AbilityLayerBase, AbilityVerticalFlySpeed, defaultVerticalFlySpeed)
	a.SetPermission(PermissionMember)
	return a
}

// SetFlag sets a flag ability in a layer.
func (a *Abilities) SetFlag(l AbilityLayer, ab Ability, v bool) {
	if l >= abilityLayerCount || ab >= abilityCount || ab.isFloat() {
		return
	}
	a.layers[l][ab] = abilityValue{set: true, flag: v}
}

// SetSpeed sets a speed ability in a layer.
func (a *Abilities) SetSpeed(l AbilityLayer, ab Ability, v float32) {
	if l >= abilityLayerCount || ab >= abilityCount || !ab.isFloat() {
		return
	}
	a.layers[l][ab] = abilityValue{set: true, f: v}
}

// Unset clears every ability of a layer, so it overrides nothing.
func (a *Abilities) Unset(l AbilityLayer) {
	if l < abilityLayerCount {
		a.layers[l] = [abilityCount]abilityValue{}
	}
}

// Flag returns a flag ability: the highest layer that sets it wins, as on
// the client.
func (a *Abilities) Flag(ab Ability) bool {
	if ab >= abilityCount {
		return false
	}
	for l := abilityLayerCount - 1; l >= 0; l-- {
		if v := a.layers[l][ab]; v.set {
			return v.flag
		}
	}
	return false
}

// SetPermission sets the permission level and, as BDS does, the abilities
// it grants in the base layer. Custom leaves them as they are.
func (a *Abilities) SetPermission(level uint8) {
	a.Permission = level
	a.CommandPermission = CommandPermissionAny
	if level == PermissionOperator {
		a.CommandPermission = CommandPermissionGameDirectors
	}
	if level == PermissionCustom {
		return
	}
	member, op := level != PermissionVisitor, level == PermissionOperator
	for ab := AbilityBuild; ab <= AbilityAttackMobs; ab++ {
		a.SetFlag(AbilityLayerBase, ab, member)
	}
	a.SetFlag(AbilityLayerBase, AbilityOperatorCommands, op)
	a.SetFlag(AbilityLayerBase, AbilityTeleport, op)
}

// SetGameMode applies a game mode's abilities as BDS 1.26.60 does when a
// player's game mode is set: the spectator layer is cleared, then creative
// lets the player fly, build instantly and not be hurt, survival and
// adventure take that away, and spectator overrides the base in its own
// layer. world is the world's game mode, which GameModeDefault follows.
func (a *Abilities) SetGameMode(mode, world int32) {
	a.Unset(AbilityLayerSpectator)
	if mode == GameModeDefault {
		mode = world
	}
	switch mode {
	case GameModeCreative:
		a.SetFlag(AbilityLayerBase, AbilityMayFly, true)
		a.SetFlag(AbilityLayerBase, AbilityInstantBuild, true)
		a.SetFlag(AbilityLayerBase, AbilityInvulnerable, true)
		a.SetFlag(AbilityLayerBase, AbilityNoClip, false)
	case GameModeSurvival, GameModeAdventure:
		for _, ab := range [...]Ability{AbilityFlying, AbilityMayFly, AbilityInstantBuild, AbilityInvulnerable, AbilityNoClip} {
			a.SetFlag(AbilityLayerBase, ab, false)
		}
	case GameModeSpectator:
		for ab := AbilityBuild; ab <= AbilityAttackMobs; ab++ {
			a.SetFlag(AbilityLayerSpectator, ab, false)
		}
		a.SetFlag(AbilityLayerSpectator, AbilityFlying, true)
		a.SetFlag(AbilityLayerSpectator, AbilityMayFly, false)
		a.SetFlag(AbilityLayerSpectator, AbilityInstantBuild, false)
		a.SetFlag(AbilityLayerSpectator, AbilityInvulnerable, true)
		a.SetFlag(AbilityLayerSpectator, AbilityNoClip, true)
	}
}

// Data returns the abilities as the client gets them: every layer that
// sets something, its set abilities and the flags that are on. Speeds a
// layer does not set are sent as the defaults.
func (a *Abilities) Data(uniqueID int64) protocol.AbilityData {
	d := protocol.AbilityData{EntityUniqueID: uniqueID, PlayerPermissions: a.Permission, CommandPermissions: a.CommandPermission}
	for l := range abilityLayerCount {
		layer := protocol.AbilityLayer{
			Type: uint16(l), FlySpeed: defaultFlySpeed, WalkSpeed: defaultWalkSpeed, VerticalFlySpeed: defaultVerticalFlySpeed,
		}
		for ab, v := range a.layers[l] {
			if !v.set {
				continue
			}
			layer.Abilities |= 1 << ab
			switch Ability(ab) { //nolint:exhaustive // only speeds need their value
			case AbilityFlySpeed:
				layer.FlySpeed = v.f
			case AbilityWalkSpeed:
				layer.WalkSpeed = v.f
			case AbilityVerticalFlySpeed:
				layer.VerticalFlySpeed = v.f
			default:
				if v.flag {
					layer.Values |= 1 << ab
				}
			}
		}
		if layer.Abilities != 0 {
			d.Layers = append(d.Layers, layer)
		}
	}
	return d
}

// savedAbilities are the base abilities BDS saves in a player's
// "abilities" compound, by key.
var savedAbilities = [...]struct {
	key string
	ab  Ability
}{
	{"build", AbilityBuild}, {"mine", AbilityMine}, {"doorsandswitches", AbilityDoorsAndSwitches},
	{"opencontainers", AbilityOpenContainers}, {"attackplayers", AbilityAttackPlayers},
	{"attackmobs", AbilityAttackMobs}, {"op", AbilityOperatorCommands}, {"teleport", AbilityTeleport},
	{"invulnerable", AbilityInvulnerable}, {"flying", AbilityFlying}, {"mayfly", AbilityMayFly},
	{"instabuild", AbilityInstantBuild}, {"lightning", AbilityLightning}, {"flySpeed", AbilityFlySpeed},
	{"walkSpeed", AbilityWalkSpeed}, {"verticalFlySpeed", AbilityVerticalFlySpeed},
}

// load reads the abilities and permission levels from a player's saved
// data; what is missing keeps its default.
func (a *Abilities) load(data map[string]any) {
	if lv, ok := data["playerPermissionsLevel"].(int32); ok && lv >= 0 && lv <= PermissionCustom {
		a.SetPermission(uint8(lv))
	}
	if lv, ok := data["permissionsLevel"].(int32); ok && lv >= 0 && lv <= 0xff {
		a.CommandPermission = uint8(lv)
	}
	saved, ok := data["abilities"].(map[string]any)
	if !ok {
		return
	}
	for _, s := range savedAbilities {
		switch v := saved[s.key].(type) {
		case uint8:
			a.SetFlag(AbilityLayerBase, s.ab, v != 0)
		case float32:
			a.SetSpeed(AbilityLayerBase, s.ab, v)
		}
	}
}

// save writes the base abilities and permission levels into a player's
// saved data, as BDS writes them.
func (a *Abilities) save(data map[string]any) {
	saved, ok := data["abilities"].(map[string]any)
	if !ok {
		saved = map[string]any{}
		data["abilities"] = saved
	}
	for _, s := range savedAbilities {
		v := a.layers[AbilityLayerBase][s.ab]
		if s.ab.isFloat() {
			saved[s.key] = v.f
			continue
		}
		var b uint8
		if v.flag {
			b = 1
		}
		saved[s.key] = b
	}
	data["playerPermissionsLevel"] = int32(a.Permission)
	data["permissionsLevel"] = int32(a.CommandPermission)
}
