package world

// sprite_material_test.go — a sprite and a sky wearing a .fmat.

import (
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

func TestASpriteWithNoMaterialReadsItsOwnFields(t *testing.T) {
	s := Sprite{
		Texture: "grass", Blend: gfx.SpriteBlendAdditive, Billboard: gfx.BillboardYAxis,
		Tint: mgl32.Vec3{1, 0.5, 0.25}, Alpha: 0.6, Lit: true, SheetCols: 2, SheetRows: 3,
	}
	if s.DrawTexture() != "grass" || s.DrawBlend() != gfx.SpriteBlendAdditive ||
		s.DrawBillboard() != gfx.BillboardYAxis || s.DrawAlpha() != 0.6 || !s.DrawLit() {
		t.Error("a sprite with no material did not read back its own settings")
	}
	if c, r := s.DrawSheet(); c != 2 || r != 3 {
		t.Errorf("sheet = %dx%d", c, r)
	}
}

func TestASpriteMaterialOverlaysTheBlendAndBillboard(t *testing.T) {
	d := &material.Doc{Name: "glow", Domain: material.DomainSprite}
	d.AddEffect(material.KindBlendMode).Set("mode", material.EnumValue("additive"))
	d.AddEffect(material.KindBillboard).Set("mode", material.EnumValue("full"))
	putMaterial(t, d)

	s := Sprite{Texture: "grass", Blend: gfx.SpriteBlendCutout, Billboard: gfx.BillboardFixed,
		Material: "glow", Alpha: 1, Tint: mgl32.Vec3{1, 1, 1}}
	if s.DrawBlend() != gfx.SpriteBlendAdditive {
		t.Errorf("blend = %v", s.DrawBlend())
	}
	if s.DrawBillboard() != gfx.BillboardFull {
		t.Errorf("billboard = %v", s.DrawBillboard())
	}
	// The sprite's own texture survives: the material declared none.
	if s.DrawTexture() != "grass" {
		t.Errorf("texture = %q", s.DrawTexture())
	}
}

func TestASpriteMaterialsTintAndAlphaMultiply(t *testing.T) {
	// Rather than replace, so a script fading one sprite out still reaches
	// zero and an author's red lamp stays red under a warming material.
	d := &material.Doc{Name: "warm", Domain: material.DomainSprite}
	e := d.AddEffect(material.KindTint)
	e.Set("color", material.ColorValue(mgl32.Vec3{1, 0.5, 0.5}))
	d.AddEffect(material.KindAlpha).Set("value", material.FloatValue(0.5))
	putMaterial(t, d)

	s := Sprite{Texture: "grass", Material: "warm", Tint: mgl32.Vec3{0.5, 1, 1}, Alpha: 0.5}
	if got := s.DrawTint(); got != (mgl32.Vec3{0.5, 0.5, 0.5}) {
		t.Errorf("tint = %v, want the two multiplied", got)
	}
	if got := s.DrawAlpha(); got != 0.25 {
		t.Errorf("alpha = %v, want the two multiplied", got)
	}
}

func TestASpriteMaterialCanOnlyTurnLitOn(t *testing.T) {
	// `lit` is the presence of an effect, so there is nothing that means "not
	// lit" — a sprite that asked for lighting keeps it.
	putMaterial(t, &material.Doc{Name: "plain", Domain: material.DomainSprite})
	s := Sprite{Texture: "grass", Material: "plain", Lit: true}
	if !s.DrawLit() {
		t.Error("a material with no lit effect must not unlight a sprite that asked for it")
	}
}

func TestAMaterialSuppliedSheetCutsTheUVRect(t *testing.T) {
	d := &material.Doc{Name: "sheet", Domain: material.DomainSprite}
	e := d.AddEffect(material.KindSpriteSheet)
	e.Set("cols", material.FloatValue(2))
	e.Set("rows", material.FloatValue(2))
	putMaterial(t, d)

	s := Sprite{Texture: "grass", Material: "sheet", Frame: 3}
	u0, v0, du, dv := s.UVRect()
	if du != 0.5 || dv != 0.5 {
		t.Errorf("cell size = %v x %v, want a quarter", du, dv)
	}
	if u0 != 0.5 || v0 != 0.5 {
		t.Errorf("frame 3 landed at %v,%v, want the bottom-right cell", u0, v0)
	}
}

func TestADrawableSpriteMayTakeItsTextureFromItsMaterial(t *testing.T) {
	d := &material.Doc{Name: "art", Domain: material.DomainSprite, Texture: "grass"}
	d.AddEffect(material.KindBlendMode)
	putMaterial(t, d)

	s := Sprite{Material: "art", Visible: true, Width: 1, Height: 1}
	if !s.Drawable() {
		t.Error("a sprite whose material supplies the texture is drawable")
	}
	if s.DrawTexture() != "grass" {
		t.Errorf("texture = %q", s.DrawTexture())
	}
}

func TestASkyMaterialIsFoldedIntoTheSky(t *testing.T) {
	d := &material.Doc{Name: "canyon", Domain: material.DomainSky}
	d.AddEffect(material.KindSkyCubemap).Set("px", material.TextureValue("sky_canyon_px"))
	c := d.AddEffect(material.KindClouds)
	c.Set("coverage", material.FloatValue(0.9))
	putMaterial(t, d)

	s := DefaultSky()
	s.Material = "canyon"
	s.ApplyMaterial()

	if s.Kind != SkyCubemap {
		t.Error("a cubemap material should switch the sky to cubemap")
	}
	if s.Faces[0] != "sky_canyon_px" || s.Faces[5] != "sky_canyon_nz" {
		t.Errorf("faces = %v, want all six filled from the one named", s.Faces)
	}
	if s.Clouds == nil || !s.Clouds.Enabled {
		t.Fatal("the cloud deck did not arrive")
	}
	if s.Clouds.Coverage != 0.9 {
		t.Errorf("coverage = %v", s.Clouds.Coverage)
	}
}

func TestASkyWithNoMaterialIsUntouched(t *testing.T) {
	s := DefaultSky()
	before := s
	s.ApplyMaterial()
	if s.Kind != before.Kind || s.Zenith != before.Zenith || s.Tint != before.Tint {
		t.Error("ApplyMaterial changed a sky that named no material")
	}
}

func TestASkyMaterialLeavesWhatItDoesNotMention(t *testing.T) {
	d := &material.Doc{Name: "just_clouds", Domain: material.DomainSky}
	d.AddEffect(material.KindClouds)
	putMaterial(t, d)

	s := DefaultSky()
	s.Zenith = mgl32.Vec3{0.1, 0.2, 0.3}
	s.Material = "just_clouds"
	s.ApplyMaterial()
	if s.Zenith != (mgl32.Vec3{0.1, 0.2, 0.3}) {
		t.Errorf("a clouds-only material overwrote the gradient: %v", s.Zenith)
	}
}

func TestSyncSkyMaterialReappliesWhenTheLibraryMoves(t *testing.T) {
	d := &material.Doc{Name: "deck", Domain: material.DomainSky}
	d.AddEffect(material.KindClouds).Set("coverage", material.FloatValue(0.2))
	putMaterial(t, d)

	sky := DefaultSky()
	sky.Material = "deck"
	lvl := &Level{Sky: &sky}
	InvalidateSkyMaterial()
	SyncSkyMaterial(lvl)
	if lvl.Sky.Clouds == nil || lvl.Sky.Clouds.Coverage != 0.2 {
		t.Fatalf("first sync did not apply: %+v", lvl.Sky.Clouds)
	}

	// An edit in the material editor: the document is replaced and the
	// generation moves, and the next frame's sync has to notice.
	d2 := &material.Doc{Name: "deck", Domain: material.DomainSky}
	d2.AddEffect(material.KindClouds).Set("coverage", material.FloatValue(0.85))
	material.Put(d2)
	SyncSkyMaterial(lvl)
	if lvl.Sky.Clouds.Coverage != 0.85 {
		t.Errorf("coverage = %v, want the edited material's", lvl.Sky.Clouds.Coverage)
	}
}
