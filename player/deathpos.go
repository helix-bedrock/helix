package player

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
)

// BDS 1.26.60 keeps where a player last died in its synched actor data (getters at
// ServerPlayer's vtable +1008, +1016, +1024: keys 127 position, 128 dimension, 129 has died),
// which is what the client's recovery compass points to, and saves it in the player's data
// (Player::addAdditionalSaveData, 0xbce58b0) only once the player has died.
const (
	tagDeathX         = "DeathPositionX"
	tagDeathY         = "DeathPositionY"
	tagDeathZ         = "DeathPositionZ"
	tagDeathDimension = "DeathDimension"
	tagHasDiedBefore  = "HasDiedBefore"
)

// lastDeath is where and in which dimension the player last died.
type lastDeath struct {
	pos       cube.Pos
	dimension int32
	died      bool
}

// LastDeath is where the player last died and the dimension's ID; false if it never has.
// Runs: the player's region.
func (p *Player) LastDeath() (cube.Pos, int32, bool) {
	d := p.death
	return d.pos, d.dimension, d.died
}

// SetLastDeath changes where the player last died, which its recovery compass points to.
// Runs: the player's region.
func (p *Player) SetLastDeath(pos cube.Pos, dimension int32) {
	p.death = lastDeath{pos: pos, dimension: dimension, died: true}
	p.send(actorData(p.runtimeID, p.deathMetadata()))
}

// recordDeath keeps where the player is dying.
func (p *Player) recordDeath() {
	p.SetLastDeath(p.blockPos(), int32(p.s.dimensionID(p.dim)))
}

// deathMetadata is the death position as synched actor data.
func (p *Player) deathMetadata() protocol.EntityMetadata {
	d := p.death
	return protocol.EntityMetadata{
		protocol.EntityDataKeyPlayerLastDeathPosition:  protocol.BlockPos{int32(d.pos[0]), int32(d.pos[1]), int32(d.pos[2])}, //nolint:gosec // block coordinates fit int32
		protocol.EntityDataKeyPlayerLastDeathDimension: d.dimension,
		protocol.EntityDataKeyPlayerHasDied:            flagByte(d.died),
	}
}

// loadDeath reads the saved death position: all four numbers or none.
func (p *Player) loadDeath(data map[string]any) {
	x, okX := data[tagDeathX].(int32)
	y, okY := data[tagDeathY].(int32)
	z, okZ := data[tagDeathZ].(int32)
	dim, okD := data[tagDeathDimension].(int32)
	if okX && okY && okZ && okD {
		p.death = lastDeath{pos: cube.Pos{int(x), int(y), int(z)}, dimension: dim, died: true}
	}
}

// saveDeath writes it as BDS does, only for a player who has died.
func (p *Player) saveDeath(data map[string]any) {
	d := p.death
	if !d.died {
		return
	}
	data[tagDeathX], data[tagDeathY], data[tagDeathZ] = int32(d.pos[0]), int32(d.pos[1]), int32(d.pos[2]) //nolint:gosec // block coordinates fit int32
	data[tagDeathDimension] = d.dimension
	data[tagHasDiedBefore] = uint8(1)
}
