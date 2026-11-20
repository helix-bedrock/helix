package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// Aim check: Helix's own rule, beyond BDS. Kill aura hits whoever is in range, wherever the player
// looks. An attack counts only when the target is where the player looked in one of its last few
// inputs. Mouse and gamepad players hit what is under the crosshair, so the look must pass
// through the target's box (grown for lag and third person); touch players tap anywhere on
// screen, so the target only has to be in front of them.

const (
	// aimKeep is how many of the latest input rotations an attack may match: the attack packet
	// and the input that turned toward the target do not always arrive in order.
	aimKeep = 3
	// aimGrow is how far the box is grown on every side for crosshair players.
	aimGrow = 0.5
	// aimCone is the widest angle, in degrees, from the look to the nearest point of the box for
	// touch players: past what any field of view shows.
	aimCone = 80
)

// aimState is the player's latest input rotations and how it inputs.
type aimState struct {
	rots [aimKeep]world.Rotation
	next int
	mode uint32
}

// record keeps the rotation and input mode of one input.
func (a *aimState) record(rot world.Rotation, mode uint32) {
	a.rots[a.next] = rot
	a.next = (a.next + 1) % aimKeep
	a.mode = mode
}

// aimsAt reports whether the player looked at the box lo..hi in one of its latest inputs.
func (p *Player) aimsAt(lo, hi mgl32.Vec3) bool {
	eyes := p.entity.Position()
	crosshair := p.aim.mode == packet.InputModeMouse || p.aim.mode == packet.InputModeGamePad
	if crosshair {
		g := mgl32.Vec3{aimGrow, aimGrow, aimGrow}
		lo, hi = lo.Sub(g), hi.Add(g)
	}
	if insideBox(eyes, lo, hi) {
		return true
	}
	nearest := mgl32.Vec3{clamp32(eyes[0], lo[0], hi[0]), clamp32(eyes[1], lo[1], hi[1]), clamp32(eyes[2], lo[2], hi[2])}
	to := nearest.Sub(eyes).Normalize()
	minCos := float32(math.Cos(aimCone * math.Pi / 180))
	for _, rot := range p.aim.rots {
		dir := lookDirection(rot)
		if crosshair && rayHitsBox(eyes, dir, lo, hi) {
			return true
		}
		if !crosshair && dir.Dot(to) >= minCos {
			return true
		}
	}
	return false
}

func clamp32(v, lo, hi float32) float32 { return min(max(v, lo), hi) }

// rayHitsBox reports whether the ray from origin along dir enters lo..hi in front of origin
// (the slab test).
func rayHitsBox(origin, dir, lo, hi mgl32.Vec3) bool {
	tNear, tFar := math.Inf(-1), math.Inf(1)
	for i := range 3 {
		d := float64(dir[i])
		if math.Abs(d) < 1e-9 {
			if origin[i] < lo[i] || origin[i] > hi[i] {
				return false
			}
			continue
		}
		t1 := float64(lo[i]-origin[i]) / d
		t2 := float64(hi[i]-origin[i]) / d
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		tNear, tFar = max(tNear, t1), min(tFar, t2)
		if tNear > tFar {
			return false
		}
	}
	return tFar >= 0
}
