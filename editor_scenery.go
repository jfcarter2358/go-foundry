package main

// editor_scenery.go — scenery editor: a focused, single-item model compositor.
//
// Build reusable models by placing/moving/rotating/scaling/texturing primitives
// around a local origin (0,0,0). Save to resources/scenery/<name>.yaml and
// import into levels from the 3D editor.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
	"gopkg.in/yaml.v2"
)

// RawScenery is the YAML representation of a scenery asset.
type RawScenery struct {
	ID         string         `yaml:"id"`
	Primitives []RawPrimitive `yaml:"primitives"`
}

// ---------- Enter / Exit ----------

// EnterSceneryEditor switches to the scenery editor mode with a blank world.
func EnterSceneryEditor() {
	Editor.SceneryPrevMode = Editor.Mode
	Editor.ScenerySavedCamPos = Cam.Position
	Editor.ScenerySavedCamYaw = Cam.Yaw
	Editor.ScenerySavedCamPitch = Cam.Pitch

	// Position camera looking at the origin
	Cam.Position = mgl32.Vec3{0, 2, 5}
	Cam.Yaw = float32(math.Pi) // face toward -Z (toward origin)
	Cam.Pitch = -0.3
	Cam.Direction = DirectionFromAngles(Cam.Yaw, Cam.Pitch)
	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), WorldUp)
	gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])

	// Upload neutral lighting (full ambient, no directional/point)
	UploadLights(LightSet{
		Global: GlobalLight{
			Color:      mgl32.Vec3{1, 1, 1},
			Brightness: 1.0,
		},
	})

	// Clear existing scenery state
	for i := range Editor.SceneryPrimitives {
		Editor.SceneryPrimitives[i].Geometry.Delete()
	}
	Editor.SceneryPrimitives = nil
	Editor.SceneryActivePrimIdx = -1
	Editor.SceneryPrimPlacement = false
	Editor.SceneryFilePath = ""
	Editor.SceneryID = ""

	Editor.Mode = ModeScenery
}

// exitSceneryEditor returns to the previous editor mode.
func exitSceneryEditor() {
	// Clean up GPU resources
	for i := range Editor.SceneryPrimitives {
		Editor.SceneryPrimitives[i].Geometry.Delete()
	}
	Editor.SceneryPrimitives = nil
	Editor.SceneryActivePrimIdx = -1
	Editor.SceneryPrimPlacement = false

	// Restore camera
	Cam.Position = Editor.ScenerySavedCamPos
	Cam.Yaw = Editor.ScenerySavedCamYaw
	Cam.Pitch = Editor.ScenerySavedCamPitch
	Cam.Direction = DirectionFromAngles(Cam.Yaw, Cam.Pitch)
	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), WorldUp)
	gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])

	// Restore level lighting
	if Editor.level != nil {
		UploadLights(Editor.level.Lights)
	}

	Editor.Mode = Editor.SceneryPrevMode
}

// ---------- Event Handling ----------

// sceneryEditorHandleKey handles keyboard input in scenery mode.
// Returns true if the key was consumed.
func sceneryEditorHandleKey(t sdl.KeyboardEvent) bool {
	if t.Type != sdl.KEYDOWN {
		return false
	}
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_DELETE:
		if Editor.SceneryActivePrimIdx >= 0 && Editor.SceneryActivePrimIdx < len(Editor.SceneryPrimitives) {
			Editor.SceneryPrimitives[Editor.SceneryActivePrimIdx].Geometry.Delete()
			Editor.SceneryPrimitives = append(
				Editor.SceneryPrimitives[:Editor.SceneryActivePrimIdx],
				Editor.SceneryPrimitives[Editor.SceneryActivePrimIdx+1:]...,
			)
			Editor.SceneryActivePrimIdx = -1
		}
		return true
	case sdl.SCANCODE_ESCAPE:
		exitSceneryEditor()
		return true
	}
	return false
}

// SceneryEditorHandleClick processes a mouse click in scenery mode.
func SceneryEditorHandleClick(t sdl.MouseButtonEvent) {
	if t.Button != sdl.ButtonLeft {
		return
	}

	if Editor.SceneryPrimPlacement {
		// Place a new primitive 1 unit in front of camera
		Editor.SceneryPrimPlacement = false
		spawnPos := Cam.Position.Add(Cam.Direction.Mul(1.0))

		var kind PrimitiveType
		switch Editor.SceneryPrimPlaceType {
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
			ID:       fmt.Sprintf("prim_%d", len(Editor.SceneryPrimitives)),
			Kind:     kind,
			Position: spawnPos,
			Scale:    mgl32.Vec3{1, 1, 1},
			Texture:  "concrete",
			Sides:    16,
			Alpha:    1.0,
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
			Editor.SceneryPrimitives = append(Editor.SceneryPrimitives, prim)
			Editor.SceneryActivePrimIdx = len(Editor.SceneryPrimitives) - 1
		}
		return
	}

	// Selection: ray cast against scenery primitive AABBs
	origin, dir := screenRay(t.X, t.Y)
	bestT := float32(math.MaxFloat32)
	bestIdx := -1

	for i := range Editor.SceneryPrimitives {
		p := &Editor.SceneryPrimitives[i]
		bmin := p.Position.Sub(mgl32.Vec3{p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ})
		bmax := p.Position.Add(mgl32.Vec3{p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ})
		if hit, ht := rayAABBIntersect(origin, dir, bmin, bmax); hit && ht < bestT {
			bestT = ht
			bestIdx = i
		}
	}

	Editor.SceneryActivePrimIdx = bestIdx
}

// ---------- Rendering ----------

// SceneryEditorRenderGizmos draws wireframe AABBs and a ground grid.
func SceneryEditorRenderGizmos(projView mgl32.Mat4) {
	green := mgl32.Vec3{0, 1, 0}
	gray := mgl32.Vec3{0.4, 0.4, 0.4}
	red := mgl32.Vec3{1, 0.3, 0.3}
	blue := mgl32.Vec3{0.3, 0.3, 1}

	// Draw XZ ground grid centered on origin
	gridSize := float32(10)
	gridStep := float32(1)
	for x := -gridSize; x <= gridSize; x += gridStep {
		GizmoLine(mgl32.Vec3{x, 0, -gridSize}, mgl32.Vec3{x, 0, gridSize}, gray)
	}
	for z := -gridSize; z <= gridSize; z += gridStep {
		GizmoLine(mgl32.Vec3{-gridSize, 0, z}, mgl32.Vec3{gridSize, 0, z}, gray)
	}

	// Draw origin axes
	GizmoLine(mgl32.Vec3{-0.5, 0, 0}, mgl32.Vec3{0.5, 0, 0}, red)
	GizmoLine(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0}, green)
	GizmoLine(mgl32.Vec3{0, 0, -0.5}, mgl32.Vec3{0, 0, 0.5}, blue)

	// Draw primitive AABBs
	for i := range Editor.SceneryPrimitives {
		p := &Editor.SceneryPrimitives[i]
		bmin := p.Position.Sub(mgl32.Vec3{p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ})
		bmax := p.Position.Add(mgl32.Vec3{p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ})
		color := gray
		if i == Editor.SceneryActivePrimIdx {
			color = green
		}
		GizmoAABB(bmin, bmax, color)
	}
}

// ---------- ImGui Panel ----------

// SceneryEditorPanel draws the side panel for the scenery editor.
func SceneryEditorPanel() {
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(WinWidth) - Editor.PanelWidth, Y: 28})
	imgui.SetNextWindowSize(imgui.Vec2{X: Editor.PanelWidth, Y: float32(WinHeight) - 28})
	imgui.BeginV("Scenery Editor", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	// Scenery ID
	imgui.InputText("Scenery ID", &Editor.SceneryID)

	imgui.Separator()

	// Grid settings
	editorGridSettings()

	// Navigation buttons
	if imgui.CollapsingHeaderV("Navigation", imgui.TreeNodeFlagsDefaultOpen) {
		imgui.Text("Right-click drag to rotate")
		imgui.Button("Fwd##scnav")
		if imgui.IsItemActive() {
			Editor.NavForward = true
		}
		imgui.SameLine()
		imgui.Button("Back##scnav")
		if imgui.IsItemActive() {
			Editor.NavBack = true
		}
		imgui.SameLine()
		imgui.Button("Left##scnav")
		if imgui.IsItemActive() {
			Editor.NavLeft = true
		}
		imgui.SameLine()
		imgui.Button("Right##scnav")
		if imgui.IsItemActive() {
			Editor.NavRight = true
		}
		imgui.SameLine()
		imgui.Button("Up##scnav")
		if imgui.IsItemActive() {
			Editor.NavUp = true
		}
		imgui.SameLine()
		imgui.Button("Down##scnav")
		if imgui.IsItemActive() {
			Editor.NavDown = true
		}
	}

	// Primitive catalog
	if imgui.CollapsingHeader("Primitive Catalog") {
		primTypes := []string{"rect_prism", "sphere", "cylinder", "plane"}
		for _, pt := range primTypes {
			selected := pt == Editor.SceneryPrimPlaceType
			if imgui.SelectableV(pt, selected, 0, imgui.Vec2{}) {
				Editor.SceneryPrimPlaceType = pt
			}
		}
		if imgui.Button("Place Primitive##sc") && Editor.SceneryPrimPlaceType != "" {
			Editor.SceneryPrimPlacement = true
		}
		if Editor.SceneryPrimPlacement {
			imgui.SameLine()
			imgui.Text("Click to place...")
		}
	}

	// Primitives list
	if imgui.CollapsingHeaderV("Primitives", imgui.TreeNodeFlagsDefaultOpen) {
		for i := range Editor.SceneryPrimitives {
			p := &Editor.SceneryPrimitives[i]
			selected := i == Editor.SceneryActivePrimIdx
			if imgui.SelectableV(fmt.Sprintf("%s##scprim%d", p.ID, i), selected, 0, imgui.Vec2{}) {
				Editor.SceneryActivePrimIdx = i
			}
		}
	}

	// Selected primitive properties
	if Editor.SceneryActivePrimIdx >= 0 && Editor.SceneryActivePrimIdx < len(Editor.SceneryPrimitives) {
		imgui.Separator()
		if imgui.CollapsingHeaderV("Primitive Properties", imgui.TreeNodeFlagsDefaultOpen) {
			p := &Editor.SceneryPrimitives[Editor.SceneryActivePrimIdx]
			imgui.InputText("ID##scprimid", &p.ID)

			typeNames := []string{"rect_prism", "sphere", "cylinder", "plane"}
			currentType := int(p.Kind)
			if imgui.BeginCombo("Type##scprimtype", typeNames[currentType]) {
				for i, tn := range typeNames {
					if imgui.SelectableV(tn, i == currentType, 0, imgui.Vec2{}) {
						p.Kind = PrimitiveType(i)
						reloadSceneryPrimitive(Editor.SceneryActivePrimIdx)
					}
				}
				imgui.EndCombo()
			}

			pos := [3]float32{p.Position.X(), p.Position.Y(), p.Position.Z()}
			if editorDragFloat3("Position##scprim", &pos, 0, 0, "%.3f") {
				p.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
			}
			rot := [3]float32{
				p.Rotation.X() * 180 / math.Pi,
				p.Rotation.Y() * 180 / math.Pi,
				p.Rotation.Z() * 180 / math.Pi,
			}
			if editorDragFloat3("Rotation (deg)##scprim", &rot, -360, 360, "%.1f") {
				p.Rotation = mgl32.Vec3{
					rot[0] * math.Pi / 180,
					rot[1] * math.Pi / 180,
					rot[2] * math.Pi / 180,
				}
			}
			sc := [3]float32{p.Scale.X(), p.Scale.Y(), p.Scale.Z()}
			if editorDragFloat3("Scale##scprim", &sc, 0.01, 100, "%.2f") {
				p.Scale = mgl32.Vec3{sc[0], sc[1], sc[2]}
				reloadSceneryPrimitive(Editor.SceneryActivePrimIdx)
			}

			imgui.InputText("Texture##scprimtex", &p.Texture)

			if p.Kind == PrimitiveCylinder {
				sides := int32(p.Sides)
				if imgui.DragIntV("Sides##scprimsides", &sides, 1, 3, 64, "%d", 0) {
					p.Sides = int(sides)
					reloadSceneryPrimitive(Editor.SceneryActivePrimIdx)
				}
			}

			imgui.Checkbox("Collision##scprimcol", &p.Collision)
			if editorDragFloat("Alpha##scprima", &p.Alpha, 0, 1, "%.2f") {
				p.Geometry.Alpha = p.Alpha
			}

			if imgui.CollapsingHeader("Texture UV##scprimUVhdr") {
				uvChanged := false
				uvOff := [2]float32{p.UVOffset.X(), p.UVOffset.Y()}
				if editorDragFloat("UV Offset U##scprimuvou", &uvOff[0], 0, 0, "%.3f") ||
					editorDragFloat("UV Offset V##scprimuvov", &uvOff[1], 0, 0, "%.3f") {
					p.UVOffset = mgl32.Vec2{uvOff[0], uvOff[1]}
					uvChanged = true
				}
				uvSc := p.UVScale
				if uvSc == (mgl32.Vec2{}) {
					uvSc = mgl32.Vec2{1, 1}
				}
				uvScArr := [2]float32{uvSc.X(), uvSc.Y()}
				if editorDragFloat("UV Scale U##scprimuvsu", &uvScArr[0], 0.01, 100, "%.3f") ||
					editorDragFloat("UV Scale V##scprimuvsv", &uvScArr[1], 0.01, 100, "%.3f") {
					p.UVScale = mgl32.Vec2{uvScArr[0], uvScArr[1]}
					uvChanged = true
				}
				uvRotDeg := p.UVRotation * 180 / math.Pi
				if editorDragFloat("UV Rotation (deg)##scprimuvrot", &uvRotDeg, -360, 360, "%.1f") {
					p.UVRotation = uvRotDeg * math.Pi / 180
					uvChanged = true
				}
				if uvChanged {
					reloadSceneryPrimitive(Editor.SceneryActivePrimIdx)
				}
			}

			if imgui.Button("Delete Primitive##sc") {
				Editor.SceneryPrimitives[Editor.SceneryActivePrimIdx].Geometry.Delete()
				Editor.SceneryPrimitives = append(
					Editor.SceneryPrimitives[:Editor.SceneryActivePrimIdx],
					Editor.SceneryPrimitives[Editor.SceneryActivePrimIdx+1:]...,
				)
				Editor.SceneryActivePrimIdx = -1
			}
		}
	}

	imgui.End()

	// Popups
	sceneryPickerPopup()
	scenerySaveAsPopup()
}

// reloadSceneryPrimitive regenerates geometry and bounds for a scenery primitive.
func reloadSceneryPrimitive(idx int) {
	if idx < 0 || idx >= len(Editor.SceneryPrimitives) {
		return
	}
	p := &Editor.SceneryPrimitives[idx]
	p.Geometry.Delete()

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

// ---------- Save / Load ----------

// SaveScenery writes the scenery asset to a YAML file.
func SaveScenery(prims []Primitive, id, path string) error {
	raw := RawScenery{ID: id}
	for i := range prims {
		raw.Primitives = append(raw.Primitives, sceneryPrimToRaw(&prims[i]))
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// sceneryPrimToRaw converts a primitive to its YAML form (no chunk transform).
func sceneryPrimToRaw(p *Primitive) RawPrimitive {
	sc := p.Scale
	if sc == (mgl32.Vec3{}) {
		sc = mgl32.Vec3{1, 1, 1}
	}

	var typeStr string
	switch p.Kind {
	case PrimitiveSphere:
		typeStr = "sphere"
	case PrimitiveCylinder:
		typeStr = "cylinder"
	case PrimitivePlane:
		typeStr = "plane"
	default:
		typeStr = "rect_prism"
	}

	uvSc := p.UVScale
	if uvSc == (mgl32.Vec2{}) {
		uvSc = mgl32.Vec2{1, 1}
	}

	return RawPrimitive{
		ID:         p.ID,
		Type:       typeStr,
		Position:   RawEntityPosition{X: p.Position.X(), Y: p.Position.Y(), Z: p.Position.Z()},
		Rotation:   RawEntityPosition{X: p.Rotation.X(), Y: p.Rotation.Y(), Z: p.Rotation.Z()},
		Scale:      RawEntityPosition{X: sc.X(), Y: sc.Y(), Z: sc.Z()},
		Texture:    p.Texture,
		Sides:      p.Sides,
		Collision:  p.Collision,
		Alpha:      float32PtrIfNotOne(p.Alpha),
		UVOffset:   Point{X: p.UVOffset.X(), Y: p.UVOffset.Y()},
		UVScale:    Point{X: uvSc.X(), Y: uvSc.Y()},
		UVRotation: p.UVRotation,
	}
}

// LoadSceneryForEditor reads a scenery YAML and converts to runtime primitives.
func LoadSceneryForEditor(path string) (string, []Primitive, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var raw RawScenery
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return "", nil, err
	}

	var prims []Primitive
	for _, rp := range raw.Primitives {
		prim := rawToSceneryPrimitive(rp)
		if err := prim.Load(); err != nil {
			return "", nil, fmt.Errorf("primitive %q load: %w", rp.ID, err)
		}
		prims = append(prims, prim)
	}
	return raw.ID, prims, nil
}

// rawToSceneryPrimitive converts a RawPrimitive to a runtime Primitive
// without applying any chunk transforms.
func rawToSceneryPrimitive(rp RawPrimitive) Primitive {
	scale := mgl32.Vec3{rp.Scale.X, rp.Scale.Y, rp.Scale.Z}
	if scale == (mgl32.Vec3{}) {
		scale = mgl32.Vec3{1, 1, 1}
	}

	var kind PrimitiveType
	switch rp.Type {
	case "sphere":
		kind = PrimitiveSphere
	case "cylinder":
		kind = PrimitiveCylinder
	case "plane":
		kind = PrimitivePlane
	default:
		kind = PrimitiveRectPrism
	}

	sides := rp.Sides
	if sides < 3 {
		sides = 16
	}

	uvScale := mgl32.Vec2{rp.UVScale.X, rp.UVScale.Y}
	if uvScale == (mgl32.Vec2{}) {
		uvScale = mgl32.Vec2{1, 1}
	}

	prim := Primitive{
		ID:         rp.ID,
		Kind:       kind,
		Position:   mgl32.Vec3{rp.Position.X, rp.Position.Y, rp.Position.Z},
		Rotation:   mgl32.Vec3{rp.Rotation.X, rp.Rotation.Y, rp.Rotation.Z},
		Scale:      scale,
		Texture:    rp.Texture,
		Sides:      sides,
		Collision:  rp.Collision,
		Alpha:      float32Default(rp.Alpha, 1.0),
		UVOffset:   mgl32.Vec2{rp.UVOffset.X, rp.UVOffset.Y},
		UVScale:    uvScale,
		UVRotation: rp.UVRotation,
	}

	// Compute bounding volumes
	switch kind {
	case PrimitiveRectPrism:
		prim.BoundingKind = BoundingAABB
		prim.BoundHalfX = scale.X() / 2
		prim.BoundHalfY = scale.Y() / 2
		prim.BoundHalfZ = scale.Z() / 2
	case PrimitiveSphere:
		prim.BoundingKind = BoundingSphere
		maxS := scale.X()
		if scale.Y() > maxS {
			maxS = scale.Y()
		}
		if scale.Z() > maxS {
			maxS = scale.Z()
		}
		prim.BoundHalfX = maxS / 2
		prim.BoundHalfY = maxS / 2
		prim.BoundHalfZ = maxS / 2
	case PrimitiveCylinder:
		prim.BoundingKind = BoundingCylinder
		maxXZ := scale.X()
		if scale.Z() > maxXZ {
			maxXZ = scale.Z()
		}
		prim.BoundHalfX = maxXZ / 2
		prim.BoundHalfY = scale.Y() / 2
		prim.BoundHalfZ = maxXZ / 2
	case PrimitivePlane:
		prim.BoundingKind = BoundingAABB
		prim.BoundHalfX = scale.X() / 2
		prim.BoundHalfY = 0.01
		prim.BoundHalfZ = scale.Z() / 2
	}

	return prim
}

// ScanSceneryFiles returns sorted .yaml filenames from the scenery directory.
func ScanSceneryFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	return files
}

// ImportSceneryIntoChunk loads a scenery file and adds its primitives to the chunk,
// offset by spawnPos.
func ImportSceneryIntoChunk(ch *Chunk, path string, spawnPos mgl32.Vec3) error {
	id, prims, err := LoadSceneryForEditor(path)
	if err != nil {
		return err
	}
	// Clean up the loaded GPU resources — we'll re-load with proper positions
	for i := range prims {
		prims[i].Geometry.Delete()
	}

	baseIdx := len(ch.Primitives)
	for i := range prims {
		p := prims[i]
		p.Position = p.Position.Add(spawnPos)
		p.ID = fmt.Sprintf("%s_%s_%d", id, p.ID, baseIdx+i)
		p.ChunkID = ch.ID

		if err := p.Load(); err != nil {
			return fmt.Errorf("import primitive %q: %w", p.ID, err)
		}

		// Ensure texture is loaded
		if p.Texture != "" {
			if _, ok := Textures[p.Texture]; !ok {
				if tpath, ok := textureNames[p.Texture]; ok {
					LoadTexture(p.Texture, filepath.Join(executablePath, tpath))
				}
			}
		}

		ch.Primitives = append(ch.Primitives, p)
	}

	rebuildPrimitiveIndices(ch)
	return nil
}

// ---------- Popups ----------

func sceneryPickerPopup() {
	if Editor.ShowSceneryPicker {
		imgui.OpenPopup("Load Scenery##popup")
		Editor.ShowSceneryPicker = false
	}
	if imgui.BeginPopupModal("Load Scenery##popup") {
		sceneryDir := filepath.Join(executablePath, "scenery")
		if Editor.SceneryFiles == nil {
			Editor.SceneryFiles = ScanSceneryFiles(sceneryDir)
		}
		for _, f := range Editor.SceneryFiles {
			if imgui.Selectable(f) {
				path := filepath.Join(sceneryDir, f)
				// Clean up current scenery
				for i := range Editor.SceneryPrimitives {
					Editor.SceneryPrimitives[i].Geometry.Delete()
				}
				id, prims, err := LoadSceneryForEditor(path)
				if err == nil {
					Editor.SceneryPrimitives = prims
					Editor.SceneryID = id
					Editor.SceneryFilePath = path
					Editor.SceneryActivePrimIdx = -1
					// Load textures
					for i := range prims {
						tex := prims[i].Texture
						if tex != "" {
							if _, ok := Textures[tex]; !ok {
								if tpath, ok := textureNames[tex]; ok {
									LoadTexture(tex, filepath.Join(executablePath, tpath))
								}
							}
						}
					}
				}
				Editor.SceneryFiles = nil
				imgui.CloseCurrentPopup()
			}
		}
		if imgui.Button("Cancel##scload") {
			Editor.SceneryFiles = nil
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}
}

func scenerySaveAsPopup() {
	if Editor.ShowScenerySaveAs {
		imgui.OpenPopup("Save Scenery As##popup")
		Editor.ShowScenerySaveAs = false
	}
	if imgui.BeginPopupModal("Save Scenery As##popup") {
		imgui.InputText("Filename##scsaveas", &Editor.ScenerySaveAsName)
		if imgui.Button("Save##scsaveas") && Editor.ScenerySaveAsName != "" {
			name := Editor.ScenerySaveAsName
			if !strings.HasSuffix(name, ".yaml") {
				name += ".yaml"
			}
			sceneryDir := filepath.Join(executablePath, "scenery")
			path := filepath.Join(sceneryDir, name)
			id := Editor.SceneryID
			if id == "" {
				id = strings.TrimSuffix(name, ".yaml")
			}
			if err := SaveScenery(Editor.SceneryPrimitives, id, path); err == nil {
				Editor.SceneryFilePath = path
				if Editor.SceneryID == "" {
					Editor.SceneryID = id
				}
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##scsaveas") {
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}
}

// importSceneryPopup is the popup for importing scenery into a level chunk (used from 3D editor).
func importSceneryPopup() {
	if Editor.ShowImportScenery {
		imgui.OpenPopup("Import Scenery##popup")
		Editor.ShowImportScenery = false
	}
	if imgui.BeginPopupModal("Import Scenery##popup") {
		sceneryDir := filepath.Join(executablePath, "scenery")
		if Editor.ImportSceneryFiles == nil {
			Editor.ImportSceneryFiles = ScanSceneryFiles(sceneryDir)
		}
		for i, f := range Editor.ImportSceneryFiles {
			if imgui.SelectableV(f, int32(i) == Editor.ImportSceneryIdx, 0, imgui.Vec2{}) {
				Editor.ImportSceneryIdx = int32(i)
			}
		}
		if imgui.Button("Import##scimport") && len(Editor.ImportSceneryFiles) > 0 &&
			int(Editor.ImportSceneryIdx) < len(Editor.ImportSceneryFiles) {
			lvl := Editor.level
			if lvl != nil && Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
				ch := &lvl.Chunks[Editor.ActiveChunkIdx]
				path := filepath.Join(sceneryDir, Editor.ImportSceneryFiles[Editor.ImportSceneryIdx])
				spawnPos := Cam.Position.Add(Cam.Direction.Mul(4.0))
				spawnPos[1] -= eyeHeight
				_ = ImportSceneryIntoChunk(ch, path, spawnPos)
			}
			Editor.ImportSceneryFiles = nil
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##scimport") {
			Editor.ImportSceneryFiles = nil
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}
}
