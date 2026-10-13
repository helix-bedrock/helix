package main

import (
	"fmt"
	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/server"
	"github.com/helix-bedrock/helix/server/player/chat"
	"github.com/pelletier/go-toml"
	"log/slog"
	"math/rand/v2"
	"os"
)

func main() {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	chat.Global.Subscribe(chat.StdoutSubscriber{})
	conf, err := readConfig(slog.Default())
	if err != nil {
		panic(err)
	}

	srv, err := helix.New(helix.Config{Server: conf})
	if err != nil {
		slog.Error("some plugins did not load", "err", err)
	}
	srv.CloseOnProgramEnd()
	go srv.Console(os.Stdin, os.Stdout)
	srv.Run()
}

// readConfig reads the configuration from the config.toml file, or creates the
// file if it does not yet exist.
func readConfig(log *slog.Logger) (server.Config, error) {
	c := server.DefaultConfig()
	var zero server.Config
	if _, err := os.Stat("config.toml"); os.IsNotExist(err) {
		c.World.Seed = rand.Int64()
		data, err := toml.Marshal(c)
		if err != nil {
			return zero, fmt.Errorf("encode default config: %v", err)
		}
		if err := os.WriteFile("config.toml", data, 0644); err != nil {
			return zero, fmt.Errorf("create default config: %v", err)
		}
		return c.Config(log)
	}
	data, err := os.ReadFile("config.toml")
	if err != nil {
		return zero, fmt.Errorf("read config: %v", err)
	}
	if err := toml.Unmarshal(data, &c); err != nil {
		return zero, fmt.Errorf("decode config: %v", err)
	}
	// Write settings added since the file was made, such as a new world
	// seed, back to it, so they stay the same on the next start.
	if tree, err := toml.LoadBytes(data); err == nil && !tree.Has("World.Seed") {
		c.World.Seed = rand.Int64()
		if out, err := toml.Marshal(c); err == nil {
			if err := os.WriteFile("config.toml", out, 0644); err != nil {
				log.Error("update config.toml", "err", err)
			}
		}
	}
	return c.Config(log)
}
