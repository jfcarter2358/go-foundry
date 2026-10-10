package editor

// editor_decal.go — placing, selecting and picking decals.
//
// The sprite workflow with one thing added, and that one thing is most of what
// makes decals usable: a placed decal is ORIENTED BY THE SURFACE IT WAS CLICKED
// ON. A decal is a projection, so its rotation is not a detail an author dials in
// afterwards — it is the difference between a poster on the wall and a poster
// projected edge-on through it, and the click already knows which wall was meant.
// Nothing else in this editor derives a rotation from the pick, because nothing
// else needs to.
//
// Selection is by index rather than by pointer, for the reason every other level
// object's is: undo swaps the whole level out from under the editor and a pointer
// into the old one would outlive it.
//
// Picking is done against the decal's projection box rather than against its
// built mesh. The mesh is a sliver lying flat on a wall and picking it would mean
// competing with that wall for every click; the box is the thing the author
// placed, is drawn as a cage while selected, and is a target a click can land on.

import (
	"math"

	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// ---------- selection ----------

// activeDecal returns the selected decal, or nil.
func activeDecal() *world.Decal {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveDecalIdx < 0 || Editor.ActiveDecalIdx >= len(lvl.Decals) {
		return nil
	}
	return &lvl.Decals[Editor.ActiveDecalIdx]
}

// editorSelectDecal makes the decal at idx the selection, giving it the gizmo and
// opening its properties section.
func editorSelectDecal(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Decals) {
		return
	}
	editorClearObjectSelections()
	Editor.ActiveDecalIdx = idx
	Editor.GizmoKind = gizmoKindDecal
	Editor.PanelFocus = panelFocusDecal
}

// editorClearDecalSelection drops the decal from the selection, taking the gizmo
// with it so it cannot keep dragging something no longer selected.
func editorClearDecalSelection() {
	Editor.ActiveDecalIdx = -1
	if Editor.GizmoKind == gizmoKindDecal {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoDecal returns the decal the gizmo is attached to, or nil. The gizmo
// kind is checked as well as the index, so a stale selection left by another tool
// cannot silently be dragged.
func activeGizmoDecal() *world.Decal {
	if Editor.GizmoKind != gizmoKindDecal {
		return nil
	}
	return activeDecal()
}

// ---------- placement ----------

// editorArmDecalPlacement makes the next left-click in the viewport drop a decal.
func editorArmDecalPlacement() {
	Editor.DecalPlacementMode = true
	Editor.SpritePlacementMode = false
	Editor.VolumePlacementMode = false
	Editor.EntityPlacementMode = false
	Editor.SpawnPlacementMode = false
}

// placeDecalAt creates a decal on the surface the click at (mx, my) landed on,
// selects it, and leaves placement mode. Reports whether one was placed.
//
// It takes the screen point rather than a world position, unlike every other
// place*At in this editor, because the SURFACE is the thing being placed on: the
// point alone would leave the decal to guess which way to face, and a guess is
// exactly what the click has already answered.
func placeDecalAt(mx, my int32) bool {
	Editor.DecalPlacementMode = false
	lvl := Editor.level
	if lvl == nil {
		return false
	}
	pos, normal, onSurface := workspaceSurfaceAt(mx, my)

	EditorPushUndo()
	base := Editor.NewDecalID
	if base == "" {
		base = "decal"
	}
	d := world.NewDecal(world.UniqueDecalID(lvl, base), pos, normal)
	if Editor.NewDecalTexture != "" {
		d.Texture = Editor.NewDecalTexture
	}
	if err := d.EnsureDecalTexture(); err != nil {
		editorSetStatus("Decal %s: %v", d.ID, err)
	}
	lvl.Decals = append(lvl.Decals, d)
	editorSelectDecal(len(lvl.Decals) - 1)
	if onSurface {
		editorSetStatus("Placed decal %q on the surface you clicked", d.ID)
	} else {
		// Nothing was under the click, so the decal is facing the camera in mid
		// air and will project onto nothing. Said out loud rather than silently
		// placed: an invisible decal with no explanation is the worst outcome
		// here, and moving it onto a wall is a drag away.
		editorSetStatus("Placed decal %q in mid-air — move it onto a surface to see it", d.ID)
	}
	return true
}

// workspaceSurfaceAt casts the picking ray and reports where it met the world and
// which way that surface faces. ok is false when it met nothing, and then the
// point is a fixed distance ahead of the camera and the normal faces back at it.
//
// The normal comes from the picked BRUSH FACE's own plane, carried into world
// space by its chunk's matrix — the same plane the renderer draws and the decal
// clipper will project against, so what lands is flush with what was clicked. A
// hit on anything else — an entity's bounds, a volume's cage — has no surface
// worth the name, so it is treated as a miss.
func workspaceSurfaceAt(mx, my int32) (pos, normal mgl32.Vec3, ok bool) {
	origin, dir := ws().ray(mx, my)
	hit := pick3D(mx, my)
	if hit.kind != pickBrushFace {
		return origin.Add(dir.Mul(4)), dir.Mul(-1), false
	}
	pos = origin.Add(dir.Mul(hit.t))
	n, found := pickedFaceNormal(hit)
	if !found {
		return pos, dir.Mul(-1), false
	}
	// Toward the viewer, always. A face clicked from behind — the inside of a
	// hollowed room, a double-sided pane — hands back its authored outward
	// normal, and a decal facing away from the eye that placed it projects onto
	// nothing at all.
	if n.Dot(dir) > 0 {
		n = n.Mul(-1)
	}
	return pos, n, true
}

// pickedFaceNormal is the world-space plane normal of the brush face a pick
// landed on.
func pickedFaceNormal(hit pickHit) (mgl32.Vec3, bool) {
	lvl := Editor.level
	if lvl == nil || hit.chunkIdx < 0 || hit.chunkIdx >= len(lvl.Chunks) {
		return mgl32.Vec3{}, false
	}
	ch := &lvl.Chunks[hit.chunkIdx]
	if hit.brushIdx < 0 || hit.brushIdx >= len(ch.Brushes) {
		return mgl32.Vec3{}, false
	}
	br := &ch.Brushes[hit.brushIdx]
	// The same mesh the click was tested against — the detail mesh in detail
	// mode, the hull otherwise — or the face index means nothing.
	m, _, okMesh := pickMeshFor(hit.chunkIdx, hit.brushIdx, br)
	if !okMesh || hit.faceIdx < 0 || hit.faceIdx >= len(m.Faces) {
		return mgl32.Vec3{}, false
	}
	n := m.FaceNormal(&m.Faces[hit.faceIdx])
	// Out of the brush's own rest space, through its placement and its chunk's,
	// exactly as the face's triangles are.
	xf := ch.ModelMatrix()
	if br.Dynamic {
		xf = xf.Mul4(br.Placement())
	}
	v := xf.Mul4x1(mgl32.Vec4{n.X(), n.Y(), n.Z(), 0})
	out := mgl32.Vec3{v.X(), v.Y(), v.Z()}
	if out.LenSqr() < 1e-12 {
		return mgl32.Vec3{}, false
	}
	return out.Normalize(), true
}

// ---------- duplicate / delete ----------

// duplicateActiveDecal copies the selected decal beside itself and selects the
// copy. Offset along the decal's own U axis rather than along world X, which is
// what keeps the copy ON the surface the original is on — offsetting a floor
// decal upward, as a sprite's duplicate does, would lift it off the floor.
func duplicateActiveDecal() {
	lvl := Editor.level
	src := activeDecal()
	if lvl == nil || src == nil {
		return
	}
	EditorPushUndo()
	cp := *src
	cp.ScriptData = world.CloneScriptData(src.ScriptData)
	cp.ID = world.UniqueDecalID(lvl, src.ID)
	// A fresh mesh, not the original's: the copy owns no GPU buffers until
	// SyncDecals builds them, and sharing the original's handles would free them
	// twice.
	cp.Pieces = nil
	world.MarkDecalStale(&cp)
	u, _, _ := src.Basis()
	cp.Position = src.Position.Add(u.Mul(src.Width))
	lvl.Decals = append(lvl.Decals, cp)
	editorSelectDecal(len(lvl.Decals) - 1)
	editorSetStatus("Duplicated decal %q", cp.ID)
}

// deleteActiveDecal removes the selected decal, freeing its mesh.
func deleteActiveDecal() {
	lvl := Editor.level
	idx := Editor.ActiveDecalIdx
	if lvl == nil || idx < 0 || idx >= len(lvl.Decals) {
		return
	}
	EditorPushUndo()
	d := &lvl.Decals[idx]
	id := d.ID
	// Unlike a sprite, a decal owns real buffers — its mesh is clipped out of the
	// world and uploaded per decal — so they have to be released rather than
	// dropped with the slice element.
	world.ReleaseDecal(d)
	lvl.Decals = append(lvl.Decals[:idx], lvl.Decals[idx+1:]...)
	editorClearDecalSelection()
	editorSetStatus("Deleted decal %q", id)
}

// ---------- picking ----------

// decalBoxCorners returns the eight world-space corners of a decal's projection
// box, in the order decalBoxEdges indexes them: the four on the -N face first
// (the side AWAY from the viewer, sunk into the surface), then the four on +N.
//
// Within each face the winding is bottom-left, bottom-right, top-right, top-left,
// so corner i and corner i+4 are the same (u, v) at the two depths.
func decalBoxCorners(d *world.Decal) [8]mgl32.Vec3 {
	u, v, n := d.Basis()
	hu := u.Mul(d.Width * 0.5)
	hv := v.Mul(d.Height * 0.5)
	hn := n.Mul(d.Depth * 0.5)
	var out [8]mgl32.Vec3
	i := 0
	for _, sn := range [2]float32{-1, 1} {
		c := d.Position.Add(hn.Mul(sn))
		out[i+0] = c.Sub(hu).Sub(hv)
		out[i+1] = c.Add(hu).Sub(hv)
		out[i+2] = c.Add(hu).Add(hv)
		out[i+3] = c.Sub(hu).Add(hv)
		i += 4
	}
	return out
}

// decalBoxEdges are the twelve edges of that box, as index pairs.
var decalBoxEdges = [12][2]int{
	{0, 1}, {1, 2}, {2, 3}, {3, 0}, // the -N face
	{4, 5}, {5, 6}, {6, 7}, {7, 4}, // the +N face
	{0, 4}, {1, 5}, {2, 6}, {3, 7}, // the four along the projection axis
}

// pickDecalRayT returns the distance at which a picking ray crosses the decal's
// projection box, or +Inf when it misses — what puts a decal into the viewport's
// closest-hit race alongside the entities, volumes and brush faces.
//
// Tested against the box's front FACE rather than the whole box, and the reason
// is that the box is mostly buried: its back half is inside the wall the decal
// lies on, so a ray that entered through the wall would report a hit behind the
// geometry and a click on the wall next to a decal would grab the decal. The
// quad the author can actually see is the +N face, so that is the target.
//
// Visible is ignored, for the reason pickVolumeAt ignores Enabled: a script that
// hid a decal must not thereby make it unselectable in the editor.
func pickDecalRayT(origin, dir mgl32.Vec3, d *world.Decal) float32 {
	if !d.HasSize() {
		return float32(math.MaxFloat32)
	}
	c := decalBoxCorners(d)
	if t, ok := rayTriangle(origin, dir, c[4], c[5], c[6]); ok {
		return t
	}
	if t, ok := rayTriangle(origin, dir, c[4], c[6], c[7]); ok {
		return t
	}
	return float32(math.MaxFloat32)
}

// ---------- menu helpers ----------

// editor3DMoveDecalTo puts the selected decal at pos — the context menu's "move
// here", matching editor3DMoveSpriteTo. The rotation is left alone: "move here"
// means move, and re-aiming the projection would be a second, unasked-for edit.
func editor3DMoveDecalTo(pos mgl32.Vec3) {
	d := activeDecal()
	if d == nil {
		return
	}
	EditorPushUndo()
	d.Position = pos
}

// ---------- selection mark ----------

// decalSelColor is the selected decal's outline colour — the same yellow a
// selected vertex handle, volume cage and sprite quad use.
var decalSelColor = mgl32.Vec3{1, 1, 0}

// drawSelectedDecalOutline traces the selected decal's projection box through the
// gizmo line buffer, which draws with depth testing off — so the mark survives
// the decal being flat against a wall, which every decal is.
//
// The box rather than the front quad alone, because the DEPTH is the field an
// author is most likely to have wrong: a decal that has caught the floor behind a
// wall as well as the wall is diagnosed by seeing how far the cage reaches
// through it, and no other drawing in the editor shows that.
func drawSelectedDecalOutline() {
	d := activeDecal()
	if d == nil || !d.HasSize() {
		return
	}
	c := decalBoxCorners(d)
	for _, e := range decalBoxEdges {
		gizmo.GizmoLine(c[e[0]], c[e[1]], decalSelColor)
	}
	// The facing, as a stub out of the front face's centre. A decal is a
	// projection and which way it points is the whole of whether it works, so it
	// gets the same cue a fixed sprite does.
	front := c[4].Add(c[6]).Mul(0.5)
	stub := d.Width
	if d.Height < stub {
		stub = d.Height
	}
	gizmo.GizmoLine(front, front.Add(d.Facing().Mul(stub*0.5)), decalSelColor)
}
