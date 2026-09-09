package editor

// editor_entity_editor_input.go — clicking on and drawing the type's geometry.
//
// The tools are the level editor's (editor_workspace.go). What this file is,
// is the wiring that hands them a click and puts their selection on screen —
// the same wiring editor_3d.go carries for the main viewport, with everything
// that only a level has taken out.
//
// # What is taken out, and why that is most of it
//
// The main viewport's click handler resolves a ray against the spawn marker,
// entities, volumes, sprites, props, emitters, sounds and lights before it ever
// reaches a brush face. None of those exist in a type: the scratch level holds
// one chunk and nothing else. So the geometry half of that dispatch — object
// mode, then point/edge/face by selection mode — is all there is here, and it
// is written out rather than shared because the shared version's tail is a
// switch over nine kinds that would all be dead.
//
// # The one rule that has to hold
//
// Every entry point installs this window's workspace first and restores it
// after (`defer entityUseWorkspace()()`). Miss it on one path and that path
// edits the LEVEL's geometry through the entity editor's camera, which would
// look like the main window losing brushes at random. The two entry points are
// the window's Draw and its event handler; everything below is reached from one
// of those.

import (
	"math"

	"foundry/internal/gizmo"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// ---------- clicking ----------

// entityGeometryClick is a left click in the viewport, dispatched exactly as
// the 3D view dispatches one: the armed stamp first, then object mode, then the
// selection unit.
//
// Reports whether it did anything, so the caller can leave the click to the
// gizmo when it did not.
func entityGeometryClick(mx, my int32, ctrlHeld bool) bool {
	if entityDef == nil {
		return false
	}
	// The stamp owns the click while it is armed, and stays armed afterwards —
	// blocking out a hold is a dozen boxes in a row. Escape puts it away. The
	// shared handler, so what lands is the ghost that was being previewed.
	if editorBrushPlaceHandleClick() {
		entityBrushSelectionSync()
		entityMarkDirty()
		return true
	}
	// Ctrl+Shift enters DETAIL mode on the brush under the cursor, exactly as
	// it does in the 3D view: a type's hold is blocked out of the same solids a
	// room is, and its walls want the same ledges and reveals.
	if ctrlHeld && sdl.GetModState()&sdl.KMOD_SHIFT != 0 {
		editor3DDetailClick(mx, my)
		entityBrushSelectionSync()
		return true
	}
	// Edit mode and detail mode both address the parts of a brush; only object
	// mode addresses whole ones. See editorInMeshContext on why asking
	// editorInEditMode here is a bug rather than a shorthand.
	if !editorInMeshContext() {
		return entityObjectClick(mx, my, ctrlHeld)
	}

	switch entityWS.SelMode {
	case editorSelPoint:
		if !handleVertexClick(mx, my, ctrlHeld) && !ctrlHeld {
			entityClearGeometrySelection()
		}
		return true
	case editorSelEdge:
		if !handleEdgeClick(mx, my, ctrlHeld) && !ctrlHeld {
			entityClearGeometrySelection()
		}
		return true
	}

	// Face mode.
	hit := pick3D(mx, my)
	if f, ok := faceSelFromHit(hit); ok {
		editorSelectFace(f, ctrlHeld)
		entityBrushSelectionSync()
		return true
	}
	// A ctrl+click that reached no face keeps the set as it is: aiming past the
	// last wall of a dozen and missing must not undo the other eleven.
	if !ctrlHeld {
		entityClearGeometrySelection()
	}
	return true
}

// entityObjectClick picks up a whole brush, which is what object mode selects.
func entityObjectClick(mx, my int32, ctrlHeld bool) bool {
	hit := pick3D(mx, my)
	if hit.kind == pickBrushFace {
		s := brushSel{chunkIdx: hit.chunkIdx, brushIdx: hit.brushIdx}
		if brushSelValid(s) {
			editorSelectBrushObject(s, ctrlHeld)
			entityBrushSelectionSync()
			return true
		}
	}
	if ctrlHeld {
		return true
	}
	entityClearGeometrySelection()
	entitySel = entityEditorSelection{}
	return true
}

// ---------- the stamp, armed ----------

// entityArmBrushStamp arms the stamp on one primitive, or disarms it when that
// primitive is already armed — the outline's buttons toggle.
//
// The arming, the ghost and the placing are all the level editor's
// (editor_brush_tools.go); the only thing here is that a type with no geometry
// yet has no chunk for a brush to live in, and seeding one is what turns "add a
// box" from a dead button into the first box.
func entityArmBrushStamp(kind string) {
	if entityWS.BrushPlaceActive && entityWS.BrushPlaceKind == kind {
		editorDisarmBrushPlacement()
		entityStatus = "Brush stamp off"
		return
	}
	if entityGeomChunk() == nil {
		entitySeedGeometry()
	}
	editorArmBrushPlacement(kind)
}

// ---------- keys ----------

// entityGeometryHandleKey runs the geometry shortcuts, and reports whether it
// consumed the press.
//
// Tried BEFORE the window's own shortcuts (entityShortcutFor) so that the keys
// mean here what they mean in the 3D view: B is subdivide when a face is
// selected, and only falls through to the bounds toggle when it is not. That
// ordering is the whole of what makes the two viewports feel like one tool.
func entityGeometryHandleKey(sc sdl.Scancode, ctrl, shift bool) bool {
	if entityDef == nil || ctrl {
		return false
	}
	// Detail mode's tools first and the hull's after, in the same order the 3D
	// view asks them (editor.go) — the same functions, acting on this
	// workspace's selection.
	if detailMeshHandleKey(sc, shift) || brushMeshHandleKey(sc, shift) {
		entityGeometryEdited()
		entityMarkDirty()
		return true
	}
	switch sc {
	case sdl.SCANCODE_TAB:
		editorToggleEditContext()
		entityBrushSelectionSync()
		entityStatus = "Edit context: " + entityWS.EditContext.String()
		return true
	case sdl.SCANCODE_1:
		editorEnterEditUnit(editorSelPoint)
		return true
	case sdl.SCANCODE_2:
		editorEnterEditUnit(editorSelEdge)
		return true
	case sdl.SCANCODE_3:
		editorEnterEditUnit(editorSelFace)
		return true
	case sdl.SCANCODE_Z:
		// Bare Z; Ctrl+Z is undo and was excluded above.
		entityWS.Wireframe = !entityWS.Wireframe
		entityWireOverlay = entityWS.Wireframe
		return true
	case sdl.SCANCODE_ESCAPE:
		// Behind the stamp below, which wants Escape more: putting an armed
		// tool away is more urgent than leaving the mode you are stamping in.
		if !entityWS.BrushPlaceActive && editorInDetailMode() {
			editorExitDetailMode()
			entityBrushSelectionSync()
			entityStatus = "Left detail mode"
			return true
		}
	}
	// Escape puts an armed stamp away, and is otherwise the window's own.
	if editorBrushPlaceHandleKey(sc) {
		return true
	}
	return false
}

// ---------- drawing the selection ----------

// drawEntityGeometryOverlays paints what is selected and the handles that can
// be grabbed — the entity editor's counterpart of
// editorRenderInteractiveOverlays, with the level-only markers (prefab boxes,
// emitters, sounds, sprite and volume outlines) left out because a type has
// none of them.
//
// Depth testing off throughout, for the reason the main viewport's does it: a
// corner behind the hull is still a corner you have to be able to see and grab,
// and picking has no depth test to agree with.
func drawEntityGeometryOverlays(projView mgl32.Mat4) {
	if entityDef == nil {
		return
	}
	gizmo.GizmoFlush(projView)

	// The detail mesh's seams, and the one thing here drawn WITH depth testing:
	// they are edges on a surface, not marks to be seen through it. Everything
	// below is deliberately depth-free.
	gl.Enable(gl.DEPTH_TEST)
	drawDetailMeshWires()
	gizmo.GizmoFlush(projView)

	gl.Disable(gl.DEPTH_TEST)
	defer gizmo.GizmoFlush(projView)

	drawSelectedFaceOutline()
	drawSelectedBrushOutline()
	drawDetailHullCage()
	DrawBrushPlaceGhost()

	// The handles. Over the panel there is nothing under the cursor, so nothing
	// reads as hovered — but a view that draws the whole cage still wants every
	// handle drawn, not least while the author is over the panel holding the
	// switch that turns it on.
	if imgui.CurrentIO().WantCaptureMouse() {
		if editorShowsAllHandles() {
			drawSelModeHandles(math.MinInt32/2, math.MinInt32/2, vertexHandleShowAllPx)
		}
		return
	}
	drawSelModeHandles(entityMouseX, entityMouseY, editorHandleShowPx())
}
