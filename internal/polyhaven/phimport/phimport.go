// Package phimport turns downloaded Poly Haven assets into engine assets
// (docs/canyon/SPEC.md phase 4; docs/canyon/PHASE_4.md).
//
// A MODEL (ImportModel) arrives as a glTF with a .bin and JPG textures
// (diffuse, OpenGL-convention normal, ARM). It becomes:
//
//	resources/models/polyhaven/<id>/<id>.gltf      the glTF, its images re-pointed
//	                                               at the engine textures below
//	resources/models/polyhaven/<id>/<bin>          its buffer, as downloaded
//	resources/models/polyhaven/<id>/<id>[_t<n>][_b<back>].fnm
//	                                               the cooked Nanite mesh(es), in the
//	                                               placement frame (frame.go)
//	resources/models/polyhaven/<id>/polyhaven.yaml the provenance record
//	resources/textures/ph_<id>[_<part>].png        the diffuse (alpha merged in
//	                                               from the asset's alpha map for a
//	                                               cutout material)
//	resources/textures/ph_<id>[_<part>]_normal.png the nor_gl normal map — the engine's
//	                                               "<name>_normal beside a texture"
//	resources/textures/ph_<id>[_<part>]_arm.png    AO/roughness/metal, kept for the
//	                                               material phase; _height when the
//	                                               asset has a displacement map
//
// <part> is the material's name less the asset's ID (quiver_tree_01_trunk →
// trunk) when the model has more than one material.
//
// A TEXTURE SET (ImportTextures) becomes ph_<id>.png, _normal, _arm, _rough
// and _ao (both split out of the ARM), _height (the displacement), and
// resources/textures/polyhaven/<id>/polyhaven.yaml.
//
// Everything downloaded goes through the polyhaven client's md5-addressed
// store, so a re-import at the same resolution downloads nothing, and an
// unchanged mesh is not re-cooked (the .fnm carries its source's hash). The
// textures are rewritten only when their sources' md5s (recorded in the
// provenance) change.
//
// The runtime only decodes PNG, so the JPGs are converted here, once, on
// import — image/jpeg is the importer's dependency, not gfx's.
package phimport

import (
	"fmt"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
)

// Prefix starts every imported texture's name.
const Prefix = "ph_"

// ModelsDir is where imported models go, under resources/.
const ModelsDir = "models/polyhaven"

// TextureSetsDir is where texture sets' provenance goes, under resources/
// (their PNGs are in textures/ itself: the runtime scans it flat).
const TextureSetsDir = "textures/polyhaven"

// ProvenanceFile is the provenance record's name.
const ProvenanceFile = "polyhaven.yaml"

// Log is a progress line sink.
type Log func(format string, args ...any)

// Stage reports progress: the step's name and the fraction done.
type Stage func(step string, done float32)

// TextureName is an imported texture's engine name: ph_<id>[_<part>].
func TextureName(id, part string) string {
	if part == "" {
		return Prefix + id
	}
	return Prefix + id + "_" + part
}

// ModelDir is the asset's directory under resources/ (slash separated).
func ModelDir(id string) string { return ModelsDir + "/" + id }

// texturePath is a texture's absolute path.
func texturePath(resources, name string) string {
	return filepath.Join(resources, fileutil.TexturesDirName, name+".png")
}

// partName is a material's part: its name less the asset ID and separators,
// made safe for a file name; "" when that leaves nothing.
func partName(id, material string) string {
	s := strings.TrimPrefix(strings.ToLower(material), strings.ToLower(id))
	s = strings.Trim(s, "_-. ")
	out := []byte{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '_' || c == '-' || c == ' ' || c == '.':
			if len(out) > 0 && out[len(out)-1] != '_' {
				out = append(out, '_')
			}
		}
	}
	p := strings.Trim(string(out), "_")
	if p == "normal" || strings.HasSuffix(p, "_normal") {
		p += "x" // never a name the normal-map convention would swallow
	}
	return p
}

// pickResolution is the resolution to use from what is offered: want, or
// the largest below it, or the smallest above it.
func pickResolution(offered []string, want string) (string, error) {
	if len(offered) == 0 {
		return "", fmt.Errorf("no resolutions offered")
	}
	w := resPixels(want)
	best := ""
	for _, r := range offered {
		if r == want {
			return r, nil
		}
		if p := resPixels(r); p > 0 && p <= w && (best == "" || p > resPixels(best)) {
			best = r
		}
	}
	if best != "" {
		return best, nil
	}
	smallest := offered[0]
	for _, r := range offered {
		if resPixels(r) < resPixels(smallest) {
			smallest = r
		}
	}
	return smallest, nil
}

func resPixels(r string) int {
	var n int
	if _, err := fmt.Sscanf(strings.ToLower(r), "%dk", &n); err != nil {
		return 0
	}
	return n
}
