package main

// editor_lights.go — ImGui panels for editing all light types.
//
// Presents global, directional, and point lights in the side panel.
// Calls UploadLights() immediately on any change so the 3D view updates live.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"math"

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
		Editor.SelectedDirLightIdx = -1
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
	// The open tree node selects the light for the on-screen direction
	// indicator (see EditorDirLightIndicator); reset each frame so closing the
	// node — or collapsing the whole Lights header above — hides it.
	selDir := -1
	if imgui.TreeNode(fmt.Sprintf("Directional Lights (%d)###dirLights", len(lvl.Lights.Directional))) {
		for i := range lvl.Lights.Directional {
			dl := &lvl.Lights.Directional[i]
			label := fmt.Sprintf("Dir Light %d", i)
			if imgui.TreeNode(label) {
				if selDir < 0 {
					selDir = i
				}
				// Yaw/pitch instead of raw vector components: a vector edited
				// one component at a time passes through {0,0,0}, and
				// normalizing that yields NaN.
				rotated := false
				if editorDragFloatStep(fmt.Sprintf("Yaw (deg)##diryaw%d", i), &dl.Yaw, -360, 360, "%.1f", 1) {
					rotated = true
				}
				if editorDragFloatStep(fmt.Sprintf("Pitch (deg)##dirpitch%d", i), &dl.Pitch, -90, 90, "%.1f", 1) {
					rotated = true
				}
				if rotated {
					dl.SyncDirection()
					selDir = i
					changed = true
				}
				imgui.Text(fmt.Sprintf("Direction: %.2f, %.2f, %.2f",
					dl.Direction.X(), dl.Direction.Y(), dl.Direction.Z()))
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
			// Angled by default: straight down (pitch -90) lights every
			// upward-facing surface identically and makes yaw a no-op, which
			// reads as "the light can't be aimed".
			nl := DirectionalLight{
				Yaw:         DefaultDirLightYaw,
				Pitch:       DefaultDirLightPitch,
				Color:       mgl32.Vec3{1, 1, 1},
				Brightness:  1.0,
				CastShadows: true,
				// Must be non-zero, or the shadow map is rendered and then
				// multiplied out by (1 - shadow * 0) and the light reads as
				// casting nothing. Matches the Add Point Light default.
				ShadowStrength: 0.5,
			}
			nl.SyncDirection()
			lvl.Lights.Directional = append(lvl.Lights.Directional, nl)
			selDir = len(lvl.Lights.Directional) - 1
			changed = true
		}
		imgui.TreePop()
	}
	Editor.SelectedDirLightIdx = selDir

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
				Position:       gfx.Cam.Position,
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

// ---------- selected directional light indicator ----------

// dirLightIndicatorSize is the width/height of the indicator card in pixels.
const dirLightIndicatorSize float32 = 128

// EditorDirLightIndicator draws a small compass/elevation card in the top-right
// of the viewport (just left of the side panel) showing which way the selected
// directional light points. Yaw alone is hard to read off three numbers, and the
// light itself has no position to hang a world-space gizmo on.
//
// Drawn on the foreground draw list, so it must be called between
// ImGuiNewFrame and ImGuiRender, and after the side panel that sets the
// selection.
func EditorDirLightIndicator() {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	i := Editor.SelectedDirLightIdx
	if i < 0 || i >= len(lvl.Lights.Directional) {
		return
	}
	dl := &lvl.Lights.Directional[i]

	dList := imgui.ForegroundDrawList()

	const pad float32 = 12
	sz := dirLightIndicatorSize
	x1 := float32(platform.WinWidth) - Editor.PanelWidth - pad
	x0 := x1 - sz
	y0 := 28 + pad
	y1 := y0 + sz + 46 // extra room for the title, elevation view, and readout

	bg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.05, Y: 0.05, Z: 0.07, W: 0.78})
	border := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.5, Y: 0.5, Z: 0.55, W: 0.9})
	dim := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.45, Y: 0.45, Z: 0.5, W: 0.9})
	label := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.8, Y: 0.8, Z: 0.85, W: 0.95})
	// Tint the arrows with the light's own color so multiple lights are
	// distinguishable; fall back to a warm yellow for near-black lights.
	c := dl.Color
	if c.Len() < 0.15 {
		c = mgl32.Vec3{1, 0.85, 0.3}
	} else {
		c = c.Normalize()
	}
	arrow := imgui.PackedColorFromVec4(imgui.Vec4{X: c.X(), Y: c.Y(), Z: c.Z(), W: 1})

	dList.AddRectFilledV(imgui.Vec2{X: x0, Y: y0}, imgui.Vec2{X: x1, Y: y1}, bg, 6, imgui.DrawFlagsRoundCornersAll)
	dList.AddRectV(imgui.Vec2{X: x0, Y: y0}, imgui.Vec2{X: x1, Y: y1}, border, 6, imgui.DrawFlagsRoundCornersAll, 1)
	dList.AddText(imgui.Vec2{X: x0 + 8, Y: y0 + 5}, label, fmt.Sprintf("Dir Light %d", i))

	yaw := float64(mgl32.DegToRad(dl.Yaw))
	pitch := float64(mgl32.DegToRad(dl.Pitch))

	// --- Compass (top-down): +X right, +Z down, matching the 2D editor view ---
	r := sz*0.5 - 30
	cx := x0 + sz*0.5
	cy := y0 + 26 + r
	dList.AddCircleV(imgui.Vec2{X: cx, Y: cy}, r, dim, 24, 1)
	dList.AddLine(imgui.Vec2{X: cx - r, Y: cy}, imgui.Vec2{X: cx + r, Y: cy}, dim)
	dList.AddLine(imgui.Vec2{X: cx, Y: cy - r}, imgui.Vec2{X: cx, Y: cy + r}, dim)
	dList.AddText(imgui.Vec2{X: cx + r + 3, Y: cy - 7}, dim, "+X")
	dList.AddText(imgui.Vec2{X: cx - 8, Y: cy + r + 1}, dim, "+Z")
	// Horizontal component only — shortened as the light nears vertical, so a
	// straight-down light shows a dot rather than a misleading heading.
	horiz := float32(math.Cos(pitch))
	drawIndicatorArrow(dList, cx, cy,
		float32(math.Cos(yaw))*horiz*r, float32(math.Sin(yaw))*horiz*r, arrow)

	// --- Elevation (side view): horizon line, arrow tilted by pitch ---
	ey := cy + r + 18
	dList.AddLine(imgui.Vec2{X: x0 + 10, Y: ey}, imgui.Vec2{X: x1 - 10, Y: ey}, dim)
	ex := x0 + 22
	drawIndicatorArrow(dList, ex, ey,
		float32(math.Cos(pitch))*r, -float32(math.Sin(pitch))*r, arrow)

	dList.AddText(imgui.Vec2{X: x0 + 8, Y: y1 - 16}, label,
		fmt.Sprintf("yaw %.0f°  pitch %.0f°", dl.Yaw, dl.Pitch))
}

// drawIndicatorArrow draws a line from (ox, oy) along (dx, dy) with a filled
// head at the tip. A near-zero vector degenerates to a dot.
func drawIndicatorArrow(dList imgui.DrawList, ox, oy, dx, dy float32, col imgui.PackedColor) {
	length := float32(math.Hypot(float64(dx), float64(dy)))
	if length < 1.5 {
		dList.AddCircleFilled(imgui.Vec2{X: ox, Y: oy}, 3, col)
		return
	}
	ux, uy := dx/length, dy/length
	tipX, tipY := ox+dx, oy+dy
	dList.AddLineV(imgui.Vec2{X: ox, Y: oy}, imgui.Vec2{X: tipX, Y: tipY}, col, 2)
	const head float32 = 8
	baseX, baseY := tipX-ux*head, tipY-uy*head
	// Perpendicular in screen space.
	px, py := -uy*head*0.45, ux*head*0.45
	dList.AddTriangleFilled(
		imgui.Vec2{X: tipX, Y: tipY},
		imgui.Vec2{X: baseX + px, Y: baseY + py},
		imgui.Vec2{X: baseX - px, Y: baseY - py},
		col,
	)
}
