package helix

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The server's settings file is BDS's server.properties: key=value lines,
// # comments. Keys BDS has mean what they mean in BDS; Helix adds level-type
// and level-type-options. Keys Helix does not use yet are kept in the file
// and ignored.

// propertiesTemplate is written when the file does not exist yet: the keys
// Helix reads, with DefaultConfig's values.
const propertiesTemplate = `# Helix server settings, in BDS's server.properties format.

server-name=%s
# Used as the server name in the server list.

max-players=%d
# The most players that can be online at once.

online-mode=%t
# Players must be signed in to Xbox Live.

view-distance=%d
# The largest view distance in chunks a player gets.

tick-distance=%d
# Chunks this far from a player, in chunks, are loaded and ticked.

level-name=%s
# The world's folder in worlds/, and the name of a new world.

level-seed=
# The seed of a new world, a number. Empty means a random one. An existing world keeps its own.

level-type=
# How chunks the world has not saved are made: vanilla (Minecraft's own terrain), flat, void,
# or a type a plugin adds. Empty means what the world was made as; a new world is vanilla.
# Setting it on an existing world changes only chunks it has not saved yet.

level-type-options=
# The type's own setting. vanilla: the path of a worldgen JSON file that changes biomes,
# features, structures and terrain. flat: the layers, as level.dat's FlatWorldLayers JSON,
# such as {"biome_id":1,"block_layers":[{"block_name":"minecraft:bedrock","count":1},{"block_name":"minecraft:grass_block","count":3}],"world_version":"version.post_1_18"}
`

// LoadProperties reads the server settings file at path (server.properties)
// into c. A missing file is written with c's values first, as BDS ships its
// own. Keys the file leaves out keep c's values.
func (c *Config) LoadProperties(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own setting
	if errors.Is(err, os.ErrNotExist) {
		data = fmt.Appendf(nil, propertiesTemplate, c.Name, c.MaxPlayers, c.OnlineMode, c.ViewDistance, c.TickDistance, c.LevelName)
		if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a settings file others may read
			return fmt.Errorf("helix: write %s: %w", path, err)
		}
	} else if err != nil {
		return fmt.Errorf("helix: read %s: %w", path, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("helix: %s line %d: want key=value", path, n)
		}
		if err := c.setProperty(strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("helix: %s line %d: %w", path, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("helix: read %s: %w", path, err)
	}
	return nil
}

// setProperty applies one key of the settings file.
func (c *Config) setProperty(key, value string) error {
	var err error
	switch key {
	case "server-name":
		c.Name = value
	case "max-players":
		c.MaxPlayers, err = strconv.Atoi(value)
	case "online-mode":
		c.OnlineMode, err = strconv.ParseBool(value)
	case "view-distance":
		c.ViewDistance, err = parseInt32(value)
	case "tick-distance":
		c.TickDistance, err = parseInt32(value)
	case "level-name":
		c.LevelName = value
	case "level-seed":
		if value == "" {
			c.Seed = nil
			return nil
		}
		seed, perr := strconv.ParseInt(value, 10, 64)
		if perr != nil {
			// BDS turns a text seed into a number; how is not read yet.
			return fmt.Errorf("level-seed %q: only number seeds are supported so far", value)
		}
		c.Seed = &seed
	case "level-type":
		c.WorldType = value
	case "level-type-options":
		c.WorldTypeOptions = value
	}
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

func parseInt32(s string) (int32, error) {
	v, err := strconv.ParseInt(s, 10, 32)
	return int32(v), err
}
