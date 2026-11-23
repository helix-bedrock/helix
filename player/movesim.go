package player

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/physics"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// The server's simulation of the player's movement, Helix's own work toward BDS's
// server-authoritative movement. Step 1, shadow mode: each input moves a copy of the player
// with the same physics mobs run (the entity library's, matched to BDS), driven by the
// input's move vector, look, sprint, sneak and jump, the movement attribute (Speed and
// Slowness in it), Jump Boost, Levitation, Slow Falling, Depth Strider and Soul Speed. Where
// the client says it ended up is compared with where the copy did, and a difference is
// logged at debug level ("move sim differs"); nothing is corrected. The copy then takes the
// client's position, so each tick is compared on its own. Not simulated yet: water, lava,
// gliding, riding, flying, Riptide and server pushes; those reset the copy.

const (
	// simTolerance is the difference, in blocks, below which the client and the copy agree.
	simTolerance = 0.03
	// simWidth and simHeight are the player's box; simSneakHeight while sneaking.
	simWidth, simHeight, simSneakHeight = 0.6, 1.8, 1.5
	// simStep is the highest step the player walks up.
	simStep = 0.5625
	// simAirSpeed is the push in the air.
	simAirSpeed = 0.02
	// simReportEvery is how many inputs pass between two summaries of how well the copy kept
	// up.
	simReportEvery = 200
	// enchantDepthStrider and enchantSoulSpeed are the boot enchantments the copy reads.
	enchantDepthStrider = int16(7)
	enchantSoulSpeed    = int16(36)
)

// moveSim is the player's simulated copy. Region tick.
type moveSim struct {
	view *mob.PhysicsView
	body physics.Body
	// live is set while the copy follows the player.
	live bool
	// inputs and differ count the inputs compared and those that differed since the last
	// summary; worst is the biggest difference.
	inputs, differ int
	worst          float32
}

// simMove moves the player's copy by one input from from (the eyes, where the server had
// the player) and compares it with to, where the client says the player is now; the copy then
// takes accepted, where the server put the player.
func (p *Player) simMove(r *world.Region, from, to, accepted mgl32.Vec3, pk *packet.PlayerAuthInput) {
	s := &p.move.sim
	if p.s.mobs == nil {
		return
	}
	if s.view == nil {
		s.view = p.s.mobs.PhysicsView()
	}
	if p.speedFree() || p.move.speed.pushTicks > 0 {
		s.live = false
		return
	}
	w := s.view.World(r)
	b := &s.body
	if !s.live {
		p.simReset(from)
		s.live = true
	}
	p.simInputs(pk)
	b.Tick(w)
	want := feetOf(to)
	got := mgl32.Vec3{b.Pos.X, b.Pos.Y, b.Pos.Z}
	diff := got.Sub(want).Len()
	s.inputs++
	if diff > simTolerance {
		s.differ++
		s.worst = max(s.worst, diff)
		p.s.log.Debug("move sim differs", "name", p.name, "diff", diff, "client", want, "sim", got,
			"vel", b.Vel, "ground", b.OnGround, "input", pk.MoveVector, "flags", pk.InputData)
	}
	if s.inputs >= simReportEvery {
		p.s.log.Debug("move sim summary", "name", p.name, "inputs", s.inputs, "differ", s.differ, "worst", s.worst)
		s.inputs, s.differ, s.worst = 0, 0, 0
	}
	// Shadow mode: the copy goes on from where the server put the player.
	at := feetOf(accepted)
	b.Pos = path.Vec3{X: at[0], Y: at[1], Z: at[2]}
	b.Box = simBox(b.Pos, b.Height)
}

// simReset puts the copy where the player is, eyes at eyes, with the client's last speed.
func (p *Player) simReset(eyes mgl32.Vec3) {
	b := &p.move.sim.body
	feet := feetOf(eyes)
	v := p.move.velocity
	*b = physics.Body{
		Pos: path.Vec3{X: feet[0], Y: feet[1], Z: feet[2]}, Vel: path.Vec3{X: v[0], Y: v[1], Z: v[2]},
		Width: simWidth, Height: simHeight, SwimSpeed: 1, MaxAutoStep: simStep, AirSpeed: simAirSpeed,
		HasPhysics: true, JumpBoost: -1, Levitation: -1, OnGround: p.move.onGround,
	}
	b.PrevPos = b.Pos
	b.Box = simBox(b.Pos, b.Height)
}

// simInputs sets the copy's inputs for this tick from the input packet and the player's
// server state.
func (p *Player) simInputs(pk *packet.PlayerAuthInput) {
	b := &p.move.sim.body
	b.PrevPos = b.Pos
	b.Yaw = pk.Yaw
	b.Input = path.Vec3{X: pk.MoveVector[0], Z: pk.MoveVector[1]}
	b.Jumping = pk.InputData.Load(packet.InputFlagJumping) || pk.InputData.Load(packet.InputFlagStartJumping)
	sprint := p.Moving(MoveSprinting)
	b.MovementSpeed = p.attributes.get(attrMovement)
	b.AirSpeed = simAirSpeed
	if sprint {
		b.MovementSpeed *= speedSprint
		b.AirSpeed *= speedSprint
	}
	var flags path.ActorFlags
	flags.Set(physics.FlagClimb, true)
	flags.Set(physics.FlagSprinting, sprint)
	height := float32(simHeight)
	if p.Moving(MoveSneaking) {
		flags.Set(physics.FlagSneaking, true)
		height = simSneakHeight
	}
	b.Flags, b.Height = flags, height
	b.Box = simBox(b.Pos, height)
	b.JumpBoost, b.Levitation = p.simEffect(EffectJumpBoost), p.simEffect(EffectLevitation)
	_, b.SlowFalling = p.Effect(EffectSlowFalling)
	boots := p.inventory.armor[armorFeet]
	b.DepthStrider = heldEnchant(boots, enchantDepthStrider)
	b.SoulSpeed = heldEnchant(boots, enchantSoulSpeed) > 0
}

// simEffect is the amplifier of the effect, -1 without it.
func (p *Player) simEffect(id int32) int32 {
	if amp, ok := p.Effect(id); ok {
		return amp
	}
	return -1
}

// feetOf is the feet of a player whose eyes are at eyes.
func feetOf(eyes mgl32.Vec3) mgl32.Vec3 { return eyes.Sub(mgl32.Vec3{0, eyeHeight, 0}) }

// simBox is the player's box with its feet at feet.
func simBox(feet path.Vec3, height float32) path.Box {
	return path.Box{
		Min: path.Vec3{X: feet.X - simWidth/2, Y: feet.Y, Z: feet.Z - simWidth/2},
		Max: path.Vec3{X: feet.X + simWidth/2, Y: feet.Y + height, Z: feet.Z + simWidth/2},
	}
}
