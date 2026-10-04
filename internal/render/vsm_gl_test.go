package render

// vsm_gl_test.go — Phase 5 (docs/nanite/PHASE_5.md): the sun's virtual
// shadow map, through DrawWorld.
//
//   - It shadows where the cascades do, and without their acne.
//   - Its shadow edges stay sharp far away, where a cascade's soften.
//   - Its cache is right: a still scene draws no page; a moved chunk, prop
//     or cooked rock redraws a few, and the picture is a fresh map's.
//   - A compiled level's baked sun is re-shadowed by it as by the cascades.
//   - The clouds' shadow still falls on top of it.
//   - A frame without a scene target (MSAA) keeps the cascades.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 300s -run 'VSM' ./internal/render/

import (
	"fmt"
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// vsmTestSun is the scene's sun, shadowing.
func vsmTestSun(dynamic bool) world.DirectionalLight {
	sun := world.DirectionalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Yaw: 30, Pitch: -40,
		CastShadows: true, ShadowStrength: 1, Dynamic: dynamic}
	sun.SyncDirection()
	return sun
}

// vsmChunk is a chunk of box brushes, each [centre, half size].
func vsmChunk(t *testing.T, id, tex string, boxes ...[2]mgl32.Vec3) world.Chunk {
	t.Helper()
	ch := world.Chunk{ID: id, Scale: 1}
	for _, b := range boxes {
		br := world.NewBoxBrush("b", b[0], b[1], tex)
		if err := br.Load(); err != nil {
			t.Fatal(err)
		}
		ch.Brushes = append(ch.Brushes, br)
	}
	ch.RecomputeBounds()
	return ch
}

// vsmTestLevel is a ground slab with casters standing on it — a cube at the
// origin, a thin pole, a row of posts running away from the camera to past
// 80 m — each kind in a chunk of its own, so a test can move one, lit by a
// dim ambient and a shadowing sun.
func vsmTestLevel(t *testing.T) *world.Level {
	t.Helper()
	posts := [][2]mgl32.Vec3{}
	for k := 0; k < 6; k++ {
		posts = append(posts, [2]mgl32.Vec3{{-2, 1, -8 - 14*float32(k)}, {0.3, 1, 0.3}})
	}
	lvl := &world.Level{Chunks: []world.Chunk{
		vsmChunk(t, "ground", "concrete", [2]mgl32.Vec3{{0, -0.1, -40}, {30, 0.1, 60}}),
		vsmChunk(t, "cube", "brick", [2]mgl32.Vec3{{0, 0.5, 0}, {0.5, 0.5, 0.5}}),
		vsmChunk(t, "pole", "brick", [2]mgl32.Vec3{{2.5, 1.5, -1.5}, {0.05, 1.5, 0.05}}),
		vsmChunk(t, "posts", "brick", posts...),
	}}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.25,
		Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
	lvl.Lights.Directional = []world.DirectionalLight{vsmTestSun(true)}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	world.UploadLights(lvl.Lights)
	t.Cleanup(func() {
		ReleaseChunkBatches(lvl)
		world.DeleteScenery(lvl)
		world.UploadLights(world.LightSet{})
	})
	return lvl
}

// newVSMWorld is newVisWorld with the virtual map's switch pinned and its
// state forgotten before and after.
func newVSMWorld(t *testing.T, on bool) *visWorld {
	t.Helper()
	w := newVisWorld(t)
	prev := NaniteVSM
	NaniteVSM = on
	vsm.reset()
	t.Cleanup(func() {
		NaniteVSM = prev
		vsm.reset()
	})
	return w
}

// vsmLook places the camera.
func vsmLook(pos, dir mgl32.Vec3) {
	gfx.Cam.Position, gfx.Cam.Direction = pos, dir.Normalize()
	gfx.UpdateCamMatrix()
}

// vsmSettled draws frames with the virtual map on until it has drawn no page
// for two frames running — its requests read back and served — and returns
// the last. At most 12.
func vsmSettled(t *testing.T, w *visWorld, lvl *world.Level) []uint8 {
	t.Helper()
	still := 0
	var px []uint8
	for i := 0; i < 12; i++ {
		px = w.frame(lvl)
		if !DebugStats.VSMOn {
			t.Fatalf("frame %d: the virtual map was not used", i)
		}
		if DebugStats.VSMDrawn == 0 {
			still++
			if still == 2 {
				return px
			}
		} else {
			still = 0
		}
	}
	t.Fatalf("the virtual map was still drawing pages after 12 frames (%d this frame)", DebugStats.VSMDrawn)
	return px
}

// shadowMask is per pixel whether lit is darker than unshadowed by more than
// by levels (the largest channel).
func shadowMask(unshadowed, lit []uint8, by int) []bool {
	m := make([]bool, len(lit)/4)
	for i := range m {
		d := 0
		for c := 0; c < 3; c++ {
			d = max(d, int(unshadowed[4*i+c])-int(lit[4*i+c]))
		}
		m[i] = d > by
	}
	return m
}

// maskStats is the masks' intersection over union and their sizes.
func maskStats(a, b []bool) (iou float64, na, nb int) {
	in, un := 0, 0
	for i := range a {
		if a[i] {
			na++
		}
		if b[i] {
			nb++
		}
		if a[i] && b[i] {
			in++
		}
		if a[i] || b[i] {
			un++
		}
	}
	if un == 0 {
		return 1, na, nb
	}
	return float64(in) / float64(un), na, nb
}

// unshadowedFrame draws lvl with its sun's shadow off.
func unshadowedFrame(w *visWorld, lvl *world.Level) []uint8 {
	lvl.Lights.Directional[0].CastShadows = false
	world.UploadLights(lvl.Lights)
	w.frame(lvl)
	px := w.frame(lvl)
	lvl.Lights.Directional[0].CastShadows = true
	world.UploadLights(lvl.Lights)
	return px
}

// The virtual map shadows the cube, the pole and the posts where the
// cascades do — the full shadows, past 48 levels darker than the unshadowed
// frame, overlap by most of their area — and leaves the lit ground as lit
// as the unshadowed frame. (The cascades leave a ring pattern of partial
// shadow on it at this sun, which is measured and not required.)
func TestVSMShadowsWhereTheCascadesDo(t *testing.T) {
	w := newVSMWorld(t, false)
	lvl := vsmTestLevel(t)
	for pi, p := range []struct{ pos, dir mgl32.Vec3 }{
		{mgl32.Vec3{0, 3, 7}, mgl32.Vec3{0, -0.35, -1}},
		{mgl32.Vec3{4, 6, 4}, mgl32.Vec3{-0.6, -0.8, -0.6}},
	} {
		vsmLook(p.pos, p.dir)
		NaniteVSM = false
		plain := unshadowedFrame(w, lvl)
		w.frame(lvl)
		casc := w.frame(lvl)
		if DebugStats.VSMOn {
			t.Fatal("the virtual map ran with its switch off")
		}
		NaniteVSM = true
		vsm.reset()
		virt := vsmSettled(t, w, lvl)
		dumpPNG(fmt.Sprintf("vsm%d_plain", pi), plain, int(w.size))
		dumpPNG(fmt.Sprintf("vsm%d_casc", pi), casc, int(w.size))
		dumpPNG(fmt.Sprintf("vsm%d_virt", pi), virt, int(w.size))
		mc, mv := shadowMask(plain, casc, 48), shadowMask(plain, virt, 48)
		iou, nc, nv := maskStats(mc, mv)
		// Acne: open ground — not a caster (the ground alone draws it the
		// same), and more than 2 pixels from either shadow — darkened at
		// all. (A caster's own faces turned from the sun are in its shadow
		// under the virtual map, rightly; the cascades' bias lets them be.)
		ground := groundOnly(t, w, lvl)
		near := dilate(orMask(mc, mv), int(w.size), 2)
		acneC, acneV, lit := 0, 0, 0
		cs, vs := shadowMask(plain, casc, 4), shadowMask(plain, virt, 4)
		for i := range mc {
			if near[i] || !samePixel(plain, ground, i, 2) {
				continue
			}
			lit++
			if cs[i] {
				acneC++
			}
			if vs[i] {
				acneV++
			}
		}
		t.Logf("pose %d: shadowed %d (cascades) / %d (virtual), overlap %.3f; of %d lit pixels %d (cascades) / %d (virtual) darkened by more than 4 levels",
			pi, nc, nv, iou, lit, acneC, acneV)
		if nc < 200 || nv < 200 {
			t.Fatalf("pose %d: hardly any shadow (%d / %d pixels)", pi, nc, nv)
		}
		if iou < 0.85 {
			t.Errorf("pose %d: the shadows overlap by %.3f, want 0.85", pi, iou)
		}
		if acneV > lit/100 {
			t.Errorf("pose %d: the virtual map darkens %d of %d lit pixels", pi, acneV, lit)
		}
	}
}

// groundOnly draws lvl's ground alone, unshadowed: the pixels where it
// matches the unshadowed frame are open ground.
func groundOnly(t *testing.T, w *visWorld, lvl *world.Level) []uint8 {
	t.Helper()
	chunks := lvl.Chunks
	lvl.Chunks = chunks[:1]
	defer func() {
		lvl.Chunks = chunks
		if err := world.RebuildIndexes(lvl); err != nil {
			t.Fatal(err)
		}
	}()
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	vsmOn := NaniteVSM
	NaniteVSM = false
	defer func() { NaniteVSM = vsmOn }()
	return unshadowedFrame(w, lvl)
}

func samePixel(a, b []uint8, i, tol int) bool {
	for c := 0; c < 3; c++ {
		if d := int(a[4*i+c]) - int(b[4*i+c]); d > tol || d < -tol {
			return false
		}
	}
	return true
}

func orMask(a, b []bool) []bool {
	out := make([]bool, len(a))
	for i := range a {
		out[i] = a[i] || b[i]
	}
	return out
}

// dilate grows a size-square mask by r pixels.
func dilate(m []bool, size, r int) []bool {
	out := make([]bool, len(m))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !m[y*size+x] {
				continue
			}
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					if xx, yy := x+dx, y+dy; xx >= 0 && yy >= 0 && xx < size && yy < size {
						out[yy*size+xx] = true
					}
				}
			}
		}
	}
	return out
}

// edgeWidth is how many pixels of row y (of a frame size wide) are partly
// shadowed: whose lit-to-unshadowed ratio — the albedo cancels out of it — is
// between 15% and 85% of the way from the row's full shadow to fully lit.
// full is how much the full shadow darkens, in levels.
func edgeWidth(plain, lit []uint8, size, y int) (width int, full int) {
	ratio := func(x int) (float64, int) {
		i := 4 * (y*size + x)
		p := int(plain[i]) + int(plain[i+1]) + int(plain[i+2])
		l := int(lit[i]) + int(lit[i+1]) + int(lit[i+2])
		return float64(l) / float64(max(p, 1)), (p - l) / 3
	}
	lo := 1.0
	for x := 0; x < size; x++ {
		r, d := ratio(x)
		lo = min(lo, r)
		full = max(full, d)
	}
	for x := 0; x < size; x++ {
		r, _ := ratio(x)
		if r > lo+0.15*(1-lo) && r < lo+0.85*(1-lo) {
			width++
		}
	}
	return width, full
}

// A shadow edge is as sharp seen from far away as from close up: a wall's
// shadow edge on the ground, looked straight down on from 4 m and from
// 120 m, is a couple of pixels wide either way under the virtual map, whose
// level follows the pixel; under the cascades the far one is several times
// wider — a coarse cascade's texel with its filter spread over it.
func TestVSMShadowStaysSharpFarAway(t *testing.T) {
	w := newVSMWorld(t, false)
	lvl := vsmTestLevel(t)
	// A wall 24 m long running along z at x = 0, z = -30: its shadow on the
	// ground, toward +x (the sun travels +x), has a long straight edge.
	lvl.Chunks[1] = vsmChunk(t, "cube", "brick", [2]mgl32.Vec3{{0, 2, -30}, {0.2, 2, 12}})
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	prevFOV := gfx.FieldOfView
	defer func() {
		gfx.FieldOfView = prevFOV
		gfx.SyncProjection()
	}()
	sun := lvl.Lights.Directional[0].Direction
	edgeX := 4 * sun[0] / -sun[1]
	type view struct {
		h, fov float32
	}
	views := []view{{4, 45}, {120, 5}}
	widths := map[bool][]int{}
	for _, on := range []bool{false, true} {
		for vi, v := range views {
			gfx.FieldOfView = v.fov
			gfx.SyncProjection()
			// Down, tilted a little along the edge (a view straight down has
			// no up for the camera's matrix).
			vsmLook(mgl32.Vec3{edgeX, v.h, -30 + 0.15*v.h}, mgl32.Vec3{0, -1, -0.15})
			NaniteVSM = false
			plain := unshadowedFrame(w, lvl)
			var lit []uint8
			if on {
				NaniteVSM = true
				vsm.reset()
				lit = vsmSettled(t, w, lvl)
			} else {
				w.frame(lvl)
				lit = w.frame(lvl)
			}
			dumpPNG(fmt.Sprintf("sharp_%v_%d", on, vi), lit, int(w.size))
			s := int(w.size)
			total, rows := 0, 0
			for y := s / 3; y < 2*s/3; y += 8 {
				wd, full := edgeWidth(plain, lit, s, y)
				if full < 40 {
					t.Fatalf("virtual %v, from %v m: no shadow edge on row %d (full darkening %d)", on, v.h, y, full)
				}
				total += wd
				rows++
			}
			widths[on] = append(widths[on], (total+rows/2)/rows)
		}
		t.Logf("virtual map %v: edge %d px wide from %v m, %d px from %v m", on, widths[on][0], views[0].h, widths[on][1], views[1].h)
	}
	v, c := widths[true], widths[false]
	if v[0] > 4 || v[1] > 4 {
		t.Errorf("the virtual map's edge is %d px from %v m and %d px from %v m; want at most 4 either way", v[0], views[0].h, v[1], views[1].h)
	}
	if c[1] < 2*max(v[1], 1) {
		t.Errorf("the cascades' far edge (%d px) is not wider than the virtual map's (%d px): the test is not measuring what it should", c[1], v[1])
	}
}

// The cache is right: a still frame draws no page; moving a chunk redraws a
// few pages — far fewer than the map holds — and the picture is the one a
// map drawn from nothing gives; and the same for a prop, a cooked rock (the
// static layer, drawn again whole) and an entity (the moving layer, drawn
// over the pages' static copies).
func TestVSMCachedPagesMatchAFreshMap(t *testing.T) {
	w := newVSMWorld(t, true)
	lvl := vsmTestLevel(t)
	rock := visTestRock(t)
	for i, gpu := range []bool{true, false} {
		s := world.NewScenery("rock", rock, mgl32.Vec3{-1.5 + 3*float32(i), 0.3, 1})
		s.NoNanite = !gpu
		s.Materials = map[string]string{"stone": "concrete", "moss": "concrete", "": "concrete"}
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		s.Scale = mgl32.Vec3{0.6, 0.6, 0.6}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	// And a walker: an entity, of the moving layer (a slab standing on the
	// ground, 1.8 m tall).
	var body gfx.Geometry
	h := float32(0.4)
	slab := [][]float32{{-h, 0, -h, 0, 0}, {h, 0, -h, 1, 0}, {h, 1.8, h, 1, 1}, {-h, 1.8, h, 0, 1}}
	if err := body.Load(slab, []int{0, 1, 2, 0, 2, 3}, "", mgl32.Vec3{}); err != nil {
		t.Fatal(err)
	}
	body.Alpha = 1
	t.Cleanup(body.Delete)
	lvl.Entities = []world.Entity{{ID: "walker", Visible: true, Position: mgl32.Vec3{-1.2, 0, -2},
		BoundHalfX: 0.5, BoundHalfY: 1, BoundHalfZ: 0.5,
		Models: []world.EntityModel{{Visible: true, Submeshes: []gfx.Geometry{body}}}}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	vsmLook(mgl32.Vec3{0, 3, 7}, mgl32.Vec3{0, -0.35, -1})
	vsmSettled(t, w, lvl)
	w.frame(lvl)
	if DebugStats.VSMDrawn != 0 {
		t.Fatalf("a still frame drew %d pages", DebugStats.VSMDrawn)
	}
	resident := DebugStats.VSMPages
	for _, mv := range []struct {
		what string
		move func()
		// lapse: the map starts over, every page it asks for redrawn.
		lapse bool
	}{
		{"the cube's chunk", func() { lvl.Chunks[1].Origin = lvl.Chunks[1].Origin.Add(mgl32.Vec3{0.7, 0, -0.4}) }, false},
		{"the classic rock", func() { lvl.Scenery[1].Position = lvl.Scenery[1].Position.Add(mgl32.Vec3{-0.5, 0, -0.6}) }, false},
		{"the cooked rock", func() { lvl.Scenery[0].Position = lvl.Scenery[0].Position.Add(mgl32.Vec3{0.4, 0.2, -0.8}) }, false},
		{"the walker", func() { lvl.Entities[0].Position = lvl.Entities[0].Position.Add(mgl32.Vec3{0.9, 0, 0.5}) }, false},
		// A frame drawn with the cascades in between (the switch, the
		// editor's own view): the map has lapsed, and starts over.
		{"the cube's chunk, while the map was off", func() {
			NaniteVSM = false
			lvl.Chunks[1].Origin = lvl.Chunks[1].Origin.Add(mgl32.Vec3{-0.5, 0, 0.6})
			w.frame(lvl)
			NaniteVSM = true
		}, true},
	} {
		mv.move()
		if err := world.RebuildIndexes(lvl); err != nil {
			t.Fatal(err)
		}
		w.frame(lvl)
		drawn := DebugStats.VSMDrawn
		cached := vsmSettled(t, w, lvl)
		vsm.reset()
		fresh := vsmSettled(t, w, lvl)
		o8, o32, worst := pixelDiff(cached, fresh)
		t.Logf("%s moved: %d pages redrawn of %d resident; cached vs fresh map: %d px past 8, %d past 32, worst %d",
			mv.what, drawn, resident, o8, o32, worst)
		if !mv.lapse && (drawn == 0 || drawn > resident/4) || mv.lapse && drawn < resident/2 {
			t.Errorf("%s moved: %d pages redrawn (of %d)", mv.what, drawn, resident)
		}
		if o8 > 0 {
			t.Errorf("%s moved: the cached map's picture differs from a fresh one's on %d pixels (worst %d)", mv.what, o8, worst)
		}
	}
}

// bakedFloorQuad is a 1 m square of compiled floor at y = 0, its baked light
// a mid grey with the sun's whole share kept (the bake saw it lit).
func bakedFloorQuad(t *testing.T, x, z float32) world.StaticSurface {
	t.Helper()
	verts := [][]float32{{x, 0, z + 1, 0, 0}, {x + 1, 0, z + 1, 1, 0}, {x + 1, 0, z, 1, 1}, {x, 0, z, 0, 1}}
	var g gfx.Geometry
	g.CPUOnly = true
	l := []float32{0.6, 0.6, 0.6, 1}
	g.VertexLight = [][]float32{l, l, l, l}
	if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, "concrete", mgl32.Vec3{}); err != nil {
		t.Fatal(err)
	}
	g.Alpha = 1
	return world.StaticSurface{Geometry: g, Chunk: "floor", Normal: mgl32.Vec3{0, 1, 0},
		Lo: mgl32.Vec3{x, 0, z}, Hi: mgl32.Vec3{x + 1, 0, z + 1}}
}

// A compiled level's baked sun is re-shadowed by the virtual map as by the
// cascades: the bake lit the whole floor (share 1), live chunks stand on
// it, and the shadows they cast are taken out of the baked light alike.
func TestVSMShadowsBakedSurfacesLikeTheCascades(t *testing.T) {
	w := newVSMWorld(t, false)
	lvl := vsmTestLevel(t)
	s := &world.StaticGeometry{}
	for x := -6; x < 6; x++ {
		for z := -10; z < 4; z++ {
			s.Surfaces = append(s.Surfaces, bakedFloorQuad(t, float32(x), float32(z)))
		}
	}
	lvl.Static = s
	lvl.Chunks = lvl.Chunks[1:] // the cube, the pole and the posts, live
	lvl.Lights.Directional = []world.DirectionalLight{vsmTestSun(false)}
	if lvl.Lights.BakedSun() != 0 {
		t.Fatal("the sun is not the baked sun")
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	world.UploadLights(lvl.Lights)
	vsmLook(mgl32.Vec3{0, 3, 7}, mgl32.Vec3{0, -0.35, -1})
	NaniteVSM = false
	plain := unshadowedFrame(w, lvl)
	w.frame(lvl)
	casc := w.frame(lvl)
	NaniteVSM = true
	vsm.reset()
	virt := vsmSettled(t, w, lvl)
	dumpPNG("baked_plain", plain, int(w.size))
	dumpPNG("baked_casc", casc, int(w.size))
	dumpPNG("baked_virt", virt, int(w.size))
	// Past 48 levels: the cascades' acne on this floor reaches 32.
	iou, nc, nv := maskStats(shadowMask(plain, casc, 48), shadowMask(plain, virt, 48))
	t.Logf("baked floor: shadowed %d (cascades) / %d (virtual), overlap %.3f", nc, nv, iou)
	if nc < 200 || nv < 200 || iou < 0.85 {
		t.Errorf("the baked floor's live shadow: %d / %d pixels, overlap %.3f", nc, nv, iou)
	}
}

// The clouds' shadow falls on top of the virtual map's as on the cascades':
// a cloud deck over the scene darkens the ground the same under both.
func TestVSMKeepsTheCloudShadow(t *testing.T) {
	w := newVSMWorld(t, false)
	lvl := vsmTestLevel(t)
	vsmLook(mgl32.Vec3{0, 3, 7}, mgl32.Vec3{0, -0.35, -1})
	frames := func(on bool) []uint8 {
		NaniteVSM = on
		vsm.reset()
		if on {
			return vsmSettled(t, w, lvl)
		}
		w.frame(lvl)
		return w.frame(lvl)
	}
	open := frames(true)
	slab := world.NewVolume("deck", mgl32.Vec3{0, 60, -20})
	slab.HalfX, slab.HalfY, slab.HalfZ = 200, 5, 200
	c := world.DefaultVolumeCloud()
	c.Coverage, c.Density, c.Erosion, c.Scale = 1, 3, 0, 400
	c.Wind = mgl32.Vec3{}
	slab.Cloud = &c
	lvl.Volumes = []world.Volume{slab}
	defer func() { lvl.Volumes = nil }()
	cloudCasc := frames(false)
	cloudVirt := frames(true)
	if !CloudShadowActive() {
		t.Fatal("no cloud shadow map for a casting deck")
	}
	// The lit ground in the frame's lower half, which the deck darkens.
	s := int(w.size)
	mean := func(px []uint8) float64 {
		sum, n := 0, 0
		for y := 0; y < s/3; y++ {
			for x := 0; x < s; x++ {
				sum += int(px[4*(y*s+x)])
				n++
			}
		}
		return float64(sum) / float64(n)
	}
	t.Logf("ground: %.1f open, %.1f under the deck (cascades), %.1f (virtual)", mean(open), mean(cloudCasc), mean(cloudVirt))
	if mean(cloudVirt) > mean(open)-10 {
		t.Errorf("the deck did not darken the ground under the virtual map (%.1f -> %.1f)", mean(open), mean(cloudVirt))
	}
	if d := math.Abs(mean(cloudVirt) - mean(cloudCasc)); d > 3 {
		t.Errorf("under the deck the virtual map's ground is %.1f levels off the cascades'", d)
	}
}

// A frame without a scene target — MSAA — keeps the cascades: the virtual
// map is not used, and the picture is the one with its switch off.
func TestVSMLeavesForwardFramesOnCascades(t *testing.T) {
	w := newVSMWorld(t, false)
	lvl := vsmTestLevel(t)
	vsmLook(mgl32.Vec3{0, 3, 7}, mgl32.Vec3{0, -0.35, -1})
	prev := gfx.AntiAliasMode
	defer func() { gfx.AntiAliasMode = prev }()
	gfx.AntiAliasMode = gfx.AAModeMSAA
	w.frame(lvl)
	off := w.frame(lvl)
	NaniteVSM = true
	w.frame(lvl)
	on := w.frame(lvl)
	if DebugStats.VSMOn {
		t.Fatal("the virtual map ran in an MSAA frame")
	}
	if o8, _, worst := pixelDiff(off, on); o8 > 0 {
		t.Errorf("an MSAA frame changed with the switch: %d px past 8 (worst %d)", o8, worst)
	}
}
