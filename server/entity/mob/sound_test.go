package mob

import (
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/molang"
	"github.com/helix-bedrock/helix/server/world"
	"github.com/helix-bedrock/helix/server/world/sound"
)

func TestExperienceRewards(t *testing.T) {
	d := defs(t)
	byPlayer := molang.Env{Query: func(n string) float64 { return b2f(n == "last_hit_by_player") }}
	for id, want := range map[string]float64{"minecraft:zombie": 5, "minecraft:creeper": 5} {
		typ := &Type{def: d[id]}
		x := d[id].spec(typ.spawnGroups(nil, nil)).xp
		if x == nil {
			t.Fatalf("%s has no experience reward", id)
		}
		if got := x.Eval(byPlayer); got != want {
			t.Errorf("%s killed by a player gives %v, want %v", id, got, want)
		}
		if got := x.Eval(molang.Env{}); got != 0 {
			t.Errorf("%s not killed by a player gives %v", id, got)
		}
	}
}

func TestNoExperienceWithoutPlayer(t *testing.T) {
	w := mobWorld(t)
	h := spawn(t, w, "minecraft:zombie", mgl64.Vec3{0.5, 1, 0.5})
	withMob(t, w, h, func(m *Mob) { m.Hurt(1000, entity.VoidDamageSource{}) })
	ticks(w, 25)
	if n := count(w, func(e world.Entity) bool { return e.H().Type() == entity.ExperienceOrbType }); n != 0 {
		t.Fatalf("%d experience orbs from a zombie no player killed", n)
	}
}

type soundViewer struct {
	world.NopViewer
	events chan sound.MobEvent
}

func (v soundViewer) ViewSound(_ mgl64.Vec3, s world.Sound) {
	if e, ok := s.(sound.MobEvent); ok {
		select {
		case v.events <- e:
		default:
		}
	}
}

func TestMobSounds(t *testing.T) {
	w := mobWorld(t)
	v := soundViewer{events: make(chan sound.MobEvent, 64)}
	l := world.NewLoader(2, w, v)
	w.Do(func(tx *world.Tx) {
		l.Move(tx, mgl64.Vec3{})
		l.Load(tx, 50)
	})
	h := spawn(t, w, "minecraft:cow", mgl64.Vec3{0.5, 1, 0.5})
	ticks(w, 600)
	withMob(t, w, h, func(m *Mob) { m.Hurt(1, entity.VoidDamageSource{}) })
	got := map[string]bool{}
	for len(v.events) > 0 {
		e := <-v.events
		if e.Entity == "minecraft:cow" {
			got[e.Event] = true
		}
	}
	if !got["ambient"] && !got["ambient.baby"] {
		t.Errorf("no ambient sound in 30 seconds: %v", got)
	}
	if !got["hurt"] {
		t.Errorf("no hurt sound: %v", got)
	}
}
