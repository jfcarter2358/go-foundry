package material

// material_splat_atlas_test.go — the splat's shared atlas (`atlas1..8`,
// SurfaceFx.SplatAtlas; docs/regions/PHASE_6.md "Per-tile materials"): a
// four-layer tile material of a region terrain names its eight-layer
// union's layers so both sample one atlas. Kept only when every layer the
// splat draws is in it; a splat that names none resolves as it always did.

import "testing"

func TestSplatAtlasIsKeptOnlyWhenItHoldsEveryLayer(t *testing.T) {
	union := [SplatAtlasMax]string{"a", "b", "c", "d", "e", "f", "g", "h"}
	mk := func(layers []string, atlas bool) SurfaceFx {
		d := &Doc{Name: "t", Domain: DomainSurface}
		e := d.AddEffect(KindSplat)
		e.Set("weights", TextureValue("w"))
		for i, l := range layers {
			e.Set(SplatLayerParam(i, ""), TextureValue(l))
		}
		if atlas {
			for i, n := range union {
				e.Set(SplatAtlasParam(i), TextureValue(n))
			}
		}
		return ResolveSurfaceDoc(d).Fx
	}
	if fx := mk([]string{"b", "e", "f", "h"}, true); fx.SplatAtlas != union || fx.SplatEight || !fx.HasSplat {
		t.Errorf("a four-layer splat inside the atlas: %+v", fx)
	}
	if fx := mk([]string{"b", "x"}, true); fx.SplatAtlas != ([SplatAtlasMax]string{}) {
		t.Errorf("a layer outside the atlas kept it: %v", fx.SplatAtlas)
	}
	plain, again := mk([]string{"b", "e"}, false), mk([]string{"b", "e"}, false)
	if plain.SplatAtlas != ([SplatAtlasMax]string{}) || plain != again {
		t.Errorf("no atlas named: %v", plain.SplatAtlas)
	}
	if SplatAtlasParam(0) != "atlas1" || SplatAtlasParam(7) != "atlas8" || SplatAtlasParam(9) != "atlas10" || SplatAtlasParam(31) != "atlas32" {
		t.Error("atlas param names")
	}
}

// A palette of more than eight (docs/regions/PRESETS_1.md): a tile's splat
// names layers 9 and up of a 32-layer atlas and keeps it.
func TestSplatAtlasHoldsAPalette(t *testing.T) {
	var pal [SplatAtlasMax]string
	for i := range pal {
		pal[i] = "p" + string(rune('A'+i))
	}
	d := &Doc{Name: "t", Domain: DomainSurface}
	e := d.AddEffect(KindSplat)
	e.Set("weights", TextureValue("w"))
	e.Set("weights2", TextureValue("w2"))
	for i, l := range []string{pal[9], pal[17], pal[30], pal[2], pal[31]} {
		e.Set(SplatLayerParam(i, ""), TextureValue(l))
	}
	for i, n := range pal {
		e.Set(SplatAtlasParam(i), TextureValue(n))
	}
	fx := ResolveSurfaceDoc(d).Fx
	if fx.SplatAtlas != pal || !fx.SplatEight {
		t.Fatalf("palette not kept: %v eight %v", fx.SplatAtlas, fx.SplatEight)
	}
	// The encoded file round-trips atlas10..32.
	data, err := EncodeMaterial(d)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeMaterial(data)
	if err != nil {
		t.Fatal(err)
	}
	if ResolveSurfaceDoc(back).Fx.SplatAtlas != pal {
		t.Fatal("atlas10..32 lost in the file")
	}
}
