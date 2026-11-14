package helix

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/helix-bedrock/world/convert"
	"github.com/helix-bedrock/world/hx"
)

// Helix keeps worlds in its own format (package hx). A BDS world put in the
// worlds folder is converted on start: the conversion goes to a folder of its
// own first, and once it is complete the BDS world is moved aside to
// "<name>-bds", untouched, and the Helix world takes its name. A server
// stopped halfway starts the conversion again; one stopped during the two
// renames finishes them. ExportBDS turns a Helix world back into a BDS one.

const (
	// convertingSuffix is the folder a BDS world is converted into.
	convertingSuffix = ".converting"
	// convertedMarker is written into that folder once the conversion is
	// complete, before any rename.
	convertedMarker = ".converted"
	// bdsSuffix is the name the BDS world is kept under after conversion.
	bdsSuffix = "-bds"
	// convertProgressEvery is how often a conversion logs how far it got.
	convertProgressEvery = 5 * time.Second
)

// worldKind is what a world folder holds.
type worldKind int

const (
	worldNone worldKind = iota
	worldHelix
	worldBDS
)

// kindOf tells what dir holds: a Helix world (world.hx), a BDS world (a
// LevelDB database in db), or neither.
func kindOf(dir string) (worldKind, error) {
	for _, c := range []struct {
		path string
		kind worldKind
	}{{filepath.Join(dir, hx.MetaFile), worldHelix}, {filepath.Join(dir, "db", "CURRENT"), worldBDS}} {
		_, err := os.Stat(c.path)
		if err == nil {
			return c.kind, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return worldNone, fmt.Errorf("helix: open world %q: %w", dir, err)
		}
	}
	return worldNone, nil
}

// prepareWorld makes dir a Helix world folder if it holds a BDS world,
// converting it, and finishes a conversion a stop interrupted.
func prepareWorld(log *slog.Logger, dir string) error {
	tmp := dir + convertingSuffix
	kind, err := kindOf(dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(tmp, convertedMarker)); err == nil {
		// Stopped between the renames: the BDS world may already be moved.
		return finishConversion(log, dir, tmp, kind == worldBDS)
	}
	if kind != worldBDS {
		return nil
	}
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf("helix: remove unfinished conversion %q: %w", tmp, err)
	}
	log.Info("Converting the BDS world to Helix's format; the BDS world is kept as it is", "from", dir, "to", tmp)
	start := time.Now()
	opts := convert.Options{ProgressEvery: convertProgressEvery, Progress: func(p convert.Progress) {
		log.Info("Converting world", "phase", p.Phase, "chunks", p.Chunks, "MB", p.Bytes>>20,
			"block entities", p.BlockEntities, "entities", p.Entities, "time", p.Elapsed.Round(time.Second))
	}}
	if err := convert.Import(dir, tmp, opts); err != nil {
		return fmt.Errorf("helix: convert BDS world %q: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(tmp, convertedMarker), nil, 0o644); err != nil { //nolint:gosec // a world file
		return fmt.Errorf("helix: convert BDS world %q: %w", dir, err)
	}
	log.Info("World converted", "time", time.Since(start).Round(time.Second))
	return finishConversion(log, dir, tmp, true)
}

// finishConversion moves the BDS world aside (if still there) and the
// converted world into its place.
func finishConversion(log *slog.Logger, dir, tmp string, bdsThere bool) error {
	if bdsThere {
		keep, err := freeName(dir + bdsSuffix)
		if err != nil {
			return err
		}
		if err := os.Rename(dir, keep); err != nil {
			return fmt.Errorf("helix: move the BDS world aside: %w", err)
		}
		log.Info("The BDS world is kept", "dir", keep)
	} else if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("helix: %q and the converted world %q both exist; move one away", dir, tmp)
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("helix: move the converted world in place: %w", err)
	}
	if err := os.Remove(filepath.Join(dir, convertedMarker)); err != nil {
		return fmt.Errorf("helix: %w", err)
	}
	return nil
}

// freeName returns name, or name-2, name-3... if taken.
func freeName(name string) (string, error) {
	for i := 1; ; i++ {
		n := name
		if i > 1 {
			n += "-" + strconv.Itoa(i)
		}
		_, err := os.Stat(n)
		if errors.Is(err, os.ErrNotExist) {
			return n, nil
		}
		if err != nil {
			return "", fmt.Errorf("helix: %w", err)
		}
	}
}

// ExportBDS writes the world the server is running into a new BDS world in
// dst, which must not hold a database, while the server keeps running: it
// exports the newest save round when it begins. progress, if not nil, is
// called every few seconds. Call it from any goroutine while Run runs.
func (s *Server) ExportBDS(dst string, progress func(convert.Progress)) error {
	db := s.db
	if db == nil {
		return errors.New("helix: export: no world open")
	}
	if err := db.Flush(); err != nil {
		return fmt.Errorf("helix: export: %w", err)
	}
	if err := convert.ExportWorld(db.World(), dst, convert.Options{Progress: progress, ProgressEvery: convertProgressEvery}); err != nil {
		return fmt.Errorf("helix: export: %w", err)
	}
	return nil
}

// ExportBDSWorld writes the Helix world in dir into a new BDS world in dst,
// which must not hold a database. No server may have the world open.
// progress, if not nil, is called every few seconds.
func ExportBDSWorld(dir, dst string, progress func(convert.Progress)) error {
	kind, err := kindOf(dir)
	if err != nil {
		return err
	}
	if kind != worldHelix {
		return fmt.Errorf("helix: export: %q holds no Helix world", dir)
	}
	if err := convert.Export(dir, dst, convert.Options{Progress: progress, ProgressEvery: convertProgressEvery}); err != nil {
		return fmt.Errorf("helix: export: %w", err)
	}
	return nil
}
