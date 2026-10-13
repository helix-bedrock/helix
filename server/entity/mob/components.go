package mob

import (
	"encoding/json"
	"github.com/helix-bedrock/helix/server/entity"
	"slices"
	"time"

	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/item"
	"github.com/helix-bedrock/helix/server/world"
)

// Components that act every tick rather than as goals (Helix). Each reads
// its settings from the mob's definition.

// sensorSpec is minecraft:target_nearby_sensor: events when the target
// comes within or leaves a range.
type sensorSpec struct {
	inside, outside     float64
	mustSee             bool
	onInside, onOutside string
	onVisionLost        string
}

func eventName(raw json.RawMessage) string {
	var e struct {
		Event string `json:"event"`
	}
	_ = json.Unmarshal(raw, &e)
	return e.Event
}

func parseSensor(raw json.RawMessage) *sensorSpec {
	return &sensorSpec{
		inside: numberField(raw, "inside_range", 1), outside: numberField(raw, "outside_range", 5),
		mustSee: boolField(raw, "must_see", false), onInside: eventName(field(raw, "on_inside_range")),
		onOutside: eventName(field(raw, "on_outside_range")), onVisionLost: eventName(field(raw, "on_vision_lost_inside_range")),
	}
}

// explodeSpec is minecraft:explode: a fuse, then an explosion that kills
// the mob.
type explodeSpec struct {
	power  float64
	fuse   int
	fire   bool
	breaks bool
	lit    bool
}

func parseExplode(raw json.RawMessage) *explodeSpec {
	return &explodeSpec{
		power: numberField(raw, "power", 3), fuse: int(numberField(raw, "fuse_length", 0) * 20),
		fire: boolField(raw, "causes_fire", false), breaks: boolField(raw, "breaks_blocks", true),
		lit: boolField(raw, "fuse_lit", false),
	}
}

// ageableSpec is minecraft:ageable: a baby grows up after duration.
type ageableSpec struct {
	duration  int
	feed      []string
	growEvent string
}

func parseAgeable(raw json.RawMessage) *ageableSpec {
	a := &ageableSpec{duration: int(numberField(raw, "duration", 1200) * 20), growEvent: eventName(field(raw, "grow_up"))}
	a.feed = itemNames(field(raw, "feed_items"))
	return a
}

// breedSpec is minecraft:breedable.
type breedSpec struct {
	items    []string
	mates    map[string]string // mate type to baby type
	needTame bool
}

func parseBreedable(raw json.RawMessage, self string) *breedSpec {
	b := &breedSpec{items: itemNames(field(raw, "breed_items")), mates: map[string]string{},
		needTame: boolField(raw, "require_tame", false)}
	var list []json.RawMessage
	with := field(raw, "breeds_with")
	if json.Unmarshal(with, &list) != nil {
		list = []json.RawMessage{with}
	}
	for _, w := range list {
		var m struct {
			Mate string `json:"mate_type"`
			Baby string `json:"baby_type"`
		}
		if json.Unmarshal(w, &m) == nil {
			if m.Mate == "" {
				m.Mate = self
			}
			if m.Baby == "" {
				m.Baby = self
			}
			b.mates[m.Mate] = m.Baby
		}
	}
	if len(b.mates) == 0 {
		b.mates[self] = self
	}
	return b
}

// itemNames reads a list of item names, or of {item} objects.
func itemNames(raw json.RawMessage) []string {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		var one string
		if json.Unmarshal(raw, &one) == nil {
			return []string{qualify(one)}
		}
		return nil
	}
	var out []string
	for _, e := range list {
		var name string
		if json.Unmarshal(e, &name) == nil {
			out = append(out, qualify(name))
			continue
		}
		var o struct {
			Item string `json:"item"`
		}
		if json.Unmarshal(e, &o) == nil && o.Item != "" {
			out = append(out, qualify(o.Item))
		}
	}
	return out
}

func itemName(s item.Stack) string {
	if s.Empty() {
		return ""
	}
	name, _ := s.Item().EncodeItem()
	return name
}

// tickComponents runs the components that act every tick.
func (m *Mob) tickComponents(tx *world.Tx) {
	s := m.s
	if sp := s.spec.sensor; sp != nil {
		m.tickSensor(sp)
	}
	if ex := s.spec.explode; ex != nil && ex.lit {
		if s.fuse < 0 {
			s.fuse = ex.fuse
			m.updateState()
		}
		if s.fuse--; s.fuse <= 0 {
			m.explode(tx, ex)
			return
		}
	} else if s.fuse >= 0 {
		s.fuse = -1
		m.updateState()
	}
	if a := s.spec.ageable; a != nil && s.spec.baby {
		if s.age++; s.age >= a.duration {
			s.age = 0
			m.TriggerEvent(a.growEvent)
		}
	}
	if s.love > 0 {
		s.love--
	}
	if s.breedCooldown > 0 {
		s.breedCooldown--
	}
}

func (m *Mob) tickSensor(sp *sensorSpec) {
	t, ok := m.Target()
	if !ok {
		if m.s.sensorInside {
			m.s.sensorInside = false
			m.TriggerEvent(sp.onOutside)
		}
		return
	}
	d := t.Position().Sub(m.data.Pos).Len()
	sees := !sp.mustSee || m.canSee(t)
	switch {
	case d <= sp.inside && sees && !m.s.sensorInside:
		m.s.sensorInside = true
		m.TriggerEvent(sp.onInside)
	case m.s.sensorInside && d <= sp.inside && !sees:
		m.s.sensorInside = false
		m.TriggerEvent(sp.onVisionLost)
	case m.s.sensorInside && d > sp.outside:
		m.s.sensorInside = false
		m.TriggerEvent(sp.onOutside)
	}
}

func (m *Mob) explode(tx *world.Tx, ex *explodeSpec) {
	conf := block.ExplosionConfig{SpawnFire: ex.fire}
	if !ex.breaks {
		conf.ItemDropChance = -1
	}
	m.s.health.AddHealth(-m.s.health.Health())
	_ = m.Close()
	conf.Explode(tx, world.EntityExplosionSource{Entity: m, ExplosionSize: ex.power})
}

// FuseLit returns how long until the mob explodes, if its fuse burns. The
// client shows it swelling and flashing.
func (m *Mob) FuseLit() (time.Duration, bool) {
	if m.s.fuse < 0 {
		return 0, false
	}
	return time.Duration(m.s.fuse) * time.Second / 20, true
}

// Interact is called when a player uses held on the mob. It reports whether
// the mob took the item: breeding food puts it in love, and food for its
// age makes a baby grow faster.
func (m *Mob) Interact(user world.Entity, held item.Stack) bool {
	s := m.s
	name := itemName(held)
	if name == "" || m.Dead() {
		return false
	}
	if a := s.spec.ageable; a != nil && s.spec.baby && slices.Contains(a.feed, name) {
		s.age += a.duration / 10
		return true
	}
	if b := s.spec.breed; b != nil && !s.spec.baby && !b.needTame && slices.Contains(b.items, name) && s.love == 0 && s.breedCooldown == 0 {
		s.love = 600
		m.viewAction(entity.LoveAction{})
		return true
	}
	return false
}

// InLove reports whether the mob wants to breed.
func (m *Mob) InLove() bool { return m.s.love > 0 }
