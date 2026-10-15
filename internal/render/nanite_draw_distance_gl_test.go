package render

// nanite_draw_distance_gl_test.go — draw distances, their fades and the
// impostor tier, drawn (nanite_draw_distance.go; world/scenery_draw.go;
// docs/regions/PRESETS_5.md).
//
//   - A placement past its draw distance is not drawn — on the visibility
//     buffer, the GPU cull's forward path and the classic path — and one
//     inside it, or with none, is; FOUNDRY_DRAW_DISTANCE=0 draws them all.
//   - In its fade band the visibility buffer dissolves it per pixel: about
//     the fade's share of its pixels survive. The forward path cuts it whole.
//   - Past its impostor distance a placement draws its impostor, not its
//     mesh; in the hand-over band the two dither COMPLEMENTARY halves of the
//     same pixels — every pixel is one or the other, none neither, which is
//     what makes the cross-fade seamless.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 600s -run DrawDistance ./internal/render/

import (
	"image"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// solidTexture uploads a flat opaque texture called name.
func solidTexture(t *testing.T, name string, c [3]uint8) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c[0], c[1], c[2], 255
	}
	if err := gfx.UpdateTextureFromRGBA(name, img); err != nil {
		t.Fatal(err)
	}
}

// ddCounts classifies the pixels in a box of half-size r round the screen
// point of world point p: red, blue and neither.
func ddCounts(px []uint8, size int, p mgl32.Vec3, r int) (red, blue, other int) {
	c := gfx.Projection.Mul4(gfx.Cam.Matrix).Mul4x1(p.Vec4(1))
	x := int((c[0]/c[3]*0.5 + 0.5) * float32(size))
	y := int((c[1]/c[3]*0.5 + 0.5) * float32(size))
	for j := y - r; j <= y+r; j++ {
		for i := x - r; i <= x+r; i++ {
			if i < 0 || j < 0 || i >= size || j >= size {
				continue
			}
			o := 4 * (j*size + i)
			pr, pb := int(px[o]), int(px[o+2])
			switch {
			case pr > 2*pb && pr > 40:
				red++
			case pb > 2*pr && pb > 40:
				blue++
			default:
				other++
			}
		}
	}
	return
}

// ddLevel is a level of placements of card, one per x, each given its
// fields by set, lit by an ambient.
func ddLevel(t *testing.T, card, impostor string, gpu bool, xs []float32, set func(i int, s *world.Scenery)) *world.Level {
	t.Helper()
	lvl := &world.Level{}
	for i, x := range xs {
		s := world.NewScenery("dd", card, mgl32.Vec3{x, 0, 0})
		s.NoNanite = !gpu
		s.Impostor = impostor
		set(i, &s)
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.9,
		Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	world.UploadLights(lvl.Lights)
	t.Cleanup(func() {
		world.DeleteScenery(lvl)
		naniteScenery.lvl = nil
		world.UploadLights(world.LightSet{})
	})
	return lvl
}

// ddPaths are the three ways a placement is drawn.
var ddPaths = []struct {
	name     string
	gpu, vis bool
}{
	{"vis", true, true},
	{"forward", true, false},
	{"classic", false, false},
}

func ddCamera() {
	gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 0.6, 4}, mgl32.Vec3{0, 0, -1}
	gfx.UpdateCamMatrix()
	ForgetTAAHistory()
}

func TestDrawDistanceCullsOnEveryPath(t *testing.T) {
	w := newVisWorld(t)
	solidTexture(t, "dd_red", [3]uint8{230, 30, 30})
	card := cutoutCard(t, "dd_card", "dd_red", 0.9, 1.2, 0)
	prev := DrawDistances
	t.Cleanup(func() { DrawDistances = prev })
	// Three cards ~4.1 m away: none, a draw distance past them, one short
	// of them.
	xs := []float32{-1.2, 0, 1.2}
	dist := []float32{0, 50, 3}
	for _, p := range ddPaths {
		NaniteVisBuffer = p.vis
		lvl := ddLevel(t, card, "", p.gpu, xs, func(i int, s *world.Scenery) { s.DrawDistance = dist[i] })
		for _, on := range []bool{true, false} {
			DrawDistances = on
			ddCamera()
			w.frame(lvl)
			px := w.frame(lvl)
			dumpPNG("draw_distance_"+p.name, px, int(w.size))
			for i, x := range xs {
				red, _, _ := ddCounts(px, int(w.size), mgl32.Vec3{x, 0.6, 0}, 4)
				want := !on || dist[i] == 0 || dist[i] > 5
				if (red > 60) != want {
					t.Errorf("%s (switch %v): card %d (draw distance %g, 4.1 m away): %d red pixels of 81, want drawn %v",
						p.name, on, i, dist[i], red, want)
				}
			}
		}
		world.DeleteScenery(lvl)
		naniteScenery.lvl = nil
	}
}

func TestDrawDistanceFadeDithersOnTheVisibilityBuffer(t *testing.T) {
	w := newVisWorld(t)
	solidTexture(t, "dd_red", [3]uint8{230, 30, 30})
	card := cutoutCard(t, "dd_fade_card", "dd_red", 1.6, 1.6, 0)
	NaniteVisBuffer = true
	ddCamera()
	d := mgl32.Vec3{0, 0, 0}.Sub(gfx.Cam.Position).Len()
	// Half-way through its fade band: (to - d) / (0.1 to) = 0.5.
	to := d / 0.95
	lvl := ddLevel(t, card, "", true, []float32{0}, func(_ int, s *world.Scenery) { s.DrawDistance = to })
	w.frame(lvl)
	px := w.frame(lvl)
	dumpPNG("draw_distance_fade", px, int(w.size))
	red, _, other := ddCounts(px, int(w.size), mgl32.Vec3{0, 0.8, 0}, 12)
	share := float64(red) / float64(red+other)
	t.Logf("half-faded card: %d of %d pixels drawn (%.2f)", red, red+other, share)
	if share < 0.3 || share > 0.7 {
		t.Errorf("half-way through its fade the card kept %.2f of its pixels, want about half", share)
	}

	// The forward path cuts the instance whole, at its own distance in the
	// band: all of it or none.
	NaniteVisBuffer = false
	world.DeleteScenery(lvl)
	naniteScenery.lvl = nil
	lvl = ddLevel(t, card, "", true, []float32{0}, func(_ int, s *world.Scenery) { s.DrawDistance = to })
	ddCamera()
	w.frame(lvl)
	px = w.frame(lvl)
	red, _, other = ddCounts(px, int(w.size), mgl32.Vec3{0, 0.8, 0}, 12)
	share = float64(red) / float64(red+other)
	h := lvl.Scenery[0].DrawHash()
	wantDrawn := d < to*(0.9+0.1*h)
	t.Logf("forward: %.2f drawn, hash %.3f, drawn by the cut %v", share, h, wantDrawn)
	if wantDrawn && share < 0.9 || !wantDrawn && share > 0.1 {
		t.Errorf("forward: %.2f of the card drawn, want all or none (the per-instance cut says %v)", share, wantDrawn)
	}
}

func TestDrawDistanceImpostorTakesOverAndCrossFades(t *testing.T) {
	w := newVisWorld(t)
	solidTexture(t, "dd_red", [3]uint8{230, 30, 30})
	solidTexture(t, "dd_blue", [3]uint8{30, 30, 230})
	mesh := cutoutCard(t, "dd_mesh", "dd_red", 1.6, 1.6, 0)
	imp := cutoutCard(t, "dd_imp", "dd_blue", 1.6, 1.6, 0)
	ddCamera()
	d := mgl32.Vec3{0, 0, 0}.Sub(gfx.Cam.Position).Len()
	for _, c := range []struct {
		name string
		at   float32 // the impostor distance
		vis  bool
		// what the box should be: the mesh's red, the impostor's blue, or
		// both (the band), and never the background.
		red, blue bool
	}{
		{"near: the mesh", d * 2, true, true, false},
		{"far: the impostor", d * 0.5, true, false, true},
		{"the band: both, complementary", d / 0.95, true, true, true},
		{"far, forward", d * 0.5, false, false, true},
	} {
		NaniteVisBuffer = c.vis
		lvl := ddLevel(t, mesh, imp, true, []float32{0}, func(_ int, s *world.Scenery) {
			s.DrawDistance, s.ImpostorDistance = 1000, c.at
		})
		if !lvl.Scenery[0].HasImpostor() {
			t.Fatalf("%s: the impostor did not load", c.name)
		}
		ddCamera()
		w.frame(lvl)
		px := w.frame(lvl)
		dumpPNG("draw_distance_impostor_"+c.name[:4], px, int(w.size))
		red, blue, other := ddCounts(px, int(w.size), mgl32.Vec3{0, 0.8, 0}, 12)
		t.Logf("%s: %d red, %d blue, %d neither", c.name, red, blue, other)
		if (red > 40) != c.red || (blue > 40) != c.blue {
			t.Errorf("%s: %d red (mesh), %d blue (impostor) pixels; want mesh %v, impostor %v", c.name, red, blue, c.red, c.blue)
		}
		// Complementary: no pixel left to the background.
		if other > (red+blue+other)/50 {
			t.Errorf("%s: %d of %d pixels are neither mesh nor impostor: the dithers are not complementary", c.name, other, red+blue+other)
		}
		world.DeleteScenery(lvl)
		naniteScenery.lvl = nil
	}
}
