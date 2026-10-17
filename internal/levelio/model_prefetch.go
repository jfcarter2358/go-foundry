package levelio

// model_prefetch.go — parsing every model a level wears before the loops that
// place its entities, props and viewmodel ask for them one at a time.
//
// Those loops run on the main thread, in placement order, and each first use
// of a model was a parse of its .fmdl — most of a second of YAML on a level
// with a few rigged characters — followed by a synchronous decode of its
// texture. Both are pure CPU over files: the parses go to every core at once
// (world.PrefetchModelDocuments, into the document cache the loops read), and
// the textures they name go to the texture prefetch, which LoadTexture takes
// from when the loop reaches them. The loops themselves are unchanged and run
// in the same order; they just find their work already done.

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/model"
	"foundry/internal/nanite"
	"foundry/internal/world"
)

// prefetchLevelModels parses the models of ents, of the raw scenery and of the
// player block, and starts their textures decoding — along with the sprites',
// which the object loop loads the same synchronous way.
func prefetchLevelModels(ents []world.Entity, scenery []world.RawScenery, player *world.RawPlayer, sprites []world.RawSprite) {
	var paths []string
	// The texture each attachment overrides with, by resolved path, so the
	// prefetch asks for what will actually be drawn.
	var overrides []string
	addModels := func(models []world.EntityModel) {
		for i := range models {
			if p := prefetchableModel(models[i].Path); p != "" {
				paths = append(paths, p)
			}
			if models[i].Texture != "" {
				overrides = append(overrides, models[i].Texture)
			}
		}
	}
	for i := range ents {
		addModels(ents[i].Models)
	}
	if p, err := world.PlayerSettingsFromRaw(player); err == nil {
		addModels(p.Models)
		addModels(p.Body.Models)
	}
	for i := range scenery {
		if p := prefetchableModel(scenery[i].Model); p != "" {
			paths = append(paths, p)
		}
	}
	docTextures := world.PrefetchModelDocuments(paths)
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, p := range paths {
		add(docTextures[p])
	}
	for _, n := range overrides {
		add(n)
	}
	for i := range sprites {
		add(sprites[i].Texture)
	}
	gfx.PrefetchTextures(names)
}

// prefetchableModel is the file a model path loads from, or "" for one the
// document parser does not read (a cooked Nanite mesh, which has a loader of
// its own) or none at all.
func prefetchableModel(path string) string {
	if path == "" {
		return ""
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case model.ModelExt, ".obj":
		return fileutil.ResolveAssetPath(path)
	}
	return ""
}

// cookedPrefetch is a level's cooked (Nanite) scenery meshes being read on
// every core, into nanite.Load's cache, before the main thread's scenery loop
// asks for them one placement at a time. A generated level wears a few hundred
// distinct scans over tens of thousands of placements, and each first read is
// a gob decode and a decompress: work for the other cores while the main one
// keeps the bar moving.
type cookedPrefetch struct {
	total int
	read  atomic.Int64
	// done delivers the textures the meshes' slots and the placements'
	// overrides name, once every mesh has been read, for the texture
	// prefetch — which only the main thread may start.
	done chan []string
	// over is set, on the main thread, once done has been taken.
	over bool
}

// finished reports whether every mesh has been read, and the first time it
// is, starts their textures decoding on the other cores — LoadTexture takes
// them as the scenery loop reaches each. Main thread only.
func (cp *cookedPrefetch) finished() bool {
	if cp.over {
		return true
	}
	select {
	case textures := <-cp.done:
		gfx.PrefetchTextures(textures)
		cp.over = true
	default:
	}
	return cp.over
}

// startCookedPrefetch starts reading the cooked meshes of the placements in
// lvl that will load (their chunk wants geometry). Nil when there are none.
func startCookedPrefetch(lvl *world.Level) *cookedPrefetch {
	byID := world.ChunksByID(lvl)
	seen := map[string]bool{}
	var paths, overrides []string
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if !nanite.IsCookedPath(s.Model) {
			continue
		}
		if _, unload := world.PlacementChunkState(byID, s.Chunk); unload {
			continue
		}
		if p := fileutil.ResolveAssetPath(s.Model); !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
		// And its far impostor's cards (world/scenery_draw.go).
		if s.Impostor != "" && nanite.IsCookedPath(s.Impostor) {
			if p := fileutil.ResolveAssetPath(s.Impostor); !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
		// A slot override naming a texture rather than a material. Asked
		// here, on the main thread, since the material registry is its.
		for _, t := range s.Materials {
			if t != "" && !seen["tex:"+t] && !material.Exists(t) {
				seen["tex:"+t] = true
				overrides = append(overrides, t)
			}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	cp := &cookedPrefetch{total: len(paths), done: make(chan []string, 1)}
	go func() {
		var mu sync.Mutex
		textures := append([]string(nil), overrides...)
		next := atomic.Int64{}
		var wg sync.WaitGroup
		for w := 0; w < runtime.NumCPU(); w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1)) - 1
					if i >= len(paths) {
						return
					}
					// An error is the scenery loop's to report, when it
					// asks for the same file.
					if m, err := nanite.Load(paths[i]); err == nil {
						mu.Lock()
						for si := range m.Slots {
							textures = append(textures, m.Slots[si].Texture)
						}
						mu.Unlock()
					}
					cp.read.Add(1)
				}
			}()
		}
		wg.Wait()
		cp.done <- textures
	}()
	return cp
}
