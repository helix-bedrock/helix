package mob

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/world"
)

// Movement constants of vanilla's LivingEntity.travel, as Pumpkin uses them.
const (
	gravity    = 0.08
	airDrag    = 0.98
	airSpeed   = 0.02
	stepHeight = 0.6
	// groundFactor is 0.6³: on normal ground, friction-influenced speed is
	// the mob's speed.
	groundFactor = 0.21600002
)

type moved struct {
	m        *Mob
	from     mgl64.Vec3
	fromRot  cube.Rotation
	fromHead float64
}

// Send shows the movement to the mob's viewers, if it moved or turned.
func (mv *moved) Send() {
	m := mv.m
	// Compare absolutely: mgl64's ApproxEqual is relative, so far from the
	// origin a whole walking step would count as standing still.
	if m.data.Pos.Sub(mv.from).LenSqr() < 1e-6 && m.data.Rot == mv.fromRot && m.s.headYaw == mv.fromHead {
		return
	}
	for _, v := range m.tx.Viewers(m.data.Pos) {
		v.ViewEntityMovement(m, m.data.Pos, m.data.Rot, m.s.onGround)
	}
}

// travel moves the mob by its walking input, jumping, gravity and drag.
func (m *Mob) travel(tx *world.Tx) *moved {
	s := m.s
	mv := &moved{m: m, from: m.data.Pos, fromRot: m.data.Rot, fromHead: s.headYaw}
	if s.jumpCooldown > 0 {
		s.jumpCooldown--
	}
	vel := m.data.Vel
	if s.jumping {
		switch {
		case s.inWater || s.inLava:
			vel[1] += 0.04
		case s.onGround && s.jumpCooldown == 0:
			vel[1] = math.Max(vel[1], s.spec.jumpPower)
			s.jumpCooldown = 10
		}
	}
	input := s.forward * s.speed
	yaw := m.data.Rot.Yaw()
	g := gravity
	if !s.spec.gravity && !(s.spec.swims && !s.inWater) {
		// Fish have no gravity in water but fall out of it.
		g = 0
	}

	switch {
	case s.inWater:
		vel = vel.Add(inputVelocity(input, airSpeed, yaw))
		vel = m.move(tx, vel)
		vel = vel.Mul(0.8)
		vel[1] -= g / 16
	case s.inLava:
		vel = vel.Add(inputVelocity(input, airSpeed, yaw))
		vel = m.move(tx, vel)
		vel = vel.Mul(0.5)
		vel[1] -= g / 4
	default:
		slip := 1.0
		if s.onGround {
			slip = frictionBelow(tx, m.data.Pos)
		}
		amount := input * airSpeed
		if s.onGround {
			amount = input * groundFactor / (slip * slip * slip)
		}
		vel = vel.Add(inputVelocity(input, amount, yaw))
		vel = m.move(tx, vel)
		vel[1] = (vel[1] - g) * airDrag
		vel[0] *= slip * 0.91
		vel[2] *= slip * 0.91
	}
	if math.Abs(vel[0]) < 0.003 {
		vel[0] = 0
	}
	if math.Abs(vel[2]) < 0.003 {
		vel[2] = 0
	}
	m.data.Vel = vel
	return mv
}

// inputVelocity turns forward input into velocity facing yaw, as vanilla's
// Entity.moveRelative.
func inputVelocity(input, amount, yaw float64) mgl64.Vec3 {
	if math.Abs(input) < 1e-7 {
		return mgl64.Vec3{}
	}
	f := math.Max(-1, math.Min(1, input)) * amount
	r := yaw * math.Pi / 180
	return mgl64.Vec3{-f * math.Sin(r), 0, f * math.Cos(r)}
}

func frictionBelow(tx *world.Tx, pos mgl64.Vec3) float64 {
	below := cube.PosFromVec3(pos.Sub(mgl64.Vec3{0, 0.500001}))
	if f, ok := tx.Block(below).(interface{ Friction() float64 }); ok {
		return f.Friction()
	}
	return 0.6
}

// move moves the mob by vel against block collisions, stepping up low
// ledges, and returns the velocity left after hitting things.
func (m *Mob) move(tx *world.Tx, vel mgl64.Vec3) mgl64.Vec3 {
	s := m.s
	box := m.handle.Type().BBox(m).Translate(m.data.Pos)
	d := collide(tx, box, vel)
	if s.onGround && (d[0] != vel[0] || d[2] != vel[2]) {
		// Blocked sideways on the ground: try stepping up first.
		up := collide(tx, box, mgl64.Vec3{0, stepHeight, 0})
		stepped := collide(tx, box.Translate(up), mgl64.Vec3{vel[0], 0, vel[2]})
		down := collide(tx, box.Translate(up).Translate(stepped), mgl64.Vec3{0, -up[1], 0})
		if stepped[0]*stepped[0]+stepped[2]*stepped[2] > d[0]*d[0]+d[2]*d[2] {
			d = up.Add(stepped).Add(down)
		}
	}
	m.data.Pos = m.data.Pos.Add(d)

	wasOnGround := s.onGround
	s.onGround = vel[1] < 0 && d[1] > vel[1]
	if d[1] < 0 {
		s.fallDistance -= d[1]
	}
	if s.onGround || s.inWater {
		if !wasOnGround && s.fallDistance > 3 && !s.inWater {
			m.Hurt(math.Ceil(s.fallDistance-3), entity.FallDamageSource{})
		}
		s.fallDistance = 0
	}
	if d[0] != vel[0] {
		vel[0] = 0
	}
	if d[1] != vel[1] {
		vel[1] = 0
	}
	if d[2] != vel[2] {
		vel[2] = 0
	}
	return vel
}

// collide returns how far box can move by vel before hitting blocks,
// moving along Y, then X, then Z.
func collide(tx *world.Tx, box cube.BBox, vel mgl64.Vec3) mgl64.Vec3 {
	boxes := blocksAround(tx, box.Extend(vel))
	dx, dy, dz := vel[0], vel[1], vel[2]
	for _, b := range boxes {
		dy = box.YOffset(b, dy)
	}
	box = box.Translate(mgl64.Vec3{0, dy})
	for _, b := range boxes {
		dx = box.XOffset(b, dx)
	}
	box = box.Translate(mgl64.Vec3{dx})
	for _, b := range boxes {
		dz = box.ZOffset(b, dz)
	}
	return mgl64.Vec3{dx, dy, dz}
}

func blocksAround(tx *world.Tx, box cube.BBox) []cube.BBox {
	grown := box.Grow(0.25)
	lo, hi := grown.Min(), grown.Max()
	var out []cube.BBox
	for y := int(math.Floor(lo[1])); y < int(math.Ceil(hi[1])); y++ {
		for x := int(math.Floor(lo[0])); x < int(math.Ceil(hi[0])); x++ {
			for z := int(math.Floor(lo[2])); z < int(math.Ceil(hi[2])); z++ {
				pos := cube.Pos{x, y, z}
				for _, b := range tx.Block(pos).Model().BBox(pos, tx) {
					out = append(out, b.Translate(pos.Vec3()))
				}
			}
		}
	}
	return out
}
