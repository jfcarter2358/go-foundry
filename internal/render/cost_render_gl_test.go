package render

// cost_render_gl_test.go — the passes made cheaper without changing the
// picture: the entities' and the viewmodel's depth prepass
// (entity_prepass.go) and the volumetrics reading a scene-target frame's
// depth where it is (volumetric.go). Each is drawn with its switch off and
// on, through DrawWorld, and the two pictures compared.
//
//	FOUNDRY_GL_TEST=1 go test -run 'EntityPrepass|VolumetricsRead' ./internal/render/

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/model"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// riggedCubePath saves a two-bone cube, its top half on the upper bone, and
// returns its path: a skinned model, which the prepass must deform exactly
// as the colour pass does.
func riggedCubePath(t *testing.T) string {
	t.Helper()
	doc := model.NewCubeDoc()
	lo := doc.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0, 0})
	hi := doc.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0.5, 0})
	doc.RenameBone(lo, "lo")
	doc.RenameBone(hi, "hi")
	var top, bottom []int
	for i, v := range doc.Verts {
		if v.Y() > 0 {
			top = append(top, i)
		} else {
			bottom = append(bottom, i)
		}
	}
	doc.AssignVertsToBone(bottom, lo, 1)
	doc.AssignVertsToBone(top, hi, 1)
	ci := doc.AddClip("idle")
	doc.Clip(ci).Duration = 1
	doc.Clip(ci).SetKey(lo, 0, model.IdentBonePose())
	doc.Texture = "brick"
	path := filepath.Join(t.TempDir(), "rig"+model.ModelExt)
	if err := model.SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}
	return path
}

// costTestEntity is an entity at pos wearing the model at path, scaled by s.
func costTestEntity(t *testing.T, id, path string, pos mgl32.Vec3, s float32) world.Entity {
	t.Helper()
	e := world.Entity{ID: id, Visible: true, Position: pos, Scale: mgl32.Vec3{1, 1, 1}}
	m := world.EntityModel{Name: "body", Path: path, Visible: true, Alpha: 1, Scale: mgl32.Vec3{s, s, s}}
	if err := m.Load(id); err != nil {
		t.Fatal(err)
	}
	e.Models = []world.EntityModel{m}
	t.Cleanup(func() { e.Models[0].Delete() })
	return e
}

// The prepass changes which surface is SHADED at a pixel, never which one is
// SEEN: entities in front of the world and of each other, one skinned and
// posed off its rest, and the player's viewmodel, draw the same picture with
// it as without it.
func TestEntityPrepassDrawsTheSamePicture(t *testing.T) {
	w := newVisWorld(t)
	lvl := visTestLevel(t, visTestRock(t))
	rig := riggedCubePath(t)
	ship := filepath.Join("models", "dropship.fmdl")

	cube := costTestEntity(t, "cube", rig, mgl32.Vec3{0.4, 0.1, 2.6}, 0.6)
	pose := []mgl32.Mat4{mgl32.Ident4(), mgl32.Translate3D(0.1, 0.2, 0).Mul4(mgl32.HomogRotate3DY(0.8))}
	for si := range cube.Models[0].Submeshes {
		cube.Models[0].Submeshes[si].BonePalette = pose
	}
	cube.Rotation = mgl32.Vec3{0.3, 0.7, 0}
	lvl.Entities = []world.Entity{
		costTestEntity(t, "ship", ship, mgl32.Vec3{-0.6, -0.3, 2.2}, 0.08),
		cube,
	}
	defer func() { lvl.Entities = nil }()

	prevPlayer := world.PlayerEntity
	defer func() { world.PlayerEntity = prevPlayer }()
	player := costTestEntity(t, "player", ship, mgl32.Vec3{}, 0.03)
	player.Models[0].Offset = mgl32.Vec3{0.15, -0.12, -0.5}
	player.Models[0].Rotation = mgl32.Vec3{0.2, 2.6, 0}
	world.PlayerEntity = &player

	prev := EntityPrepass
	defer func() { EntityPrepass = prev }()
	shots := map[bool][]uint8{}
	for _, on := range []bool{false, true} {
		EntityPrepass = on
		w.frame(lvl)
		shots[on] = w.frame(lvl)
		dumpPNG(fmt.Sprintf("entity_prepass_%v", on), shots[on], int(w.size))
	}
	if !entityPrepassProg.ok {
		t.Fatal("the prepass program did not build")
	}
	over8, over32, worst := pixelDiff(shots[false], shots[true])
	drawn, _ := diffPixels(shots[false], prepassBackground(t, w, lvl))
	t.Logf("entities and viewmodel cover %d pixels; %d differ past 8 levels, %d past 32, worst %d", drawn, over8, over32, worst)
	if drawn < 2000 {
		t.Fatalf("the entities cover only %d pixels: the test is not looking at them", drawn)
	}
	if over8 > 0 {
		t.Errorf("the prepass changed the picture: %d pixels past 8 levels, %d past 32 (worst %d)", over8, over32, worst)
	}
}

// prepassBackground is the frame without the entities or the viewmodel, to
// count the pixels they cover.
func prepassBackground(t *testing.T, w *visWorld, lvl *world.Level) []uint8 {
	t.Helper()
	ents, player := lvl.Entities, world.PlayerEntity
	lvl.Entities, world.PlayerEntity = nil, nil
	defer func() { lvl.Entities, world.PlayerEntity = ents, player }()
	w.frame(lvl)
	return w.frame(lvl)
}

// In a scene-target frame the volumetrics read scene.depth itself rather than
// a copy of it, and composite through a framebuffer of the scene's colour:
// the same picture to the bit, a cloud cut off where the world stands in it.
func TestVolumetricsReadTheSceneDepthDirectly(t *testing.T) {
	w := newVisWorld(t)
	lvl := visTestLevel(t, visTestRock(t))
	bank := world.NewVolume("bank", mgl32.Vec3{0, 0, 1.5})
	bank.HalfX, bank.HalfY, bank.HalfZ = 2, 1.2, 1.5
	c := world.DefaultVolumeCloud()
	c.Coverage, c.Density, c.Erosion, c.Scale = 0.8, 2, 0.3, 3
	c.Wind = mgl32.Vec3{}
	bank.Cloud = &c
	lvl.Volumes = []world.Volume{bank}
	defer func() { lvl.Volumes = nil }()

	prevDirect, prevQ := volumeDepthDirect, gfx.GIQuality
	defer func() { volumeDepthDirect, gfx.GIQuality = prevDirect, prevQ }()
	shots := map[bool][]uint8{}
	for _, direct := range []bool{false, true} {
		volumeDepthDirect = direct
		w.frame(lvl)
		shots[direct] = w.frame(lvl)
		if DebugStats.VolumeDraws == 0 {
			t.Fatal("the cloud bank was not marched")
		}
		dumpPNG(fmt.Sprintf("volume_direct_%v", direct), shots[direct], int(w.size))
	}
	if d, _ := diffPixels(shots[false], shots[true]); d > 0 {
		over8, _, worst := pixelDiff(shots[false], shots[true])
		t.Errorf("reading scene.depth directly changed %d pixels (%d past 8 levels, worst %d)", d, over8, worst)
	}
	lvl.Volumes = nil
	w.frame(lvl)
	if clear := w.frame(lvl); func() bool { d, _ := diffPixels(clear, shots[true]); return d == 0 }() {
		t.Error("the cloud bank changed nothing on screen: the test is not looking at it")
	}
}

// The material pass's half-resolution GI (gi_halfres.go) is the per-pixel
// answer, a little softer: on the virtual shadow map's sunlit field, and on
// the visibility buffer's parity scene with its rocks and edges, the picture
// with it is within a few levels of the one without, on average no brighter
// and no darker.
func TestGIHalfResMatchesPerPixel(t *testing.T) {
	w := newVSMWorld(t, true)
	prevQ, prevHalf := gfx.GIQuality, GIHalfRes
	gfx.GIQuality = gfx.GIMedium
	gi.failed = false
	t.Cleanup(func() { gfx.GIQuality, GIHalfRes = prevQ, prevHalf; gi.release() })
	for _, scene := range []struct {
		name     string
		lvl      func() *world.Level
		pos, dir mgl32.Vec3
	}{
		{"field", func() *world.Level { return vsmTestLevel(t) }, mgl32.Vec3{0, 1.7, 6}, mgl32.Vec3{0.1, -0.25, -1}},
		{"rocks", func() *world.Level { return visTestLevel(t, visTestRock(t)) }, mgl32.Vec3{0.2, 0.3, 4.5}, mgl32.Vec3{-0.03, -0.08, -1}},
	} {
		lvl := scene.lvl()
		lvl.Lights.Global.GI = world.DefaultGISettings()
		vsmLook(scene.pos, scene.dir)
		shots := map[bool][]uint8{}
		// A round first that is thrown away: what an earlier test left in
		// the frame's caches (its pages, its cloud shadow, its probes) is
		// gone from both of the compared ones.
		for _, on := range []bool{false, false, true} {
			GIHalfRes = on
			for i := 0; i < 12; i++ {
				DrawWorld(lvl, WorldDraw{Now: 1, Target: &FrameTarget{FBO: w.fbo, Viewport: [4]int32{0, 0, w.size, w.size}}})
			}
			DrawWorld(lvl, WorldDraw{Now: 1, Target: &FrameTarget{FBO: w.fbo, Viewport: [4]int32{0, 0, w.size, w.size}}})
			shots[on] = readTarget(w)
			if on != giHalf.active {
				t.Fatalf("%s: half-resolution GI active %v, want %v", scene.name, giHalf.active, on)
			}
			dumpPNG(fmt.Sprintf("gi_halfres_%s_%v", scene.name, on), shots[on], int(w.size))
		}
		mad, n := meanAbs(shots[false], shots[true])
		over8, over32, worst := pixelDiff(shots[false], shots[true])
		lum := func(px []uint8) float64 {
			s := 0.0
			for i := 0; i+3 < len(px); i += 4 {
				s += 0.2126*float64(px[i]) + 0.7152*float64(px[i+1]) + 0.0722*float64(px[i+2])
			}
			return s / float64(len(px)/4)
		}
		la, lb := lum(shots[false]), lum(shots[true])
		t.Logf("%s: mean abs %.3f over %d px; %d past 8 levels, %d past 32, worst %d; luminance %.2f -> %.2f",
			scene.name, mad, n, over8, over32, worst, la, lb)
		total := int(w.size * w.size)
		if math.Abs(lb-la) > 0.01*la+0.3 {
			t.Errorf("%s: half-resolution GI moved the mean luminance %.2f -> %.2f", scene.name, la, lb)
		}
		if over32 > total/200 {
			t.Errorf("%s: %d pixels differ by more than 32 levels (allowed %d)", scene.name, over32, total/200)
		}
	}
}

// The cheap march (half the steps, the offset turning each frame) converges
// under TAA to the full march's cloud: on a bank filling most of the view,
// after the history has settled, no brighter or darker on average and close
// at every pixel.
func TestVolumetricCheapMarchConvergesToTheFullOne(t *testing.T) {
	w := newVisWorld(t)
	TAAResolve = true
	lvl := visTestLevel(t, visTestRock(t))
	bank := world.NewVolume("bank", mgl32.Vec3{0, 0, 1.5})
	bank.HalfX, bank.HalfY, bank.HalfZ = 2, 1.2, 1.5
	c := world.DefaultVolumeCloud()
	c.Coverage, c.Density, c.Erosion, c.Scale = 0.8, 2, 0.3, 3
	c.Wind = mgl32.Vec3{}
	bank.Cloud = &c
	lvl.Volumes = []world.Volume{bank}
	defer func() { lvl.Volumes = nil }()
	prevCheap, prevQ := VolumetricCheapMarch, gfx.GIQuality
	defer func() { VolumetricCheapMarch, gfx.GIQuality = prevCheap, prevQ }()
	gfx.GIQuality = gfx.GIMedium // below High: the cheap march's tier
	shots := map[bool][]uint8{}
	for _, cheap := range []bool{false, true} {
		VolumetricCheapMarch = cheap
		ForgetTAAHistory()
		for i := 0; i < 48; i++ {
			w.frame(lvl)
		}
		if volumes.cheapMarch != cheap {
			t.Fatalf("cheap march %v, want %v", volumes.cheapMarch, cheap)
		}
		shots[cheap] = w.frame(lvl)
		dumpPNG(fmt.Sprintf("volume_cheap_%v", cheap), shots[cheap], int(w.size))
	}
	lum := func(px []uint8) float64 {
		s := 0.0
		for i := 0; i+3 < len(px); i += 4 {
			s += 0.2126*float64(px[i]) + 0.7152*float64(px[i+1]) + 0.0722*float64(px[i+2])
		}
		return s / float64(len(px)/4)
	}
	mad, _ := meanAbs(shots[false], shots[true])
	over8, over32, worst := pixelDiff(shots[false], shots[true])
	la, lb := lum(shots[false]), lum(shots[true])
	t.Logf("mean abs %.3f; %d px past 8 levels, %d past 32, worst %d; luminance %.2f -> %.2f", mad, over8, over32, worst, la, lb)
	if math.Abs(lb-la) > 0.01*la {
		t.Errorf("the cheap march moved the mean luminance %.2f -> %.2f", la, lb)
	}
	if total := int(w.size * w.size); over32 > total/100 {
		t.Errorf("%d pixels differ by more than 32 levels", over32)
	}
}

// readTarget reads the visWorld's target back.
func readTarget(w *visWorld) []uint8 {
	gl.BindFramebuffer(gl.FRAMEBUFFER, w.fbo)
	px := make([]uint8, w.size*w.size*4)
	glext.ReadPixels(0, 0, w.size, w.size, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

// timeFrames is the best of five batches' mean milliseconds for one frame of
// lvl at size square into fbo, after warm frames.
func timeFrames(lvl *world.Level, fbo uint32, size int32, frames int) float64 {
	draw := func() {
		DrawWorld(lvl, WorldDraw{Now: 1, Target: &FrameTarget{FBO: fbo, Viewport: [4]int32{0, 0, size, size}}})
	}
	for i := 0; i < 10; i++ {
		draw()
	}
	best := math.Inf(1)
	for batch := 0; batch < 5; batch++ {
		gl.Finish()
		start := time.Now()
		for i := 0; i < frames; i++ {
			draw()
		}
		gl.Finish()
		best = min(best, float64(time.Since(start).Microseconds())/1000/float64(frames))
	}
	return best
}

// The entity prepass's cost and saving, at 768 square: the dropship close up
// and a skinned cube in front of it, and the ship again as the viewmodel.
func TestRenderTimingEntities(t *testing.T) {
	skipUnlessTiming(t)
	w := newVisWorld(t)
	lvl := visTestLevel(t, visTestRock(t))
	ship := filepath.Join("models", "dropship.fmdl")
	cube := costTestEntity(t, "cube", riggedCubePath(t), mgl32.Vec3{0.4, 0.1, 3.2}, 0.6)
	lvl.Entities = []world.Entity{costTestEntity(t, "ship", ship, mgl32.Vec3{-0.2, -0.3, 2.6}, 0.2), cube}
	defer func() { lvl.Entities = nil }()
	prevPlayer := world.PlayerEntity
	defer func() { world.PlayerEntity = prevPlayer }()
	player := costTestEntity(t, "player", ship, mgl32.Vec3{}, 0.03)
	player.Models[0].Offset = mgl32.Vec3{0.15, -0.12, -0.5}
	player.Models[0].Rotation = mgl32.Vec3{0.2, 2.6, 0}
	world.PlayerEntity = &player
	prev := EntityPrepass
	defer func() { EntityPrepass = prev }()
	const size = 768
	fbo := w.target(size)
	for round := 0; round < 2; round++ {
		for _, on := range []bool{false, true} {
			EntityPrepass = on
			t.Logf("entity prepass %v: %.3f ms a frame", on, timeFrames(lvl, fbo, size, 20))
		}
	}
}

// The volumetrics' costs at 768 square, a cloud bank filling the view: the
// depth copy against reading scene.depth, and the full march against the
// cheap one (which only runs with TAA resolving, so TAA is on here).
func TestRenderTimingVolumetrics(t *testing.T) {
	skipUnlessTiming(t)
	w := newVisWorld(t)
	TAAResolve = true
	lvl := visTestLevel(t, visTestRock(t))
	bank := world.NewVolume("bank", mgl32.Vec3{0, 0, 1.5})
	bank.HalfX, bank.HalfY, bank.HalfZ = 4, 3, 3
	c := world.DefaultVolumeCloud()
	c.Coverage, c.Density, c.Erosion, c.Scale = 0.8, 2, 0.3, 3
	bank.Cloud = &c
	lvl.Volumes = []world.Volume{bank}
	defer func() { lvl.Volumes = nil }()
	prevDirect, prevCheap, prevQ := volumeDepthDirect, VolumetricCheapMarch, gfx.GIQuality
	defer func() { volumeDepthDirect, VolumetricCheapMarch, gfx.GIQuality = prevDirect, prevCheap, prevQ }()
	const size = 768
	fbo := w.target(size)
	for round := 0; round < 2; round++ {
		for _, c := range []struct {
			name          string
			direct, cheap bool
		}{{"copy, full march", false, false}, {"direct, full march", true, false}, {"direct, cheap march", true, true}} {
			volumeDepthDirect, VolumetricCheapMarch = c.direct, c.cheap
			t.Logf("%s: %.3f ms a frame", c.name, timeFrames(lvl, fbo, size, 20))
		}
	}
}
