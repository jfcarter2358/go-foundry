package main

// editor_3d.go — 3D fly-around sub-mode of the level editor.
//
// Handles:
//   - Entity selection via screen-space ray casting (AABB test)
//   - Entity placement from the catalog
//   - Chunk creation at camera position
//   - Gizmo drawing for selected objects

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
	"gopkg.in/yaml.v2"
)

// ---------- Entity catalog ----------

// EntityDef is a template read from resources/entities/*.yaml.
type EntityDef struct {
	ID         string  `yaml:"id"`
	Model      string  `yaml:"model"`   // path relative to executablePath
	Texture    string  `yaml:"texture"` // logical texture name
	Bounding   string  `yaml:"bounding"`
	BoundHalfX float32 `yaml:"bound_half_x"`
	BoundHalfY float32 `yaml:"bound_half_y"`
	BoundHalfZ float32 `yaml:"bound_half_z"`
}

// ScanEntityDefs reads all .yaml files in entitiesDir and returns their definitions.
func ScanEntityDefs(entitiesDir string) []EntityDef {
	var defs []EntityDef
	for _, name := range scanFilesByExt(entitiesDir, ".yaml") {
		data, err := os.ReadFile(filepath.Join(entitiesDir, name))
		if err != nil {
			continue
		}
		var def EntityDef
		if err := yaml.Unmarshal(data, &def); err != nil {
			continue
		}
		defs = append(defs, def)
	}
	return defs
}

// ---------- Ray casting ----------

// screenRay returns the world-space ray origin and normalised direction for the
// given screen pixel (sx, sy) using the current projection and camera matrices.
func screenRay(sx, sy int32) (origin, dir mgl32.Vec3) {
	// NDC
	ndcX := (float32(sx)/float32(WinWidth))*2.0 - 1.0
	ndcY := 1.0 - (float32(sy)/float32(WinHeight))*2.0

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

// rayAABBIntersect tests a ray against an AABB. Returns true and the hit
// distance if there is an intersection.
func rayAABBIntersect(origin, dir, aabbMin, aabbMax mgl32.Vec3) (bool, float32) {
	tmin := float32(math.Inf(-1))
	tmax := float32(math.Inf(1))
	for i := 0; i < 3; i++ {
		if math.Abs(float64(dir[i])) < 1e-6 {
			if origin[i] < aabbMin[i] || origin[i] > aabbMax[i] {
				return false, 0
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
			return false, 0
		}
	}
	if tmax < 0 {
		return false, 0
	}
	t := tmin
	if t < 0 {
		t = tmax
	}
	return true, t
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

// Editor3DHandleClick processes a mouse click in 3D mode (entity/sector selection or placement).
func Editor3DHandleClick(t sdl.MouseButtonEvent) {
	if t.Button != sdl.ButtonLeft {
		return
	}

	if Editor.PlacementMode {
		// 4m in front of the camera, base aligned to the floor.
		spawnPos := Cam.Position.Add(Cam.Direction.Mul(4.0))
		spawnPos[1] -= eyeHeight
		placeSelectedEntityAt(spawnPos)
		return
	}

	if Editor.PrimPlacementMode {
		spawnPos := Cam.Position.Add(Cam.Direction.Mul(4.0))
		spawnPos[1] -= eyeHeight
		placeSelectedPrimitiveAt(spawnPos)
		return
	}

	// Vertex handles win over world geometry: they are small, deliberate
	// targets drawn on top of everything. Repeated clicks on the same cluster
	// cycle through every vertex under the cursor; ctrl+click takes them all.
	// (Ctrl rather than shift — shift is held to fly downward.)
	ctrlHeld := sdl.GetModState()&sdl.KMOD_CTRL != 0
	if handleVertexClick(t.X, t.Y, ctrlHeld) {
		return
	}

	// Selection: one ray, closest hit wins across entities, primitives, and
	// sector faces.
	hit := pick3D(t.X, t.Y)

	// Start from a clean slate so no stale selection survives the click.
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = -1
	clearVertexSelection()
	Editor.ActiveSurface = ""
	Editor.ActiveEdgeKind = ""
	Editor.ActiveEdgeIdx = -1
	Editor.GizmoKind = gizmoKindNone

	switch hit.kind {
	case pickEntity:
		Editor.ActiveEntityIdx = hit.idx
		Editor.ActiveSectorIdx = -1
		Editor.GizmoKind = gizmoKindEntity
		Editor.PanelFocus = panelFocusEntity

	case pickPrimitive:
		Editor.ActiveChunkIdx = hit.chunkIdx
		Editor.ActivePrimitiveIdx = hit.idx
		Editor.ActiveSectorIdx = -1
		Editor.GizmoKind = gizmoKindPrimitive
		Editor.PanelFocus = panelFocusPrimitive

	case pickWall:
		Editor.ActiveChunkIdx = hit.chunkIdx
		Editor.ActiveSectorIdx = hit.sectorIdx
		Editor.ActiveSurface = "wall"
		Editor.ActiveEdgeKind = "wall"
		Editor.ActiveEdgeIdx = hit.idx
		Editor.PanelFocus = panelFocusWall

	case pickFloor:
		Editor.ActiveChunkIdx = hit.chunkIdx
		Editor.ActiveSectorIdx = hit.sectorIdx
		Editor.ActiveSurface = "floor"
		Editor.PanelFocus = panelFocusFloor

	case pickCeiling:
		Editor.ActiveChunkIdx = hit.chunkIdx
		Editor.ActiveSectorIdx = hit.sectorIdx
		Editor.ActiveSurface = "ceiling"
		Editor.PanelFocus = panelFocusCeiling

	default:
		// Click on empty space — deselect.
		Editor.ActiveSectorIdx = -1
		Editor.PanelFocus = panelFocusNone
	}
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
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		s.Floor.Height += dy
		s.Ceiling.Height += dy
		rebuildSectorEdgePoints(ch, s)
		_ = s.Load(SectorPoints(ch, s))
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

	// Highlight all entity AABBs (dim white)
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if !e.Visible {
			continue
		}
		col := white
		if i == Editor.ActiveEntityIdx {
			col = yellow
		}
		GizmoAABB(
			e.Position.Sub(mgl32.Vec3{e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ}),
			e.Position.Add(mgl32.Vec3{e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ}),
			col,
		)
	}

	// Highlight primitive AABBs
	green := mgl32.Vec3{0, 1, 0}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for pi := range ch.Primitives {
			p := &ch.Primitives[pi]
			col := white
			if ci == Editor.ActiveChunkIdx && pi == Editor.ActivePrimitiveIdx {
				col = green
			}
			GizmoAABB(
				p.Position.Sub(mgl32.Vec3{p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ}),
				p.Position.Add(mgl32.Vec3{p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ}),
				col,
			)
		}
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

	// Vertex handle boxes near the cursor plus the translate gizmo, drawn last
	// and without depth testing so a handle is always visible where it is
	// clickable.
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
					_ = s.Load(SectorPoints(ch, s))
				}
			}
			if primary.vertIdx < len(s.Ceiling.VertexOffsets) {
				cOff := s.Ceiling.VertexOffsets[primary.vertIdx]
				if editorDragFloat("Ceil Offset##v3dco", &cOff, -100, 100, "%.3f") {
					s.Ceiling.VertexOffsets[primary.vertIdx] = cOff
					_ = s.Load(SectorPoints(ch, s))
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

	imgui.SetNextWindowPos(imgui.Vec2{X: float32(WinWidth) - Editor.PanelWidth, Y: 28})
	imgui.SetNextWindowSize(imgui.Vec2{X: Editor.PanelWidth, Y: float32(WinHeight) - 28})
	imgui.BeginV("3D Editor", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	// ---- Grid settings ----
	editorGridSettings()

	// ---- Navigation buttons ----
	if imgui.CollapsingHeaderV("Navigation", imgui.TreeNodeFlagsDefaultOpen) {
		imgui.Text("Right-click drag to rotate")
		imgui.Text("Click a face, object or vertex to select it")
		imgui.Text("Drag an R/G/B arm to move it along X/Y/Z")
		imgui.Checkbox("Show All Point Boxes", &Editor.ShowPointSelectRadius)
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

	// --- Entity catalog ---
	editorEntityCatalogSection()

	// --- Primitive catalog ---
	editorPrimitiveCatalogSection()

	// --- Scenery import ---
	editorSceneryImportSection()

	// --- Selected primitive properties ---
	editorPrimitivePropertiesSection(lvl)

	// --- Selected entity properties ---
	editorEntityPropertiesSection(lvl)

	// --- Selected sector/edge properties (from 3D click) ---
	if Editor.ActiveEntityIdx < 0 && Editor.ActiveSectorIdx >= 0 &&
		Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
		imgui.Separator()
		sch := &lvl.Chunks[Editor.ActiveChunkIdx]
		if Editor.ActiveSectorIdx < len(sch.Sectors) {
			s3d := &sch.Sectors[Editor.ActiveSectorIdx]
			editorFocusOpen(panelFocusFloor, panelFocusCeiling, panelFocusWall)
			if imgui.CollapsingHeaderV("Sector: "+s3d.ID, imgui.TreeNodeFlagsDefaultOpen) {
				imgui.InputText("ID##sid3d", &s3d.ID)
				if Editor.ActiveSurface != "" {
					imgui.Text("Clicked face: " + Editor.ActiveSurface)
				}
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
						_ = s3d.Load(SectorPoints(sch, s3d))
					}
					imgui.TreePop()
				}
				if imgui.Button("Edit Floor Surface##floorSurf3d") {
					EnterSurface2D("floor")
				}
				if editorUVControls("Floor", &s3d.Floor.UVOffset, &s3d.Floor.UVScale, &s3d.Floor.UVRotation) {
					_ = s3d.Load(SectorPoints(sch, s3d))
				}
				editorSurfaceExtrusionPanel(sch, s3d, "Floor", &s3d.Floor.Extrusions)
				editorTerrainPanel(sch, s3d, "Floor", &s3d.Floor.Terrain, &s3d.Floor.TerrainPoints)
				if editorSmoothPanel("Floor", &s3d.Floor.Smooth, &s3d.Floor.SmoothAngle) {
					_ = s3d.Load(SectorPoints(sch, s3d))
				}
				editorDetailPanel("Floor", &s3d.Floor.Detail)
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
						_ = s3d.Load(SectorPoints(sch, s3d))
					}
					imgui.TreePop()
				}
				if imgui.Button("Edit Ceiling Surface##ceilSurf3d") {
					EnterSurface2D("ceiling")
				}
				if editorUVControls("Ceiling", &s3d.Ceiling.UVOffset, &s3d.Ceiling.UVScale, &s3d.Ceiling.UVRotation) {
					_ = s3d.Load(SectorPoints(sch, s3d))
				}
				editorSurfaceExtrusionPanel(sch, s3d, "Ceiling", &s3d.Ceiling.Extrusions)
				editorTerrainPanel(sch, s3d, "Ceiling", &s3d.Ceiling.Terrain, &s3d.Ceiling.TerrainPoints)
				if editorSmoothPanel("Ceiling", &s3d.Ceiling.Smooth, &s3d.Ceiling.SmoothAngle) {
					_ = s3d.Load(SectorPoints(sch, s3d))
				}
				editorDetailPanel("Ceiling", &s3d.Ceiling.Detail)
				if Editor.ActiveEdgeIdx >= 0 {
					imgui.Separator()
					editorEdgeKindCombo(sch, s3d, "3d")
				}
				if Editor.ActiveEdgeKind == "wall" && Editor.ActiveEdgeIdx >= 0 &&
					Editor.ActiveEdgeIdx < len(s3d.Walls) {
					imgui.Separator()
					editorFocusScroll(panelFocusWall)
					w := &s3d.Walls[Editor.ActiveEdgeIdx]
					imgui.Text(fmt.Sprintf("Wall #%d", Editor.ActiveEdgeIdx))
					changed := false
					changed = textureCombo("Texture##wt3d", &w.Texture, false) || changed
					changed = imgui.Checkbox("Collision##wc3d", &w.Collision) || changed
					changed = imgui.Checkbox("Visible##wv3d", &w.Visible) || changed
					changed = imgui.Checkbox("Double Sided##wds3d", &w.DoubleSided) || changed
					changed = editorDragFloat("Alpha##wa3d", &w.Alpha, 0, 1, "%.2f") || changed
					if changed {
						_ = s3d.Load(SectorPoints(sch, s3d))
					}
					// Surface 2D editor button
					if imgui.Button("Edit Wall Surface##wallSurf3d") {
						EnterSurface2D("wall")
					}
					if editorUVControls("Wall", &w.UVOffset, &w.UVScale, &w.UVRotation) {
						_ = s3d.Load(SectorPoints(sch, s3d))
					}
					// Extrusion sub-panel
					editorExtrusionPanel(sch, s3d, w)
					// Terrain sub-panel
					editorTerrainPanel(sch, s3d, "Wall", &w.Terrain, &w.TerrainPoints)
					// Smooth shading (rebuilds the wall: normals are made at upload)
					if editorSmoothPanel("Wall", &w.Smooth, &w.SmoothAngle) {
						_ = s3d.Load(SectorPoints(sch, s3d))
					}
					// Detail sub-panel
					editorDetailPanel("Wall", &w.Detail)
				} else if Editor.ActiveEdgeKind == "portal" && Editor.ActiveEdgeIdx >= 0 &&
					Editor.ActiveEdgeIdx < len(s3d.Portals) {
					imgui.Separator()
					p3d := &s3d.Portals[Editor.ActiveEdgeIdx]
					imgui.Text(fmt.Sprintf("Portal #%d", Editor.ActiveEdgeIdx))
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
						_ = s3d.Load(SectorPoints(sch, s3d))
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

// reloadPrimitive regenerates the geometry and bounding volumes for a primitive
// after its type, scale, or side count changes.
func reloadPrimitive(ch *Chunk, idx int) {
	p := &ch.Primitives[idx]
	p.Geometry.Delete()

	// Switching an existing primitive to water needs a usable starting look.
	if p.Kind == PrimitiveWater && !p.Water.Enabled {
		p.Water = DefaultWaterParams()
	}

	// Recompute bounding volumes from type + scale
	scale := p.Scale
	if scale == (mgl32.Vec3{}) {
		scale = mgl32.Vec3{1, 1, 1}
	}
	switch p.Kind {
	case PrimitiveRectPrism:
		p.BoundingKind = BoundingAABB
		p.BoundHalfX = scale.X() / 2
		p.BoundHalfY = scale.Y() / 2
		p.BoundHalfZ = scale.Z() / 2
	case PrimitiveSphere:
		p.BoundingKind = BoundingSphere
		maxS := scale.X()
		if scale.Y() > maxS {
			maxS = scale.Y()
		}
		if scale.Z() > maxS {
			maxS = scale.Z()
		}
		p.BoundHalfX = maxS / 2
		p.BoundHalfY = maxS / 2
		p.BoundHalfZ = maxS / 2
	case PrimitiveCylinder:
		p.BoundingKind = BoundingCylinder
		maxXZ := scale.X()
		if scale.Z() > maxXZ {
			maxXZ = scale.Z()
		}
		p.BoundHalfX = maxXZ / 2
		p.BoundHalfY = scale.Y() / 2
		p.BoundHalfZ = maxXZ / 2
	case PrimitivePlane, PrimitiveWater:
		p.BoundingKind = BoundingAABB
		p.BoundHalfX = scale.X() / 2
		p.BoundHalfY = 0.01
		p.BoundHalfZ = scale.Z() / 2
	}

	_ = p.Load()
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
	_ = sector.Load(SectorPoints(&chunk, &sector))
	chunk.Sectors[0] = sector

	lvl.Chunks = append(lvl.Chunks, chunk)
	Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
}

// duplicateActiveChunk deep-copies the currently active chunk, reuploads all
// GPU geometry, and makes the copy the new active chunk. The new chunk keeps
// the same world-space position as the source; the user can move it via the
// Chunk Properties Origin drag. Sector and primitive IDs are suffixed with
// _copy (or _copyN) to stay unique within the level.
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
		_ = s.Load(SectorPoints(&clone, s))
	}
	for pi := range clone.Primitives {
		_ = clone.Primitives[pi].Load()
	}
	clone.RebuildGaps()

	lvl.Chunks = append(lvl.Chunks, clone)
	Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
}

// deleteActiveChunk removes the currently active chunk from the level after
// releasing all of its GPU resources (sector walls, floor/ceiling, portals,
// extrusion geometries, and primitives).
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
	for pi := range ch.Primitives {
		ch.Primitives[pi].Geometry.Delete()
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
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = -1
}

// uniqueChunkCopyID returns a chunk ID + suffix pair that does not collide
// with any existing chunk in lvl. The suffix ("_copy", "_copy2", ...) is
// reused for nested sector/primitive IDs so they stay consistent.
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
