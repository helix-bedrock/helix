package debug

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"time"
)

//go:embed dashboard.html
var dashboard []byte

// maxRecord bounds a recording.
const maxRecord = 10 * time.Minute

// Serve serves the dashboard on address until the listener fails. Bind it
// to localhost: it shows every player's packets.
//
//   - /             the dashboard
//   - /api/packets  recent packets: ?player= &name= &dir=in|out &limit=
//   - /api/counts   packets per type
//   - /api/ticks    tick timings per world
//   - /record       a recording of every packet for ?seconds= (default 60),
//     one JSON object per line; &full=1 includes whole packets
//   - /debug/pprof/ Go's profiler
func (m *Monitor) Serve(address string, log *slog.Logger) (net.Addr, error) {
	if m == nil {
		return nil, errors.New("debug: nil monitor")
	}
	l, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("debug: listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(dashboard)
	})
	mux.HandleFunc("/api/packets", m.servePackets)
	mux.HandleFunc("/api/counts", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, m.Counts()) })
	mux.HandleFunc("/api/ticks", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, m.Ticks()) })
	mux.HandleFunc("/record", m.serveRecord)
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) && log != nil {
			log.Error("debug server stopped", "err", err)
		}
	}()
	return l.Addr(), nil
}

func (m *Monitor) servePackets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	player, name, dir := strings.ToLower(q.Get("player")), strings.ToLower(q.Get("name")), q.Get("dir")
	writeJSON(w, m.Packets(func(p Packet) bool {
		return (player == "" || strings.Contains(strings.ToLower(p.Player), player)) &&
			(name == "" || strings.Contains(strings.ToLower(p.Name), name)) &&
			(dir == "" || (dir == "in") == p.In)
	}, limit))
}

func (m *Monitor) serveRecord(w http.ResponseWriter, r *http.Request) {
	secs, _ := strconv.Atoi(r.URL.Query().Get("seconds"))
	d := time.Duration(secs) * time.Second
	if d <= 0 {
		d = time.Minute
	}
	d = min(d, maxRecord)
	full := r.URL.Query().Get("full") == "1"

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=helix-%s.jsonl", time.Now().Format("20060102-150405")))
	ch, cancel := m.subscribe()
	defer cancel()
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case p := <-ch:
			rec := struct {
				Packet
				Full any `json:"packet,omitempty"`
			}{Packet: p}
			if full {
				rec.Full = p.pk
			}
			if err := enc.Encode(rec); err != nil {
				// Some packets cannot be encoded as JSON; keep the summary.
				rec.Full = nil
				if enc.Encode(rec) != nil {
					return
				}
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-timer.C:
			_ = enc.Encode(map[string]any{"ticks": m.Ticks()})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
