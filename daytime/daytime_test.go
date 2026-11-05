package daytime

import "testing"

func TestDarkenAndSignal(t *testing.T) {
	for _, c := range []struct {
		name             string
		time             int32
		darken           int32
		signal, inverted int32
	}{
		{"noon", 6000, 0, 15, 0},
		{"midnight", 18000, 11, 0, 11},
	} {
		a, d := SunAngle(c.time, false), SkyDarken(c.time, false)
		if d != c.darken {
			t.Errorf("%s: darken %d, want %d", c.name, d, c.darken)
		}
		if got := SensorSignal(15, a, d, false); got != c.signal {
			t.Errorf("%s: signal %d, want %d", c.name, got, c.signal)
		}
		if got := SensorSignal(15, a, d, true); got != c.inverted {
			t.Errorf("%s: inverted %d, want %d", c.name, got, c.inverted)
		}
	}
}

func TestClock(t *testing.T) {
	tick := int64(100)
	c := NewClock(func() int64 { return tick }, 50, true)
	tick += 30
	if got := c.Time(); got != 80 {
		t.Errorf("time %d, want 80", got)
	}
	c.SetCycle(false)
	tick += 30
	if got := c.Time(); got != 80 {
		t.Errorf("stopped clock moved to %d", got)
	}
}

func TestNextOccurrence(t *testing.T) {
	for _, c := range []struct{ now, at, want int32 }{
		{1000, 6000, 6000},
		{7000, 6000, 30000},
		{6000, 6000, 6000},
		{30000, 13000, 37000},
	} {
		if got := NextOccurrence(c.now, c.at); got != c.want {
			t.Errorf("from %d to %d: %d, want %d", c.now, c.at, got, c.want)
		}
	}
	if at, ok := MarkerTime("midnight"); !ok || at != 18000 {
		t.Errorf("midnight is %d, %v", at, ok)
	}
}
