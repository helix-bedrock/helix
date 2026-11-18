package player

import (
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/world/cube"

	"github.com/helix-bedrock/helix/world"
)

// railDirections is how many straight rail directions a structure cart's rail picks from:
// along z or along x.
const railDirections = 2

// StructureCartFunc spawns a minecart generation may place in a structure (a mineshaft
// corridor's chest cart) on the first tick of its chunk, and returns it; nil when none
// spawns. Replace it to change, move or drop these carts. Runs: owner of the region.
type StructureCartFunc func(s *Host, r *world.Region, m world.GeneratedMob) *Minecart

// vanillaStructureCart is BDS 1.26.60's chest cart in a mineshaft corridor: while mobs may
// spawn, the spot's chance is rolled; the spot must be air over anything but air. It then
// lays a straight rail without telling the neighbours, and the cart stands at the block's
// corner with its loot table still unrolled.
func vanillaStructureCart(s *Host, r *world.Region, m world.GeneratedMob) *Minecart {
	if !s.level.DoMobSpawning || (m.OneIn > 0 && rand.Int32N(m.OneIn) != 0) { //nolint:gosec // gameplay randomness
		return nil
	}
	at := mgl32.Vec3{m.X, m.Y, m.Z}
	pos := floorPos(at)
	if !s.cartSpotFree(r, pos) {
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
