package player

import (
	"testing"

	"github.com/go-gl/mathgl/mgl64"
)

func TestFallDistance(t *testing.T) {
	p := &Player{}
	down := &MoveInfo{From: mgl64.Vec3{0, 10, 0}, To: mgl64.Vec3{0, 9.5, 0}}
	vanillaFall(p, down)
	vanillaFall(p, down)
	if p.move.fall != 1 {
		t.Fatalf("fall %v after two half-block drops", p.move.fall)
	}
	vanillaFall(p, &MoveInfo{From: down.To, To: down.To, InWater: true})
	if p.move.fall != 0 {
		t.Fatalf("fall %v in water", p.move.fall)
	}
	vanillaFall(p, down)
	vanillaFall(p, &MoveInfo{From: down.To, To: down.To.Add(mgl64.Vec3{0, 0.1, 0})})
	if p.move.fall != 0 {
		t.Fatalf("fall %v after moving up", p.move.fall)
	}
}

func TestExhaustionValues(t *testing.T) {
	v, err := vanillaExhaustionValues()
	if err != nil || v.Sprint != 0.1 || v.SprintJump != 0.2 || v.Mine != 0.005 {
		t.Fatalf("%+v, %v", v, err)
	}
}
