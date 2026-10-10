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
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/model"
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
