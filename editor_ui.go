package main

// editor_ui.go — top-level ImGui panel orchestration for the level editor.
//
// EditorDrawUI() is called each frame between ImGuiNewFrame and ImGuiRender.
// It draws the toolbar and delegates to the mode-specific side panels.

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// editorTextEdits tracks which fields are in text-input mode and their buffer.
var editorTextEdits = map[string]string{}

// snapValue rounds v to the nearest multiple of step.
func snapValue(v, step float32) float32 {
	if step <= 0 {
		return v
	}
	return float32(math.Round(float64(v)/float64(step))) * step
}

// clampValue clamps v to [min, max] when the range is non-zero.
func clampValue(v, min, max float32) float32 {
	if min != 0 && v < min {
		v = min
	}
	if max != 0 && v > max {
		v = max
	}
	return v
}

// editorDragFloat draws a drag field with [-] and [+] increment buttons and
// an inline text-input toggle button.
// The visible label is rendered on a line above the widget so it never clips.
// Layout: label (own line) then [-] [drag field] [=] [+]
func editorDragFloat(label string, value *float32, min, max float32, format string) bool {
	changed := false

	// Extract the visible portion of the label (before ##) for display
	visibleLabel := label
	if idx := strings.Index(label, "##"); idx >= 0 {
		visibleLabel = label[:idx]
	}
	if visibleLabel != "" {
		imgui.Text(visibleLabel)
	}

	// Compute button sizes
	btnW := imgui.CalcTextSize("+", false, 0).X + imgui.CurrentStyle().FramePadding().X*2
	spacing := imgui.CurrentStyle().ItemSpacing().X
	editBtnW := imgui.CalcTextSize("=", false, 0).X + imgui.CurrentStyle().FramePadding().X*2

	// [-] button
	imgui.PushButtonRepeat(true)
	if imgui.ButtonV("-##dec"+label, imgui.Vec2{X: btnW, Y: 0}) {
		*value -= Editor.StepSize
		if Editor.SnapToGrid {
			*value = snapValue(*value, Editor.StepSize)
		}
		*value = clampValue(*value, min, max)
		changed = true
	}
	imgui.PopButtonRepeat()
	imgui.SameLine()

	// Field width = remaining space minus edit button and [+] button and their spacing
	fieldWidth := imgui.ContentRegionAvail().X - editBtnW - btnW - spacing*2
	if fieldWidth < 40 {
		fieldWidth = 40
	}

	// Use ##-only ID so no inline label is drawn by ImGui after the widget
	hiddenLabel := "##" + label

	if buf, editing := editorTextEdits[label]; editing {
		// Text-input mode
		imgui.SetNextItemWidth(fieldWidth)
		if imgui.InputTextV("##input"+label, &buf, imgui.InputTextFlagsCharsDecimal|imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil) {
			if f, err := strconv.ParseFloat(buf, 32); err == nil {
				*value = float32(f)
				if Editor.SnapToGrid {
					*value = snapValue(*value, Editor.StepSize)
				}
				*value = clampValue(*value, min, max)
				changed = true
			}
			delete(editorTextEdits, label)
		} else {
			editorTextEdits[label] = buf
		}
		imgui.SameLine()
		if imgui.ButtonV("x##close"+label, imgui.Vec2{X: editBtnW, Y: 0}) {
			delete(editorTextEdits, label)
		}
	} else {
		// Drag mode
		imgui.SetNextItemWidth(fieldWidth)
		if imgui.DragFloatV(hiddenLabel, value, Editor.StepSize, min, max, format, 0) {
			if Editor.SnapToGrid {
				*value = snapValue(*value, Editor.StepSize)
				*value = clampValue(*value, min, max)
			}
			changed = true
		}
		imgui.SameLine()
		if imgui.ButtonV("=##edit"+label, imgui.Vec2{X: editBtnW, Y: 0}) {
			editorTextEdits[label] = fmt.Sprintf("%g", *value)
		}
	}

	// [+] button
	imgui.SameLine()
	imgui.PushButtonRepeat(true)
	if imgui.ButtonV("+##inc"+label, imgui.Vec2{X: btnW, Y: 0}) {
		*value += Editor.StepSize
		if Editor.SnapToGrid {
			*value = snapValue(*value, Editor.StepSize)
		}
		*value = clampValue(*value, min, max)
		changed = true
	}
	imgui.PopButtonRepeat()

	return changed
}

// editorDragFloat3 draws three drag fields with inline text-input toggle buttons.
func editorDragFloat3(label string, values *[3]float32, min, max float32, format string) bool {
	changed := false
	labels := [3]string{"X", "Y", "Z"}

	imgui.Text(label)
	imgui.PushID(label)
	for i := 0; i < 3; i++ {
		subLabel := fmt.Sprintf("%s##%d", labels[i], i)
		if editorDragFloat(subLabel, &values[i], min, max, format) {
			changed = true
		}
	}
	imgui.PopID()
	return changed
}

// editorUVControls draws collapsible UV offset/scale/rotation controls.
// Returns true if any value changed.
func editorUVControls(label string, offset *mgl32.Vec2, scale *mgl32.Vec2, rotation *float32) bool {
	if !imgui.CollapsingHeader(fmt.Sprintf("Texture UV##%sUV", label)) {
		return false
	}
	changed := false
	uvOff := [2]float32{offset.X(), offset.Y()}
	if editorDragFloat(fmt.Sprintf("UV Offset U##%suvou", label), &uvOff[0], 0, 0, "%.3f") ||
		editorDragFloat(fmt.Sprintf("UV Offset V##%suvov", label), &uvOff[1], 0, 0, "%.3f") {
		*offset = mgl32.Vec2{uvOff[0], uvOff[1]}
		changed = true
	}
	uvSc := *scale
	if uvSc == (mgl32.Vec2{}) {
		uvSc = mgl32.Vec2{1, 1}
	}
	uvScArr := [2]float32{uvSc.X(), uvSc.Y()}
	if editorDragFloat(fmt.Sprintf("UV Scale U##%suvsu", label), &uvScArr[0], 0.01, 100, "%.3f") ||
		editorDragFloat(fmt.Sprintf("UV Scale V##%suvsv", label), &uvScArr[1], 0.01, 100, "%.3f") {
		*scale = mgl32.Vec2{uvScArr[0], uvScArr[1]}
		changed = true
	}
	rotDeg := *rotation * 180 / math.Pi
	if editorDragFloat(fmt.Sprintf("UV Rotation (deg)##%suvrot", label), &rotDeg, -360, 360, "%.1f") {
		*rotation = rotDeg * math.Pi / 180
		changed = true
	}
	return changed
}

// editorGridSettings draws the step-size and snap-to-grid controls.
func editorGridSettings() {
	imgui.Checkbox("Snap to Grid", &Editor.SnapToGrid)
	imgui.SameLine()

	label := "Step##gridStep"
	stepDelta := float32(0.05)
	btnW := imgui.CalcTextSize("+", false, 0).X + imgui.CurrentStyle().FramePadding().X*2
	editBtnW := imgui.CalcTextSize("=", false, 0).X + imgui.CurrentStyle().FramePadding().X*2

	// [-] button
	imgui.PushButtonRepeat(true)
	if imgui.ButtonV("-##decStep", imgui.Vec2{X: btnW, Y: 0}) {
		Editor.StepSize -= stepDelta
		if Editor.StepSize < 0.01 {
			Editor.StepSize = 0.01
		}
	}
	imgui.PopButtonRepeat()
	imgui.SameLine()

	if buf, editing := editorTextEdits[label]; editing {
		imgui.SetNextItemWidth(60)
		if imgui.InputTextV("##inputStep", &buf, imgui.InputTextFlagsCharsDecimal|imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil) {
			if f, err := strconv.ParseFloat(buf, 32); err == nil {
				v := float32(f)
				if v < 0.01 {
					v = 0.01
				}
				if v > 10.0 {
					v = 10.0
				}
				Editor.StepSize = v
			}
			delete(editorTextEdits, label)
		} else {
			editorTextEdits[label] = buf
		}
		imgui.SameLine()
		if imgui.ButtonV("x##closeStep", imgui.Vec2{X: editBtnW, Y: 0}) {
			delete(editorTextEdits, label)
		}
	} else {
		imgui.SetNextItemWidth(60)
		imgui.DragFloatV(label, &Editor.StepSize, 0.01, 0.01, 10.0, "%.2f", 0)
		imgui.SameLine()
		if imgui.ButtonV("=##editStep", imgui.Vec2{X: editBtnW, Y: 0}) {
			editorTextEdits[label] = fmt.Sprintf("%g", Editor.StepSize)
		}
	}

	// [+] button
	imgui.SameLine()
	imgui.PushButtonRepeat(true)
	if imgui.ButtonV("+##incStep", imgui.Vec2{X: btnW, Y: 0}) {
		Editor.StepSize += stepDelta
		if Editor.StepSize > 10.0 {
			Editor.StepSize = 10.0
		}
	}
	imgui.PopButtonRepeat()

	imgui.Separator()
}

// EditorDrawUI draws all ImGui panels for the current editor mode.
func EditorDrawUI() {
	lvl := Editor.level
	if lvl == nil {
		return
	}

	// ---- Top toolbar ----
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(WinWidth), Y: 28})
	imgui.BeginV("##toolbar", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoScrollbar)

	// Mode toggle buttons — snapshot mode before rendering so Push/Pop stay matched
	currentMode := Editor.Mode

	if currentMode == Mode3D {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.6, Z: 0.2, W: 1})
	}
	if imgui.Button("3D Mode [Tab]") && Editor.Mode != Mode3D {
		Editor.Mode = Mode3D
	}
	if currentMode == Mode3D {
		imgui.PopStyleColor()
	}

	imgui.SameLine()
	if currentMode == Mode2D {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.6, Z: 0.2, W: 1})
	}
	if imgui.Button("2D Mode [Tab]") && Editor.Mode != Mode2D {
		Editor.Mode = Mode2D
	}
	if currentMode == Mode2D {
		imgui.PopStyleColor()
	}

	imgui.SameLine()
	if currentMode == ModeSurface2D {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.8, Y: 0.4, Z: 0.1, W: 1})
	}
	if imgui.Button("Surface") && Editor.Mode == ModeSurface2D {
		exitSurface2D()
	}
	if currentMode == ModeSurface2D {
		imgui.PopStyleColor()
	}


	imgui.SameLine()
	imgui.Separator()
	imgui.SameLine()

	// Chunk selector
	imgui.Text("Chunk:")
	imgui.SameLine()
	chunkNames := make([]string, len(lvl.Chunks))
	for i := range lvl.Chunks {
		chunkNames[i] = lvl.Chunks[i].ID
	}
	if len(chunkNames) > 0 {
		if Editor.ActiveChunkIdx < 0 {
			Editor.ActiveChunkIdx = 0
		}
		if Editor.ActiveChunkIdx >= len(chunkNames) {
			Editor.ActiveChunkIdx = len(chunkNames) - 1
		}
		currentName := chunkNames[Editor.ActiveChunkIdx]
		imgui.SetNextItemWidth(150)
		if imgui.BeginCombo("##chunk", currentName) {
			for i, name := range chunkNames {
				selected := i == Editor.ActiveChunkIdx
				if imgui.SelectableV(name, selected, 0, imgui.Vec2{}) {
					Editor.ActiveChunkIdx = i
				}
			}
			imgui.EndCombo()
		}
	}

	imgui.SameLine()
	imgui.Separator()
	imgui.SameLine()

	// Level path input
	imgui.Text("Path:")
	imgui.SameLine()
	imgui.SetNextItemWidth(200)
	imgui.InputText("##levelpath", &Editor.SavePath)
	imgui.SameLine()

	// Save button
	if imgui.Button("Save") {
		if err := SaveLevel(lvl, Editor.SavePath); err != nil {
			Editor.StatusMsg = fmt.Sprintf("Save failed: %v", err)
		} else {
			Editor.StatusMsg = fmt.Sprintf("Saved to %s", Editor.SavePath)
		}
		Editor.StatusTime = sdl.GetTicks()
	}

	imgui.SameLine()

	// Load button
	if imgui.Button("Load") {
		if err := EditorLoadLevel(Editor.SavePath); err != nil {
			Editor.StatusMsg = fmt.Sprintf("Load failed: %v", err)
		} else {
			Editor.StatusMsg = fmt.Sprintf("Loaded %s", Editor.SavePath)
		}
		Editor.StatusTime = sdl.GetTicks()
	}

	imgui.SameLine()

	// Status message (shown for 3 seconds after save/load)
	if Editor.StatusMsg != "" {
		if sdl.GetTicks()-Editor.StatusTime < 3000 {
			imgui.Text(Editor.StatusMsg)
		} else {
			Editor.StatusMsg = ""
		}
	}

	imgui.End()

	// ---- Mode-specific side panel ----
	if Editor.Mode == Mode3D {
		Editor3DPanel()
	} else if Editor.Mode == Mode2D {
		Editor2DPanel()
	} else if Editor.Mode == ModeSurface2D {
		Surface2DPanel()
	}
}

// Editor2DPanel draws the 2D mode side panel.
func Editor2DPanel() {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]

	imgui.SetNextWindowPos(imgui.Vec2{X: float32(WinWidth) - Editor.PanelWidth, Y: 28})
	imgui.SetNextWindowSize(imgui.Vec2{X: Editor.PanelWidth, Y: float32(WinHeight) - 28})
	imgui.BeginV("2D Sector Editor", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	imgui.Text(fmt.Sprintf("Chunk: %s", ch.ID))
	imgui.Text(fmt.Sprintf("Zoom: %.0f px/unit", Editor.Zoom2D))
	imgui.Text("Scroll: zoom | MMB drag: pan | Shift+click: add vertex")

	// ---- Grid settings ----
	imgui.Separator()
	editorGridSettings()

	// ---- Sector list ----
	imgui.Separator()
	if imgui.CollapsingHeader(fmt.Sprintf("Sectors (%d)", len(ch.Sectors))) {
		for i := range ch.Sectors {
			s := &ch.Sectors[i]
			selected := i == Editor.ActiveSectorIdx
			if imgui.SelectableV(s.ID, selected, 0, imgui.Vec2{}) {
				Editor.ActiveSectorIdx = i
				Editor.ActiveEdgeIdx = -1
			}
		}
	}

	// ---- Selected sector properties ----
	if Editor.ActiveSectorIdx >= 0 && Editor.ActiveSectorIdx < len(ch.Sectors) {
		imgui.Separator()
		s := &ch.Sectors[Editor.ActiveSectorIdx]
		sectorPts := SectorPoints(ch, s)
		if imgui.CollapsingHeaderV("Sector: "+s.ID, imgui.TreeNodeFlagsDefaultOpen) {
			imgui.InputText("Sector ID##sid", &s.ID)

			// Floor
			if imgui.TreeNode("Floor") {
				changed := false
				changed = imgui.Checkbox("Enabled##fe", &s.Floor.Enabled) || changed
				changed = editorDragFloat("Height##fh", &s.Floor.Height, 0, 0, "%.3f") || changed
				changed = imgui.InputText("Texture##ft", &s.Floor.Texture) || changed
				changed = imgui.Checkbox("Terrain##fter", &s.Floor.Terrain) || changed
				imgui.Checkbox("Double Sided##fds", &s.Floor.DoubleSided)
				if changed {
					_ = s.Load(SectorPoints(ch, s))
				}
				imgui.TreePop()
			}

			// Ceiling
			if imgui.TreeNode("Ceiling") {
				changed := false
				changed = imgui.Checkbox("Enabled##ce", &s.Ceiling.Enabled) || changed
				changed = editorDragFloat("Height##ch", &s.Ceiling.Height, 0, 0, "%.3f") || changed
				changed = imgui.InputText("Texture##ct", &s.Ceiling.Texture) || changed
				changed = imgui.Checkbox("Terrain##cter", &s.Ceiling.Terrain) || changed
				imgui.Checkbox("Double Sided##cds", &s.Ceiling.DoubleSided)
				if changed {
					_ = s.Load(SectorPoints(ch, s))
				}
				imgui.TreePop()
			}

			// Floor/ceiling surface editors + extrusion + terrain panels
			if imgui.Button("Edit Floor Surface##floorSurf2d") {
				EnterSurface2D("floor")
			}
			editorSurfaceExtrusionPanel(ch, s, "Floor2D", &s.Floor.Extrusions)
			editorTerrainPanel(ch, s, "Floor2D", &s.Floor.Terrain, &s.Floor.TerrainPoints)
			if imgui.Button("Edit Ceiling Surface##ceilSurf2d") {
				EnterSurface2D("ceiling")
			}
			editorSurfaceExtrusionPanel(ch, s, "Ceil2D", &s.Ceiling.Extrusions)
			editorTerrainPanel(ch, s, "Ceil2D", &s.Ceiling.Terrain, &s.Ceiling.TerrainPoints)
		}

		// ---- Floor/Ceiling vertex edit toggle ----
		imgui.Separator()
		editingFloor := Editor.EditFloorVerts
		if editingFloor {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.4, Z: 0.9, W: 1})
		}
		if imgui.Button("Floor Verts") {
			Editor.EditFloorVerts = true
		}
		if editingFloor {
			imgui.PopStyleColor()
		}
		imgui.SameLine()
		if !editingFloor {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.9, Y: 0.3, Z: 0.3, W: 1})
		}
		if imgui.Button("Ceiling Verts") {
			Editor.EditFloorVerts = false
		}
		if !editingFloor {
			imgui.PopStyleColor()
		}

		// ---- Selected vertex position and height ----
		si := Editor.ActiveVertexSI
		vi := Editor.ActiveVertexVI
		if si == Editor.ActiveSectorIdx && si >= 0 && vi >= 0 && vi < len(sectorPts) {
			imgui.Separator()
			imgui.Text(fmt.Sprintf("Vertex #%d", vi))
			x := sectorPts[vi].X()
			z := sectorPts[vi].Y()
			xChanged := editorDragFloat(fmt.Sprintf("X##vx%d", vi), &x, -1e6, 1e6, "%.3f")
			zChanged := editorDragFloat(fmt.Sprintf("Z##vz%d", vi), &z, -1e6, 1e6, "%.3f")
			if xChanged || zChanged {
				if vi < len(s.PointIndices) {
					ch.Points[s.PointIndices[vi]] = mgl32.Vec2{x, z}
				}
				rebuildSectorEdgePoints(ch, s)
				reloadSectorsUsingPoint(ch, s.PointIndices[vi])
			}

			// Floor vertex offset
			if vi < len(s.Floor.VertexOffsets) {
				fOff := s.Floor.VertexOffsets[vi]
				if editorDragFloat(fmt.Sprintf("Floor Offset##fo%d", vi), &fOff, -100, 100, "%.3f") {
					s.Floor.VertexOffsets[vi] = fOff
					_ = s.Load(SectorPoints(ch, s))
				}
			}
			// Ceiling vertex offset
			if vi < len(s.Ceiling.VertexOffsets) {
				cOff := s.Ceiling.VertexOffsets[vi]
				if editorDragFloat(fmt.Sprintf("Ceil Offset##co%d", vi), &cOff, -100, 100, "%.3f") {
					s.Ceiling.VertexOffsets[vi] = cOff
					_ = s.Load(SectorPoints(ch, s))
				}
			}
		}

		// ---- Selected edge properties ----
		if Editor.ActiveEdgeIdx >= 0 {
			imgui.Separator()
			editorEdgePanel(ch, s)
		}
	}

	imgui.End()
}

// editorEdgePanel draws the property editor for the currently selected edge.
func editorEdgePanel(ch *Chunk, s *Sector) {
	kind := Editor.ActiveEdgeKind
	idx := Editor.ActiveEdgeIdx

	imgui.Text(fmt.Sprintf("Edge: %s #%d", kind, idx))

	// Kind toggle
	kindStr := kind
	if imgui.BeginCombo("Kind##ek", kindStr) {
		if imgui.SelectableV("wall", kindStr == "wall", 0, imgui.Vec2{}) {
			convertEdge(ch, s, kind, idx, "wall")
			Editor.ActiveEdgeKind = "wall"
		}
		if imgui.SelectableV("portal", kindStr == "portal", 0, imgui.Vec2{}) {
			convertEdge(ch, s, kind, idx, "portal")
			Editor.ActiveEdgeKind = "portal"
		}
		imgui.EndCombo()
	}

	changed := false

	if kind == "wall" && idx < len(s.Walls) {
		w := &s.Walls[idx]
		changed = imgui.InputText("Texture##wt", &w.Texture) || changed
		changed = imgui.Checkbox("Collision##wc", &w.Collision) || changed
		changed = imgui.Checkbox("Visible##wv", &w.Visible) || changed
		imgui.Checkbox("Double Sided##wds", &w.DoubleSided)
		if changed {
			_ = s.Load(SectorPoints(ch, s))
			ch.RebuildGaps()
		}
		changed = false
		if imgui.Button("Edit Wall Surface##wallSurf2d") {
			EnterSurface2D("wall")
		}
		editorExtrusionPanel(ch, s, w)
		editorTerrainPanel(ch, s, "Wall2D", &w.Terrain, &w.TerrainPoints)
	} else if kind == "portal" && idx < len(s.Portals) {
		p := &s.Portals[idx]
		changed = imgui.InputText("To Sector##pts", &p.ToSector) || changed
		changed = imgui.Checkbox("Draw Gap##pdg", &p.DrawGap) || changed
		if p.DrawGap {
			changed = imgui.InputText("Gap Texture##pgt", &p.GapTexture) || changed
		}
		changed = imgui.Checkbox("Visible##pv", &p.Visible) || changed
		imgui.Checkbox("Double Sided##pds", &p.DoubleSided)
	}

	if changed {
		_ = s.Load(SectorPoints(ch, s))
		ch.RebuildGaps()
	}
}

// convertEdge converts an edge between wall and portal types.
func convertEdge(ch *Chunk, s *Sector, fromKind string, idx int, toKind string) {
	if fromKind == toKind {
		return
	}

	// Find the polygon edge index BEFORE modifying any arrays, so the
	// wall/portal sub-array index still maps correctly to EdgeKinds.
	polyIdx := polyEdgeIdx(s, fromKind, idx)
	if polyIdx < 0 || polyIdx >= len(s.EdgeKinds) {
		return
	}

	// Compute the correct insertion position in the target sub-array so
	// that the ordering stays consistent with EdgeKinds.
	insertIdx := 0
	for ei := 0; ei < polyIdx; ei++ {
		if s.EdgeKinds[ei] == toKind {
			insertIdx++
		}
	}

	if fromKind == "wall" && idx < len(s.Walls) {
		w := s.Walls[idx]
		p := Portal{StartPoint: w.StartPoint, EndPoint: w.EndPoint, Visible: true, DoubleSided: w.DoubleSided}
		s.Walls = append(s.Walls[:idx], s.Walls[idx+1:]...)
		// Insert portal at the correct position
		s.Portals = append(s.Portals, Portal{})
		copy(s.Portals[insertIdx+1:], s.Portals[insertIdx:])
		s.Portals[insertIdx] = p
		Editor.ActiveEdgeKind = "portal"
		Editor.ActiveEdgeIdx = insertIdx
	} else if fromKind == "portal" && idx < len(s.Portals) {
		p := s.Portals[idx]
		w := Wall{StartPoint: p.StartPoint, EndPoint: p.EndPoint, Texture: "concrete", Collision: true, Visible: true, Pinned: true, DoubleSided: p.DoubleSided}
		s.Portals = append(s.Portals[:idx], s.Portals[idx+1:]...)
		// Insert wall at the correct position
		s.Walls = append(s.Walls, Wall{})
		copy(s.Walls[insertIdx+1:], s.Walls[insertIdx:])
		s.Walls[insertIdx] = w
		Editor.ActiveEdgeKind = "wall"
		Editor.ActiveEdgeIdx = insertIdx
	}

	s.EdgeKinds[polyIdx] = toKind
	_ = s.Load(SectorPoints(ch, s))
}

// polyEdgeIdx returns the polygon edge index for the wall/portal at kind+idx.
func polyEdgeIdx(s *Sector, kind string, idx int) int {
	count := 0
	for ei, k := range s.EdgeKinds {
		if k == kind {
			if count == idx {
				return ei
			}
			count++
		}
	}
	return -1
}
