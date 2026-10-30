package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Pack is a resource pack sent to players.
type Pack interface {
	// Info describes the pack in ResourcePacksInfo. Info().Size is the
	// size of the pack file.
	Info() protocol.TexturePackInfo
	// Type is the pack's packet.ResourcePackType value.
	Type() byte
	// Checksum is the SHA-256 of the pack file.
	Checksum() [32]byte
	// ReaderAt reads the pack file.
	io.ReaderAt
}

// packChunkSize is the size of one pack chunk. Not taken from BDS yet.
const packChunkSize = 256 << 10

// resourcePacks sends the pack list, the packs the client asks for and the
// pack stack, until the client is done.
func resourcePacks(ctx context.Context, lg *Login) error {
	conf := lg.l.conf
	info := &packet.ResourcePacksInfo{TexturePackRequired: conf.PacksRequired}
	for _, p := range conf.Packs {
		i := p.Info()
		info.HasScripts = info.HasScripts || i.HasScripts
		info.HasAddons = info.HasAddons || i.AddonPack
		info.TexturePacks = append(info.TexturePacks, i)
	}
	if err := lg.Write(info); err != nil {
		return err
	}
	var sending []Pack
	for {
		pk, err := lg.Read(ctx)
		if err != nil {
			return err
		}
		switch pk := pk.(type) {
		case *packet.ClientCacheStatus:
			lg.CacheEnabled = pk.Enabled
		case *packet.ResourcePackChunkRequest:
			if err := sendChunk(lg, sending, pk); err != nil {
				return err
			}
		case *packet.ResourcePackClientResponse:
			switch pk.Response {
			case packet.PackResponseRefused:
				return errors.New("client refused the resource packs")
			case packet.PackResponseSendPacks:
				sending = sending[:0]
				for _, id := range pk.PacksToDownload {
					p, ok := findPack(conf.Packs, id)
					if !ok {
						return fmt.Errorf("client asked for unknown pack %q", id)
					}
					sending = append(sending, p)
					if err := lg.c.WritePacket(dataInfo(p)); err != nil {
						return err
					}
				}
				if err := lg.c.Flush(); err != nil {
					return err
				}
			case packet.PackResponseAllPacksDownloaded:
				if err := lg.Write(packStack(conf)); err != nil {
					return err
				}
			case packet.PackResponseCompleted:
				return nil
			default:
				return fmt.Errorf("unknown resource pack response %d", pk.Response)
			}
		}
	}
}

// packStack is the ResourcePackStack: the packs, the base game version and
// the experiments (BDS: built when the client has every pack).
func packStack(conf Config) *packet.ResourcePackStack {
	stack := &packet.ResourcePackStack{TexturePackRequired: conf.PacksRequired, BaseGameVersion: protocol.CurrentVersion, Experiments: conf.Experiments}
	for _, p := range conf.Packs {
		i := p.Info()
		stack.TexturePacks = append(stack.TexturePacks, protocol.StackResourcePack{UUID: i.UUID.String(), Version: i.Version, SubPackName: i.SubPackName})
	}
	return stack
}

// findPack finds a pack by "uuid" or "uuid_version".
func findPack(packs []Pack, id string) (Pack, bool) {
	u, v, hasV := strings.Cut(id, "_")
	for _, p := range packs {
		i := p.Info()
		if i.UUID.String() == u && (!hasV || v == "" || v == i.Version) {
			return p, true
		}
	}
	return nil, false
}

func dataInfo(p Pack) *packet.ResourcePackDataInfo {
	i := p.Info()
	sum := p.Checksum()
	return &packet.ResourcePackDataInfo{
		UUID: i.UUID.String() + "_" + i.Version, DataChunkSize: packChunkSize,
		ChunkCount: uint32((i.Size + packChunkSize - 1) / packChunkSize), Size: i.Size,
		Hash: sum[:], PackType: p.Type(),
	}
}

func sendChunk(lg *Login, sending []Pack, req *packet.ResourcePackChunkRequest) error {
	u, _, _ := strings.Cut(req.UUID, "_")
	for _, p := range sending {
		i := p.Info()
		if i.UUID.String() != u {
			continue
		}
		if req.ChunkIndex < 0 {
			return fmt.Errorf("pack %s: negative chunk %d", u, req.ChunkIndex)
		}
		off := uint64(req.ChunkIndex) * packChunkSize
		if off >= i.Size {
			return fmt.Errorf("pack %s: chunk %d out of range", u, req.ChunkIndex)
		}
		data := make([]byte, min(packChunkSize, i.Size-off))
		if _, err := p.ReadAt(data, int64(off)); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read pack %s: %w", u, err)
		}
		return lg.Write(&packet.ResourcePackChunkData{UUID: req.UUID, ChunkIndex: uint32(req.ChunkIndex), DataOffset: off, Data: data})
	}
	return fmt.Errorf("chunk asked for pack %q that is not being sent", req.UUID)
}
