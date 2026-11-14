package player

import (
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/world/cube"
)

// A chest, barrel or shulker box keeps its items on disk while nothing uses them: Helix's own
// rule, beyond BDS, which holds every loaded container's items in memory. A world of chests
// stuffed with books would otherwise need its whole size in memory. A cold chest keeps only
// each slot's count and stack size, for comparators; anything that touches its items reads
// them back from the chunk's stored copy first (warm), and a chest unused for a while drops
// them again once that copy is current (world.ColdBlockEntity).

// coolIdleRounds is how many cooling rounds (world.coolEveryTicks apart) a chest goes unused
// before it leaves its items on disk: one, so a chest nobody uses holds its items for at most
// about two rounds. Its slots are stored one record each (hxdb), so warming it again reads
// only what it holds.
const coolIdleRounds = 1

// coldSlot is what a cold chest keeps of a slot: enough for a comparator.
type coldSlot struct {
	count, maxStack uint8
}

// coolable reports whether the chest may leave its items on disk: a plain chest, barrel or
// shulker box in a block. Machines tick on their items and carts are not block entities.
func (c *Chest) coolable() bool {
	return !c.ender && c.cart == nil && c.boat == nil && (c.machine == nil || c.machine.barrel)
}

// Cold reports whether the chest's items are on disk only.
func (c *Chest) Cold() bool { return c.cold }

// Cool leaves the chest's items on disk once it has gone unused for coolIdleRounds rounds.
// The world asks only while the chunk's stored copy is current.
func (c *Chest) Cool(r *world.Region, _ cube.Pos) bool {
	if c.cold || !c.coolable() {
		return false
	}
	if c.inUse(r) {
		c.idle = 0
		return false
	}
	if c.idle++; c.idle < coolIdleRounds {
		return false
	}
	c.goCold()
	return true
}

// inUse reports whether someone has the chest open or it has a change still to send.
func (c *Chest) inUse(r *world.Region) bool {
	lead, _ := c.halves(r)
	return c.changed || len(c.viewers) > 0 || lead != nil && len(lead.viewers) > 0
}

// goCold keeps each slot's count and stack size and drops the items.
func (c *Chest) goCold() {
	for i := range c.items {
		c.coldSlots[i] = c.coldSlotOf(c.items[i])
	}
	c.items = [chestSize]ItemStack{}
	c.cold, c.idle = true, 0
	c.coldGen++
}

// coldSlotOf is what a cold chest keeps of s.
func (c *Chest) coldSlotOf(s ItemStack) coldSlot {
	if s.Empty() {
		return coldSlot{}
	}
	maxStack := uint8(0)
	if t, ok := c.s.reg.Items.ByName(s.Name); ok {
		maxStack = uint8(min(t.MaxStack, 255)) //nolint:gosec // capped
	}
	return coldSlot{count: s.Count, maxStack: maxStack}
}

// Warm reads the chest's items back from its chunk's stored copy. On a read error the chest
// stays cold, so its save keeps the stored items rather than writing none.
func (c *Chest) Warm(pos cube.Pos) {
	if !c.cold {
		return
	}
	saved, err := c.chunk.Stored(pos)
	if err != nil {
		c.s.log.Error("read chest items from disk", "pos", pos, "err", err)
		return
	}
	c.fillItems(saved)
	c.cold, c.idle, c.coldSlots = false, 0, [chestSize]coldSlot{}
}

// whenWarm reports whether the chest and its mate hold their items. If not, it starts reading
// them on the chunk's worker (warmLater) and runs fn, if not nil, once they are back. Code
// on the tick uses it instead of Inventory where it can wait, so a chest in a chunk of
// hundreds of megabytes does not stall the tick while its items are found.
func (c *Chest) whenWarm(r *world.Region, fn func(*world.Region)) bool {
	lead, follower := c.halves(r)
	for _, h := range [...]*Chest{lead, follower} {
		if h != nil && h.cold {
			var waiter func(*world.Region)
			if fn != nil {
				// Once this half is back, check both again: the other may still be cold.
				waiter = func(r *world.Region) {
					if c.whenWarm(r, fn) {
						fn(r)
					}
				}
			}
			h.warmLater(r, waiter)
			return false
		}
	}
	return true
}

// warmLater reads the items on a worker and applies them on the owner if the chest is still
// as cold as when asked, then runs waiter, if not nil.
func (c *Chest) warmLater(r *world.Region, waiter func(*world.Region)) {
	if waiter != nil {
		c.waiters = append(c.waiters, waiter)
	}
	// A read whose answer was dropped (its chunk moved to another region meanwhile) is
	// asked again after a while.
	if c.warming && r.CurrentTick()-c.warmAt < warmRetryTicks {
		return
	}
	c.warming, c.warmAt = true, r.CurrentTick()
	gen := c.coldGen
	c.chunk.StoredLater(c.pos, func(r *world.Region, saved map[string]any, err error) {
		c.warming = false
		waiters := c.waiters
		c.waiters = nil
		if !c.cold || c.coldGen != gen {
			// Warmed meanwhile on the tick, maybe cold again since: this read is old.
			c.runWaiters(r, waiters)
			return
		}
		if err != nil {
			c.s.log.Error("read chest items from disk", "pos", c.pos, "err", err)
			return
		}
		c.fillItems(saved)
		c.cold, c.idle, c.coldSlots = false, 0, [chestSize]coldSlot{}
		c.runWaiters(r, waiters)
	})
}

// warmRetryTicks is how long a read of a cold chest's items may take before it is asked
// again.
const warmRetryTicks = 100

// runWaiters runs what waited for the items.
func (c *Chest) runWaiters(r *world.Region, waiters []func(*world.Region)) {
	if r == nil {
		return
	}
	for _, w := range waiters {
		w(r)
	}
}

// warm is Warm at the chest's own position, for code that holds the chest.
func (c *Chest) warm() { c.Warm(c.pos) }

// fillItems puts the saved items in their slots.
func (c *Chest) fillItems(saved map[string]any) {
	for _, item := range compounds(saved["Items"]) {
		slot, _ := item["Slot"].(uint8)
		if int(slot) < c.halfSize() {
			c.items[slot] = withID(itemFromNBT(item), c.s.nextStackID)
		}
	}
}

// used marks the chest as just used, warming it.
func (c *Chest) used() {
	c.warm()
	c.idle = 0
}

// coldSignal is the comparator signal of a chest of which a half is cold, from what each half
// keeps.
func coldSignal(lead, follower *Chest) int32 {
	var sum circuit.ContainerSum
	size := 0
	for _, h := range [...]*Chest{lead, follower} {
		if h == nil {
			continue
		}
		size += h.halfSize()
		for i := range h.halfSize() {
			s := h.coldSlots[i]
			if !h.cold {
				s = h.coldSlotOf(h.items[i])
			}
			sum.Add(int(s.count), int(s.maxStack))
		}
	}
	return sum.Signal(size)
}

// LoadsCold reports whether block entities saved with this id load with their items left on
// disk (counts only): a store may give their Items light, without each item's tag
// (hxdb.Config.LightItems). Runs: anywhere.
func LoadsCold(id string) bool { return coldEntityID(id) }

// coldEntityID reports a saved ID of block entities that may load cold.
func coldEntityID(id string) bool {
	if id == chestEntityID || id == shulkerEntityID {
		return true
	}
	spec := machineByID(id)
	return spec != nil && spec.barrel
}

// LoadRaw loads a chest, barrel or shulker box cold from its stored bytes: its items are
// decoded without their tags, only for each slot's count and stack size, and left on disk.
// Any other block entity is left to Load.
func (e chestEntities) LoadRaw(pos cube.Pos, raw []byte) (world.BlockEntity, bool) {
	head, err := nbt.DecodeWithout(raw, nbt.LittleEndian, notIDTag)
	if err != nil {
		return nil, false
	}
	if id, _ := head["id"].(string); !coldEntityID(id) {
		return nil, false
	}
	light, err := nbt.DecodeWithout(raw, nbt.LittleEndian, isItemTagTag)
	if err != nil {
		return nil, false
	}
	be, ok := e.Load(pos, light)
	c, isChest := be.(*Chest)
	if !ok || !isChest || !c.coolable() {
		return nil, false
	}
	c.goCold()
	return c, true
}

// notIDTag is every tag name but a block entity's id.
func notIDTag(name string) bool { return name != "id" }

// isItemTagTag names an item's tag, which holds what makes an item big: book pages, the
// items in a shulker box.
func isItemTagTag(name string) bool { return name == "tag" }
