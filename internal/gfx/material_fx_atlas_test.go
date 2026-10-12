package gfx

// material_fx_atlas_test.go — the splat atlases' when and how, without GL:
// the library-wide EnsureMaterialTextures (startup, a material save, a
// texture reload) must not build a splat's atlases or load its data maps —
// level load and the draw path do, for the materials actually in use — and
// the geometry rule that lets a compressed atlas be packed at full size and
// drop its top levels instead of being packed small.

import (
	"image"
	"testing"

	"foundry/internal/material"
	"foundry/internal/texcompress"

	"github.com/go-gl/mathgl/mgl32"
)

func TestEnsureMaterialTexturesBuildsNoSplatAtlas(t *testing.T) {
	// Uploads "on", as in the game: the old EnsureMaterialTextures went on
	// into EnsureMaterialFxTextures, which would build the atlas here (and,
	// its layers not existing, fail inside buildSplatArrays before any GL).
	prev := GeometryUploadsEnabled
	GeometryUploadsEnabled = true
	t.Cleanup(func() { GeometryUploadsEnabled = prev })

	const name = "fxatlas_test_splat"
	d := &material.Doc{Name: name, Domain: material.DomainSurface}
	e := d.AddEffect(material.KindSplat)
	e.Set("weights", material.TextureValue("fxatlas_test_no_such_weights"))
	e.Set("bounds_min", material.Vec2Value(mgl32.Vec2{-10, -10}))
	e.Set("bounds_max", material.Vec2Value(mgl32.Vec2{10, 10}))
	e.Set("layer1", material.TextureValue("fxatlas_test_no_such_layer"))
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("%v", probs)
	}
	material.Put(d)
	t.Cleanup(func() { material.Remove(name) })
	if s, ok := resolveSurfaceFx(name); !ok || !s.HasSplat {
		t.Fatal("the test material does not resolve to a splat")
	}

	builds, atlases, maps := splatBuilds, len(splatArrays), len(fxDataMaps)
	EnsureMaterialTextures()
	if splatBuilds != builds || len(splatArrays) != atlases {
		t.Errorf("EnsureMaterialTextures built splat atlases (%d builds, %d cached; were %d, %d)",
			splatBuilds, len(splatArrays), builds, atlases)
	}
	if len(fxDataMaps) != maps {
		t.Errorf("EnsureMaterialTextures loaded data maps (%d, were %d)", len(fxDataMaps), maps)
	}
}

func TestSplatHalvesExactly(t *testing.T) {
	for _, c := range []struct {
		full, skip int
		want       bool
	}{
		{2048, 1, true}, {2048, 3, true}, {1024, 2, true}, {512, 1, true},
		// 1000 halves to 500 with a 15-texel gutter, not 31's half.
		{1000, 1, false},
		// An odd size does not halve at all.
		{1025, 1, false},
	} {
		if got := splatHalvesExactly(c.full, c.skip); got != c.want {
			t.Errorf("splatHalvesExactly(%d, %d) = %v, want %v", c.full, c.skip, got, c.want)
		}
		if c.want {
			if e, s := splatEdge(c.full), splatEdge(c.full>>c.skip); e>>c.skip != s || s<<c.skip != e {
				t.Errorf("%d skip %d: edge %d halves to %d, small atlas is %d", c.full, c.skip, e, e>>c.skip, s)
			}
		}
	}
}

// The full-size atlas, box-halved, is the small one texel for texel when the
// geometry halves exactly — so dropping levels is the old skip-mips result.
func TestSplatPackFullThenHalveIsPackSmall(t *testing.T) {
	const full = 512
	layer := func(seed int) splatDecoded {
		var l splatDecoded
		for k, p := range []**image.RGBA{&l.albedo, &l.height, &l.normal} {
			img := image.NewRGBA(image.Rect(0, 0, full, full))
			for i := range img.Pix {
				img.Pix[i] = uint8((i*7 + i/(4*full)*13 + seed*31 + k*57) % 251)
			}
			*p = img
		}
		return l
	}
	layers := []splatDecoded{layer(1), layer(2), layer(3)}
	bigA, bigN, bigEdge := packSplatAtlases(layers, full)
	smallA, smallN, smallEdge := packSplatAtlases(layers, full/2)
	if bigEdge/2 != smallEdge {
		t.Fatalf("edges %d and %d", bigEdge, smallEdge)
	}
	for _, c := range []struct {
		name       string
		big, small []byte
	}{{"albedo", bigA, smallA}, {"normal", bigN, smallN}} {
		// texcompress.Downsample is how the cached chain's levels are made.
		h := texcompress.Downsample(&image.RGBA{Pix: c.big, Stride: 4 * bigEdge, Rect: image.Rect(0, 0, bigEdge, bigEdge)}).Pix
		diff := 0
		for i := range h {
			if h[i] != c.small[i] {
				diff++
			}
		}
		if diff != 0 {
			t.Errorf("%s: the halved full atlas differs from the small one in %d of %d bytes", c.name, diff, len(h))
		}
	}
}
