package main

// editor_building.go — the building editor (ModeBuilding).
//
// A dedicated 3D editor mode for designing reusable BuildingTemplates. The
// current template is carved into a small standalone chunk (BuildBuildingPreview)
// and shown in an isolated blank-world 3D view the user can free-fly / orbit
// around. Templates are saved into the active world's Palette, which the city
// generator then stamps from (see generateBuildingsFromPalette).

import (
	"fmt"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// EnterBuildingEditor switches into the building editor, seeding the working
// template from the active world's palette selection (or the default) and
// building the initial preview.
func EnterBuildingEditor() {
	Editor.BuildingPrevMode = Editor.Mode
	Editor.Mode = ModeBuilding
	Cam.MouseLock = false
	sdl.ShowCursor(sdl.ENABLE)
	Window.SetGrab(false)

	g := activeWorldChunkGrid()
	if g != nil && len(g.Palette) > 0 {
		if Editor.PaletteSel < 0 || Editor.PaletteSel >= len(g.Palette) {
			Editor.PaletteSel = 0
		}
		Editor.EditTemplate = g.Palette[Editor.PaletteSel]
	} else {
		Editor.PaletteSel = -1
		if Editor.EditTemplate.Name == "" {
			Editor.EditTemplate = defaultBuildingTemplate()
		}
	}
	Editor.BuildingDirty = true
	rebuildBuildingPreview()
	frameBuildingCamera()
}

// activeWorldChunkGrid returns the active chunk's WorldGrid, or nil.
func activeWorldChunkGrid() *WorldGrid {
	if ch := activeWorldChunk(); ch != nil {
		return ch.WorldGrid
	}
	return nil
}

// rebuildBuildingPreview recarves the preview chunk from the working template,
// freeing the previous preview's GPU buffers first.
func rebuildBuildingPreview() {
	if Editor.BuildingPreview != nil {
		deleteChunkSectorGeometry(Editor.BuildingPreview)
		Editor.BuildingPreview = nil
	}
	ch, names := BuildBuildingPreview(Editor.EditTemplate)
	ensureTexturesLoaded(names)
	Editor.BuildingPreview = ch
	Editor.BuildingDirty = false
}

// frameBuildingCamera positions the fly camera to view the whole preview
// building from the door (front, +Z) side at a pleasant angle.
func frameBuildingCamera() {
	t := Editor.EditTemplate
	top := float32(t.Stories)*t.StoryHeight + t.Parapet
	if top < 4 {
		top = 4
	}
	radius := t.Width
	if t.Depth > radius {
		radius = t.Depth
	}
	dist := radius + top + 8
	Cam.Position = mgl32.Vec3{dist * 0.55, top*0.75 + 3, dist * 0.85}
	target := mgl32.Vec3{0, top * 0.4, 0}
	dir := target.Sub(Cam.Position).Normalize()
	Cam.Yaw = float32(math.Atan2(float64(dir.Z()), float64(dir.X())))
	Cam.Pitch = float32(math.Asin(float64(dir.Y())))
	updateCamDirection()
	updateCamMatrix()
}

// DrawBuildingPreview renders the preview chunk's sector geometry (called from the
// main render loop when in ModeBuilding). No culling — the preview is tiny.
func DrawBuildingPreview() {
	ch := Editor.BuildingPreview
	if ch == nil {
		return
	}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if s.Floor.Enabled {
			s.Floor.Geometry.Draw()
			for gi := range s.Floor.ExtrusionGeometries {
				s.Floor.ExtrusionGeometries[gi].Draw()
			}
		}
		if s.Ceiling.Enabled {
			s.Ceiling.Geometry.Draw()
		}
		for wi := range s.Walls {
			w := &s.Walls[wi]
			if !w.Visible {
				continue
			}
			w.Geometry.Draw()
			for gi := range w.ExtrusionGeometries {
				w.ExtrusionGeometries[gi].Draw()
			}
		}
	}
}

// ---------- panel ----------

// BuildingEditorPanel draws the building-editor side panel.
func BuildingEditorPanel() {
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(WinWidth) - Editor.PanelWidth, Y: 28})
	imgui.SetNextWindowSize(imgui.Vec2{X: Editor.PanelWidth, Y: float32(WinHeight) - 28})
	imgui.BeginV("Building Editor", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	imgui.Text("Design a building, then Add to Palette.")
	imgui.Text("RMB drag: orbit | WASD/EQ: fly")
	imgui.Separator()

	g := activeWorldChunkGrid()
	if g == nil {
		imgui.Text("No active world.")
		imgui.Text("Create/select a world (World mode) to")
		imgui.Text("store a building palette.")
	}

	// ---- Palette list ----
	if g != nil {
		imgui.Text(fmt.Sprintf("Palette (%d)", len(g.Palette)))
		if imgui.BeginChildV("##palette", imgui.Vec2{X: 0, Y: 120}, true, 0) {
			for i := range g.Palette {
				label := fmt.Sprintf("%s##pal%d", g.Palette[i].Name, i)
				if imgui.SelectableV(label, i == Editor.PaletteSel, 0, imgui.Vec2{}) {
					Editor.PaletteSel = i
					Editor.EditTemplate = g.Palette[i]
					Editor.BuildingDirty = true
				}
			}
			imgui.EndChild()
		}
		if imgui.Button("New##bld") {
			Editor.EditTemplate = defaultBuildingTemplate()
			Editor.PaletteSel = -1
			Editor.BuildingDirty = true
		}
		imgui.SameLine()
		addLabel := "Add to Palette"
		if Editor.PaletteSel >= 0 {
			addLabel = "Update"
		}
		if imgui.Button(addLabel+"##bld") {
			paletteAddOrUpdate(g)
		}
		imgui.SameLine()
		if imgui.Button("Delete##bld") && Editor.PaletteSel >= 0 && Editor.PaletteSel < len(g.Palette) {
			g.Palette = append(g.Palette[:Editor.PaletteSel], g.Palette[Editor.PaletteSel+1:]...)
			Editor.PaletteSel = -1
		}
	}

	imgui.Separator()
	imgui.Text("Template")

	t := &Editor.EditTemplate
	changed := false

	if imgui.InputText("Name##bt", &t.Name) {
		// name change doesn't affect geometry
	}
	changed = editorDragFloat("Width##bt", &t.Width, 3, 40, "%.1f") || changed
	changed = editorDragFloat("Depth##bt", &t.Depth, 3, 40, "%.1f") || changed
	stories := int32(t.Stories)
	if imgui.InputInt("Stories##bt", &stories) {
		if stories < 1 {
			stories = 1
		}
		if stories > 12 {
			stories = 12
		}
		t.Stories = int(stories)
		changed = true
	}
	changed = editorDragFloat("Story Height##bt", &t.StoryHeight, 2, 8, "%.1f") || changed
	changed = editorDragFloat("Wall Thickness##bt", &t.WallThick, 0.15, 3, "%.2f") || changed
	changed = editorDragFloat("Parapet##bt", &t.Parapet, 0, 3, "%.2f") || changed
	if imgui.Checkbox("Enterable (door)##bt", &t.Enterable) {
		changed = true
	}
	if t.Enterable {
		changed = editorDragFloat("Door Width##bt", &t.DoorWidth, 1.0, 6, "%.1f") || changed
	}
	changed = textureCombo("Wall Tex##bt", &t.WallTex) || changed
	changed = textureCombo("Roof Tex##bt", &t.RoofTex) || changed
	changed = textureCombo("Floor Tex##bt", &t.FloorTex) || changed
	editorDragFloat("Palette Weight##bt", &t.Weight, 0, 20, "%.1f")

	if changed {
		Editor.BuildingDirty = true
	}
	if Editor.BuildingDirty {
		rebuildBuildingPreview()
	}

	imgui.Separator()
	if imgui.Button("Reframe Camera##bld") {
		frameBuildingCamera()
	}
	imgui.SameLine()
	if imgui.Button("Back to World##bld") {
		exitBuildingEditor(ModeWorld)
	}

	imgui.End()
}

// paletteAddOrUpdate saves the working template to the palette (update if a slot
// is selected, else append) and keeps the selection on it.
func paletteAddOrUpdate(g *WorldGrid) {
	if g == nil {
		return
	}
	t := Editor.EditTemplate
	if t.Name == "" {
		t.Name = fmt.Sprintf("building%d", len(g.Palette)+1)
		Editor.EditTemplate.Name = t.Name
	}
	if Editor.PaletteSel >= 0 && Editor.PaletteSel < len(g.Palette) {
		g.Palette[Editor.PaletteSel] = t
	} else {
		g.Palette = append(g.Palette, t)
		Editor.PaletteSel = len(g.Palette) - 1
	}
}

// exitBuildingEditor leaves the building editor, freeing the preview geometry.
func exitBuildingEditor(to EditorMode) {
	if Editor.BuildingPreview != nil {
		deleteChunkSectorGeometry(Editor.BuildingPreview)
		Editor.BuildingPreview = nil
	}
	if to == ModeBuilding {
		to = ModeWorld
	}
	Editor.Mode = to
	if to == ModeWorld {
		EnterWorldEditor()
	}
}

// textureCombo renders a texture picker populated from the scanned texture names.
// Returns true if the selection changed.
func textureCombo(label string, cur *string) bool {
	changed := false
	if imgui.BeginCombo(label, *cur) {
		names := make([]string, 0, len(textureNames))
		for name := range textureNames {
			names = append(names, name)
		}
		sort.Strings(names) // stable order so the dropdown doesn't shuffle each frame
		for _, name := range names {
			if imgui.SelectableV(name, name == *cur, 0, imgui.Vec2{}) {
				if *cur != name {
					*cur = name
					changed = true
				}
			}
		}
		imgui.EndCombo()
	}
	return changed
}
