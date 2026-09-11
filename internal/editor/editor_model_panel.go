package editor

// editor_model_panel.go — the model editor's menu bar, dialogs and properties
// panel.
//
// Layout follows the texture editor (texture_editor_ui.go): a full-window ImGui
// root owns the chrome, and the 3D viewport shows through underneath because
// the mesh is drawn into the framebuffer before any of this runs.

import (
	"fmt"
	"foundry/internal/imguibackend"
	"foundry/internal/model"
	"math"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

const modelPanelWidth = float32(280)

// Dialog visibility and the text buffers they edit.
var (
	modelShowOpenDialog     bool
	modelShowSaveAsDialog   bool
	modelShowImportDialog   bool
	modelShowExportDialog   bool
	modelShowMergeDialog    bool
	modelShowMergeOBJDialog bool

	modelSaveAsName string
	modelExportName string

	modelFileList []string // cached .fmdl listing; nil forces a rescan
	modelOBJList  []string

	modelStatus string
)

// modelSetStatus records the outcome of the last file action for the panel.
func modelSetStatus(format string, args ...any) {
	modelStatus = fmt.Sprintf(format, args...)
}

// ---------- menu bar ----------

func modelEditorMenuBar() {
	if imgui.Button("New") {
		modelNewDocument()
	}
	imgui.SameLine()
	if imgui.Button("Open...") {
		modelFileList = nil
		modelShowOpenDialog = true
	}
	imgui.SameLine()

	imguibackend.EditorPushDisabled(modelDoc == nil)
	if imgui.Button("Save") {
		modelDoSave()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	imguibackend.EditorPushDisabled(modelDoc == nil)
	if imgui.Button("Save As...") {
		if modelDoc != nil {
			modelSaveAsName = modelDoc.ID
		}
		modelShowSaveAsDialog = true
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	if imgui.Button("Import OBJ...") {
		modelOBJList = nil
		modelShowImportDialog = true
	}
	imgui.SameLine()

	imguibackend.EditorPushDisabled(modelDoc == nil)
	if imgui.Button("Export OBJ...") {
		if modelDoc != nil {
			modelExportName = modelDoc.ID
		}
		modelShowExportDialog = true
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	imguibackend.EditorPushDisabled(!ModelCanUndo())
	if imgui.Button("Undo") {
		ModelUndo()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	imguibackend.EditorPushDisabled(!ModelCanRedo())
	if imgui.Button("Redo") {
		ModelRedo()
	}
	imguibackend.EditorPopDisabled()
}

// ---------- file actions ----------

func modelNewDocument() {
	modelDoc = model.NewCubeDoc()
	modelCam.frame(modelDoc)
	modelClearSelection()
	ModelClearUndoHistory()
	modelUVInvalidate()
	modelSetStatus("New model")
}

func modelDoSave() {
	if modelDoc == nil {
		return
	}
	if modelDoc.Path == "" {
		// Never saved: fall through to Save As rather than inventing a filename.
		modelSaveAsName = modelDoc.ID
		modelShowSaveAsDialog = true
		return
	}
	if err := model.SaveModel(modelDoc, modelDoc.Path); err != nil {
		modelSetStatus("Save failed: %v", err)
		return
	}
	modelSetStatus("Saved %s", filepath.Base(modelDoc.Path))
}

func modelDialogs() {
	imguibackend.WithModalPopup("Open Model##modelopen", &modelShowOpenDialog, func() {
		if modelFileList == nil {
			modelFileList = model.ScanModelFiles()
		}
		if len(modelFileList) == 0 {
			imgui.Text(fmt.Sprintf("No %s files in %s", model.ModelExt, model.ModelsDir()))
		}
		for _, name := range modelFileList {
			if !imgui.Selectable(name) {
				continue
			}
			doc, err := model.LoadModel(filepath.Join(model.ModelsDir(), name))
			if err != nil {
				modelSetStatus("Open failed: %v", err)
			} else {
				modelAdoptDocument(doc)
				modelSetStatus("Opened %s", name)
			}
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##modelopencancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Save Model As##modelsaveas", &modelShowSaveAsDialog, func() {
		imgui.Text(fmt.Sprintf("Saved to %s", model.ModelsDir()))
		imgui.InputText("Name##modelsaveasname", &modelSaveAsName)
		imgui.Separator()
		if imgui.Button("Save##modelsaveasok") && modelSaveAsName != "" && modelDoc != nil {
			path := model.ModelPath(modelSaveAsName)
			if err := model.SaveModel(modelDoc, path); err != nil {
				modelSetStatus("Save failed: %v", err)
			} else {
				modelDoc.ID = modelSaveAsName
				modelDoc.Path = path
				modelFileList = nil
				modelSetStatus("Saved %s", filepath.Base(path))
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##modelsaveascancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Import OBJ##modelimport", &modelShowImportDialog, func() {
		if modelOBJList == nil {
			modelOBJList = model.ScanOBJFiles()
		}
		if len(modelOBJList) == 0 {
			imgui.Text(fmt.Sprintf("No .obj files in %s", model.ModelsDir()))
		}
		for _, name := range modelOBJList {
			if !imgui.Selectable(name) {
				continue
			}
			doc, err := model.LoadOBJMesh(filepath.Join(model.ModelsDir(), name))
			if err != nil {
				modelSetStatus("Import failed: %v", err)
			} else {
				modelAdoptDocument(doc)
				modelSetStatus("Imported %s (%d faces)", name, len(doc.Faces))
			}
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##modelimportcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Merge Model##modelmerge", &modelShowMergeDialog, func() {
		if modelFileList == nil {
			modelFileList = model.ScanModelFiles()
		}
		imgui.Text("Appends to the current model.")
		for _, name := range modelFileList {
			if !imgui.Selectable(name) {
				continue
			}
			other, err := model.LoadModel(filepath.Join(model.ModelsDir(), name))
			if err != nil {
				modelSetStatus("Merge failed: %v", err)
			} else {
				ModelPushUndo()
				modelAdoptAddition(modelDoc.Merge(other, modelAddPosition()))
				modelSetStatus("Merged %s (%d faces)", name, len(other.Faces))
			}
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##modelmergecancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Merge OBJ##modelmergeobj", &modelShowMergeOBJDialog, func() {
		if modelOBJList == nil {
			modelOBJList = model.ScanOBJFiles()
		}
		imgui.Text("Appends to the current model.")
		for _, name := range modelOBJList {
			if !imgui.Selectable(name) {
				continue
			}
			other, err := model.LoadOBJMesh(filepath.Join(model.ModelsDir(), name))
			if err != nil {
				modelSetStatus("Merge failed: %v", err)
			} else {
				ModelPushUndo()
				modelAdoptAddition(modelDoc.Merge(other, modelAddPosition()))
				modelSetStatus("Merged %s (%d faces)", name, len(other.Faces))
			}
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##modelmergeobjcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Export OBJ##modelexport", &modelShowExportDialog, func() {
		imgui.Text(fmt.Sprintf("Written to %s, with a sibling .mtl", model.ModelsDir()))
		imgui.InputText("Name##modelexportname", &modelExportName)
		imgui.Separator()
		if imgui.Button("Export##modelexportok") && modelExportName != "" && modelDoc != nil {
			path := filepath.Join(model.ModelsDir(), modelExportName+model.ObjExt)
			if err := model.SaveOBJ(modelDoc, path); err != nil {
				modelSetStatus("Export failed: %v", err)
			} else {
				modelOBJList = nil
				modelSetStatus("Exported %s", filepath.Base(path))
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##modelexportcancel") {
			imgui.CloseCurrentPopup()
		}
	})
}

// modelAdoptDocument swaps in a freshly loaded or imported document. The undo
// history goes with the old one: its snapshots describe a mesh that is no
// longer open, so restoring one would silently replace the new model.
func modelAdoptDocument(doc *model.ModelDoc) {
	modelDoc = doc
	modelClearSelection()
	ModelClearUndoHistory()
	modelUVInvalidate()
	modelCam.frame(modelDoc)
}

// ---------- properties panel ----------

func modelEditorPanel() {
	w := modelEditorWindow
	if w == nil || modelDoc == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(w.W) - modelPanelWidth, Y: modelToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: modelPanelWidth, Y: float32(w.H) - modelToolbarH})
	imgui.BeginV("Model##modelpanel", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	imgui.Text(fmt.Sprintf("Mode: %s select", modelSelection.Mode))
	imgui.Text(fmt.Sprintf("Selected: %s", modelSelectionSummary()))
	if modelStatus != "" {
		imgui.Separator()
		imgui.Text(modelStatus)
	}

	imgui.Separator()
	modelAddSection()
	imgui.Separator()
	// The texture is a whole-document property, so it leads rather than sitting
	// inside the face section it used to live in.
	modelTextureSection()
	imgui.Separator()
	modelFaceSection()
	imgui.Separator()
	modelBoneSection()
	imgui.Separator()
	// Animation follows the bones it drives — a clip is meaningless without an
	// armature, and the two are edited together.
	modelAnimSection()
	imgui.Separator()
	modelUVSection()
	imgui.Separator()
	modelMeshSection()
	imgui.Separator()
	modelTransformSection()

	// Modals are drawn inside the panel window, after its sections, so a popup
	// opened by one of them is queued in the same frame it was asked for.
	drawModelUnwrapDialog()

	imgui.End()
}

func modelSelectionSummary() string {
	switch modelSelection.Mode {
	case modelSelVertex:
		return fmt.Sprintf("%d vertices", len(modelSelection.Verts))
	case modelSelEdge:
		return fmt.Sprintf("%d edges", len(modelSelection.Edges))
	case modelSelBone:
		return fmt.Sprintf("%d bones (%s)", len(modelSelection.Bones), modelSelection.BoneJoint)
	default:
		return fmt.Sprintf("%d faces", len(modelSelection.Faces))
	}
}

// modelMeshSection holds the whole-mesh operations.
func modelMeshSection() {
	if !imgui.CollapsingHeader("Mesh##modelmesh") {
		return
	}
	imgui.Text(fmt.Sprintf("%d verts  %d faces  %d edges",
		len(modelDoc.Verts), len(modelDoc.Faces), len(modelDoc.Edges())))

	if imgui.Button("Smooth Subdivide##modelcc") {
		ModelPushUndo()
		modelDoc.CatmullClark()
		// Catmull-Clark renumbers everything, so no prior selection survives it.
		modelClearSelection()
		modelSetStatus("Subdivided: %d faces", len(modelDoc.Faces))
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Catmull-Clark: one level over the whole mesh.\nThe surface moves — it gets rounder each time.")
	}

	changed := editorSmoothPanel("Model", &modelSmoothAll, &modelDoc.SmoothAngle)
	if changed {
		modelDoc.SetFaceSmooth(modelAllFaceIndices(), modelSmoothAll)
	}
}

// modelSmoothAll drives the whole-mesh smoothing toggle in the panel. Per-face
// smoothing is still authoritative; this just sets every face at once.
var modelSmoothAll bool

func modelAllFaceIndices() []int {
	out := make([]int, len(modelDoc.Faces))
	for i := range out {
		out[i] = i
	}
	return out
}

// modelFaceSection holds the per-face operations, which need a face selection.
func modelFaceSection() {
	// Open by default: extrude is the single most common thing to do in here,
	// and a collapsed header hides it.
	if !imgui.CollapsingHeaderV("Face##modelface", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	faces := modelSelection.Faces
	hasFaces := modelSelection.Mode == modelSelFace && len(faces) > 0
	if !hasFaces {
		imgui.Text("Switch to Face select [3]")
		imgui.Text("and click a face to edit it.")
		return
	}

	smooth := modelSelectedFaceSmooth()
	if imgui.Checkbox("Smooth##modelfacesmooth", &smooth) {
		modelSetSelectedFaceSmooth(smooth)
	}

	imgui.Separator()

	// The button always extrudes — E, pressed on an extrude that is still
	// selected, re-aims it instead, and a button labelled Extrude that quietly
	// did that would be a lie.
	if imgui.Button("Extrude##modelextrude") {
		modelExtrudeAgain()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("E / Shift+E — push the selected faces along their shared normal.\n" +
			"Drag the gizmo afterwards to set the depth.\n" +
			"E again re-aims that drag along the face normal instead of\n" +
			"the world axes; Shift+E always stacks another extrude.")
	}
	imgui.SameLine()
	if imgui.Button("Inset##modelinset") {
		modelInsetSelectedFaces()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("I — a border round each face, in its own plane, mitred so it\n" +
			"is the same width all the way round. What is left in the middle\n" +
			"stays selected: inset then extrude is a recess.")
	}
	imgui.SameLine()
	if imgui.Button("Subdivide##modelsub") {
		modelSubdivideSelectedFaces()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Midpoint split. The surface does not move —\nneighbouring faces absorb the new midpoints.")
	}
	imgui.SameLine()
	if imgui.Button("Join##modeljoin") {
		modelJoinSelectedFaces()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("J — make the selected faces one face, along the outline they share.\n" +
			"They must meet along edges and leave no hole.")
	}
	imgui.SameLine()
	if imgui.Button("Delete##modeldelface") {
		modelDeleteSelectedFaces()
	}

	// The drag direction for the extrude that is still pending. It is the same
	// switch the second press of E throws, surfaced because the keystroke can
	// only reach it while nothing has been dragged yet — this one has no such
	// window, so a run already pulled out along its normal can still be put
	// back on the world axes.
	if modelPendingExtrudeMatches() {
		along := modelExtrudePending.AlongNormal
		if imgui.Checkbox("Drag along face normal##modelextrudenormal", &along) {
			modelToggleExtrudeAlongNormal()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("E, pressed again before the extrude is dragged.\n" +
				"The gizmo shows one arm along the cap's normal.")
		}
	}

	if len(faces) == 1 {
		fi := faces[0]
		n := modelDoc.FaceNormal(fi)
		imgui.Text(fmt.Sprintf("Normal: %.2f %.2f %.2f", n.X(), n.Y(), n.Z()))
		imgui.Text(fmt.Sprintf("Sides: %d", len(modelDoc.Faces[fi].Verts)))
	}
}

// modelTransformSection shows and edits the selection's position.
func modelTransformSection() {
	if !imgui.CollapsingHeader("Transform##modelxform") {
		return
	}
	origin, ok := modelGizmoOrigin()
	if !ok {
		imgui.Text("Nothing selected.")
		return
	}
	imgui.Text(fmt.Sprintf("Centre: %.3f %.3f %.3f", origin.X(), origin.Y(), origin.Z()))

	// Nudges are a keyboard-free alternative to the gizmo, and the only way to
	// move by an exact amount when snapping is off. With snapping on they land
	// on the grid rather than stepping off it, exactly as a drag does.
	step := Editor.StepSize
	if step <= 0 {
		step = 0.1
	}
	imgui.Text(fmt.Sprintf("Nudge by %.3f:", step))
	for i, name := range []string{"X", "Y", "Z"} {
		if i > 0 {
			imgui.SameLine()
		}
		if imgui.Button(fmt.Sprintf("-%s##modelnudge", name)) {
			ModelPushUndo()
			modelNudgeSelection(i, -step)
		}
		imgui.SameLine()
		if imgui.Button(fmt.Sprintf("+%s##modelnudge", name)) {
			ModelPushUndo()
			modelNudgeSelection(i, step)
		}
	}

	// The 3D view's own control, not a generic editorDragFloat over StepSize.
	// That generic field passes the value being edited as its *own* drag
	// increment and then snaps it to itself, so with snapping on a single drag
	// walks the step 0.25 -> 0.5 -> 1 -> 2. At a step of 2 a one-unit model has
	// only two reachable grid lines and a dragged vertex flips between them.
	editorGridSettings()
}

// modelExtrudePending remembers the sides of the last interactive extrude, so
// the drag that sets the depth can keep their UVs in step with it.
//
// It carries the caps as well and is only honoured while they are still exactly
// what is selected. That is what makes it self-expiring: the moment the user
// selects anything else, the pending extrude is inert, and no later drag can
// reach back and rewrite the UVs of faces it has nothing to do with.
//
// Normal and AlongNormal serve the second half of the gesture: E extrudes, and
// pressing E again — while that extrude is still what is selected — aims the
// drag along the cap's own normal instead of the world axes, and again to put
// it back. Normal is recorded when the extrude is made, so the aim survives the
// cap being dragged off the plane it grew from.
var modelExtrudePending struct {
	Caps        []int
	Sides       []int
	Normal      mgl32.Vec3 // the cap's averaged normal at creation
	AlongNormal bool       // drag along Normal rather than the world axes
}

// modelPendingExtrudeMatches reports whether the remembered extrude still
// describes exactly what is selected. Everything the pending extrude is allowed
// to do — rewriting side UVs, re-aiming the gizmo — is gated on this.
func modelPendingExtrudeMatches() bool {
	if modelDoc == nil || len(modelExtrudePending.Caps) == 0 {
		return false
	}
	if modelSelection.Mode != modelSelFace ||
		len(modelSelection.Faces) != len(modelExtrudePending.Caps) {
		return false
	}
	for i, fi := range modelSelection.Faces {
		if fi != modelExtrudePending.Caps[i] {
			return false
		}
	}
	return true
}

// modelPendingExtrudeSides returns the side quads whose UVs should follow the
// current drag, or nil when the pending extrude no longer matches the selection.
func modelPendingExtrudeSides() []int {
	if len(modelExtrudePending.Sides) == 0 || !modelPendingExtrudeMatches() {
		return nil
	}
	return modelExtrudePending.Sides
}

// modelClearPendingExtrude forgets the remembered extrude. Its indices are
// positional, so anything that renumbers or replaces the faces — an undo, a
// different document — makes them describe geometry that is not there any
// more, and a later selection that happens to land on the same indices would
// otherwise wake it up.
func modelClearPendingExtrude() {
	modelExtrudePending.Caps = nil
	modelExtrudePending.Sides = nil
	modelExtrudePending.AlongNormal = false
}

// modelExtrudeNormalAxis is the direction the translate gizmo drags along while
// a pending extrude is aimed at its own normal, or false when the world axes
// are in charge.
func modelExtrudeNormalAxis() (mgl32.Vec3, bool) {
	if !modelExtrudePending.AlongNormal || !modelPendingExtrudeMatches() {
		return mgl32.Vec3{}, false
	}
	n := modelExtrudePending.Normal
	if n.Len() < 1e-6 {
		return mgl32.Vec3{}, false
	}
	return n.Normalize(), true
}

// modelFacesNormal is the averaged normal of a set of faces — the same
// direction ExtrudeFaces pushed them along. False when they have no shared
// direction, which is the case ExtrudeFaces refuses outright.
func modelFacesNormal(faces []int) (mgl32.Vec3, bool) {
	if modelDoc == nil {
		return mgl32.Vec3{}, false
	}
	var n mgl32.Vec3
	for _, fi := range faces {
		if fi >= 0 && fi < len(modelDoc.Faces) {
			n = n.Add(modelDoc.FaceNormal(fi))
		}
	}
	if n.Len() < 1e-6 {
		return mgl32.Vec3{}, false
	}
	return n.Normalize(), true
}

// modelAfterMeshMove runs the bookkeeping every path that moves mesh vertices
// owes: right now, keeping a live extrude's side UVs matched to the depth the
// drag is still choosing.
func modelAfterMeshMove() {
	if sides := modelPendingExtrudeSides(); sides != nil {
		modelDoc.RefreshExtrudeSideUVs(sides)
		// The panel caches the selection's UVs to apply its transform against;
		// those caches now describe UVs that no longer exist.
		modelUVInvalidate()
	}
}

// modelStartExtrude is what the E key does: extrude the selection, or — when
// the extrude E just made is still what is selected — re-aim the drag that
// sets its depth along the cap's own normal, and a further press back to the
// world axes.
//
// The second press re-aims rather than stacking a second extrude because on
// screen a second extrude is nothing: it is created at zero depth, so it adds
// invisible geometry and the arms the user is looking at do not change. Making
// the keystroke mean the thing they can see is worth more than a repeat they
// cannot. modelExtrudeAgain (Shift+E, and the panel's Extrude button) is the
// unconditional one, for stacking segments without touching the selection.
func modelStartExtrude() {
	if modelDoc == nil {
		return
	}
	// E means "grow the thing that is selected" in both modes; which topology
	// that is depends only on what the user is looking at.
	if modelSelection.Mode == modelSelBone {
		modelExtrudeSelectedBones()
		return
	}
	if modelPendingExtrudeMatches() {
		modelToggleExtrudeAlongNormal()
		return
	}
	modelExtrudeAgain()
}

// modelExtrudeAgain always extrudes, even onto a cap that was just extruded.
// This is the path for stacking one segment on the next.
func modelExtrudeAgain() {
	if modelDoc == nil {
		return
	}
	if modelSelection.Mode == modelSelBone {
		modelExtrudeSelectedBones()
		return
	}
	if modelSelection.Mode != modelSelFace || len(modelSelection.Faces) == 0 {
		// Silence here is what "E does nothing" feels like from the outside:
		// the usual cause is being in vertex or edge select mode, which the
		// viewport does not otherwise announce.
		modelSetStatus("Nothing to extrude — switch to face select [3] and click a face")
		return
	}
	// Read before the extrude replaces the pending state; only a run being
	// continued gets to hand its aim on, not a stale one from another face.
	alongNormal := modelExtrudePending.AlongNormal && modelPendingExtrudeMatches()
	ModelPushUndo()
	caps, sides := modelDoc.ExtrudeFaces(modelSelection.Faces, 0)
	if caps == nil {
		// Refused: the selection has no single normal. Drop the undo entry we
		// just pushed, since nothing changed.
		modelUndoStack.pop()
		modelSetStatus("Cannot extrude: the selected faces have no shared direction")
		return
	}
	modelSelection.Faces = caps
	modelExtrudePending.Caps = append([]int(nil), caps...)
	modelExtrudePending.Sides = sides
	// The cap is coplanar with the face it grew from, so its normal is the
	// direction ExtrudeFaces pushed along — recorded now so a later press has
	// it even after the cap has been dragged off that plane.
	modelExtrudePending.Normal, _ = modelFacesNormal(caps)
	// alongNormal carries over from the extrude this one continues: having said
	// "along the normal" once, pulling a second segment out of the same run
	// means the *new* cap's normal, not a return to the world axes.
	modelExtrudePending.AlongNormal = alongNormal
	modelUVInvalidate()
	if alongNormal {
		modelSetStatus("Extruded %d faces along the face normal — drag the arm to set the depth", len(caps))
	} else {
		modelSetStatus("Extruded %d faces — drag an axis to set the depth (E again for the face normal)", len(caps))
	}
}

// modelToggleExtrudeAlongNormal flips the pending extrude between the world
// axes and its own normal. Nothing is moved: it only decides what the next drag
// means, so it is not an undo step.
func modelToggleExtrudeAlongNormal() {
	if modelExtrudePending.AlongNormal {
		modelExtrudePending.AlongNormal = false
		modelSetStatus("Extruding along the world axes — drag an arm to set the depth")
		return
	}
	if modelExtrudePending.Normal.Len() < 1e-6 {
		// Older pending state, or a cap that lost its direction: recover it
		// from the faces themselves rather than refusing the toggle.
		if n, ok := modelFacesNormal(modelExtrudePending.Caps); ok {
			modelExtrudePending.Normal = n
		} else {
			modelSetStatus("Cannot aim along the normal: the cap has no single direction")
			return
		}
	}
	modelExtrudePending.AlongNormal = true
	// The normal arm belongs to the translate gizmo, so make sure that is the
	// one on screen — the toggle exists to set up the drag that follows it.
	modelSetGizmoTool(modelToolMove)
	n := modelExtrudePending.Normal
	modelSetStatus("Extruding along the face normal %.2f %.2f %.2f — drag the arm to set the depth",
		n.X(), n.Y(), n.Z())
}

// ---------- add geometry ----------

// modelAddSection is the primitive palette plus merge-from-file. Both append to
// the current model rather than replacing it, which is the difference between
// this and File > Open.
func modelAddSection() {
	if !imgui.CollapsingHeaderV("Add##modeladd", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	imgui.Text("Adds to the current model:")
	for i, kind := range model.ModelPrimitiveKinds {
		if i > 0 && i%3 != 0 {
			imgui.SameLine()
		}
		if imgui.Button(fmt.Sprintf("%s##modelprim%d", kind, i)) {
			modelAddPrimitive(kind)
		}
	}

	segs := int32(model.ModelPrimitiveSegments)
	if imgui.SliderInt("Segments##modelprimseg", &segs, 3, 64) {
		model.ModelPrimitiveSegments = int(segs)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Radial resolution for cylinder, cone and sphere.")
	}

	imgui.Separator()
	if imgui.Button("Merge Model...##modelmerge") {
		modelFileList = nil
		modelShowMergeDialog = true
	}
	imgui.SameLine()
	if imgui.Button("Merge OBJ...##modelmergeobj") {
		modelOBJList = nil
		modelShowMergeOBJDialog = true
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Append another file's geometry to this model,\nkeeping what is already here.")
	}
}

// modelAddPrimitive drops a new primitive into the document, in front of the
// camera so it lands where the user is looking rather than at the origin under
// whatever is already there.
func modelAddPrimitive(kind model.ModelPrimitiveKind) {
	if modelDoc == nil {
		return
	}
	prim := model.NewPrimitiveDoc(kind, model.ModelPrimitiveSegments)
	ModelPushUndo()
	added := modelDoc.Merge(prim, modelAddPosition())
	modelAdoptAddition(added)
	modelSetStatus("Added %s (%d faces)", kind, len(added))
}

// modelAddPosition is where new geometry lands: the orbit pivot, or a short way
// in front of the eye when flying. Snapped to the grid when snapping is on, so
// pieces line up rather than landing at arbitrary fractions.
func modelAddPosition() mgl32.Vec3 {
	p := modelCam.Target
	if modelCam.Fly {
		p = modelCam.Pos.Add(modelCam.forward().Mul(modelCam.Dist))
	}
	if Editor.SnapToGrid {
		p = mgl32.Vec3{
			snapValue(p.X(), Editor.StepSize),
			snapValue(p.Y(), Editor.StepSize),
			snapValue(p.Z(), Editor.StepSize),
		}
	}
	return p
}

// modelAdoptAddition leaves the newly added faces selected in face mode, so the
// next thing the user does acts on what they just added.
func modelAdoptAddition(added []int) {
	if len(added) == 0 {
		return
	}
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = added
	modelSelection.Verts = nil
	modelSelection.Edges = nil
	modelUVInvalidate()
}

// ---------- UV editing ----------

// modelUVSection edits the selected faces' texture mapping.
func modelUVSection() {
	if !imgui.CollapsingHeader("Texture UV##modeluv") {
		return
	}
	faces := modelSelection.Faces
	if modelSelection.Mode != modelSelFace || len(faces) == 0 {
		imgui.Text("Select faces to edit their mapping.")
		return
	}

	// Capture the base UVs for this selection; the controls below are a
	// transform re-applied to that base, not a compounding edit.
	modelUVSync(faces)

	imgui.Text(fmt.Sprintf("Mapping %d face(s)", len(faces)))

	x := &modelUVEdit.Xform
	changed := false
	off := [2]float32{x.Offset.X(), x.Offset.Y()}
	if editorDragFloat("Offset U##modeluvou", &off[0], 0, 0, "%.3f") ||
		editorDragFloat("Offset V##modeluvov", &off[1], 0, 0, "%.3f") {
		x.Offset = mgl32.Vec2{off[0], off[1]}
		changed = true
	}
	sc := x.Scale
	if sc == (mgl32.Vec2{}) {
		sc = mgl32.Vec2{1, 1}
	}
	scArr := [2]float32{sc.X(), sc.Y()}
	if editorDragFloat("Scale U##modeluvsu", &scArr[0], 0.01, 100, "%.3f") ||
		editorDragFloat("Scale V##modeluvsv", &scArr[1], 0.01, 100, "%.3f") {
		x.Scale = mgl32.Vec2{scArr[0], scArr[1]}
		changed = true
	}
	rotDeg := x.Rotation * 180 / math.Pi
	if editorDragFloat("Rotation (deg)##modeluvrot", &rotDeg, -360, 360, "%.1f") {
		x.Rotation = rotDeg * math.Pi / 180
		changed = true
	}
	if changed {
		modelUVApply(faces)
	}

	imguibackend.EditorPushDisabled(x.IsIdentity())
	if imgui.Button("Reset transform##modeluvreset") {
		modelUVEdit.Xform = identityUVTransform()
		modelUVApply(faces)
	}
	imguibackend.EditorPopDisabled()

	imgui.Separator()

	if imgui.Button("Unwrap##modeluvunwrap") {
		modelUnwrapSelectedFaces(false)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Project each face onto its own plane, keeping world\nscale so texel density matches across faces.")
	}
	imgui.SameLine()
	if imgui.Button("Unwrap to 0..1##modeluvfit") {
		modelUnwrapSelectedFaces(true)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Project each face onto its own plane and stretch it\nto fill the texture exactly once.")
	}

	if imgui.Button("Flip U##modeluvflipu") {
		modelFlipSelectedFaceUV(true, false)
	}
	imgui.SameLine()
	if imgui.Button("Flip V##modeluvflipv") {
		modelFlipSelectedFaceUV(false, true)
	}

	// Per-corner numbers, for the cases the transform cannot express.
	if len(faces) == 1 {
		fi := faces[0]
		if imgui.TreeNodeV("Corners##modeluvcorners", imgui.TreeNodeFlagsNone) {
			f := &modelDoc.Faces[fi]
			for i := range f.UVs {
				uv := [2]float32{f.UVs[i].X(), f.UVs[i].Y()}
				if editorDragFloat(fmt.Sprintf("U %d##modeluvcu%d", i, i), &uv[0], 0, 0, "%.3f") ||
					editorDragFloat(fmt.Sprintf("V %d##modeluvcv%d", i, i), &uv[1], 0, 0, "%.3f") {
					ModelPushUndo()
					f.UVs[i] = mgl32.Vec2{uv[0], uv[1]}
					modelDoc.Touch()
					modelUVInvalidate()
				}
			}
			imgui.TreePop()
		}
	}
}
