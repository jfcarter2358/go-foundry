package particle

// particle_material_test.go — a layer wearing a .fmat.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/material"

	"github.com/go-gl/mathgl/mgl32"
)

func putMaterial(t *testing.T, d *material.Doc) {
	t.Helper()
	material.Put(d)
	t.Cleanup(material.Clear)
}

func TestALayerWithNoMaterialReadsItsOwnFields(t *testing.T) {
	l := NewLayer("smoke")
	l.Texture = "grass"
	l.Blend = gfx.SpriteBlendAlpha
	l.Billboard = gfx.BillboardYAxis
	if l.DrawTexture() != "grass" || l.DrawBlend() != gfx.SpriteBlendAlpha ||
		l.DrawBillboard() != gfx.BillboardYAxis {
		t.Error("a layer with no material did not read back its own settings")
	}
	if v := l.DrawVolume(); v.Density != l.Volume.Density || v.Albedo != l.Volume.Albedo {
		t.Error("DrawVolume changed a layer with no material")
	}
}

func TestALayerMaterialOverlaysTheAppearance(t *testing.T) {
	d := &material.Doc{Name: "ember", Domain: material.DomainParticle, Texture: "default"}
	d.AddEffect(material.KindBlendMode).Set("mode", material.EnumValue("additive"))
	putMaterial(t, d)

	l := NewLayer("sparks")
	l.Texture = "grass"
	l.Blend = gfx.SpriteBlendCutout
	l.Material = "ember"
	if l.DrawBlend() != gfx.SpriteBlendAdditive {
		t.Errorf("blend = %v", l.DrawBlend())
	}
	if l.DrawTexture() != "default" {
		t.Errorf("texture = %q, want the material's", l.DrawTexture())
	}
	// The billboard mode the material said nothing about is the layer's.
	if l.DrawBillboard() != l.Billboard {
		t.Error("a blend-only material changed the billboard mode")
	}
}

func TestAMaterialsVolumeConstantsWinButItsRampsStay(t *testing.T) {
	// The ramps are the one part of a volumetric look that cannot be a
	// material parameter, and they have to survive one being named.
	d := &material.Doc{Name: "fire", Domain: material.DomainParticle}
	e := d.AddEffect(material.KindVolume)
	e.Set("density", material.FloatValue(9))
	e.Set("emission", material.FloatValue(3))
	putMaterial(t, d)

	l := NewLayer("core")
	l.Material = "fire"
	l.Volume.Density = 1
	l.Volume.EmissionRamp = Ramp{{T: 0, V: 1}, {T: 1, V: 0}}

	v := l.DrawVolume()
	if v.Density != 9 || v.Emission != 3 {
		t.Errorf("volume constants = %+v, want the material's", v)
	}
	if len(v.EmissionRamp) != 2 {
		t.Errorf("the layer's emission ramp was lost: %v", v.EmissionRamp)
	}
}

func TestALayerMaterialSuppliesTheSheet(t *testing.T) {
	d := &material.Doc{Name: "sheet", Domain: material.DomainParticle}
	e := d.AddEffect(material.KindSpriteSheet)
	e.Set("cols", material.FloatValue(4))
	e.Set("rows", material.FloatValue(1))
	putMaterial(t, d)

	l := NewLayer("puff")
	l.Material = "sheet"
	if c, r := l.DrawSheet(); c != 4 || r != 1 {
		t.Errorf("sheet = %dx%d", c, r)
	}
}

func TestALayerWithOnlyAMaterialKeepsNoFallbackTexture(t *testing.T) {
	// The .fpar loader fills in "default" for a layer with no texture. A layer
	// whose MATERIAL supplies one must be left alone, or the fallback would
	// win over what the material names.
	src := "id: e\nlayers:\n  - name: l\n    material: art\n"
	path := filepath.Join(t.TempDir(), "e"+ParticleExt)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := LoadParticle(path)
	if err != nil {
		t.Fatal(err)
	}
	if d.Layers[0].Texture != "" {
		t.Errorf("texture = %q, want it left empty for the material to supply", d.Layers[0].Texture)
	}
	if d.Layers[0].Material != "art" {
		t.Errorf("material = %q", d.Layers[0].Material)
	}
}

func TestATintMaterialMultipliesTheLayersOwn(t *testing.T) {
	d := &material.Doc{Name: "warm", Domain: material.DomainParticle}
	d.AddEffect(material.KindTint).Set("color", material.ColorValue(mgl32.Vec3{1, 0.5, 0}))
	putMaterial(t, d)

	l := NewLayer("l")
	l.Material = "warm"
	l.Tint = mgl32.Vec3{0.5, 1, 1}
	if got := l.DrawTint(); got != (mgl32.Vec3{0.5, 0.5, 0}) {
		t.Errorf("tint = %v", got)
	}
}
