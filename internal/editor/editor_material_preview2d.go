package editor

// editor_material_preview2d.go — the preview for the domains that are not a
// surface.
//
// A surface material is previewed on a lit mesh through the world shader
// (editor_material_render.go), because a surface's effects are about SHADING —
// how light falls on it, how it displaces, how two textures mix.
//
// A sprite's are not. A quad's material decides which pass it draws in, what
// it is tinted, how its alpha reads, and how its sheet is cut up; the billboard
// mode is about the camera and is meaningless without one. All of that is
// exactly what can be drawn honestly in two dimensions, over the checkerboard
// that says where the transparency is — so that is what this does, rather than
// standing a billboard up in the 3D view and pretending the flat result was
// worth the pass.
//
// The sky is previewed the same way and for the same reason: a gradient is
// three colours and a cubemap is six images, and both read better laid out than
// wrapped round a dome the size of a panel.

import (
	"fmt"

	"foundry/internal/gfx"
	"foundry/internal/material"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// materialPreview2DWindow draws the centre panel for a sprite, particle or sky
// material. Positioned between the material list and the parameter panel, in
// the space the 3D swatch occupies for a surface.
func materialPreview2DWindow() {
	w := materialEditorWindow
	if w == nil || materialDoc == nil || materialDoc.Domain == material.DomainSurface {
		return
	}
	x := materialListWidth
	width := float32(w.W) - materialListWidth - materialPanelWidth
	if width < 120 {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: x, Y: materialToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: width, Y: float32(w.H) - materialToolbarH})
	imgui.BeginV("Preview##mprev2d", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoMove|
			imgui.WindowFlagsNoSavedSettings)

	switch materialDoc.Domain {
	case material.DomainSprite, material.DomainParticle:
		materialQuadPreview(width)
	case material.DomainSky:
		materialSkyPreview(width)
	}
	imgui.End()
}

// materialQuadPreview draws the material's texture as the quad would be drawn:
// tinted, at its alpha, over a checkerboard, with the sheet grid on top.
func materialQuadPreview(width float32) {
	q := material.ResolveQuadDoc(materialDoc)

	tex := q.Texture
	if tex == "" {
		editorTextMutedWrapped("This material supplies no texture, so what wears it keeps its own. " +
			"Set one above to see the tint and the sheet against real art.")
		tex = "default"
	}
	_ = gfx.EnsureTextureLoaded(tex)
	id, ok := gfx.Textures[tex]
	if !ok || id == 0 {
		editorTextMutedWrapped("Texture " + tex + " is not loaded.")
		return
	}

	side := width - 40
	if side > 420 {
		side = 420
	}
	if side < 64 {
		return
	}
	imgui.Spacing()
	origin := imgui.CursorScreenPos()
	topLeft := imgui.Vec2{X: origin.X + 20, Y: origin.Y + 8}
	botRight := imgui.Vec2{X: topLeft.X + side, Y: topLeft.Y + side}
	dl := imgui.WindowDrawList()

	// The checkerboard first, so the alpha is visible as transparency rather
	// than as a colour against the panel.
	materialDrawChecker(dl, topLeft, botRight)

	tint := imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1}
	if q.HasTint {
		tint.X, tint.Y, tint.Z = q.Tint.X(), q.Tint.Y(), q.Tint.Z()
	}
	if q.HasAlpha {
		tint.W = q.Alpha
	}
	dl.AddImageV(imgui.TextureID(id), topLeft, botRight,
		imgui.Vec2{}, imgui.Vec2{X: 1, Y: 1}, imgui.PackedColorFromVec4(tint))

	if q.HasSheet && q.SheetCols > 0 && q.SheetRows > 0 {
		materialDrawSheetGrid(dl, topLeft, side, q.SheetCols, q.SheetRows)
	}

	imgui.Dummy(imgui.Vec2{X: side, Y: side + 16})

	// What the two enums mean cannot be drawn, so it is said.
	if q.HasBlendMode {
		editorTextMuted("Blend: " + q.BlendMode)
		switch q.BlendMode {
		case "additive":
			editorTextMutedWrapped("Adds light and never occludes. Order-independent, so it needs no sorting.")
		case "alpha":
			editorTextMutedWrapped("Sorted back to front, no depth write.")
		default:
			editorTextMutedWrapped("Opaque with a hard alpha edge; writes depth and is antialiased by the opaque pass.")
		}
	}
	if q.HasBillboard {
		editorTextMuted("Billboard: " + q.Billboard)
	}
	if q.HasVolume {
		editorTextMutedWrapped("This layer is marched as volumetric balls rather than drawn as quads, " +
			"so the texture above is not sampled at all — the albedo, density and emission below are what it looks like. " +
			"Open the effect in the particle editor to watch it move.")
	}
}

// materialSkyPreview draws a gradient as a vertical strip and a cubemap as its
// six faces, since that is what each one is.
func materialSkyPreview(width float32) {
	s := material.ResolveSkyDoc(materialDoc)

	tint := imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1}
	if s.HasTint {
		tint.X = s.Tint.X() * s.Intensity
		tint.Y = s.Tint.Y() * s.Intensity
		tint.Z = s.Tint.Z() * s.Intensity
	}

	if s.HasGradient {
		imgui.Text("Gradient")
		dl := imgui.WindowDrawList()
		origin := imgui.CursorScreenPos()
		w := width - 40
		if w > 360 {
			w = 360
		}
		const h = float32(180)
		x0, y0 := origin.X+20, origin.Y+4
		zen := materialTintedColor(s.Zenith, tint)
		hor := materialTintedColor(s.Horizon, tint)
		gnd := materialTintedColor(s.Ground, tint)
		// Two bands: zenith down to horizon, then horizon down to ground —
		// which is how the dome is actually interpolated.
		dl.AddRectFilledMultiColor(
			imgui.Vec2{X: x0, Y: y0}, imgui.Vec2{X: x0 + w, Y: y0 + h*0.6}, zen, zen, hor, hor)
		dl.AddRectFilledMultiColor(
			imgui.Vec2{X: x0, Y: y0 + h*0.6}, imgui.Vec2{X: x0 + w, Y: y0 + h}, hor, hor, gnd, gnd)
		imgui.Dummy(imgui.Vec2{X: w, Y: h + 12})
		editorTextMutedWrapped("Ground is what is seen looking down where the level has no floor.")
	}

	if s.HasCubemap {
		imgui.Text("Cubemap")
		names := [material.SkyFaces]string{"+X", "-X", "+Y", "-Y", "+Z", "-Z"}
		cell := (width - 60) / 3
		if cell > 110 {
			cell = 110
		}
		if cell < 40 {
			cell = 40
		}
		for i, face := range s.Faces {
			if i%3 != 0 {
				imgui.SameLine()
			}
			materialFaceThumb(names[i], face, cell, tint)
		}
		if s.Rotation != 0 {
			editorTextMuted(fmt.Sprintf("Rotated %.0f deg about +Y", s.Rotation))
		}
	}

	if s.HasClouds {
		imgui.Separator()
		editorTextMutedWrapped(fmt.Sprintf(
			"A cloud deck at altitude %.0f, %.0f thick, marched out to %.0f. "+
				"It is a volume rather than art, so there is nothing to show here — "+
				"look at it over the level with Live Update on.",
			s.Clouds.Altitude, s.Clouds.Thickness, s.Clouds.Range))
	}
	if s.HasFog {
		imgui.Separator()
		editorTextMutedWrapped("A fog volume's medium. Place a volume in the level and give it this material to see it.")
	}
	if !s.HasGradient && !s.HasCubemap && !s.HasClouds && !s.HasFog {
		editorTextMutedWrapped("No sky effects yet. Add a gradient or a cubemap.")
	}
}

// materialFaceThumb draws one cubemap face, labelled, or an empty slot.
func materialFaceThumb(label, texture string, side float32, tint imgui.Vec4) {
	imgui.BeginGroup()
	imgui.Text(label)
	origin := imgui.CursorScreenPos()
	topLeft := origin
	botRight := imgui.Vec2{X: origin.X + side, Y: origin.Y + side}
	dl := imgui.WindowDrawList()
	if texture != "" {
		_ = gfx.EnsureTextureLoaded(texture)
	}
	if id, ok := gfx.Textures[texture]; ok && id != 0 {
		dl.AddImageV(imgui.TextureID(id), topLeft, botRight,
			imgui.Vec2{}, imgui.Vec2{X: 1, Y: 1}, imgui.PackedColorFromVec4(tint))
	} else {
		dl.AddRectFilled(topLeft, botRight,
			imgui.PackedColorFromVec4(imgui.Vec4{X: 0.16, Y: 0.16, Z: 0.18, W: 1}))
	}
	imgui.Dummy(imgui.Vec2{X: side, Y: side})
	imgui.EndGroup()
}

func materialTintedColor(c [3]float32, tint imgui.Vec4) imgui.PackedColor {
	return imgui.PackedColorFromVec4(imgui.Vec4{
		X: clamp01f(c[0] * tint.X), Y: clamp01f(c[1] * tint.Y), Z: clamp01f(c[2] * tint.Z), W: 1,
	})
}

// materialDrawChecker fills a rectangle with the transparency checkerboard, so
// a material's alpha reads as see-through rather than as a shade of the panel.
func materialDrawChecker(dl imgui.DrawList, topLeft, botRight imgui.Vec2) {
	const square = float32(12)
	light := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.32, Y: 0.32, Z: 0.34, W: 1})
	dark := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.22, Y: 0.22, Z: 0.24, W: 1})
	dl.AddRectFilled(topLeft, botRight, dark)
	row := 0
	for y := topLeft.Y; y < botRight.Y; y += square {
		col := 0
		for x := topLeft.X; x < botRight.X; x += square {
			if (row+col)%2 == 0 {
				x1, y1 := x+square, y+square
				if x1 > botRight.X {
					x1 = botRight.X
				}
				if y1 > botRight.Y {
					y1 = botRight.Y
				}
				dl.AddRectFilled(imgui.Vec2{X: x, Y: y}, imgui.Vec2{X: x1, Y: y1}, light)
			}
			col++
		}
		row++
	}
}

// materialDrawSheetGrid lays the sheet's cell boundaries over the art, which is
// the only way to tell a 4x4 that lines up from one that does not.
func materialDrawSheetGrid(dl imgui.DrawList, topLeft imgui.Vec2, side float32, cols, rows int) {
	line := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 1, W: 0.35})
	for c := 1; c < cols; c++ {
		x := topLeft.X + side*float32(c)/float32(cols)
		dl.AddLineV(imgui.Vec2{X: x, Y: topLeft.Y}, imgui.Vec2{X: x, Y: topLeft.Y + side}, line, 1)
	}
	for r := 1; r < rows; r++ {
		y := topLeft.Y + side*float32(r)/float32(rows)
		dl.AddLineV(imgui.Vec2{X: topLeft.X, Y: y}, imgui.Vec2{X: topLeft.X + side, Y: y}, line, 1)
	}
}
