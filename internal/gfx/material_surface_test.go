package gfx

// material_surface_test.go — what a material does to a draw, and what it
// leaves alone.
//
// The whole safety argument for the port is in these tests: a surface that
// names no material must come out bit-for-bit what it was, and a surface that
// names one must get its effects overlaid rather than its settings replaced.

import (
	"testing"

	"foundry/internal/material"

	"github.com/go-gl/mathgl/mgl32"
)

func putMaterial(t *testing.T, d *material.Doc) {
	t.Helper()
	material.Put(d)
	t.Cleanup(material.Clear)
}

func TestASurfaceWithNoMaterialIsUnchanged(t *testing.T) {
	g := Geometry{
		Texture: "brick", Alpha: 0.5, Unlit: true,
		BlendTexture: "moss", BlendMask: "dirt",
		UVOffset: mgl32.Vec2{0.25, 0},
		Water:    WaterParams{Enabled: true, WaveSpeed: 2},
	}
	s := g.SurfaceState()
	if s.Texture != "brick" || s.Alpha != 0.5 || !s.Unlit {
		t.Errorf("state = %+v", s)
	}
	if s.BlendTexture != "moss" || s.BlendMask != "dirt" {
		t.Errorf("blend pair changed: %+v", s)
	}
	if s.UVOffset != (mgl32.Vec2{0.25, 0}) {
		t.Errorf("uv offset = %v", s.UVOffset)
	}
	if !s.Water.Enabled || s.Water.WaveSpeed != 2 {
		t.Errorf("water = %+v", s.Water)
	}
	// The three uniforms materials added must be the identity, or every
	// surface in a level built before materials existed would tile wrong.
	if s.UVScale != (mgl32.Vec2{1, 1}) || s.UVRotation != 0 || s.CutoffBias != 0 {
		t.Errorf("a material-free surface is not at the identity: %+v", s)
	}
}

func TestAMaterialOverlaysRatherThanReplaces(t *testing.T) {
	d := &material.Doc{Name: "scroller", Domain: material.DomainSurface}
	d.AddEffect(material.KindUVScroll).Set("rate", material.Vec2Value(mgl32.Vec2{1, 0}))
	putMaterial(t, d)

	MaterialScrollTime = 0.5
	g := Geometry{Texture: "brick", Alpha: 0.5, MaterialName: "scroller"}
	s := g.SurfaceState()
	// The surface's own art and alpha survive: the material said nothing
	// about either.
	if s.Texture != "brick" || s.Alpha != 0.5 {
		t.Errorf("a scroll-only material took over the surface: %+v", s)
	}
	if s.UVOffset.X() != 0.5 {
		t.Errorf("scroll offset = %v, want the rate times the clock", s.UVOffset)
	}
}

func TestAMaterialsTextureWinsWhenItHasOne(t *testing.T) {
	d := &material.Doc{Name: "puddle", Domain: material.DomainSurface, Texture: "water"}
	d.AddEffect(material.KindWater)
	putMaterial(t, d)

	g := Geometry{Texture: "concrete", MaterialName: "puddle"}
	s := g.SurfaceState()
	if s.Texture != "water" {
		t.Errorf("texture = %q, want the material's", s.Texture)
	}
	// The normal map follows the texture, or a puddle would light with the
	// concrete's relief.
	if s.NormalMap != "water" {
		t.Errorf("normal map = %q, want it to follow the material's texture", s.NormalMap)
	}
	if !s.Water.Enabled {
		t.Error("water did not resolve")
	}
}

func TestAMaterialsScrollAddsToTheSurfacesOwn(t *testing.T) {
	// A belt on a moving vehicle: the attachment's own scroll is advanced per
	// tick, the material's is a function of the clock, and both apply.
	d := &material.Doc{Name: "belt", Domain: material.DomainSurface}
	d.AddEffect(material.KindUVScroll).Set("rate", material.Vec2Value(mgl32.Vec2{0, 1}))
	putMaterial(t, d)

	MaterialScrollTime = 0.25
	g := Geometry{Texture: "brick", MaterialName: "belt", UVOffset: mgl32.Vec2{0.5, 0}}
	s := g.SurfaceState()
	if s.UVOffset.X() != 0.5 {
		t.Errorf("the surface's own scroll was lost: %v", s.UVOffset)
	}
	if s.UVOffset.Y() != 0.25 {
		t.Errorf("the material's scroll did not apply: %v", s.UVOffset)
	}
}

func TestMaterialScrollWrapsIntoTheUnitSquare(t *testing.T) {
	// After an hour of play the raw product is in the thousands, and a float32
	// UV coordinate there has lost the precision the texture needs.
	d := &material.Doc{Name: "fast", Domain: material.DomainSurface}
	d.AddEffect(material.KindUVScroll).Set("rate", material.Vec2Value(mgl32.Vec2{3, -3}))
	putMaterial(t, d)

	MaterialScrollTime = 3600
	g := Geometry{Texture: "brick", MaterialName: "fast"}
	s := g.SurfaceState()
	for i, v := range []float32{s.UVOffset.X(), s.UVOffset.Y()} {
		if v < 0 || v >= 1 {
			t.Errorf("axis %d offset %v is outside [0,1)", i, v)
		}
	}
}

func TestACutoutThresholdBecomesABiasFromTheShadersOwn(t *testing.T) {
	d := &material.Doc{Name: "leaves", Domain: material.DomainSurface}
	d.AddEffect(material.KindCutout).Set("threshold", material.FloatValue(0.75))
	putMaterial(t, d)

	g := Geometry{Texture: "grass", MaterialName: "leaves"}
	if got := g.SurfaceState().CutoffBias; got != 0.75-DefaultAlphaCutoff {
		t.Errorf("bias = %v, want %v", got, 0.75-DefaultAlphaCutoff)
	}
}

func TestAMaterialsUnlitSendsTheSurfaceToTheBlendedPass(t *testing.T) {
	d := &material.Doc{Name: "beam", Domain: material.DomainSurface}
	d.AddEffect(material.KindUnlit)
	putMaterial(t, d)

	g := Geometry{Texture: "concrete", Alpha: 1}
	if g.IsTransparent() {
		t.Fatal("an opaque lit surface should be in the opaque pass")
	}
	g.MaterialName = "beam"
	if !g.IsTransparent() {
		t.Error("a material's unlit must reach the pass choice, not just the shading")
	}
}

func TestAMaterialsTextureDecidesTheUntexturedPath(t *testing.T) {
	// A face with no texture is painted with its own normal. A material that
	// supplies one has to switch that off, or a painted wall would come out in
	// normals.
	d := &material.Doc{Name: "plated", Domain: material.DomainSurface, Texture: "hull_plate"}
	d.AddEffect(material.KindUnlit)
	putMaterial(t, d)

	g := Geometry{Texture: ""}
	if g.NormalColorFlag() != 1 {
		t.Fatal("an untextured surface should be normal-coloured")
	}
	g.MaterialName = "plated"
	if g.NormalColorFlag() != 0 {
		t.Error("a material supplying a texture must turn the normal-colour path off")
	}
}

func TestABatchWearsItsMaterialToo(t *testing.T) {
	// Chunk and static batches are the path every wall in a level actually
	// draws through; a material that only reached Geometry.Draw would work in
	// the preview and nowhere else.
	d := &material.Doc{Name: "sheen", Domain: material.DomainSurface}
	d.AddEffect(material.KindAlpha).Set("value", material.FloatValue(0.4))
	putMaterial(t, d)

	m := Material{Texture: "brick", Alpha: 1}
	if m.IsTransparent() {
		t.Fatal("an opaque batch should be in the opaque pass")
	}
	m.MaterialName = "sheen"
	if s := m.SurfaceState(); s.Alpha != 0.4 {
		t.Errorf("batch alpha = %v", s.Alpha)
	}
	if !m.IsTransparent() {
		t.Error("a material's alpha must reach the batch's pass choice")
	}
}

func TestAMaterialOfTheWrongDomainIsIgnored(t *testing.T) {
	putMaterial(t, &material.Doc{Name: "the_sky", Domain: material.DomainSky})
	g := Geometry{Texture: "brick", Alpha: 1, MaterialName: "the_sky"}
	s := g.SurfaceState()
	if s.Texture != "brick" || s.Alpha != 1 {
		t.Errorf("a sky material changed a wall: %+v", s)
	}
}

func TestANamedNormalMapWinsOverTheTexturesOwn(t *testing.T) {
	d := &material.Doc{Name: "rough", Domain: material.DomainSurface}
	d.AddEffect(material.KindNormalMap).Set("map", material.TextureValue("rock"))
	putMaterial(t, d)

	g := Geometry{Texture: "concrete", MaterialName: "rough"}
	if got := g.SurfaceState().NormalMap; got != "rock" {
		t.Errorf("normal map = %q, want the one the material named", got)
	}
}

func TestAnEmptyNormalMapMeansTheTexturesOwn(t *testing.T) {
	d := &material.Doc{Name: "relief", Domain: material.DomainSurface}
	d.AddEffect(material.KindNormalMap)
	putMaterial(t, d)

	g := Geometry{Texture: "concrete", MaterialName: "relief"}
	if got := g.SurfaceState().NormalMap; got != "concrete" {
		t.Errorf("normal map = %q, want the surface's own texture", got)
	}
}

func TestAMaterialsSmoothReachesTheBakedLightChoice(t *testing.T) {
	// ShadesSmooth picks more than the uniform: a compiled surface's baked
	// light comes in a flat and a smoothed variant, and shading smooth off the
	// flat one lights the surface for a shape it no longer has.
	d := &material.Doc{Name: "welded", Domain: material.DomainSurface}
	d.AddEffect(material.KindSmooth)
	putMaterial(t, d)

	was := SmoothLighting
	SmoothLighting = false
	t.Cleanup(func() { SmoothLighting = was })

	g := Geometry{Texture: "brick", Normals: []float32{0, 1, 0}}
	if g.ShadesSmooth() {
		t.Fatal("a flat surface with the setting off should not shade smooth")
	}
	g.MaterialName = "welded"
	if !g.ShadesSmooth() {
		t.Error("a material's smooth effect must reach ShadesSmooth, not just the uniform")
	}
	st := g.SurfaceState()
	if st.SmoothFlag() != 1 {
		t.Error("the uniform and ShadesSmooth disagree")
	}
}
