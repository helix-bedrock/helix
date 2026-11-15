package mob

import (
	"math"
	"strings"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/trace"
)

// Tracing: a mob given a trace.Log records what it does each tick (entity's goal selector,
// navigation, movement pipeline and events, plus the mob's own flags, hurts and wanted
// position here). A mob without one records nothing. While the log is paused the mob does
// not tick at all, except for the steps asked for.

// SetTrace starts recording into l, or stops with nil. Runs: the mob's region, between
// ticks.
func (m *Mob) SetTrace(l *trace.Log) {
	m.trace = l
	m.sel.Trace = l
	m.body.Trace = l
	m.stateEnv.Trace = l
	if m.inst != nil {
		m.inst.Trace = l
	}
}

// Trace is the mob's log, nil when it records nothing.
func (m *Mob) Trace() *trace.Log { return m.trace }

// RunningGoals appends the component names of the mob's running goals to dst.
func (m *Mob) RunningGoals(dst []string) []string {
	m.sel.EachRunning(func(name string, _ int32) { dst = append(dst, name) })
	return dst
}

// Wanted is the position the move control heads for, and whether it has one.
func (m *Mob) Wanted() (behavior.Vec3, bool) {
	return m.ctl.Wanted, m.ctl.HasWanted
}

// traceTick stamps the log with the tick and records where the mob starts it.
func (m *Mob) traceTick() {
	if !m.trace.On() || m.v.r == nil {
		return
	}
	m.trace.SetTick(uint64(m.v.r.CurrentTick())) //nolint:gosec // ticks are positive
	p := m.body.Pos
	m.trace.Add(trace.Record{Kind: trace.KindTick, What: "tick", N: int64(m.target), V: [3]float32{p.X, p.Y, p.Z}, F: m.health})
	m.traceLook()
}

// traceWanted records the path when it or the node walked to changes, and the move
// control's wanted position after navigation.
func (m *Mob) traceWanted() {
	if !m.trace.On() {
		return
	}
	m.tracePath()
	if !m.ctl.HasWanted {
		return
	}
	w := m.ctl.Wanted
	m.trace.Add(trace.Record{Kind: trace.KindNav, What: "wanted", V: [3]float32{w.X, w.Y, w.Z}, F: m.moveSpeed})
}

// traceFlag records a flag change.
func (m *Mob) traceFlag(n int, on bool) {
	if !m.trace.On() {
		return
	}
	what := "off"
	if on {
		what = "on"
	}
	m.trace.Add(trace.Record{Kind: trace.KindFlag, What: what, Name: FlagName(n), N: int64(n)})
}

// traceHurt records damage taken, or refused with why.
func (m *Mob) traceHurt(what string, amount float32, cause int) {
	if m.trace.On() {
		m.trace.Add(trace.Record{Kind: trace.KindHurt, What: what, N: int64(cause), F: amount})
	}
}

// flagNames are the actor flags' names, as the protocol numbers them.
var flagNames = [...]string{
	"on_fire", "sneaking", "riding", "sprinting", "using_item", "invisible", "tempted", "in_love",
	"saddled", "powered", "ignited", "baby", "converting", "critical", "show_name", "always_show_name",
	"no_ai", "silent", "wall_climbing", "can_climb", "can_swim", "can_fly", "can_walk", "resting",
	"sitting", "angry", "interested", "charged", "tamed", "orphaned", "leashed", "sheared",
	"gliding", "elder", "moving", "breathing", "chested", "stackable", "show_bottom", "standing",
	"shaking", "idling", "casting", "charging", "keyboard_controlled", "power_jump", "dash", "lingering",
	"has_collision", "has_gravity", "fire_immune", "dancing", "enchanted", "return_trident", "container_private", "transforming",
	"damage_nearby_mobs", "swimming", "bribed", "pregnant", "laying_egg", "passenger_can_pick", "transition_sitting", "eating",
	"laying_down", "sneezing", "trusting", "rolling", "scared", "in_scaffolding", "over_scaffolding", "descend_through_block",
	"blocking", "transition_blocking", "blocked_using_shield", "blocked_using_damaged_shield", "sleeping", "wants_to_wake", "trade_interest", "door_breaker",
	"breaking_obstruction", "door_opener", "captain", "stunned", "roaring", "delayed_attack", "avoiding_mobs", "avoiding_block",
	"facing_target_to_range_attack", "hidden_when_invisible", "in_ui", "stalking", "emoting", "celebrating", "admiring", "celebrating_special",
	"out_of_control", "ram_attack", "playing_dead", "in_ascendable_block", "over_descendable_block", "croaking", "eat_mob", "jump_goal_jump",
	"emerging", "sniffing", "digging", "sonic_boom", "has_dash_cooldown", "push_towards_closest_space", "scenting", "rising",
	"feeling_happy", "searching", "crawling", "timer_flag_1", "timer_flag_2", "timer_flag_3", "body_rotation_blocked", "render_when_invisible",
	"body_rotation_axis_aligned", "collidable", "wasd_air_controlled", "does_server_auth_only_dismount", "body_rotation_always_follows_head",
}

// FlagName is an actor flag's name, "" for one past the list.
func FlagName(n int) string {
	if n >= 0 && n < len(flagNames) {
		return flagNames[n]
	}
	return ""
}

// tracePath records a new path (its node count and end) or the next node walked to.
func (m *Mob) tracePath() {
	p, i := m.nv.Path()
	n := 0
	if p != nil {
		n = len(p.Points)
	}
	if n == m.tracePathAt[0] && i == m.tracePathAt[1] {
		return
	}
	newPath := n != m.tracePathAt[0]
	m.tracePathAt = [2]int{n, i}
	switch {
	case n == 0:
		m.trace.Note(trace.KindNav, "no path", "", 0)
	case newPath:
		e := p.Points[n-1].Pos
		m.trace.Add(trace.Record{Kind: trace.KindNav, What: "new path", N: int64(n), V: [3]float32{float32(e.X), float32(e.Y), float32(e.Z)}})
	case i < n:
		q := p.Points[i].Pos
		m.trace.Add(trace.Record{Kind: trace.KindNav, What: "next node", N: int64(i), V: [3]float32{float32(q.X), float32(q.Y), float32(q.Z)}})
	default:
		m.trace.Note(trace.KindNav, "path done", "", int64(i))
	}
}

// traceTarget records a target change, the target's place and the code that changed it.
func (m *Mob) traceTarget(id behavior.ID) {
	if !m.trace.Wants(trace.KindTarget) {
		return
	}
	r := trace.Record{Kind: trace.KindTarget, What: "target set", N: int64(id), Who: trace.Callers(2, 2)}
	if id == behavior.NoID {
		r.What = "target cleared"
	} else if a := m.actor(id); a != nil {
		p := a.Pos()
		r.V, r.F = [3]float32{p.X, p.Y, p.Z}, traceDist(p, m.body.Pos)
		r.Name = a.Type()
	}
	m.trace.Add(r)
}

// traceVel records a change of velocity v of the kind what, and the code that made it.
func (m *Mob) traceVel(what string, v behavior.Vec3) {
	if m.trace.Wants(trace.KindVelocity) {
		m.trace.Add(trace.Record{Kind: trace.KindVelocity, What: what, V: [3]float32{v.X, v.Y, v.Z}, Who: trace.Callers(2, 2)})
	}
}

// traceLook records the mob's rotation and where its target is, after the tick record.
func (m *Mob) traceLook() {
	if !m.trace.Wants(trace.KindNote) {
		return
	}
	m.trace.Add(trace.Record{Kind: trace.KindNote, What: "look yaw/pitch/head", V: [3]float32{m.yaw, m.pitch, m.headYaw}})
	if a := m.actor(m.target); a != nil {
		p := a.Pos()
		m.trace.Add(trace.Record{Kind: trace.KindNote, What: "target at", Name: a.Type(), N: int64(m.target), V: [3]float32{p.X, p.Y, p.Z}, F: traceDist(p, m.body.Pos)})
	}
}

// traceOp records an action Apply carried out.
func (m *Mob) traceOp(o *op, what string, ok bool) {
	if !m.trace.Wants(trace.KindAttack) {
		return
	}
	r := trace.Record{Kind: trace.KindAttack, What: what, N: int64(o.id), V: [3]float32{o.at.X, o.at.Y, o.at.Z}, F: o.amount, Name: o.name}
	if !ok {
		r.Why = "did not land"
	}
	m.trace.Add(r)
}

// traceSound records a sound the mob plays.
func (m *Mob) traceSound(n int, at behavior.Vec3) {
	if m.trace.Wants(trace.KindSound) {
		m.trace.Add(trace.Record{Kind: trace.KindSound, What: "sound", Name: SoundName(n), N: int64(n), V: [3]float32{at.X, at.Y, at.Z}})
	}
}

// touchReach is how far past the box the touched-block scan looks.
const touchReach = 0.01

// traceBlocks records the block the mob stands on when it changes, the block its feet are
// in when that changes, and every block it pushes against this tick (walls on the sides
// it collided on, a ceiling), with names and positions.
func (m *Mob) traceBlocks() {
	if !m.trace.Wants(trace.KindBlock) || m.v.r == nil {
		return
	}
	b := &m.body
	below := path.Pos{X: floor32(b.Pos.X), Y: floor32(b.Box.Min.Y - 0.1), Z: floor32(b.Pos.Z)}
	if b.OnGround && below != m.traceGround {
		m.traceGround = below
		m.traceBlock("standing on", below)
	}
	feet := path.Pos{X: floor32(b.Pos.X), Y: floor32(b.Pos.Y), Z: floor32(b.Pos.Z)}
	if feet != m.traceFeet {
		m.traceFeet = feet
		m.traceBlock("feet in", feet)
	}
	box := b.Box
	if b.HorizontalCollision[0] {
		m.traceSide(path.Box{Min: path.Vec3{X: box.Min.X - touchReach, Y: box.Min.Y, Z: box.Min.Z}, Max: path.Vec3{X: box.Min.X, Y: box.Max.Y, Z: box.Max.Z}}, "wall -x")
		m.traceSide(path.Box{Min: path.Vec3{X: box.Max.X, Y: box.Min.Y, Z: box.Min.Z}, Max: path.Vec3{X: box.Max.X + touchReach, Y: box.Max.Y, Z: box.Max.Z}}, "wall +x")
	}
	if b.HorizontalCollision[1] {
		m.traceSide(path.Box{Min: path.Vec3{X: box.Min.X, Y: box.Min.Y, Z: box.Min.Z - touchReach}, Max: path.Vec3{X: box.Max.X, Y: box.Max.Y, Z: box.Min.Z}}, "wall -z")
		m.traceSide(path.Box{Min: path.Vec3{X: box.Min.X, Y: box.Min.Y, Z: box.Max.Z}, Max: path.Vec3{X: box.Max.X, Y: box.Max.Y, Z: box.Max.Z + touchReach}}, "wall +z")
	}
	if b.VerticalCollision && !b.OnGround {
		m.traceSide(path.Box{Min: path.Vec3{X: box.Min.X, Y: box.Max.Y, Z: box.Min.Z}, Max: path.Vec3{X: box.Max.X, Y: box.Max.Y + touchReach, Z: box.Max.Z}}, "ceiling")
	}
}

// traceSide records each block whose collision boxes reach into the thin box s.
func (m *Mob) traceSide(s path.Box, what string) {
	cells(s, func(p path.Pos) bool {
		var buf [4]path.Box
		for _, bx := range m.v.boxesAt(buf[:0], p) {
			if overlap(bx, s) {
				m.traceBlock(what, p)
				break
			}
		}
		return true
	})
}

// traceBlock records the block at p by name.
func (m *Mob) traceBlock(what string, p path.Pos) {
	id := m.v.block(p, 0)
	m.trace.Add(trace.Record{Kind: trace.KindBlock, What: what, Name: m.rt.Blocks.reg.Name(id), V: [3]float32{float32(p.X), float32(p.Y), float32(p.Z)}})
}

// TraceHeader is the dump's header lines about the mob: its component groups, the flags
// set and the running goals.
func (m *Mob) TraceHeader() []string {
	var groups []string
	if m.inst != nil {
		for _, e := range m.inst.Stack() {
			sign := "+"
			if !e.Added {
				sign = "-"
			}
			groups = append(groups, sign+e.Group)
		}
	}
	var flags []string
	for n := range len(flagNames) {
		if m.flags.Has(n) {
			flags = append(flags, flagNames[n])
		}
	}
	return []string{
		"groups: " + strings.Join(groups, " "),
		"flags: " + strings.Join(flags, " "),
		"goals running: " + strings.Join(m.RunningGoals(nil), " "),
	}
}

// traceDist is the distance between two points.
func traceDist(a, b behavior.Vec3) float32 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

// BlocksAt is the name of the block under the mob's feet and the block its feet are in.
// Runs: the mob's region.
func (m *Mob) BlocksAt() (below, in string) {
	if m.v.r == nil {
		return "", ""
	}
	b := &m.body
	under := path.Pos{X: floor32(b.Pos.X), Y: floor32(b.Box.Min.Y - 0.1), Z: floor32(b.Pos.Z)}
	feet := path.Pos{X: floor32(b.Pos.X), Y: floor32(b.Pos.Y), Z: floor32(b.Pos.Z)}
	return m.rt.Blocks.reg.Name(m.v.block(under, 0)), m.rt.Blocks.reg.Name(m.v.block(feet, 0))
}
