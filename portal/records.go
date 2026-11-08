package portal

import (
	"fmt"
	"sync"

	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Record is one nether portal as BDS's PortalForcer keeps it: the lowest inside block
// at one end, its width and the step along it.
type Record struct {
	Pos    cube.Pos
	Span   int8
	XA, ZA int8
}

// column is the record's i-th column.
func (rec Record) column(i int) cube.Pos {
	return rec.Pos.Add(cube.Pos{int(rec.XA) * i, 0, int(rec.ZA) * i})
}

// dimensions BDS keeps portal records for.
const dimensions = 3

// Records are the portal records of every dimension, saved under the world's
// "portals" level key. Safe for use by many goroutines at once.
type Records struct {
	mu   sync.Mutex
	dims [dimensions][]Record
	save func(value []byte)
}

// The portals level key as BDS 1.26.60 writes it: {data: {PortalRecords: [...]}}.
// The field names are BDS's; Helix has not compared a saved world byte for byte.
const (
	keyData    = "data"
	keyRecords = "PortalRecords"
	keyDim     = "DimId"
	keySpan    = "Span"
	keyX, keyY = "TpX", "TpY"
	keyZ       = "TpZ"
	keyXA      = "Xa"
	keyZA      = "Za"
)

// LoadRecords reads the records from the level key's value (nil for none); save
// writes them back when they change, on the goroutine that changed them.
func LoadRecords(value []byte, save func(value []byte)) (*Records, error) {
	rs := &Records{save: save}
	if len(value) == 0 {
		return rs, nil
	}
	var m map[string]any
	if err := nbt.UnmarshalEncoding(value, &m, nbt.LittleEndian); err != nil {
		return rs, fmt.Errorf("portal: read %s: %w", mcdb.LevelKeyPortals, err)
	}
	data, _ := m[keyData].(map[string]any)
	list, _ := data[keyRecords].([]any)
	for _, v := range list {
		c, ok := v.(map[string]any)
		if !ok {
			continue
		}
		dim := num(c[keyDim])
		if dim < 0 || dim >= dimensions {
			continue
		}
		rs.dims[dim] = append(rs.dims[dim], Record{
			Pos: cube.Pos{int(num(c[keyX])), int(num(c[keyY])), int(num(c[keyZ]))},
			//nolint:gosec // spans and steps are small
			Span: int8(num(c[keySpan])), XA: int8(num(c[keyXA])), ZA: int8(num(c[keyZA])),
		})
	}
	return rs, nil
}

// num reads an NBT whole number of any width.
func num(v any) int32 {
	switch n := v.(type) {
	case int32:
		return n
	case uint8:
		return int32(n)
	case int8:
		return int32(n)
	case int16:
		return int32(n)
	case int64:
		return int32(n) //nolint:gosec // record fields fit
	}
	return 0
}

// Add keeps a record for dim and saves.
func (rs *Records) Add(dim mcdb.Dimension, rec Record) {
	if dim < 0 || dim >= dimensions {
		return
	}
	rs.mu.Lock()
	rs.dims[dim] = append(rs.dims[dim], rec)
	rs.storeLocked()
	rs.mu.Unlock()
}

// Remove forgets the records of dim that fail keep, and saves if any went.
func (rs *Records) Remove(dim mcdb.Dimension, drop func(Record) bool) {
	if dim < 0 || dim >= dimensions {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	kept := rs.dims[dim][:0]
	for _, rec := range rs.dims[dim] {
		if !drop(rec) {
			kept = append(kept, rec)
		}
	}
	if len(kept) != len(rs.dims[dim]) {
		rs.dims[dim] = kept
		rs.storeLocked()
	}
}

// Nearest is BDS's PortalForcer findPortal (sub_CDADE40): of every column of every
// record of dim within radius of pos on x and z, the closest one (by squared distance,
// height counted), and its record.
func (rs *Records) Nearest(dim mcdb.Dimension, pos cube.Pos, radius int) (Record, cube.Pos, bool) {
	if dim < 0 || dim >= dimensions {
		return Record{}, cube.Pos{}, false
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var (
		best    Record
		bestCol cube.Pos
		bestD   = -1
	)
	for _, rec := range rs.dims[dim] {
		for i := range int(rec.Span) {
			c := rec.column(i)
			dx, dy, dz := c[0]-pos[0], c[1]-pos[1], c[2]-pos[2]
			if abs(dx) > radius || abs(dz) > radius {
				continue
			}
			if d := dx*dx + dy*dy + dz*dz; bestD < 0 || d < bestD {
				best, bestCol, bestD = rec, c, d
			}
		}
	}
	return best, bestCol, bestD >= 0
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// storeLocked writes the records with save.
func (rs *Records) storeLocked() {
	if rs.save == nil {
		return
	}
	var list []any
	for dim, recs := range rs.dims {
		for _, rec := range recs {
			list = append(list, map[string]any{
				keyDim: int32(dim), keySpan: uint8(rec.Span), //nolint:gosec // spans are small
				keyX: int32(rec.Pos[0]), keyY: int32(rec.Pos[1]), keyZ: int32(rec.Pos[2]), //nolint:gosec // positions fit
				keyXA: uint8(rec.XA), keyZA: uint8(rec.ZA), //nolint:gosec // steps are -1, 0 or 1, kept as BDS's byte
			})
		}
	}
	if list == nil {
		list = []any{}
	}
	// Plain maps, lists and numbers always encode.
	if value, err := nbt.MarshalEncoding(map[string]any{keyData: map[string]any{keyRecords: list}}, nbt.LittleEndian); err == nil {
		rs.save(value)
	}
}
