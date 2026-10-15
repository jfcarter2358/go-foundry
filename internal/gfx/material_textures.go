package gfx

// material_textures.go — making sure a material's textures are on the GPU.
//
// Everything else in the engine loads its textures by being walked: the level
// loader visits every chunk's geometry, the model loader every submesh, the
// emitter loader every layer. A material is not reached by any of those walks —
// it is named, and what it names is one indirection further out — so a wall
// wearing `rust_splat.fmat` would draw its blend texture as texture object 0,
// which samples black.
//
// So materials load their own. There are tens of them in a project rather than
// hundreds, every one is referenced by something or it would not have been
// written, and a texture already resident costs a map lookup — so loading all
// of them once, when the library loads, is both simpler and cheaper than
// working out which are reachable from the level being opened.
//
// That reasoning holds for world textures and stops at the terrain effects'
// own (material_fx_textures.go). A splat's layer atlases are a pair of
// 4352² textures built from sixteen files — tens of megabytes even
// compressed, and the better part of a second to pack — so they are built
// only for a material something draws: at level load for the materials its
// scenery wears (EnsureMaterialFxTextures), or on the first draw. The data
// maps load the same way. EnsureMaterialTextures leaves both alone.
//
// It lives in gfx rather than in internal/material because material must not
// import gfx: gfx imports material (material_surface.go), and the arrow only
// goes one way.

import (
	"log"

	"foundry/internal/material"
)

// LoadMaterials reads every .fmat in the project and uploads the textures they
// name. Called at startup, and again by the editor whenever a material is
// saved or a project is opened. Returns how many materials loaded.
func LoadMaterials() int {
	n := material.LoadAll()
	EnsureMaterialTextures()
	return n
}

// EnsureMaterialTextures uploads every texture the loaded materials refer to —
// their own albedo, their blend pairs and masks, their named normal maps, a
// sky material's six cubemap faces. Called at startup, on a material save and
// on a texture reload.
//
// The terrain and rock effects' own maps and splat atlases are not built
// here (see the file comment): level load and the draw path build them for
// the materials actually in use.
//
// A texture that will not load is logged once and left; the surface wearing it
// draws with whatever it names itself, which is a better failure than refusing
// to open the project.
func EnsureMaterialTextures() {
	for _, name := range material.Names() {
		d := material.Get(name)
		if d == nil {
			continue
		}
		fx := fxOwnTextures(name)
		for _, tex := range d.Textures() {
			if fx[tex] {
				continue
			}
			if err := EnsureTextureLoaded(tex); err != nil {
				log.Printf("material %s: texture %q: %v", name, tex, err)
			}
		}
	}
}

// fxOwnTextures is the textures a material's terrain and rock effects load
// their own way — a splat's layers (packed into arrays from their files) and
// the data maps — which must not also be uploaded as world textures: a
// four-layer 2k splat would cost another 85 MB, and a data map named by a
// path is not a texture name at all.
func fxOwnTextures(name string) map[string]bool {
	f, ok := resolveSurfaceFx(name)
	if !ok || !f.Any() {
		return nil
	}
	out := map[string]bool{}
	add := func(n string) {
		if n != "" {
			out[n] = true
		}
	}
	add(f.SplatWeights)
	add(f.SplatWeights2)
	add(f.SplatStrata)
	for _, l := range f.SplatLayers {
		add(l.Texture)
	}
	add(f.GroundColor)
	add(f.GroundHeight)
	return out
}

// MaterialTextureNames is every texture the loaded materials refer to, without
// loading anything. What the release packer and the unused-asset walk ask, so
// that a texture reached only through a material is packed and is not pruned.
func MaterialTextureNames() []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range material.Names() {
		d := material.Get(name)
		if d == nil {
			continue
		}
		for _, tex := range d.Textures() {
			if tex == "" || seen[tex] {
				continue
			}
			seen[tex] = true
			out = append(out, tex)
		}
	}
	return out
}
