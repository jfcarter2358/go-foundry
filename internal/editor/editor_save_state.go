package editor

// editor_save_state.go — whether each editor's document still matches what was
// last saved, the "Saved"/"Unsaved Changes" chip every toolbar carries, and
// Save All.
//
// The documents' own Dirty flags cannot answer this. The level has none; the
// model, particle and UI documents have one, but it means "rebuild derived
// state" and is cleared every frame by whatever consumes it. So the question is
// answered by content instead: a fingerprint of the document as its file would
// store it (fileutil.Fingerprint), taken when it is loaded or saved and compared
// with one taken now. That is also the honest answer after an undo — stepping
// back to the saved state reads "Saved" again.
//
// Fingerprinting is not free (a large model ~15 ms, level1 ~20 ms), so the
// current one is only retaken when something could have changed it, never
// mid-drag, and at most every so often. The script and entity editors already
// track this exactly and are asked directly.

import (
	"fmt"
	"strings"

	"foundry/internal/aitree"
	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/material"
	"foundry/internal/model"
	"foundry/internal/particle"
	"foundry/internal/platform"
	"foundry/internal/textureui"
	"foundry/internal/ui"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// savedState tracks one editor's document against its last save.
type savedState struct {
	base, cur uint64
	set       bool   // base describes the open document
	key       any    // what cur was taken at; a different key means look again
	takenAt   uint32 // SDL ticks when cur was taken
}

// docKey is the change signal for a document with an edit counter. The pointer
// is in it because undo swaps the document for a snapshot rather than editing
// it back.
type docKey struct {
	doc any
	rev uint64
}

// baseline records fp as the saved state of the document identified by key:
// just loaded, just created, or just written.
func (s *savedState) baseline(key any, fp uint64) {
	s.base, s.cur, s.set, s.key = fp, fp, true, key
	s.takenAt = sdl.GetTicks()
}

// refresh retakes the current fingerprint if key says the document may have
// changed, the mouse is not mid-gesture, and minGap ms have passed since the
// last one. A document nothing baselined is baselined here, as it stands.
func (s *savedState) refresh(key any, minGap uint32, fingerprint func() uint64) {
	if !s.set {
		s.baseline(key, fingerprint())
		return
	}
	if key == s.key {
		return
	}
	now := sdl.GetTicks()
	if !saveStateFresh && (platform.MouseButtons != 0 || now-s.takenAt < minGap) {
		return
	}
	s.cur, s.key, s.takenAt = fingerprint(), key, now
}

func (s *savedState) dirty() bool { return s.set && s.cur != s.base }

// saveStateFresh makes every check below take the fingerprint now, whatever the
// throttles say — Save All must not skip an edit made a moment ago.
var saveStateFresh bool

var (
	levelSaved    savedState
	modelSaved    savedState
	particleSaved savedState
	materialSaved savedState
	uiSaved       savedState
	aiSaved       savedState
)

// ---------- the level ----------

// The level has no edit counter, so its change signal is input: an edit is
// always the consequence of a key, a click or the wheel, in some window. The
// fingerprint waits for input to settle, since the edit an event causes can
// land a frame or two after it.
var (
	editorInputGen uint64
	editorInputAt  uint32
)

// NoteInputEvent counts the events that can edit something. Called for every
// event, ahead of routing, so a secondary window's input counts too.
func NoteInputEvent(event sdl.Event) {
	switch event.(type) {
	case sdl.KeyboardEvent, *sdl.KeyboardEvent, sdl.MouseButtonEvent, *sdl.MouseButtonEvent,
		sdl.MouseWheelEvent, *sdl.MouseWheelEvent, sdl.TextInputEvent, *sdl.TextInputEvent,
		sdl.DropEvent, *sdl.DropEvent:
		editorInputGen++
		editorInputAt = sdl.GetTicks()
	}
}

func levelFingerprint() uint64 {
	raw, err := editorLevelToRaw()
	if err != nil {
		return 0
	}
	return fileutil.Fingerprint(raw)
}

// levelMarkSaved baselines the open level: after a load or a save.
func levelMarkSaved() {
	if Editor.level == nil {
		return
	}
	levelSaved.baseline(editorInputGen, levelFingerprint())
}

// editorLevelDirty reports whether the level differs from its file. A play
// session keeps the last answer: the running game moves everything, and what
// the author has made is put back when it ends.
func editorLevelDirty() bool {
	if Editor.level == nil {
		return false
	}
	if !inPlaySession() && (saveStateFresh || sdl.GetTicks()-editorInputAt >= 300) {
		levelSaved.refresh(editorInputGen, 750, levelFingerprint)
	}
	return levelSaved.dirty()
}

// ---------- the document editors ----------

func modelKey() any { return docKey{modelDoc, modelDoc.Rev()} }

func modelMarkSaved() {
	if modelDoc != nil {
		modelSaved.baseline(modelKey(), model.Fingerprint(modelDoc))
	}
}

func modelEditorDirty() bool {
	if modelDoc == nil {
		return false
	}
	modelSaved.refresh(modelKey(), 400, func() uint64 { return model.Fingerprint(modelDoc) })
	return modelSaved.dirty()
}

func particleKey() any { return docKey{particleDoc, uint64(particleDoc.Rev())} }

func particleMarkSaved() {
	if particleDoc != nil {
		particleSaved.baseline(particleKey(), particle.Fingerprint(particleDoc))
	}
}

func particleEditorDirty() bool {
	if particleDoc == nil {
		return false
	}
	particleSaved.refresh(particleKey(), 200, func() uint64 { return particle.Fingerprint(particleDoc) })
	return particleSaved.dirty()
}

func aiKey() any { return docKey{aiDoc, aiDoc.Rev} }

func aiMarkSaved() {
	if aiDoc != nil {
		aiSaved.baseline(aiKey(), aitree.Fingerprint(aiDoc))
	}
}

func aiEditorDirty() bool {
	if aiDoc == nil {
		return false
	}
	aiSaved.refresh(aiKey(), 200, func() uint64 { return aitree.Fingerprint(aiDoc) })
	return aiSaved.dirty()
}

// The material document has no edit counter of its own, so — like the level —
// its change signal is input: an edit is always the consequence of a click or
// a key in some window. A .fmat encodes to a few hundred bytes, so the
// fingerprint is cheap enough to take on a short throttle.
func materialMarkSaved() {
	if materialDoc != nil {
		materialSaved.baseline(editorInputGen, material.Fingerprint(materialDoc))
	}
}

func materialEditorDirty() bool {
	if materialDoc == nil {
		return false
	}
	materialSaved.refresh(editorInputGen, 200, func() uint64 { return material.Fingerprint(materialDoc) })
	return materialSaved.dirty()
}

func uiKey() any { return docKey{uiDoc, uiDoc.Rev()} }

func uiMarkSaved() {
	if uiDoc != nil {
		uiSaved.baseline(uiKey(), ui.Fingerprint(uiDoc))
	}
}

func uiEditorDirty() bool {
	if uiDoc == nil {
		return false
	}
	uiSaved.refresh(uiKey(), 200, func() uint64 { return ui.Fingerprint(uiDoc) })
	return uiSaved.dirty()
}

func entityEditorDirty() bool { return entityDef != nil && entityDefDirty }

// scriptEditorDirty is any open tab with unsaved text.
func scriptEditorDirty() bool {
	for _, d := range scriptDocs {
		if d.Dirty() {
			return true
		}
	}
	return false
}

// ---------- save all ----------

// openEditor is one editor Save All can reach: open, and able to say whether
// it has anything to save and to save it.
type openEditor struct {
	name  string
	dirty func() bool
	save  func()
}

// openEditors lists the editors that are up. The level editor counts while the
// editor is on screen and no play session is running over it.
func openEditors() []openEditor {
	var out []openEditor
	if Editor.Active && Editor.level != nil && !inPlaySession() {
		out = append(out, openEditor{"Level", editorLevelDirty, saveLevelForSaveAll})
	}
	if EntityEditorOpen() {
		// From the main GL context: DrainWindowOpens runs Save All there, not in
		// the entity window entitySaveCurrent assumes.
		out = append(out, openEditor{"Entity", entityEditorDirty, func() { entitySaveCurrentFrom(nil) }})
	}
	if ModelEditorOpen() {
		out = append(out, openEditor{"Model", modelEditorDirty, modelDoSave})
	}
	if textureui.TextureEditorOpen() {
		out = append(out, openEditor{"Texture", textureui.TextureEditorDirty, textureui.SaveTextureEditor})
	}
	if ParticleEditorOpen() {
		out = append(out, openEditor{"Particle", particleEditorDirty, particleSaveCurrent})
	}
	if MaterialEditorOpen() {
		out = append(out, openEditor{"Material", materialEditorDirty, materialSaveCurrent})
	}
	if UIEditorOpen() {
		out = append(out, openEditor{"UI", uiEditorDirty, func() { uiRequestSave(false) }})
	}
	if AIEditorOpen() {
		out = append(out, openEditor{"AI", aiEditorDirty, aiSaveCurrent})
	}
	if TrackerEditorOpen() {
		out = append(out, openEditor{"Tracker", trackerEditorDirty, trkSaveCurrent})
	}
	if ScoreEditorOpen() {
		out = append(out, openEditor{"Score", scoreEditorDirty, scoreSaveCurrent})
	}
	if ScriptEditorOpen() {
		out = append(out, openEditor{"Script", scriptEditorDirty, scriptSaveAllTabs})
	}
	return out
}

func saveLevelForSaveAll() {
	if err := editorSaveLevel(Editor.level, Editor.SavePath); err != nil {
		editorSetStatus("Save failed: %v", err)
	}
}

// scriptSaveAllTabs writes every tab with unsaved text. A tab that has never
// been saved has no name to write under; the active one gets the Save As
// dialog, as its own Save would, and the rest are left for the author.
func scriptSaveAllTabs() {
	saved := 0
	for _, d := range scriptDocs {
		if !d.Dirty() {
			continue
		}
		if d.Path == "" {
			if d == activeScript {
				scriptEditorShowSaveAs = true
			}
			continue
		}
		if err := d.Save(""); err != nil {
			scriptEditorStatus("Save failed: %v", err)
			continue
		}
		saved++
	}
	if saved > 0 {
		scriptEditorStatus("Saved %d script(s)", saved)
		scriptEditorAfterSave(nil)
	}
}

// unsavedEditorNames names the open editors with something to save.
func unsavedEditorNames() []string {
	var names []string
	for _, e := range openEditors() {
		if e.dirty() {
			names = append(names, e.name)
		}
	}
	return names
}

// EditorSaveAll saves every open editor that has unsaved changes. A document
// that has never been written opens its editor's Save As dialog, the same as
// that editor's own Save.
//
// Run through uiwin.RequestWindowOpen, never straight from a button: the
// button is inside some window's Draw, and saves upload into — and restore —
// whichever GL context they think is theirs.
func EditorSaveAll() {
	saveStateFresh = true
	defer func() { saveStateFresh = false }()
	var saved, pending []string
	for _, e := range openEditors() {
		if !e.dirty() {
			continue
		}
		e.save()
		if e.dirty() {
			pending = append(pending, e.name)
		} else {
			saved = append(saved, e.name)
		}
	}
	switch {
	case len(saved) == 0 && len(pending) == 0:
		editorSetStatus("Nothing to save")
	case len(pending) == 0:
		editorSetStatus("Saved %s", strings.Join(saved, ", "))
	default:
		msg := fmt.Sprintf("Not saved: %s — see that editor", strings.Join(pending, ", "))
		if len(saved) > 0 {
			msg = fmt.Sprintf("Saved %s. %s", strings.Join(saved, ", "), msg)
		}
		editorSetStatus("%s", msg)
	}
}

// ---------- widgets ----------

var (
	saveChipUnsaved = imgui.Vec4{X: 0.72, Y: 0.16, Z: 0.16, W: 1}
	saveChipSaved   = imgui.Vec4{X: 0.18, Y: 0.55, Z: 0.26, W: 1}
)

// editorSaveChip draws the rounded "Saved" / "Unsaved Changes" badge. A button
// with every state painted the same, so it reads as a label and lines up with
// the toolbar buttons around it.
func editorSaveChip(dirty bool) {
	label, col := "Saved", saveChipSaved
	if dirty {
		label, col = "Unsaved Changes", saveChipUnsaved
	}
	imgui.PushStyleColor(imgui.StyleColorButton, col)
	imgui.PushStyleColor(imgui.StyleColorButtonHovered, col)
	imgui.PushStyleColor(imgui.StyleColorButtonActive, col)
	imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1})
	imgui.PushStyleVarFloat(imgui.StyleVarFrameRounding, 9)
	imgui.Button(label + "##savechip")
	imgui.PopStyleVar()
	imgui.PopStyleColorV(4)
}

// editorSaveAllButton draws Save All, disabled while no open editor has
// anything to save, with the ones that do in its tooltip.
func editorSaveAllButton() {
	names := unsavedEditorNames()
	imguibackend.EditorPushDisabled(len(names) == 0)
	if imgui.Button("Save All##saveall") {
		uiwin.RequestWindowOpen(EditorSaveAll)
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHoveredV(imgui.HoveredFlagsAllowWhenDisabled) {
		if len(names) == 0 {
			imgui.SetTooltip("Every open editor is saved.")
		} else {
			imgui.SetTooltip("Save every open editor with unsaved changes:\n" + strings.Join(names, ", "))
		}
	}
}
