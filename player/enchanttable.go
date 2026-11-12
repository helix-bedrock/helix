package player

import (
	"maps"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// BDS 1.26.60's enchanting table.
const (
	enchantingTable = "minecraft:enchanting_table"
	// enchantTableEntityID and enchantTableRotation are the saved block entity's ID and
	// the field holding the book's turn.
	enchantTableEntityID = "EnchantTable"
	enchantTableRotation = "rott"
	enchantInputSlot     = 14
	enchantLapisSlot     = 15
	enchantSeedTag       = "EnchantmentSeed"
	enchantOptionCount   = 3
	enchantMaxShelves    = 15
	// optionWordKinds and optionWordsMin make an option's name 3 or 4 words.
	optionWordKinds = 2
	optionWordsMin  = 3
	// The sums of a draw: its spread and how the chance of more falls.
	drawSpread     = 0.3
	drawSpreadBase = 0.15
	drawMoreSpan   = 50
	// enchantIDBase keeps option IDs clear of the recipes' network IDs, as BDS's shared
	// counter does.
	enchantIDBase = 1 << 30
)

// mt19937 is BDS's Random: the standard Mersenne Twister, its results reduced as BDS's
// nextInt and nextFloat do. A table's options come from it, seeded by the player's seed.
type mt19937 struct {
	state [624]uint32
	next  int
}

func newMT(seed uint32) *mt19937 {
	m := &mt19937{next: len(mt19937{}.state)}
	m.state[0] = seed
	for i := 1; i < len(m.state); i++ {
		m.state[i] = 1812433253*(m.state[i-1]^(m.state[i-1]>>30)) + uint32(i) //nolint:gosec // i is below 624
	}
	return m
}

func (m *mt19937) u32() uint32 {
	const (
		n, shift = 624, 397
		upper    = 0x80000000
		lower    = 0x7FFFFFFF
		twist    = 0x9908B0DF
	)
	if m.next >= n {
		for i := range n {
			y := m.state[i]&upper | m.state[(i+1)%n]&lower
			m.state[i] = m.state[(i+shift)%n] ^ y>>1
			if y&1 != 0 {
				m.state[i] ^= twist
			}
		}
		m.next = 0
	}
	y := m.state[m.next]
	m.next++
	y ^= y >> 11
	y ^= y << 7 & 0x9D2C5680
	y ^= y << 15 & 0xEFC60000
	return y ^ y>>18
}

// intN is BDS's nextInt: 0 for no span.
func (m *mt19937) intN(n int32) int32 {
	if n <= 0 {
		return 0
	}
	return int32(m.u32() % uint32(n)) //nolint:gosec // n is positive and the result below it
}

// float is BDS's nextFloat.
func (m *mt19937) float() float32 { return float32(float64(m.u32()) * 2.3283064365386963e-10) }

// positive is BDS's unsigned next value.
func (m *mt19937) positive() int32 { return int32(m.u32() >> 1) } //nolint:gosec // one bit is shifted out

// EnchantOption is one of the three choices of an enchanting table.
type EnchantOption struct {
	// Cost is the level the player must have. The level paid is the option's place, 1 to 3.
	Cost int32
	// Slot is the item's enchant slot.
	Slot uint32
	// Enchants are what it adds, in the three activation lists.
	Enchants [enchantActivations][]EnchantLevel
	// Name is the words the client shows in the table's script.
	Name string
	// NetworkID is what the client names the option by.
	NetworkID uint32
}

// EnchantOptionsFunc makes a table's options for an item. The list may hold any number,
// though the client's screen shows three.
type EnchantOptionsFunc func(p *Player, item ItemStack, shelves int32) []EnchantOption

// EnchantRequirement is what picking one of a table's options asks of a player.
type EnchantRequirement struct {
	// Refuse turns the player away whatever it has.
	Refuse bool
	// MinLevel is the level it must have, Levels what is taken. Lapis is what the material
	// slot must hold and what is taken from it.
	MinLevel, Levels, Lapis int32
}

// EnchantRequireFunc says what picking an option asks of p; place is its place, 1 to 3.
type EnchantRequireFunc func(p *Player, option EnchantOption, place int32) EnchantRequirement

// BookshelvesFunc counts the shelves powering the table at pos.
type BookshelvesFunc func(p *Player, r *world.Region, pos cube.Pos) int32

// AnvilFunc works out what an anvil makes of first and second, with name its new name
// ("" to leave off). A result with no item means it makes nothing.
type AnvilFunc func(p *Player, first, second ItemStack, name string) AnvilResult

// GrindstoneFunc works out what a grindstone makes of first and second. A result with no
// item means it makes nothing.
type GrindstoneFunc func(p *Player, first, second ItemStack) GrindstoneResult

// EnchantOptionsEvent fires when a table has worked out its options for an item. Change
// Options to show others. Runs: owner of the player's entity.
type EnchantOptionsEvent struct {
	Player  *Player
	Item    ItemStack
	Options []EnchantOption
}

// OnEnchantOptions listens to a table's options being worked out.
func OnEnchantOptions(s *Setup, fn func(e *EnchantOptionsEvent), p Priority) error {
	return addListener(s, &s.h.game.enchantOptionsEv, fn, p)
}

// EnchantEvent fires when a player picks an option that passed BDS's checks. Cancel
// refuses it. Change Result to give another item, Levels and Lapis to take others. Runs:
// owner of the player's entity.
type EnchantEvent struct {
	Cancellable
	Player *Player
	Option EnchantOption
	Item   ItemStack
	Result ItemStack
	// Levels and Lapis are 0 in creative, else the option's place.
	Levels, Lapis int32
}

// OnEnchant listens to players enchanting.
func OnEnchant(s *Setup, fn func(e *EnchantEvent), p Priority) error {
	return addListener(s, &s.h.game.enchantEv, fn, p)
}

// enchantState is the player's enchanting table: BDS's seed, which the options come from
// and which changes each time the player enchants, and the options on show.
type enchantState struct {
	seed    int32
	seeded  bool
	input   int32
	options []EnchantOption
}

// craftAfter is what a craft does once its whole request has passed.
type craftAfter struct {
	// levels are taken from the player.
	levels int32
	// newSeed gives the player a new enchanting seed.
	newSeed bool
	// experience is spawned where the player is.
	experience int32
	// settle sets the material slot to materialLeft: a plugin changed what is taken, so
	// what the client used is not the answer.
	settle       bool
	materialLeft uint8
	// anvilWear gives the anvil on show its chance to wear.
	anvilWear bool
}

// afterCraft does what the request's craft left to do.
func (p *Player) afterCraft(a craftAfter) {
	if a.levels != 0 {
		p.SetLevel(p.Level()-a.levels, p.ExperienceProgress())
	}
	if a.settle {
		lapis := &p.inventory.ui[enchantLapisSlot]
		if a.materialLeft == 0 {
			*lapis = ItemStack{}
		} else if !lapis.Empty() {
			lapis.Count = a.materialLeft
		}
	}
	if a.newSeed {
		p.enchant.seed, p.enchant.seeded = newMT(rand.Uint32()).positive(), true //nolint:gosec // a table's draw, not a secret
	}
	if a.anvilWear && p.entity != nil {
		if r := p.entity.Region(); r != nil {
			p.s.game.sys.AnvilWear(p, r, p.window.pos)
		}
	}
	if a.experience > 0 && p.entity != nil {
		if r := p.entity.Region(); r != nil {
			p.s.SpawnExperience(r, p.entity.Position(), a.experience)
		}
	}
}

// enchantTableEntity is an enchanting table's block entity. The client draws the table's
// shape and book from it, so a table without one looks like a broken block.
type enchantTableEntity struct {
	// saved keeps the fields of a loaded table (CustomName, rott...), written back as
	// they were. BDS's tick only turns the book, so rott stays what it was.
	saved map[string]any
}

// SaveData is what BDS saves of a table.
func (e *enchantTableEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1), enchantTableRotation: float32(0)}
	}
	m["id"] = enchantTableEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	return m
}

// NetworkData is the same: BDS sends a table as it saves it.
func (e *enchantTableEntity) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// useEnchantingTable is what clicking an enchanting table does.
func (p *Player) useEnchantingTable(r *world.Region, u *BlockUse) bool {
	if p.openStation(r, u, ContainerEnchantingTable, byte(protocol.ContainerTypeEnchantment)) {
		p.enchant.input, p.enchant.options = -1, nil
		p.refreshEnchantOptions()
		p.flush()
	}
	return true
}

// holds reports whether the open window has a container's slot.
func (w *windowState) holds(container, slot byte) bool {
	switch container {
	case protocol.ContainerEnchantingInput:
		return w.kind == ContainerEnchantingTable && slot == enchantInputSlot
	case protocol.ContainerEnchantingMaterial:
		return w.kind == ContainerEnchantingTable && slot == enchantLapisSlot
	case protocol.ContainerAnvilInput:
		return w.kind == ContainerAnvil && slot == anvilFirstSlot
	case protocol.ContainerAnvilMaterial:
		return w.kind == ContainerAnvil && slot == anvilSecondSlot
	case protocol.ContainerGrindstoneInput:
		return w.kind == ContainerGrindstone && slot == grindstoneFirstSlot
	case protocol.ContainerGrindstoneAdditional:
		return w.kind == ContainerGrindstone && slot == grindstoneSecondSlot
	case protocol.ContainerLoomInput:
		return w.kind == ContainerLoom && slot == loomBannerSlot
	case protocol.ContainerLoomDye:
		return w.kind == ContainerLoom && slot == loomDyeSlot
	case protocol.ContainerLoomMaterial:
		return w.kind == ContainerLoom && slot == loomMaterialSlot
	case protocol.ContainerStonecutterInput:
		return w.kind == ContainerStonecutter && slot == stonecutterSlot
	}
	return false
}

// refreshEnchantOptions works out and sends the options when the table's item changed,
// as BDS's container does on a slot change.
func (p *Player) refreshEnchantOptions() {
	if p.window.kind != ContainerEnchantingTable || p.entity == nil {
		return
	}
	item := p.inventory.ui[enchantInputSlot]
	if p.enchant.input == item.netID {
		return
	}
	p.enchant.input = item.netID
	var opts []EnchantOption
	if r := p.entity.Region(); r != nil && !item.Empty() {
		if !p.enchant.seeded {
			p.enchant.seed, p.enchant.seeded = int32(rand.Uint32()>>1), true //nolint:gosec // a table's draw, not a secret
		}
		opts = p.s.game.sys.EnchantOptions(p, item, p.s.game.sys.Bookshelves(p, r, p.window.pos))
	}
	if g := p.s.game; g.enchantOptionsEv.Has() {
		e := EnchantOptionsEvent{Player: p, Item: item, Options: opts}
		g.enchantOptionsEv.fire(p.s.log, &e)
		opts = e.Options
	}
	p.enchant.options = opts
	p.sendEnchantOptions(opts)
}

// network is the option as the packet carries it.
func (o EnchantOption) network() protocol.EnchantmentOption {
	out := protocol.EnchantmentOption{
		Cost: uint8(max(min(o.Cost, 255), 0)), Name: o.Name, RecipeNetworkID: o.NetworkID, //nolint:gosec // clamped
		Enchantments: protocol.ItemEnchantments{Slot: int32(o.Slot)}, //nolint:gosec // BDS keeps it as the same bits
	}
	for i, list := range o.Enchants {
		for _, v := range list {
			out.Enchantments.Enchantments[i] = append(out.Enchantments.Enchantments[i], protocol.EnchantmentInstance{Type: byte(v.ID), Level: byte(v.Level)}) //nolint:gosec // the wire holds a byte
		}
	}
	return out
}

// countBookshelves is BDS's count around a table: each of the 8 spots around it must be
// clear on both levels, and then the shelves behind it count on both levels, with the
// two beside the corner for a corner spot.
func (s *Host) countBookshelves(r *world.Region, at cube.Pos) int32 {
	en := s.game.rules()
	name := func(x, y, z int) string {
		b, ok := r.Block(cube.Pos{at[0] + x, at[1] + y, at[2] + z}, 0)
		if !ok {
			return ""
		}
		return s.reg.Blocks.Name(b)
	}
	var n int32
	count := func(x, z int) {
		for y := range 2 {
			if name(x, y, z) == en.bookshelf {
				n++
			}
		}
	}
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dz == 0 || !en.transmit[name(dx, 0, dz)] || !en.transmit[name(dx, 1, dz)] {
				continue
			}
			count(2*dx, 2*dz)
			if dx != 0 && dz != 0 {
				count(2*dx, dz)
				count(dx, 2*dz)
			}
		}
	}
	return n
}

// vanillaBookshelves is BDS's count around a table.
func vanillaBookshelves(p *Player, r *world.Region, pos cube.Pos) int32 {
	return p.s.countBookshelves(r, pos)
}

// optionCosts is BDS's sum of a table's three costs; limit is the most shelves that count.
func optionCosts(m *mt19937, shelves, limit int32) [enchantOptionCount]int32 {
	b := min(shelves, limit)
	base := m.intN(8) + b>>1
	base += m.intN(b + 1)
	return [enchantOptionCount]int32{max((base+1)/3, 1), (2*base+2)/3 + 1, max(base+1, 2*b)}
}

// vanillaEnchantOptions is BDS's options: nothing for an item already enchanted or one
// that takes none; else three costs and a draw for each, all from one Random seeded by
// the player's seed, so the same item shows the same options until the player enchants.
func vanillaEnchantOptions(p *Player, item ItemStack, shelves int32) []EnchantOption {
	g := p.s.game
	if item.Empty() || item.Tag[enchantTag] != nil || g.rules().items[item.Name].Value <= 0 {
		return nil
	}
	m := newMT(uint32(p.enchant.seed)) //nolint:gosec // BDS seeds with the same bits
	costs := optionCosts(m, shelves, int32(p.Tuning(TuneEnchantMaxShelves)))
	opts := make([]EnchantOption, 0, enchantOptionCount)
	var name strings.Builder
	for _, cost := range costs {
		l := g.drawEnchants(m, item.Name, cost, 0, false)
		words := m.intN(optionWordKinds) + optionWordsMin
		name.Reset()
		for range words {
			name.WriteString(g.rules().words[m.intN(int32(len(g.rules().words)))]) //nolint:gosec // a short list
		}
		opts = append(opts, EnchantOption{Cost: cost, Slot: l.slot, Enchants: l.lists, Name: name.String(), NetworkID: p.s.enchantIDs.Add(1) + enchantIDBase})
	}
	return opts
}

// drawEntry is an enchantment in a draw's pool.
type drawEntry struct {
	v      EnchantLevel
	weight int32
}

// drawEnchants is BDS's draw for an item: its enchant value and the cost set a power;
// every enchantment the item takes goes in the pool at the highest level that power
// reaches; one is picked by weight, then more while chance allows, each only if it fits
// with those picked. BDS never reads the maximum cost here.
func (g *gameplay) drawEnchants(m *mt19937, item string, cost, bonus int32, treasure bool) enchantList {
	it := g.rules().items[item]
	l := enchantList{slot: it.Slot}
	if it.Slot == 0 || bonus+it.Value <= 0 {
		return l
	}
	span := (bonus+it.Value)>>2 + 1
	a := m.intN(span)
	b := m.intN(span)
	spread := m.float()*drawSpread - drawSpreadBase + 1
	power := max(int32(spread*float32(a+cost+b+1)+0.5), 1)
	pool := g.enchantPool(it.Slot, power, treasure)
	n := len(pool)
	if n == 0 {
		return l
	}
	var total int32
	for _, e := range pool {
		total += e.weight
	}
	pool, total = pickDraw(g, m, &l, pool, total)
	for i := 1; m.intN(drawMoreSpan) <= power && i < n; i++ {
		pool, total = pickDraw(g, m, &l, pool, total)
		power = 4*power/5 + 1
	}
	return l
}

// enchantPool is what a draw can pick from at power.
func (g *gameplay) enchantPool(slot uint32, power int32, treasure bool) []drawEntry {
	var pool []drawEntry
	for _, e := range g.enchants.Load().order {
		if e.Disabled || slot != allSlots && slot&e.Slots == 0 && slot&e.SecondarySlots == 0 {
			continue
		}
		if e.Treasure && !treasure || !e.Discoverable {
			continue
		}
		if e.MinCost == nil {
			continue
		}
		at := -1
		for level := e.MinLevel; level <= e.MaxLevel && power >= e.MinCost(level); level++ {
			if at >= 0 {
				pool[at].v.Level = int16(level) //nolint:gosec // levels are small
				continue
			}
			at = len(pool)
			pool = append(pool, drawEntry{v: EnchantLevel{ID: e.ID, Level: int16(level)}, weight: drawWeight(e.Frequency)}) //nolint:gosec // levels are small
		}
	}
	return pool
}

// drawWeight is what a frequency is worth in a draw.
func drawWeight(frequency int32) int32 {
	switch frequency {
	case freqVeryRare:
		return 1
	case freqRare:
		return 2
	case freqUncommon:
		return 5
	case freqCommon:
		return 10
	}
	return 0
}

// pickDraw picks one entry by weight, adds it if it fits, and takes it out of the pool.
// With nothing left to weigh it takes the first and leaves the pool.
func pickDraw(g *gameplay, m *mt19937, l *enchantList, pool []drawEntry, total int32) ([]drawEntry, int32) {
	left := m.intN(total)
	for i, e := range pool {
		if left -= e.weight; left < 0 {
			g.add(l, e.v)
			return append(pool[:i], pool[i+1:]...), total - e.weight
		}
	}
	if len(pool) > 0 {
		g.add(l, pool[0].v)
	}
	return pool, total
}

// EnchantItem gives s what a draw at cost gives it, as BDS's loot functions do; treasure
// lets treasure enchantments in. A book becomes an enchanted book. Runs: anywhere.
func (s *Host) EnchantItem(it ItemStack, cost, bonus int32, treasure bool) ItemStack {
	if it.Empty() {
		return it
	}
	g := s.game
	g.applyEnchants(&it, g.drawEnchants(newMT(rand.Uint32()), it.Name, cost, bonus, treasure)) //nolint:gosec // a draw, not a secret
	return it
}

// vanillaEnchantRequire is BDS's requirement: outside creative the level must reach the
// option's cost, and its place is both the lapis and the levels taken.
func vanillaEnchantRequire(p *Player, option EnchantOption, place int32) EnchantRequirement {
	if p.creative() {
		return EnchantRequirement{}
	}
	return EnchantRequirement{MinLevel: option.Cost, Levels: place, Lapis: place}
}

// craftEnchant is BDS's enchanting action: the option must be on show and the item there,
// then Systems.EnchantRequire says what it asks of the player.
func (p *Player) craftEnchant(a *protocol.CraftRecipeStackRequestAction) (uint8, []slotRef) {
	st := &p.requests
	c := &st.craft
	if c.active {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	index := p.enchantOptionIndex(a.RecipeNetworkID)
	if index < 0 {
		return protocol.ItemStackResponseStatusFailedToFindRecipeByNetId, nil
	}
	option := p.enchant.options[index]
	item, lapis := st.work.ui[enchantInputSlot], st.work.ui[enchantLapisSlot]
	if item.Empty() {
		return protocol.ItemStackResponseStatusMissingInputItem, nil
	}
	// The options are the item's they were made for. A request that first swaps another
	// item in would put them on that one; a client's own request never does, as it only
	// sees new options once the server has answered.
	if item.netID != p.enchant.input {
		return protocol.ItemStackResponseStatusFailedToEnchant, nil
	}
	place := int32(index) + 1 //nolint:gosec // a short list
	req := p.s.game.sys.EnchantRequire(p, option, place)
	if status := p.checkEnchantRequirement(req, lapis); status != protocol.ItemStackResponseStatusOK {
		return status, nil
	}
	result, levels, taken, ok := p.enchantResult(option, item, req)
	if !ok {
		return protocol.ItemStackResponseStatusFailedToEnchant, nil
	}
	if result.Empty() {
		return protocol.ItemStackResponseStatusInvalidCraftResultItem, nil
	}
	c.active, c.times = true, 1
	c.after.newSeed, c.after.levels = true, levels
	c.slots = append(c.slots, expectedSlot{container: protocol.ContainerEnchantingInput, slot: enchantInputSlot, item: item, left: 1})
	p.expectLapis(lapis, taken, place)
	c.results = append(c.results, craftResult{stack: result, left: int32(result.Count)})
	// The response only carries a count and an ID, so the client is sent the item the
	// server made rather than left with what it worked out itself.
	st.refresh = true
	return protocol.ItemStackResponseStatusOK, nil
}

// enchantOptionIndex is the index of the option on show with the network ID id, or -1.
func (p *Player) enchantOptionIndex(id uint32) int {
	for i, o := range p.enchant.options {
		if o.NetworkID == id {
			return i
		}
	}
	return -1
}

// checkEnchantRequirement checks the player meets req: not refused, level enough and the
// lapis there.
func (p *Player) checkEnchantRequirement(req EnchantRequirement, lapis ItemStack) uint8 {
	switch {
	case req.Refuse:
		return protocol.ItemStackResponseStatusFailedToEnchant
	case req.MinLevel > p.Level():
		return protocol.ItemStackResponseStatusInsufficientPlayerLevelToEnchant
	case req.Lapis > 0 && (lapis.Empty() || req.Lapis > int32(lapis.Count)):
		return protocol.ItemStackResponseStatusFailedToEnchant
	}
	return protocol.ItemStackResponseStatusOK
}

// enchantResult is the enchanted item, the levels it costs and the lapis taken, after the
// OnEnchant listeners changed them. False if a listener cancelled.
func (p *Player) enchantResult(option EnchantOption, item ItemStack, req EnchantRequirement) (result ItemStack, levels, lapis int32, ok bool) {
	g := p.s.game
	result = item
	result.Count = 1
	g.applyEnchants(&result, enchantList{lists: option.Enchants})
	levels, lapis = req.Levels, req.Lapis
	if !g.enchantEv.Has() {
		return result, levels, lapis, true
	}
	e := EnchantEvent{Player: p, Option: option, Item: item, Result: result, Levels: levels, Lapis: lapis}
	g.enchantEv.fire(p.s.log, &e)
	if e.Cancelled() {
		return result, levels, lapis, false
	}
	if !sameStack(e.Result, result) {
		p.requests.refresh = true
	}
	return e.Result, e.Levels, e.Lapis, true
}

// expectLapis records what the craft takes from the lapis slot. The client takes what
// vanilla would (place, none in creative), so when taken differs it may use anything up
// to the whole slot; the slot is then set to what was asked and sent again.
func (p *Player) expectLapis(lapis ItemStack, taken, place int32) {
	st := &p.requests
	c := &st.craft
	clientPays := place
	if p.creative() {
		clientPays = 0
	}
	taken = max(min(taken, int32(lapis.Count)), 0)
	switch {
	case taken != clientPays:
		if !lapis.Empty() {
			c.slots = append(c.slots, expectedSlot{container: protocol.ContainerEnchantingMaterial, slot: enchantLapisSlot, item: lapis, left: lapis.Count, upTo: true})
		}
		c.after.settle, c.after.materialLeft = true, lapis.Count-uint8(taken) //nolint:gosec // clamped to the slot
		st.refresh = true
	case taken > 0:
		c.slots = append(c.slots, expectedSlot{container: protocol.ContainerEnchantingMaterial, slot: enchantLapisSlot, item: lapis, left: uint8(taken)}) //nolint:gosec // clamped to the slot
	}
}

// EnchantSeed returns the seed the player's table options come from. Runs: the player's
// region.
func (p *Player) EnchantSeed() int32 { return p.enchant.seed }

// SetEnchantSeed sets the seed and works out a table's options again if one is open.
// Runs: the player's region.
func (p *Player) SetEnchantSeed(seed int32) {
	p.enchant.seed, p.enchant.seeded = seed, true
	p.enchant.input = -1
	p.refreshEnchantOptions()
}

// EnchantOptions returns a copy of the options on show. Runs: the player's region.
func (p *Player) EnchantOptions() []EnchantOption { return slices.Clone(p.enchant.options) }

// SetEnchantOptions shows other options on the player's open table, until the item in it
// changes. An option with no NetworkID gets one. Runs: the player's region.
func (p *Player) SetEnchantOptions(opts []EnchantOption) {
	opts = slices.Clone(opts)
	for i := range opts {
		if opts[i].NetworkID == 0 {
			opts[i].NetworkID = p.s.enchantIDs.Add(1) + enchantIDBase
		}
	}
	p.enchant.options = opts
	p.sendEnchantOptions(opts)
}

// sendEnchantOptions tells the client what its table shows.
func (p *Player) sendEnchantOptions(opts []EnchantOption) {
	pk := &packet.PlayerEnchantOptions{Options: make([]protocol.EnchantmentOption, len(opts))}
	for i, o := range opts {
		pk.Options[i] = o.network()
	}
	p.send(pk)
}

// playSound plays a level sound where the player is, for those who hear it.
func (p *Player) playSound(sound string) {
	if p.entity == nil {
		return
	}
	if r := p.entity.Region(); r != nil {
		pos := p.entity.Position()
		playSound(r, p.blockPos(), soundEvent{name: sound, pos: pos, extra: -1}, nil)
	}
}
