package editor

// editor_model_reload.go — a model saved from the model editor shows up in the
// level straight away.
//
// Nothing else would pick the save up. Entities, props and the player's
// viewmodel upload their mesh once, when the level loads, and play mode runs on
// that same live level rather than reading it back from disk — so without this
// a re-saved .fmdl would stay stale until the level was reopened. The parsed
// document cache (world.loadModelDocument) already checks the file's size and
// modification time, so a Load after the save reads the new file; all this has
// to do is call Load on everything wearing it.
//
// The reload cannot run where the save happens. The model editor draws inside
// its own window, with its own GL context current, and vertex arrays — unlike
// textures — are not shared between contexts: a level entity rebuilt there
// would get VAOs the main window cannot draw. So the save parks the path here
// and the main loop drains it (devDrainWork), with the main context current.
// The entity editor's preview is rebuilt in that window's own frame instead, by
// marking it dirty.

import (
	"path/filepath"

	"foundry/internal/fileutil"
	"foundry/internal/model"
	"foundry/internal/world"
)

// pendingModelReloads is the saved model files waiting for the main loop,
// as cleaned absolute paths.
var pendingModelReloads []string

// requestModelReload queues everything wearing the model at path for a reload.
func requestModelReload(path string) {
	if path == "" {
		return
	}
	pendingModelReloads = append(pendingModelReloads, modelReloadKey(path))
	// The entity editor rebuilds its preview from the type, in its own window's
	// frame, so the new mesh reaches it without the main loop's help.
	if entityDef != nil {
		for i := range entityDef.Models {
			if modelReloadKey(fileutil.ResolveAssetPath(entityDef.Models[i].Path)) == modelReloadKey(path) {
				entityPreviewInvalidate()
				break
			}
		}
	}
}

// requestSkeletonReload queues every *other* model on the skeleton called name,
// for when its .fskl has just been rewritten. Reports how many, so the caller
// can say whose rig it just changed.
//
// The models' own .fmdl files are untouched by a skeleton save, so nothing else
// would notice: the reload works because the parsed-document cache stamps the
// skeleton file alongside the model (world.skeletonStamp), and that stamp is
// what has moved.
func requestSkeletonReload(name, excludePath string) int {
	if name == "" {
		return 0
	}
	n := 0
	for _, path := range model.ModelPathsUsingSkeleton(name) {
		if modelReloadKey(path) == modelReloadKey(excludePath) {
			continue
		}
		requestModelReload(path)
		n++
	}
	return n
}

// modelReloadKey is the form paths are compared in: an attachment names its
// model relative to the resources directory, the model editor by absolute path.
func modelReloadKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

// DrainModelReloads reloads everything wearing a model saved since the last
// frame. Called once per frame from the main loop with the main GL context
// current — see the file comment.
func DrainModelReloads() {
	if len(pendingModelReloads) == 0 {
		return
	}
	saved := make(map[string]bool, len(pendingModelReloads))
	for _, p := range pendingModelReloads {
		saved[p] = true
	}
	pendingModelReloads = nil

	n := reloadModelsIn(Editor.level, saved)
	if world.PlayerEntity != nil {
		n += reloadSavedEntityModels(world.PlayerEntity, saved)
	}
	if brushGhost.Model != "" && saved[modelReloadKey(fileutil.ResolveAssetPath(brushGhost.Model))] {
		if err := brushGhost.Load(); err == nil {
			brushGhost.SetAlpha(brushGhostAlpha)
		}
	}
	if n > 0 {
		editorSetStatus("Reloaded %d model(s) from the saved file", n)
	}
}

// reloadModelsIn reloads every entity attachment and prop in lvl whose model is
// one of saved, and reports how many it reloaded.
func reloadModelsIn(lvl *world.Level, saved map[string]bool) int {
	if lvl == nil {
		return 0
	}
	n := 0
	for i := range lvl.Entities {
		n += reloadSavedEntityModels(&lvl.Entities[i], saved)
	}
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if s.Model == "" || !saved[modelReloadKey(fileutil.ResolveAssetPath(s.Model))] {
			continue
		}
		if err := s.Load(); err != nil {
			editorSetStatus("Scenery %s: %v", s.ID, err)
			continue
		}
		n++
	}
	return n
}

// reloadSavedEntityModels reloads e's attachments whose model is one of saved.
// Load keeps the attachment's authored fields and restarts its autoplay, so an
// entity mid-clip starts that clip again — the mesh it was posing is gone.
func reloadSavedEntityModels(e *world.Entity, saved map[string]bool) int {
	n := 0
	for i := range e.Models {
		m := &e.Models[i]
		if m.Path == "" || !saved[modelReloadKey(fileutil.ResolveAssetPath(m.Path))] {
			continue
		}
		if err := m.Load(e.ID); err != nil {
			editorSetStatus("Entity %s: %v", e.ID, err)
			continue
		}
		n++
	}
	return n
}
