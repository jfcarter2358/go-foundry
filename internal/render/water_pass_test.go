package render

// water_pass_test.go — the water pass's pieces that need no GL: the sky map's
// source rewrite against the repository's sky shader, the split of the
// transparent queue, and the shader files keeping the height field in one
// place. The drawing itself is water_gl_test.go's.

import (
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/material"
)

func TestWaterSkySourceRewritesTheSkyShader(t *testing.T) {
	src, err := waterSkySource(mustReadShader(repoShader(t, "sky_frag.glsl")))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"void skyMain()", "vec3 vDir;", "waterOctDecode(", "#include \"water_oct.glsl\""} {
		if !strings.Contains(src, want) {
			t.Errorf("the sky map's source has no %q", want)
		}
	}
	if strings.Count(src, "void main()") != 1 {
		t.Error("want exactly one main")
	}
	if _, err := waterSkySource("void main() {}"); err == nil {
		t.Error("a sky shader without vDir was accepted")
	}
}

// waterHeight lives in water_waves.glsl alone, included by both stages: the
// vertex stage displaces with exactly the field the fragment stage lights.
func TestWaterHeightIsOneFileForBothStages(t *testing.T) {
	for _, f := range []string{"vertex.glsl", "fragment.glsl"} {
		src := mustReadShader(repoShader(t, f))
		if !strings.Contains(src, "#include \"water_waves.glsl\"") {
			t.Errorf("%s does not include water_waves.glsl", f)
		}
		if strings.Contains(src, "float waterHeight(") {
			t.Errorf("%s defines a waterHeight of its own", f)
		}
	}
	if !strings.Contains(mustReadShader(repoShader(t, "water_waves.glsl")), "float waterHeight(") {
		t.Error("water_waves.glsl has no waterHeight")
	}
}

// The water surfaces leave the transparent queue for the water pass; the
// rest stay, in order.
func TestSplitWaterDrawsMovesOnlyWater(t *testing.T) {
	d := &material.Doc{Name: "split_pond", Domain: material.DomainSurface}
	d.AddEffect(material.KindWater)
	material.Put(d)
	t.Cleanup(material.Clear)
	glass := &gfx.Geometry{Texture: "glass", Alpha: 0.5}
	pond := &gfx.Geometry{Texture: "x", Alpha: 1, MaterialName: "split_pond"}
	draws := []TransparentDraw{{Geom: glass, Distance: 3}, {Geom: pond, Distance: 2}, {Geom: glass, Distance: 1}}
	rest, water := splitWaterDraws(draws)
	if len(water) != 1 || water[0].Geom != pond {
		t.Fatalf("water = %+v", water)
	}
	if len(rest) != 2 || rest[0].Distance != 3 || rest[1].Distance != 1 {
		t.Fatalf("rest = %+v", rest)
	}
}
