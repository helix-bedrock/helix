package path

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block/cube"
)

// maxTurn is how many degrees a mob turns per tick while walking a path.
const maxTurn = 90

// Navigator walks a mob along paths to where its goals send it. It belongs
// to one mob and is used from that mob's tick only.
type Navigator struct {
	// Mob holds the mob's abilities and costs. Its position fields are set
	// on every Tick.
	Mob Mob
	// FollowRange is how far a path may reach, at least 16 blocks.
	FollowRange float64
	// Reach is how close to the destination a path must end.
	Reach int
	// AvoidSun cuts paths short of positions in full sunlight.
	AvoidSun bool

	dest   mgl64.Vec3
	speed  float64
	active bool
	path   *Path

	tick, lastStuckCheck  int
	lastStuckPos          mgl64.Vec3
	timeoutNode           cube.Pos
	timeoutTimer, timeout float64
	repathCooldown        int
	ticksOnNode, lastNode int
	totalTicks            int
	pathStart             mgl64.Vec3
	stuck                 bool
	maxDistanceToWaypoint float64
}

// Steering is what the navigator wants the mob to do this tick.
type Steering struct {
	// Yaw is the direction to face, in degrees.
	Yaw float64
	// Forward is the walking speed multiplier, 0 when standing still.
	Forward float64
	// Jump asks the mob to jump.
	Jump bool
}

// MoveTo makes the mob walk to dest at speed times its movement speed.
func (n *Navigator) MoveTo(dest mgl64.Vec3, speed float64) {
	if n.active && n.dest == dest {
		n.speed = speed
		return
	}
	n.dest, n.speed, n.active = dest, speed, true
	n.path = nil
	n.lastStuckCheck = n.tick
	n.resetTimeout()
}

// SetSpeed changes the speed of the current move.
func (n *Navigator) SetSpeed(speed float64) { n.speed = speed }

// Stop ends the current move.
func (n *Navigator) Stop() {
	n.active, n.path = false, nil
	n.ticksOnNode, n.totalTicks = 0, 0
}

// Idle reports whether the mob has nowhere to go.
func (n *Navigator) Idle() bool { return !n.active }

// Stuck reports whether the last move ended because the mob got stuck.
func (n *Navigator) Stuck() bool { return n.stuck }

// Path returns the path being walked, or nil.
func (n *Navigator) Path() *Path { return n.path }

// CanReach reports whether the mob can walk to within dist of dest.
func (n *Navigator) CanReach(src Source, dest mgl64.Vec3, dist float64) bool {
	p := Find(src, &n.Mob, dest, 1, n.followRange())
	return p != nil && (p.Reaches() || p.DistToTarget() <= dist)
}

func (n *Navigator) followRange() float64 { return math.Max(n.FollowRange, 16) }

func (n *Navigator) resetTimeout() {
	n.timeoutNode, n.timeoutTimer, n.timeout, n.stuck = cube.Pos{}, 0, 0, false
}

// Tick advances the mob along its path. pos, onGround and inWater describe
// the mob now; moveSpeed is its movement speed in blocks per tick; yaw is
// where it faces.
func (n *Navigator) Tick(src Source, pos mgl64.Vec3, onGround, inWater bool, moveSpeed, yaw float64) Steering {
	n.tick++
	stand := Steering{Yaw: yaw}
	if !n.active {
		return stand
	}
	n.Mob.Position, n.Mob.OnGround, n.Mob.InWater = pos, onGround, inWater
	n.totalTicks++
	if n.repathCooldown > 0 {
		n.repathCooldown--
	}
	if n.needsNewPath() {
		dest := cube.PosFromVec3(n.dest)
		dest = Surface(src, dest, n.Mob.MinY, dest[1]+64)
		n.path = Find(src, &n.Mob, centre(dest), n.Reach, n.followRange())
		n.ticksOnNode, n.lastNode = 0, 0
		n.pathStart = pos
		n.repathCooldown = 15
	}
	if n.path == nil {
		n.Stop()
		return stand
	}
	if n.AvoidSun {
		n.trimSunlit(src, pos)
	}
	mobPos := mgl64.Vec3{pos[0], n.surfaceY(src, pos, inWater), pos[2]}
	n.detectStuck(mobPos, moveSpeed)
	if !n.active {
		return stand
	}
	p := n.path
	if p.Done() {
		n.Stop()
		return stand
	}
	if p.next == n.lastNode {
		n.ticksOnNode++
	} else {
		n.ticksOnNode, n.lastNode = 0, p.next
	}
	if n.ticksOnNode > 100 {
		n.Stop()
		return stand
	}
	if n.totalTicks%100 == 0 {
		if pos.Sub(n.pathStart).LenSqr() < 4 {
			n.Stop()
			return stand
		}
		n.pathStart = pos
	}

	next := p.Next()
	target := centre(next.Pos)
	dx, dy, dz := target[0]-pos[0], target[1]-pos[1], target[2]-pos[2]
	hSqr := dx*dx + dz*dz
	h := math.Sqrt(hSqr)
	w := n.Mob.Width
	n.maxDistanceToWaypoint = 0.75 - w*0.5
	if w > 0.75 {
		n.maxDistanceToWaypoint = w * 0.5
	}
	if !onGround && h < n.maxDistanceToWaypoint && dy < -0.5 {
		p.advance()
		return n.forward(yaw)
	}
	closeEnough := h < n.maxDistanceToWaypoint && math.Abs(dy) < 1
	cutCorner := next.Type != DangerFire && next.Type != DamageFire && next.Type != WalkableDoor && n.aheadOfNode(mobPos)
	if closeEnough || cutCorner {
		p.advance()
		return n.forward(yaw)
	}

	want := wrapDegrees(math.Atan2(dz, dx)*180/math.Pi - 90)
	turn := math.Max(-maxTurn, math.Min(maxTurn, wrapDegrees(want-yaw)))
	jumpDist := math.Max(1, w)
	return Steering{
		Yaw:     yaw + turn,
		Forward: n.speed,
		// Vanilla's step height is 0.6: anything higher needs a jump.
		Jump: (dy > 0.6 || float64(next.Pos[1]) > pos[1]) && hSqr < jumpDist*jumpDist,
	}
}

// forward is the steering for a tick in which the mob only advanced to the
// next node: it keeps walking forward.
func (n *Navigator) forward(yaw float64) Steering {
	return Steering{Yaw: yaw, Forward: n.speed}
}

func (n *Navigator) needsNewPath() bool {
	if n.path == nil {
		return true
	}
	if n.repathCooldown > 0 {
		return false
	}
	t, d := n.path.Target(), cube.PosFromVec3(n.dest)
	dx, dy, dz := float64(t[0]-d[0]), float64(t[1]-d[1]), float64(t[2]-d[2])
	threshold := math.Max(4, math.Min(16, n.path.remaining())) * 0.5
	return dx*dx+dy*dy+dz*dz > threshold*threshold
}

func (n *Navigator) surfaceY(src Source, pos mgl64.Vec3, inWater bool) float64 {
	if !inWater || !n.Mob.CanFloat {
		return math.Floor(pos[1] + 0.5)
	}
	ctx := context{src: src}
	y := int(math.Floor(pos[1]))
	for i := 0; ctx.isWater(cube.Pos{int(math.Floor(pos[0])), y, int(math.Floor(pos[2]))}); i++ {
		if i > 16 {
			return pos[1]
		}
		y++
	}
	return float64(y)
}

func (n *Navigator) detectStuck(pos mgl64.Vec3, speed float64) {
	if n.tick-n.lastStuckCheck > 100 {
		eff := speed
		if speed < 1 {
			eff = speed * speed
		}
		threshold := eff * 100 * 0.25
		if pos.Sub(n.lastStuckPos).LenSqr() < threshold*threshold {
			n.stuck = true
			n.Stop()
		} else {
			n.stuck = false
		}
		n.lastStuckCheck, n.lastStuckPos = n.tick, pos
	}
	if n.path == nil || n.path.Done() {
		return
	}
	next := n.path.Next().Pos
	if next == n.timeoutNode {
		n.timeoutTimer++
	} else {
		n.timeoutNode = next
		n.timeoutTimer = 0
		n.timeout = 0
		if speed > 0 {
			n.timeout = pos.Sub(centre(next)).Len() / speed * 20
		}
	}
	if n.timeout > 0 && n.timeoutTimer > n.timeout*3 {
		n.resetTimeout()
		n.Stop()
	}
}

// aheadOfNode reports whether the mob is already past the next node,
// heading for the one after, so it can cut the corner.
func (n *Navigator) aheadOfNode(pos mgl64.Vec3) bool {
	p := n.path
	if p.next+1 >= len(p.nodes) {
		return false
	}
	cur, next := centre(p.nodes[p.next].Pos), centre(p.nodes[p.next+1].Pos)
	if pos.Sub(cur).LenSqr() > 4 {
		return false
	}
	toCur, toNext := cur.Sub(pos), next.Sub(pos)
	if toNext.LenSqr() >= toCur.LenSqr() && toCur.LenSqr() >= 0.5 {
		return false
	}
	if toCur.Len() < 1e-5 || toNext.Len() < 1e-5 {
		return false
	}
	return toNext.Normalize().Dot(toCur.Normalize()) < 0
}

// SkyLighter gives the sky light at a position, for AvoidSun.
type SkyLighter interface {
	SkyLight(pos cube.Pos) uint8
}

func (n *Navigator) trimSunlit(src Source, pos mgl64.Vec3) {
	sl, ok := src.(SkyLighter)
	if !ok || sl.SkyLight(cube.PosFromVec3(pos.Add(mgl64.Vec3{0, 0.5}))) >= 15 {
		return
	}
	for i, node := range n.path.nodes {
		if sl.SkyLight(node.Pos) >= 15 {
			n.path.truncate(i)
			return
		}
	}
}
