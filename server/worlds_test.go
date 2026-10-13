package server

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/helix-bedrock/helix/server/world"
)

func TestLoadAndUnloadWorld(t *testing.T) {
	srv := Config{Log: slog.New(slog.DiscardHandler)}.New()
	defer srv.close()
	w, err := srv.LoadWorld("arena", WorldConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := srv.WorldByName("arena"); !ok || got != w {
		t.Fatal("WorldByName does not find the loaded world")
	}
	if len(srv.Worlds()) != 4 {
		t.Fatalf("%d worlds, want 4", len(srv.Worlds()))
	}
	if _, err := srv.LoadWorld("arena", WorldConfig{}); !errors.Is(err, ErrWorldExists) {
		t.Fatalf("loading twice: %v", err)
	}
	if _, err := srv.LoadWorld("nether", WorldConfig{}); !errors.Is(err, ErrWorldExists) {
		t.Fatalf("loading over the nether: %v", err)
	}
	// The world runs on the server's pool.
	if _, err := world.Call(t.Context(), w, func(tx *world.Tx) (world.Block, error) { return tx.Block([3]int{0, 0, 0}), nil }); err != nil {
		t.Fatal(err)
	}
	if err := srv.UnloadWorld("arena"); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.WorldByName("arena"); ok {
		t.Fatal("world still known after unloading")
	}
	if err := srv.UnloadWorld("overworld"); !errors.Is(err, ErrNoWorld) {
		t.Fatalf("unloading the overworld: %v", err)
	}
}
