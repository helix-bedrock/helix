package player

import (
	"slices"

	"github.com/helix-bedrock/world/mcdb"
)

// Items a player moves between itself and a container saved with a chunk (a chest, barrel,
// hopper, chest boat, chest cart...) are saved on both sides in one commit: the chunk and the
// player together, at the end of the tick the request ran in (one save per tick however many
// requests). Helix's own rule, beyond BDS, which saves the player and the chunk at different
// times, so a server stopped between them (a crash, a kill) could keep the items on both sides
// or on neither.

// linkContainer notes the chunks holding the open container, whose items a request just
// changed, for the save at the end of the tick.
func (p *Player) linkContainer(c *Chest) {
	switch {
	case c.boat != nil:
		p.linkChunk(c.boat.entity.Chunk())
	case c.cart != nil:
		p.linkChunk(c.cart.entity.Chunk())
	case c.ender:
		// An ender chest's items are the player's own.
	default:
		p.linkChunk(chunkPosOf(c.pos[0], c.pos[2]))
		if c.paired {
			p.linkChunk(chunkPosOf(c.partner[0], c.partner[2]))
		}
	}
}

func (p *Player) linkChunk(pos mcdb.ChunkPos) {
	if !slices.Contains(p.linkChunks, pos) {
		p.linkChunks = append(p.linkChunks, pos)
	}
}

// saveLinked saves the noted chunks, each with the player in the same commit. A chunk its
// region does not hold any more is left to its own save, and the player saved at once. Runs:
// the player's region, at the end of its input.
func (p *Player) saveLinked() {
	if len(p.linkChunks) == 0 {
		return
	}
	defer func() { p.linkChunks = p.linkChunks[:0] }()
	if p.entity == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	players := []mcdb.LinkedPlayer{{IDs: p.ids, Data: p.saveCopy(p.entity.Position(), p.entity.Rotation())}}
	for _, pos := range p.linkChunks {
		if !r.SaveLinked(pos, players) {
			p.save(p.entity.Position(), p.entity.Rotation())
		}
	}
}

// chunkPosOf is the chunk holding block column x, z.
func chunkPosOf(x, z int) mcdb.ChunkPos {
	return mcdb.ChunkPos{int32(x >> 4), int32(z >> 4)} //nolint:gosec // chunk coordinates fit int32
}
