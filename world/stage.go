package world

import (
	"errors"

	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Chunks load in stages. A chunk a region holds (a simulation zone, a ticket,
// RunAt) loads whole: blocks, entities, block entities made live, tick queues
// and light (Dimension.load). A chunk players are only shown, which is most of
// a view, loads only what its network form needs: blocks, biomes and each
// block entity's network data (loadView). Its entities are never decoded and
// its block entities never made live, and a big field the client never sees,
// such as the Items of a chest full of books, is not even read from a store
// that keeps such fields apart (TerrainLoader, BlockEntityViewer). Helix's
// own: BDS loads every chunk it sends whole.

// TerrainLoader is a WorldStore that reads a column's terrain alone: blocks,
// biomes and height map, without entities, tick queues or block entities.
// world/hxdb is one.
type TerrainLoader interface {
	LoadTerrain(pos mcdb.ChunkPos, dim mcdb.Dimension) (*chunk.Column, error)
}

// BlockEntityViewer is a WorldStore that gives a chunk's block entities
// without the fields skip names, never reading those it keeps apart.
// world/hxdb is one.
type BlockEntityViewer interface {
	EachBlockEntityView(chunkPos mcdb.ChunkPos, dim mcdb.Dimension, skip func(name string) bool, fn func(pos cube.Pos, raw []byte) error) error
}

// loadView runs on a worker: it reads or generates the chunk at pos as far as
// its network form needs and gives it to the players waiting. The chunk does
// not join a region; one that is held is loaded whole by load.
func (d *Dimension) loadView(pos mcdb.ChunkPos) {
	col, err := d.loadTerrain(pos)
	if err != nil {
		if !errors.Is(err, mcdb.ErrNotFound) {
			d.log.Error("load chunk", "pos", pos, "err", err)
		}
		col = &chunk.Column{Chunk: chunk.New(d.conf.Blocks, d.r), Finalisation: chunk.FinalisationNeedsPopulation}
		if err := d.generate(pos, col); err != nil {
			d.log.Error("generate chunk", "pos", pos, "err", err)
		}
	} else {
		col.Chunk.ReplaceBiomes(d.resolveBiome)
	}
	entities := make([]chunk.BlockEntity, 0, len(col.BlockEntities))
	for _, be := range col.BlockEntities {
		entities = append(entities, chunk.BlockEntity{Pos: be.Pos, Data: savedNetworkData(be.Data)})
	}
	if err == nil {
		if entities, err = d.viewBlockEntities(pos, entities); err != nil {
			d.log.Error("load chunk block entities", "pos", pos, "err", err)
		}
	}
	d.publish(pos, encodeNetChunk(pos, d.conf.Dim, col.Chunk, entities))
}

// loadTerrain reads the chunk's terrain, from the store's terrain call if it
// has one.
func (d *Dimension) loadTerrain(pos mcdb.ChunkPos) (*chunk.Column, error) {
	if tl, ok := d.conf.Store.(TerrainLoader); ok {
		return tl.LoadTerrain(pos, d.conf.Dim)
	}
	col, err := d.conf.Store.LoadColumn(pos, d.conf.Dim)
	if err != nil {
		return nil, err
	}
	col.Entities, col.ScheduledBlocks, col.RandomTicks = nil, nil, nil
	switch d.conf.Store.(type) {
	case BlockEntityViewer, mcdb.BlockEntityLoader:
		// viewBlockEntities reads them.
		col.BlockEntities = nil
	}
	return col, nil
}

// viewBlockEntities appends the network data of the chunk's stored block
// entities to network.
func (d *Dimension) viewBlockEntities(pos mcdb.ChunkPos, network []chunk.BlockEntity) ([]chunk.BlockEntity, error) {
	add := func(at cube.Pos, raw []byte) error {
		network = append(network, chunk.BlockEntity{Pos: at, Data: rawNetworkData(raw)})
		return nil
	}
	switch s := d.conf.Store.(type) {
	case BlockEntityViewer:
		return network, s.EachBlockEntityView(pos, d.conf.Dim, networkHiddenTag, add)
	case mcdb.BlockEntityLoader:
		return network, s.EachBlockEntity(pos, d.conf.Dim, add)
	}
	return network, nil
}
