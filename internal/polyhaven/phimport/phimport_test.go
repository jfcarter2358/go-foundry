package phimport

// phimport_test.go — a model and a texture set imported from an httptest
// server serving a made-up asset (never the live API): a curved rock face
// scanned from +X (open at the back) with a leaf card whose material is a
// MASK cutout, JPG textures and an alpha map. Checked: the engine textures
// (the cutout's alpha merged in), the re-pointed glTF, the placement frame
// (base on 0, back estimated and turned to −Z on z = 0), the cooked
// variants and the light copy, the provenance, and that a second import
// downloads and converts nothing.

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/model"
	"foundry/internal/nanite"
	"foundry/internal/polyhaven/phtest"

	"github.com/go-gl/mathgl/mgl32"
)

func TestImportModelEndToEnd(t *testing.T) {
	a := phtest.New(t)
	c := a.Client(t)
	res := t.TempDir()
	var logs []string
	opt := ModelOptions{Resources: res, Resolution: "2k", Cook: true, LiteTriangles: 100,
		Log: func(f string, args ...any) { logs = append(logs, fmt.Sprintf(f, args...)) }}
	imp, err := ImportModel(context.Background(), c, "t1", opt)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Resolution != "1k" {
		t.Fatalf("resolution %s, want the 1k on offer", imp.Resolution)
	}
	// Textures: names, the cutout's alpha, the normal map beside its albedo.
	for _, n := range []string{"ph_t1_rock", "ph_t1_rock_normal", "ph_t1_rock_arm", "ph_t1_leaf"} {
		if _, err := os.Stat(filepath.Join(res, "textures", n+".png")); err != nil {
			t.Fatalf("texture %s: %v", n, err)
		}
	}
	leaf := readPNG(t, filepath.Join(res, "textures", "ph_t1_leaf.png"))
	if _, _, _, al := leaf.At(4, 16).RGBA(); al < 0xf000 {
		t.Fatalf("leaf left half alpha %x, want opaque", al)
	}
	if _, _, _, ar := leaf.At(28, 16).RGBA(); ar > 0x1000 {
		t.Fatalf("leaf right half alpha %x, want cut out", ar)
	}
	rock := readPNG(t, filepath.Join(res, "textures", "ph_t1_rock.png"))
	if r, _, _, al := rock.At(5, 5).RGBA(); al != 0xffff || r>>8 < 170 {
		t.Fatalf("rock albedo r=%d a=%x", r>>8, al)
	}
	// The normal map's green is turned over (nor_gl → the engine's +V down).
	nrm := readPNG(t, filepath.Join(res, "textures", "ph_t1_rock_normal.png"))
	if _, g, b, _ := nrm.At(5, 5).RGBA(); g>>8 < 185 || g>>8 > 205 || b>>8 < 220 {
		t.Fatalf("normal map green %d blue %d, want ~195 (255−60) and ~230", g>>8, b>>8)
	}
	if len(imp.Cutout) != 1 || imp.Cutout[0] != "t1_leaf" {
		t.Fatalf("cutout %v", imp.Cutout)
	}
	// The glTF points at them, and loads with those slot textures.
	g, err := os.ReadFile(filepath.Join(res, "models/polyhaven/t1/t1.gltf"))
	if err != nil || !strings.Contains(string(g), "../../../textures/ph_t1_rock_normal.png") {
		t.Fatalf("gltf %v", err)
	}
	m, err := model.LoadGLTF(filepath.Join(res, "models/polyhaven/t1/t1.gltf"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Slots[0].Texture != "ph_t1_rock" || m.Slots[1].Texture != "ph_t1_leaf" || !m.Slots[1].DoubleSided {
		t.Fatalf("slots %+v", m.Slots)
	}
	// The frame: the face looked out along +X; now its back is −Z on z = 0.
	if !imp.Frame.HasBack || imp.Frame.Confidence < 0.5 {
		t.Fatalf("frame %+v", imp.Frame)
	}
	if len(imp.Variants) != 2 {
		t.Fatalf("%d variants, want full + lite", len(imp.Variants))
	}
	full := imp.Variants[0].Meta
	if math.Abs(float64(full.Min[1])) > 1e-4 || math.Abs(float64(full.Min[2])) > 1e-4 || full.Back == nil {
		t.Fatalf("frame min %v back %v", full.Min, full.Back)
	}
	if math.Abs(float64(full.Min[0]+full.Max[0])) > 1e-3 {
		t.Fatalf("not centred in x: %v..%v", full.Min, full.Max)
	}
	if full.Height < 4.4 || full.Height > 4.6 || full.PlaceCollision != "mesh" || full.Collision != "mesh" {
		t.Fatalf("meta %+v", full)
	}
	// The cooked mesh is in the frame too: its outside faces +Z.
	nm, err := nanite.Load(filepath.Join(res, "models/polyhaven/t1", full.File))
	if err != nil {
		t.Fatal(err)
	}
	if nm.Lo[1] > 1e-3 || nm.Lo[2] > 1e-3 || nm.Lo[2] < -1e-3 {
		t.Fatalf("cooked bounds %v..%v", nm.Lo, nm.Hi)
	}
	lite := imp.Variants[1].Meta
	if lite.Triangles >= full.Triangles || lite.Name != "t1_lite" || !strings.Contains(lite.File, "_t100") {
		t.Fatalf("lite %+v vs %d", lite, full.Triangles)
	}
	if imp.Variants[0].Model != "models/polyhaven/t1/"+full.File {
		t.Fatalf("model path %s", imp.Variants[0].Model)
	}
	// Provenance.
	p, err := ReadProvenance(filepath.Join(res, "models/polyhaven/t1/polyhaven.yaml"))
	if err != nil || p.ID != "t1" || p.License == "" || len(p.Files) != 7 || p.Import == nil || len(p.Import.Meshes) != 2 || p.Authors[0] != "Ann" {
		t.Fatalf("provenance %+v %v", p, err)
	}
	for _, f := range p.Files {
		if len(f.MD5) != 32 {
			t.Fatalf("file %+v has no md5", f)
		}
	}

	// Again: nothing downloaded, nothing converted, nothing cooked.
	hits := a.Hits()
	st, _ := os.Stat(filepath.Join(res, "textures", "ph_t1_rock.png"))
	time.Sleep(10 * time.Millisecond)
	imp2, err := ImportModel(context.Background(), c, "t1", opt)
	if err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(filepath.Join(res, "textures", "ph_t1_rock.png"))
	if imp2.Downloaded != 0 || a.Hits() != hits || !st2.ModTime().Equal(st.ModTime()) {
		t.Fatalf("re-import downloaded %d (%d requests) or rewrote textures", imp2.Downloaded, a.Hits()-hits)
	}
	// A plant gets no back and no collision.
	plant, err := ImportModel(context.Background(), c, "t1", ModelOptions{Resources: res, Plant: true, Cook: false})
	if err != nil {
		t.Fatal(err)
	}
	if plant.Frame.HasBack || plant.Variants[0].Meta.PlaceCollision != "none" || !strings.HasSuffix(plant.Variants[0].Meta.File, ".glb") {
		t.Fatalf("plant %+v %+v", plant.Frame, plant.Variants[0].Meta)
	}
}

func TestImportSplitsRootNodesIntoParts(t *testing.T) {
	c := phtest.New(t).Client(t)
	res := t.TempDir()
	imp, err := ImportModel(context.Background(), c, "s2", ModelOptions{Resources: res, Cook: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Variants) != 2 || imp.Variants[0].Meta.Name != "s2_a" || imp.Variants[1].Meta.Name != "s2_b" {
		t.Fatalf("variants %+v", imp.Variants)
	}
	for _, v := range imp.Variants {
		// Each part alone: about 6 m across, centred, not 36 m of both.
		if v.Meta.Extent[0] > 7 || math.Abs(float64(v.Meta.Min[0]+v.Meta.Max[0])) > 1e-3 {
			t.Fatalf("part %s extent %v min %v", v.Meta.Name, v.Meta.Extent, v.Meta.Min)
		}
	}
	whole, err := ImportModel(context.Background(), c, "s2", ModelOptions{Resources: res, Cook: false, NoSplit: true})
	if err != nil || len(whole.Variants) != 1 || whole.Variants[0].Meta.Size < 30 {
		t.Fatalf("unsplit %+v %v", whole.Variants, err)
	}
}

func TestImportTextureSet(t *testing.T) {
	c := phtest.New(t).Client(t)
	res := t.TempDir()
	ts, err := ImportTextures(context.Background(), c, "sand", TextureOptions{Resources: res, MaxSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	if ts.Albedo != "ph_sand" || ts.Normal != "ph_sand_normal" || ts.Rough != "ph_sand_rough" || ts.AO != "ph_sand_ao" || ts.Height != "ph_sand_height" || ts.Size != 3 {
		t.Fatalf("set %+v", ts)
	}
	rough := readPNG(t, filepath.Join(res, "textures", "ph_sand_rough.png"))
	if rough.Bounds().Dx() != 16 {
		t.Fatalf("max size: %v", rough.Bounds())
	}
	if g, ok := rough.(*image.Gray); !ok || g.Pix[0] < 170 || g.Pix[0] > 190 {
		t.Fatalf("roughness is the ARM's green: %T", rough)
	}
	if _, err := os.Stat(filepath.Join(res, "textures/polyhaven/sand/polyhaven.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportModel(context.Background(), c, "sand", ModelOptions{Resources: res}); err == nil {
		t.Fatal("a texture imported as a model")
	}
}

func TestNormaliseBackChoices(t *testing.T) {
	// A unit-ish sheet facing +X: auto turns it so its back is −Z.
	mk := func() *model.ImportMesh {
		return &model.ImportMesh{Positions: []mgl32.Vec3{{0, 0, 0}, {0, 0, 1}, {0, 1, 0}}, Indices: []uint32{0, 1, 2},
			Normals: []mgl32.Vec3{{1, 0, 0}, {1, 0, 0}, {1, 0, 0}}}
	}
	m := mk()
	fr, err := Normalise(m, "auto", true)
	if err != nil || !fr.HasBack {
		t.Fatal(fr, err)
	}
	if n := m.Normals[0]; math.Abs(float64(n[2])-1) > 1e-5 {
		t.Fatalf("front normal %v, want +Z", n)
	}
	m = mk()
	if fr, _ := Normalise(m, "none", true); fr.HasBack {
		t.Fatal("none gave a back")
	}
	m = mk()
	if fr, _ := Normalise(m, "auto", false); fr.HasBack {
		t.Fatal("a plant got a back")
	}
	m = mk()
	if fr, _ := Normalise(m, "+z", true); !fr.HasBack || math.Abs(fr.Yaw-math.Pi) > 1e-9 {
		t.Fatalf("+z back yaw %v", fr.Yaw)
	}
	if _, err := Normalise(mk(), "sideways", true); err == nil {
		t.Fatal("a bad back was accepted")
	}
	if partName("quiver_tree_01", "quiver_tree_01_trunk") != "trunk" || partName("a", "Leaf.001") != "leaf_001" {
		t.Fatal("part names")
	}
	if r, _ := pickResolution([]string{"1k", "4k", "8k"}, "2k"); r != "1k" {
		t.Fatal(r)
	}
	if r, _ := pickResolution([]string{"4k", "8k"}, "2k"); r != "4k" {
		t.Fatal(r)
	}
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}
