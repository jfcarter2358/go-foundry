package material

// material_fx_test.go — the terrain and rock effects' schema and resolution
// (material_kinds_terrain.go, material_resolve_fx.go), and the library
// finding files by name.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"

	"github.com/go-gl/mathgl/mgl32"
)

func TestLoadAllFindsMaterialsByName(t *testing.T) {
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = t.TempDir()
	t.Cleanup(func() { fileutil.ResourcesPath = prev; Clear() })
	if err := os.MkdirAll(MaterialsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "kind: surface\ndoc: x\neffects:\n  - effect: triplanar\n    scale: 3\n"
	if err := os.WriteFile(filepath.Join(MaterialsDir(), "gen_rock"+MaterialExt), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if names := ScanMaterialFiles(); len(names) != 1 || names[0] != "gen_rock" {
		t.Fatalf("ScanMaterialFiles = %v, want [gen_rock]", names)
	}
	if n := LoadAll(); n != 1 {
		t.Fatalf("LoadAll loaded %d, want 1", n)
	}
	s, ok := ResolveSurface("gen_rock")
	if !ok || !s.Fx.HasTriplanar || s.Fx.TriplanarScale != 3 {
		t.Fatalf("gen_rock resolves to %+v (%v)", s.Fx, ok)
	}
}

func TestSurfaceFxResolveAndDefaults(t *testing.T) {
	d := &Doc{Name: "t", Domain: DomainSurface}
	if s := ResolveSurfaceDoc(d); s.Fx.Any() {
		t.Fatal("a material with none of the effects has some")
	}
	e := d.AddEffect(KindSplat)
	// A splat without weights resolves to none.
	e.Set(SplatLayerParam(0, ""), TextureValue("sand"))
	if s := ResolveSurfaceDoc(d); s.Fx.HasSplat {
		t.Fatal("a splat without weights should not resolve")
	}
	e.Set("weights", TextureValue("generated/x/weights"))
	e.Set(SplatLayerParam(3, "triplanar"), BoolValue(true))
	e.Set(SplatLayerParam(2, "anti_tile"), BoolValue(true))
	e.Set(SplatLayerParam(2, "relief"), FloatValue(0.05))
	s := ResolveSurfaceDoc(d)
	if !s.Fx.SplatLayers[2].AntiTile || s.Fx.SplatLayers[1].AntiTile || s.Fx.SplatLayers[2].Relief != 0.05 {
		t.Fatalf("anti_tile resolved wrong: %+v", s.Fx.SplatLayers)
	}
	if !s.Fx.HasSplat || s.Fx.SplatLayers[0].Texture != "sand" || !s.Fx.SplatLayers[3].Triplanar ||
		s.Fx.SplatLayers[1].Scale != 3 || s.Fx.SplatLayers[2].Tint != (mgl32.Vec3{1, 1, 1}) {
		t.Fatalf("splat resolved wrong: %+v", s.Fx)
	}
	g := d.AddEffect(KindGroundBlend)
	g.Set("ground", TextureValue("a"))
	if ResolveSurfaceDoc(d).Fx.HasGroundBlend {
		t.Fatal("a ground blend without its height map should not resolve")
	}
	g.Set("height", TextureValue("b"))
	d.AddEffect(KindColorGrade).Set("match", FloatValue(0.5))
	d.AddEffect(KindMacroVariation).Set("vertical", FloatValue(0.6))
	s = ResolveSurfaceDoc(d)
	if !s.Fx.HasGroundBlend || !s.Fx.HasGrade || s.Fx.GradeMatch != 0.5 || s.Fx.GradeSaturation != 1 ||
		!s.Fx.HasMacro || s.Fx.MacroVertical != 0.6 {
		t.Fatalf("ground blend / grade resolved wrong: %+v", s.Fx)
	}
	// The document round-trips, defaults left out.
	data, err := EncodeMaterial(d)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeMaterial(data)
	if err != nil {
		t.Fatal(err)
	}
	if ResolveSurfaceDoc(back).Fx != s.Fx {
		t.Fatalf("round trip changed the effects:\n%s", data)
	}
}

func TestSplatLayerParamNames(t *testing.T) {
	if SplatLayerParam(0, "") != "layer1" || SplatLayerParam(3, "scale") != "layer4_scale" {
		t.Fatal(SplatLayerParam(0, ""), SplatLayerParam(3, "scale"))
	}
	k := KindByName(KindSplat)
	for i := 0; i < SplatLayers; i++ {
		for _, f := range []string{"", "scale", "triplanar", "height", "tint"} {
			if k.Param(SplatLayerParam(i, f)) == nil {
				t.Errorf("splat has no %s", SplatLayerParam(i, f))
			}
		}
	}
}
