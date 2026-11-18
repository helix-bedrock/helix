package main

import (
	"fmt"
	"math"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/player"
)

// cloudBombCommand drops many lingering potion clouds at once around the player, to try the
// load of thousands of clouds in one place in game. Only with -testcmds.
const cloudBombCommand = "cloudbomb"

const (
	// cloudBombDefault is how many clouds /cloudbomb makes without a count, cloudBombMax the
	// most it makes.
	cloudBombDefault = 6000
	cloudBombMax     = 20000
	// cloudBombPotion is the potion the clouds hold without one given: regeneration, which
	// does no harm while testing.
	cloudBombPotion = 28
	// cloudBombArea is the side of the square, in blocks, the clouds are spread over around
	// the player: 32 blocks keeps them in about four chunks, the worst case.
	cloudBombArea = 32
	// cloudBombProjectile is the projectile whose cloud the bomb copies.
	cloudBombProjectile = "minecraft:lingering_potion"
)

// cloudBomb runs /cloudbomb [count] [potion]: count lingering clouds of the potion (by ID)
// on a grid over a square around the player, each as a thrown lingering potion leaves it.
func cloudBomb(p *player.Player, args []string) string {
	count, potion, err := cloudBombArgs(args)
	if err != nil {
		return err.Error()
	}
	e := p.Entity()
	if e == nil || e.Region() == nil {
		return "Not in the world."
	}
	h := p.Host()
	spec, ok := h.ProjectileSpec(cloudBombProjectile)
	if !ok || spec.Cloud == nil {
		return "The lingering potion has no cloud."
	}
	if _, known := h.Potion(int32(potion)); !known {
		return fmt.Sprintf("No potion %d.", potion)
	}
	d := e.Region().Dimension()
	at := e.Position()
	side := int(math.Ceil(math.Sqrt(float64(count))))
	step := float32(cloudBombArea) / float32(side)
	for i := range count {
		off := mgl32.Vec3{float32(i%side)*step - cloudBombArea/2, 0, float32(i/side)*step - cloudBombArea/2}
		h.SpawnCloud(d, at.Add(off), int16(potion), *spec.Cloud, p) //nolint:gosec // checked against the potion table
	}
	return fmt.Sprintf("Dropped %d lingering clouds of potion %d over %d by %d blocks.", count, potion, cloudBombArea, cloudBombArea)
}

// cloudBombArgs reads the count and the potion.
func cloudBombArgs(args []string) (count, potion int, err error) {
	count, potion = cloudBombDefault, cloudBombPotion
	use := fmt.Errorf("use: /cloudbomb [count 1-%d] [potion id]", cloudBombMax)
	if len(args) > 2 {
		return 0, 0, use
	}
	if len(args) > 0 {
		if count, err = strconv.Atoi(args[0]); err != nil || count < 1 || count > cloudBombMax {
			return 0, 0, use
		}
	}
	if len(args) > 1 {
		if potion, err = strconv.Atoi(args[1]); err != nil || potion < 0 || potion > math.MaxInt16 {
			return 0, 0, use
		}
	}
	return count, potion, nil
}
