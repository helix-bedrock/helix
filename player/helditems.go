package player

import (
	"crypto/sha256"
	"maps"
	"sync/atomic"

	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/world/hx"
)

// A box of full books is about a megabyte of items held inside one item. Kept in the item,
// every dropped box, every box in a player's inventory and every save of either holds that
// megabyte: a few thousand boxes on the ground took gigabytes and every autosave wrote them
// all again. Big contents therefore move into the world's item contents records (hx.ItemsFile,
// keyed by their SHA-256, so equal contents are stored once) and the item keeps a reference:
// Helix's own rule, beyond BDS, which keeps them in the item. Nothing needs them while the
// item only lies, moves or is shown: the client is shown them without book pages, kept with
// the reference. They are read back only when they are taken out (a box placed).

// heldOffloadBytes is the size of contents at which they move to their own record: below
// it a reference and a record cost more than they save.
const heldOffloadBytes = 16 << 10

// ItemContents stores the items held inside items (a shulker box item's contents) on their
// own, keyed by the SHA-256 of their bytes (the world's hxdb.DB). Both run on any goroutine.
type ItemContents interface {
	// StoreItems stores records at once, in their own commit; sums[i] is list[i]'s SHA-256.
	StoreItems(list [][]byte, sums [][32]byte) error
	// LoadItems reads a record into dst[:0].
	LoadItems(sum [32]byte, dst []byte) ([]byte, error)
}

// heldItems is a big list of items held inside an item, kept under its tag's "Items" in
// place of the list. Copies of the tag share it; it is never changed but for moving its
// bytes to disk: once stored, raw is dropped and sum names the record. Its fields are read
// from any goroutine.
type heldItems struct {
	// size is the list's size in bytes, shown the list as a client is shown it.
	size  int
	shown nbt.Raw
	// raw is the list while it is in memory, nil once stored.
	raw atomic.Pointer[nbt.Raw]
	// stored is set, after sum, once the record is committed.
	stored atomic.Bool
	sum    [32]byte
	// queued is set once the holder is handed to the writer.
	queued atomic.Bool
}

// newHeld is a holder of raw, a list, when it is big enough to go to disk, else nil.
func newHeld(raw nbt.Raw) *heldItems {
	if raw.Len() < heldOffloadBytes || !raw.IsList() {
		return nil
	}
	shown, err := shownRaw(raw)
	if err != nil {
		return nil
	}
	h := &heldItems{size: raw.Len(), shown: shown}
	h.raw.Store(&raw)
	return h
}

// shownRaw is a list without book pages, as a client is shown it, packed.
func shownRaw(raw nbt.Raw) (nbt.Raw, error) {
	v, err := raw.ValueWithout(isPagesTag)
	if err != nil {
		return nbt.Raw{}, err
	}
	return nbt.MarshalRaw(v, nbt.LittleEndian)
}

// heldFromRef is a stored holder from a saved reference, or nil if m is not one.
func heldFromRef(m map[string]any) *heldItems {
	sum, size, shown, ok := hx.ItemsRef(m)
	if !ok {
		return nil
	}
	h := &heldItems{size: int(size), sum: sum}
	if shown != nil {
		if r, err := nbt.MarshalRaw(shown, nbt.LittleEndian); err == nil {
			h.shown = r
		}
	}
	h.stored.Store(true)
	return h
}

// saved is the tag entry the holder saves as: the reference once stored, else the list.
func (h *heldItems) saved() (string, any) {
	if h.stored.Load() {
		var shown any
		if !h.shown.IsZero() {
			shown = h.shown
		}
		return hx.ItemsRefTag, hx.NewItemsRef(h.sum, int32(min(h.size, 1<<31-1)), shown) //nolint:gosec // capped
	}
	if r := h.raw.Load(); r != nil {
		return shulkerItemsTag, *r
	}
	return "", nil
}

// list is the held items, read from disk if they are there. Without a store, or on a read
// error, it reports false.
func (h *heldItems) list(store ItemContents) (nbt.Raw, bool) {
	if r := h.raw.Load(); r != nil {
		return *r, true
	}
	if store == nil || !h.stored.Load() {
		return nbt.Raw{}, false
	}
	b, err := store.LoadItems(h.sum, nil)
	if err != nil {
		return nbt.Raw{}, false
	}
	return nbt.RawList(b, nbt.LittleEndian), true
}

// savedTagOf is tag as saved: every holder in it turned into its list or its reference.
// The tag passed is not changed.
func savedTagOf(tag map[string]any) map[string]any {
	h, ok := tag[shulkerItemsTag].(*heldItems)
	if !ok {
		return tag
	}
	tag = maps.Clone(tag)
	delete(tag, shulkerItemsTag)
	if key, v := h.saved(); key != "" {
		tag[key] = v
	}
	return tag
}

// tagHolder is the holder a tag keeps, or nil.
func tagHolder(tag map[string]any) *heldItems {
	h, _ := tag[shulkerItemsTag].(*heldItems)
	return h
}

// heldWriter moves held items to disk on its own goroutine, in batches.
type heldWriter struct {
	store ItemContents
	queue chan *heldItems
	done  chan struct{}
	host  *Host
}

// heldQueue is how many holders may wait to be written; more stay in memory.
const heldQueue = 1 << 14

// heldBatchBytes is about how much one commit of item contents writes.
const heldBatchBytes = 32 << 20

// startHeldWriter starts writing held items to store.
func (s *Host) startHeldWriter(store ItemContents) {
	w := &heldWriter{store: store, queue: make(chan *heldItems, heldQueue), done: make(chan struct{}), host: s}
	s.held = w
	go w.run()
}

// stopHeldWriter stops the writer; holders not written keep their items in memory and save
// them in place.
func (s *Host) stopHeldWriter() {
	if s.held == nil {
		return
	}
	close(s.held.queue)
	<-s.held.done
}

// offload hands every big holder in the stack's tag to the writer. Runs: anywhere.
func (s *Host) offload(st ItemStack) {
	h := tagHolder(st.Tag)
	if s.held == nil || h == nil || h.stored.Load() || !h.queued.CompareAndSwap(false, true) {
		return
	}
	select {
	case s.held.queue <- h:
	default:
		h.queued.Store(false)
	}
}

// loadedItem is a saved item read back with a network ID, its big contents handed to the
// writer. Every holder of items loads through it.
func (s *Host) loadedItem(m map[string]any) ItemStack {
	st := withID(itemFromNBT(m), s.nextStackID)
	s.offload(st)
	return st
}

// heldList is what a held item list holds: the holder's items, read from disk if they are
// there, or v as it is.
func (s *Host) heldList(v any) any {
	h, ok := v.(*heldItems)
	if !ok {
		return v
	}
	var store ItemContents
	if s.held != nil {
		store = s.held.store
	}
	r, ok := h.list(store)
	if !ok {
		s.log.Error("read item contents", "sum", h.sum[:8])
		return nil
	}
	return r
}

// run writes holders as they come, a batch at a time.
func (w *heldWriter) run() {
	defer close(w.done)
	var batch []*heldItems
	for h := range w.queue {
		batch = append(batch[:0], h)
		size := h.size
	fill:
		for size < heldBatchBytes {
			select {
			case more, ok := <-w.queue:
				if !ok {
					break fill
				}
				batch = append(batch, more)
				size += more.size
			default:
				break fill
			}
		}
		w.write(batch)
	}
}

// write commits a batch and drops the items of those written.
func (w *heldWriter) write(batch []*heldItems) {
	list := make([][]byte, 0, len(batch))
	sums := make([][32]byte, 0, len(batch))
	kept := batch[:0]
	for _, h := range batch {
		r := h.raw.Load()
		if r == nil {
			continue
		}
		b := r.Bytes()
		h.sum = sha256.Sum256(b)
		list, sums = append(list, b), append(sums, h.sum)
		kept = append(kept, h)
	}
	if err := w.store.StoreItems(list, sums); err != nil {
		w.host.log.Error("store item contents", "err", err)
		for _, h := range kept {
			h.queued.Store(false)
		}
		return
	}
	for _, h := range kept {
		h.stored.Store(true)
		h.raw.Store(nil)
	}
}
