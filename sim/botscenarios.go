package sim

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/protocol"
)

const (
	// walkSpeed is a sprinting player's speed in blocks per tick; turnOdds one in how many
	// ticks a walker picks a new heading.
	walkSpeed = 0.28
	turnOdds  = 40
	// breakCell is the side of each breaking bot's own patch of ground, breakReach how far
	// from its middle it digs, breakDepth how many layers down.
	breakCell  = 6
	breakReach = 2
	breakDepth = 3
	// breakEvery is how many ticks a creative breaker waits between blocks: two a second.
	breakEvery = 10
	// faceUp is the top face of a block.
	faceUp = 1
	// explorerSpeed is how fast explorers fly, blocks per tick (an elytra's pace),
	// explorerRange how far out they go before turning back, explorerHeight their height.
	explorerSpeed  = 1.5
	explorerRange  = 250
	explorerHeight = 140
)

// botScenarios are the scenarios with bots.
func botScenarios() []Scenario {
	return []Scenario{
		{Name: "players", About: "n bots sprinting about the area, sending input 20 times a second", Unit: "bots", Bot: walker},
		{Name: "breakers", About: "n creative bots each breaking two blocks a second on its own patch", Unit: "bots", Bot: creativeBreaker, BotMode: creative},
		{Name: "miners", About: "n survival bots each mining blocks by hand on its own patch, server-side break time", Unit: "bots", Bot: survivalMiner, BotMode: player.GameModeSurvival},
		{Name: "explorers", About: "n bots flying out from spawn over vanilla terrain and back: generation, loading, saving", Unit: "bots", Bot: explorer, BotMode: creative, WorldType: "vanilla"},
	}
}

// walker sprints about the area, turning now and then and turning back at its edge.
func walker(w *World, b *Bot, i, tick int) []protocol.PlayerBlockAction {
	if tick == 0 {
		b.MoveTo(w.spotOf(i, 3))
	}
	feet := b.Feet()
	if tick%turnOdds == i%turnOdds {
		b.heading = b.Rand.Float64() * 2 * math.Pi
	}
	next := feet.Add(mgl32.Vec3{float32(math.Cos(b.heading)) * walkSpeed, 0, float32(math.Sin(b.heading)) * walkSpeed})
	if !w.inArea(next) {
		b.heading += math.Pi
		return nil
	}
	b.MoveTo(next)
	return nil
}

// inArea reports whether pos is inside the loaded square.
func (w *World) inArea(pos mgl32.Vec3) bool {
	x0, z0, x1, z1 := w.Area()
	return pos[0] >= float32(x0) && pos[0] <= float32(x1) && pos[2] >= float32(z0) && pos[2] <= float32(z1)
}

// digTarget is the k-th block of bot i's patch, top layer first; false once it is dug out.
func (w *World) digTarget(i, k int) (protocol.BlockPos, bool) {
	side := 2*breakReach + 1
	if k >= side*side*breakDepth {
		return protocol.BlockPos{}, false
	}
	spot := w.spotOf(i, breakCell)
	layer, rest := k/(side*side), k%(side*side)
	return protocol.BlockPos{
		int32(math.Floor(float64(spot[0]))) + int32(rest%side) - breakReach, //nolint:gosec // small
		int32(w.Surface()) - 1 - int32(layer),                               //nolint:gosec // small
		int32(math.Floor(float64(spot[2]))) + int32(rest/side) - breakReach, //nolint:gosec // small
	}, true
}

// standOnPatch puts bot i above the middle of its patch, high enough to stay above the
// hole it digs.
func (w *World) standOnPatch(b *Bot, i int) {
	b.MoveTo(w.spotOf(i, breakCell).Add(mgl32.Vec3{0, 1, 0}))
}

// creativeBreaker breaks the next block of its patch every breakEvery ticks: in creative a
// block breaks as soon as breaking starts.
func creativeBreaker(w *World, b *Bot, i, tick int) []protocol.PlayerBlockAction {
	if tick == 0 {
		w.standOnPatch(b, i)
	}
	if tick%breakEvery != i%breakEvery {
		return nil
	}
	pos, ok := w.digTarget(i, tick/breakEvery)
	if !ok {
		return nil
	}
	return []protocol.PlayerBlockAction{{Action: protocol.PlayerActionStartBreak, BlockPos: pos, Face: faceUp}}
}

// survivalMiner mines its patch block by block as a survival client does: start breaking,
// then keep at it every tick until the server says the block went.
func survivalMiner(w *World, b *Bot, i, tick int) []protocol.PlayerBlockAction {
	if tick == 0 {
		w.standOnPatch(b, i)
	}
	pos, ok := w.digTarget(i, b.dug)
	if !ok {
		return nil
	}
	if b.mining && !b.Waiting(pos) {
		b.mining = false
		b.dug++
		return nil
	}
	if !b.mining {
		b.mining = true
		return []protocol.PlayerBlockAction{{Action: protocol.PlayerActionStartBreak, BlockPos: pos, Face: faceUp}}
	}
	return []protocol.PlayerBlockAction{{Action: protocol.PlayerActionContinueDestroyBlock, BlockPos: pos, Face: faceUp}}
}

// explorer flies straight out from spawn on its own heading and back, over and over, so
// the server generates, sends, saves, unloads and reloads chunks all the time.
func explorer(_ *World, b *Bot, i, tick int) []protocol.PlayerBlockAction {
	n := float64(i)
	heading := n * 2.399963 // the golden angle: headings spread evenly for any count
	trip := float64(tick) * explorerSpeed
	dist := math.Mod(trip, 2*explorerRange)
	if dist > explorerRange {
		dist = 2*explorerRange - dist
	}
	b.MoveTo(mgl32.Vec3{float32(math.Cos(heading) * dist), explorerHeight, float32(math.Sin(heading) * dist)})
	return nil
}
