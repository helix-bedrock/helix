package helix

import (
	"testing"

	"github.com/helix-bedrock/protocol"
)

// TestAbilities checks the abilities against BDS 1.26.60: a creative
// operator who was flying gets exactly what BDS sent such a player.
func TestAbilities(t *testing.T) {
	a := DefaultAbilities()
	a.SetGameMode(GameModeSurvival, GameModeSurvival)
	d := a.Data(1)
	if len(d.Layers) != 1 || d.Layers[0].Abilities != protocol.AbilityCount-1 || d.Layers[0].Values != 63 ||
		d.PlayerPermissions != PermissionMember || d.CommandPermissions != CommandPermissionAny {
		t.Fatalf("survival member %+v", d)
	}

	a = DefaultAbilities()
	a.load(map[string]any{"playerPermissionsLevel": int32(2), "permissionsLevel": int32(1),
		"abilities": map[string]any{"flying": uint8(1), "flySpeed": float32(0.05)}})
	a.SetGameMode(GameModeDefault, GameModeCreative)
	d = a.Data(1)
	l := d.Layers[0]
	if len(d.Layers) != 1 || d.PlayerPermissions != 2 || d.CommandPermissions != 1 || l.Type != protocol.AbilityLayerTypeBase ||
		l.Abilities != 1048575 || l.Values != 4095 || l.FlySpeed != 0.05 || l.VerticalFlySpeed != 1 || l.WalkSpeed != 0.1 {
		t.Fatalf("creative operator %+v", d)
	}

	a.SetGameMode(GameModeSpectator, GameModeCreative)
	if d = a.Data(1); len(d.Layers) != 2 || d.Layers[1].Type != protocol.AbilityLayerTypeSpectator || a.Flag(AbilityBuild) ||
		!a.Flag(AbilityNoClip) || !a.Flag(AbilityFlying) {
		t.Fatalf("spectator %+v", d)
	}
	a.SetGameMode(GameModeSurvival, GameModeCreative)
	if len(a.Data(1).Layers) != 1 || a.Flag(AbilityFlying) || !a.Flag(AbilityBuild) {
		t.Fatal("spectator layer left after survival")
	}

	saved := map[string]any{}
	a.save(saved)
	b := DefaultAbilities()
	b.load(saved)
	b.SetGameMode(GameModeSurvival, GameModeSurvival)
	if a.Data(1).Layers[0] != b.Data(1).Layers[0] {
		t.Fatal("abilities changed through a save")
	}
}

func TestAttributes(t *testing.T) {
	a := DefaultAttributes()
	a.load(map[string]any{"Attributes": []any{
		map[string]any{"Name": "minecraft:player.level", "Base": float32(0), "Current": float32(6)},
		map[string]any{"Name": "minecraft:health", "Base": float32(20)},
	}})
	l := a.list()
	if len(l) != 17 || l[0].Name != "minecraft:health" || l[0].Value != 20 || l[15].Value != 6 || l[13].Default != 5 {
		t.Fatalf("attributes %+v", l)
	}
	saved := map[string]any{}
	def := DefaultAttributes()
	def.save(saved)
	if list, ok := saved["Attributes"].([]any); !ok || len(list) != 8 {
		t.Fatalf("saved %v", saved["Attributes"])
	}
}
