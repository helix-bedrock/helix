package player

import (
	"math"
	"testing"
)

// tunedPlayer is a test player on a Host with vanilla's defaults, where one was set
// at boot.
func tunedPlayer(t *testing.T) *Player {
	t.Helper()
	p := testPlayer(t)
	p.s.game = &gameplay{tuning: vanillaTuning(exhaustionValues{Jump: 0.2})}
	p.s.game.tuning[TuneArmor] = 5
	p.s.ClearAllTuning()
	return p
}

// TestTuningLevels walks one number through all its levels and back: the player's
// value wins over the server's, and clearing each drops to the next.
func TestTuningLevels(t *testing.T) {
	p := tunedPlayer(t)
	if got := p.Tuning(TuneArmor); got != 5 {
		t.Fatalf("default = %v", got)
	}
	p.s.SetTuning(TuneArmor, 7)
	if got := p.Tuning(TuneArmor); got != 7 {
		t.Errorf("server = %v", got)
	}
	// Zero is a value, not "none".
	p.SetTuning(TuneArmor, 0)
	if got := p.Tuning(TuneArmor); got != 0 || !p.HasTuning(TuneArmor) {
		t.Errorf("player = %v", got)
	}
	p.ClearTuning(TuneArmor)
	if got := p.Tuning(TuneArmor); got != 7 || p.HasTuning(TuneArmor) {
		t.Errorf("player cleared = %v", got)
	}
	p.s.ClearTuning(TuneArmor)
	if got := p.Tuning(TuneArmor); got != 5 {
		t.Errorf("server cleared = %v", got)
	}
	p.SetTuning(TuneArmor, 1)
	p.SetTuning(TuneHurtTicks, 1)
	p.ClearAllTuning()
	if p.Tuning(TuneArmor) != 5 || p.Tuning(TuneHurtTicks) != hurtTicks {
		t.Error("clear all kept a value")
	}
	p.s.SetTuning(TuneExhaustJump, 9)
	p.s.ClearAllTuning()
	if got := p.Tuning(TuneExhaustJump); got != 0.2 {
		t.Errorf("exhaustion from the player definition = %v", got)
	}
}

// TestTuningRefused keeps bad numbers and unknown Tunes out.
func TestTuningRefused(t *testing.T) {
	p := tunedPlayer(t)
	for _, v := range []float32{float32(math.NaN()), float32(math.Inf(1))} {
		if p.SetTuning(TuneArmor, v) || p.s.SetTuning(TuneArmor, v) {
			t.Errorf("%v was taken", v)
		}
	}
	if p.SetTuning(tuneCount, 1) || p.SetTuning(-1, 1) || p.Tuning(tuneCount) != 0 {
		t.Error("an unknown Tune was taken")
	}
	p.ClearTuning(tuneCount)
	p.s.ClearTuning(-1)
}

// TestTuneNames gives every Tune a name that finds it again.
func TestTuneNames(t *testing.T) {
	for i := Tune(0); i < tuneCount; i++ {
		if got, ok := TuneByName(i.String()); !ok || got != i {
			t.Errorf("tune %d has name %q", i, i.String())
		}
	}
}

// TestTuningChangesDamage checks armour numbers reach the sum, and that the player's
// own takes away what it wears.
func TestTuningChangesDamage(t *testing.T) {
	p := testPlayer(t)
	p.s.game = &gameplay{tuning: vanillaTuning(exhaustionValues{}), combat: &combatTable{}}
	p.s.ClearAllTuning()
	vanilla := p.afterArmor(10, DamageEntityAttack)
	p.SetTuning(TuneArmor, 10)
	if got := p.afterArmor(10, DamageEntityAttack); got >= vanilla {
		t.Errorf("more armour: %v, was %v", got, vanilla)
	}
	p.ClearTuning(TuneArmor)
	if got := p.afterArmor(10, DamageEntityAttack); got != vanilla {
		t.Errorf("cleared: %v, was %v", got, vanilla)
	}
	p.SetTuning(TuneKnockbackPush, 0)
	p.SetTuning(TuneKnockbackLift, 0)
	if v := p.knockbackStep([3]float32{}, 1, 0, p.Tuning(TuneKnockbackPush), p.Tuning(TuneKnockbackLift)); v[0] != 0 || v[1] != 0 {
		t.Errorf("no knockback = %v", v)
	}
}

// TestTuningEveryOff stops a timer at 0 instead of dividing by it.
func TestTuningEveryOff(t *testing.T) {
	if everyTicks(10, 0) || everyTicks(10, -1) || everyTicks(10, 0.5) || !everyTicks(10, 10) {
		t.Error("everyTicks")
	}
	if timerDone(100, 0) || !timerDone(80, 80) || timerDone(79, 80) {
		t.Error("timerDone")
	}
}

// BenchmarkTuning reads a number the way a tick does.
func BenchmarkTuning(b *testing.B) {
	p := &Player{s: &Host{}}
	p.SetTuning(TuneArmor, 1)
	b.ReportAllocs()
	for b.Loop() {
		_ = p.Tuning(TuneArmor)
		_ = p.Tuning(TuneHurtTicks)
	}
}
