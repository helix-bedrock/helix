package player

import (
	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// SetClock sets the clock players are told of. Call it before any player joins.
func (s *Host) SetClock(c *daytime.Clock) { s.clock = c }

// clockRegistry is what BDS's join tells a player of its clocks: all of them, with their markers.
func (s *Host) clockRegistry() *packet.SyncWorldClocks {
	markers := make([]protocol.TimeMarkerData, len(daytime.Markers))
	for i, m := range daytime.Markers {
		markers[i] = protocol.TimeMarkerData{ID: m.ID, Name: m.Name, Time: m.Time, Period: protocol.Option(int32(daytime.MarkerPeriod))}
	}
	return &packet.SyncWorldClocks{
		PayloadType: protocol.ClockPayloadTypeInitializeRegistry,
		Clocks: []protocol.WorldClockData{{
			ID: daytime.OverworldClockID, Name: daytime.OverworldClockName, Time: s.clock.Time(), TimeMarkers: markers,
		}},
	}
}

// SyncClocks tells everyone online the time, which BDS does every 256 ticks. Clients run their
// clocks themselves between these. Runs: anywhere but the global owner.
func (s *Host) SyncClocks() {
	if s.clock == nil {
		return
	}
	pk := &packet.SyncWorldClocks{
		PayloadType: protocol.ClockPayloadTypeSyncState,
		SyncStates:  []protocol.SyncWorldClockStateData{{ClockID: daytime.OverworldClockID, Time: s.clock.Time()}},
	}
	g := s.global
	g.call(func() {
		for _, p := range g.order {
			p.sendNow(pk)
		}
	})
}
