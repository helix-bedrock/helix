package circuit

import "testing"

// A default Config is already in range, so setting it changes nothing.
func TestDefaultConfigIsSane(t *testing.T) {
	if got, want := *NewSettings(DefaultConfig()).Get(), DefaultConfig(); got != want {
		t.Errorf("default changed when set:\n%+v\nwant\n%+v", got, want)
	}
}

// Numbers that would stop something working are brought back in range.
func TestSettingsBringNumbersInRange(t *testing.T) {
	c := DefaultConfig()
	c.Interval, c.Lamp.OffDelay, c.Piston.Step, c.Tripwire.HookReach = 0, -3, 2, hookReachLimit+1
	c.TNT.BlastFuseMin, c.TNT.BlastFuseMax = 40, 10
	got := NewSettings(c).Get()
	if got.Interval != 1 || got.Lamp.OffDelay != minTicks || got.Piston.Step != DefaultConfig().Piston.Step || got.Tripwire.HookReach != hookReachLimit {
		t.Errorf("not in range: %+v", got)
	}
	if got.TNT.BlastFuseMax < got.TNT.BlastFuseMin {
		t.Errorf("fuse range %d to %d", got.TNT.BlastFuseMin, got.TNT.BlastFuseMax)
	}
}
