package mob

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity/path"
)

// Swimming and flying mobs do not use the walking pathfinder: they steer
// straight at a point in water or air (Helix). Vanilla has pathfinders for
// both; porting Pumpkin's swim and fly node evaluators is still to do.

// steer moves a swimming or flying mob towards its destination.
func (m *Mob) steer() {
	s := m.s
	if s.dest == nil {
		return
	}
	d := s.dest.Sub(m.data.Pos)
	if d.Len() < 1 || (s.spec.swims && !s.inWater) {
		s.dest = nil
		return
	}
	want := d.Normalize().Mul(s.directSpeed)
	// Ease the velocity towards the wanted one so turns look smooth.
	m.data.Vel = m.data.Vel.Add(want.Sub(m.data.Vel).Mul(0.15))
	yaw := math.Atan2(d[2], d[0])*180/math.Pi - 90
	m.data.Rot = cube.Rotation{turn(m.data.Rot.Yaw(), yaw, 20), m.data.Rot.Pitch()}
}

// directGoal wanders a swimming or flying mob: Pumpkin's
// SwimAroundGoal and WaterAvoidingRandomFlyingGoal, driven by BDS's
// random_swim, swim_wander, random_hover, random_fly and float_wander.
type directGoal struct {
	speed    float64
	xz, y    int
	interval int
	water    bool
}

func (*directGoal) Controls() Control { return Move }
func (g *directGoal) CanStart(m *Mob) bool {
	if g.interval > 0 && m.s.rand.IntN(g.interval) != 0 {
		return false
	}
	if g.water && !m.s.inWater {
		return false
	}
	for range 10 {
		r := m.s.rand
		p := cube.PosFromVec3(m.data.Pos).Add(cube.Pos{r.IntN(2*g.xz+1) - g.xz, r.IntN(2*g.y+1) - g.y, r.IntN(2*g.xz+1) - g.xz})
		if _, loaded := m.tx.BlockLoaded(p); !loaded {
			continue
		}
		if path.Solid(m.tx, p) || path.Solid(m.tx, p.Side(cube.FaceUp)) {
			continue
		}
		if _, liquid := m.tx.Liquid(p); liquid != g.water {
			continue
		}
		if !g.water && p[1] > m.tx.HighestBlock(p[0], p[2])+8 {
			// Flyers keep near the ground, as they do in vanilla.
			continue
		}
		dest := mgl64.Vec3{float64(p[0]) + 0.5, float64(p[1]) + 0.5, float64(p[2]) + 0.5}
		m.s.dest = &dest
		m.s.directSpeed = m.s.spec.directSpeed * g.speed
		return true
	}
	return false
}
func (*directGoal) CanContinue(m *Mob) bool { return m.s.dest != nil }
func (*directGoal) Start(*Mob)              {}
func (*directGoal) Stop(m *Mob)             { m.s.dest = nil }
func (*directGoal) Tick(*Mob)               {}

func newDirectGoal(b behaviourSpec) Goal {
	r := b.raw
	switch b.name {
	case "random_swim", "swim_wander":
		return &directGoal{speed: numberField(r, "speed_multiplier", 1), xz: int(numberField(r, "xz_dist", 10)),
			y: int(numberField(r, "y_dist", 7)), interval: int(numberField(r, "interval", 120)), water: true}
	case "random_hover", "random_fly", "float_wander":
		return &directGoal{speed: numberField(r, "speed_multiplier", 1), xz: int(numberField(r, "xz_dist", 8)),
			y: int(numberField(r, "y_dist", 4)), interval: int(numberField(r, "interval", 60))}
	}
	return nil
}
