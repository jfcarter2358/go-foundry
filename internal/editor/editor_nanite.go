package editor

// editor_nanite.go — cooking and placing Nanite meshes from the editor. Phase 1
// of the Nanite plan (docs/nanite/PHASE_1.md).
//
// A Nanite mesh is a dense source (glTF, OBJ, or one of the project's own
// .fmdl) cooked once into a .fnm beside it (internal/nanitecook). Three ways in:
//
//   - Window > Cook Nanite Meshes cooks every glTF under resources/ that has
//     changed since it was last cooked — the import step for scans and
//     sculpts, which arrive as glTF and cannot be placed until cooked.
//   - A scenery placement of a .fmdl or .obj gets a Nanite group with a
//     "Cook and use" button: the same model, cooked beside itself, and the
//     placement pointed at the cooked file.
//   - Every scenery model picker lists the cooked meshes beside the models.
//
// A placement of a cooked mesh shows what it is (triangles, clusters, whether
// the GPU cull draws it) and a texture override per material slot, which is
// how one cooked rock is placed in several looks.
//
// `foundry nanite cook` is the same cook from a terminal; both go through
// nanitecook.CookFile, so a mesh cooked either way is the same file.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/model"
	"foundry/internal/nanite"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// sceneryModelFileCombo is modelFileCombo for a scenery placement, which can
// also take a cooked Nanite mesh.
func sceneryModelFileCombo(label string, path *string) bool {
	return modelFileComboOf(label, path, sceneryModelFileNames)
}

// sceneryModelFileNames is modelFileNames plus every cooked mesh under
// models/.
func sceneryModelFileNames() []string {
	names := modelFileNames()
	for _, f := range fileutil.ScanFilesByExt(model.ModelsDir(), nanite.Ext) {
		names = append(names, model.ModelsDirName+"/"+f)
	}
	sort.Strings(names)
	return names
}

// editorCookAllNanite cooks every stale glTF under resources/.
func editorCookAllNanite() {
	sources := nanitecook.FindSources(fileutil.ResourcesPath)
	if len(sources) == 0 {
		editorSetStatus("Nanite: no glTF (.gltf, .glb) under resources/ to cook")
		return
	}
	cooked, fresh, failed := 0, 0, 0
	var firstErr error
	for _, src := range sources {
		res, err := nanitecook.CookFile(src, "", false, nanitecook.ProjectOptions())
		switch {
		case err != nil:
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", filepath.Base(src), err)
			}
		case res.Skipped:
			fresh++
		default:
			cooked++
		}
	}
	if failed > 0 {
		editorSetStatus("Nanite: cooked %d, %d up to date, %d failed — %v", cooked, fresh, failed, firstErr)
		return
	}
	editorSetStatus("Nanite: cooked %d, %d already up to date", cooked, fresh)
}

// editorSceneryNanite draws a placement's Nanite group.
func editorSceneryNanite(s *world.Scenery) {
	if s.Nanite == nil {
		editorTextMutedWrapped("A dense model draws faster cooked: cut into clusters the GPU culls, " +
			"one shared buffer for every placement, and a distance field for GI.")
		if imgui.Button("Cook and use##sccook") {
			editorCookSceneryModel(s)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Cook this model into a .fnm beside it and point the\nplacement at the cooked file. The source is not touched.")
		}
		return
	}
	m := s.Nanite
	path := "classic path (FOUNDRY_NANITE_MESHES=0, or no GPU cull)"
	if s.NaniteGPU {
		path = "GPU cluster cull"
	}
	editorTextMutedWrapped(fmt.Sprintf("%d triangles, %d clusters over %d levels of detail, drawn by the %s",
		m.TriangleCount(), len(m.Clusters), m.Levels, path))
	if m.Source != "" {
		fresh, err := nanitecook.UpToDate(fileutil.ResolveAssetPath(m.Source), fileutil.ResolveAssetPath(s.Model))
		switch {
		case err != nil:
			editorTextMutedWrapped("Cooked from " + m.Source + " (not found)")
		case !fresh:
			editorTextMutedWrapped("Cooked from " + m.Source + " — which has changed since")
			if imgui.Button("Re-cook##screcook") {
				if _, err := nanitecook.CookFile(fileutil.ResolveAssetPath(m.Source), fileutil.ResolveAssetPath(s.Model), true, nanitecook.ProjectOptions()); err != nil {
					editorSetStatus("Nanite: %v", err)
				} else {
					editorSceneryReload(s)
				}
			}
		default:
			editorTextMutedWrapped("Cooked from " + m.Source)
		}
	}
	for si, slot := range m.Slots {
		tex := s.SlotTexture(si)
		label := fmt.Sprintf("%s##scslot%d", slot.Name, si)
		if textureCombo(label, &tex, true) {
			EditorPushUndo()
			if s.Materials == nil {
				s.Materials = map[string]string{}
			}
			if tex == slot.Texture {
				delete(s.Materials, slot.Name)
			} else {
				s.Materials[slot.Name] = tex
			}
			editorSceneryReload(s)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(fmt.Sprintf("Material slot %q: cooked with %q.\nPick another texture to override it on this placement.", slot.Name, slot.Texture))
		}
	}
}

// editorCookSceneryModel cooks s's model and points s at the cooked file.
func editorCookSceneryModel(s *world.Scenery) {
	src := fileutil.ResolveAssetPath(s.Model)
	res, err := nanitecook.CookFile(src, "", false, nanitecook.ProjectOptions())
	if err != nil {
		editorSetStatus("Nanite: %v", err)
		return
	}
	cooked := strings.TrimSuffix(s.Model, filepath.Ext(s.Model)) + nanite.Ext
	EditorPushUndo() // of the placement before it pointed at the cook
	s.Model = cooked
	editorSceneryReload(s)
	if res.Skipped {
		editorSetStatus("Nanite: %s was already cooked; %s now uses it", res.Out, s.ID)
	} else {
		editorSetStatus("Nanite: cooked %s (%s)", res.Out, nanitecook.Describe(res.Mesh))
	}
}
