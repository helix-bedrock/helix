package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/protocol/packet"
)

// Moving is anything whose speed can be read and set: players and mobs. Pulls and pushes
// that are not a hit's knockback (a fishing rod reeling in, and later leads and the like)
// are written against it so they work on both.
type Moving interface {
	// Velocity is its speed in blocks a tick.
	Velocity() mgl32.Vec3
	// SetVelocity replaces its speed. A player's client is told, as BDS's SetActorMotion.
	SetVelocity(v mgl32.Vec3)
	// Pull is BDS's pull toward a point (see [PullVelocity]): it gets off what it rides, and
	// its speed is replaced by the pull.
	Pull(to mgl32.Vec3, scale float32)
}

var (
	_ Moving = (*Player)(nil)
	_ Moving = (*Mob)(nil)
)

// pullLift is what each block of a pull's length, square rooted, adds upward.
const pullLift = 0.08

// PullVelocity is the speed BDS's FishingHook::_pullCloser (0xC08DAA0) gives what it pulls
// from from toward to: the way there times scale, plus the square root of that step's
// length times 0.08 upward, so a long pull arcs. The fishing rod uses scale 0.1, toward one
// block above the angler's head.
func PullVelocity(from, to mgl32.Vec3, scale float32) mgl32.Vec3 {
	v := to.Sub(from).Mul(scale)
	v[1] += float32(math.Sqrt(float64(v.Len()))) * pullLift
	return v
}

// Velocity returns the player's speed as the server last knew it, in blocks a tick.
// Runs: the player's region.
func (p *Player) Velocity() mgl32.Vec3 { return p.move.velocity }

// SetVelocity replaces the player's speed and tells its client, as BDS's SetActorMotion.
// Runs: the player's region.
func (p *Player) SetVelocity(v mgl32.Vec3) {
	if p.entity == nil {
		return
	}
	p.move.velocity = v
	p.send(&packet.SetActorMotion{EntityRuntimeID: p.runtimeID, Velocity: v, Tick: p.entity.Tick()})
	p.flush()
}

// Pull takes the player off what it rides and sets its speed toward to (see
// [PullVelocity]). Runs: the player's region.
func (p *Player) Pull(to mgl32.Vec3, scale float32) {
	if p.entity == nil || p.dead() {
		return
	}
	if p.vehicle != nil {
		letOff(p.vehicle, p)
	}
	p.SetVelocity(PullVelocity(p.entity.Position(), to, scale))
}

// Velocity returns the mob's speed in blocks a tick. Runs: the mob's region.
func (m *Mob) Velocity() mgl32.Vec3 {
	v := m.m.Velocity()
	return mgl32.Vec3{v.X, v.Y, v.Z}
}

// SetVelocity replaces the mob's speed; those who see it follow its moves. Runs: the
// mob's region.
func (m *Mob) SetVelocity(v mgl32.Vec3) { m.m.SetVelocity(behavior.Vec3{X: v[0], Y: v[1], Z: v[2]}) }

// Pull sets the mob's speed toward to (see [PullVelocity]). Mobs riding something are not
// taken off yet: mobs do not ride in Helix so far. Runs: the mob's region.
func (m *Mob) Pull(to mgl32.Vec3, scale float32) {
	if m.entity == nil || m.Dead() {
		return
	}
	m.SetVelocity(PullVelocity(m.entity.Position(), to, scale))
}
