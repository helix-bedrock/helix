// Command helix runs a Helix server with the default configuration.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/helix-bedrock/helix"
)

func main() {
	settings := flag.String("config", "server.properties", "the server settings file, in BDS's server.properties format; written with the defaults if missing")
	debug := flag.Bool("debug", false, "log every packet players send, and other details")
	testcmds := flag.Bool("testcmds", false, "let anyone use /gamemode and /itemtest and show a countdown over dropped items, for in-game testing")
	publicIP := flag.String("public-ip", "", "BDS's server-public-ip: this network's public IP, so players outside it can join; forward TCP 19132 and the UDP ports on the router")
	udpPort := flag.Uint("udp-port", 0, "carry every connection over this one UDP port, so one forwarded port is enough; not something BDS can do")
	portMappings := flag.String("port-mappings", "", "BDS's server-port-mappings: public:local single UDP ports")
	nethernetID := flag.String("nethernet-id", "", "BDS's nethernet-id, a UUID: sign in to Microsoft's signaling service as this")
	applicationID := flag.String("application-id", "", "BDS's application-id: your Azure application, which signs in with AppCertificate.pfx")
	applicationTenantID := flag.String("application-tenant-id", "", "BDS's application-tenant-id: your Azure application's tenant")
	lan := flag.Bool("lan", true, "answer players looking for games on this network")
	udpPorts := flag.String("udp-ports", "", "BDS's server-udp-ports: each connection takes its own UDP port from these, such as 19133-19200 or 19133-19200:29133-29200 for public:local; forward them on the router")
	tickDistance := flag.Int("tick-distance", int(helix.DefaultConfig().TickDistance), "radius in chunks kept loaded and ticked around each player, BDS's tick-distance")
	flag.Parse()
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	// Ctrl-C, and on Windows Ctrl-Break and closing the window (SIGTERM), stop the server:
	// everyone is told it shut down and the world is saved. A second one kills it at once.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	conf := helix.DefaultConfig()
	conf.Log = log
	if err := conf.LoadProperties(*settings); err != nil {
		log.Error("cannot start", "err", err)
		return
	}
	conf.NetherNet.PublicIP = *publicIP
	conf.NetherNet.UDPPort = uint16(*udpPort) //nolint:gosec // a port number
	conf.NetherNet.UDPPorts = *udpPorts
	conf.NetherNet.PortMappings = *portMappings
	conf.NetherNet.LAN = *lan
	conf.NetherNet.SignInID = *nethernetID
	conf.NetherNet.ApplicationID = *applicationID
	conf.NetherNet.ApplicationTenantID = *applicationTenantID
	// The flag wins over the settings file only when given.
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "tick-distance" {
			conf.TickDistance = int32(*tickDistance) //nolint:gosec // a small setting, checked by helix.New
		}
	})
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
