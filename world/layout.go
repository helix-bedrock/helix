package world

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"github.com/helix-bedrock/world/mcdb"
)

// The layout of regions changes only between ticks (RFC 0002): a region
// never grows into another while either ticks. A region that must merge
// with one still ticking parks, without ticking, until that one ends its
// tick; a region a parked one waits for parks too when its tick ends, so
// the merge always happens and no tick ever waits.

// overrunWarnEvery limits the over-budget warnings of one region.
const overrunWarnEvery = 5 * time.Second

// newRegion makes an empty region. Under d.mu.
func (d *Dimension) newRegion(next time.Time) *Region {
	d.nextID++
	r := &Region{
		d: d, id: d.nextID, seq: d.sched.seq.Add(1), next: next,
		sections: map[sectionPos]struct{}{}, chunks: map[mcdb.ChunkPos]*Chunk{}, holds: map[mcdb.ChunkPos]int{},
		sectionHolds: map[sectionPos]int{}, loading: map[mcdb.ChunkPos]bool{}, awaiting: map[mcdb.ChunkPos][]func(*Region){},
		byChunk: map[mcdb.ChunkPos][]*Entity{}, lcg: randomTickSeed,
	}
	d.regions = append(d.regions, r)
	return r
}

// schedule queues a ready region's next tick. Under d.mu.
func (d *Dimension) schedule(r *Region) {
	if !d.closing {
		d.sched.schedule(r)
	}
}

// kill removes a region that was merged away or has nothing left. Under
// d.mu.
func (d *Dimension) kill(r *Region) {
	r.state = stateDead
	for s := range r.sections {
		if d.sections[s] == r {
			delete(d.sections, s)
		}
	}
	d.regions = slices.DeleteFunc(d.regions, func(o *Region) bool { return o == r })
	d.parked = slices.DeleteFunc(d.parked, func(o *Region) bool { return o == r })
}

// runTick ticks r on a scheduler goroutine, then merges, splits or removes
// it as needed.
func (d *Dimension) runTick(r *Region) {
	d.mu.Lock()
	if r.state != stateReady || d.closing {
		d.mu.Unlock()
		return
	}
	r.state = stateTicking
	d.ticking++
	d.mu.Unlock()

	start := time.Now()
	r.ticking.Store(true)
	r.tick()
	r.ticking.Store(false)
	took := time.Since(start)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.ticking--
	r.state = stateReady
	r.info = RegionInfo{Chunks: len(r.chunks), Viewers: len(r.viewers), LastTick: took}
	// A late region does not tick twice in a row to catch up.
	if r.next.Before(start) {
		r.next = start
	}
	r.next = r.next.Add(TickDuration)
	if took > TickDuration && start.Sub(r.lastWarn) > overrunWarnEvery {
		r.lastWarn = start
		d.log.Warn("region tick over budget", "region", r.id, "took", took, "chunks", len(r.chunks), "viewers", len(r.viewers))
	}
	d.maintain(r)
	if r.state == stateReady && d.wantedByParked(r) {
		r.state = stateParked
		d.parked = append(d.parked, r)
	}
	reschedule := r.state == stateReady
	d.settleParked()
	if d.closing {
		d.idle.Broadcast()
		return
	}
	if reschedule && r.state == stateReady {
		d.sched.schedule(r)
	}
}

// maintain merges r with the regions it came close to, splits it if its
// sections no longer touch, and removes it once it has nothing left. Under
// d.mu, with r not ticking.
func (d *Dimension) maintain(r *Region) {
	if near := d.neighbours(r, false); len(near) > 0 {
		if anyTicking(near) {
			r.state = stateParked
			d.parked = append(d.parked, r)
			return
		}
		d.absorbAll(r, near)
	}
	d.settle(r)
}

// settle ends a layout change of r: it splits r if needed and removes it if
// it has nothing left. Under d.mu.
func (d *Dimension) settle(r *Region) {
	r.added = r.added[:0]
	if r.reshaped {
		r.reshaped = false
		d.split(r)
	}
	if r.idle() {
		d.kill(r)
	}
}

// claim gives r the section s if no region owns it. Under d.mu.
func (d *Dimension) claim(r *Region, s sectionPos) {
	switch o, ok := d.sections[s]; {
	case !ok:
		d.sections[s] = r
		r.sections[s] = struct{}{}
		r.added = append(r.added, s)
		r.reshaped = true
	case o != r:
		r.contested = true
	}
}

// neighbours returns the regions r must merge with, ordered by ID: the
// owners of sections r holds chunks in, and the regions within
// MergeDistance of its new sections (of all its sections if all is set).
// Sections r waited for that are free now are claimed. Under d.mu.
func (d *Dimension) neighbours(r *Region, all bool) []*Region {
	var out []*Region
	add := func(o *Region) {
		if o != r && !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	if r.contested {
		for s := range r.sectionHolds {
			if _, own := r.sections[s]; own {
				continue
			}
			if o, ok := d.sections[s]; ok {
				add(o)
			} else {
				d.claim(r, s)
			}
		}
	}
	secs := r.added
	if all {
		secs = slices.Collect(maps.Keys(r.sections))
	}
	m := d.conf.MergeDistance
	for _, s := range secs {
		for dx := -m; dx <= m; dx++ {
			for dz := -m; dz <= m; dz++ {
				if o, ok := d.sections[sectionPos{s[0] + dx, s[1] + dz}]; ok {
					add(o)
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b *Region) int { return cmp.Compare(a.id, b.id) })
	return out
}

func anyTicking(regions []*Region) bool {
	return slices.ContainsFunc(regions, func(o *Region) bool { return o.state == stateTicking })
}

// absorbAll merges every region of near into r, then loads what r holds in
// the sections it gained. Under d.mu, with none of them ticking.
func (d *Dimension) absorbAll(r *Region, near []*Region) {
	for _, o := range near {
		d.absorb(r, o)
	}
	r.contested = false
	for s := range r.sectionHolds {
		if _, own := r.sections[s]; !own {
			r.contested = true
			break
		}
	}
	r.requestMissing()
}

// absorb moves everything src owns into dst, by pointer. Under d.mu.
func (d *Dimension) absorb(dst, src *Region) {
	for s := range src.sections {
		d.sections[s] = dst
		dst.sections[s] = struct{}{}
	}
	clear(src.sections)
	maps.Copy(dst.chunks, src.chunks)
	for pos, n := range src.holds {
		dst.holds[pos] += n
	}
	for s, n := range src.sectionHolds {
		dst.sectionHolds[s] += n
	}
	for pos := range src.loading {
		dst.loading[pos] = true
	}
	for pos, fns := range src.awaiting {
		dst.awaiting[pos] = append(dst.awaiting[pos], fns...)
	}
	for _, v := range src.viewers {
		v.region = dst
	}
	dst.viewers = append(dst.viewers, src.viewers...)
	slices.SortFunc(dst.viewers, func(a, b *Viewer) int { return cmp.Compare(a.id, b.id) })
	for _, e := range src.entities {
		e.region = dst
		dst.insertEntity(e)
	}
	dst.added = append(dst.added, src.added...)
	dst.reshaped = dst.reshaped || src.reshaped
	dst.contested = dst.contested || src.contested
	src.inMu.Lock()
	dst.inMu.Lock()
	dst.in = append(dst.in, src.in...)
	src.in = nil
	dst.inMu.Unlock()
	src.inMu.Unlock()
	d.kill(src)
}

// wantedByParked reports whether a parked region waits to merge with r.
// Under d.mu.
func (d *Dimension) wantedByParked(r *Region) bool {
	for _, p := range d.parked {
		if p != r && slices.Contains(d.neighbours(p, true), r) {
			return true
		}
	}
	return false
}

// settleParked merges every parked region whose neighbours all stopped
// ticking, and schedules it again. Under d.mu.
func (d *Dimension) settleParked() {
	for changed := true; changed; {
		changed = false
		for _, p := range slices.Clone(d.parked) {
			if p.state != stateParked {
				continue
			}
			near := d.neighbours(p, true)
			if anyTicking(near) {
				continue
			}
			d.absorbAll(p, near)
			d.parked = slices.DeleteFunc(d.parked, func(o *Region) bool { return o == p })
			p.state = stateReady
			d.settle(p)
			if p.state == stateReady {
				d.schedule(p)
			}
			changed = true
		}
	}
}

// split gives each group of r's sections that no longer touches the rest
// its own region; the largest group stays in r. Under d.mu, with r not
// ticking.
func (d *Dimension) split(r *Region) {
	groups := d.sectionGroups(r.sections)
	if len(groups) < 2 {
		return
	}
	for _, g := range groups[1:] {
		n := d.newRegion(r.next)
		in := func(pos mcdb.ChunkPos) bool { _, ok := g[d.sectionOf(pos)]; return ok }
		for s := range g {
			d.sections[s] = n
			n.sections[s] = struct{}{}
			delete(r.sections, s)
			if h, ok := r.sectionHolds[s]; ok {
				n.sectionHolds[s] = h
				delete(r.sectionHolds, s)
			}
		}
		moveIf(n.chunks, r.chunks, in)
		moveIf(n.holds, r.holds, in)
		moveIf(n.loading, r.loading, in)
		moveIf(n.awaiting, r.awaiting, in)
		r.viewers = slices.DeleteFunc(r.viewers, func(v *Viewer) bool {
			if v.lastRadius < 0 || !in(v.lastCentre) {
				return false
			}
			v.region = n
			n.viewers = append(n.viewers, v)
			return true
		})
		// A player's entity goes with its viewer, any other with its chunk.
		r.entities = slices.DeleteFunc(r.entities, func(e *Entity) bool {
			if v := e.conf.Viewer; (v != nil && v.region != n) || (v == nil && !in(e.chunk)) {
				return false
			}
			r.unindex(e)
			e.region = n
			n.insertEntity(e)
			return true
		})
		r.inMu.Lock()
		r.in = slices.DeleteFunc(r.in, func(e entry) bool {
			if (e.at && in(e.pos)) || (e.v != nil && e.v.region == n) {
				n.in = append(n.in, e)
				return true
			}
			return false
		})
		r.inMu.Unlock()
		d.schedule(n)
	}
}

// moveIf moves the entries of src whose key passes in to dst.
func moveIf[V any](dst, src map[mcdb.ChunkPos]V, in func(mcdb.ChunkPos) bool) {
	for pos, v := range src {
		if in(pos) {
			dst[pos] = v
			delete(src, pos)
		}
	}
}

// sectionGroups splits sections into groups whose members are within
// MergeDistance of each other, largest first, in a fixed order.
func (d *Dimension) sectionGroups(sections map[sectionPos]struct{}) []map[sectionPos]struct{} {
	keys := slices.SortedFunc(maps.Keys(sections), func(a, b sectionPos) int {
		return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
	})
	seen := make(map[sectionPos]bool, len(keys))
	var groups []map[sectionPos]struct{}
	m := d.conf.MergeDistance
	for _, k := range keys {
		if seen[k] {
			continue
		}
		g := map[sectionPos]struct{}{}
		stack := []sectionPos{k}
		seen[k] = true
		for len(stack) > 0 {
			s := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			g[s] = struct{}{}
			for dx := -m; dx <= m; dx++ {
				for dz := -m; dz <= m; dz++ {
					o := sectionPos{s[0] + dx, s[1] + dz}
					if _, ok := sections[o]; ok && !seen[o] {
						seen[o] = true
						stack = append(stack, o)
					}
				}
			}
		}
		groups = append(groups, g)
	}
	slices.SortStableFunc(groups, func(a, b map[sectionPos]struct{}) int { return len(b) - len(a) })
	return groups
}
