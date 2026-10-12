package world

import (
	"testing"

	"github.com/helix-bedrock/helix/server/block/cube"
)

var redstoneDirtyTickBenchmarkPower int

func BenchmarkRedstoneDirtyTickLongLineWithClocks(b *testing.B) {
	const lineLength = 96

	w := Config{Synchronous: true, Blocks: redstoneSignalLossTestRegistry()}.New()
	defer w.Close()

	runWorld(w, func(tx *Tx) {
		clockA := cube.Pos{-1, 64, 0}
		clockB := cube.Pos{lineLength / 2, 64, 1}
		line := make([]cube.Pos, lineLength)
		for x := range lineLength {
			line[x] = cube.Pos{x, 64, 0}
			tx.SetBlock(line[x], redstoneLossRelayer{}, nil)
		}
		tx.SetBlock(clockA, redstoneLossSource{Power: 15}, nil)
		tx.SetBlock(clockB, redstoneLossSource{Power: 15}, nil)
		tx.SetBlock(cube.Pos{lineLength, 64, 0}, redstoneLossConsumer{}, nil)
		tx.r.redstone.tick(tx, 0)

		b.ReportAllocs()
		b.ResetTimer()
		for tick := range b.N {
			tx.r.redstone.invalidateAround(clockA, clockA, RedstoneUpdateCauseBlockUpdate, tx.Range())
			tx.r.redstone.invalidateAround(clockB, clockB, RedstoneUpdateCauseBlockUpdate, tx.Range())
			tx.r.redstone.tick(tx, int64(tick+1))
		}
		b.StopTimer()

		if consumer, ok := tx.Block(cube.Pos{lineLength, 64, 0}).(redstoneLossConsumer); ok {
			redstoneDirtyTickBenchmarkPower = consumer.Power
		}
	})
}
