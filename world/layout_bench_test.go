package world

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol/packet"
)

// BenchmarkRegionLayout runs 400 fake players on the regions, for each
// section size and merge distance the entity view disc allows, in three
// scenarios: all in one spot, in towns, and spread out. Each walks or
// flies in random directions on its region's tick. It reports what a
// player feels (the slowest region tick), the CPU used per tick, the
// number of regions and how many were made by merges and splits.
//
//	go test ./world -run '^$' -bench RegionLayout -benchtime 1x
func BenchmarkRegionLayout(b *testing.B) {
	reg := vanillaRegistry(b)
	layouts := []struct {
		shift uint
		merge int32
	}{{2, 2}, {3, 1}, {3, 2}, {4, 1}, {5, 1}}
	scenarios := []struct {
		name  string
		place func(rng *rand.Rand, i int) mgl32.Vec3
	}{
		{"crowd", func(rng *rand.Rand, _ int) mgl32.Vec3 { return square(rng, 0, 0, 64) }},
		{"towns", func(rng *rand.Rand, i int) mgl32.Vec3 {
			town := rand.New(rand.NewPCG(uint64(i%20), 1)) //nolint:gosec // a fixed layout
			return square(rng, town.Float32()*3000, town.Float32()*3000, 96)
		}},
		{"spread", func(rng *rand.Rand, _ int) mgl32.Vec3 { return square(rng, 0, 0, 4000) }},
	}
	for _, sc := range scenarios {
		for _, l := range layouts {
			b.Run(fmt.Sprintf("%s/section=%d/merge=%d", sc.name, 1<<l.shift, l.merge), func(b *testing.B) {
				for range b.N {
					runLayout(b, reg, l.shift, l.merge, sc.place)
				}
			})
		}
	}
}

func square(rng *rand.Rand, x, z, side float32) mgl32.Vec3 {
	return mgl32.Vec3{x + rng.Float32()*side, 0, z + rng.Float32()*side}
}

const (
	benchPlayers = 400
	benchRadius  = 4
	benchWarmup  = 5 * time.Second
	benchMeasure = 10 * time.Second
	// walkSpeed and flySpeed are blocks per tick: sprinting, and an
	// elytra.
	walkSpeed = 0.28
	flySpeed  = 1.5
)

func runLayout(b *testing.B, reg *registry.Registry, shift uint, merge int32, place func(*rand.Rand, int) mgl32.Vec3) {
	b.Helper()
	d, db := openDimWith(b, reg, b.TempDir(), func(c *Config) {
		c.SectionShift, c.MergeDistance, c.DiscardGenerated = shift, merge, true
	})
	defer func() { _ = db.Close() }()
	defer func() { _ = d.Close() }()
	viewers := make([]*Viewer, 0, benchPlayers)
	for i := range benchPlayers {
		rng := rand.New(rand.NewPCG(uint64(i), 2)) //nolint:gosec // a fixed run
		pos := place(rng, i)
		speed := float32(walkSpeed)
		if i%10 == 0 {
			speed = flySpeed
		}
		v := d.AddViewer(func(packet.Packet) {}, func() {})
		v.Move(chunkOf(pos), benchRadius)
		viewers = append(viewers, v)
		yaw := rng.Float64() * 2 * math.Pi
		d.AddEntity(EntityConfig{
			UniqueID: int64(i + 1), RuntimeID: uint64(i + 1), Pos: pos, Viewer: v, //nolint:gosec // small
			Spawn: func(e *Entity) []packet.Packet {
				return []packet.Packet{&packet.AddPlayer{EntityRuntimeID: e.RuntimeID()}}
			},
			Move: func(e *Entity) packet.Packet {
				return &packet.MovePlayer{EntityRuntimeID: e.RuntimeID(), Position: e.Position()}
			},
			Input: func(e *Entity) {
				if rng.IntN(100) == 0 {
					yaw = rng.Float64() * 2 * math.Pi
				}
				p := e.Position().Add(mgl32.Vec3{float32(math.Cos(yaw)) * speed, 0, float32(math.Sin(yaw)) * speed})
				old := e.chunk
				e.Apply(Movement{Pos: p})
				if c := chunkOf(p); c != old {
					v.Move(c, benchRadius)
				}
			},
		})
	}
	defer func() {
		for _, v := range viewers {
			v.Close()
		}
	}()
	time.Sleep(benchWarmup)

	seen := map[uint64]bool{}
	for _, r := range d.Regions() {
		seen[r.ID] = true
	}
	var worst, cpu time.Duration
	var maxes []time.Duration
	samples, regions, made := 0, 0, 0
	for end := time.Now().Add(benchMeasure); time.Now().Before(end); time.Sleep(TickDuration) {
		var tickMax, tickSum time.Duration
		infos := d.Regions()
		for _, r := range infos {
			if !seen[r.ID] {
				seen[r.ID] = true
				made++
			}
			tickMax = max(tickMax, r.LastTick)
			tickSum += r.LastTick
		}
		worst = max(worst, tickMax)
		maxes = append(maxes, tickMax)
		cpu += tickSum
		regions += len(infos)
		samples++
	}
	slices.Sort(maxes)
	b.ReportMetric(float64(maxes[len(maxes)*99/100].Microseconds())/1000, "p99-slowest-ms")
	b.ReportMetric(float64(worst.Microseconds())/1000, "worst-ms")
	b.ReportMetric(float64(cpu.Microseconds())/1000/float64(samples), "cpu-ms/tick")
	b.ReportMetric(float64(regions)/float64(samples), "regions")
	b.ReportMetric(float64(made)/benchMeasure.Seconds(), "new-regions/s")
}
