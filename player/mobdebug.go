package player

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/entity/trace"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// Mob debugging (a test tool, reached through -testcmds' /mobdebug): a player picks mobs to
// watch; each watched mob records its ticks in its own trace.Log (entity's goal selector,
// navigation, movement pipeline, events, and the mob's flags and hurts). The player sees
// a live summary of one of them above the hotbar, can pause and step them, and can dump
// every watched mob's last ticks to a text file.

const (
	// mobDebugReach is how far the looked-at mob may be.
	mobDebugReach = 32
	// mobDebugNear and mobDebugTypeRange are the default radii of near and type.
	mobDebugNear      = 8
	mobDebugTypeRange = 32
	// mobDebugEvery is how often, in ticks, the live summary is sent.
	mobDebugEvery = 5
	// mobDebugDir is the folder dumps are written to, under the server's folder.
	mobDebugDir = "mobdebug"
)

// mobDebug is a player's watched mobs.
type mobDebug struct {
	watched []*Mob
	ticks   int
}

// MobDebug runs /mobdebug for p and tells p the result. Runs: anywhere; the work runs on
// p's region.
func (p *Player) MobDebug(args []string) bool {
	return p.Run(func(p *Player) { p.Message(p.mobDebugRun(args)) })
}

// mobDebugRun does one /mobdebug and returns what to tell the player.
func (p *Player) mobDebugRun(args []string) string {
	r := p.entity.Region()
	if r == nil {
		return "You are not in the world."
	}
	p.mobDebugPrune(r)
	if len(args) == 0 {
		return p.mobDebugLook(r)
	}
	d := &p.mobDebugState
	switch args[0] {
	case "near":
		return p.mobDebugAdd(r, "", radiusArg(args, 1, mobDebugNear))
	case "type":
		if len(args) < 2 {
			return "Use: /mobdebug type <id> [radius]"
		}
		id := args[1]
		if !strings.Contains(id, ":") {
			id = "minecraft:" + id
		}
		return p.mobDebugAdd(r, id, radiusArg(args, 2, mobDebugTypeRange))
	case "off":
		n := len(d.watched)
		p.mobDebugStop(r)
		return fmt.Sprintf("Stopped watching %d mobs.", n)
	case "pause", "resume":
		for _, o := range d.watched {
			o.m.Trace().Paused = args[0] == "pause"
		}
		return fmt.Sprintf("%d watched mobs %sd.", len(d.watched), args[0])
	case "step":
		n := radiusArg(args, 1, 1)
		for _, o := range d.watched {
			l := o.m.Trace()
			l.Paused, l.Steps = true, int(n)
		}
		return fmt.Sprintf("%d watched mobs step %d ticks.", len(d.watched), int(n))
	case "dump":
		return p.mobDebugDump(r)
	case "list":
		return p.mobDebugList()
	}
	return "Use: /mobdebug (the mob you look at), near [radius], type <id> [radius], list, pause, resume, step [n], dump, off"
}

// radiusArg reads args[i] as a number, def when missing or bad.
func radiusArg(args []string, i int, def float32) float32 {
	if i >= len(args) {
		return def
	}
	f, err := strconv.ParseFloat(args[i], 32)
	if err != nil || f <= 0 {
		return def
	}
	return float32(f)
}

// mobDebugLook starts or stops watching the mob p looks at.
func (p *Player) mobDebugLook(r *world.Region) string {
	o := p.lookedAtMob(r)
	if o == nil {
		return "Look at a mob within 32 blocks (or use near / type)."
	}
	if o.m.Trace() != nil {
		p.mobDebugUnwatch(o)
		return "Stopped watching " + mobLabel(o) + "."
	}
	p.mobDebugWatch(o)
	return "Watching " + mobLabel(o) + ". /mobdebug dump writes its last ticks."
}

// mobDebugAdd watches every mob of type typ ("" for any) within radius of p.
func (p *Player) mobDebugAdd(r *world.Region, typ string, radius float32) string {
	at := p.entity.Position()
	n := 0
	for _, e := range r.AppendEntitiesIn(nil, at[0]-radius, at[2]-radius, at[0]+radius, at[2]+radius) {
		o, ok := e.Owner().(*Mob)
		if !ok || o.Dead() || typ != "" && o.m.Type() != typ {
			continue
		}
		if e.Position().Sub(at).Len() > radius+eyeHeight {
			continue
		}
		if o.m.Trace() == nil {
			p.mobDebugWatch(o)
			n++
		}
	}
	return fmt.Sprintf("Watching %d more mobs (%d in all).", n, len(p.mobDebugState.watched))
}

// mobDebugWatch gives o a log and adds it to p's watched mobs. A mob another player
// watches keeps its log.
func (p *Player) mobDebugWatch(o *Mob) {
	if o.m.Trace() == nil {
		o.m.SetTrace(trace.New(0))
	}
	p.mobDebugState.watched = append(p.mobDebugState.watched, o)
}

// mobDebugUnwatch stops watching o.
func (p *Player) mobDebugUnwatch(o *Mob) {
	d := &p.mobDebugState
	for i, w := range d.watched {
		if w == o {
			d.watched = append(d.watched[:i], d.watched[i+1:]...)
			break
		}
	}
	o.m.SetTrace(nil)
}

// mobDebugStop stops watching every mob, which then run and record nothing again. Runs:
// region r, the player's (also as it leaves).
func (p *Player) mobDebugStop(r *world.Region) {
	for _, o := range p.mobDebugState.watched {
		if o.entity != nil && o.entity.Region() == r {
			o.m.SetTrace(nil)
		}
	}
	p.mobDebugState.watched = nil
}

// mobDebugPrune drops watched mobs that are gone or in another region, whose state this
// region may not touch.
func (p *Player) mobDebugPrune(r *world.Region) {
	d := &p.mobDebugState
	kept := d.watched[:0]
	for _, o := range d.watched {
		if o.entity != nil && o.entity.Region() == r && !o.Dead() {
			kept = append(kept, o)
		}
	}
	clear(d.watched[len(kept):])
	d.watched = kept
}

// lookedAtMob is the nearest mob whose box p's look line passes through.
func (p *Player) lookedAtMob(r *world.Region) *Mob {
	eye := p.entity.Position()
	dir := lookDirection(p.entity.Rotation())
	var best *Mob
	bestT := float32(mobDebugReach)
	for _, e := range r.AppendEntitiesIn(nil, eye[0]-mobDebugReach, eye[2]-mobDebugReach, eye[0]+mobDebugReach, eye[2]+mobDebugReach) {
		o, ok := e.Owner().(*Mob)
		if !ok || o.Dead() {
			continue
		}
		b := o.m.Box()
		if t, hit := rayBox(eye, dir, mgl32.Vec3{b.Min.X, b.Min.Y, b.Min.Z}, mgl32.Vec3{b.Max.X, b.Max.Y, b.Max.Z}); hit && t < bestT {
			best, bestT = o, t
		}
	}
	return best
}

// rayBox is where the line from o along d first enters the box, if it does.
func rayBox(o, d, lo, hi mgl32.Vec3) (float32, bool) {
	tmin, tmax := float32(0), float32(1e9)
	for i := range 3 {
		if d[i] == 0 {
			if o[i] < lo[i] || o[i] > hi[i] {
				return 0, false
			}
			continue
		}
		t1, t2 := (lo[i]-o[i])/d[i], (hi[i]-o[i])/d[i]
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		tmin, tmax = max(tmin, t1), min(tmax, t2)
		if tmin > tmax {
			return 0, false
		}
	}
	return tmin, true
}

// mobLabel names a mob for the player: type and unique ID.
func mobLabel(o *Mob) string {
	return fmt.Sprintf("%s #%d", strings.TrimPrefix(o.m.Type(), "minecraft:"), o.m.ID())
}

// mobDebugList lists the watched mobs.
func (p *Player) mobDebugList() string {
	d := &p.mobDebugState
	if len(d.watched) == 0 {
		return "No mobs watched."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d watched:", len(d.watched))
	for _, o := range d.watched {
		b.WriteString(" ")
		b.WriteString(mobLabel(o))
		if o.m.Trace().Paused {
			b.WriteString(" (paused)")
		}
	}
	return b.String()
}

// mobDebugDump writes each watched mob's records to its own file.
func (p *Player) mobDebugDump(r *world.Region) string {
	d := &p.mobDebugState
	if len(d.watched) == 0 {
		return "No mobs watched."
	}
	if err := os.MkdirAll(mobDebugDir, 0o755); err != nil {
		return "Could not make the dump folder: " + err.Error()
	}
	var names []string
	for _, o := range d.watched {
		name := filepath.Join(mobDebugDir, fmt.Sprintf("%s-%d-%d.txt", strings.TrimPrefix(o.m.Type(), "minecraft:"), o.m.ID(), r.CurrentTick()))
		if err := writeMobDump(name, o); err != nil {
			return "Could not write " + name + ": " + err.Error()
		}
		names = append(names, name)
	}
	return "Wrote " + strings.Join(names, ", ")
}

// writeMobDump writes one mob's header and records to a file.
func writeMobDump(name string, o *Mob) error {
	f, err := os.Create(name) //nolint:gosec // a test tool's own folder
	if err != nil {
		return err
	}
	at := o.entity.Position()
	fmt.Fprintf(f, "%s at (%.3f %.3f %.3f) health %.2f, %d records\n", mobLabel(o), at[0], at[1], at[2], o.Health(), o.m.Trace().Len())
	if _, err := o.m.Trace().WriteTo(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// mobDebugTick shows the live summary of one watched mob: the one looked at if watched,
// else the first. Runs: the player's region, each tick.
func (p *Player) mobDebugTick() {
	d := &p.mobDebugState
	if len(d.watched) == 0 || p.entity == nil {
		return
	}
	if d.ticks++; d.ticks%mobDebugEvery != 0 {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	p.mobDebugPrune(r)
	if len(d.watched) == 0 {
		return
	}
	o := d.watched[0]
	if l := p.lookedAtMob(r); l != nil && l.m.Trace() != nil {
		o = l
	}
	p.send(&packet.Text{TextType: packet.TextTypeTip, Message: mobSummary(o)})
}

// mobSummary is the live view of one mob.
func mobSummary(o *Mob) string {
	m := o.m
	b := m.Body()
	var s strings.Builder
	fmt.Fprintf(&s, "%s  hp %.1f", mobLabel(o), o.Health())
	if m.Trace().Paused {
		s.WriteString("  PAUSED")
	}
	fmt.Fprintf(&s, "\npos %.2f %.2f %.2f  vel %.3f %.3f %.3f", b.Pos.X, b.Pos.Y, b.Pos.Z, b.Vel.X, b.Vel.Y, b.Vel.Z)
	fmt.Fprintf(&s, "\nground %v  wall %v  target %d", b.OnGround, b.HorizontalCollision[0] || b.HorizontalCollision[1], m.Target())
	if w, ok := m.Wanted(); ok {
		fmt.Fprintf(&s, "  to %.1f %.1f %.1f", w.X, w.Y, w.Z)
	}
	goals := m.RunningGoals(nil)
	for i, g := range goals {
		goals[i] = strings.TrimPrefix(g, "minecraft:behavior.")
	}
	s.WriteString("\ngoals: " + strings.Join(goals, ", "))
	writePathAhead(&s, o)
	return s.String()
}

// mobDebugAhead is how many path nodes ahead the live view shows.
const mobDebugAhead = 3

// writePathAhead adds the next nodes of the mob's path and its end.
func writePathAhead(s *strings.Builder, o *Mob) {
	nodes, i, ok := o.m.PathNodes()
	if !ok || i >= len(nodes) {
		s.WriteString("\npath: none")
		return
	}
	fmt.Fprintf(s, "\npath %d/%d:", i, len(nodes))
	for j := i; j < len(nodes) && j < i+mobDebugAhead; j++ {
		fmt.Fprintf(s, " (%d %d %d)", nodes[j].X, nodes[j].Y, nodes[j].Z)
	}
	e := nodes[len(nodes)-1]
	fmt.Fprintf(s, " .. end (%d %d %d)", e.X, e.Y, e.Z)
}
