package main

// editor_volume.go — placing, selecting and editing volumes.
//
// This is deliberately the entity workflow with the entity-specific parts taken
// out. A volume has no definition to instantiate, no models to reload and no
// collision flags to set, so placement is "make one here" rather than "look up
// a type and copy it", and the property panel is a transform, a shape, and the
// tags/attributes/script every scriptable object shares.
//
// What it does keep is the selection model: an index rather than a pointer,
// because undo swaps the whole level out from under the editor and a pointer
// into the old one would outlive it.

import (
	"foundry/internal/gfx"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// ---------- selection ----------

// activeVolume returns the selected volume, or nil.
func activeVolume() *Volume {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveVolumeIdx < 0 || Editor.ActiveVolumeIdx >= len(lvl.Volumes) {
		return nil
	}
	return &lvl.Volumes[Editor.ActiveVolumeIdx]
}

// editorSelectVolume makes the volume at idx the selection, giving it the gizmo
// and opening its properties section.
func editorSelectVolume(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Volumes) {
		return
	}
	Editor.ActiveVolumeIdx = idx
	Editor.GizmoKind = gizmoKindVolume
	Editor.PanelFocus = panelFocusVolume
}

// editorClearVolumeSelection drops the volume from the selection, taking the
// gizmo with it so it cannot keep dragging something no longer selected.
func editorClearVolumeSelection() {
	Editor.ActiveVolumeIdx = -1
	if Editor.GizmoKind == gizmoKindVolume {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoVolume returns the volume the gizmo is attached to, or nil. The
// gizmo kind is checked as well as the index, so a stale selection left by
// another tool cannot silently be dragged.
func activeGizmoVolume() *Volume {
	if Editor.GizmoKind != gizmoKindVolume {
		return nil
	}
	return activeVolume()
}

// ---------- placement ----------

// editorArmVolumePlacement makes the next left-click in the viewport drop a
// volume.
func editorArmVolumePlacement() {
	Editor.VolumePlacementMode = true
	Editor.EntityPlacementMode = false
	Editor.SpawnPlacementMode = false
}

// placeVolumeAt creates a volume at pos, selects it, and leaves placement mode.
// Reports whether one was placed.
func placeVolumeAt(pos mgl32.Vec3) bool {
	Editor.VolumePlacementMode = false
	lvl := Editor.level
	if lvl == nil {
		return false
	}

	EditorPushUndo()
	base := Editor.NewVolumeID
	if base == "" {
		base = "volume"
	}
	v := NewVolume(uniqueVolumeID(lvl, base), pos)
	if err := v.Load(); err != nil {
		// The cage is an authoring aid; a volume without one still triggers.
		editorSetStatus("Volume %s: %v", v.ID, err)
	}
	lvl.Volumes = append(lvl.Volumes, v)
	editorSelectVolume(len(lvl.Volumes) - 1)
	editorSetStatus("Placed volume %q", v.ID)
	return true
}

// ---------- duplicate / delete ----------

// duplicateActiveVolume copies the selected volume beside itself and selects the
// copy. Offset rather than coincident, for the same reason a duplicated entity
// is: two regions in exactly the same place cannot be told apart or clicked
// apart.
func duplicateActiveVolume() {
	lvl := Editor.level
	src := activeVolume()
	if lvl == nil || src == nil {
		return
	}
	EditorPushUndo()
	cp := *src
	cp.ScriptData = cloneScriptData(src.ScriptData)
	cp.Geometry = gfx.Geometry{}
	cp.ID = uniqueVolumeID(lvl, src.ID)
	cp.Position = src.Position.Add(mgl32.Vec3{src.HalfX * 2, 0, 0})
	if err := cp.Load(); err != nil {
		editorSetStatus("Volume %s: %v", cp.ID, err)
	}
	lvl.Volumes = append(lvl.Volumes, cp)
	editorSelectVolume(len(lvl.Volumes) - 1)
	editorSetStatus("Duplicated volume %q", cp.ID)
}

// deleteActiveVolume removes the selected volume.
func deleteActiveVolume() {
	lvl := Editor.level
	idx := Editor.ActiveVolumeIdx
	if lvl == nil || idx < 0 || idx >= len(lvl.Volumes) {
		return
	}
	EditorPushUndo()
	id := lvl.Volumes[idx].ID
	lvl.Volumes[idx].Geometry.Delete()
	lvl.Volumes = append(lvl.Volumes[:idx], lvl.Volumes[idx+1:]...)
	editorClearVolumeSelection()
	editorSetStatus("Deleted volume %q", id)
}

// rebuildActiveVolumeCage re-uploads the selected volume's wireframe after a
// change of shape or size. Moving or rotating one needs no rebuild — the cage is
// built in volume-local space and positioned by its model matrix.
func rebuildActiveVolumeCage() {
	if v := activeVolume(); v != nil {
		_ = v.Load()
	}
}

// ---------- picking ----------

// pickVolumeRayT returns the distance at which a picking ray enters volume v,
// or +Inf when it does not — what puts a volume into the 3D viewport's
// closest-hit race alongside the entities and the sector faces.
//
// Only the entry counts. A volume the camera is standing *inside* is skipped
// altogether rather than answering with its exit: a room-sized trigger would
// otherwise sit in front of every surface in the room it wraps and swallow
// every click meant for one. Such a volume is still reachable from the panel's
// list, which is what that list is for.
//
// Enabled is ignored for the same reason pickVolumeAt ignores it: a script that
// disarmed a trigger must not thereby make it unselectable.
func pickVolumeRayT(origin, dir mgl32.Vec3, v *Volume) float32 {
	if !v.HasBounds() {
		return float32(math.MaxFloat32)
	}
	t0, _, ok := rayShapeRange(origin, dir, v.Position, v.Rotation, v.HalfExtents(), v.boundingKind())
	if !ok || t0 <= 0 {
		return float32(math.MaxFloat32)
	}
	return t0
}

// pickVolumeAt returns the index of the volume whose region contains p, or -1.
// The smallest containing volume wins, so a small trigger inside a large room
// volume is still selectable.
func pickVolumeAt(lvl *Level, p mgl32.Vec3) int {
	if lvl == nil {
		return -1
	}
	best, bestVol := -1, float32(0)
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		// Placement ignores Enabled: a disarmed trigger still has to be
		// clickable, or a script that switched one off would make it
		// unselectable in the editor.
		if !v.HasBounds() {
			continue
		}
		if !shapeContainsPoint(p, v.Position, v.Rotation, v.HalfExtents(), v.boundingKind()) {
			continue
		}
		vol := v.HalfX * v.HalfY * v.HalfZ
		if best < 0 || vol < bestVol {
			best, bestVol = i, vol
		}
	}
	return best
}

// ---------- menu helpers ----------

// editor3DMoveVolumeTo puts the selected volume at pos — the context menu's
// "move here", the same one-shot placement of something already in the level
// that editor3DMoveEntityTo is for an entity.
func editor3DMoveVolumeTo(pos mgl32.Vec3) {
	v := activeVolume()
	if v == nil {
		return
	}
	EditorPushUndo()
	v.Position = pos
}

// editor3DAddVolumeAt drops a volume at pos, the same way the panel's placement
// button does — the menu offers "add one here" rather than arming a mode, since
// it was opened on the very point the volume would go.
func editor3DAddVolumeAt(pos mgl32.Vec3) { placeVolumeAt(pos) }

// ---------- selection mark ----------

// volumeSelColor is the selected volume's cage colour — the same yellow a
// selected vertex handle uses, so "this is the thing the gizmo is holding"
// reads the same way whatever kind of thing it is.
var volumeSelColor = mgl32.Vec3{1, 1, 0}

// drawSelectedVolumeOutline re-traces the selected volume's cage through the
// gizmo line buffer, which draws with depth testing off. The cage is already
// drawn with the world (volume_render.go), but a region is mostly seen from
// outside the geometry it wraps, so the mark that says which one is selected
// has to survive being behind a wall the way a selected face's outline does.
func drawSelectedVolumeOutline() {
	v := activeVolume()
	if v == nil || !v.HasBounds() {
		return
	}
	verts, idx := v.cageMesh()
	model := v.ModelMatrix()
	at := func(i int) mgl32.Vec3 {
		p := model.Mul4x1(mgl32.Vec4{verts[i][0], verts[i][1], verts[i][2], 1})
		return mgl32.Vec3{p.X(), p.Y(), p.Z()}
	}
	for i := 0; i+1 < len(idx); i += 2 {
		a, b := idx[i], idx[i+1]
		if a < 0 || a >= len(verts) || b < 0 || b >= len(verts) {
			continue
		}
		GizmoLine(at(a), at(b), volumeSelColor)
	}
}
