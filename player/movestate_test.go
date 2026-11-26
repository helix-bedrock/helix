package player

import (
	"testing"
)

func TestExhaustionValues(t *testing.T) {
	v, err := vanillaExhaustionValues()
	if err != nil || v.Sprint != 0.1 || v.SprintJump != 0.2 || v.Mine != 0.005 {
		t.Fatalf("%+v, %v", v, err)
	}
}
