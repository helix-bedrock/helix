package helix

import (
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/player"
	"github.com/helix-bedrock/helix/server/world"
)

// Task is a scheduled piece of plugin work. Stop cancels it.
type Task struct {
	stop chan struct{}
	once sync.Once
	s    *pluginState
}

// Stop cancels the task. A run already started finishes. It is safe to call
// more than once and from anywhere.
func (t *Task) Stop() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		close(t.stop)
		t.s.removeTask(t)
	})
}

// Stopped reports whether the task was stopped.
func (t *Task) Stopped() bool {
	select {
	case <-t.stop:
		return true
	default:
		return false
	}
}

func (s *pluginState) newTask() *Task {
	t := &Task{stop: make(chan struct{}), s: s}
	s.taskMu.Lock()
	if s.tasks == nil {
		s.tasks = map[*Task]struct{}{}
	}
	s.tasks[t] = struct{}{}
	s.taskMu.Unlock()
	return t
}

func (s *pluginState) removeTask(t *Task) {
	s.taskMu.Lock()
	delete(s.tasks, t)
	s.taskMu.Unlock()
}

// stopTasks stops every task of the plugin, when the server closes.
func (s *pluginState) stopTasks() {
	s.taskMu.Lock()
	ts := make([]*Task, 0, len(s.tasks))
	for t := range s.tasks {
		ts = append(ts, t)
	}
	s.taskMu.Unlock()
	for _, t := range ts {
		t.Stop()
	}
}

// After runs fn once after d, on a goroutine of its own: use Do or DoPlayer
// from fn to touch the world. It is skipped if the plugin is disabled by
// then.
func (p *Plugin) After(d time.Duration, fn func()) *Task {
	s := p.state
	t := s.newTask()
	go func() {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
			if s.enabled.Load() {
				s.call("After task", fn)
			}
			t.Stop()
		case <-t.stop:
		}
	}()
	return t
}

// Every runs fn every d until stopped, on a goroutine of its own: use Do or
// DoPlayer from fn to touch the world. Runs are skipped while the plugin is
// disabled and resume when it is enabled again.
func (p *Plugin) Every(d time.Duration, fn func()) *Task {
	s := p.state
	t := s.newTask()
	if d <= 0 {
		d = time.Second / 20
	}
	go func() {
		tick := time.NewTicker(d)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				if s.enabled.Load() {
					s.call("Every task", fn)
				}
			case <-t.stop:
				return
			}
		}
	}()
	return t
}

// Do runs fn on the region of w that owns pos, with its Tx, and returns at
// once. It is skipped if the plugin is disabled by the time it runs.
func (p *Plugin) Do(w *world.World, pos mgl64.Vec3, fn func(tx *world.Tx)) {
	s := p.state
	w.DoAt(pos, func(tx *world.Tx) {
		if s.enabled.Load() {
			s.call("Do task", func() { fn(tx) })
		}
	})
}

// DoPlayer runs fn with the player on its region, wherever it is by then,
// and returns at once. It is skipped if the player left or the plugin is
// disabled by the time it runs.
func (p *Plugin) DoPlayer(pl *player.Player, fn func(tx *world.Tx, p *player.Player)) {
	s := p.state
	pl.H().Do(func(tx *world.Tx, e world.Entity) {
		if pl, ok := e.(*player.Player); ok && s.enabled.Load() {
			s.call("DoPlayer task", func() { fn(tx, pl) })
		}
	})
}
