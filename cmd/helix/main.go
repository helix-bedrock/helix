// Command helix runs a Helix server with the default configuration.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	"github.com/helix-bedrock/helix"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	conf := helix.DefaultConfig()
	conf.Log = log
	srv, err := helix.New(conf)
	if err != nil {
		log.Error("cannot start", "err", err)
		return
	}
	if err := srv.Run(ctx); err != nil {
		log.Error("server stopped", "err", err)
	}
}
