package helix

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestNewWorld checks that a new world is made in WorldsDir/LevelName with
// LevelName as its name, and saved when the server closes it.
func TestNewWorld(t *testing.T) {
	conf := DefaultConfig()
	conf.WorldsDir, conf.LevelName, conf.Log = t.TempDir(), "Test level", slog.New(slog.DiscardHandler)
	s := &Server{conf: conf, log: conf.Log}
	if err := s.openWorld(); err != nil {
		t.Fatal(err)
	}
	if s.level.name != "Test level" {
		t.Fatalf("name %q, want %q", s.level.name, "Test level")
	}
	if _, ok, err := s.PlayerStore().LoadPlayer(playerIDs("a", "b", "1")); ok || err != nil {
		t.Fatalf("LoadPlayer of an unknown player: %v %v", ok, err)
	}
	if err := s.closeWorld(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"level.dat", "levelname.txt", "db"} {
		if _, err := os.Stat(filepath.Join(conf.WorldsDir, conf.LevelName, f)); err != nil {
			t.Error(err)
		}
	}
}
