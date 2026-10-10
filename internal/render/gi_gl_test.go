package render

// gi_gl_test.go — live GI, on a GPU.
//
// GI is three compute programs and a sampling function, and every one of them
// can be wrong while everything still links: a distance off by a voxel, a
// probe whose rays never leave it, a bake whose indirect part is never taken
// back off. So each test builds a small level, runs the real update, and reads
// back what the GPU wrote — the field's distances, the probes' irradiance, a
// shaded pixel.
//
//	FOUNDRY_GL_TEST=1 go test -run GI ./internal/render/

import (
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/gltest"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// giTestSetup stands GI up at q with surfaces coloured by colors (texture name
// -> albedo; anything else is the untextured grey), and tears it down after.
func giTestSetup(t *testing.T, q gfx.GIQualityLevel, colors map[string]mgl32.Vec3) {
	t.Helper()
	gltest.Context(t)
	withRepoResources(t)
	prevQ, prevChosen, prevMean := gfx.GIQuality, gfx.GIQualityChosen, giTextureMean
	gfx.GIQuality, gfx.GIQualityChosen = q, true
	giTextureMean = func(name string) (mgl32.Vec3, bool) {
		c, ok := colors[name]
		return c, ok
	}
	gi.failed = false
	t.Cleanup(func() {
		gi.release()
		gfx.GIQuality, gfx.GIQualityChosen, giTextureMean = prevQ, prevChosen, prevMean
	})
}

// giRoom is a level of one hollow room, 10 x 4 x 10 from the origin, walls
// half a unit thick, lit by lights.
func giRoom(lights world.LightSet, wallTex map[string]string) *world.Level {
	ch := world.Chunk{ID: "room"}
	ch.Brushes = world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 4, 10}, 0.5, "grey")
	for i := range ch.Brushes {
		if tex, ok := wallTex[ch.Brushes[i].ID]; ok {
			for f := range ch.Brushes[i].Faces {
				ch.Brushes[i].Faces[f].Texture = tex
			}
		}
	}
	lights.Global.GI = world.DefaultGISettings()
	lights.Global.Wrap = world.DefaultLightWrap
	return &world.Level{Chunks: []world.Chunk{ch}, Lights: lights}
}

func giRunFrames(t *testing.T, lvl *world.Level, cam mgl32.Vec3, frames int) {
	t.Helper()
	gfx.Cam.Position = cam
	for i := 0; i < frames; i++ {
		UpdateGI(lvl, WorldDraw{})
	}
	if !gi.active {
		t.Fatal("GI did not run")
	}
	gl.Finish()
}

// giFieldAt reads level l's stored distance at world point p.
func giFieldAt(t *testing.T, l int, p mgl32.Vec3) float32 {
	t.Helper()
	lv := &gi.field.levels[l]
	data := make([]float32, giSdfRes*giSdfRes*giSdfRes)
	gl.BindTexture(gl.TEXTURE_3D, lv.sdf)
	glext.GetTexImage(gl.TEXTURE_3D, 0, gl.RED, gl.FLOAT, gl.Ptr(&data[0]))
	gl.BindTexture(gl.TEXTURE_3D, 0)
	var v [3]int
	for k := 0; k < 3; k++ {
		w := int(math.Floor(float64(p[k] / lv.voxel)))
		v[k] = ((w % giSdfRes) + giSdfRes) % giSdfRes
	}
	return data[(v[2]*giSdfRes+v[1])*giSdfRes+v[0]]
}

// giOctEncode is light_model.glsl's.
func giOctEncode(n mgl32.Vec3) (float32, float32) {
	s := float32(math.Abs(float64(n[0])) + math.Abs(float64(n[1])) + math.Abs(float64(n[2])))
	n = n.Mul(1 / s)
	ex, ey := n[0], n[2]
	if n[1] < 0 {
		sx, sy := float32(1), float32(1)
		if ex < 0 {
			sx = -1
		}
		if ey < 0 {
			sy = -1
		}
		ex, ey = (1-float32(math.Abs(float64(n[2]))))*sx, (1-float32(math.Abs(float64(n[0]))))*sy
	}
	return ex, ey
}

// giIrradianceAt reads cascade 0's probe at world index wi, facing dir.
func giIrradianceAt(t *testing.T, wi [3]int, dir mgl32.Vec3) mgl32.Vec3 {
	t.Helper()
	w, h := giDimX*giDimZ*giIrrTile, giDimY*GIMaxCascades*giIrrTile
	data := make([]float32, w*h*3)
	gl.BindTexture(gl.TEXTURE_2D, gi.irr)
	glext.GetTexImage(gl.TEXTURE_2D, 0, gl.RGB, gl.FLOAT, gl.Ptr(&data[0]))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	dims := [3]int{giDimX, giDimY, giDimZ}
	var slot [3]int
	for k := 0; k < 3; k++ {
		slot[k] = ((wi[k] % dims[k]) + dims[k]) % dims[k]
	}
	tx, ty := slot[0]+slot[2]*giDimX, slot[1]
	ox, oy := giOctEncode(dir.Normalize())
	inner := giIrrTile - 2
	px := tx*giIrrTile + 1 + min(int((ox*0.5+0.5)*float32(inner)), inner-1)
	py := ty*giIrrTile + 1 + min(int((oy*0.5+0.5)*float32(inner)), inner-1)
	i := (py*w + px) * 3
	return mgl32.Vec3{data[i], data[i+1], data[i+2]}
}

func TestGIProgramsBuildAtEveryTier(t *testing.T) {
	giTestSetup(t, gfx.GILow, nil)
	lvl := giRoom(world.LightSet{}, nil)
	for _, q := range []gfx.GIQualityLevel{gfx.GILow, gfx.GIMedium, gfx.GIHigh} {
		gfx.GIQuality = q
		giRunFrames(t, lvl, mgl32.Vec3{5, 2, 5}, 2)
		if gi.tier != q {
			t.Errorf("GI is at %v, want %v", gi.tier, q)
		}
	}
}

func TestGIFieldMeasuresDistanceToABrush(t *testing.T) {
	giTestSetup(t, gfx.GIMedium, nil)
	ch := world.Chunk{ID: "box"}
	ch.Brushes = []world.Brush{world.NewBoxBrush("box", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{2, 2, 2}, "")}
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	lvl.Lights.Global.GI = world.DefaultGISettings()
	giRunFrames(t, lvl, mgl32.Vec3{0, 5, 0}, 1)

	voxel := gi.field.levels[0].voxel
	if voxel != 0.25 {
		t.Fatalf("level 0 voxel is %v, want 0.25 at the default spacing", voxel)
	}
	for _, c := range []struct {
		name string
		p    mgl32.Vec3
		want float32
	}{
		// Voxel centres, so the stored value is exactly the distance there.
		{"above the top face", mgl32.Vec3{0.125, 2.625, 0.125}, 0.625},
		{"inside, near the top", mgl32.Vec3{0.125, 1.625, 0.125}, -0.375},
		{"beside the +X face", mgl32.Vec3{2.375, 0.125, 0.125}, 0.375},
		{"out past the band", mgl32.Vec3{0.125, 4.125, 0.125}, giSdfBand * 0.25},
	} {
		if got := giFieldAt(t, 0, c.p); math.Abs(float64(got-c.want)) > 0.02 {
			t.Errorf("%s: field is %.3f, want %.3f", c.name, got, c.want)
		}
	}
}

func TestGISealedRoomIsDarkAndAnOpenOneSeesTheSky(t *testing.T) {
	giTestSetup(t, gfx.GIMedium, nil)
	sky := world.LightSet{Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Hemisphere: 0}}
	centre := [3]int{2, 1, 2} // the probe at (4, 2, 4)

	sealed := giRoom(sky, nil)
	giRunFrames(t, sealed, mgl32.Vec3{5, 2, 5}, 20)
	up := giIrradianceAt(t, centre, mgl32.Vec3{0, 1, 0})
	if up.Len() > 0.05 {
		t.Errorf("a sealed room with no light in it reads %v looking up; it should be dark", up)
	}

	open := giRoom(sky, nil)
	for i, b := range open.Chunks[0].Brushes {
		if b.ID == "room_ceiling" {
			open.Chunks[0].Brushes = append(open.Chunks[0].Brushes[:i], open.Chunks[0].Brushes[i+1:]...)
			break
		}
	}
	giRunFrames(t, open, mgl32.Vec3{5, 2, 5}, 20)
	up = giIrradianceAt(t, centre, mgl32.Vec3{0, 1, 0})
	down := giIrradianceAt(t, centre, mgl32.Vec3{0, -1, 0})
	if up.X() < 0.3 {
		t.Errorf("an open room reads %v looking up at the sky; want at least 0.3", up)
	}
	if down.X() >= up.X() {
		t.Errorf("looking down at the floor (%v) is as bright as looking up at the sky (%v)", down, up)
	}
}

func TestGIRedWallTintsTheLightNearIt(t *testing.T) {
	giTestSetup(t, gfx.GIMedium, map[string]mgl32.Vec3{
		"grey": {0.5, 0.5, 0.5},
		"red":  {0.9, 0.1, 0.1},
	})
	lamp := world.LightSet{Points: []world.PointLight{{
		Position: mgl32.Vec3{5, 2, 5}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1.5, Radius: 20,
	}}}
	lvl := giRoom(lamp, map[string]string{"room_wall_posx": "red"})
	giRunFrames(t, lvl, mgl32.Vec3{5, 2, 5}, 30)

	probe := [3]int{4, 1, 2} // (8, 2, 4): two units from the red wall
	toRed := giIrradianceAt(t, probe, mgl32.Vec3{1, 0, 0})
	away := giIrradianceAt(t, probe, mgl32.Vec3{-1, 0, 0})
	if toRed.Len() < 0.02 {
		t.Fatalf("no bounce light reached the probe at all: %v", toRed)
	}
	ratio := func(c mgl32.Vec3) float32 { return c.X() / max(c.Y(), 1e-4) }
	if ratio(toRed) < 2 {
		t.Errorf("facing the red wall the probe reads %v (red/green %.2f); want it clearly red", toRed, ratio(toRed))
	}
	if ratio(away) > ratio(toRed)*0.6 {
		t.Errorf("facing away (%v) is nearly as red as facing the wall (%v)", away, toRed)
	}
}

func TestGIKeepsBakedDirectLightAndDropsBakedIndirect(t *testing.T) {
	p := newNormalProbe(t)
	gi.failed = false
	defer gi.release()
	// No ambient, no lights: with GI on and no cascades, ambientLight is the
	// flat ambient, here zero — so the baked surface is lit by exactly its
	// direct part.
	world.UploadLightsTo(gfx.Program, world.LightSet{}, false)
	UploadPointShadowUniformsTo(gfx.Program, world.LightSet{})
	gl.UseProgram(gfx.Program)
	gi.bindAtlases(func(n string) int32 { return world.ProgUniformLoc(gfx.Program, n+"\x00") })
	set := func(on int32) {
		gl.UseProgram(gfx.Program)
		gl.Uniform1i(world.ProgUniformLoc(gfx.Program, "giEnabled\x00"), on)
		gl.Uniform1i(world.ProgUniformLoc(gfx.Program, "giCascades\x00"), 0)
		gl.Uniform1f(world.ProgUniformLoc(gfx.Program, "giIntensity\x00"), 1)
		gl.ActiveTexture(gl.TEXTURE0)
	}
	const total, indirect, albedoRed = 0.6, 0.4, 0.5
	row := []float32{total, total, total, 0, indirect, indirect, indirect}

	set(0)
	nearlyByte(t, "GI off: the whole bake", uint8(p.bakedQuadRed(row)*255), albedoRed*total)
	set(1)
	nearlyByte(t, "GI on: the bake less its indirect part", uint8(p.bakedQuadRed(row)*255), albedoRed*(total-indirect))
	set(0)
}

// giSurfaceAt reads cascade 0's nearest-surface plane for the probe at world
// index wi: the normal facing the probe and the distance to the surface.
func giSurfaceAt(t *testing.T, wi [3]int) mgl32.Vec4 {
	t.Helper()
	w, h := giDimX*giDimZ, giDimY*GIMaxCascades
	data := make([]float32, w*h*4)
	gl.BindTexture(gl.TEXTURE_2D, gi.surf)
	glext.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.FLOAT, gl.Ptr(&data[0]))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	dims := [3]int{giDimX, giDimY, giDimZ}
	var slot [3]int
	for k := 0; k < 3; k++ {
		slot[k] = ((wi[k] % dims[k]) + dims[k]) % dims[k]
	}
	i := ((slot[1])*w + slot[0] + slot[2]*giDimX) * 4
	return mgl32.Vec4{data[i], data[i+1], data[i+2], data[i+3]}
}

// A probe just under a thin floor records the floor as the surface it is up
// against, facing it — which is what lets the shading refuse it for points on
// top of that floor, where its light would be the void under it (level1's
// hangar: a 25 cm deck over a 100 m drop lit every wall foot from below). One
// out in the open records nothing, and is refused nothing.
func TestGIProbeUnderAThinFloorKnowsTheFloorIsBetween(t *testing.T) {
	giTestSetup(t, gfx.GIMedium, nil)
	sky := world.LightSet{Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}}
	// Spacing 2: probes on even coordinates. The deck's underside is half a
	// unit over the probe row at y = 0.
	deck := world.NewBoxBrush("deck", mgl32.Vec3{0, 0.625, 0}, mgl32.Vec3{20, 0.125, 20}, "grey")
	lvl := &world.Level{Chunks: []world.Chunk{{ID: "deck", Brushes: []world.Brush{deck}}}, Lights: sky}
	lvl.Lights.Global.GI = world.DefaultGISettings()
	giRunFrames(t, lvl, mgl32.Vec3{1, 2, 1}, 4)

	under := giSurfaceAt(t, [3]int{0, 0, 0}) // the probe at (0, 0, 0)
	if under.Vec3().Len() < 0.5 {
		t.Fatalf("the probe under the deck recorded no surface: %v", under)
	}
	if under.Y() > -0.8 {
		t.Errorf("the probe under the deck faces %v; the deck is above it, so its normal should point down", under.Vec3())
	}
	if d := under.W(); d < 0.3 || d > 0.7 {
		t.Errorf("the probe under the deck puts it %.2f away; want about 0.5", d)
	}
	open := giSurfaceAt(t, [3]int{0, -4, 0}) // (0, -8, 0): far below anything
	if open.Vec3().Len() > 0.01 {
		t.Errorf("a probe in open air recorded a surface: %v", open)
	}
}
