package editor

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
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/platform"
	"foundry/internal/world"
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
	invProj := gfx.Projection.Inv()
	clip := mgl32.Vec4{ndcX, ndcY, -1, 1}
	eye := invProj.Mul4x1(clip)
	eye[2] = -1
	eye[3] = 0

	invCam := gfx.Cam.Matrix.Inv()
	world := invCam.Mul4x1(eye)
	dir = mgl32.Vec3{world.X(), world.Y(), world.Z()}.Normalize()
	return gfx.Cam.Position, dir
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

// rayShapeRange is the ray-cast counterpart of ShapeContainsPoint: the range of
// distances along a ray that lie inside one of the three bounding solids. It
// takes the same arguments so the two agree on what a shape is — rotated by its
// Euler angles, inscribed in the box its half-extents describe — and so a
// picked region is the same region a script tests points against.
//
// t0 negative means the ray's origin is inside the solid. ok is false when the
// ray misses, or when the extents describe no solid at all.
func rayShapeRange(origin, dir, center, rotation, half mgl32.Vec3, kind world.BoundingType) (t0, t1 float32, ok bool) {
	if half.X() <= 0 || half.Y() <= 0 || half.Z() <= 0 {
		return 0, 0, false
	}
	o := origin.Sub(center)
	d := dir
	if rotation != (mgl32.Vec3{}) {
		// The inverse of a rotation is its transpose, the same shortcut
		// ShapeContainsPoint takes. Being orthonormal it leaves lengths alone,
		// so the distances that come back are directly comparable with every
		// other hit measured in world space.
		inv := world.EulerMatrixYXZ(rotation).Transpose()
		o4 := inv.Mul4x1(o.Vec4(0))
		d4 := inv.Mul4x1(d.Vec4(0))
		o = mgl32.Vec3{o4.X(), o4.Y(), o4.Z()}
		d = mgl32.Vec3{d4.X(), d4.Y(), d4.Z()}
	}
	switch kind {
	case world.BoundingSphere:
		// Dividing through by the half-extents turns the ellipsoid into the
		// unit sphere. Origin and direction are scaled together, which leaves
		// the ray parameter untouched — the roots are still world distances.
		return rayUnitSphereRange(shapeNormalize(o, half), shapeNormalize(d, half))
	case world.BoundingCylinder:
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
// cylinder ShapeContainsPoint describes.
func rayCylinderRange(o, d, half mgl32.Vec3) (float32, float32, bool) {
	ox, oz := o.X()/half.X(), o.Z()/half.Z()
	dx, dz := d.X()/half.X(), d.Z()/half.Z()

	a := dx*dx + dz*dz
	c := ox*ox + oz*oz - 1
	var t0, t1 float32
	if a < 1e-12 {
		// Running straight up the axis: the ray never crosses the side, so it
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
		pos := gfx.Cam.Position.Add(gfx.Cam.Direction.Mul(4.0))
		pos[1] -= world.EyeHeight
		placeSpawnPointAt(pos)
		return
	}

	if Editor.EntityPlacementMode {
		// editor3DPointAt puts it on the surface the click is aimed at, so an
		// entity lands on the floor rather than floating at a fixed distance.
		placeEntityAt(editor3DPointAt(t.X, t.Y))
		return
	}

	if Editor.SpritePlacementMode {
		// On the surface the click is aimed at, like an entity — a sprite is a
		// thing standing somewhere rather than a region, and placeSpriteAt
		// lifts it by half its height so it stands on that surface rather than
		// through it.
		placeSpriteAt(editor3DPointAt(t.X, t.Y))
		return
	}

	if Editor.EmitterPlacementMode {
		// An emitter is a point, so it goes where the click landed — lifting it
		// as a sprite is lifted would put a floor-level dust effect at knee
		// height for no reason an author asked for.
		placeEmitterAt(editor3DPointAt(t.X, t.Y))
		return
	}

	if Editor.SoundPlacementMode {
		// A point too, on the emitter's reasoning: a sound placed on a stream
		// belongs at the stream, not floating above it.
		placeSoundAt(editor3DPointAt(t.X, t.Y))
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

	// What a click means is the selection mode's call. Point and edge mode each
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
	editorClearChunkSelection()
	editorClearLightSelection()
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

	case pickSprite:
		// The same gizmos a volume gets. Scale drags the quad's width and
		// height (applyScaleToSelection), since a sprite's size is its extents
		// and it has no scale factor of its own.
		Editor.ActiveSectorIdx = -1
		editorSelectSprite(hit.idx)

	case pickEmitter:
		Editor.ActiveSectorIdx = -1
		editorSelectEmitter(hit.idx)

	case pickSound:
		Editor.ActiveSectorIdx = -1
		editorSelectSound(hit.idx)

	case pickLight:
		// The Objects list is no longer the only way to reach a light: click
		// its marker and the gizmo attaches to it, the same as an entity's.
		Editor.ActiveSectorIdx = -1
		editorSelectLight(lightKindPoint, hit.idx)

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
	editorClearChunkSelection()
	editorClearLightSelection()
	clearFaceSelection()
	Editor.ActiveSectorIdx = -1
	Editor.ActiveBiomeSectorIdx = -1
	Editor.GizmoKind = gizmoKindNone
	Editor.PanelFocus = panelFocusNone
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

// moveChunk places a chunk at newOrigin.
//
// That is the whole of it: Points, sector heights, biome paint and every other
// per-chunk coordinate are chunk-local (see Chunk's own doc comment) and never
// baked with Origin, so nothing about them needs touching when Origin changes
// — they ride along automatically through Chunk.ModelMatrix, the same way the
// structural mesh already does at render time. There is no geometry to reload
// and no bounds to recompute, because none of it moved.
func moveChunk(ch *world.Chunk, newOrigin mgl32.Vec3) {
	ch.Origin = newOrigin
	editorChunkTransformChanged()
}

// editorChunkTransformChanged re-buckets the spatial hash after a chunk's
// Origin/Rotation/Scale has been written.
//
// The hash is keyed on world cells derived from each chunk's ModelMatrix, so a
// transform edit invalidates it — without this the moved chunk drops out of
// every world-space lookup (collision, entity location, the script location
// verbs) until some unrelated edit rebuilds it. See RebuildSpatialHash. Cheap
// enough to run on every frame of a drag.
func editorChunkTransformChanged() {
	world.RebuildSpatialHash(Editor.level)
}

// chunkWorldOBB describes ch's bounding box where it actually sits in the
// world: its centre, the three unit axes of its own local frame carried out
// through ModelMatrix, and its half-extents along those axes.
//
// An oriented box rather than a refitted world AABB on purpose — a refit of a
// rotated chunk is looser than the chunk itself, so the outline would visibly
// float off the geometry it is supposed to be outlining. Reading the basis out
// of ModelMatrix's columns (rather than rebuilding it from Rotation/Scale)
// keeps this correct for a frame-owned chunk too, whose matrix carries the
// vehicle's own orientation on top of its own.
func chunkWorldOBB(ch *world.Chunk) (center mgl32.Vec3, axes [3]mgl32.Vec3, half mgl32.Vec3) {
	m := ch.ModelMatrix()
	center = ch.LocalToWorld(ch.MinBound.Add(ch.MaxBound).Mul(0.5))
	localHalf := ch.MaxBound.Sub(ch.MinBound).Mul(0.5)
	for i := 0; i < 3; i++ {
		col := mgl32.Vec3{m[i*4], m[i*4+1], m[i*4+2]}
		l := col.Len()
		if l < 1e-6 {
			// Degenerate (zero-scaled) axis: keep a usable basis rather than
			// dividing by zero, and let the extent collapse with it.
			axes[i] = mgl32.Vec3{}
			axes[i][i] = 1
			half[i] = 0
			continue
		}
		axes[i] = col.Mul(1 / l)
		half[i] = localHalf[i] * l
	}
	return center, axes, half
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
		gizmo.GizmoAABB(smin, smax, col)
	}

	// Draw point light spheres. The selected one goes yellow, like every other
	// selected object, so it is clear which light the translate gizmo's arms
	// are attached to.
	selKind, selIdx := Editor.SelectedLightKind, Editor.SelectedLightIdx
	for i := range lvl.Lights.Points {
		pl := &lvl.Lights.Points[i]
		col := mgl32.Vec3{pl.Color.X(), pl.Color.Y(), pl.Color.Z()}
		if col.Len() < 0.1 {
			col = mgl32.Vec3{1, 0.8, 0.2}
		}
		if selKind == lightKindPoint && selIdx == i {
			col = yellow
		}
		// The same radius picking aims at, so what a click can reach is what
		// the view draws — see pointLightMarkerRadius.
		gizmo.GizmoSphere(pl.Position, pointLightMarkerRadius(pl), col)
		gizmo.GizmoLine(pl.Position, pl.Position.Add(mgl32.Vec3{0, 0.3, 0}), col)
	}

	// Highlight the selected chunk's box, where the chunk actually is:
	// MinBound/MaxBound are chunk-local, so they have to be carried out
	// through the chunk's own placement or the outline sits at the world
	// origin however far the chunk has been moved.
	if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
		ch := &lvl.Chunks[Editor.ActiveChunkIdx]
		center, axes, half := chunkWorldOBB(ch)
		gizmo.GizmoOBB(center, axes, half, cyan)
	}

	// The surface being shaped, if any: its wireframe, handles and drag arms.
	drawSurfaceEditOverlay()

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
func editor3DVertexPanel(lvl *world.Level) {
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
	if imgui.CollapsingHeaderV(title+"##vertexhdr", imgui.TreeNodeFlagsDefaultOpen) {
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
					_ = world.ReloadSector(ch, s)
				}
			}
			if primary.vertIdx < len(s.Ceiling.VertexOffsets) {
				cOff := s.Ceiling.VertexOffsets[primary.vertIdx]
				if editorDragFloat("Ceil Offset##v3dco", &cOff, -100, 100, "%.3f") {
					s.Ceiling.VertexOffsets[primary.vertIdx] = cOff
					_ = world.ReloadSector(ch, s)
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

// Editor3DPanel draws the 3D-mode ImGui side panel: Context, Objects,
// Properties, View, Editor — see editor_panel.go for the shared frame this and
// Editor2DPanel both draw through.
func Editor3DPanel() {
	lvl := Editor.level
	if lvl == nil {
		return
	}

	imgui.SetNextWindowPos(imgui.Vec2{X: float32(platform.WinWidth) - Editor.PanelWidth, Y: 28})
	imgui.SetNextWindowSize(imgui.Vec2{X: Editor.PanelWidth, Y: float32(platform.WinHeight) - 28})
	imgui.BeginV("3D Editor", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	editorPanelBody(lvl, panelKind3D)

	imgui.End()
	editorChunkPopups(lvl)
}

// createChunkAtCamera creates a minimal rectangular chunk at the camera's XZ position.
func createChunkAtCamera(id string) {
	lvl := Editor.level
	// Centre the new chunk on whatever the active sub-mode is looking at: the
	// fly camera in 3D, the map pan in 2D.
	cx := gfx.Cam.Position.X()
	cz := gfx.Cam.Position.Z()
	if Editor.Mode == Mode2D {
		// Pan2D is on the map's plane — the *currently active* chunk's local
		// space (editor2DAnchor) — and Origin below is world. Using the pan
		// raw would place the new chunk offset by the old chunk's placement.
		cx, cz = editor2DFromPlane(Editor.Pan2D.X(), Editor.Pan2D.Y())
	}
	halfSize := float32(5.0)

	// Points are built around the chunk's own local origin, like every other
	// chunk's authored geometry — the camera/pan position is carried by Origin
	// instead of baked into them.
	pts := []mgl32.Vec2{
		{-halfSize, -halfSize},
		{halfSize, -halfSize},
		{halfSize, halfSize},
		{-halfSize, halfSize},
	}
	floorH := float32(0.0)
	ceilH := float32(4.0)

	// Wall/Floor/Ceiling.Alpha and DoubleSided have no zero-value-safe default
	// like the YAML loader's float32Default/BoolDefault helpers apply — leaving
	// them unset here means Alpha stays 0 (fully transparent) and DoubleSided
	// stays false, so set them explicitly to match what a loaded level gets.
	_ = gfx.EnsureTextureLoaded("concrete")
	_ = gfx.EnsureTextureLoaded("stone_floor")

	walls := []world.Wall{
		{StartPoint: pts[0], EndPoint: pts[1], Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0},
		{StartPoint: pts[1], EndPoint: pts[2], Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0},
		{StartPoint: pts[2], EndPoint: pts[3], Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0},
		{StartPoint: pts[3], EndPoint: pts[0], Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0},
	}

	sector := world.Sector{
		ID:           id + "_room",
		PointIndices: []int{0, 1, 2, 3},
		EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
		Floor:        world.Floor{Enabled: true, Height: floorH, Texture: "stone_floor", DoubleSided: true, Alpha: 1.0},
		Ceiling:      world.Ceiling{Enabled: true, Height: ceilH, Texture: "concrete", DoubleSided: true, Alpha: 1.0},
		Walls:        walls,
	}

	chunk := world.Chunk{
		ID:         id,
		Origin:     mgl32.Vec3{cx, 0, cz},
		Points:     pts,
		Sectors:    []world.Sector{sector},
		MinBound:   mgl32.Vec3{-halfSize, floorH, -halfSize},
		MaxBound:   mgl32.Vec3{halfSize, ceilH, halfSize},
		PointNames: []string{"A", "B", "C", "D"},
	}
	_ = world.ReloadSector(&chunk, &sector)
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
		_ = world.ReloadSector(&clone, s)
	}
	clone.RebuildGaps()

	// A duplicated vehicle interior keeps riding the same vehicle (Chunk.Clone
	// copies FrameOwner), so the vehicle's own interior list has to name the
	// copy too — editorChunkFrameOwnerRow keeps those two halves in step for
	// every other binding and this one must not be the exception.
	if clone.FrameOwner != "" {
		if e := world.FindEntity(lvl, clone.FrameOwner); e != nil && e.Vehicle != nil {
			e.Vehicle.AddInterior(clone.ID)
		}
	}

	lvl.Chunks = append(lvl.Chunks, clone)
	Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
	// The new chunk needs bucketing, and a frame-owned one needs its Xform.
	if err := world.RebuildIndexes(lvl); err != nil {
		editorSetStatus("Duplicated %s, but frames are unhappy: %v", clone.ID, err)
	}
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

	// Release every GPU resource the chunk owns — the same pass that unloading
	// a disabled chunk runs, so a delete and a disable free exactly the same
	// set and neither can drift from the other. See world/chunk_enable.go.
	ch.ReleaseGeometry()

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
func uniqueChunkCopyID(lvl *world.Level, srcID string) (string, string) {
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
