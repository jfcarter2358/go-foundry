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

	if Editor.PrimPlacementMode {
		// Place a new primitive 4m in front of camera
		Editor.PrimPlacementMode = false
		if Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
			return
		}
		ch := &lvl.Chunks[Editor.ActiveChunkIdx]
		spawnPos := Cam.Position.Add(Cam.Direction.Mul(4.0))
		spawnPos[1] -= eyeHeight

		var kind PrimitiveType
		switch Editor.PrimPlacementType {
		case "sphere":
			kind = PrimitiveSphere
		case "cylinder":
			kind = PrimitiveCylinder
		case "plane":
			kind = PrimitivePlane
		default:
			kind = PrimitiveRectPrism
		}

		prim := Primitive{
			ID:       fmt.Sprintf("prim_%d", len(ch.Primitives)),
			Kind:     kind,
			Position: spawnPos,
			Scale:    mgl32.Vec3{1, 1, 1},
			Texture:  "concrete",
			Sides:    16,
			ChunkID:  ch.ID,
		}

		// Compute bounding volumes
		switch kind {
		case PrimitiveRectPrism:
			prim.BoundingKind = BoundingAABB
			prim.BoundHalfX = 0.5
			prim.BoundHalfY = 0.5
			prim.BoundHalfZ = 0.5
		case PrimitiveSphere:
			prim.BoundingKind = BoundingSphere
			prim.BoundHalfX = 0.5
			prim.BoundHalfY = 0.5
			prim.BoundHalfZ = 0.5
		case PrimitiveCylinder:
			prim.BoundingKind = BoundingCylinder
			prim.BoundHalfX = 0.5
			prim.BoundHalfY = 0.5
			prim.BoundHalfZ = 0.5
		case PrimitivePlane:
			prim.BoundingKind = BoundingAABB
			prim.BoundHalfX = 0.5
			prim.BoundHalfY = 0.01
			prim.BoundHalfZ = 0.5
		}

		if err := prim.Load(); err == nil {
			ch.Primitives = append(ch.Primitives, prim)
			Editor.ActivePrimitiveIdx = len(ch.Primitives) - 1
			Editor.ActiveEntityIdx = -1
			rebuildPrimitiveIndices(ch)
		}
		return
	}

	// Selection: find closest hit among entities and sector geometry
	origin, dir := screenRay(t.X, t.Y)
	bestT := float32(math.MaxFloat32)
	bestEntityIdx := -1
	bestPrimitiveIdx := -1
	bestPrimitiveChunkIdx := -1
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

	// Test primitive AABBs
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for pi := range ch.Primitives {
			p := &ch.Primitives[pi]
			bmin := p.Position.Sub(mgl32.Vec3{p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ})
			bmax := p.Position.Add(mgl32.Vec3{p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ})
			if hit, ht := rayAABBIntersect(origin, dir, bmin, bmax); hit && ht < bestT {
				bestT = ht
				bestPrimitiveIdx = pi
				bestPrimitiveChunkIdx = ci
				bestEntityIdx = -1
				bestChunkIdx = -1
				bestSectorIdx = -1
				bestEdgeKind = ""
				bestEdgeIdx = -1
			}
		}
	}

	// Test sector geometry (walls, portals, floors, ceilings)
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for seci := range ch.Sectors {
			s := &ch.Sectors[seci]
			sPts := SectorPoints(ch, s)
			// Walls — iterate all triangles in the wall's vertex buffer.
			// Track the closest hit per wall using effective distance (back
			// faces are biased outward by pickFaceBias) so coincident walls at
			// chunk boundaries deterministically resolve in favor of the
			// front-facing one the user actually sees.
			for wi := range s.Walls {
				w := &s.Walls[wi]
				verts := w.Geometry.Vertices
				// Each triangle = 3 vertices × 5 floats = 15 floats
				bestWallEff := float32(math.MaxFloat32)
				for ti := 0; ti+14 < len(verts); ti += 15 {
					v0 := mgl32.Vec3{verts[ti+0], verts[ti+1], verts[ti+2]}
					v1 := mgl32.Vec3{verts[ti+5], verts[ti+6], verts[ti+7]}
					v2 := mgl32.Vec3{verts[ti+10], verts[ti+11], verts[ti+12]}
					h, t2, front := rayTriangleIntersect(origin, dir, v0, v1, v2)
					if !h {
						continue
					}
					eff := effectivePickT(t2, front)
					if eff < bestWallEff {
						bestWallEff = eff
					}
				}
				if bestWallEff < bestT {
					bestT = bestWallEff
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
					if h, ht, front := rayTriangleIntersect(origin, dir, v0, v1, v2); h {
						if eff := effectivePickT(ht, front); eff < bestT {
							bestT = eff
							bestEntityIdx = -1
							bestChunkIdx = ci
							bestSectorIdx = seci
							bestEdgeKind = ""
							bestEdgeIdx = -1
						}
					}
				}
			}
			// Ceiling triangles
			if s.Ceiling.Enabled && len(sPts) >= 3 {
				for i := len(sPts) - 2; i >= 1; i-- {
					v0 := mgl32.Vec3{sPts[0].X(), s.Ceiling.VertexHeight(0), sPts[0].Y()}
					v1 := mgl32.Vec3{sPts[i+1].X(), s.Ceiling.VertexHeight(i+1), sPts[i+1].Y()}
					v2 := mgl32.Vec3{sPts[i].X(), s.Ceiling.VertexHeight(i), sPts[i].Y()}
					if h, ht, front := rayTriangleIntersect(origin, dir, v0, v1, v2); h {
						if eff := effectivePickT(ht, front); eff < bestT {
							bestT = eff
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
	}

	Editor.ActiveEntityIdx = bestEntityIdx
	Editor.ActivePrimitiveIdx = -1
	if bestPrimitiveIdx >= 0 {
		Editor.ActivePrimitiveIdx = bestPrimitiveIdx
		Editor.ActiveChunkIdx = bestPrimitiveChunkIdx
		Editor.ActiveEntityIdx = -1
		Editor.ActiveSectorIdx = -1
		Editor.ActiveEdgeIdx = -1
	} else if bestSectorIdx >= 0 {
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

	// --- Primitive catalog ---
	if imgui.CollapsingHeader("Primitive Catalog") {
		primTypes := []string{"rect_prism", "sphere", "cylinder", "plane"}
		for _, pt := range primTypes {
			selected := pt == Editor.PrimPlacementType
			if imgui.SelectableV(pt, selected, 0, imgui.Vec2{}) {
				Editor.PrimPlacementType = pt
			}
		}
		if imgui.Button("Place Primitive") && Editor.PrimPlacementType != "" {
			Editor.PrimPlacementMode = true
			Editor.PlacementMode = false
		}
		if Editor.PrimPlacementMode {
			imgui.SameLine()
			imgui.Text("Click to place...")
		}
	}

	// --- Scenery import ---
	if imgui.CollapsingHeader("Scenery") {
		if imgui.Button("Import Scenery") {
			Editor.ImportSceneryFiles = nil
			Editor.ImportSceneryIdx = 0
			Editor.ShowImportScenery = true
		}
		importSceneryPopup()
	}

	// --- Selected primitive properties ---
	if Editor.ActivePrimitiveIdx >= 0 && Editor.ActiveChunkIdx >= 0 &&
		Editor.ActiveChunkIdx < len(lvl.Chunks) {
		ch := &lvl.Chunks[Editor.ActiveChunkIdx]
		if Editor.ActivePrimitiveIdx < len(ch.Primitives) {
			imgui.Separator()
			if imgui.CollapsingHeaderV("Primitive Properties", imgui.TreeNodeFlagsDefaultOpen) {
				p := &ch.Primitives[Editor.ActivePrimitiveIdx]
				imgui.InputText("ID##primid", &p.ID)

				typeNames := []string{"rect_prism", "sphere", "cylinder", "plane"}
				currentType := int(p.Kind)
				if imgui.BeginCombo("Type##primtype", typeNames[currentType]) {
					for i, tn := range typeNames {
						if imgui.SelectableV(tn, i == currentType, 0, imgui.Vec2{}) {
							p.Kind = PrimitiveType(i)
							reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
						}
					}
					imgui.EndCombo()
				}

				pos := [3]float32{p.Position.X(), p.Position.Y(), p.Position.Z()}
				if editorDragFloat3("Position##prim", &pos, 0, 0, "%.3f") {
					p.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
					rebuildPrimitiveIndices(ch)
				}
				rot := [3]float32{
					p.Rotation.X() * 180 / math.Pi,
					p.Rotation.Y() * 180 / math.Pi,
					p.Rotation.Z() * 180 / math.Pi,
				}
				if editorDragFloat3("Rotation (deg)##prim", &rot, -360, 360, "%.1f") {
					p.Rotation = mgl32.Vec3{
						rot[0] * math.Pi / 180,
						rot[1] * math.Pi / 180,
						rot[2] * math.Pi / 180,
					}
				}
				sc := [3]float32{p.Scale.X(), p.Scale.Y(), p.Scale.Z()}
				if editorDragFloat3("Scale##prim", &sc, 0.01, 100, "%.2f") {
					p.Scale = mgl32.Vec3{sc[0], sc[1], sc[2]}
					reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
				}

				imgui.InputText("Texture##primtex", &p.Texture)

				if p.Kind == PrimitiveCylinder {
					sides := int32(p.Sides)
					if imgui.DragIntV("Sides##primsides", &sides, 1, 3, 64, "%d", 0) {
						p.Sides = int(sides)
						reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
					}
				}

				imgui.Checkbox("Collision##primcol", &p.Collision)
				if editorDragFloat("Alpha##prima", &p.Alpha, 0, 1, "%.2f") {
					p.Geometry.Alpha = p.Alpha
				}

				if imgui.CollapsingHeader("Texture UV##primUVhdr") {
					uvChanged := false
					uvOff := [2]float32{p.UVOffset.X(), p.UVOffset.Y()}
					if editorDragFloat("UV Offset U##primuvou", &uvOff[0], 0, 0, "%.3f") ||
						editorDragFloat("UV Offset V##primuvov", &uvOff[1], 0, 0, "%.3f") {
						p.UVOffset = mgl32.Vec2{uvOff[0], uvOff[1]}
						uvChanged = true
					}
					uvSc := p.UVScale
					if uvSc == (mgl32.Vec2{}) {
						uvSc = mgl32.Vec2{1, 1}
					}
					uvScArr := [2]float32{uvSc.X(), uvSc.Y()}
					if editorDragFloat("UV Scale U##primuvsu", &uvScArr[0], 0.01, 100, "%.3f") ||
						editorDragFloat("UV Scale V##primuvsv", &uvScArr[1], 0.01, 100, "%.3f") {
						p.UVScale = mgl32.Vec2{uvScArr[0], uvScArr[1]}
						uvChanged = true
					}
					uvRotDeg := p.UVRotation * 180 / math.Pi
					if editorDragFloat("UV Rotation (deg)##primuvrot", &uvRotDeg, -360, 360, "%.1f") {
						p.UVRotation = uvRotDeg * math.Pi / 180
						uvChanged = true
					}
					if uvChanged {
						reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
					}
				}

				if imgui.Button("Delete Primitive") {
					ch.Primitives[Editor.ActivePrimitiveIdx].Geometry.Delete()
					ch.Primitives = append(ch.Primitives[:Editor.ActivePrimitiveIdx], ch.Primitives[Editor.ActivePrimitiveIdx+1:]...)
					rebuildPrimitiveIndices(ch)
					Editor.ActivePrimitiveIdx = -1
				}
			}
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
			rot := [3]float32{
				e.Rotation.X() * 180 / math.Pi,
				e.Rotation.Y() * 180 / math.Pi,
				e.Rotation.Z() * 180 / math.Pi,
			}
			if editorDragFloat3("Rotation (deg)", &rot, -360, 360, "%.1f") {
				e.Rotation = mgl32.Vec3{
					rot[0] * math.Pi / 180,
					rot[1] * math.Pi / 180,
					rot[2] * math.Pi / 180,
				}
			}
			sc := [3]float32{e.Scale.X(), e.Scale.Y(), e.Scale.Z()}
			if editorDragFloat3("Scale", &sc, 0.01, 100, "%.2f") {
				e.Scale = mgl32.Vec3{sc[0], sc[1], sc[2]}
			}

			imgui.Text(fmt.Sprintf("Model: %s", filepath.Base(e.ModelPath)))
			imgui.Text(fmt.Sprintf("Texture: %s", e.TexturePath))
			if editorDragFloat("Alpha##enta", &e.Alpha, 0, 1, "%.2f") {
				e.Geometry.Alpha = e.Alpha
			}
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
				if editorUVControls("Floor", &s3d.Floor.UVOffset, &s3d.Floor.UVScale, &s3d.Floor.UVRotation) {
					_ = s3d.Load(SectorPoints(sch, s3d))
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
				if editorUVControls("Ceiling", &s3d.Ceiling.UVOffset, &s3d.Ceiling.UVScale, &s3d.Ceiling.UVRotation) {
					_ = s3d.Load(SectorPoints(sch, s3d))
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
					if editorUVControls("Wall", &w.UVOffset, &w.UVScale, &w.UVRotation) {
						_ = s3d.Load(SectorPoints(sch, s3d))
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
		if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
			imgui.SameLine()
			if imgui.Button("Duplicate Chunk") {
				duplicateActiveChunk()
			}
			imgui.SameLine()
			if imgui.Button("Save Chunk") {
				Editor.SaveChunkName = lvl.Chunks[Editor.ActiveChunkIdx].ID
				Editor.ShowSaveChunk = true
			}
			imgui.SameLine()
			if imgui.Button("Delete Chunk") {
				Editor.ShowDeleteChunk = true
			}
		}
		if imgui.Button("Import Chunk") {
			files, _ := filepath.Glob(filepath.Join(executablePath, "chunks", "*.yaml"))
			Editor.ImportChunkFiles = nil
			for _, f := range files {
				Editor.ImportChunkFiles = append(Editor.ImportChunkFiles, filepath.Base(f))
			}
			Editor.ImportChunkIdx = 0
			Editor.ShowImportChunk = true
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

	// Save chunk popup
	if Editor.ShowSaveChunk {
		imgui.OpenPopup("Save Chunk")
		Editor.ShowSaveChunk = false
	}
	if imgui.BeginPopupModalV("Save Chunk", nil, imgui.WindowFlagsAlwaysAutoResize) {
		imgui.InputText("Filename", &Editor.SaveChunkName)
		if imgui.Button("Save") && Editor.SaveChunkName != "" {
			ch := &lvl.Chunks[Editor.ActiveChunkIdx]
			path := filepath.Join(executablePath, "chunks", Editor.SaveChunkName+".yaml")
			if err := SaveChunk(ch, path); err != nil {
				Editor.StatusMsg = fmt.Sprintf("Save chunk error: %v", err)
			} else {
				Editor.StatusMsg = fmt.Sprintf("Saved chunk to %s", path)
			}
			Editor.StatusTime = sdl.GetTicks()
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##savechunk") {
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}

	// Delete chunk confirmation popup
	if Editor.ShowDeleteChunk {
		imgui.OpenPopup("Delete Chunk")
		Editor.ShowDeleteChunk = false
	}
	if imgui.BeginPopupModalV("Delete Chunk", nil, imgui.WindowFlagsAlwaysAutoResize) {
		chunkName := ""
		if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
			chunkName = lvl.Chunks[Editor.ActiveChunkIdx].ID
		}
		imgui.Text(fmt.Sprintf("Delete chunk %q? This cannot be undone.", chunkName))
		if imgui.Button("Delete") {
			deleteActiveChunk()
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##deletechunk") {
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}

	// Import chunk popup
	if Editor.ShowImportChunk {
		imgui.OpenPopup("Import Chunk")
		Editor.ShowImportChunk = false
	}
	if imgui.BeginPopupModalV("Import Chunk", nil, imgui.WindowFlagsAlwaysAutoResize) {
		if len(Editor.ImportChunkFiles) == 0 {
			imgui.Text("No chunk files found in chunks/")
		} else {
			if imgui.BeginComboV("##chunkfile", Editor.ImportChunkFiles[Editor.ImportChunkIdx], 0) {
				for i, name := range Editor.ImportChunkFiles {
					if imgui.SelectableV(name, int32(i) == Editor.ImportChunkIdx, 0, imgui.Vec2{}) {
						Editor.ImportChunkIdx = int32(i)
					}
				}
				imgui.EndCombo()
			}
			if imgui.Button("Import") {
				path := filepath.Join(executablePath, "chunks", Editor.ImportChunkFiles[Editor.ImportChunkIdx])
				chunkID, err := ImportChunkIntoLevel(lvl, path)
				if err != nil {
					Editor.StatusMsg = fmt.Sprintf("Import error: %v", err)
				} else {
					Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
					Editor.ActiveSectorIdx = -1
					Editor.ActiveEdgeIdx = -1
					Editor.ActivePrimitiveIdx = -1
					Editor.StatusMsg = fmt.Sprintf("Imported chunk %q", chunkID)
				}
				Editor.StatusTime = sdl.GetTicks()
				imgui.CloseCurrentPopup()
			}
		}
		imgui.SameLine()
		if imgui.Button("Cancel##importchunk") {
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}
}

// reloadPrimitive regenerates the geometry and bounding volumes for a primitive
// after its type, scale, or side count changes.
func reloadPrimitive(ch *Chunk, idx int) {
	p := &ch.Primitives[idx]
	p.Geometry.Delete()

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
	case PrimitivePlane:
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
