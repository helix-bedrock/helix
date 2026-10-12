package debug

import (
	"testing"
	"time"
)

func TestNilMonitorDoesNothing(t *testing.T) {
	var m *Monitor
	if m.Hooks() != nil {
		t.Fatal("nil monitor returned hooks")
	}
	tk := m.StartTick("w")
	tk.Phase("x")
	tk.Block("minecraft:water", time.Millisecond)
	tk.End()
	if m.Ticks() != nil || m.Packets(nil, 10) != nil {
		t.Fatal("nil monitor returned data")
	}
}

func TestTicksRecorded(t *testing.T) {
	m := New(Config{Ticks: 4, SlowTick: time.Nanosecond})
	for range 6 {
		tk := m.StartTick("overworld")
		time.Sleep(2 * time.Millisecond) // Windows clocks are coarse
		tk.Phase("entities")
		tk.Block("minecraft:water", time.Millisecond)
		tk.End()
	}
	ws := m.Ticks()
	if len(ws) != 1 || ws[0].Total != 6 || len(ws[0].Ticks) != 4 || ws[0].Slow != 6 {
		t.Fatalf("ticks %+v", ws)
	}
	if b := ws[0].Ticks[3].Blocks; len(b) != 1 || b[0].Count != 1 {
		t.Fatalf("blocks %+v", b)
	}
}
