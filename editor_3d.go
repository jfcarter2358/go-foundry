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
	"strings"

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
	entries, err := os.ReadDir(entitiesDir)
	if err != nil {
		return nil
	}
	var defs []EntityDef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(entitiesDir, e.Name()))
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
// Returns true and the distance t if the ray hits the triangle in front of the origin.
func rayTriangleIntersect(origin, dir, v0, v1, v2 mgl32.Vec3) (bool, float32) {
	const eps = 1e-6
	edge1 := v1.Sub(v0)
	edge2 := v2.Sub(v0)
	h := dir.Cross(edge2)
	a := edge1.Dot(h)
	if a > -eps && a < eps {
		return false, 0
	}
	f := 1.0 / a
	sv := origin.Sub(v0)
	u := f * sv.Dot(h)
	if u < 0 || u > 1 {
		return false, 0
	}
	q := sv.Cross(edge1)
	v := f * dir.Dot(q)
	if v < 0 || u+v > 1 {
		return false, 0
	}
	t := f * edge2.Dot(q)
	if t < eps {
		return false, 0
	}
	return true, t
}

// Editor3DHandleClick processes a mouse click in 3D mode (entity/sector selection or placement).
func Editor3DHandleClick(t sdl.MouseButtonEvent) {
	if t.Button != sdl.ButtonLeft {
		return
	}
	lvl := Editor.level

	if Editor.PlacementMode {
		// Place selected entity definition at 4m in front of camera
		Editor.PlacementMode = false
		if len(Editor.EntityDefs) == 0 || Editor.SelectedDefIdx < 0 {
			return
		}
		def := Editor.EntityDefs[Editor.SelectedDefIdx]
		spawnPos := Cam.Position.Add(Cam.Direction.Mul(4.0))
		spawnPos[1] -= eyeHeight // align base to floor

		scale := mgl32.Vec3{1, 1, 1}
		entity := Entity{
			ID:          fmt.Sprintf("%s_%d", def.ID, len(lvl.Entities)),
			Position:    spawnPos,
			Scale:       scale,
			ModelPath:   executablePath + "/" + def.Model,
			TexturePath: def.Texture,
			BoundHalfX:  def.BoundHalfX,
			BoundHalfY:  def.BoundHalfY,
			BoundHalfZ:  def.BoundHalfZ,
			Visible:     def.Model != "",
		}
		switch def.Bounding {
		case "sphere":
			entity.BoundingKind = BoundingSphere
		case "cylinder":
			entity.BoundingKind = BoundingCylinder
		default:
			entity.BoundingKind = BoundingAABB
		}
		if err := entity.Load(); err == nil {
			lvl.Entities = append(lvl.Entities, entity)
			Editor.ActiveEntityIdx = len(lvl.Entities) - 1
		}
		return
	}

	// Selection: find closest hit among entities and sector geometry
	origin, dir := screenRay(t.X, t.Y)
	bestT := float32(math.MaxFloat32)
	bestEntityIdx := -1
	bestChunkIdx := -1
	bestSectorIdx := -1
	bestEdgeKind := ""
	bestEdgeIdx := -1

	// Test entity AABBs
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		bmin := e.Position.Sub(mgl32.Vec3{e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ})
		bmax := e.Position.Add(mgl32.Vec3{e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ})
		if hit, ht := rayAABBIntersect(origin, dir, bmin, bmax); hit && ht < bestT {
			bestT = ht
			bestEntityIdx = i
			bestChunkIdx = -1
			bestSectorIdx = -1
			bestEdgeKind = ""
			bestEdgeIdx = -1
		}
	}

	// Test sector geometry (walls, portals, floors, ceilings)
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for seci := range ch.Sectors {
			s := &ch.Sectors[seci]
			sPts := SectorPoints(ch, s)
			// Walls — iterate all triangles in the wall's vertex buffer
			for wi := range s.Walls {
				w := &s.Walls[wi]
				verts := w.Geometry.Vertices
				// Each triangle = 3 vertices × 5 floats = 15 floats
				ht := float32(math.MaxFloat32)
				for ti := 0; ti+14 < len(verts); ti += 15 {
					v0 := mgl32.Vec3{verts[ti+0], verts[ti+1], verts[ti+2]}
					v1 := mgl32.Vec3{verts[ti+5], verts[ti+6], verts[ti+7]}
					v2 := mgl32.Vec3{verts[ti+10], verts[ti+11], verts[ti+12]}
					if h, t2 := rayTriangleIntersect(origin, dir, v0, v1, v2); h && t2 < ht {
						ht = t2
					}
				}
				if ht < bestT {
					bestT = ht
					bestEntityIdx = -1
					bestChunkIdx = ci
					bestSectorIdx = seci
					bestEdgeKind = "wall"
					bestEdgeIdx = wi
				}
			}
			// Portals — skip hit-testing so clicks pass through to
			// walls in adjacent chunks behind the portal opening.
			// Floor triangles
			if s.Floor.Enabled && len(sPts) >= 3 {
				for i := 1; i < len(sPts)-1; i++ {
					v0 := mgl32.Vec3{sPts[0].X(), s.Floor.VertexHeight(0), sPts[0].Y()}
					v1 := mgl32.Vec3{sPts[i].X(), s.Floor.VertexHeight(i), sPts[i].Y()}
					v2 := mgl32.Vec3{sPts[i+1].X(), s.Floor.VertexHeight(i+1), sPts[i+1].Y()}
					if h, ht := rayTriangleIntersect(origin, dir, v0, v1, v2); h && ht < bestT {
						bestT = ht
						bestEntityIdx = -1
						bestChunkIdx = ci
						bestSectorIdx = seci
						bestEdgeKind = ""
						bestEdgeIdx = -1
					}
				}
			}
			// Ceiling triangles
			if s.Ceiling.Enabled && len(sPts) >= 3 {
				for i := len(sPts) - 2; i >= 1; i-- {
					v0 := mgl32.Vec3{sPts[0].X(), s.Ceiling.VertexHeight(0), sPts[0].Y()}
					v1 := mgl32.Vec3{sPts[i+1].X(), s.Ceiling.VertexHeight(i+1), sPts[i+1].Y()}
					v2 := mgl32.Vec3{sPts[i].X(), s.Ceiling.VertexHeight(i), sPts[i].Y()}
					if h, ht := rayTriangleIntersect(origin, dir, v0, v1, v2); h && ht < bestT {
						bestT = ht
						bestEntityIdx = -1
						bestChunkIdx = ci
						bestSectorIdx = seci
						bestEdgeKind = ""
						bestEdgeIdx = -1
					}
				}
			}
		}
	}

	Editor.ActiveEntityIdx = bestEntityIdx
	if bestSectorIdx >= 0 {
		Editor.ActiveChunkIdx = bestChunkIdx
		Editor.ActiveSectorIdx = bestSectorIdx
		Editor.ActiveEdgeKind = bestEdgeKind
		Editor.ActiveEdgeIdx = bestEdgeIdx
	} else if bestEntityIdx < 0 {
		// Click on empty space — deselect sector
		Editor.ActiveSectorIdx = -1
		Editor.ActiveEdgeIdx = -1
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
}

// ---------- ImGui panels for 3D mode ----------

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
	if imgui.CollapsingHeader("Entity Catalog") {
		for i, def := range Editor.EntityDefs {
			selected := i == Editor.SelectedDefIdx
			if imgui.SelectableV(def.ID, selected, 0, imgui.Vec2{}) {
				Editor.SelectedDefIdx = i
			}
		}
		if imgui.Button("Place Selected") && Editor.SelectedDefIdx >= 0 {
			Editor.PlacementMode = true
		}
		if Editor.PlacementMode {
			imgui.SameLine()
			imgui.Text("Click to place...")
		}
	}

	// --- Selected entity properties ---
	if Editor.ActiveEntityIdx >= 0 && Editor.ActiveEntityIdx < len(lvl.Entities) {
		imgui.Separator()
		if imgui.CollapsingHeader("Entity Properties") {
			e := &lvl.Entities[Editor.ActiveEntityIdx]
			imgui.Text(fmt.Sprintf("ID: %s", e.ID))

			pos := [3]float32{e.Position.X(), e.Position.Y(), e.Position.Z()}
			if editorDragFloat3("Position", &pos, 0, 0, "%.3f") {
				e.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
			}
			rot := [3]float32{e.Rotation.X(), e.Rotation.Y(), e.Rotation.Z()}
			if editorDragFloat3("Rotation", &rot, -math.Pi, math.Pi, "%.2f") {
				e.Rotation = mgl32.Vec3{rot[0], rot[1], rot[2]}
			}
			sc := [3]float32{e.Scale.X(), e.Scale.Y(), e.Scale.Z()}
			if editorDragFloat3("Scale", &sc, 0.01, 100, "%.2f") {
				e.Scale = mgl32.Vec3{sc[0], sc[1], sc[2]}
			}

			imgui.Text(fmt.Sprintf("Model: %s", filepath.Base(e.ModelPath)))
			imgui.Text(fmt.Sprintf("Texture: %s", e.TexturePath))
		}
	}

	// --- Selected sector/edge properties (from 3D click) ---
	if Editor.ActiveEntityIdx < 0 && Editor.ActiveSectorIdx >= 0 &&
		Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
		imgui.Separator()
		sch := &lvl.Chunks[Editor.ActiveChunkIdx]
		if Editor.ActiveSectorIdx < len(sch.Sectors) {
			s3d := &sch.Sectors[Editor.ActiveSectorIdx]
			if imgui.CollapsingHeaderV("Sector: "+s3d.ID, imgui.TreeNodeFlagsDefaultOpen) {
				imgui.InputText("ID##sid3d", &s3d.ID)
				if imgui.TreeNode("Floor##3df") {
					changed := false
					changed = imgui.Checkbox("Enabled##fe3d", &s3d.Floor.Enabled) || changed
					changed = editorDragFloat("Height##fh3d", &s3d.Floor.Height, 0, 0, "%.3f") || changed
					changed = imgui.InputText("Texture##ft3d", &s3d.Floor.Texture) || changed
					if changed {
						_ = s3d.Load(SectorPoints(sch, s3d))
					}
					imgui.TreePop()
				}
				if imgui.Button("Edit Floor Surface##floorSurf3d") {
					EnterSurface2D("floor")
				}
				editorSurfaceExtrusionPanel(sch, s3d, "Floor", &s3d.Floor.Extrusions)
				editorTerrainPanel(sch, s3d, "Floor", &s3d.Floor.Terrain, &s3d.Floor.TerrainPoints)
				if imgui.TreeNode("Ceiling##3dc") {
					changed := false
					changed = imgui.Checkbox("Enabled##ce3d", &s3d.Ceiling.Enabled) || changed
					changed = editorDragFloat("Height##ch3d", &s3d.Ceiling.Height, 0, 0, "%.3f") || changed
					changed = imgui.InputText("Texture##ct3d", &s3d.Ceiling.Texture) || changed
					if changed {
						_ = s3d.Load(SectorPoints(sch, s3d))
					}
					imgui.TreePop()
				}
				if imgui.Button("Edit Ceiling Surface##ceilSurf3d") {
					EnterSurface2D("ceiling")
				}
				editorSurfaceExtrusionPanel(sch, s3d, "Ceiling", &s3d.Ceiling.Extrusions)
				editorTerrainPanel(sch, s3d, "Ceiling", &s3d.Ceiling.Terrain, &s3d.Ceiling.TerrainPoints)
				if Editor.ActiveEdgeKind == "wall" && Editor.ActiveEdgeIdx >= 0 &&
					Editor.ActiveEdgeIdx < len(s3d.Walls) {
					imgui.Separator()
					w := &s3d.Walls[Editor.ActiveEdgeIdx]
					imgui.Text(fmt.Sprintf("Wall #%d", Editor.ActiveEdgeIdx))
					changed := false
					changed = imgui.InputText("Texture##wt3d", &w.Texture) || changed
					changed = imgui.Checkbox("Collision##wc3d", &w.Collision) || changed
					changed = imgui.Checkbox("Visible##wv3d", &w.Visible) || changed
					changed = imgui.Checkbox("Double Sided##wds3d", &w.DoubleSided) || changed
					if changed {
						_ = s3d.Load(SectorPoints(sch, s3d))
					}
					// Surface 2D editor button
					if imgui.Button("Edit Wall Surface##wallSurf3d") {
						EnterSurface2D("wall")
					}
					// Extrusion sub-panel
					editorExtrusionPanel(sch, s3d, w)
					// Terrain sub-panel
					editorTerrainPanel(sch, s3d, "Wall", &w.Terrain, &w.TerrainPoints)
				} else if Editor.ActiveEdgeKind == "portal" && Editor.ActiveEdgeIdx >= 0 &&
					Editor.ActiveEdgeIdx < len(s3d.Portals) {
					imgui.Separator()
					p3d := &s3d.Portals[Editor.ActiveEdgeIdx]
					imgui.Text(fmt.Sprintf("Portal #%d", Editor.ActiveEdgeIdx))
					changed := false
					changed = imgui.InputText("To Sector##pts3d", &p3d.ToSector) || changed
					changed = imgui.Checkbox("Draw Gap##pdg3d", &p3d.DrawGap) || changed
					if p3d.DrawGap {
						changed = imgui.InputText("Gap Texture##pgt3d", &p3d.GapTexture) || changed
					}
					changed = imgui.Checkbox("Visible##pv3d", &p3d.Visible) || changed
					if changed {
						_ = s3d.Load(SectorPoints(sch, s3d))
						sch.RebuildGaps()
					}
				} else {
					imgui.Text("Floor/Ceiling — switch to 2D to edit vertices")
				}
			}
		}
	}

	// --- Chunk list ---
	imgui.Separator()
	if imgui.CollapsingHeader("Chunks") {
		for i := range lvl.Chunks {
			ch := &lvl.Chunks[i]
			selected := i == Editor.ActiveChunkIdx
			if imgui.SelectableV(ch.ID, selected, 0, imgui.Vec2{}) {
				Editor.ActiveChunkIdx = i
				Editor.ActiveEntityIdx = -1
			}
		}
		if imgui.Button("New Chunk") {
			Editor.ShowNewChunk = true
		}
	}

	// --- Selected chunk properties ---
	if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
		imgui.Separator()
		if imgui.CollapsingHeader("Chunk Properties") {
			ch := &lvl.Chunks[Editor.ActiveChunkIdx]
			imgui.Text(fmt.Sprintf("ID: %s", ch.ID))
			imgui.Text(fmt.Sprintf("Sectors: %d", len(ch.Sectors)))

			orig := [3]float32{ch.Origin.X(), ch.Origin.Y(), ch.Origin.Z()}
			if editorDragFloat3("Origin", &orig, 0, 0, "%.3f") {
				moveChunk(ch, mgl32.Vec3{orig[0], orig[1], orig[2]})
			}
			editorDragFloat("Rotation##chunkRot", &ch.Rotation, 0, 0, "%.3f")
			sc := ch.Scale
			if sc == 0 {
				sc = 1
			}
			if editorDragFloat("Scale##chunkScale", &sc, 0.01, 100, "%.2f") {
				ch.Scale = sc
			}

			if imgui.Button("Switch to Sector Editor") {
				Editor.Mode = Mode2D
				Editor.Pan2D = mgl32.Vec2{
					(ch.MinBound.X() + ch.MaxBound.X()) / 2,
					(ch.MinBound.Z() + ch.MaxBound.Z()) / 2,
				}
			}
		}
	}

	// --- Light editor ---
	imgui.Separator()
	EditorLightPanel()

	imgui.End()

	// New chunk popup
	if Editor.ShowNewChunk {
		imgui.OpenPopup("New Chunk")
		Editor.ShowNewChunk = false
	}
	if imgui.BeginPopupModalV("New Chunk", nil, imgui.WindowFlagsAlwaysAutoResize) {
		imgui.InputText("Chunk ID", &Editor.NewChunkID)
		if imgui.Button("Create") && Editor.NewChunkID != "" {
			createChunkAtCamera(Editor.NewChunkID)
			Editor.NewChunkID = ""
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel") {
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}
}

// createChunkAtCamera creates a minimal rectangular chunk at the camera's XZ position.
func createChunkAtCamera(id string) {
	lvl := Editor.level
	cx := Cam.Position.X()
	cz := Cam.Position.Z()
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

	walls := []Wall{
		{StartPoint: pts[0], EndPoint: pts[1], Texture: "concrete", Collision: true, Visible: true},
		{StartPoint: pts[1], EndPoint: pts[2], Texture: "concrete", Collision: true, Visible: true},
		{StartPoint: pts[2], EndPoint: pts[3], Texture: "concrete", Collision: true, Visible: true},
		{StartPoint: pts[3], EndPoint: pts[0], Texture: "concrete", Collision: true, Visible: true},
	}

	sector := Sector{
		ID:           id + "_room",
		PointIndices: []int{0, 1, 2, 3},
		EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
		Floor:        Floor{Enabled: true, Height: floorH, Texture: "stone_floor"},
		Ceiling:      Ceiling{Enabled: true, Height: ceilH, Texture: "concrete"},
		Walls:        walls,
	}

	chunk := Chunk{
		ID:       id,
		Points:   pts,
		Sectors:  []Sector{sector},
		MinBound: mgl32.Vec3{cx - halfSize, floorH, cz - halfSize},
		MaxBound: mgl32.Vec3{cx + halfSize, ceilH, cz + halfSize},
		PointNames: map[string]mgl32.Vec2{
			"A": pts[0], "B": pts[1], "C": pts[2], "D": pts[3],
		},
	}
	_ = sector.Load(SectorPoints(&chunk, &sector))
	chunk.Sectors[0] = sector

	lvl.Chunks = append(lvl.Chunks, chunk)
	Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
}
