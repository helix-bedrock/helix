package player

import (
	"maps"
	"slices"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol/packet"
)

// BDS 1.26.60's enchantment numbers.
const (
	// enchantActivations is how many lists an item keeps its enchantments in.
	enchantActivations = 3
	// enchantTag, enchantID and enchantLevel are how an item saves them.
	enchantTag   = "ench"
	enchantID    = "id"
	enchantLevel = "lvl"
	// repairCostTag is what using an anvil has added to the item's price.
	repairCostTag = "RepairCost"
	// itemBook becomes itemEnchantedBook once it has an enchantment.
	itemBook          = "minecraft:book"
	itemEnchantedBook = "minecraft:enchanted_book"
	itemLapis         = "minecraft:lapis_lazuli"
	// allSlots is the slot of an item that takes any enchantment.
	allSlots = 0xFFFFFFFF

	breachPerLevel  = 0.15
	thornsPerLevel  = 0.15
	thornsFixedFrom = 11
	thornsFixedBase = 10
	thornsRandom    = 4
	smiteLikeDamage = 2.5
	densityPerFall  = 0.5
	damageThorns    = DamageCause(18)
	// The frequencies an enchantment can have, with what each is worth to a table's
	// draw and on an anvil.
	freqVeryRare = 1
	freqRare     = 3
	freqUncommon = 10
	freqCommon   = 30
)

// The enchantments vanilla's systems read by ID (BDS 1.26.60).
const (
	EnchantThorns     int16 = 5
	EnchantImpaling   int16 = 29
	EnchantRiptide    int16 = 30
	EnchantChanneling int16 = 32
	EnchantMultishot  int16 = 33
	EnchantPiercing   int16 = 34
	EnchantDensity    int16 = 39
	EnchantBreach     int16 = 40
	EnchantMending    int16 = 26
)

// Enchantment is an enchantment's entry. Vanilla's carry BDS 1.26.60's numbers, on
// this same struct; plugins change them or add their own, before the server starts
// through Setup, or while it runs through Host.EditEnchantment.
type Enchantment struct {
	// ID is the ID saved in an item's "ench" list.
	ID   int16
	Name string
	// Translation is the key of the name the client shows.
	Translation string
	// Frequency is how often it comes up: BDS's are 1, 3, 10 and 30. It sets the weight
	// in a table's draw and the anvil's price.
	Frequency int32
	// Lootable lets it be picked for a random enchanted book.
	Lootable bool
	// Slots and SecondarySlots are the item kinds it goes on, as bits.
	Slots, SecondarySlots uint32
	// Group is the set it excludes itself from; 0 is none.
	Group int32
	// Activation is which of an item's three lists it is kept in.
	Activation uint8
	// MinLevel and MaxLevel bound a level from a table. A higher level on an item still
	// counts wherever a level is read.
	MinLevel, MaxLevel int32
	// MinCost and MaxCost are the enchanting power a level takes. A table's draw reads
	// MinCost, as BDS does; grindstones pay it back.
	MinCost, MaxCost func(level int32) int32
	// Compatible reports whether it may share an item with other. Nil is the group rule.
	Compatible func(self, other *Enchantment) bool
	// Treasure keeps it off tables unless asked for; Discoverable false always does.
	Treasure, Discoverable bool
	// Disabled takes it out of every draw.
	Disabled bool

	// ToolSpeed changes the held tool's speed when the enchantment is on it
	// at level. Nil changes nothing.
	ToolSpeed func(b *BreakInfo, level int16, speed float32) float32
	// Underwater, on the worn helmet, lifts the under-water slowdown.
	Underwater bool
	// DamageChance is the percent chance, at level, that an item with the
	// enchantment takes damage when it would (Unbreaking). Nil always
	// takes it.
	DamageChance func(level int16) int32
	// Melee is the damage the enchantment adds to attacker's hit on target. Nil adds none.
	Melee func(attacker *Player, level int16, target *Player) float32
	// Protection is what the enchantment, on worn armour, counts for against
	// damage of cause. Nil counts for nothing.
	Protection func(level int16, cause DamageCause) int32
	// Breach changes the share of a hit the target's armour takes off, for an
	// enchantment on the attacker's held item. Nil changes nothing.
	Breach func(level int16, share float32) float32
	// AfterHurt runs for each worn piece with the enchantment when attacker hits wearer.
	AfterHurt func(wearer, attacker *Player, level int16)
	// Mends lets an item with it drink experience as it is picked up.
	Mends bool
	// Knockback makes its level count toward a hit's extra knockback.
	Knockback bool
	// SilkTouch and Fortune make its level count in what a broken block drops.
	SilkTouch, Fortune bool
	// StaysWorn keeps armour with it on a player out of creative.
	StaysWorn bool
	// VanishesOnDeath destroys an item with it when the player dies keeping its items.
	VanishesOnDeath bool
	// Curse stays on through a grindstone and pays nothing back.
	Curse bool
}

// compatibleWith is the enchantment's rule against other.
func (e *Enchantment) compatibleWith(other *Enchantment) bool {
	if e.Compatible != nil {
		return e.Compatible(e, other)
	}
	return groupCompatible(e, other)
}

// groupCompatible is BDS's base rule: no group, or not the same one.
func groupCompatible(e, other *Enchantment) bool { return e.Group == 0 || e.Group != other.Group }

// enchantTable is the registered enchantments: by ID, and in ID order for a draw.
type enchantTable struct {
	byID  map[int16]*Enchantment
	order []*Enchantment
}

func newEnchantTable(all map[int16]*Enchantment) *enchantTable {
	t := &enchantTable{byID: maps.Clone(all), order: make([]*Enchantment, 0, len(all))}
	for _, e := range all {
		t.order = append(t.order, e)
	}
	slices.SortFunc(t.order, func(a, b *Enchantment) int { return int(a.ID) - int(b.ID) })
	return t
}

// itemHas reports whether an enchantment on s is registered and passes want. A removed
// enchantment counts for nothing.
func (g *gameplay) itemHas(s ItemStack, want func(e *Enchantment) bool) bool {
	found := false
	enchantLevels(s, func(id, _ int16) {
		if e, ok := g.enchant(id); ok && want(e) {
			found = true
		}
	})
	return found
}

// rules is what enchanting reads about items and blocks. Runs: anywhere.
func (g *gameplay) rules() *enchanting {
	if r := g.enchantRules.Load(); r != nil {
		return r
	}
	return newEnchanting()
}

// enchant returns the enchantment with id. Runs: anywhere.
func (g *gameplay) enchant(id int16) (*Enchantment, bool) {
	t := g.enchants.Load()
	if t == nil {
		return nil, false
	}
	e, ok := t.byID[id]
	return e, ok
}

// Enchantment returns a copy of the enchantment with id as it is now. Runs: anywhere.
func (s *Host) Enchantment(id int16) (Enchantment, bool) {
	e, ok := s.game.enchant(id)
	if !ok {
		return Enchantment{}, false
	}
	return *e, true
}

// Enchantments returns a copy of every registered enchantment, in ID order. Runs: anywhere.
func (s *Host) Enchantments() []Enchantment {
	t := s.game.enchants.Load()
	out := make([]Enchantment, len(t.order))
	for i, e := range t.order {
		out[i] = *e
	}
	return out
}

// EditEnchantment changes the enchantment with id while the server runs. fn gets a
// copy, and may run more than once if others edit at the same time. Runs: anywhere.
func (s *Host) EditEnchantment(id int16, fn func(e *Enchantment)) bool {
	for {
		old := s.game.enchants.Load()
		cur, ok := old.byID[id]
		if !ok {
			return false
		}
		e := *cur
		fn(&e)
		e.ID = id
		next := maps.Clone(old.byID)
		next[id] = &e
		if s.game.enchants.CompareAndSwap(old, newEnchantTable(next)) {
			return true
		}
	}
}

// SetEnchantment adds an enchantment or replaces the one with its ID while the server
// runs. Runs: anywhere.
func (s *Host) SetEnchantment(e Enchantment) {
	for {
		old := s.game.enchants.Load()
		next := maps.Clone(old.byID)
		next[e.ID] = &e
		if s.game.enchants.CompareAndSwap(old, newEnchantTable(next)) {
			return
		}
	}
}

// RemoveEnchantment takes the enchantment with id out while the server runs. Items
// keep it saved, where nothing reads it. Runs: anywhere.
func (s *Host) RemoveEnchantment(id int16) {
	for {
		old := s.game.enchants.Load()
		next := maps.Clone(old.byID)
		delete(next, id)
		if s.game.enchants.CompareAndSwap(old, newEnchantTable(next)) {
			return
		}
	}
}

// enchanting is what enchanting reads about items and blocks, set up at boot.
type enchanting struct {
	items     map[string]data.EnchantItem
	repair    map[string]map[string]bool
	transmit  map[string]bool
	bookshelf string
	words     []string
	// material is what a table's lapis slot takes; "" takes anything.
	material string
	// repairable are the items with BDS's repairable component, which work out repairs
	// through their own rules.
	repairable map[string]*repairRules
}

func newEnchanting() *enchanting {
	return &enchanting{items: map[string]data.EnchantItem{}, repair: map[string]map[string]bool{}, transmit: map[string]bool{}, repairable: map[string]*repairRules{}}
}

// clone is a copy that can be changed without touching what others read.
func (r *enchanting) clone() *enchanting {
	c := *r
	c.items, c.repair, c.transmit = maps.Clone(r.items), maps.Clone(r.repair), maps.Clone(r.transmit)
	return &c
}

// setItem, setRepair, setPath are one rule's change, for boot and while running.
func (r *enchanting) setItem(item string, slot uint32, value int32) {
	if slot == 0 && value == 0 {
		delete(r.items, item)
		return
	}
	r.items[item] = data.EnchantItem{Slot: slot, Value: value}
}

func (r *enchanting) setRepair(item string, materials []string) {
	if len(materials) == 0 {
		delete(r.repair, item)
		return
	}
	r.repair[item] = set(materials)
}

func (r *enchanting) setPath(block string, passes bool) {
	if passes {
		r.transmit[block] = true
	} else {
		delete(r.transmit, block)
	}
}

// editRules swaps in changed rules. Runs: anywhere.
func (s *Host) editRules(fn func(r *enchanting)) {
	for {
		old := s.game.enchantRules.Load()
		next := old.clone()
		fn(next)
		if s.game.enchantRules.CompareAndSwap(old, next) {
			return
		}
	}
}

// SetItemEnchantable is Setup.SetItemEnchantable while running. Runs: anywhere.
func (s *Host) SetItemEnchantable(item string, slot uint32, value int32) {
	s.editRules(func(r *enchanting) { r.setItem(item, slot, value) })
}

// SetItemRepair is Setup.SetItemRepair while running. Runs: anywhere.
func (s *Host) SetItemRepair(item string, materials ...string) {
	s.editRules(func(r *enchanting) { r.setRepair(item, materials) })
}

// SetEnchantPathBlock is Setup.SetEnchantPathBlock while running. Runs: anywhere.
func (s *Host) SetEnchantPathBlock(block string, passes bool) {
	s.editRules(func(r *enchanting) { r.setPath(block, passes) })
}

// SetEnchantBookshelf is Setup.SetEnchantBookshelf while running. Runs: anywhere.
func (s *Host) SetEnchantBookshelf(block string) {
	s.editRules(func(r *enchanting) { r.bookshelf = block })
}

// SetEnchantMaterial is Setup.SetEnchantMaterial while running. Runs: anywhere.
func (s *Host) SetEnchantMaterial(item string) {
	s.editRules(func(r *enchanting) { r.material = item })
}

// RemoveEnchantment takes a registered enchantment out, vanilla's too: no table offers
// it, and on items it counts for nothing. Not usable after the Setup froze.
func (s *Setup) RemoveEnchantment(id int16) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	delete(s.h.game.enchants, id)
	return nil
}

// SetEnchantBookshelf sets the block that powers an enchanting table. Not usable after
// the Setup froze.
func (s *Setup) SetEnchantBookshelf(block string) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.enchanting.bookshelf = block
	return nil
}

// SetEnchantMaterial sets the item a table's lapis slot takes; "" takes anything. Not
// usable after the Setup froze.
func (s *Setup) SetEnchantMaterial(item string) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.enchanting.material = item
	return nil
}

// SetItemEnchantable sets which enchantments an item takes (a bit set of slots) and how
// well (a table's power rises with value). Zero for both takes it off tables. Not
// usable after the Setup froze.
func (s *Setup) SetItemEnchantable(item string, slot uint32, value int32) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.enchanting.setItem(item, slot, value)
	return nil
}

// SetItemRepair sets what repairs an item on an anvil. Not usable after the Setup froze.
func (s *Setup) SetItemRepair(item string, materials ...string) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.enchanting.setRepair(item, materials)
	return nil
}

// SetEnchantPathBlock sets whether bookshelf power passes through a block on its way to
// an enchanting table. Not usable after the Setup froze.
func (s *Setup) SetEnchantPathBlock(block string, passes bool) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.enchanting.setPath(block, passes)
	return nil
}

// vanillaEnchanting registers BDS's enchantments from helix-data and what they do.
func vanillaEnchanting(s *Setup) error {
	d, err := data.EnchantingData()
	if err != nil {
		return err
	}
	rep := make(map[string]map[string]bool, len(d.Repair))
	for item, materials := range d.Repair {
		rep[item] = set(materials)
	}
	rules, err := loadRepairRules()
	if err != nil {
		return err
	}
	s.h.game.enchanting = &enchanting{
		items: d.Items, repair: rep, transmit: set(d.Transmit), bookshelf: d.Bookshelf, words: d.OptionWords, repairable: rules, material: itemLapis,
	}
	for _, v := range d.Enchantments {
		e := enchantmentFromData(v)
		vanillaEnchantHooks(&e)
		if err := s.RegisterEnchantment(e); err != nil {
			return err
		}
	}
	return nil
}

// enchantmentFromData builds an entry from BDS's table.
func enchantmentFromData(v data.Enchantment) Enchantment {
	return Enchantment{
		ID: v.ID, Name: v.Name, Translation: v.Translation, Frequency: v.Frequency, Lootable: v.Lootable,
		Slots: v.Primary, SecondarySlots: v.Secondary, Group: v.Group, Activation: v.Activation,
		MinLevel: 1, MaxLevel: v.MaxLevel, MinCost: linearCost(v.MinCost), MaxCost: linearCost(v.MaxCost),
		Treasure: v.Treasure, Discoverable: v.Discoverable,
	}
}

// linearCost is a cost of a + b*level.
func linearCost(c [2]int32) func(level int32) int32 {
	return func(level int32) int32 { return c[0] + c[1]*level }
}

// vanillaEnchantHooks gives BDS's enchantments what they do. Those of systems not built
// yet (bows, tridents, burning, swimming...) keep their numbers and do nothing until
// those systems read them.
func vanillaEnchantHooks(e *Enchantment) {
	switch e.ID {
	case EnchantProtection:
		e.Protection = protectionKind(protectAll)
	case EnchantFireProtection:
		e.Protection = protectionKind(protectFire)
	case EnchantFeatherFalling:
		e.Protection = protectionKind(protectFall)
	case EnchantBlastProtection:
		e.Protection = protectionKind(protectBlast)
	case EnchantProjectileProtection:
		e.Protection = protectionKind(protectProjectile)
	case EnchantThorns:
		e.AfterHurt = thornsAfterHurt
	case EnchantAquaAffinity:
		e.Underwater = true
	case EnchantSharpness:
		e.Melee = sharpnessDamage
	case EnchantEfficiency:
		e.ToolSpeed = efficiencySpeed
	case EnchantUnbreaking:
		e.DamageChance = unbreakingChance
	case EnchantMending:
		e.Mends = true
	case EnchantKnockback:
		e.Knockback = true
	case enchantSilkTouch:
		e.SilkTouch = true
	case enchantFortune:
		e.Fortune = true
	case enchantBinding:
		e.StaysWorn, e.Curse = true, true
	case enchantVanishing:
		e.VanishesOnDeath, e.Curse = true, true
	case EnchantImpaling:
		e.Melee = impalingDamage
	case EnchantDensity:
		e.Melee = densityDamage
	case EnchantBreach:
		e.Breach = breachShare
	case EnchantRiptide:
		e.Compatible = excludes(EnchantChanneling)
	case EnchantChanneling:
		e.Compatible = excludes(EnchantRiptide)
	case EnchantMultishot:
		e.Compatible = excludes(EnchantPiercing)
	case EnchantPiercing:
		e.Compatible = excludes(EnchantMultishot)
	}
}

// excludes is BDS's group rule plus one more enchantment that cannot share an item.
func excludes(id int16) func(self, other *Enchantment) bool {
	return func(self, other *Enchantment) bool { return groupCompatible(self, other) && other.ID != id }
}

// impalingDamage is BDS's Impaling on a target in water. BDS also counts rain, which is
// not built.
func impalingDamage(_ *Player, level int16, target *Player) float32 {
	if target == nil || !target.move.inWater {
		return 0
	}
	return float32(level) * smiteLikeDamage
}

// densityDamage is BDS's Density: more the further the attacker has fallen.
func densityDamage(attacker *Player, level int16, _ *Player) float32 {
	return float32(level) * attacker.FallDistance() * densityPerFall
}

// breachShare is BDS's Breach: armour's share falls by its level.
func breachShare(level int16, share float32) float32 {
	return max(min(share-breachPerLevel*float32(level), 1), 0)
}

// thornsAfterHurt is BDS's Thorns: a chance by level to hurt the attacker, a fixed
// amount from level 11 on.
func thornsAfterHurt(wearer, attacker *Player, level int16) {
	if attacker == wearer || wearer.rng == nil || wearer.rng.Float32() >= thornsPerLevel*float32(level) {
		return
	}
	damage := int32(level) - thornsFixedBase
	if level < thornsFixedFrom {
		damage = wearer.rng.Int32N(thornsRandom) + 1
	}
	if !attacker.s.game.sys.Hurt(attacker, float32(damage), DamageSource{Cause: damageThorns, Attacker: wearer}) || attacker.entity == nil {
		return
	}
	attacker.playSound(packet.SoundEventThorns)
}

// EnchantLevel is an enchantment and its level on an item.
type EnchantLevel struct {
	ID    int16
	Level int16
}

// enchantList is an item's enchantments as BDS works on them: the slots of the item, and
// a list for each activation.
type enchantList struct {
	slot  uint32
	lists [enchantActivations][]EnchantLevel
}

// activationOf is the list an enchantment is kept in. One nothing knows goes in the
// last, so it is written back with the rest.
func (g *gameplay) activationOf(id int16) uint8 {
	if e, ok := g.enchant(id); ok && e.Activation < enchantActivations {
		return e.Activation
	}
	return enchantActivations - 1
}

// itemEnchants reads the enchantments on s.
func (g *gameplay) itemEnchants(s ItemStack) enchantList {
	l := enchantList{slot: g.rules().items[s.Name].Slot}
	enchantLevels(s, func(id, level int16) {
		a := g.activationOf(id)
		l.lists[a] = append(l.lists[a], EnchantLevel{ID: id, Level: level})
	})
	return l
}

// addStatus is what adding an enchantment to a list did.
type addStatus uint8

const (
	addRefused addStatus = iota
	addAtMax
	addRaised
	addNew
)

// canAdd is BDS's check of an enchantment against a list: the item's slots, then the list up to the same enchantment, which is raised to the higher
// level (one above when equal) unless at its maximum. Only the same activation's list
// is compared.
func (g *gameplay) canAdd(l *enchantList, id, level int16) (addStatus, int, int16) {
	e, ok := g.enchant(id)
	if !ok {
		return addRefused, 0, 0
	}
	if l.slot == 0 || l.slot&e.Slots == 0 && l.slot&e.SecondarySlots == 0 {
		return addRefused, 0, 0
	}
	for i, have := range l.lists[e.Activation%enchantActivations] {
		if have.ID == id {
			if int32(have.Level) >= e.MaxLevel {
				return addAtMax, 0, 0
			}
			next := max(have.Level, level)
			if have.Level == level {
				next = have.Level + 1
			}
			return addRaised, i, next
		}
		if !e.compatibleWith(&Enchantment{ID: have.ID, Group: g.groupOf(have.ID)}) {
			return addAtMax, 0, 0
		}
	}
	return addNew, 0, level
}

// groupOf is the group of an enchantment, 0 for one nothing knows.
func (g *gameplay) groupOf(id int16) int32 {
	if e, ok := g.enchant(id); ok {
		return e.Group
	}
	return 0
}

// add puts an enchantment on a list if canAdd allows, and reports whether it did.
func (g *gameplay) add(l *enchantList, v EnchantLevel) bool {
	status, i, level := g.canAdd(l, v.ID, v.Level)
	a := g.activationOf(v.ID)
	switch status {
	case addNew:
		l.lists[a] = append(l.lists[a], EnchantLevel{ID: v.ID, Level: level})
	case addRaised:
		l.lists[a][i].Level = level
	default:
		return false
	}
	return true
}

// writeEnchants saves a list on s, on a Tag of its own.
func writeEnchants(s *ItemStack, l enchantList) {
	tag := maps.Clone(s.Tag)
	if tag == nil {
		tag = map[string]any{}
	}
	var saved []any
	for _, list := range l.lists {
		for _, v := range list {
			saved = append(saved, map[string]any{enchantID: v.ID, enchantLevel: v.Level})
		}
	}
	if len(saved) == 0 {
		delete(tag, enchantTag)
	} else {
		tag[enchantTag] = saved
	}
	s.Tag = tag
}

// applyEnchants is BDS's adding of a list to an item: what fits goes on, and a book
// with anything on it becomes an enchanted book.
func (g *gameplay) applyEnchants(s *ItemStack, from enchantList) {
	l, added := g.itemEnchants(*s), 0
	for _, list := range from.lists {
		for _, v := range list {
			if g.add(&l, v) {
				added++
			}
		}
	}
	if added > 0 {
		if s.Name == itemBook {
			s.Name = itemEnchantedBook
		}
		writeEnchants(s, l)
	}
}

// enchantWeight is what an item's enchantments are worth on an anvil, by their
// frequency: a book's cost half what an item's do.
func (g *gameplay) enchantWeight(l enchantList, book bool) int32 {
	var sum int32
	for _, list := range l.lists {
		for _, v := range list {
			e, ok := g.enchant(v.ID)
			if !ok {
				continue
			}
			switch {
			case book && e.Frequency == freqVeryRare:
				sum += int32(v.Level) << 2
			case book && e.Frequency == freqRare:
				sum += int32(v.Level) << 1
			case book:
				sum += int32(v.Level)
			case e.Frequency == freqVeryRare:
				sum += int32(v.Level) * 8
			case e.Frequency == freqRare:
				sum += int32(v.Level) * 4
			case e.Frequency == freqUncommon:
				sum += int32(v.Level) * 2
			case e.Frequency == freqCommon:
				sum += int32(v.Level)
			}
		}
	}
	return sum
}

// Enchants returns the enchantments saved on the item, in the order it saves them.
func (s ItemStack) Enchants() []EnchantLevel { return flatEnchants(s) }

// WithEnchant returns the item with an enchantment at level, whatever the rules say:
// level replaces one already there. Use Host.AddEnchant for the rules.
func (s ItemStack) WithEnchant(id, level int16) ItemStack {
	list := s.WithoutEnchant(id).Enchants()
	var l enchantList
	l.lists[0] = append(list, EnchantLevel{ID: id, Level: level})
	writeEnchants(&s, l)
	return s
}

// WithoutEnchant returns the item without an enchantment.
func (s ItemStack) WithoutEnchant(id int16) ItemStack {
	var l enchantList
	for _, v := range flatEnchants(s) {
		if v.ID != id {
			l.lists[0] = append(l.lists[0], v)
		}
	}
	writeEnchants(&s, l)
	return s
}

// RepairCost returns what anvils have added to the item's price.
func (s ItemStack) RepairCost() int32 { return repairCost(s) }

// WithRepairCost returns the item with that price.
func (s ItemStack) WithRepairCost(cost int32) ItemStack { return withTag(s, repairCostTag, cost) }

// AddEnchant adds an enchantment by the rules a table's own go by, and reports whether
// it went on. A book becomes an enchanted book. Runs: anywhere.
func (s *Host) AddEnchant(it ItemStack, id, level int16) (ItemStack, bool) {
	var one enchantList
	one.lists[0] = []EnchantLevel{{ID: id, Level: level}}
	before := flatEnchants(it)
	s.game.applyEnchants(&it, one)
	return it, !slices.Equal(before, flatEnchants(it))
}
