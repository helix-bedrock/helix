package helix

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml"
)

// DataDir returns the plugin's own folder, plugins/<Name> by default,
// creating it if needed.
func (p *Plugin) DataDir() (string, error) {
	if p.state == nil {
		return "", errors.New("helix: plugin not loaded")
	}
	if err := os.MkdirAll(p.state.dir, 0o755); err != nil {
		return "", fmt.Errorf("helix: create data folder: %w", err)
	}
	return p.state.dir, nil
}

// Config loads the plugin's config.toml from its data folder into v, a
// pointer to a struct. If the file does not exist, it is written from v's
// current values, so set v to the defaults first:
//
//	conf := MyConfig{Radius: 100}
//	if err := p.Config(&conf); err != nil { ... }
func (p *Plugin) Config(v any) error {
	dir, err := p.DataDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config.toml")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		out, err := toml.Marshal(v)
		if err != nil {
			return fmt.Errorf("helix: encode default config: %w", err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return fmt.Errorf("helix: write default config: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("helix: read config: %w", err)
	}
	if err := toml.Unmarshal(b, v); err != nil {
		return fmt.Errorf("helix: decode %s: %w", path, err)
	}
	return nil
}
