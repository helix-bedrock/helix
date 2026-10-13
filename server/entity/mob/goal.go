package mob

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
)

// Control is a part of a mob a goal takes over while it runs.
type Control uint8

// The controls, as vanilla's Goal.Flag.
const (
	Move Control = 1 << iota
	Look
	Jump
	Target
)

// Goal is one behaviour of a mob, such as wandering or attacking. A mob
// runs the goals it can start, lower priorities first, as long as no
// running goal of a lower priority holds one of their controls. Ported
// from Pumpkin's Goal and GoalSelector, which follow vanilla.
type Goal interface {
	// Controls returns the controls the goal takes over.
	Controls() Control
	// CanStart reports whether the goal wants to start now.
	CanStart(m *Mob) bool
	// CanContinue reports whether a running goal keeps running.
	CanContinue(m *Mob) bool
	// Start, Tick and Stop are called when the goal starts, on every tick
	// it runs, and when it stops.
	Start(m *Mob)
	Tick(m *Mob)
	Stop(m *Mob)
}

type prioritized struct {
	Goal
	priority int
	running  bool
}

// selector picks which goals run.
type selector struct {
	goals     []*prioritized
	byControl [4]*prioritized
}

func (s *selector) add(priority int, g Goal) {
	s.goals = append(s.goals, &prioritized{Goal: g, priority: priority})
}

func controlIndexes(c Control) []int {
	var out []int
	for i := range 4 {
		if c&(1<<i) != 0 {
			out = append(out, i)
		}
	}
	return out
}

// canReplaceAll reports whether g may take every control it needs from the
// goals holding them: only goals of a higher priority number give way.
func (s *selector) canReplaceAll(g *prioritized) bool {
	for _, i := range controlIndexes(g.Controls()) {
		if h := s.byControl[i]; h != nil && h != g && h.priority <= g.priority {
			return false
		}
	}
	return true
}

func (s *selector) tick(m *Mob) {
	for _, g := range s.goals {
		if g.running && !g.CanContinue(m) {
			g.running = false
			g.Stop(m)
		}
	}
	for i, h := range s.byControl {
		if h != nil && !h.running {
			s.byControl[i] = nil
		}
	}
	for _, g := range s.goals {
		if g.running || !s.canReplaceAll(g) || !g.CanStart(m) {
			continue
		}
		for _, i := range controlIndexes(g.Controls()) {
			if h := s.byControl[i]; h != nil && h.running {
				h.running = false
				h.Stop(m)
			}
			s.byControl[i] = g
		}
		g.running = true
		g.Start(m)
	}
	for _, g := range s.goals {
		if g.running {
			g.Tick(m)
		}
	}
}

// look turns a mob's head towards what it looks at, Pumpkin's LookControl.
type look struct {
	pos          mgl64.Vec3
	maxYaw, maxP float64
	timer        int
}

func (l *look) at(pos mgl64.Vec3, maxYaw, maxPitch float64) {
	l.pos, l.maxYaw, l.maxP, l.timer = pos, maxYaw, maxPitch, 2
}

// tick returns the new head yaw and pitch. eye is where the mob looks from,
// body its body yaw; walking keeps the head within 75 degrees of the body.
func (l *look) tick(eye mgl64.Vec3, headYaw, pitch, body float64, walking bool) (float64, float64) {
	if l.timer > 0 {
		l.timer--
		dx, dy, dz := l.pos[0]-eye[0], l.pos[1]-eye[1], l.pos[2]-eye[2]
		if math.Abs(dx) > 1e-5 || math.Abs(dz) > 1e-5 {
			headYaw = turn(headYaw, math.Atan2(dz, dx)*180/math.Pi-90, l.maxYaw)
			pitch = turn(pitch, -math.Atan2(dy, math.Hypot(dx, dz))*180/math.Pi, l.maxP)
		}
	} else {
		headYaw = turn(headYaw, body, 10)
		pitch = turn(pitch, 0, 10)
	}
	if walking {
		headYaw = body + math.Max(-75, math.Min(75, wrap(headYaw-body)))
	}
	return headYaw, pitch
}

func turn(from, to, max float64) float64 {
	return from + math.Max(-max, math.Min(max, wrap(to-from)))
}

func wrap(d float64) float64 {
	d = math.Mod(d, 360)
	if d >= 180 {
		d -= 360
	} else if d < -180 {
		d += 360
	}
	return d
}
