package main

import (
	"fmt"
	"maps"
	"math"
	"strconv"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// chestBombCommand builds the heaviest chests a player could make, to try a lag machine in
// game: every chest slot a shulker box, every box slot a written book of 50 full pages of
// Chinese characters. Only with -testcmds.
const chestBombCommand = "chestbomb"

const (
	// chestBombDefault is how many chests /chestbomb builds without a count, chestBombMax
	// the most it builds: one chest is about 28 MB once saved.
	chestBombDefault = 10
	chestBombMax     = 1000
	// chestBombSlots is the slots of a chest and of a shulker box.
	chestBombSlots = 27
	// chestBombPages is a book's most pages (BDS's 50), chestBombPageRunes the characters
	// put on each page. The server takes pages of any length; 256 is about what a client
	// lets a player type on one page, which is not checked against BDS.
	chestBombPages     = 50
	chestBombPageRunes = 256
	// chestBombFirstRune is the first CJK ideograph; the text walks on from it so pages differ.
	chestBombFirstRune = 0x4E00
	chestBombRuneSpan  = 0x5000
	// chestBombColumn is how far apart machines stand along X (power spot, piston, chest,
	// the place the chest is pushed into), chestBombRow along Z: a gap keeps chests from
	// pairing. chestBombPerRow machines go in a row.
	chestBombColumn = 4
	chestBombRow    = 2
	chestBombPerRow = 16
	// chestBombAhead is how far east of the player the first machine stands.
	chestBombAhead = 3
	// chestBombPistonEast is a piston's facing_direction pointing east.
	chestBombPistonEast = int32(4)
)

// chestBombStates are the blocks /chestbomb places.
type chestBombStates struct {
	chest, piston uint32
}

// chestBomb runs /chestbomb [count] [pistons]: count chests in rows east of the player, each
// full of full shulker boxes of full books. With pistons, a sticky piston facing east stands
// west of each chest, and the block west of the piston is left free for a redstone block or
// a clock, so the chests can be pushed and pulled.
func chestBomb(srv *helix.Server, p *player.Player, args []string) string {
	count, pistons, err := chestBombArgs(args)
	if err != nil {
		return err.Error()
	}
	e := p.Entity()
	if e == nil || e.Region() == nil {
		return "Not in the world."
	}
	states, err := chestBombBlocks(srv.Registry().Blocks)
	if err != nil {
		return err.Error()
	}
	box, err := chestBombBox(p.Host())
	if err != nil {
		return err.Error()
	}
	r := e.Region()
	at := e.Position()
	origin := cube.Pos{int(math.Floor(float64(at[0]))) + chestBombAhead, int(math.Floor(float64(at[1]))), int(math.Floor(float64(at[2])))}
	built := 0
	for i := range count {
		piston := origin.Add(cube.Pos{i % chestBombPerRow * chestBombColumn, 0, i / chestBombPerRow * chestBombRow})
		if buildChestBomb(r, p.Host(), piston, states, box, pistons) {
			built++
		}
	}
	msg := fmt.Sprintf("Built %d of %d chests (27 boxes x 27 books x %d pages x %d characters each).", built, count, chestBombPages, chestBombPageRunes)
	if pistons {
		msg += " Power the free block west of each piston to push them."
	}
	return msg
}

// chestBombArgs reads the count and the pistons word.
func chestBombArgs(args []string) (int, bool, error) {
	count, pistons := chestBombDefault, false
	for _, a := range args {
		if a == "pistons" {
			pistons = true
			continue
		}
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 || n > chestBombMax {
			return 0, false, fmt.Errorf("use: /chestbomb [count 1-%d] [pistons]", chestBombMax)
		}
		count = n
	}
	return count, pistons, nil
}

// chestBombBlocks looks up the chest and the east-facing sticky piston.
func chestBombBlocks(b *registry.Blocks) (chestBombStates, error) {
	chest, okC := b.Block("minecraft:chest")
	def, okP := b.Block("minecraft:sticky_piston")
	if !okC || !okP {
		return chestBombStates{}, fmt.Errorf("chest or sticky piston missing")
	}
	states := maps.Clone(b.States(def))
	states["facing_direction"] = chestBombPistonEast
	piston, ok := b.StateID("minecraft:sticky_piston", states)
	if !ok {
		return chestBombStates{}, fmt.Errorf("sticky piston facing east missing")
	}
	return chestBombStates{chest: chest, piston: piston}, nil
}

// chestBombBox is a shulker box with a full book in every slot. Every slot and every chest
// shares the same data, so memory stays small while saves still write all of it.
func chestBombBox(h *player.Host) (player.ItemStack, error) {
	box, ok := h.NewItem("minecraft:undyed_shulker_box", 1, 0)
	if !ok {
		return player.ItemStack{}, fmt.Errorf("undyed shulker box item missing")
	}
	book := chestBombBook()
	items := make([]any, chestBombSlots)
	for slot := range items {
		item := maps.Clone(book)
		item["Slot"] = uint8(slot) //nolint:gosec // 27 slots
		items[slot] = item
	}
	box.Tag = map[string]any{"Items": items}
	return box, nil
}

// chestBombBook is a signed book of chestBombPages full pages, as BDS saves an item in a box.
func chestBombBook() map[string]any {
	pages := make([]any, chestBombPages)
	for i := range pages {
		text := make([]rune, chestBombPageRunes)
		for j := range text {
			text[j] = rune(chestBombFirstRune + (i*chestBombPageRunes+j)%chestBombRuneSpan)
		}
		pages[i] = map[string]any{"text": string(text), "photoname": ""}
	}
	return map[string]any{
		"Name": "minecraft:written_book", "Count": uint8(1), "Damage": int16(0), "WasPickedUp": uint8(0),
		"tag": map[string]any{"pages": pages, "author": "chestbomb", "title": "chestbomb", "xuid": "", "generation": int32(0)},
	}
}

// buildChestBomb places one machine with its piston at piston, reporting whether the chest
// stands and was filled. Blocks outside the player's region are skipped.
func buildChestBomb(r *world.Region, h *player.Host, piston cube.Pos, s chestBombStates, box player.ItemStack, pistons bool) bool {
	pos := piston.Add(cube.Pos{1, 0, 0})
	if pistons && r.SetBlock(piston, 0, s.piston) {
		r.UpdateNeighbours(piston)
	}
	if !r.SetBlock(pos, 0, s.chest) {
		return false
	}
	r.UpdateNeighbours(pos)
	c := h.ChestAt(r, pos)
	if c == nil {
		return false
	}
	inv := c.Inventory(r)
	for slot := range chestBombSlots {
		inv.SetItem(slot, box)
	}
	return true
}
