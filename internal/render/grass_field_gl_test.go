package render

// grass_field_gl_test.go — the grass instancer drawing (grass_field.go), on
// the visibility buffer's parity scene:
//
//   - a level without a grass volume draws exactly what it drew with the
//     pass switched off;
//   - a field of clumps in front of the camera draws, on the forward path
//     and on the visibility buffer, and the two pictures agree;
//   - the density setting thins it, and a field past the draw range draws
//     nothing;
//   - its near clumps cast into the shadow map without error.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 300s -run 'GrassField' ./internal/render/

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/grassfield"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// grassTestField writes a kit of one crossed-card clump and a field of n×n
// clumps over x0..x1, z0..z1 at height y, and returns the field's path.
func grassTestField(t *testing.T, n int, x0, x1, z0, z1, y float32) string {
	t.Helper()
	dir := t.TempDir()
	src := &grassfield.SourceMesh{}
	quad := func(ax, az float32) {
		b := uint32(len(src.Positions))
		src.Positions = append(src.Positions, [3]float32{-ax * 0.2, 0, -az * 0.2}, [3]float32{ax * 0.2, 0, az * 0.2},
			[3]float32{ax * 0.2, 0.5, az * 0.2}, [3]float32{-ax * 0.2, 0.5, -az * 0.2})
		nn := [3]float32{-az, 0, ax}
		src.Normals = append(src.Normals, nn, nn, nn, nn)
		src.UVs = append(src.UVs, [2]float32{0, 0}, [2]float32{1, 0}, [2]float32{1, 1}, [2]float32{0, 1})
		src.Indices = append(src.Indices, b, b+1, b+2, b, b+2, b+3)
	}
	quad(1, 0)
	quad(0, 1)
	quad(0.7, 0.7)
	h, r := src.Extent()
	kit := &grassfield.Kit{Kinds: []grassfield.Kind{{Name: "card", Texture: "brick", Height: h, Radius: r, Bend: 0.2, Flutter: 0.02,
		Flags: grassfield.KindCasts, LODs: []grassfield.Mesh{grassfield.KindMesh(src, h, r, 0.2, 0.02),
			grassfield.KindMesh(grassfield.Thin(src, 0.4, 1), h, r, 0.2, 0.02)}}}}
	kdata, err := grassfield.EncodeKit(kit)
	if err != nil {
		t.Fatal(err)
	}
	kitPath := filepath.Join(dir, "grass.fgk")
	if err := os.WriteFile(kitPath, kdata, 0o644); err != nil {
		t.Fatal(err)
	}
	var ins []grassfield.Instance
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			fx := (float32(i) + 0.5) / float32(n)
			fz := (float32(j) + 0.5) / float32(n)
			ins = append(ins, grassfield.Instance{Pos: [3]float32{x0 + (x1-x0)*fx, y, z0 + (z1-z0)*fz},
				Yaw: float32(i*7+j*3) * 0.4, Scale: 1, Tint: [3]float32{0.8, 1, 0.7}})
		}
	}
	f, err := grassfield.BuildField(grassfield.FieldSpec{Kit: kitPath, Min: [2]float32{x0, z0}, Max: [2]float32{x1 + 0.01, z1 + 0.01},
		Cell: 0.5, Kinds: 1}, ins)
	if err != nil {
		t.Fatal(err)
	}
	fdata, err := grassfield.EncodeField(f)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "grass_0_0.fgrass")
	if err := os.WriteFile(path, fdata, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// grassVolume is the level volume naming the field over the box.
func grassVolume(path string, lo, hi mgl32.Vec3) world.Volume {
	c := lo.Add(hi).Mul(0.5)
	h := hi.Sub(lo).Mul(0.5)
	return world.Volume{ID: "grass_0_0", Position: c, HalfX: h[0], HalfY: h[1], HalfZ: h[2], Enabled: true,
		Grass: &world.VolumeGrass{File: path}}
}

func TestGrassFieldDraws(t *testing.T) {
	w := newVisWorld(t)
	if !grassFields.initialized {
		t.Fatal("GrassFieldInit did not build its programs")
	}
	t.Cleanup(ReleaseGrassFields)
	prevLoad := grassFields.loadAll
	grassFields.loadAll = true
	t.Cleanup(func() { grassFields.loadAll = prevLoad })
	lvl := visTestLevel(t, visTestRock(t))
	total := int(w.size * w.size)

	// No grass volume: the pass changes nothing.
	// (A few frames each: the occlusion history and the shadow pages settle.)
	settle := func() []uint8 {
		w.frame(lvl)
		w.frame(lvl)
		return w.frame(lvl)
	}
	plain := settle()
	grassFields.initialized = false
	off := settle()
	grassFields.initialized = true
	if over8, _, worst := pixelDiff(plain, off); over8 != 0 {
		t.Fatalf("a level without grass drew differently with the pass on: %d pixels (worst %d)", over8, worst)
	}

	path := grassTestField(t, 24, -1.2, 1.2, 2.9, 3.9, -0.75)
	lvl.Volumes = append(lvl.Volumes, grassVolume(path, mgl32.Vec3{-1.2, -0.8, 2.9}, mgl32.Vec3{1.2, 0, 3.9}))
	shots := map[bool][]uint8{}
	for _, vis := range []bool{false, true} {
		NaniteVisBuffer = vis
		w.frame(lvl)
		shots[vis] = w.frame(lvl)
		st := GrassFieldsLastStats()
		over8, _, _ := pixelDiff(plain, shots[vis])
		t.Logf("vis=%v: %+v, %d pixels changed by the grass", vis, st, over8)
		if st.Instances == 0 || st.Commands == 0 {
			t.Fatalf("vis=%v: nothing submitted (%+v)", vis, st)
		}
		if over8 < total/50 {
			t.Fatalf("vis=%v: the grass changed only %d pixels", vis, over8)
		}
	}
	NaniteVisBuffer = true
	over8, over32, worst := pixelDiff(shots[false], shots[true])
	t.Logf("forward against visibility buffer: %d past 8, %d past 32, worst %d", over8, over32, worst)
	if over32 > total/200 {
		t.Errorf("the grass draws differently on the two paths: %d pixels past 32 levels", over32)
	}

	// Density thins it.
	prev := gfx.GrassDensity
	gfx.GrassDensity = 0.25
	w.frame(lvl)
	w.frame(lvl)
	thin := GrassFieldsLastStats()
	gfx.GrassDensity = prev
	w.frame(lvl)
	full := GrassFieldsLastStats()
	if thin.Instances*2 > full.Instances {
		t.Errorf("density 0.25 drew %d instances, density 1 drew %d", thin.Instances, full.Instances)
	}

	// Out of range: nothing.
	far := grassTestField(t, 8, 500, 502, 500, 502, 0)
	lvl.Volumes[len(lvl.Volumes)-1] = grassVolume(far, mgl32.Vec3{500, 0, 500}, mgl32.Vec3{502, 1, 502})
	w.frame(lvl)
	if st := GrassFieldsLastStats(); st.Instances != 0 {
		t.Errorf("a field 700 m away drew %d instances", st.Instances)
	}

	// The near clumps cast (the shadow pass ran inside the frames above;
	// here, straight into a cascade-sized target, it must not disturb GL).
	lvl.Volumes[len(lvl.Volumes)-1] = grassVolume(path, mgl32.Vec3{-1.2, -0.8, 2.9}, mgl32.Vec3{1.2, 0, 3.9})
	w.frame(lvl)
	if lo, hi, ok := grassShadowBox(); !ok || hi[0]-lo[0] <= 0 {
		t.Errorf("no grass shadow box with grass in view")
	}
	a := w.frame(lvl)
	b := w.frame(lvl)
	if over8, _, _ := pixelDiff(a, b); over8 > total/100 {
		t.Errorf("two frames of the same still scene differ in %d pixels", over8)
	}
}
