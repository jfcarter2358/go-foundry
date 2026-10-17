package gfx

// material_fx_shared_atlas_test.go — a splat with a shared atlas
// (material.SurfaceFx.SplatAtlas; docs/regions/PHASE_6.md "Per-tile
// materials") is built from the atlas's names and samples, for each of its
// layers, the tile of the same texture there; without one, its own layers
// and tiles exactly as before.

import (
	"hash/fnv"
	"testing"

	"foundry/internal/material"
)

func TestSplatLayerTilesFollowTheSharedAtlas(t *testing.T) {
	var f material.SurfaceFx
	for i, n := range []string{"b", "e", "f", "h"} {
		f.SplatLayers[i].Texture = n
	}
	own := splatAtlasNames(&f)
	if own != [material.SplatAtlasMax]string{"b", "e", "f", "h"} {
		t.Fatalf("own names %v", own)
	}
	arr := &SplatArrays{Layer: [material.SplatAtlasMax]int{0, 1, 2, 3, -1, -1, -1, -1}}
	if got := splatLayerTiles(&f, arr); got != [material.SplatLayersMax]int{0, 1, 2, 3, -1, -1, -1, -1} {
		t.Errorf("own atlas tiles %v", got)
	}
	f.SplatAtlas = [material.SplatAtlasMax]string{"a", "b", "c", "d", "e", "f", "g", "h"}
	if splatAtlasNames(&f) != f.SplatAtlas {
		t.Error("a shared atlas is not built from its names")
	}
	// The union's atlas: "c" and "d" share a tile (the same texture twice
	// would), so every later layer's tile is one less than its index.
	arr = &SplatArrays{Layer: [material.SplatAtlasMax]int{0, 1, 2, 2, 3, 4, 5, 6}}
	want := [material.SplatLayersMax]int{1, 3, 4, 6, -1, -1, -1, -1}
	if got := splatLayerTiles(&f, arr); got != want {
		t.Errorf("shared atlas tiles %v, want %v", got, want)
	}
}

// A palette: a tile's splat names layers 9 and up of a 32-name atlas and
// samples their tiles there.
func TestSplatLayerTilesInAPalette(t *testing.T) {
	var f material.SurfaceFx
	for i := range f.SplatAtlas {
		f.SplatAtlas[i] = string(rune('A' + i))
	}
	for i, n := range []string{"J", "R", "zz", "C"} { // atlas 9, 17, none, 2
		f.SplatLayers[i].Texture = n
	}
	var arr SplatArrays
	for i := range arr.Layer {
		arr.Layer[i] = i
	}
	got := splatLayerTiles(&f, &arr)
	want := [material.SplatLayersMax]int{9, 17, -1, 2, -1, -1, -1, -1}
	if got != want {
		t.Fatalf("palette tiles %v, want %v", got, want)
	}
	if n := splatAtlasNames(&f); n != f.SplatAtlas {
		t.Fatal("a palette is not built from its names")
	}
}

// Palette geometry and keys: nine or more distinct names are 8 across, as
// many rows as needed; eight names or fewer hash as they always did.
func TestSplatPaletteKeysAndGeometry(t *testing.T) {
	var eight [material.SplatAtlasMax]string
	for i := 0; i < 8; i++ {
		eight[i] = string(rune('a' + i))
	}
	var e8 [material.SplatLayersMax]string
	copy(e8[:], eight[:8])
	h := fnv8(e8)
	if splatKeyHash(eight) != h {
		t.Fatal("eight names' key moved")
	}
	pal := eight
	pal[20] = "u"
	if splatKeyHash(pal) == h {
		t.Fatal("a palette keys like its first eight")
	}
	// 32 layers at 512: 8×4 tiles of 544, 4352×2176 — the eight-layer 1k
	// pair's size.
	a, n, w, hh := packSplatAtlasGridRows(make([]splatDecoded, 0), 512, 8, 4)
	if w != 4352 || hh != 2176 || len(a) != 4*w*hh || len(n) != len(a) {
		t.Fatalf("palette atlas %d×%d", w, hh)
	}
}

func fnv8(names [material.SplatLayersMax]string) uint64 {
	h := fnv.New64a()
	for _, n := range names {
		h.Write([]byte(n))
		var z [1]byte
		h.Write(z[:])
	}
	return h.Sum64()
}
