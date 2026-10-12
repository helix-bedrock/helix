package hook

import (
	"strings"
	"testing"
)

type owner struct{ off bool }

func (o *owner) Enabled() bool          { return !o.off }
func (o *owner) Run(_ string, f func()) { f() }

type ev struct {
	Cancellable
	log *[]string
}

func TestOrderCancelAndFilters(t *testing.T) {
	var l List[ev]
	o, off := &owner{}, &owner{off: true}
	add := func(o *owner, name string, p Priority, ic bool, cancel bool) {
		l.Add(o, func(e *ev) {
			*e.log = append(*e.log, name)
			if cancel {
				e.Cancel()
			}
		}, p, ic, nil)
	}
	add(o, "monitor", Monitor, true, false)
	add(o, "high", High, false, true)
	add(off, "disabled", Normal, false, false)
	add(o, "low", Low, false, false)
	var log []string
	if !l.Fire(&ev{log: &log}) {
		t.Fatal("event not reported cancelled")
	}
	if got := strings.Join(log, ","); got != "low,high" {
		t.Fatalf("ran %s, want low,high", got)
	}
}

// BenchmarkInactive measures an event nobody listens to: the cost every
// call site pays. It must not allocate.
func BenchmarkInactive(b *testing.B) {
	var l List[ev]
	b.ReportAllocs()
	for b.Loop() {
		if l.Active() {
			l.Fire(&ev{})
		}
	}
}
