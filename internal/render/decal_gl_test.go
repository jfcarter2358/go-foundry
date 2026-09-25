package render

// decal_gl_test.go — the two things about decals that only a GPU can answer.
//
//	FOUNDRY_GL_TEST=1 go test -run 'TestTint|TestDecal' ./internal/render/
//
// One: the tint uniform. It is a bias from white, so that the zero Vec3 every
// other surface in the engine carries means "no tint" rather than "black" — and
// getting that backwards would paint the entire level black, which is a
// catastrophic failure that no CPU test can see because the whole trick lives in
// the uniform's encoding and the shader's `1.0 + bias`.
//
// Two: the pass draws. RenderDecals owns blend, depth-mask and polygon-offset
// state and puts all of it back; a decal whose mesh was built but which lands on
// no pixels, or which leaves the depth mask off for the sky that follows it, is
// invisible in every unit test and obvious in the game.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// The zero Tint has to be white. This is the whole of the bias-from-white trick,
// and it is the case every existing surface in the engine hits.
func TestTintZeroValueIsWhiteNotBlack(t *testing.T) {
	p := newNormalProbe(t)

	// The untextured normal colour stands in for the albedo: facing the camera it
	// is (0.5, 0.5, 1) under flat white light.
	plain := p.shadeQuadWith(gfx.Geometry{Alpha: 1}, "", mgl32.Ident4())
	nearlyByte(t, "untinted, red", plain[0], 0.5)
	nearlyByte(t, "untinted, blue", plain[2], 1.0)

	// And the same geometry with Tint explicitly white agrees with it, which is
	// what says the zero and the explicit white take the same path.
	white := p.shadeQuadWith(gfx.Geometry{Alpha: 1, Tint: mgl32.Vec3{1, 1, 1}}, "", mgl32.Ident4())
	for i := range 3 {
		if d := int(white[i]) - int(plain[i]); d > 2 || d < -2 {
			t.Errorf("channel %d: an explicitly white tint gave %d where the zero value gave %d",
				i, white[i], plain[i])
		}
	}
}

// And a tint that is not white multiplies the albedo, per channel.
func TestTintMultipliesTheAlbedo(t *testing.T) {
	p := newNormalProbe(t)

	// Albedo (0.5, 0.5, 1) times (1, 0.5, 0.25).
	tinted := p.shadeQuadWith(gfx.Geometry{Alpha: 1, Tint: mgl32.Vec3{1, 0.5, 0.25}}, "", mgl32.Ident4())
	nearlyByte(t, "tinted, red", tinted[0], 0.5)
	nearlyByte(t, "tinted, green", tinted[1], 0.25)
	nearlyByte(t, "tinted, blue", tinted[2], 0.25)

	// And it does not stick: the next untinted draw is untinted. This is the
	// failure applyTintUniform's "write it on every draw" rule exists to stop —
	// one red decal recolouring every wall drawn after it.
	again := p.shadeQuadWith(gfx.Geometry{Alpha: 1}, "", mgl32.Ident4())
	nearlyByte(t, "untinted after tinted, green", again[1], 0.5)
	nearlyByte(t, "untinted after tinted, blue", again[2], 1.0)
}

// The pass, end to end: a decal on a wall reaches the framebuffer, and does so
// through the surface the wall already wrote to depth — which is what the
// polygon offset is for.
func TestRenderDecalsDrawsOntoAWall(t *testing.T) {
	p := newNormalProbe(t)

	// A wall filling the view at z = 0, facing the camera at z = 4.
	lvl := decalGLLevel(t)

	// The wall alone, so there is something for the decal to be compared against
	// and something in the depth buffer for it to sit on.
	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.DEPTH_TEST)
	gl.ClearColor(0, 1, 0, 1) // a colour the shader cannot produce
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	drawGLWall(lvl)
	wall := readCentre()
	if wall[1] == 255 && wall[0] == 0 {
		t.Fatal("the wall itself did not draw, so nothing here is measuring a decal")
	}

	// Now the decal over it: black, fully opaque, so the centre pixel goes dark
	// if and only if the decal actually rasterised.
	d := world.NewDecal("mark", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, 1})
	d.Width, d.Height, d.Depth = 2, 2, 1
	d.Unlit = true                           // its own colour, so the light cannot be blamed
	d.Tint = mgl32.Vec3{0.001, 0.001, 0.001} // near-black; the exact zero reads as "unset"
	lvl.Decals = append(lvl.Decals, d)

	RenderDecals(lvl, nil)
	over := readCentre()

	if DebugStats.Decals != 1 {
		t.Fatalf("decals drawn: got %d, want 1", DebugStats.Decals)
	}
	if !(int(over[0]) < int(wall[0]) && int(over[2]) < int(wall[2])) {
		t.Errorf("the decal did not darken the wall it was drawn over: wall %v, after %v", wall, over)
	}

	// And the pass put the depth mask back. Left off, the sky and every
	// transparent surface after this point would stop depth-testing correctly.
	var mask int32
	gl.GetIntegerv(gl.DEPTH_WRITEMASK, &mask)
	if mask == 0 {
		t.Error("RenderDecals left the depth mask off")
	}
	var blend bool
	gl.GetBooleanv(gl.BLEND, &blend)
	if blend {
		t.Error("RenderDecals left blending on")
	}
	var offset bool
	gl.GetBooleanv(gl.POLYGON_OFFSET_FILL, &offset)
	if offset {
		t.Error("RenderDecals left the polygon offset on")
	}
	_ = p
}

// A MULTIPLY decal must never brighten the wall it lies on, and the case that
// caught this is a BRIGHT texel at partial alpha.
//
// The GL multiply equation can express `src * dst` and cannot express
// `dst * mix(1, src, a)`, so an implementation that left the alpha to the blender
// (source factor DST_COLOR, destination factor ONE_MINUS_SRC_ALPHA) came out at
// `dst * (src + 1 - a)`: for a white texel at half alpha, one and a half times the
// wall. That reads as a glowing halo round every scorch mark, and only a GPU can
// see it.
//
// Two things about the setup are load-bearing. The light is turned DOWN, so the
// wall is dim enough that a channel multiplied by 1.5 is not simply clamped at
// 255 — at full brightness the bug is invisible. And the decal names no texture,
// so it is painted with its own normal (0.5, 0.5, 1): a real, bright albedo with
// no texture needing to be resident.
func TestMultiplyDecalNeverBrightensTheWall(t *testing.T) {
	newNormalProbe(t)
	lvl := decalGLLevel(t)

	var ls world.LightSet
	ls.Global.Color = mgl32.Vec3{1, 1, 1}
	ls.Global.Brightness = 0.4
	world.UploadLightsTo(gfx.Program, ls, false)

	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.DEPTH_TEST)
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	drawGLWall(lvl)
	wall := readCentre()
	if wall[2] > 200 {
		t.Fatalf("the wall came out at blue %d — too bright for a 1.5x error to show "+
			"below saturation, so this test would not catch the bug", wall[2])
	}

	d := world.NewDecal("mask", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, 1})
	d.Width, d.Height, d.Depth = 2, 2, 1
	d.Blend = gfx.DecalBlendMultiply
	d.Texture = "" // painted with its own normal: a bright albedo, no texture needed
	d.Alpha = 0.5
	lvl.Decals = append(lvl.Decals, d)

	RenderDecals(lvl, nil)
	over := readCentre()

	for i := range 3 {
		if int(over[i]) > int(wall[i])+3 {
			t.Errorf("channel %d: a bright multiply mask BRIGHTENED the wall, %d -> %d "+
				"— the alpha is being left to the blend equation instead of folded "+
				"toward white in the shader", i, wall[i], over[i])
		}
	}

	// And a near-black mask at full alpha does black the wall out, so the path is
	// doing something rather than nothing.
	world.ReleaseDecals(lvl)
	lvl.Decals = lvl.Decals[:0]
	black := world.NewDecal("black", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, 1})
	black.Width, black.Height, black.Depth = 2, 2, 1
	black.Blend = gfx.DecalBlendMultiply
	black.Texture = ""
	black.Tint = mgl32.Vec3{0.001, 0.001, 0.001}
	lvl.Decals = append(lvl.Decals, black)

	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	drawGLWall(lvl)
	RenderDecals(lvl, nil)
	dark := readCentre()
	for i := range 3 {
		if int(dark[i]) > 12 {
			t.Errorf("channel %d: a black multiply mask left %d, want near 0", i, dark[i])
		}
	}
}

// A decal outside the frustum costs nothing. The cull is per decal rather than
// per piece, so this also pins down that the whole thing goes at once.
func TestRenderDecalsCullsWhatIsBehindTheCamera(t *testing.T) {
	newNormalProbe(t)
	lvl := decalGLLevel(t)

	d := world.NewDecal("mark", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, 1})
	d.Width, d.Height, d.Depth = 2, 2, 1
	lvl.Decals = append(lvl.Decals, d)
	world.SyncDecals(lvl, 0)
	if len(lvl.Decals[0].Pieces) == 0 {
		t.Fatal("the decal built no mesh, so the cull is not what is being tested")
	}

	// Looking the other way.
	gfx.Cam.Direction = mgl32.Vec3{0, 0, 1}
	gfx.UpdateCamMatrix()
	frustum := ExtractFrustum(gfx.Projection.Mul4(gfx.Cam.Matrix))
	RenderDecals(lvl, &frustum)
	if DebugStats.Decals != 0 {
		t.Errorf("a decal behind the camera was drawn: %d", DebugStats.Decals)
	}
}

// ---------- helpers ----------

// decalGLLevel is one chunk holding one wall brush at z = 0, its +Z face toward
// the camera the probe set up, with its mesh built and uploaded.
func decalGLLevel(t *testing.T) *world.Level {
	t.Helper()
	b := world.NewBoxBrush("wall", mgl32.Vec3{0, 0, -0.5}, mgl32.Vec3{4, 4, 0.5}, "")
	if err := b.Load(); err != nil {
		t.Fatalf("brush load: %v", err)
	}
	ch := world.Chunk{ID: "room", Brushes: []world.Brush{b}}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	t.Cleanup(func() { world.ReleaseDecals(lvl) })
	return lvl
}

// drawGLWall draws the wall's faces straight through the world program. The
// chunk batches are not used: this test wants something in the colour and depth
// buffers, not a test of batching.
func drawGLWall(lvl *world.Level) {
	for bi := range lvl.Chunks[0].Brushes {
		m := lvl.Chunks[0].Brushes[bi].DrawMesh()
		for fi := range m.Faces {
			g := &m.Faces[fi].Geometry
			if len(g.Vertices) == 0 {
				continue
			}
			// A face's Geometry is CPUOnly (its chunk's batch owns the buffers),
			// so it has to be uploaded here to be drawn on its own.
			var own gfx.Geometry
			own.Alpha = 1
			verts := make([][]float32, 0, len(g.Vertices)/5)
			idx := make([]int, 0, len(g.Vertices)/5)
			for i := 0; i+4 < len(g.Vertices); i += 5 {
				verts = append(verts, []float32{g.Vertices[i], g.Vertices[i+1], g.Vertices[i+2],
					g.Vertices[i+3], g.Vertices[i+4]})
				idx = append(idx, len(verts)-1)
			}
			if own.Load(verts, idx, "", mgl32.Vec3{}) != nil {
				continue
			}
			own.DrawWithTransform(mgl32.Ident4())
			own.Delete()
		}
	}
}

func readCentre() [4]uint8 {
	var px [4]uint8
	gl.ReadPixels(skyTestSize/2, skyTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}
