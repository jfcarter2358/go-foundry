package material

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestAnUnsetParameterReadsItsKindsDefault(t *testing.T) {
	d := &Doc{Domain: DomainSurface}
	d.AddEffect(KindWater)
	s := ResolveSurfaceDoc(d)
	if !s.HasWater {
		t.Fatal("a bare water effect should resolve as water")
	}
	// The defaults are DefaultWater's to the digit (gfx.DefaultWaterParams
	// copies the same value), so a bare effect and a water sector agree.
	if s.Water != DefaultWater() {
		t.Errorf("water defaults drifted: %+v, want %+v", s.Water, DefaultWater())
	}
	if s.Water.WaveAmplitude != 0.03 || s.Water.WaveLength != 4 || s.Water.WaveSpeed != 0.3 {
		t.Errorf("the swell's defaults moved: %+v", s.Water)
	}
}

func TestAMaterialOverridesOnlyWhatItDeclares(t *testing.T) {
	d := &Doc{Domain: DomainSurface}
	d.AddEffect(KindUVScroll).Set("rate", Vec2Value(mgl32.Vec2{0, 0.25}))
	s := ResolveSurfaceDoc(d)
	if !s.HasScroll || s.UVScroll != (mgl32.Vec2{0, 0.25}) {
		t.Errorf("scroll = %v (has %v)", s.UVScroll, s.HasScroll)
	}
	// Everything the document said nothing about must come back "not set", so
	// the wearer's own value survives.
	if s.HasAlpha || s.HasWater || s.HasBlend || s.HasSmooth || s.Unlit {
		t.Errorf("a scroll-only material claimed other effects: %+v", s)
	}
}

func TestAZeroScrollRateIsStillAScroll(t *testing.T) {
	// The distinction the Has* flags exist for: a rate of zero and no scroll
	// effect are different answers.
	d := &Doc{Domain: DomainSurface}
	d.AddEffect(KindUVScroll).Set("rate", Vec2Value(mgl32.Vec2{}))
	if s := ResolveSurfaceDoc(d); !s.HasScroll {
		t.Error("a scroll effect set to zero resolved as no scroll at all")
	}
}

func TestADisabledEffectDoesNotResolve(t *testing.T) {
	d := &Doc{Domain: DomainSurface}
	d.AddEffect(KindUnlit).Disabled = true
	if ResolveSurfaceDoc(d).Unlit {
		t.Error("a disabled effect still resolved")
	}
	if d.Find(KindUnlit) == nil {
		t.Error("Find must still see a disabled effect — the editor switches it back on")
	}
}

func TestHalfABlendIsNoBlend(t *testing.T) {
	d := &Doc{Domain: DomainSurface}
	d.AddEffect(KindBlend).Set("texture", TextureValue("moss"))
	if ResolveSurfaceDoc(d).HasBlend {
		t.Error("a blend with no mask has nothing to mix by and must not run")
	}
	d.Find(KindBlend).Set("mask", TextureValue("dirt"))
	if !ResolveSurfaceDoc(d).HasBlend {
		t.Error("a blend with both textures should run")
	}
}

func TestRoundTripKeepsWhatWasChangedAndDropsWhatWasNot(t *testing.T) {
	d := &Doc{Name: "hangar_strip", Domain: DomainSurface, Texture: "hull_plate"}
	d.AddEffect(KindUnlit)
	e := d.AddEffect(KindWater)
	e.Set("wave_speed", FloatValue(1.5))
	e.Set("wave_length", FloatValue(DefaultWater().WaveLength)) // equal to the default

	data, err := EncodeMaterial(d)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "wave_speed") {
		t.Errorf("a changed parameter was not written:\n%s", text)
	}
	if strings.Contains(text, "wave_length") {
		t.Errorf("a parameter still at its default was written:\n%s", text)
	}

	back, err := DecodeMaterial(data)
	if err != nil {
		t.Fatal(err)
	}
	back.Name = d.Name
	if back.Texture != "hull_plate" || back.Domain != DomainSurface {
		t.Errorf("header did not round-trip: %+v", back)
	}
	s := ResolveSurfaceDoc(back)
	if !s.Unlit || !s.HasWater || s.Water.WaveSpeed != 1.5 || s.Water.WaveLength != DefaultWater().WaveLength {
		t.Errorf("round trip changed the surface: %+v", s)
	}
}

func TestEncodingIsStableAcrossSaves(t *testing.T) {
	// A save with nothing changed must produce no diff, or every material in
	// the project churns in version control.
	d := &Doc{Name: "m", Domain: DomainSurface}
	e := d.AddEffect(KindClouds)
	e.Set("coverage", FloatValue(0.8))
	e.Set("density", FloatValue(0.2))
	e.Set("scale", FloatValue(600))
	first, _ := EncodeMaterial(d)
	for i := 0; i < 8; i++ {
		again, _ := EncodeMaterial(d)
		if string(again) != string(first) {
			t.Fatalf("encoding shuffled between saves:\n%s\n---\n%s", first, again)
		}
	}
}

func TestAnUnknownKeyIsKeptSoAnOlderBuildCannotDeleteIt(t *testing.T) {
	src := "kind: surface\neffects:\n  - effect: water\n    wave_speed: 2\n    future_param: 7\n"
	d, err := DecodeMaterial([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out, err := EncodeMaterial(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "future_param") {
		t.Errorf("an unrecognised parameter was dropped on save:\n%s", out)
	}
}

func TestAnUnknownEffectKindSurvivesARoundTrip(t *testing.T) {
	src := "kind: surface\neffects:\n  - effect: iridescence\n    strength: 3\n"
	d, err := DecodeMaterial([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Effects) != 1 || d.Effects[0].Kind != "iridescence" {
		t.Fatalf("effect = %+v", d.Effects)
	}
	out, _ := EncodeMaterial(d)
	if !strings.Contains(string(out), "iridescence") || !strings.Contains(string(out), "strength") {
		t.Errorf("an unknown effect kind was dropped:\n%s", out)
	}
	if len(d.Validate()) == 0 {
		t.Error("Validate should report an effect kind that does not exist")
	}
}

func TestVectorsReadAsEitherASequenceOrAMap(t *testing.T) {
	seq, err := DecodeMaterial([]byte("kind: surface\neffects:\n  - effect: uv_scroll\n    rate: [0.5, -0.25]\n"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := DecodeMaterial([]byte("kind: surface\neffects:\n  - effect: uv_scroll\n    rate: {x: 0.5, y: -0.25}\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := mgl32.Vec2{0.5, -0.25}
	if got := ResolveSurfaceDoc(seq).UVScroll; got != want {
		t.Errorf("sequence form = %v", got)
	}
	if got := ResolveSurfaceDoc(m).UVScroll; got != want {
		t.Errorf("map form = %v", got)
	}
}

func TestAValueOutsideItsRangeIsClampedOnLoad(t *testing.T) {
	d, err := DecodeMaterial([]byte("kind: surface\neffects:\n  - effect: alpha\n    value: 4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveSurfaceDoc(d).Alpha; got != 1 {
		t.Errorf("alpha = %v, want clamped to 1", got)
	}
}

func TestAnUnknownEnumChoiceLoadsAsTheFirst(t *testing.T) {
	d, err := DecodeMaterial([]byte("kind: sprite\neffects:\n  - effect: blend_mode\n    mode: refractive\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveQuadDoc(d).BlendMode; got != "cutout" {
		t.Errorf("mode = %q, want the first choice", got)
	}
}

func TestValidateNamesAnEffectInTheWrongDomain(t *testing.T) {
	d := &Doc{Domain: DomainSprite}
	d.AddEffect(KindWater)
	problems := d.Validate()
	if len(problems) != 1 || !strings.Contains(problems[0], "sprite") {
		t.Errorf("problems = %v", problems)
	}
}

func TestATintResolvesColourTimesIntensity(t *testing.T) {
	d := &Doc{Domain: DomainSprite}
	e := d.AddEffect(KindTint)
	e.Set("color", ColorValue(mgl32.Vec3{0.5, 0.5, 0.5}))
	e.Set("intensity", FloatValue(2))
	if got := ResolveQuadDoc(d).Tint; got != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("tint = %v", got)
	}
}

func TestOneCubemapFaceFillsTheOtherFive(t *testing.T) {
	d := &Doc{Domain: DomainSky}
	d.AddEffect(KindSkyCubemap).Set("px", TextureValue("sky_canyon_px"))
	s := ResolveSkyDoc(d)
	want := [SkyFaces]string{
		"sky_canyon_px", "sky_canyon_nx", "sky_canyon_py",
		"sky_canyon_ny", "sky_canyon_pz", "sky_canyon_nz",
	}
	if s.Faces != want {
		t.Errorf("faces = %v", s.Faces)
	}
}

func TestTexturesListsEverythingAMaterialPullsIn(t *testing.T) {
	d := &Doc{Domain: DomainSurface, Texture: "rock"}
	b := d.AddEffect(KindBlend)
	b.Set("texture", TextureValue("moss"))
	b.Set("mask", TextureValue("splat"))
	d.AddEffect(KindNormalMap).Set("map", TextureValue("rock_rough_normal"))
	got := d.Textures()
	for _, want := range []string{"rock", "moss", "splat", "rock_rough_normal"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Textures() missed %q: %v", want, got)
		}
	}
}

func TestTexturesFollowsACubemapNamedByOneFace(t *testing.T) {
	d := &Doc{Domain: DomainSky}
	d.AddEffect(KindSkyCubemap).Set("pz", TextureValue("sky_day_pz"))
	got := d.Textures()
	if len(got) != SkyFaces {
		t.Errorf("Textures() = %v, want all six faces", got)
	}
}

func TestCloneDoesNotShareParameterMaps(t *testing.T) {
	d := &Doc{Domain: DomainSurface}
	d.AddEffect(KindAlpha).Set("value", FloatValue(0.5))
	c := d.Clone()
	c.Find(KindAlpha).Set("value", FloatValue(0.1))
	if d.Find(KindAlpha).Float("value") != 0.5 {
		t.Error("editing a clone reached the original")
	}
}

func TestPutBumpsTheGeneration(t *testing.T) {
	defer Clear()
	before := Generation()
	Put(&Doc{Name: "gen_test", Domain: DomainSurface})
	if Generation() == before {
		t.Error("installing a material must invalidate every cached resolution")
	}
	if !Exists("gen_test") {
		t.Error("the material did not land in the library")
	}
}

func TestResolveRefusesTheWrongDomain(t *testing.T) {
	defer Clear()
	Put(&Doc{Name: "the_sky", Domain: DomainSky})
	if _, ok := ResolveSurface("the_sky"); ok {
		t.Error("a wall must not be able to wear a sky")
	}
	if _, ok := ResolveSky("the_sky"); !ok {
		t.Error("the sky should resolve as a sky")
	}
}

func TestEveryBuiltInKindDeclaresItselfCoherently(t *testing.T) {
	for _, k := range AllKinds() {
		if len(k.Domains) == 0 {
			t.Errorf("kind %q belongs to no domain, so nothing can ever use it", k.Name)
		}
		if k.Label == "" || k.Doc == "" {
			t.Errorf("kind %q has no label or no documentation; the editor shows both", k.Name)
		}
		seen := map[string]bool{}
		for _, p := range k.Params {
			if seen[p.Name] {
				t.Errorf("kind %q declares %q twice", k.Name, p.Name)
			}
			seen[p.Name] = true
			if p.Name == effectKindKey || p.Name == effectEnabledKey {
				t.Errorf("kind %q has a parameter called %q, which the file format reserves", k.Name, p.Name)
			}
			if p.Type == ParamEnum && len(p.Choices) == 0 {
				t.Errorf("kind %q parameter %q is an enum with no choices", k.Name, p.Name)
			}
			if p.Type == ParamEnum && !containsStr(p.Choices, p.Default.Str) {
				t.Errorf("kind %q parameter %q defaults to %q, which is not a choice",
					k.Name, p.Name, p.Default.Str)
			}
			if p.Min != p.Max && p.Clamp(p.Default) != p.Default {
				t.Errorf("kind %q parameter %q defaults outside its own range", k.Name, p.Name)
			}
		}
	}
}

func TestThePreviewSlotIsNotInAnyPicker(t *testing.T) {
	defer Clear()
	Put(&Doc{Name: "real", Domain: DomainSurface})
	SetPreview(&Doc{Name: "\x00preview", Domain: DomainSurface})

	for _, n := range Names() {
		if n == "\x00preview" {
			t.Fatal("the preview slot appeared in the library listing")
		}
	}
	for _, n := range NamesFor(DomainSurface) {
		if n == "\x00preview" {
			t.Fatal("the preview slot appeared in a surface picker")
		}
	}
	if !Exists("\x00preview") {
		t.Error("the preview must still resolve by name — its own swatch reads it")
	}
	if !Exists("real") {
		t.Error("the preview slot hid the library")
	}
}

func TestSettingAnUnchangedPreviewDoesNotInvalidateCaches(t *testing.T) {
	// The material editor redraws sixty times a second. Bumping the generation
	// on every one of those frames would leave every generation-stamped cache
	// in the engine permanently stale for as long as the editor is open.
	defer Clear()
	d := &Doc{Name: "\x00preview", Domain: DomainSurface}
	d.AddEffect(KindWater)
	SetPreview(d)
	gen := Generation()
	for i := 0; i < 5; i++ {
		SetPreview(d.Clone())
	}
	if Generation() != gen {
		t.Error("re-setting an unchanged preview invalidated every cached resolution")
	}

	changed := d.Clone()
	changed.Find(KindWater).Set("wave_speed", FloatValue(2))
	SetPreview(changed)
	if Generation() == gen {
		t.Error("a changed preview must invalidate the caches")
	}
}

func TestClearingThePreviewLeavesTheLibrary(t *testing.T) {
	defer Clear()
	Put(&Doc{Name: "real", Domain: DomainSurface})
	SetPreview(&Doc{Name: "\x00preview", Domain: DomainSurface})
	SetPreview(nil)
	if Exists("\x00preview") {
		t.Error("the preview did not clear")
	}
	if !Exists("real") {
		t.Error("clearing the preview took the library with it")
	}
}
