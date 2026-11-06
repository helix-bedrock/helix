package commands

import (
	"testing"

	"github.com/helix-bedrock/helix/player"
)

// TestRegister puts the built-in commands on a Setup, and a plugin can take one away.
func TestRegister(t *testing.T) {
	s, err := player.NewSetup(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(s); err != nil {
		t.Fatal(err)
	}
	m := player.Commands(s)
	if c, ok := m.Command("give"); !ok || c.Owner() != Owner {
		t.Fatalf("give: %v %v", c, ok)
	}
	if err := m.Remove("give"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Command("give"); ok {
		t.Fatal("give still there")
	}
}
