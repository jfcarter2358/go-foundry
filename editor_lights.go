package main

// editor_lights.go — ImGui panels for editing all light types.
//
// Presents global, directional, and point lights in the side panel.
// Calls UploadLights() immediately on any change so the 3D view updates live.

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// EditorLightPanel draws the light-editing section of the side panel.
// Must be called inside an imgui.Begin/End block.
func EditorLightPanel() {
	lvl := Editor.level
	if lvl == nil {
		return
	}

	if !imgui.CollapsingHeader("Lights") {
		return
	}

	changed := false

	// ---- Global light ----
	if imgui.TreeNode("Global Light") {
		col := [3]float32{lvl.Lights.Global.Color.X(), lvl.Lights.Global.Color.Y(), lvl.Lights.Global.Color.Z()}
		if imgui.ColorEdit3("Color##global", &col) {
			lvl.Lights.Global.Color = mgl32.Vec3{col[0], col[1], col[2]}
			changed = true
		}
		if editorDragFloat("Brightness##global", &lvl.Lights.Global.Brightness, 0, 10, "%.2f") {
			changed = true
		}
		imgui.TreePop()
	}

	// ---- Directional lights ----
	if imgui.TreeNode(fmt.Sprintf("Directional Lights (%d)###dirLights", len(lvl.Lights.Directional))) {
		for i := range lvl.Lights.Directional {
			dl := &lvl.Lights.Directional[i]
			label := fmt.Sprintf("Dir Light %d", i)
			if imgui.TreeNode(label) {
				dir := [3]float32{dl.Direction.X(), dl.Direction.Y(), dl.Direction.Z()}
				if editorDragFloat3(fmt.Sprintf("Direction##dir%d", i), &dir, -1, 1, "%.2f") {
					dl.Direction = mgl32.Vec3{dir[0], dir[1], dir[2]}.Normalize()
					changed = true
				}
				col := [3]float32{dl.Color.X(), dl.Color.Y(), dl.Color.Z()}
				if imgui.ColorEdit3(fmt.Sprintf("Color##dir%d", i), &col) {
					dl.Color = mgl32.Vec3{col[0], col[1], col[2]}
					changed = true
				}
				if editorDragFloat(fmt.Sprintf("Brightness##dir%d", i), &dl.Brightness, 0, 10, "%.2f") {
					changed = true
				}
				if imgui.Checkbox(fmt.Sprintf("Cast Shadows##dir%d", i), &dl.CastShadows) {
					if dl.CastShadows && dl.ShadowStrength == 0 {
						dl.ShadowStrength = 0.5
					}
					changed = true
				}
				if dl.CastShadows {
					imgui.Text("Shadow Strength")
					if editorDragFloat(fmt.Sprintf("##shadowStr_dir%d", i), &dl.ShadowStrength, 0, 1, "%.2f") {
						changed = true
					}
				}
				if imgui.Button(fmt.Sprintf("Remove##dir%d", i)) {
					lvl.Lights.Directional = append(lvl.Lights.Directional[:i], lvl.Lights.Directional[i+1:]...)
					changed = true
					imgui.TreePop()
					break
				}
				imgui.TreePop()
			}
		}
		if imgui.Button("Add Directional Light") {
			lvl.Lights.Directional = append(lvl.Lights.Directional, DirectionalLight{
				Direction:   mgl32.Vec3{0, -1, 0},
				Color:       mgl32.Vec3{1, 1, 1},
				Brightness:  1.0,
				CastShadows: true,
			})
			changed = true
		}
		imgui.TreePop()
	}

	// ---- Point lights ----
	if imgui.TreeNode(fmt.Sprintf("Point Lights (%d)###pointLights", len(lvl.Lights.Points))) {
		for i := range lvl.Lights.Points {
			pl := &lvl.Lights.Points[i]
			label := fmt.Sprintf("Point Light %d", i)
			if imgui.TreeNode(label) {
				pos := [3]float32{pl.Position.X(), pl.Position.Y(), pl.Position.Z()}
				if editorDragFloat3(fmt.Sprintf("Position##pt%d", i), &pos, 0, 0, "%.3f") {
					pl.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
					changed = true
				}
				col := [3]float32{pl.Color.X(), pl.Color.Y(), pl.Color.Z()}
				if imgui.ColorEdit3(fmt.Sprintf("Color##pt%d", i), &col) {
					pl.Color = mgl32.Vec3{col[0], col[1], col[2]}
					changed = true
				}
				if editorDragFloat(fmt.Sprintf("Brightness##pt%d", i), &pl.Brightness, 0, 10, "%.2f") {
					changed = true
				}
				if editorDragFloat(fmt.Sprintf("Radius##pt%d", i), &pl.Radius, 0.1, 200, "%.1f") {
					changed = true
				}
				if imgui.Checkbox(fmt.Sprintf("Cast Shadows##pt%d", i), &pl.CastShadows) {
					if pl.CastShadows && pl.ShadowStrength == 0 {
						pl.ShadowStrength = 0.5
					}
					changed = true
				}
				if pl.CastShadows {
					imgui.Text("Shadow Strength")
					if editorDragFloat(fmt.Sprintf("##shadowStr_pt%d", i), &pl.ShadowStrength, 0, 1, "%.2f") {
						changed = true
					}
				}
				if imgui.Button(fmt.Sprintf("Remove##pt%d", i)) {
					lvl.Lights.Points = append(lvl.Lights.Points[:i], lvl.Lights.Points[i+1:]...)
					changed = true
					imgui.TreePop()
					break
				}
				imgui.TreePop()
			}
		}
		if imgui.Button("Add Point Light") {
			lvl.Lights.Points = append(lvl.Lights.Points, PointLight{
				Position:       Cam.Position,
				Color:          mgl32.Vec3{1, 0.9, 0.7},
				Brightness:     2.0,
				Radius:         10.0,
				CastShadows:    true,
				ShadowStrength: 0.5,
			})
			changed = true
		}
		imgui.TreePop()
	}

	if changed {
		UploadLights(lvl.Lights)
	}
}
