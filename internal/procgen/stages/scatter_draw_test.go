package stages

import (
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/impostor"
	"foundry/internal/model"
	"foundry/internal/nanite"
	"foundry/internal/nanitecook"
	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/rocks"

	"github.com/go-gl/mathgl/mgl32"
)

func drawItem(set map[string]procgen.Value) procgen.Params {
	p := procgen.Params{}
	for _, d := range drawRuleFields() {
		p[d.Name] = d.Default
	}
	for k, v := range set {
		p[k] = v
	}
	return p
}

func TestScatterDrawDistancesBySize(t *testing.T) {
	bush := &place.Variant{Model: "models/x/bush.fnm", Extent: [3]float64{1, 1, 1}}
	r := &place.Rule{Scale: [2]float64{1, 1}}
	plants := &RocksArtifact{Manifest: rocksManifestWithPreset(polyHavenPlantPreset)}
	want := float32(math.Round(220*0.5*math.Sqrt(3)*10) / 10)

	// A region's rule, all auto: by size, shadows half as far.
	d := drawRuleOf(drawItem(nil), plants, r, true)
	var pl procgen.Placement
	d.applyDraw(&pl, bush, 1, "")
	if pl.DrawDistance != want || pl.ShadowDistance != round1(want*0.5) || pl.Impostor != "" {
		t.Errorf("a 1 m bush: draw %g shadow %g impostor %q, want %g, %g, none", pl.DrawDistance, pl.ShadowDistance, pl.Impostor, want, round1(want*0.5))
	}
	// Small and huge clamp.
	pl = procgen.Placement{}
	d.applyDraw(&pl, bush, 0.1, "")
	if pl.DrawDistance != DrawMin {
		t.Errorf("a 10 cm pebble: draw %g, want the floor %d", pl.DrawDistance, DrawMin)
	}
	// A tree: it gets an impostor (auto, plants, tall), drawn at least 800 m.
	tree := &place.Variant{Model: "models/x/tree.fnm", Extent: [3]float64{6, 15, 6}, Max: [3]float64{3, 15, 3}, Min: [3]float64{-3, 0, -3}}
	if !d.wantsImpostor(tree) || d.wantsImpostor(bush) {
		t.Errorf("auto impostors: tree %v, bush %v; want the tree only", d.wantsImpostor(tree), d.wantsImpostor(bush))
	}
	pl = procgen.Placement{}
	d.applyDraw(&pl, tree, 1, "models/x/tree_imp.fnm")
	if pl.DrawDistance < TreeDrawMin || pl.DrawDistance > DrawMax || pl.Impostor == "" ||
		pl.ImpostorDistance < ImpostorMin || pl.ImpostorDistance > ImpostorMax || pl.ShadowDistance != pl.ImpostorDistance {
		t.Errorf("a 15 m tree: %+v", pl)
	}

	// A classic document's rule that sets nothing writes nothing.
	c := drawRuleOf(drawItem(nil), plants, r, false)
	pl = procgen.Placement{}
	c.applyDraw(&pl, tree, 1, "")
	if pl.DrawDistance != 0 || pl.ShadowDistance != 0 || pl.Impostor != "" || c.wantsImpostor(tree) {
		t.Errorf("a classic rule at its defaults wrote %+v (impostor wanted %v)", pl, c.wantsImpostor(tree))
	}
	// Explicit values, in either; negative is no limit.
	e := drawRuleOf(drawItem(map[string]procgen.Value{"draw_distance": procgen.Float(400), "shadow_distance": procgen.Float(-1)}), plants, r, false)
	pl = procgen.Placement{}
	e.applyDraw(&pl, bush, 1, "")
	if pl.DrawDistance != 400 || pl.ShadowDistance != 0 {
		t.Errorf("explicit 400 m, shadows as drawn: %+v", pl)
	}
	n := drawRuleOf(drawItem(map[string]procgen.Value{"draw_distance": procgen.Float(-1)}), plants, r, true)
	pl = procgen.Placement{}
	n.applyDraw(&pl, bush, 1, "")
	if pl.DrawDistance != 0 || pl.ShadowDistance != 0 {
		t.Errorf("draw_distance -1 is no limit: %+v", pl)
	}
	// An impostor past the draw distance is no impostor.
	f := drawRuleOf(drawItem(map[string]procgen.Value{"draw_distance": procgen.Float(100), "impostor_distance": procgen.Float(200)}), plants, r, true)
	pl = procgen.Placement{}
	f.applyDraw(&pl, tree, 1, "models/x/tree_imp.fnm")
	if pl.Impostor != "" {
		t.Errorf("an impostor beyond the draw distance was written: %+v", pl)
	}
}

func rocksManifestWithPreset(preset string) *rocks.Manifest {
	m := &rocks.Manifest{}
	m.Preset = preset
	return m
}

func TestEnsureImpostorBakesOnceBesideTheMesh(t *testing.T) {
	res := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	if err := os.MkdirAll(filepath.Join(res, "textures"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A green 8x8 leaf texture.
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 40, 160, 40, 255
	}
	f, err := os.Create(filepath.Join(res, "textures", "leaf.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	// A cooked cone of a tree.
	im := &model.ImportMesh{Slots: []model.ImportSlot{{Name: "crown", Texture: "leaf", BaseColor: [4]float32{1, 1, 1, 1}, DoubleSided: true}}}
	const seg = 24
	for i := 0; i < seg; i++ {
		a0, a1 := 2*math.Pi*float64(i)/seg, 2*math.Pi*float64(i+1)/seg
		base := uint32(len(im.Positions))
		im.Positions = append(im.Positions, mgl32.Vec3{2 * float32(math.Cos(a0)), 0, 2 * float32(math.Sin(a0))},
			mgl32.Vec3{0, 8, 0}, mgl32.Vec3{2 * float32(math.Cos(a1)), 0, 2 * float32(math.Sin(a1))})
		for k := 0; k < 3; k++ {
			im.Normals = append(im.Normals, mgl32.Vec3{0, 1, 0})
			im.UVs = append(im.UVs, mgl32.Vec2{0.5, 0.5})
		}
		im.Indices = append(im.Indices, base, base+1, base+2)
		im.TriSlot = append(im.TriSlot, 0)
	}
	rel := "models/test/tree.fnm"
	if err := os.MkdirAll(filepath.Join(res, "models", "test"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := nanitecook.CookMeshFile(im, filepath.Join(res, rel), true, nanitecook.Options{}); err != nil {
		t.Fatal(err)
	}
	logs := 0
	logf := func(string, ...any) { logs++ }
	got, err := ensureImpostor(logf, rel)
	if err != nil {
		t.Fatal(err)
	}
	p := impostor.PathsFor(rel)
	if got != p.Model || logs != 1 {
		t.Fatalf("baked %q (%d bakes), want %q once", got, logs, p.Model)
	}
	for _, f := range []string{filepath.Join(res, p.Model), filepath.Join(res, p.Provenance),
		filepath.Join(res, "textures", p.Texture+".png"), filepath.Join(res, "textures", p.Texture+"_normal.png")} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	cards, err := nanite.Read(filepath.Join(res, p.Model))
	if err != nil {
		t.Fatal(err)
	}
	if len(cards.Slots) != 9 || cards.Slots[0].Texture != p.Texture {
		t.Errorf("the cards: %d slots wearing %q, want 9 wearing %q", len(cards.Slots), cards.Slots[0].Texture, p.Texture)
	}
	// Current: a second run bakes nothing.
	if _, err := ensureImpostor(logf, rel); err != nil || logs != 1 {
		t.Errorf("a second run baked again (%d bakes, %v)", logs, err)
	}
}
