package circuit

import (
	"testing"

	"github.com/helix-bedrock/redstone"
)

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
