package main

// editor_3d.go — 3D fly-around sub-mode of the level editor.
//
// Handles:
//   - Selection via screen-space ray casting
//   - Spawn-point placement
//   - Chunk creation at camera position
//   - Gizmo drawing for selected objects

import (
	"fmt"
	"foundry/internal/geom"
	"foundry/internal/platform"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// ---------- Ray casting ----------

// screenRay returns the world-space ray origin and normalised direction for the
// given screen pixel (sx, sy) using the current projection and camera matrices.
func screenRay(sx, sy int32) (origin, dir mgl32.Vec3) {
	// NDC
	ndcX := (float32(sx)/float32(platform.WinWidth))*2.0 - 1.0
	ndcY := 1.0 - (float32(sy)/float32(platform.WinHeight))*2.0

	// Unproject through inverse projection
	invProj := Projection.Inv()
	clip := mgl32.Vec4{ndcX, ndcY, -1, 1}
	eye := invProj.Mul4x1(clip)
	eye[2] = -1
	eye[3] = 0

	invCam := Cam.Matrix.Inv()
	world := invCam.Mul4x1(eye)
	dir = mgl32.Vec3{world.X(), world.Y(), world.Z()}.Normalize()
	return Cam.Position, dir
}

// raySlabRange returns the range of distances along a ray that lie inside an
// AABB, as the classic slab test. ok is false when the ray misses the box
// altogether; a range that starts at a negative distance is the ray's origin
// being inside it, which is a thing callers want to tell apart from a hit.
func raySlabRange(origin, dir, aabbMin, aabbMax mgl32.Vec3) (tmin, tmax float32, ok bool) {
	tmin = float32(math.Inf(-1))
	tmax = float32(math.Inf(1))
	for i := 0; i < 3; i++ {
		if math.Abs(float64(dir[i])) < 1e-6 {
			// Parallel to this pair of planes: either between them for the
			// whole ray, or never.
			if origin[i] < aabbMin[i] || origin[i] > aabbMax[i] {
				return 0, 0, false
			}
			continue
		}
		t1 := (aabbMin[i] - origin[i]) / dir[i]
		t2 := (aabbMax[i] - origin[i]) / dir[i]
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		if t1 > tmin {
			tmin = t1
		}
		if t2 < tmax {
			tmax = t2
		}
		if tmin > tmax {
			return 0, 0, false
		}
	}
	return tmin, tmax, true
}

// rayAABBIntersect tests a ray against an AABB. Returns true and the hit
// distance if there is an intersection — the exit distance when the origin is
// inside the box, so that a hit is still reported from within.
func rayAABBIntersect(origin, dir, aabbMin, aabbMax mgl32.Vec3) (bool, float32) {
	tmin, tmax, ok := raySlabRange(origin, dir, aabbMin, aabbMax)
	if !ok || tmax < 0 {
		return false, 0
	}
	if tmin < 0 {
		return true, tmax
	}
	return true, tmin
}

// rayShapeRange is the ray-cast counterpart of shapeContainsPoint: the range of
// distances along a ray that lie inside one of the three bounding solids. It
// takes the same arguments so the two agree on what a shape is — rotated by its
// Euler angles, inscribed in the box its half-extents describe — and so a
// picked region is the same region a script tests points against.
//
// t0 negative means the ray's origin is inside the solid. ok is false when the
// ray misses, or when the extents describe no solid at all.
func rayShapeRange(origin, dir, center, rotation, half mgl32.Vec3, kind BoundingType) (t0, t1 float32, ok bool) {
	if half.X() <= 0 || half.Y() <= 0 || half.Z() <= 0 {
		return 0, 0, false
	}
	o := origin.Sub(center)
	d := dir
	if rotation != (mgl32.Vec3{}) {
		// The inverse of a rotation is its transpose, the same shortcut
		// shapeContainsPoint takes. Being orthonormal it leaves lengths alone,
		// so the distances that come back are directly comparable with every
		// other hit measured in world space.
		inv := eulerMatrixYXZ(rotation).Transpose()
		o4 := inv.Mul4x1(o.Vec4(0))
		d4 := inv.Mul4x1(d.Vec4(0))
		o = mgl32.Vec3{o4.X(), o4.Y(), o4.Z()}
		d = mgl32.Vec3{d4.X(), d4.Y(), d4.Z()}
	}
	switch kind {
	case BoundingSphere:
		// Dividing through by the half-extents turns the ellipsoid into the
		// unit sphere. Origin and direction are scaled together, which leaves
		// the ray parameter untouched — the roots are still world distances.
		return rayUnitSphereRange(shapeNormalize(o, half), shapeNormalize(d, half))
	case BoundingCylinder:
		return rayCylinderRange(o, d, half)
	default:
		return raySlabRange(o, d, half.Mul(-1), half)
	}
}

// shapeNormalize divides a vector through by the half-extents, which is what
// maps an inscribed ellipsoid or elliptical cylinder onto its unit form.
func shapeNormalize(v, half mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{v.X() / half.X(), v.Y() / half.Y(), v.Z() / half.Z()}
}

// rayUnitSphereRange solves a ray against the unit sphere at the origin.
func rayUnitSphereRange(o, d mgl32.Vec3) (float32, float32, bool) {
	return solveQuadratic(d.Dot(d), 2*o.Dot(d), o.Dot(o)-1)
}

// rayCylinderRange solves a ray against an upright cylinder — a unit circle in
// XZ once divided by the half-extents, capped by a slab on Y, matching the
// cylinder shapeContainsPoint describes.
func rayCylinderRange(o, d, half mgl32.Vec3) (float32, float32, bool) {
	ox, oz := o.X()/half.X(), o.Z()/half.Z()
	dx, dz := d.X()/half.X(), d.Z()/half.Z()

	a := dx*dx + dz*dz
	c := ox*ox + oz*oz - 1
	var t0, t1 float32
	if a < 1e-12 {
		// platform.Running straight up the axis: the ray never crosses the side, so it
		// is within the circle for its whole length or for none of it.
		if c > 0 {
			return 0, 0, false
		}
		t0, t1 = float32(math.Inf(-1)), float32(math.Inf(1))
	} else {
		var hit bool
		if t0, t1, hit = solveQuadratic(a, 2*(ox*dx+oz*dz), c); !hit {
			return 0, 0, false
		}
	}

	if math.Abs(float64(d.Y())) < 1e-6 {
		if geom.Abs32(o.Y()) > half.Y() {
			return 0, 0, false
		}
		return t0, t1, true
	}
	ty0 := (-half.Y() - o.Y()) / d.Y()
	ty1 := (half.Y() - o.Y()) / d.Y()
	if ty0 > ty1 {
		ty0, ty1 = ty1, ty0
	}
	if ty0 > t0 {
		t0 = ty0
	}
	if ty1 < t1 {
		t1 = ty1
	}
	if t0 > t1 {
		return 0, 0, false
	}
	return t0, t1, true
}

// solveQuadratic returns the real roots of a·t² + b·t + c, smaller first. ok is
// false when there are none, and when a is too small for the equation to be a
// quadratic at all — what a degenerate ray means is the shape's business, not
// this function's.
func solveQuadratic(a, b, c float32) (float32, float32, bool) {
	if geom.Abs32(a) < 1e-12 {
		return 0, 0, false
	}
	disc := b*b - 4*a*c
	if disc < 0 {
		return 0, 0, false
	}
	sq := float32(math.Sqrt(float64(disc)))
	t0 := (-b - sq) / (2 * a)
	t1 := (-b + sq) / (2 * a)
	if t0 > t1 {
		t0, t1 = t1, t0
	}
	return t0, t1, true
}

// rayTriangleIntersect tests a ray against a triangle (two-sided) using Möller–Trumbore.
// Returns true, the distance t if the ray hits the triangle in front of the origin,
// and a front flag that is true when the ray approaches the triangle from the side
// its vertex winding faces (front-facing), false when it hits the back side.
func rayTriangleIntersect(origin, dir, v0, v1, v2 mgl32.Vec3) (bool, float32, bool) {
	const eps = 1e-6
	edge1 := v1.Sub(v0)
	edge2 := v2.Sub(v0)
	h := dir.Cross(edge2)
	a := edge1.Dot(h)
	if a > -eps && a < eps {
		return false, 0, false
	}
	f := 1.0 / a
	sv := origin.Sub(v0)
	u := f * sv.Dot(h)
	if u < 0 || u > 1 {
		return false, 0, false
	}
	q := sv.Cross(edge1)
	v := f * dir.Dot(q)
	if v < 0 || u+v > 1 {
		return false, 0, false
	}
	t := f * edge2.Dot(q)
	if t < eps {
		return false, 0, false
	}
	return true, t, a > 0
}

// pickFaceBias is added to a back-facing triangle's distance when comparing
// picker hits, so that a coincident front-facing triangle wins the tie. Chosen
// to be large enough to dominate float noise at typical editor scales (~1 mm)
// yet small enough that a genuinely-closer back face still beats a distant
// front face.
const pickFaceBias float32 = 1e-3

// effectivePickT returns t biased by pickFaceBias when the hit is back-facing.
func effectivePickT(t float32, front bool) float32 {
	if front {
		return t
	}
	return t + pickFaceBias
}

// Editor3DHandleClick processes a mouse click in 3D mode (selection or spawn
// placement).
func Editor3DHandleClick(t sdl.MouseButtonEvent) {
	if t.Button != sdl.ButtonLeft {
		return
	}

	if Editor.SpawnPlacementMode {
		// 4m in front of the camera, at the floor the camera is standing on.
		pos := Cam.Position.Add(Cam.Direction.Mul(4.0))
		pos[1] -= eyeHeight
		placeSpawnPointAt(pos)
		return
	}

	if Editor.EntityPlacementMode {
		// editor3DPointAt puts it on the surface the click is aimed at, so an
		// entity lands on the floor rather than floating at a fixed distance.
		placeEntityAt(editor3DPointAt(t.X, t.Y))
		return
	}

	if Editor.VolumePlacementMode {
		// A volume is a region rather than something that rests on a surface, so
		// it is centred a little above where the click landed — a trigger flush
		// with the floor would have half of itself underneath it.
		pos := editor3DPointAt(t.X, t.Y)
		pos[1] += 1.0
		placeVolumeAt(pos)
		return
	}

	// What a click means is the selection mode's call. geom.Point and edge mode each
	// own the click outright: hitting nothing there clears the selection rather
	// than falling through to the surface behind, which is what makes the modes
	// predictable. Repeated clicks on the same cluster cycle through every
	// handle under the cursor; ctrl+click takes them all. (Ctrl rather than
	// shift — shift is held to fly downward.)
	ctrlHeld := sdl.GetModState()&sdl.KMOD_CTRL != 0
	switch Editor.SelMode {
	case editorSelPoint:
		if !handleVertexClick(t.X, t.Y, ctrlHeld) && !ctrlHeld {
			editor3DClearSelection()
		}
		return
	case editorSelEdge:
		if !handleEdgeClick(t.X, t.Y, ctrlHeld) && !ctrlHeld {
			editor3DClearSelection()
		}
		return
	}

	// Face mode. Selection: one ray, closest hit wins across the spawn marker,
	// the placed entities and the sector faces.
	hit := pick3D(t.X, t.Y)

	// A face, and ctrl held: add it to (or take it back out of) the set being
	// built, leaving everything else alone. This is the only branch that does
	// not start from a clean slate, because building a multi-selection is
	// exactly the case where the previous click's result has to survive.
	if f, ok := faceSelFromHit(hit); ok && ctrlHeld {
		editorSelectFace(f, true)
		Editor.ActiveBiomeSectorIdx = -1
		return
	}
	// A ctrl+click that reached no face keeps the set as it is rather than
	// throwing it away: aiming past the last wall of a dozen and missing must
	// not undo the other eleven.
	if ctrlHeld {
		return
	}

	// Start from a clean slate so no stale selection survives the click. The
	// biome sector goes with the rest: it is the same selection the 2D layer
	// works in, and one sector is selected wherever it was clicked.
	editorClearSpawnSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	clearVertexSelection()
	clearFaceSelection()
	Editor.ActiveBiomeSectorIdx = -1
	Editor.GizmoKind = gizmoKindNone

	if f, ok := faceSelFromHit(hit); ok {
		editorSelectFace(f, false)
		return
	}

	switch hit.kind {
	case pickSpawn:
		Editor.ActiveSectorIdx = -1
		editorSelectSpawn()

	case pickEntity:
		Editor.ActiveSectorIdx = -1
		editorSelectEntity(hit.idx)

	case pickVolume:
		// The same gizmos an entity gets: a volume has a position, an
		// orientation and a size, and the scale gizmo drags its half-extents
		// (applyScaleToSelection) rather than a scale factor it does not have.
		Editor.ActiveSectorIdx = -1
		editorSelectVolume(hit.idx)

	case pickWater:
		// A water plane has no properties of its own — everything about it is
		// derived from the Water-kind biome sector that produced it — so the
		// click selects that sector and the panel shows its water settings.
		Editor.ActiveChunkIdx = hit.chunkIdx
		Editor.ActiveSectorIdx = -1
		Editor.ActiveBiomeSectorIdx = editorWaterSectorFor(hit.chunkIdx, hit.idx)
		Editor.PanelFocus = panelFocusWater

	default:
		// Click on empty space — deselect.
		Editor.ActiveSectorIdx = -1
		Editor.PanelFocus = panelFocusNone
	}
}

// editor3DClearSelection drops every kind of 3D selection at once — the miss
// case for point and edge mode, where nothing under the cursor means nothing
// selected.
func editor3DClearSelection() {
	clearVertexSelection()
	editorClearSpawnSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	clearFaceSelection()
	Editor.ActiveSectorIdx = -1
	Editor.ActiveBiomeSectorIdx = -1
	Editor.GizmoKind = gizmoKindNone
	Editor.PanelFocus = panelFocusNone
}

// editorWaterSection shows the settings behind the water plane the user clicked.
// The plane itself has nothing to edit — it is regenerated from its Water-kind
// biome sector whenever that sector changes — so this is that sector's water
// half, reached from the 3D view instead of from the 2D biome layer.
func editorWaterSection(lvl *Level) {
	// Shown whenever a water sector is the selection, however it was selected:
	// clicking the plane here, or picking the sector in the 2D biome layer.
	if Editor.ActiveBiomeSectorIdx < 0 ||
		Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if Editor.ActiveBiomeSectorIdx < 0 || Editor.ActiveBiomeSectorIdx >= len(ch.BiomeSectors) {
		return
	}
	bs := &ch.BiomeSectors[Editor.ActiveBiomeSectorIdx]
	if bs.Brush.Kind != BiomeWater {
		return
	}

	imgui.Separator()
	editorFocusOpen(panelFocusWater)
	if !imgui.CollapsingHeaderV("Water: "+bs.ID, imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusWater)
	biomeWaterControls(ch, bs)
	imgui.Text("Shape and extent come from the painted polygon —")
	imgui.Text("edit those in the 2D view's biome layer.")
}

// editorWaterSectorFor resolves a water surface back to the biome sector that
// produced it. The two are matched by ID, which is what biome_water.go's sync
// keys on as well; -1 when the sector has since been deleted and the surface
// has not been swept up yet.
func editorWaterSectorFor(chunkIdx, surfaceIdx int) int {
	lvl := Editor.level
	if lvl == nil || chunkIdx < 0 || chunkIdx >= len(lvl.Chunks) {
		return -1
	}
	ch := &lvl.Chunks[chunkIdx]
	if surfaceIdx < 0 || surfaceIdx >= len(ch.WaterSurfaces) {
		return -1
	}
	id := ch.WaterSurfaces[surfaceIdx].ID
	for i := range ch.BiomeSectors {
		if ch.BiomeSectors[i].ID == id {
			return i
		}
	}
	return -1
}

// moveChunk shifts all shared points in a chunk by the delta to newOrigin,
// rebuilds geometry, and updates the chunk's origin and AABB.
func moveChunk(ch *Chunk, newOrigin mgl32.Vec3) {
	delta := newOrigin.Sub(ch.Origin)
	dx := delta.X()
	dy := delta.Y()
	dz := delta.Z()
	for vi := range ch.Points {
		ch.Points[vi][0] += dx
		ch.Points[vi][1] += dz
	}
	// A chunk carries its own paint: every biome polygon stored on it slides
	// rigidly with the floor, including any part that spilled past a sector
	// edge. (Per-vertex warping is for edits that change a sector's shape —
	// see carryBiomesWithPoints — and would leave spilled paint behind here.)
	for bi := range ch.BiomeSectors {
		bs := &ch.BiomeSectors[bi]
		for pi := range bs.Points {
			bs.Points[pi] = bs.Points[pi].Add(mgl32.Vec2{dx, dz})
		}
		bs.HeightDirty = true
		bs.GroundDirty = true
	}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		s.Floor.Height += dy
		s.Ceiling.Height += dy
		rebuildSectorEdgePoints(ch, s)
		_ = reloadSector(ch, s)
	}
	ch.RebuildGaps()
	ch.MinBound = ch.MinBound.Add(delta)
	ch.MaxBound = ch.MaxBound.Add(delta)
	ch.Origin = newOrigin
}

// Editor3DRenderGizmos draws wireframe overlays for selected objects.
func Editor3DRenderGizmos(projView mgl32.Mat4) {
	lvl := Editor.level
	if lvl == nil {
		return
	}

	yellow := mgl32.Vec3{1, 1, 0}
	cyan := mgl32.Vec3{0, 1, 1}
	white := mgl32.Vec3{1, 1, 1}

	// Outline the spawn marker, so it stays findable even when the arrow itself
	// is edge-on or buried in a slope.
	if lvl.Spawn != nil {
		col := white
		if Editor.SpawnSelected {
			col = yellow
		}
		smin, smax := lvl.Spawn.WorldAABB()
		GizmoAABB(smin, smax, col)
	}

	// Draw point light spheres
	for i := range lvl.Lights.Points {
		pl := &lvl.Lights.Points[i]
		col := mgl32.Vec3{pl.Color.X(), pl.Color.Y(), pl.Color.Z()}
		if col.Len() < 0.1 {
			col = mgl32.Vec3{1, 0.8, 0.2}
		}
		GizmoSphere(pl.Position, pl.Radius*0.1, col)
		GizmoLine(pl.Position, pl.Position.Add(mgl32.Vec3{0, 0.3, 0}), col)
	}

	// Highlight selected chunk AABB
	if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
		ch := &lvl.Chunks[Editor.ActiveChunkIdx]
		GizmoAABB(ch.MinBound, ch.MaxBound, cyan)
	}

	// Extrusion gizmos
	editorExtrusionRenderGizmos()
	editorRenderExtrusionOutlines()

	// The selected face's outline, the handles the current selection mode can
	// act on, and the translate gizmo — drawn last and without depth testing,
	// so a mark is visible wherever it means something.
	editorRenderInteractiveOverlays(projView, true)
}

// ---------- ImGui panels for 3D mode ----------

// editorPanelFocused reports whether the side panel has a pending request to
// reveal one of the given sections, so a 3D click lands the user directly on
// the properties for whatever they clicked.
func editorPanelFocused(sections ...string) bool {
	for _, s := range sections {
		if Editor.PanelFocus == s {
			return true
		}
	}
	return false
}

// editorFocusOpen force-opens the next collapsing header / tree node when one
// of the given sections is focused. Call immediately before the widget.
func editorFocusOpen(sections ...string) {
	if editorPanelFocused(sections...) {
		imgui.SetNextItemOpen(true, imgui.ConditionAlways)
	}
}

// editorFocusScroll scrolls the panel to the current cursor position when one
// of the given sections is focused. Call just inside the opened section.
func editorFocusScroll(sections ...string) {
	if editorPanelFocused(sections...) {
		imgui.SetScrollHereY(0.15)
	}
}

// editor3DVertexPanel draws the properties of the sector vertices selected by
// clicking their handle boxes in 3D. With more than one selected, editing a
// coordinate collapses every selected vertex onto that value — dragging an axis
// arm is the way to move them as a group.
func editor3DVertexPanel(lvl *Level) {
	sels := Editor.SelectedVertices
	if len(sels) == 0 {
		return
	}
	primary := sels[0]
	ch, s := sectorOf(primary)
	if ch == nil || s == nil || primary.vertIdx >= len(s.PointIndices) {
		return
	}
	pIdx := s.PointIndices[primary.vertIdx]
	if pIdx < 0 || pIdx >= len(ch.Points) {
		return
	}

	which := "Ceiling"
	if primary.isFloor {
		which = "Floor"
	}
	title := fmt.Sprintf("Vertex #%d (%s) — %s", primary.vertIdx, which, s.ID)
	if len(sels) > 1 {
		title = fmt.Sprintf("%d Vertices Selected", len(sels))
	}

	imgui.Separator()
	editorFocusOpen(panelFocusVertex)
	if imgui.CollapsingHeaderV(title, imgui.TreeNodeFlagsDefaultOpen) {
		editorFocusScroll(panelFocusVertex)

		pt := ch.Points[pIdx]
		x := pt.X()
		z := pt.Y()
		xChanged := editorDragFloat("X##v3dx", &x, -1e6, 1e6, "%.3f")
		zChanged := editorDragFloat("Z##v3dz", &z, -1e6, 1e6, "%.3f")
		if xChanged || zChanged {
			setSelectedVertexPoints(func(_ int, _ vertexSel, cur mgl32.Vec2) mgl32.Vec2 {
				out := cur
				if xChanged {
					out[0] = x
				}
				if zChanged {
					out[1] = z
				}
				return out
			})
		}

		if len(sels) > 1 {
			// One field for the whole selection: each vertex's own surface
			// (floor or ceiling, per handle) gets the offset.
			off := vertexOffset(s, primary)
			if editorDragFloat("Height Offset##v3dmulti", &off, -100, 100, "%.3f") {
				setSelectedVertexOffsets(func(_ int, _ vertexSel, _ float32) float32 {
					return off
				})
			}
			imgui.Text("Editing a field collapses all selected vertices to it")
		} else {
			if primary.vertIdx < len(s.Floor.VertexOffsets) {
				fOff := s.Floor.VertexOffsets[primary.vertIdx]
				if editorDragFloat("Floor Offset##v3dfo", &fOff, -100, 100, "%.3f") {
					s.Floor.VertexOffsets[primary.vertIdx] = fOff
					_ = reloadSector(ch, s)
				}
			}
			if primary.vertIdx < len(s.Ceiling.VertexOffsets) {
				cOff := s.Ceiling.VertexOffsets[primary.vertIdx]
				if editorDragFloat("Ceil Offset##v3dco", &cOff, -100, 100, "%.3f") {
					s.Ceiling.VertexOffsets[primary.vertIdx] = cOff
					_ = reloadSector(ch, s)
				}
			}
			imgui.Text("Click the handle again to cycle overlapping vertices")
		}
		imgui.Text("Ctrl+click adds every vertex under the cursor")

		if imgui.Button("Deselect Vertices##v3dclear") {
			clearVertexSelection()
		}
	}
}

// Editor3DPanel draws the 3D-mode ImGui side panel.
func Editor3DPanel() {
	lvl := Editor.level
	if lvl == nil {
		return
	}

	imgui.SetNextWindowPos(imgui.Vec2{X: float32(platform.WinWidth) - Editor.PanelWidth, Y: 28})
	imgui.SetNextWindowSize(imgui.Vec2{X: Editor.PanelWidth, Y: float32(platform.WinHeight) - 28})
	imgui.BeginV("3D Editor", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	// ---- Grid settings ----
	editorGridSettings()

	// ---- Navigation buttons ----
	if imgui.CollapsingHeaderV("Navigation", imgui.TreeNodeFlagsDefaultOpen) {
		imgui.Text("Right- or middle-drag to turn; shift+middle to pan")
		imgui.Text("V flies or orbits, F frames the selection, wheel zooms")
		imgui.Text("1/2/3 pick points, edges or faces; Z toggles wireframe")
		imgui.Text("Ctrl+click adds to the selection, in all three modes")
		imgui.Text("Right-click without dragging for the selection's actions")
		imgui.Text("Drag an R/G/B arm to move it along X/Y/Z")
		imgui.Checkbox("Show All geom.Point/Edge Boxes", &Editor.ShowPointSelectRadius)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Wireframe already draws them all; this is the same\nfor a solid-shaded view.")
		}
		imgui.Button("Fwd##nav")
		if imgui.IsItemActive() {
			Editor.NavForward = true
		}
		imgui.SameLine()
		imgui.Button("Back##nav")
		if imgui.IsItemActive() {
			Editor.NavBack = true
		}
		imgui.SameLine()
		imgui.Button("Left##nav")
		if imgui.IsItemActive() {
			Editor.NavLeft = true
		}
		imgui.SameLine()
		imgui.Button("Right##nav")
		if imgui.IsItemActive() {
			Editor.NavRight = true
		}
		imgui.SameLine()
		imgui.Button("Up##nav")
		if imgui.IsItemActive() {
			Editor.NavUp = true
		}
		imgui.SameLine()
		imgui.Button("Down##nav")
		if imgui.IsItemActive() {
			Editor.NavDown = true
		}
	}

	// --- Player spawn point ---
	editorSpawnSection(lvl)

	// --- Entities: the catalog, the level's list, and the selected one ---
	editorEntitySection(lvl)
	editorVolumeSection(lvl)
	editorEntityProperties(lvl)

	// --- Water plane clicked in the viewport ---
	editorWaterSection(lvl)

	// --- Selected sector/edge properties (from 3D click) ---
	if !Editor.SpawnSelected && Editor.ActiveSectorIdx >= 0 &&
		Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
		imgui.Separator()
		sch := &lvl.Chunks[Editor.ActiveChunkIdx]
		if Editor.ActiveSectorIdx < len(sch.Sectors) {
			s3d := &sch.Sectors[Editor.ActiveSectorIdx]
			editorFocusOpen(panelFocusFloor, panelFocusCeiling, panelFocusWall)
			if imgui.CollapsingHeaderV("Sector: "+s3d.ID, imgui.TreeNodeFlagsDefaultOpen) {
				imgui.InputText("ID##sid3d", &s3d.ID)
				editorFaceSelectionNote()
				// The sector's own tags, attributes and script. Its floor,
				// ceiling and edges each carry their own set below.
				editorScriptDataUI(&s3d.ScriptData, "sec3d", "This sector")
				editorFocusOpen(panelFocusFloor)
				if imgui.TreeNode("Floor##3df") {
					editorFocusScroll(panelFocusFloor)
					changed := false
					changed = imgui.Checkbox("Enabled##fe3d", &s3d.Floor.Enabled) || changed
					imgui.Checkbox("Hide Texture##fhid3d", &s3d.Floor.Hidden)
					changed = editorDragFloat("Height##fh3d", &s3d.Floor.Height, 0, 0, "%.3f") || changed
					changed = textureCombo("Texture##ft3d", &s3d.Floor.Texture, false) || changed
					changed = imgui.Checkbox("Terrain##fter3d", &s3d.Floor.Terrain) || changed
					imgui.Checkbox("Double Sided##fds3d", &s3d.Floor.DoubleSided)
					changed = editorDragFloat("Alpha##fa3d", &s3d.Floor.Alpha, 0, 1, "%.2f") || changed
					if changed {
						_ = reloadSector(sch, s3d)
					}
					imgui.TreePop()
				}
				if imgui.Button("Edit Floor Surface##floorSurf3d") {
					EnterSurface2D("floor")
				}
				if editorUVControls("Floor", &s3d.Floor.UVOffset, &s3d.Floor.UVScale, &s3d.Floor.UVRotation) {
					_ = reloadSector(sch, s3d)
				}
				editorSurfaceExtrusionPanel(sch, s3d, "Floor", &s3d.Floor.Extrusions)
				editorTerrainPanel(sch, s3d, "Floor", &s3d.Floor.Terrain, &s3d.Floor.TerrainPoints)
				if editorSmoothPanel("Floor", &s3d.Floor.Smooth, &s3d.Floor.SmoothAngle) {
					_ = reloadSector(sch, s3d)
				}
				editorDetailPanel("Floor", &s3d.Floor.Detail)
				editorScriptDataUI(&s3d.Floor.ScriptData, "floor3d", "This floor")
				editorFocusOpen(panelFocusCeiling)
				if imgui.TreeNode("Ceiling##3dc") {
					editorFocusScroll(panelFocusCeiling)
					changed := false
					changed = imgui.Checkbox("Enabled##ce3d", &s3d.Ceiling.Enabled) || changed
					changed = editorDragFloat("Height##ch3d", &s3d.Ceiling.Height, 0, 0, "%.3f") || changed
					changed = textureCombo("Texture##ct3d", &s3d.Ceiling.Texture, false) || changed
					changed = imgui.Checkbox("Terrain##cter3d", &s3d.Ceiling.Terrain) || changed
					imgui.Checkbox("Double Sided##cds3d", &s3d.Ceiling.DoubleSided)
					changed = editorDragFloat("Alpha##ca3d", &s3d.Ceiling.Alpha, 0, 1, "%.2f") || changed
					if changed {
						_ = reloadSector(sch, s3d)
					}
					imgui.TreePop()
				}
				if imgui.Button("Edit Ceiling Surface##ceilSurf3d") {
					EnterSurface2D("ceiling")
				}
				if editorUVControls("Ceiling", &s3d.Ceiling.UVOffset, &s3d.Ceiling.UVScale, &s3d.Ceiling.UVRotation) {
					_ = reloadSector(sch, s3d)
				}
				editorSurfaceExtrusionPanel(sch, s3d, "Ceiling", &s3d.Ceiling.Extrusions)
				editorTerrainPanel(sch, s3d, "Ceiling", &s3d.Ceiling.Terrain, &s3d.Ceiling.TerrainPoints)
				if editorSmoothPanel("Ceiling", &s3d.Ceiling.Smooth, &s3d.Ceiling.SmoothAngle) {
					_ = reloadSector(sch, s3d)
				}
				editorDetailPanel("Ceiling", &s3d.Ceiling.Detail)
				editorScriptDataUI(&s3d.Ceiling.ScriptData, "ceil3d", "This ceiling")
				if Editor.ActiveEdgeIdx >= 0 {
					imgui.Separator()
					editorEdgeKindCombo(sch, s3d, "3d")
				}
				if Editor.ActiveEdgeKind == "wall" && Editor.ActiveEdgeIdx >= 0 &&
					Editor.ActiveEdgeIdx < len(s3d.Walls) {
					imgui.Separator()
					editorFocusScroll(panelFocusWall)
					w := &s3d.Walls[Editor.ActiveEdgeIdx]
					// The ID rather than the index: the index shifts under
					// nearly every edit, and the ID is what a script addresses.
					imgui.Text("Wall " + edgeIDDisplay(w.ID))
					if imgui.IsItemHovered() {
						imgui.SetTooltip("A script reaches this edge as @" + s3d.ID + ".edge." + w.ID)
					}
					changed := false
					changed = textureCombo("Texture##wt3d", &w.Texture, false) || changed
					changed = imgui.Checkbox("Collision##wc3d", &w.Collision) || changed
					changed = imgui.Checkbox("Visible##wv3d", &w.Visible) || changed
					changed = imgui.Checkbox("Double Sided##wds3d", &w.DoubleSided) || changed
					changed = editorDragFloat("Alpha##wa3d", &w.Alpha, 0, 1, "%.2f") || changed
					if changed {
						_ = reloadSector(sch, s3d)
					}
					// Surface 2D editor button
					if imgui.Button("Edit Wall Surface##wallSurf3d") {
						EnterSurface2D("wall")
					}
					if editorUVControls("Wall", &w.UVOffset, &w.UVScale, &w.UVRotation) {
						_ = reloadSector(sch, s3d)
					}
					// Extrusion sub-panel
					editorExtrusionPanel(sch, s3d, w)
					// Terrain sub-panel
					editorTerrainPanel(sch, s3d, "Wall", &w.Terrain, &w.TerrainPoints)
					// Smooth shading (rebuilds the wall: normals are made at upload)
					if editorSmoothPanel("Wall", &w.Smooth, &w.SmoothAngle) {
						_ = reloadSector(sch, s3d)
					}
					// Detail sub-panel
					editorDetailPanel("Wall", &w.Detail)
					editorScriptDataUI(&w.ScriptData, "wall3d", "This wall")
				} else if Editor.ActiveEdgeKind == "portal" && Editor.ActiveEdgeIdx >= 0 &&
					Editor.ActiveEdgeIdx < len(s3d.Portals) {
					imgui.Separator()
					p3d := &s3d.Portals[Editor.ActiveEdgeIdx]
					imgui.Text("Portal " + edgeIDDisplay(p3d.ID))
					if imgui.IsItemHovered() {
						imgui.SetTooltip("A script reaches this edge as @" + s3d.ID + ".edge." + p3d.ID)
					}
					changed := false
					changed = imgui.InputText("To Sector##pts3d", &p3d.ToSector) || changed
					changed = imgui.Checkbox("Draw Gap##pdg3d", &p3d.DrawGap) || changed
					if p3d.DrawGap {
						changed = textureCombo("Gap Texture##pgt3d", &p3d.GapTexture, true) || changed
					}
					changed = imgui.Checkbox("Visible##pv3d", &p3d.Visible) || changed
					// The gap quad bakes DoubleSided in, so this needs the
					// rebuild below — unlike floor/ceiling, which read the flag
					// at draw time.
					changed = imgui.Checkbox("Double Sided##pds3d", &p3d.DoubleSided) || changed
					changed = editorDragFloat("Alpha##pa3d", &p3d.Alpha, 0, 1, "%.2f") || changed
					if changed {
						_ = reloadSector(sch, s3d)
						sch.RebuildGaps()
					}
				} else {
					imgui.Text("Click a vertex handle to edit its position")
				}
			}
		}
	}

	// --- Selected vertex properties (from a 3D handle click) ---
	editor3DVertexPanel(lvl)

	// --- Chunk list ---
	editorChunkListSection(lvl)

	// --- Selected chunk properties ---
	editorChunkPropertiesSection(lvl)

	// --- Light editor ---
	imgui.Separator()
	EditorLightPanel()

	// Every section has had its chance to open and scroll itself.
	Editor.PanelFocus = panelFocusNone

	imgui.End()
	editorChunkPopups(lvl)
}

// createChunkAtCamera creates a minimal rectangular chunk at the camera's XZ position.
func createChunkAtCamera(id string) {
	lvl := Editor.level
	// Centre the new chunk on whatever the active sub-mode is looking at: the
	// fly camera in 3D, the map pan in 2D.
	cx := Cam.Position.X()
	cz := Cam.Position.Z()
	if Editor.Mode == Mode2D {
		cx = Editor.Pan2D.X()
		cz = Editor.Pan2D.Y()
	}
	halfSize := float32(5.0)

	// Build shared point pool
	pts := []mgl32.Vec2{
		{cx - halfSize, cz - halfSize},
		{cx + halfSize, cz - halfSize},
		{cx + halfSize, cz + halfSize},
		{cx - halfSize, cz + halfSize},
	}
	floorH := float32(0.0)
	ceilH := float32(4.0)

	// Wall/Floor/Ceiling.Alpha and DoubleSided have no zero-value-safe default
	// like the YAML loader's float32Default/boolDefault helpers apply — leaving
	// them unset here means Alpha stays 0 (fully transparent) and DoubleSided
	// stays false, so set them explicitly to match what a loaded level gets.
	_ = ensureTextureLoaded("concrete")
	_ = ensureTextureLoaded("stone_floor")

	walls := []Wall{
		{StartPoint: pts[0], EndPoint: pts[1], Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0},
		{StartPoint: pts[1], EndPoint: pts[2], Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0},
		{StartPoint: pts[2], EndPoint: pts[3], Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0},
		{StartPoint: pts[3], EndPoint: pts[0], Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0},
	}

	sector := Sector{
		ID:           id + "_room",
		PointIndices: []int{0, 1, 2, 3},
		EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
		Floor:        Floor{Enabled: true, Height: floorH, Texture: "stone_floor", DoubleSided: true, Alpha: 1.0},
		Ceiling:      Ceiling{Enabled: true, Height: ceilH, Texture: "concrete", DoubleSided: true, Alpha: 1.0},
		Walls:        walls,
	}

	chunk := Chunk{
		ID:         id,
		Points:     pts,
		Sectors:    []Sector{sector},
		MinBound:   mgl32.Vec3{cx - halfSize, floorH, cz - halfSize},
		MaxBound:   mgl32.Vec3{cx + halfSize, ceilH, cz + halfSize},
		PointNames: []string{"A", "B", "C", "D"},
	}
	_ = reloadSector(&chunk, &sector)
	chunk.Sectors[0] = sector

	lvl.Chunks = append(lvl.Chunks, chunk)
	Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
}

// duplicateActiveChunk deep-copies the currently active chunk, reuploads all
// GPU geometry, and makes the copy the new active chunk. The new chunk keeps
// the same world-space position as the source; the user can move it via the
// Chunk Properties Origin drag. Sector IDs are suffixed with _copy (or
// _copyN) to stay unique within the level.
func duplicateActiveChunk() {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	idx := Editor.ActiveChunkIdx
	if idx < 0 || idx >= len(lvl.Chunks) {
		return
	}
	src := &lvl.Chunks[idx]

	newID, suffix := uniqueChunkCopyID(lvl, src.ID)
	clone := src.Clone(newID, suffix)

	for si := range clone.Sectors {
		s := &clone.Sectors[si]
		_ = reloadSector(&clone, s)
	}
	clone.RebuildGaps()

	lvl.Chunks = append(lvl.Chunks, clone)
	Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
}

// deleteActiveChunk removes the currently active chunk from the level after
// releasing all of its GPU resources (sector walls, floor/ceiling, portals,
// extrusion geometries, and derived water surfaces).
func deleteActiveChunk() {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	idx := Editor.ActiveChunkIdx
	if idx < 0 || idx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[idx]

	// Release all GPU resources owned by the chunk.
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		for wi := range s.Walls {
			s.Walls[wi].Geometry.Delete()
			for ei := range s.Walls[wi].ExtrusionGeometries {
				s.Walls[wi].ExtrusionGeometries[ei].Delete()
			}
		}
		s.Floor.Geometry.Delete()
		for ei := range s.Floor.ExtrusionGeometries {
			s.Floor.ExtrusionGeometries[ei].Delete()
		}
		s.Ceiling.Geometry.Delete()
		for ei := range s.Ceiling.ExtrusionGeometries {
			s.Ceiling.ExtrusionGeometries[ei].Delete()
		}
		for pi := range s.Portals {
			s.Portals[pi].FloorGap.Delete()
			s.Portals[pi].CeilGap.Delete()
		}
	}
	for wi := range ch.WaterSurfaces {
		ch.WaterSurfaces[wi].Geometry.Delete()
	}

	// Remove the chunk from the level.
	lvl.Chunks = append(lvl.Chunks[:idx], lvl.Chunks[idx+1:]...)

	// Fix up active chunk index.
	if len(lvl.Chunks) == 0 {
		Editor.ActiveChunkIdx = -1
	} else if Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
	}
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	editorClearSpawnSelection()
}

// uniqueChunkCopyID returns a chunk ID + suffix pair that does not collide
// with any existing chunk in lvl. The suffix ("_copy", "_copy2", ...) is
// reused for nested sector IDs so they stay consistent.
func uniqueChunkCopyID(lvl *Level, srcID string) (string, string) {
	existing := make(map[string]struct{}, len(lvl.Chunks))
	for i := range lvl.Chunks {
		existing[lvl.Chunks[i].ID] = struct{}{}
	}
	suffix := "_copy"
	candidate := srcID + suffix
	for n := 2; ; n++ {
		if _, taken := existing[candidate]; !taken {
			return candidate, suffix
		}
		suffix = fmt.Sprintf("_copy%d", n)
		candidate = srcID + suffix
	}
}

// editorFaceSelectionNote says what the face selection is, and — when it holds
// more than one — that the fields below it describe only the first.
//
// The panel binds its widgets straight to the primary's fields, which is what
// makes them live; there is no honest way for a slider to edit twelve floats at
// once. So the note points at where the bulk actions actually are, rather than
// letting the panel quietly imply that typing a texture name here repaints
// everything outlined in the viewport.
func editorFaceSelectionNote() {
	n := selectedFaceCount()
	if n == 0 {
		return
	}
	if n == 1 {
		imgui.Text("Selected face: " + Editor.ActiveSurface)
		return
	}
	imgui.Text(fmt.Sprintf("%d faces selected — fields below are the first one's", n))
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Right-click the viewport for texture, visibility,\n" +
			"collision and wall/portal actions that apply to all of them.")
	}
}
