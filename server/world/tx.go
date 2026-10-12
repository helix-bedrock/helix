package world

import (
	"iter"
	"runtime/debug"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/player/chat"
)

// Tx is the owner transaction handle passed to world callbacks. It is the
// only way to perform world operations and is valid only during its callback.
type Tx struct {
	w        *World
	r        *region
	closed   bool
	deferred []scheduledTransaction
}

// newTx returns a fresh transaction on region r.
func newTx(r *region) *Tx {
	return &Tx{w: r.World, r: r}
}

// Defer schedules f to run on the owner after the current callback completes
// and before the parent Task completes. Deferred callbacks run FIFO in
// registration order, unlike Go defer's LIFO order.
func (tx *Tx) Defer(f func(tx *Tx)) *Task {
	return tx.DeferErr(func(tx *Tx) error {
		f(tx)
		return nil
	})
}

// DeferErr schedules f to run on the owner after the current callback
// completes, recording any returned error on the Task.
func (tx *Tx) DeferErr(f func(tx *Tx) error) *Task {
	return tx.deferTask(f)
}

// Range returns the lower and upper bounds of the World that the Tx is
// operating on.
func (tx *Tx) Range() cube.Range {
	return tx.r.ra
}

// SetBlock writes a block to the position passed. If a chunk is not yet loaded
// at that position, the chunk is first loaded or generated if it could not be
// found in the world save. SetBlock panics if the block passed has not yet
// been registered using RegisterBlock(). Nil may be passed as the block to set
// the block to air.
//
// A SetOpts struct may be passed to additionally modify behaviour of SetBlock,
// specifically to improve performance under specific circumstances. Nil should
// be passed where performance is not essential, to make sure the world is
// updated adequately.
//
// SetBlock should be avoided in situations where performance is critical when
// needing to set a lot of blocks to the world. BuildStructure may be used
// instead.
func (tx *Tx) SetBlock(pos cube.Pos, b Block, opts *SetOpts) {
	tx.setBlock(pos, b, opts)
}

// SetBlockEntity updates block entity data without notifying viewers, updating
// neighbouring blocks or invalidating redstone. The chunk is marked modified.
// It falls back to SetBlock if b has no block entity data or its state differs.
func (tx *Tx) SetBlockEntity(pos cube.Pos, b Block) {
	tx.setBlockEntity(pos, b)
}

// Block reads a block from the position passed. If a chunk is not yet loaded
// at that position, the chunk is loaded, or generated if it could not be found
// in the world save, and the block returned.
func (tx *Tx) Block(pos cube.Pos) Block {
	return tx.block(pos)
}

// BlockLoaded returns the block at the position passed if the chunk containing it is already loaded. It returns false
// without loading or generating the chunk when the block is unavailable.
func (tx *Tx) BlockLoaded(pos cube.Pos) (Block, bool) {
	return tx.r.blockLoaded(pos)
}

// BlocksWithin returns an iterator over the positions of blocks matching any of the block states passed, within a
// horizontal square radius around pos. Chunks not in memory are read from the world save; missing chunks are
// skipped, not generated. Only the primary block layer is searched and blocks are matched by their state alone.
func (tx *Tx) BlocksWithin(pos cube.Pos, radius int, blocks ...Block) iter.Seq[cube.Pos] {
	return tx.r.blocksWithin(pos, radius, blocks...)
}

// Liquid attempts to return a Liquid block at the position passed. This
// Liquid may be in the foreground or in any other layer. If found, the Liquid
// is returned. If not, the bool returned is false.
func (tx *Tx) Liquid(pos cube.Pos) (Liquid, bool) {
	return tx.liquid(pos)
}

// SetLiquid sets a Liquid at a specific position in the World. Unlike
// SetBlock, SetLiquid will not necessarily overwrite any existing blocks. It
// will instead be in the same position as a block currently there, unless
// there already is a Liquid at that position, in which case it will be
// overwritten. If nil is passed for the Liquid, any Liquid currently present
// will be removed.
func (tx *Tx) SetLiquid(pos cube.Pos, b Liquid) {
	tx.setLiquid(pos, b)
}

// BuildStructure builds a Structure passed at a specific position in the
// world. Unlike SetBlock, it takes a Structure implementation, which provides
// blocks to be placed at a specific location. BuildStructure is specifically
// optimised to be able to process a large batch of chunks simultaneously and
// will do so within much less time than separate SetBlock calls would. The
// method operates on a per-chunk basis, setting all blocks within a single
// chunk part of the Structure before moving on to the next chunk.
func (tx *Tx) BuildStructure(pos cube.Pos, s Structure) {
	tx.buildStructure(pos, s)
}

// ScheduleBlockUpdate schedules a block update at the position passed for the
// block type passed after a specific delay. If the block at that position does
// not handle block updates, nothing will happen.
// Block updates are both block and position specific. A block update is only
// scheduled if no block update with the same position and block type is
// already scheduled at a later time than the newly scheduled update.
func (tx *Tx) ScheduleBlockUpdate(pos cube.Pos, b Block, delay time.Duration) {
	tx.r.scheduleBlockUpdate(pos, b, delay)
}

// HighestLightBlocker gets the Y value of the highest fully light blocking
// block at the x and z values passed in the World.
func (tx *Tx) HighestLightBlocker(x, z int) int {
	return tx.highestLightBlocker(x, z)
}

// HighestBlock looks up the highest non-air block in the World at a specific x
// and z. The y value of the highest block is returned, or 0 if no blocks were
// present in the column.
func (tx *Tx) HighestBlock(x, z int) int {
	return tx.highestBlock(x, z)
}

// Light returns the light level at the position passed. This is the highest of
// the sky- and block light. The light value returned is a value in the range
// 0-15, where 0 means there is no light present, whereas 15 means the block is
// fully lit. Light does not load chunks: 0 is returned for positions in chunks
// that are not currently loaded.
func (tx *Tx) Light(pos cube.Pos) uint8 {
	return tx.light(pos)
}

// SkyLight returns the skylight level at the position passed. This light level
// is not influenced by blocks that emit light, such as torches. The light
// value, similarly to Light, is a value in the range 0-15, where 0 means no
// light is present. Unlike Light, SkyLight loads or generates the chunk at the
// position if it is not currently loaded.
func (tx *Tx) SkyLight(pos cube.Pos) uint8 {
	return tx.skyLight(pos)
}

// BlockLight returns the block light level at the position passed. This light
// level is only influenced by blocks that emit light, such as torches, and
// ignores the light of the sky. Like SkyLight, BlockLight loads or generates
// the chunk at the position if it is not currently loaded.
func (tx *Tx) BlockLight(pos cube.Pos) uint8 {
	return tx.blockLight(pos)
}

// SetBiome sets the Biome at the position passed. If a chunk is not yet loaded
// at that position, the chunk is first loaded or generated if it could not be
// found in the world save.
func (tx *Tx) SetBiome(pos cube.Pos, b Biome) {
	tx.setBiome(pos, b)
}

// Biome reads the Biome at the position passed. If a chunk is not yet loaded
// at that position, the chunk is loaded, or generated if it could not be found
// in the world save, and the Biome returned.
func (tx *Tx) Biome(pos cube.Pos) Biome {
	return tx.biome(pos)
}

// Temperature returns the temperature in the World at a specific position.
// Higher altitudes and different biomes influence the temperature returned.
func (tx *Tx) Temperature(pos cube.Pos) float64 {
	return tx.temperature(pos)
}

// RainingAt checks if it is raining at a specific cube.Pos in the World. True
// is returned if it is raining, if the temperature is high enough in the biome
// for it not to be snow and if the block is above the top-most obstructing
// block.
func (tx *Tx) RainingAt(pos cube.Pos) bool {
	return tx.rainingAt(pos)
}

// SnowingAt checks if it is snowing at a specific cube.Pos in the World. True
// is returned if the temperature in the Biome at that position is sufficiently
// low, if it is raining and if it's above the top-most obstructing block.
func (tx *Tx) SnowingAt(pos cube.Pos) bool {
	return tx.snowingAt(pos)
}

// ThunderingAt checks if it is thundering at a specific cube.Pos in the World.
// True is returned if RainingAt returns true and if it is thundering in the
// world.
func (tx *Tx) ThunderingAt(pos cube.Pos) bool {
	return tx.thunderingAt(pos)
}

// Raining checks if it is raining anywhere in the World.
func (tx *Tx) Raining() bool {
	return tx.r.raining()
}

// Thundering checks if it is thundering anywhere in the World.
func (tx *Tx) Thundering() bool {
	return tx.r.thundering()
}

// AddParticle spawns a Particle at a given position in the World. Viewers that
// are viewing the chunk will be shown the particle.
func (tx *Tx) AddParticle(pos mgl64.Vec3, p Particle) {
	tx.r.addParticle(pos, p)
}

// PlayEntityAnimation plays an animation on an entity in the World. The animation is played for all viewers
// of the entity.
func (tx *Tx) PlayEntityAnimation(e Entity, a EntityAnimation) {
	for _, viewer := range tx.r.viewersOf(e.Position()) {
		viewer.ViewEntityAnimation(e, a)
	}
}

// PlaySound plays a sound at a specific position in the World. Viewers of that
// position will be able to hear the sound if they are close enough.
func (tx *Tx) PlaySound(pos mgl64.Vec3, s Sound) {
	tx.r.playSound(tx, pos, s)
}

// AddEntity adds an EntityHandle to a World. The Entity will be visible to all
// viewers of the World that have the chunk at the EntityHandle's position. If
// the chunk that the EntityHandle is in is not yet loaded, it will first be
// loaded. AddEntity panics if the EntityHandle is already in a world.
// AddEntity returns the Entity created by the EntityHandle.
func (tx *Tx) AddEntity(e *EntityHandle) Entity {
	return tx.r.addEntity(tx, e)
}

// AddEntityAt adds an EntityHandle to a World at the position passed. The Entity will be visible to all viewers of
// the World that have the chunk at the position passed. AddEntityAt panics if the EntityHandle is already in a world.
// AddEntityAt returns the Entity created by the EntityHandle.
func (tx *Tx) AddEntityAt(e *EntityHandle, pos mgl64.Vec3) Entity {
	return tx.r.addEntityAt(tx, e, pos)
}

// RemoveEntity removes an Entity from the World that is currently present in
// it. Any viewers of the Entity will no longer be able to see it.
// RemoveEntity returns the EntityHandle of the Entity. After removing an Entity
// from the World, the Entity is no longer usable.
func (tx *Tx) RemoveEntity(e Entity) *EntityHandle {
	return tx.r.removeEntity(e, tx)
}

// EntitiesWithin returns an iterator that yields all entities contained within
// the cube.BBox passed.
func (tx *Tx) EntitiesWithin(box cube.BBox) iter.Seq[Entity] {
	return tx.r.entitiesWithin(tx, box)
}

// Entities returns an iterator that yields all entities in the World.
func (tx *Tx) Entities() iter.Seq[Entity] {
	return tx.r.allEntities(tx)
}

// Players returns an iterator that yields all player entities in the World.
func (tx *Tx) Players() iter.Seq[Entity] {
	return tx.r.allPlayers(tx)
}

// Viewers returns all viewers viewing the position passed.
func (tx *Tx) Viewers(pos mgl64.Vec3) []Viewer {
	return tx.r.viewersOf(pos)
}

// Sleepers returns an iterator that yields all sleeping entities currently added to the World.
func (tx *Tx) Sleepers() iter.Seq[Sleeper] {
	ent := tx.Entities()
	return func(yield func(Sleeper) bool) {
		for e := range ent {
			if sleeper, ok := e.(Sleeper); ok {
				if !yield(sleeper) {
					return
				}
			}
		}
	}
}

// BroadcastSleepingIndicator broadcasts a sleeping indicator to all sleepers in the world.
func (tx *Tx) BroadcastSleepingIndicator() {
	if tx.w.RegionCount() > 1 {
		// Helix: count the sleepers of the whole world.
		tx.w.DoGlobal(func(tx *Tx) { tx.broadcastSleepingIndicator() })
		return
	}
	tx.broadcastSleepingIndicator()
}

func (tx *Tx) broadcastSleepingIndicator() {
	sleepers := tx.Sleepers()

	var sleeping, allSleepers int
	for s := range sleepers {
		if spectating(s) {
			continue
		}
		allSleepers++
		if _, ok := s.Sleeping(); ok {
			sleeping++
		}
	}

	for s := range sleepers {
		s.SendSleepingIndicator(sleeping, allSleepers)
	}
}

// BroadcastSleepingReminder broadcasts a sleeping reminder message to all sleepers in the world, excluding the sleeper
// passed.
func (tx *Tx) BroadcastSleepingReminder(sleeper Sleeper) {
	if tx.w.RegionCount() > 1 {
		// Helix: remind the sleepers of the whole world.
		h := sleeper.H()
		tx.w.DoGlobal(func(tx *Tx) {
			if e, ok := h.Entity(tx); ok {
				if s, ok := e.(Sleeper); ok {
					tx.broadcastSleepingReminder(s)
				}
			}
		})
		return
	}
	tx.broadcastSleepingReminder(sleeper)
}

func (tx *Tx) broadcastSleepingReminder(sleeper Sleeper) {
	sleepers := tx.Sleepers()

	var notSleeping int
	for s := range sleepers {
		if _, ok := s.Sleeping(); !ok && !spectating(s) {
			notSleeping++
		}
	}

	for s := range sleepers {
		if _, ok := s.Sleeping(); ok {
			continue
		}
		if notSleeping == 0 {
			s.Messaget(chat.MessageSkippingNight)
		} else {
			s.Messaget(chat.MessageSleeping, sleeper.Name(), notSleeping)
		}
	}
}

func (tx *Tx) deferTask(f func(tx *Tx) error) *Task {
	if tx.closed {
		panic("world.Tx: use of transaction after transaction finishes is not permitted")
	}
	task := newTask()
	tx.deferred = append(tx.deferred, scheduledTransaction{task: task, f: f})
	return task
}

// World returns the Tx's World. It panics once the callback has
// completed. Treat the result as the off-owner handle: blocking calls like
// Save and Close deadlock from inside the callback, so do world operations
// through the Tx instead.
func (tx *Tx) World() *World {
	if tx.closed {
		panic("world.Tx: use of transaction after transaction finishes is not permitted")
	}
	return tx.w
}

// CurrentTick returns the current tick of the transaction's world.
func (tx *Tx) CurrentTick() int64 {
	w := tx.r
	w.set.Lock()
	defer w.set.Unlock()
	return w.set.CurrentTick
}

// close finishes the Tx, causing any following call on the Tx to panic.
func (tx *Tx) close() {
	tx.closed = true
}

func (tx *Tx) runDeferred() {
	for len(tx.deferred) > 0 {
		deferred := tx.deferred
		tx.deferred = nil
		for _, st := range deferred {
			st.Run(tx.r)
		}
	}
}

// normalTransaction is added to the transaction queue for transactions created
// using World.exec().
type normalTransaction struct {
	c chan struct{}
	f func(tx *Tx)
}

// Run creates a *Tx, calls ntx.f, closes the transaction and finally closes
// ntx.c.
func (ntx normalTransaction) Run(r *region) {
	defer close(ntx.c)
	tx := newTx(r)
	runRecovered(r, func() {
		ntx.f(tx)
		tx.close()
		tx.runDeferred()
	})
}

// runRecovered runs f, logging and stopping a panic in it so that one bad
// transaction, block or entity cannot stop the region or the server
// (Helix: never crash).
func runRecovered(r *region, f func()) {
	defer func() {
		if v := recover(); v != nil {
			r.conf.Log.Error("recovered from a panic in a world transaction; it was cut short",
				"region", r.String(), "panic", v, "stack", string(debug.Stack()))
		}
	}()
	f()
}

// weakTransaction is a transaction that may be cancelled by its validity
// predicate before the transaction is run.
type weakTransaction struct {
	// h is the entity the transaction is for. It runs on the entity's
	// region, following it when it moves (Helix).
	h     *EntityHandle
	c     chan bool
	f     func(tx *Tx)
	valid func() bool
	cond  *sync.Cond
}

// Run runs the transaction, first checking if it is still valid and creating a
// *Tx if so. Afterwards, a bool indicating if the transaction was run is added
// to wtx.c. Finally, wtx.cond.Broadcast() is called.
func (wtx weakTransaction) Run(r *region) {
	if wtx.h != nil && !r.conf.Synchronous {
		if o := r.regionOfEntity(wtx.h); o != nil && o != r {
			o.post(wtx)
			return
		}
	}
	valid := wtx.valid == nil || wtx.valid()
	if valid {
		tx := newTx(r)
		runRecovered(r, func() {
			wtx.f(tx)
			tx.close()
			tx.runDeferred()
		})
	}
	// We have to acquire a lock on wtx.cond.L here to make sure cond.Wait()
	// has been called before we call cond.Broadcast(). If not, we might
	// broadcast before cond.Wait() and cause a permanent suspension.
	wtx.cond.L.Lock()
	defer wtx.cond.L.Unlock()

	wtx.c <- valid
	wtx.cond.Broadcast()
}

// fail delivers false to a weak transaction that will never run, using the
// same condition handshake as Run so a waiter in cond.Wait is woken.
func (wtx weakTransaction) fail() {
	wtx.cond.L.Lock()
	defer wtx.cond.L.Unlock()
	wtx.c <- false
	wtx.cond.Broadcast()
}
