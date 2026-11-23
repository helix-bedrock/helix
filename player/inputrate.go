package player

import (
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol/packet"
)

// Input rate: Helix's own rule, beyond BDS (BDS can throttle inputs, but its default is no
// limit). A client sends one input a tick, 20 a second; timer, tick desync and net skip cheats
// send more, each within every movement limit, and so move faster. Each input costs one input
// credit; credits are earned by the wall clock at a little more than 20 a second, and up to 3
// seconds of them are kept, so a client that stalled and sends its held inputs at once, or a
// server tick that ran late, loses nothing. An input with no credit is dropped whole and the
// client is corrected back to where the server has the player.

const (
	// inputRate is how many inputs a second a player earns: 20, and a little for a client clock
	// running fast.
	inputRate = 20.5
	// inputBank is the most input credits a player keeps.
	inputBank = 60
)

// inputClock is a player's input credits. Region tick.
type inputClock struct {
	credits float64
	last    time.Time
	// dropped counts the inputs dropped since the last log.
	dropped int32
}

// earnInputs adds the credits earned since the last tick. Region tick.
func (p *Player) earnInputs(now time.Time) {
	c := &p.move.clock
	if c.last.IsZero() {
		c.credits, c.last = inputBank, now
		return
	}
	c.credits = min(c.credits+now.Sub(c.last).Seconds()*inputRate, inputBank)
	c.last = now
}

// spendInput takes one input credit, reporting false when there is none: the input is dropped
// and the client corrected to where the server has the player.
func (p *Player) spendInput(pk *packet.PlayerAuthInput) bool {
	c := &p.move.clock
	if c.credits >= 1 {
		c.credits--
		return true
	}
	c.dropped++
	if c.dropped == 1 || c.dropped%inputBank == 0 {
		p.s.log.Debug("input too fast dropped", "name", p.name, "dropped", c.dropped)
	}
	if p.entity != nil {
		rot := p.entity.Rotation()
		p.send(&packet.CorrectPlayerMovePrediction{
			PredictionType: packet.PredictionTypePlayer, Position: p.entity.Position(),
			Rotation: mgl32.Vec2{rot.Pitch, rot.Yaw}, OnGround: p.move.onGround, Tick: pk.Tick,
		})
	}
	return false
}
