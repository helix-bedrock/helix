package circuit

import (
	"sync/atomic"

	"github.com/helix-bedrock/redstone"
)

// Limits on what a Config may say, so a bad number cannot stop a server.
const (
	// hookReachLimit is the furthest a tripwire hook may look.
	hookReachLimit = 256
	// minTicks is the shortest wait a scheduled tick can have.
	minTicks = 1
)

// Config is every number redstone runs by. [DefaultConfig] is BDS 1.26.60's; change what
// you want from there. Times are in game ticks, 20 a second. Set it on helix.Config, or
// change it while running with Server.SetRedstone.
type Config struct {
	// Interval is how many game ticks pass between evaluations of the circuits.
	Interval int
	// Rules are the circuit library's numbers: signal reach, torch burn out, rail reach
	// and how much must be loaded around a chunk.
	Rules redstone.Rules

	Hopper     HopperConfig
	CartHopper CartHopperConfig
	Dispenser  DispenserConfig
	Lamp       LampConfig
	Observer   ObserverConfig
	Plate      PlateConfig
	Target     TargetConfig
	Piston     PistonConfig
	Tripwire   TripwireConfig
	Rail       RailConfig
	Bell       BellConfig
	Crafter    CrafterConfig
	TNT        TNTConfig
}

// HopperConfig is how hoppers move items. A cooldown of 1 or less means every tick.
type HopperConfig struct {
	// MoveCooldown is how long a hopper waits after moving items between containers.
	MoveCooldown int32
	// PickupCooldown is how long a hopper waits after picking up items from the ground,
	// so pick up can be faster or slower than moving.
	PickupCooldown int32
	// FirstItemCooldown is how long a hopper given its first item waits, one tick more if
	// it has not ticked yet this tick.
	FirstItemCooldown int32
	// ItemsPerMove is how many items one move pushes on, and how many it pulls from a
	// container above. Picking up from the ground takes a whole stack whatever this says.
	ItemsPerMove int32
	// ChainDepth is how many hoppers below it one hopper ticks first.
	ChainDepth int32
	// PickupLow is how far into the block above its item box starts, from the top of the
	// hopper's own block; PickupHigh is where it ends, from the bottom.
	PickupLow, PickupHigh float32
	// PickupInset is how far that box is shrunk on every side.
	PickupInset float32
}

// CartHopperConfig is how a hopper minecart moves items.
type CartHopperConfig struct {
	// Every is how often its hopper runs.
	Every int32
	// Reach is how far outside its box it takes items lying on the ground.
	Reach float32
}

// DispenserConfig is how dispensers and droppers fire.
type DispenserConfig struct {
	// Delay is how long one waits after being powered.
	Delay int32
	// Speed is how fast an item leaves, plus up to SpeedExtra at random, and Spread the
	// random part on each axis.
	Speed, SpeedExtra, Spread float32
}

// LampConfig is how redstone lamps behave.
type LampConfig struct {
	// OffDelay is how long a lamp stays lit after losing power.
	OffDelay int32
}

// ObserverConfig is how observers behave.
type ObserverConfig struct {
	// Delay is how long before one changes, and Retry how long to try again on a tick the
	// circuits do not evaluate next.
	Delay, Retry int32
}

// PlateConfig is how pressure plates behave.
type PlateConfig struct {
	// Delay is how long a plate waits before looking again, and WeightedDelay the same
	// for a weighted plate.
	Delay, WeightedDelay int32
	// LightWeight and HeavyWeight are how many entities hold each weighted plate at full
	// power.
	LightWeight, HeavyWeight int32
}

// TargetConfig is how target blocks behave.
type TargetConfig struct {
	// Ticks is how long one stays on after a hit, and ArrowTicks the same after an arrow
	// or trident.
	Ticks, ArrowTicks int32
}

// PistonConfig is how pistons behave.
type PistonConfig struct {
	// PushLimit is the most blocks one piston moves.
	PushLimit int
	// Step is how far the arm goes per tick, above 0 up to 1.
	Step float32
}

// TripwireConfig is how tripwire behaves.
type TripwireConfig struct {
	// Delay is how long a hook or a pressed wire waits to look again.
	Delay int32
	// HookReach is the first distance a hook no longer looks at, at most 256.
	HookReach int
}

// RailConfig is how rails behave. How far power carries along them is Rules.RailReach.
type RailConfig struct {
	// DetectorDelay is how long a pressed detector rail waits to look again.
	DetectorDelay int32
}

// BellConfig is how bells behave.
type BellConfig struct {
	// RingTicks is where a bell's count stops it. A ringing bell counts two a tick.
	RingTicks int32
}

// CrafterConfig is how crafters behave.
type CrafterConfig struct {
	// ShowTicks is how long a crafter shows that it crafted.
	ShowTicks int32
}

// TNTConfig is how TNT behaves.
type TNTConfig struct {
	// Power is its explosion's radius.
	Power float32
	// Fuse is how long it burns when lit by power, fire or a player.
	Fuse int32
	// BlastFuseMin and BlastFuseMax bound the fuse of one lit by an explosion.
	BlastFuseMin, BlastFuseMax int32
}

// DefaultConfig returns BDS 1.26.60's numbers, except Rules.LoadRadius (see
// helixRules).
func DefaultConfig() Config {
	return Config{
		Interval: redstone.DefaultInterval,
		Rules:    helixRules(),
		Hopper: HopperConfig{
			MoveCooldown: 4, PickupCooldown: 4, FirstItemCooldown: 4, ItemsPerMove: 1, ChainDepth: 50,
			PickupLow: 0.625, PickupHigh: 2, PickupInset: 0.0001,
		},
		CartHopper: CartHopperConfig{Every: 4, Reach: 0.25},
		Dispenser:  DispenserConfig{Delay: 4, Speed: 0.2, SpeedExtra: 0.1, Spread: 0.0075 * 6},
		Lamp:       LampConfig{OffDelay: 4},
		Observer:   ObserverConfig{Delay: 2, Retry: 1},
		Plate:      PlateConfig{Delay: 20, WeightedDelay: 10, LightWeight: 15, HeavyWeight: 150},
		Target:     TargetConfig{Ticks: 8, ArrowTicks: 20},
		Piston:     PistonConfig{PushLimit: 12, Step: 0.5},
		Tripwire:   TripwireConfig{Delay: 10, HookReach: 42},
		Rail:       RailConfig{DetectorDelay: 20},
		Bell:       BellConfig{RingTicks: 50},
		Crafter:    CrafterConfig{ShowTicks: 6},
		TNT:        TNTConfig{Power: 4, Fuse: 80, BlastFuseMin: 10, BlastFuseMax: 40},
	}
}

// sane returns c with numbers that would stop something working brought back in range.
func (c Config) sane() Config {
	c.Interval = max(c.Interval, 1)
	c.Rules.SignalReach = max(c.Rules.SignalReach, 0)
	c.Rules.BurnOutFlips = max(c.Rules.BurnOutFlips, 1)
	c.Rules.BurnOutLimit = max(c.Rules.BurnOutLimit, c.Rules.BurnOutFlips)
	c.Rules.RailReach = max(c.Rules.RailReach, 0)
	c.Rules.LoadRadius = max(c.Rules.LoadRadius, 0)
	c.Hopper.ItemsPerMove = max(c.Hopper.ItemsPerMove, 1)
	c.Hopper.ChainDepth = max(c.Hopper.ChainDepth, 0)
	c.CartHopper.Every = max(c.CartHopper.Every, 1)
	c.Dispenser.Delay = max(c.Dispenser.Delay, minTicks)
	c.Lamp.OffDelay = max(c.Lamp.OffDelay, minTicks)
	c.Observer.Delay = max(c.Observer.Delay, minTicks)
	c.Observer.Retry = max(c.Observer.Retry, minTicks)
	c.Plate.Delay = max(c.Plate.Delay, minTicks)
	c.Plate.WeightedDelay = max(c.Plate.WeightedDelay, minTicks)
	c.Plate.LightWeight = max(c.Plate.LightWeight, 1)
	c.Plate.HeavyWeight = max(c.Plate.HeavyWeight, 1)
	c.Target.Ticks = max(c.Target.Ticks, minTicks)
	c.Target.ArrowTicks = max(c.Target.ArrowTicks, minTicks)
	c.Piston.PushLimit = max(c.Piston.PushLimit, 0)
	if c.Piston.Step <= 0 || c.Piston.Step > 1 {
		c.Piston.Step = DefaultConfig().Piston.Step
	}
	c.Tripwire.Delay = max(c.Tripwire.Delay, minTicks)
	c.Tripwire.HookReach = max(1, min(c.Tripwire.HookReach, hookReachLimit))
	c.Rail.DetectorDelay = max(c.Rail.DetectorDelay, minTicks)
	c.Bell.RingTicks = max(c.Bell.RingTicks, 1)
	c.Crafter.ShowTicks = max(c.Crafter.ShowTicks, 1)
	c.TNT.Fuse = max(c.TNT.Fuse, 0)
	c.TNT.BlastFuseMin = max(c.TNT.BlastFuseMin, 0)
	c.TNT.BlastFuseMax = max(c.TNT.BlastFuseMax, c.TNT.BlastFuseMin)
	return c
}

// Settings holds the Config in use. What it hands out never changes, so the regions read
// it on their own goroutines while anything sets a new one. Safe for use by many
// goroutines at once.
type Settings struct{ now atomic.Pointer[Config] }

// NewSettings returns Settings holding c.
func NewSettings(c Config) *Settings {
	s := &Settings{}
	s.Set(c)
	return s
}

// Get returns the Config in use. Do not change what it points to.
func (s *Settings) Get() *Config { return s.now.Load() }

// Set puts c in use from the next time each part reads it. Numbers that would stop
// something working are brought back in range.
func (s *Settings) Set(c Config) {
	c = c.sane()
	s.now.Store(&c)
}

// helixRules are BDS's circuit numbers with LoadRadius 0. BDS evaluates a chunk's
// components only while every chunk within 32 blocks is loaded, and keeps the chunks of
// each player's view distance loaded, so every chunk of its simulation distance (4 by
// default) passes. Helix keeps only the simulation zone loaded and releases the rest, so
// with BDS's 32 the outer two chunks of every zone never evaluated: half of any machine
// more than a chunk or two from its player stood still. With 0 a chunk evaluates while it
// is loaded, which is every chunk BDS's ticks reach; paths that run into a chunk not
// loaded already wait for it.
func helixRules() redstone.Rules {
	r := redstone.DefaultRules()
	r.LoadRadius = 0
	return r
}
