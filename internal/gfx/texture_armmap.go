package gfx

// texture_armmap.go — ARM maps (not texture_arm.go: Go reads that suffix as
// GOARCH=arm): a texture's AO / roughness / metal, per texel
// (docs/lighting/PLAN.md, phase 1).
//
// The normal map's arrangement (texture_normal.go), for the specular's inputs:
// <name>_arm.png beside <name>.png is that texture's ARM map — red ambient
// occlusion, green roughness, blue metalness, the glTF channel order Poly
// Haven ships its sets in (phimport writes ph_<id>_arm.png) — and it is loaded
// with the albedo, by LoadTexture, so a surface wearing the texture is
// specular-mapped without a level saying so. A `pbr` material effect may name
// a different map; its factors multiply the map's channels either way.

import (
	"log"
	"path/filepath"
	"strings"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/fileutil"
)

// The hasArmMap modes (fragment.glsl).
const (
	ARMScales   = 1
	ARMReplaces = 2
)

// ARMMapSuffix is appended to a texture's name for its ARM map.
const ARMMapSuffix = fileutil.ARMMapSuffix

// ARMMapUnit is the texture image unit an ARM map binds to: past every unit
// the world program already reads (gi.go's half-resolution pair end at 46;
// 47 is the footprint trail map's).
const ARMMapUnit = 50

// armMapped is the set of albedo names known to have an ARM map resident
// under ARMMapName(name).
var armMapped = map[string]bool{}

// ARMMapName is the texture name a texture's ARM map lives under.
func ARMMapName(name string) string { return name + ARMMapSuffix }

// IsARMMapName reports whether name is an ARM map's rather than an albedo's.
func IsARMMapName(name string) bool { return fileutil.IsARMMapName(name) }

// HasARMMap reports whether the named texture has an ARM map on the GPU.
func HasARMMap(name string) bool {
	if !armMapped[name] {
		return false
	}
	h, ok := Textures[ARMMapName(name)]
	return ok && h != 0
}

// armMapPath is where the named texture's ARM map would be: beside the
// albedo's own file, else in textures/.
func armMapPath(name string) string {
	if p, ok := TextureFile(name); ok {
		return strings.TrimSuffix(p, filepath.Ext(p)) + ARMMapSuffix + ".png"
	}
	return filepath.Join(fileutil.TexturesDir(), ARMMapName(name)+".png")
}

// ARM maps load on demand, decoded in the background (texture_prefetch.go):
// a surface that would sample one asks for it when it is drawn
// (requestARMMap), draws without it — as a plain dielectric — until it is
// resident, and PumpARMMaps, once a frame, uploads the decodes that have
// finished. So a map nothing samples (a splat layer's, a triplanar rock's) is
// never loaded at all.
var (
	armPending = map[string]bool{} // albedo name -> decode asked for
	armAbsent  = map[string]bool{} // albedo name -> no file on disk
)

// requestARMMap starts loading the named texture's ARM map if it has one and
// it is neither resident nor on its way.
func requestARMMap(name string) {
	if name == "" || IsARMMapName(name) || IsNormalMapName(name) || armPending[name] || armAbsent[name] {
		return
	}
	if _, loaded := Textures[ARMMapName(name)]; loaded {
		armMapped[name] = true
		return
	}
	if !TextureUploadsEnabled || !fileExists(armMapPath(name)) {
		armAbsent[name] = true
		return
	}
	armPending[name] = true
	if _, known := TextureNames[ARMMapName(name)]; known {
		PrefetchTextures([]string{ARMMapName(name)})
	}
}

// ARMMapsFor is the ARM maps of names that exist on disk and are not loaded
// yet, by texture name — for a level load to load with its textures, so
// they are resident before play rather than arriving in it. Each still
// counts as that texture's ARM map (armMapped) once it is loaded.
func ARMMapsFor(names []string) []string {
	var out []string
	for _, name := range names {
		if name == "" || IsARMMapName(name) || IsNormalMapName(name) || armAbsent[name] {
			continue
		}
		arm := ARMMapName(name)
		if _, loaded := Textures[arm]; loaded {
			armMapped[name] = true
			continue
		}
		if _, known := TextureNames[arm]; !known {
			armAbsent[name] = true
			continue
		}
		out = append(out, arm)
	}
	return out
}

// PumpARMMaps uploads the ARM maps whose decodes have finished — at most one
// a frame, an upload with its mips being several milliseconds. Main thread,
// once a frame (render.DrawWorld); a frame with nothing pending costs a map
// length. A level load loads its surfaces' maps up front (ARMMapsFor), so
// this is for what that missed.
func PumpARMMaps() {
	if len(armPending) == 0 {
		return
	}
	for name := range armPending {
		path := armMapPath(name)
		prefetch.Lock()
		p, inFlight := prefetch.files[path]
		prefetch.Unlock()
		if inFlight {
			select {
			case <-p.done:
			default:
				continue // still decoding
			}
		} else if _, known := TextureNames[ARMMapName(name)]; known {
			// Dropped with a level load's leftovers (DropPrefetchedTextures):
			// ask again rather than decode it here.
			PrefetchTextures([]string{ARMMapName(name)})
			continue
		}
		delete(armPending, name)
		if err := LoadTexture(ARMMapName(name), path); err != nil {
			log.Printf("WARNING: ARM map for %q: %v", name, err)
			armAbsent[name] = true
			continue
		}
		armMapped[name] = true
		return // one upload a frame
	}
}

// ARMMapFor is the ARM texture a surface wearing texture should sample: the
// one a material named (loaded at once: a material asked for it by name), or
// the texture's own once it is resident — asked for, if not, and "" until it
// arrives.
func ARMMapFor(texture, named string) string {
	if named != "" {
		if h, ok := Textures[named]; ok && h != 0 {
			return named
		}
		if _, ok := TextureNames[named]; ok && EnsureTextureLoaded(named) == nil {
			if h, ok := Textures[named]; ok && h != 0 {
				return named
			}
		}
		return ""
	}
	if !HasARMMap(texture) {
		// Not yet known to be resident: a level load may have loaded it
		// (ARMMapsFor), which requestARMMap notices; otherwise it is asked for.
		requestARMMap(texture)
	}
	if HasARMMap(texture) {
		return ARMMapName(texture)
	}
	return ""
}

// BindARMMap sets the hasArmMap/armMap pair for a draw: the map bound to
// ARMMapUnit when there is one, hasArmMap 0 when there is not. mode is what
// hasArmMap says of it: ARMScales (a pbr effect's factors multiply the map)
// or ARMReplaces (no pbr effect: the map's own roughness and metal stand).
func BindARMMap(arm string, mode int32, hasLoc, samplerLoc int32) {
	h := uint32(0)
	if arm != "" {
		h = Textures[arm]
	}
	if h == 0 {
		gl.Uniform1i(hasLoc, 0)
		return
	}
	gl.Uniform1i(hasLoc, mode)
	gl.ActiveTexture(gl.TEXTURE0 + ARMMapUnit)
	gl.BindTexture(gl.TEXTURE_2D, h)
	gl.Uniform1i(samplerLoc, ARMMapUnit)
	gl.ActiveTexture(gl.TEXTURE0)
}
