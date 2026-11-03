package circuit

import (
	"testing"

	"github.com/helix-bedrock/redstone"
)

func TestKindOf(t *testing.T) {
	cases := map[string]kind{
		nameWire:                  kindWire,
		nameTorchOff:              kindTorch,
		nameLampOn:                kindLamp,
		nameRepeaterOff:           kindRepeater,
		nameCompare:               kindComparator,
		"minecraft:stone_button":  kindButton,
		"minecraft:iron_door":     kindDoor,
		"minecraft:iron_trapdoor": kindTrapdoor,
		"minecraft:fence_gate":    kindGate,
		"minecraft:stone":         kindNone,
	}
	for name, want := range cases {
		if got := kindOf(name); got != want {
			t.Errorf("%s: kind %d, want %d", name, got, want)
		}
	}
}

func TestFacesFromStates(t *testing.T) {
	if got := torchFace(map[string]any{stateTorch: "west"}); got != redstone.West {
		t.Errorf("torch on its west: face %d", got)
	}
	if got := torchFace(map[string]any{stateTorch: "top"}); got != redstone.Down {
		t.Errorf("torch on the floor: face %d", got)
	}
	if got := leverFace(map[string]any{stateLever: "east"}); got != redstone.West {
		t.Errorf("lever pointing east: face %d", got)
	}
	if got := leverFace(map[string]any{stateLever: "up_east_west"}); got != redstone.Down {
		t.Errorf("lever on the floor: face %d", got)
	}
	if got := leverFace(map[string]any{stateLever: "down_north_south"}); got != redstone.Up {
		t.Errorf("lever on the ceiling: face %d", got)
	}
}
