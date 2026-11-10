package helix

import (
	"errors"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
	"github.com/helix-bedrock/worldgen/end"
)

// The End's level key fields Helix reads and writes for the dragon fight. BDS keeps
// {data: {DragonFight: {...}}}; Helix only keeps where the exit portal stands, and has
// not compared the rest of BDS's fields yet.
const (
	endKeyData        = "data"
	endKeyDragonFight = "DragonFight"
	endKeyExitPortal  = "ExitPortalLocation"
)

// exitPortalSource is the End generator's exit portal shape.
type exitPortalSource interface {
	ExitPortal(open bool) []end.PodiumBlock
}

// enterEnd builds the End's exit portal, closed, the first time a player arrives in the
// End, as BDS's EndDragonFight spawns it (sub_497D5B0) when the fight starts rather than
// in generation. With no dragon yet it stays closed. Runs: a player's region.
func (s *Server) enterEnd() {
	if s.end == nil || !s.endPodium.CompareAndSwap(false, true) {
		return
	}
	src, ok := s.endGen.(exitPortalSource)
	if !ok {
		return
	}
	fight, data := s.dragonFight()
	if _, built := fight[endKeyExitPortal]; built {
		return
	}
	blocks := src.ExitPortal(false)
	if len(blocks) == 0 {
		return
	}
	byChunk := map[mcdb.ChunkPos][]end.PodiumBlock{}
	low := blocks[0].Y
	for _, b := range blocks {
		cp := world.ChunkPos(b.X, b.Z)
		byChunk[cp] = append(byChunk[cp], b)
		low = min(low, b.Y)
	}
	for cp, list := range byChunk {
		s.end.RunAt(cp, func(r *world.Region) {
			for _, b := range list {
				pos := cube.Pos{int(b.X), int(b.Y), int(b.Z)}
				if r.SetBlock(pos, 0, b.ID) {
					r.UpdateNeighbours(pos)
				}
			}
		})
	}
	fight[endKeyExitPortal] = []int32{0, low + 1, 0}
	data[endKeyDragonFight] = fight
	value, err := nbt.MarshalEncoding(map[string]any{endKeyData: data}, nbt.LittleEndian)
	if err == nil {
		err = s.worldStore.StoreLevel(mcdb.LevelKeyTheEnd, value)
	}
	if err != nil {
		s.log.Error("save the End's dragon fight", "err", err)
	}
}

// dragonFight reads the End's level key: its DragonFight compound and the data
// compound around it, empty when there is none.
func (s *Server) dragonFight() (fight, data map[string]any) {
	fight, data = map[string]any{}, map[string]any{}
	value, err := s.worldStore.LoadLevel(mcdb.LevelKeyTheEnd)
	if err != nil {
		if !errors.Is(err, mcdb.ErrNotFound) {
			s.log.Warn("read the End's level key", "err", err)
		}
		return fight, data
	}
	var m map[string]any
	if err := nbt.UnmarshalEncoding(value, &m, nbt.LittleEndian); err != nil {
		s.log.Warn("read the End's level key", "err", err)
		return fight, data
	}
	if d, ok := m[endKeyData].(map[string]any); ok {
		data = d
	}
	if f, ok := data[endKeyDragonFight].(map[string]any); ok {
		fight = f
	}
	return fight, data
}
