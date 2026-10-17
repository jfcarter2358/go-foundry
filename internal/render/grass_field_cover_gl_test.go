package render

// grass_field_cover_gl_test.go — ground cover through the grass instancer
// (docs/terrain-paint/PHASE_P3.md): an ORIENTED field of flat cards (16
// bytes a clump, attribute 5 the up axis) drawn beside an ordinary upright
// field (12 bytes, attribute 5 off) in the same frames, on both frame
// paths, without a GL error; the cards show; tilting their axes moves what
// they cover; and the frame stays still frame to frame (cards never sway).
//
//	FOUNDRY_GL_TEST=1 go test -timeout 300s -run 'GrassField' ./internal/render/

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/grassfield"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// grassTestCardFlags are the test cards' kind flags.
var grassTestCardFlags uint32 = grassfield.KindOriented | grassfield.KindNoWind | grassfield.KindCard

// grassTestCards writes a kit of card kinds and an oriented field of n×n
// cards over x0..x1, z0..z1 at height y (above the test level's floor:
// a card lies ON the ground, so one at the floor's own height is hidden),
// every card standing on axis up.
func grassTestCards(t *testing.T, n int, x0, x1, z0, z1, y float32, up [3]float32) string {
	t.Helper()
	dir := t.TempDir()
	kit := &grassfield.Kit{}
	for j := 0; j < 2; j++ {
		for i := 0; i < 2; i++ {
			kit.Kinds = append(kit.Kinds, grassfield.Kind{Name: "card", Texture: "brick", Height: 0.05, Radius: 0.71,
				Flags: grassTestCardFlags,
				LODs:  []grassfield.Mesh{grassfield.CardMesh(i, j, 2)}})
		}
	}
	kdata, err := grassfield.EncodeKit(kit)
	if err != nil {
		t.Fatal(err)
	}
	kitPath := filepath.Join(dir, "cards.fgk")
	if err := os.WriteFile(kitPath, kdata, 0o644); err != nil {
		t.Fatal(err)
	}
	var ins []grassfield.Instance
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			fx := (float32(i) + 0.5) / float32(n)
			fz := (float32(j) + 0.5) / float32(n)
			ins = append(ins, grassfield.Instance{Kind: (i + j) % 4, Pos: [3]float32{x0 + (x1-x0)*fx, y, z0 + (z1-z0)*fz},
				Yaw: float32(i*5+j*3) * 0.7, Scale: 0.35, Tint: [3]float32{1, 1, 1}, Up: up})
		}
	}
	f, err := grassfield.BuildField(grassfield.FieldSpec{Kit: kitPath, Min: [2]float32{x0, z0}, Max: [2]float32{x1 + 0.01, z1 + 0.01},
		Cell: 0.5, Kinds: 4, Oriented: true}, ins)
	if err != nil {
		t.Fatal(err)
	}
	fdata, err := grassfield.EncodeField(f)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cards_0_0.fgrass")
	if err := os.WriteFile(path, fdata, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGrassFieldDrawsGroundCover(t *testing.T) {
	w := newVisWorld(t)
	withoutFrameLighting(t)
	if !grassFields.initialized {
		t.Fatal("GrassFieldInit did not build its programs")
	}
	t.Cleanup(ReleaseGrassFields)
	prevLoad := grassFields.loadAll
	grassFields.loadAll = true
	t.Cleanup(func() { grassFields.loadAll = prevLoad })
	lvl := visTestLevel(t, visTestRock(t))
	total := int(w.size * w.size)
	settle := func() []uint8 {
		w.frame(lvl)
		w.frame(lvl)
		return w.frame(lvl)
	}
	plain := settle()
	for gl.GetError() != gl.NO_ERROR {
	}

	// On level ground an oriented kind is exactly the upright one: the same
	// cards with and without KindOriented draw the same picture (the
	// rotation onto +Y is the identity).
	shotOf := func(fl uint32) []uint8 {
		grassTestCardFlags = fl
		c := grassTestCards(t, 12, -0.6, 0.6, 2.9, 3.9, -0.5, [3]float32{0, 1, 0})
		lvl.Volumes = append(lvl.Volumes, grassVolume(c, mgl32.Vec3{-0.6, -0.8, 2.9}, mgl32.Vec3{0.6, 0, 3.9}))
		img := settle()
		lvl.Volumes = lvl.Volumes[:len(lvl.Volumes)-1]
		return img
	}
	upright, oriented := shotOf(0), shotOf(grassfield.KindOriented)
	grassTestCardFlags = grassfield.KindOriented | grassfield.KindNoWind | grassfield.KindCard
	if over8, _, _ := pixelDiff(plain, upright); over8 < total/200 {
		t.Fatalf("the test cards changed only %d pixels", over8)
	}
	if over8, _, worst := pixelDiff(upright, oriented); over8 > total/500 {
		t.Errorf("an oriented kind on level ground drew %d pixels differently from the upright one (worst %d)", over8, worst)
	}

	// The cards alone draw.
	cards0 := grassTestCards(t, 12, -0.6, 0.6, 2.9, 3.9, -0.5, [3]float32{0, 1, 0})
	lvl.Volumes = append(lvl.Volumes, grassVolume(cards0, mgl32.Vec3{-0.6, -0.8, 2.9}, mgl32.Vec3{0.6, 0, 3.9}))
	alone := settle()
	if over8, _, _ := pixelDiff(plain, alone); over8 < total/200 {
		t.Fatalf("an oriented card field alone changed only %d pixels (%+v)", over8, GrassFieldsLastStats())
	}
	lvl.Volumes = lvl.Volumes[:len(lvl.Volumes)-1]

	// An upright field and an oriented card field, side by side.
	grass := grassTestField(t, 12, -1.2, 0, 2.9, 3.9, -0.75)
	cards := grassTestCards(t, 12, 0, 1.2, 2.9, 3.9, -0.5, [3]float32{0, 1, 0})
	lvl.Volumes = append(lvl.Volumes,
		grassVolume(grass, mgl32.Vec3{-1.2, -0.8, 2.9}, mgl32.Vec3{0, 0, 3.9}),
		grassVolume(cards, mgl32.Vec3{0, -0.8, 2.9}, mgl32.Vec3{1.2, 0, 3.9}))
	lvl.Volumes[len(lvl.Volumes)-1].ID = "cards_0_0"
	shots := map[bool][]uint8{}
	for _, vis := range []bool{false, true} {
		NaniteVisBuffer = vis
		shots[vis] = settle()
		if e := gl.GetError(); e != gl.NO_ERROR {
			t.Fatalf("vis=%v: GL error %#x drawing an oriented field beside an upright one", vis, e)
		}
		st := GrassFieldsLastStats()
		over8, _, _ := pixelDiff(plain, shots[vis])
		t.Logf("vis=%v: %+v, %d pixels changed", vis, st, over8)
		if st.Drawn != 2 {
			t.Fatalf("vis=%v: %d fields drawn, want the upright one and the cards", vis, st.Drawn)
		}
		if over8 < total/100 {
			t.Fatalf("vis=%v: ground cover changed only %d pixels", vis, over8)
		}
	}
	NaniteVisBuffer = true
	// Still: two frames agree (no card sways).
	a, b := w.frame(lvl), w.frame(lvl)
	if over8, _, _ := pixelDiff(a, b); over8 > total/100 {
		t.Errorf("two frames of still cards differ in %d pixels", over8)
	}

	// The axis is read: the same cards stood on a steep axis (leaning
	// toward the camera, which looks down -Z) cover other pixels than flat
	// ones.
	steep := grassTestCards(t, 12, 0, 1.2, 2.9, 3.9, -0.5, [3]float32{0, 0.35, 0.94})
	lvl.Volumes[len(lvl.Volumes)-1] = grassVolume(steep, mgl32.Vec3{0, -0.8, 2.9}, mgl32.Vec3{1.2, 0, 3.9})
	lvl.Volumes[len(lvl.Volumes)-1].ID = "cards_steep"
	tilted := settle()
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("GL error %#x", e)
	}
	if over8, _, _ := pixelDiff(shots[true], tilted); over8 < total/200 {
		t.Errorf("tilting the cards' axis changed only %d pixels: the up axis is not reaching the vertex stage", over8)
	}
}
