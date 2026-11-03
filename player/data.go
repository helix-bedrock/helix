package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/mcdb"
)

// load reads the player's saved data: where they were, and the unique ID
// BDS gave them.
func (p *Player) load() (pos mgl32.Vec3, rot world.Rotation, ok bool) {
	data, found, err := p.s.playerStore.LoadPlayer(p.ids)
	if err != nil {
		p.s.log.Error("load player", "name", p.name, "err", err)
		return pos, rot, false
	}
	if !found {
		return pos, rot, false
	}
	p.saved = data
	if mode, isInt := data["PlayerGameMode"].(int32); isInt {
		p.gameMode = mode
	}
	p.abilities.load(data)
	p.attributes.load(data)
	// A player saved dead comes back on the death screen, its items long gone.
	if p.dead() {
		p.respawn = respawnStart
	}
	p.inventory.load(data, func() int32 { return p.s.stackIDs.Add(1) })
	if seed, isInt := data[enchantSeedTag].(int32); isInt {
		p.enchant.seed, p.enchant.seeded = seed, true
	}
	if id, isInt := data["UniqueID"].(int64); isInt {
		p.uniqueID = id
	}
	if r, isList := floats(data["Rotation"]); isList && len(r) == 2 {
		rot = world.Rotation{Yaw: r[0], Pitch: r[1], HeadYaw: r[0]}
	}
	if v, isList := floats(data["Pos"]); isList && len(v) == 3 {
		return mgl32.Vec3{v[0], v[1], v[2]}, rot, true
	}
	return pos, rot, false
}

// floats reads an NBT list of floats.
func floats(v any) ([]float32, bool) {
	switch l := v.(type) {
	case []float32:
		return l, true
	case []any:
		out := make([]float32, 0, len(l))
		for _, x := range l {
			f, ok := x.(float32)
			if !ok {
				return nil, false
			}
			out = append(out, f)
		}
		return out, true
	}
	return nil, false
}

// save writes the player back where BDS keeps players, from the state the
// entity had when it was removed. The saved Pos is the eye position, as
// the client reports it and StartGame takes it.
func (p *Player) save(pos mgl32.Vec3, rot world.Rotation) {
	data := p.saved
	if data == nil {
		data = map[string]any{}
	}
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	data["DimensionId"] = int32(mcdb.Overworld)
	data["UniqueID"] = p.uniqueID
	data["PlayerGameMode"] = p.gameMode
	p.abilities.save(data)
	p.attributes.save(data)
	p.inventory.save(data)
	if p.enchant.seeded {
		data[enchantSeedTag] = p.enchant.seed
	}
	if err := p.s.playerStore.StorePlayer(p.ids, data); err != nil {
		p.s.log.Error("save player", "name", p.name, "err", err)
	}
}

// playerIDs are the IDs BDS files a player's data under: the Xbox identity
// (derived from the XUID, so only signed-in players have it) and the UUID
// the client signs itself.
func playerIDs(identity, selfSigned, xuid string) mcdb.PlayerIDs {
	ids := mcdb.PlayerIDs{SelfSignedID: selfSigned}
	if xuid != "" {
		ids.MsaID = identity
	}
	return ids
}
