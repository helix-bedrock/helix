// Command helix runs a Helix server with the default configuration.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"

	"github.com/helix-bedrock/helix"
)

func main() {
	debug := flag.Bool("debug", false, "log every packet players send, and other details")
	testcmds := flag.Bool("testcmds", false, "let anyone use /gamemode and /itemtest and show a countdown over dropped items, for in-game testing")
	flag.Parse()
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	conf := helix.DefaultConfig()
	conf.Log = log
	var testServer atomic.Pointer[helix.Server]
	if *testcmds {
		conf.Setup = testCommands(&testServer)
	}
	srv, err := helix.New(conf)
	if err != nil {
		log.Error("cannot start", "err", err)
		return
	}
	testServer.Store(srv)
	if err := srv.Run(ctx); err != nil {
		log.Error("server stopped", "err", err)
	}
}
