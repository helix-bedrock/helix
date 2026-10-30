package world

import (
	"container/list"
	"sync"

	"github.com/helix-bedrock/world/mcdb"
)

// netCache keeps recently encoded chunks within a memory budget, least
// recently used out first, so players in busy areas get chunks without a
// load. Shared by every region of a dimension.
type netCache struct {
	mu           sync.Mutex
	budget, used int
	order        *list.List // front is most recent; values are *NetChunk
	byPos        map[mcdb.ChunkPos]*list.Element
}

func newNetCache(budget int) *netCache {
	return &netCache{budget: budget, order: list.New(), byPos: map[mcdb.ChunkPos]*list.Element{}}
}

func (c *netCache) get(pos mcdb.ChunkPos) *NetChunk {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byPos[pos]
	if !ok {
		return nil
	}
	c.order.MoveToFront(e)
	nc, _ := e.Value.(*NetChunk)
	return nc
}

func (c *netCache) put(pos mcdb.ChunkPos, nc *NetChunk) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remove(pos)
	if nc.size > c.budget {
		return
	}
	c.byPos[pos] = c.order.PushFront(nc)
	c.used += nc.size
	for c.used > c.budget {
		if old, ok := c.order.Back().Value.(*NetChunk); ok {
			c.remove(old.pos)
		}
	}
}

func (c *netCache) drop(pos mcdb.ChunkPos) {
	c.mu.Lock()
	c.remove(pos)
	c.mu.Unlock()
}

func (c *netCache) remove(pos mcdb.ChunkPos) {
	e, ok := c.byPos[pos]
	if !ok {
		return
	}
	if nc, ok := e.Value.(*NetChunk); ok {
		c.used -= nc.size
	}
	c.order.Remove(e)
	delete(c.byPos, pos)
}
