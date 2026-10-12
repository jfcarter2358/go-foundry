package levelio

// chunks_only.go — a level file's chunks and nothing else.
//
// `foundry gen run` splices a generator's placements into a level FILE, and a
// placement marked `chunk: auto` streams with whichever chunk's footprint it
// stands in (world.SceneryChunkAt). Answering that needs the chunks' bounds,
// which need their brushes built — but not the level's scenery, entities,
// textures or anything else LoadLevel spends its time on (the generated tiles
// themselves among them). So this builds the chunks alone, exactly as
// LoadLevel does.
//
// The caller turns GPU uploads off first (gfx.GeometryUploadsEnabled) when
// there is no GL context, as every headless tool does.

import (
	"foundry/internal/world"
)

// LoadChunks builds the chunks of the level file at path.
func LoadChunks(path string) ([]world.Chunk, error) {
	raw, err := readRawLevelFile(path)
	if err != nil {
		return nil, err
	}
	out := make([]world.Chunk, 0, len(raw.Chunks))
	for _, rc := range raw.Chunks {
		ch, _, err := loadRawChunk(rc)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, nil
}
