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
	publicIP := flag.String("public-ip", "", "this network's public IP, so players outside it can join; forward TCP and UDP 19132 on the router")
	udpPorts := flag.String("udp-ports", "", "BDS's server-udp-ports: each connection takes its own UDP port from these, such as 19133-19200 or 19133-19200:29133-29200 for public:local; forward them on the router")
	tickDistance := flag.Int("tick-distance", int(helix.DefaultConfig().TickDistance), "radius in chunks kept loaded and ticked around each player, BDS's tick-distance")
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
	conf.NetherNet.PublicIP = *publicIP
	if *udpPorts != "" {
		conf.NetherNet.UDPPort = 0
		conf.NetherNet.UDPPorts = *udpPorts
	}
	conf.TickDistance = int32(*tickDistance) //nolint:gosec // a small setting, checked by helix.New
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
