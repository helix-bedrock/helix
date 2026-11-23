package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Aim check: Helix's own rule, beyond BDS. Kill aura hits whoever is in range, wherever the player
// looks. An attack counts only when the target is where the player looked in one of its last few
// inputs. Mouse and gamepad players hit what is under the crosshair, so the look must pass
// through the target's box (grown for lag and third person); touch players tap anywhere on
// screen, so the target only has to be in front of them.
//
// Fake look: kill aura that turns only the rotation it sends, not the client's camera, sends an
// input that disagrees with itself. Such an input's rotation never counts for an attack (see
// lookAgrees).

const (
	// aimKeep is how many of the latest input rotations an attack may match: the attack packet
	// and the input that turned toward the target do not always arrive in order.
	aimKeep = 3
	// aimGrow is how far the box is grown on every side for crosshair players: the crosshair ray
	// from the camera and the one from the eyes differ a little. Kept small so a cheat that
	// grows hitboxes gains little; lag is allowed for by where the target was (attackFits).
	aimGrow = 0.2
	// aimCone is the widest angle, in degrees, from the look to the nearest point of the box for
	// touch players: past what any field of view shows.
	aimCone = 80
	// aimAgree is the most, in degrees, a crosshair player's interact rotation may differ from its
	// rotation: the client sends the same angles unless a server camera or VR moves one.
	aimAgree = 2
	// aimCamera is the least |cos| between the look and the camera's forward direction: first
	// person and third person behind look the same way, third person in front the opposite way.
	aimCamera = 0.9
)

// aimState is the player's latest input rotations and how it inputs.
type aimState struct {
	rots [aimKeep]world.Rotation
	// agrees is whether each kept rotation came from an input that agreed with itself.
	agrees [aimKeep]bool
	next   int
	mode   uint32
	// touch is whether the player's login says it plays by touch; checked is whether that is
	// known yet.
	touch, checked bool
	// faking is whether the last input disagreed, so the log notes only the change.
	faking bool
}

// recordAim keeps the rotation, input mode and agreement of one input.
func (p *Player) recordAim(pk *packet.PlayerAuthInput) {
	a := &p.aim
	a.mode = p.inputMode(pk.InputMode)
	agrees := p.lookAgrees(pk)
	a.rots[a.next] = world.Rotation{Pitch: pk.Pitch, Yaw: pk.Yaw, HeadYaw: pk.HeadYaw}
	a.agrees[a.next] = agrees
	a.next = (a.next + 1) % aimKeep
	if agrees == a.faking {
		a.faking = !agrees
		if !agrees {
			p.s.log.Debug("look disagrees with itself", "name", p.name, "pitch", pk.Pitch, "yaw", pk.Yaw,
				"interact pitch", pk.InteractPitch, "interact yaw", pk.InteractYaw, "camera", pk.CameraOrientation, "mode", pk.InputMode)
		}
	}
}

// inputMode is the input mode the aim check uses for an input that says mode. Input-mode lock:
// Helix's own rule, beyond BDS. Touch gets the lenient aim rules, and cheat clients claim touch in
// every input while playing with a mouse. Touch counts only for a player whose login says it plays
// by touch (its input mode, or a phone or tablet); anyone else is held to the crosshair rules.
func (p *Player) inputMode(mode uint32) uint32 {
	a := &p.aim
	if !a.checked && p.conn != nil {
		a.checked = true
		c := p.conn.Client()
		a.touch = c.CurrentInputMode == packet.InputModeTouch || c.DefaultInputMode == packet.InputModeTouch ||
			c.DeviceOS == protocol.DeviceAndroid || c.DeviceOS == protocol.DeviceIOS || c.DeviceOS == protocol.DeviceFireOS
	}
	if mode == packet.InputModeTouch && !a.touch {
		if a.mode != packet.InputModeMouse {
			p.s.log.Debug("touch input refused, login is not touch", "name", p.name)
		}
		return packet.InputModeMouse
	}
	return mode
}

// lookAgrees reports whether an input's rotation agrees with the rest of it: a real pitch, the
// interact rotation for crosshair players (touch players may interact along a tap's own line),
// and the camera's direction when the client sent one.
func (p *Player) lookAgrees(pk *packet.PlayerAuthInput) bool {
	if !finite32(pk.Pitch) || !finite32(pk.Yaw) || pk.Pitch < -90 || pk.Pitch > 90 {
		return false
	}
	if mode := p.aim.mode; mode == packet.InputModeMouse || mode == packet.InputModeGamePad {
		if absf(pk.Pitch-pk.InteractPitch) > aimAgree || absf(wrapDegrees(pk.Yaw-pk.InteractYaw)) > aimAgree {
			return false
		}
	}
	cam := pk.CameraOrientation
	if l := cam.Len(); l > 0.5 && finite32(l) {
		if absf(lookDirection(world.Rotation{Pitch: pk.Pitch, Yaw: pk.Yaw}).Dot(cam.Mul(1/l))) < aimCamera {
			return false
		}
	}
	return true
}

func finite32(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }

func absf(v float32) float32 { return float32(math.Abs(float64(v))) }

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
	for i, rot := range p.aim.rots {
		if !p.aim.agrees[i] {
			continue
		}
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

// Reach and hitbox: Helix's own rules, beyond BDS (BDS takes 6.7 blocks to the target's
// position). A cheat client grows every hitbox or its own reach; the server measures from the
// eyes to the target's real box. Lag: the attacker saw the target where it was its round trip
// (plus the client's smoothing) ago, so the attack counts if it fits any place the target was in
// that time (world.Entity.AppendPast). The attacker's own look needs no allowance: its rotation
// comes in the same inputs as its attack.

const (
	// attackReach is vanilla's survival attack reach, attackReachSlack what float rounding and
	// smoothing add to it, and attackReachCreative the reach in creative.
	attackReach         = 3
	attackReachSlack    = 0.5
	attackReachCreative = 6.5
	// attackSmoothTicks is how many ticks behind a client shows other entities, on top of its
	// round trip.
	attackSmoothTicks = 3
)

// attackFits reports whether an attack on e, whose box is lo..hi now, reaches and aims at it
// and sees it, where it is or where the player saw it in the last round trip.
func (p *Player) attackFits(r *world.Region, e *world.Entity, lo, hi mgl32.Vec3) bool {
	now := e.Position()
	p.pastBuf = e.AppendPast(p.pastBuf[:0], r.CurrentTick()-p.lagTicks())
	for _, at := range p.pastBuf {
		off := at.Sub(now)
		if blo, bhi := lo.Add(off), hi.Add(off); p.inAttackReach(blo, bhi) && p.aimsAt(blo, bhi) && p.seesEntity(r, blo, bhi) {
			return true
		}
	}
	return false
}

// lagTicks is how many ticks behind the player sees other entities: its round trip and the
// client's smoothing, all that is kept if the connection measures no round trip.
func (p *Player) lagTicks() int64 {
	rtt := p.conn.Latency()
	if rtt <= 0 {
		return world.PastMoves
	}
	return min(int64((rtt+world.TickDuration-1)/world.TickDuration)+attackSmoothTicks, world.PastMoves)
}

// inAttackReach reports whether the box lo..hi is within the player's attack reach of its eyes.
func (p *Player) inAttackReach(lo, hi mgl32.Vec3) bool {
	eyes := p.entity.Position()
	nearest := mgl32.Vec3{clamp32(eyes[0], lo[0], hi[0]), clamp32(eyes[1], lo[1], hi[1]), clamp32(eyes[2], lo[2], hi[2])}
	limit := float32(attackReach + attackReachSlack)
	if p.creative() {
		limit = attackReachCreative
	}
	return nearest.Sub(eyes).Len() <= limit
}
