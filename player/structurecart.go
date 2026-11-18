package player

import (
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/world/cube"

	"github.com/helix-bedrock/helix/world"
)

const (
	// railDirections is how many straight rail directions a structure cart's rail picks
	// from: along z or along x.
	railDirections = 2
	// cartChunkMask finds a block's place inside its chunk.
	cartChunkMask = 15
)

// StructureCartFunc spawns a minecart generation may place in a structure (a mineshaft
// corridor's chest cart) on the first tick of its chunk, and returns it; nil when none
// spawns. Replace it to change, move or drop these carts. Runs: owner of the region.
type StructureCartFunc func(s *Host, r *world.Region, m world.GeneratedMob) *Minecart

// vanillaStructureCart is BDS 1.26.60's chest cart in a mineshaft corridor: while mobs may
// spawn, the spot's chance is rolled; the spot must be air over anything but air. It then
// lays a rail in a random straight direction, which joins the rails beside it as any placed
// rail does, and the cart stands at the block's corner with its loot table still unrolled.
func vanillaStructureCart(s *Host, r *world.Region, m world.GeneratedMob) *Minecart {
	at := mgl32.Vec3{m.X, m.Y, m.Z}
	pos := floorPos(at)
	if !s.level.DoMobSpawning || !cartSpotChosen(m.OneIn, pos) || !s.cartSpotFree(r, pos) {
		return nil
	}
	rail, ok := s.reg.Blocks.StateID(nameRail, map[string]any{stateRailDirection: rand.Int32N(railDirections)}) //nolint:gosec // gameplay randomness
	if !ok || !r.SetBlock(pos, 0, rail) {
		return nil
	}
	cart := s.SpawnMinecart(r.Dimension(), m.Identifier, at)
	if cart != nil && m.LootTable != "" {
		cart.saved = map[string]any{lootTableTag: m.LootTable, lootTableSeedTag: rand.Int32()} //nolint:gosec // gameplay randomness
	}
	return cart
}

// cartSpotChosen rolls the spot's 1 in oneIn chance as many times as BDS gives it.
func cartSpotChosen(oneIn int32, pos cube.Pos) bool {
	if oneIn <= 0 {
		return true
	}
	for range cartSpotRolls(pos) {
		if rand.Int32N(oneIn) == 0 { //nolint:gosec // gameplay randomness
			return true
		}
	}
	return false
}

// cartSpotRolls is how many chunks try the spot at pos. BDS's pass over a chunk reaches one
// block past its east and south edges, so the chunks to the west and north also try a spot
// in a chunk's first column or row. All the rolls are made here, so a spot never needs the
// next chunk to be loaded.
func cartSpotRolls(pos cube.Pos) int {
	rolls := 1
	if pos[0]&cartChunkMask == 0 {
		rolls *= 2
	}
	if pos[2]&cartChunkMask == 0 {
		rolls *= 2
	}
	return rolls
}

// cartSpotFree reports whether pos is air and the block under it is not.
func (s *Host) cartSpotFree(r *world.Region, pos cube.Pos) bool {
	air := s.reg.Blocks.Air()
	at, ok := r.Block(pos, 0)
	if !ok || at != air {
		return false
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && below != air
}

// unpackLoot rolls the cart's loot table into its slots, if it still has one.
// Runs: owner of the cart's entity.
func (m *Minecart) unpackLoot() {
	table, _ := m.saved[lootTableTag].(string)
	if table == "" || m.chest == nil {
		return
	}
	seed, _ := m.saved[lootTableSeedTag].(int32)
	delete(m.saved, lootTableTag)
	delete(m.saved, lootTableSeedTag)
	m.chest.rollLoot(table, seed)
}
