package stages

// region_material_tiles_test.go — region_material's per-tile materials
// (region_material_tiles.go; docs/regions/PHASE_6.md "Per-tile
// materials"): which layers a tile counts, how sets are widened, the
// windows of tiles, strips and ring tiles; then a cooked two-biome world
// end to end — tiles deep in a biome re-dressed in four-layer materials
// that share the union's atlas, weights and per-layer settings, the band
// left on the union, and the switch off hashing as before.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

func TestChooseLayerSetsWidensToTheFewestMaterials(t *testing.T) {
	m := func(ls ...int) layerMask {
		var out layerMask
		for _, l := range ls {
			out |= 1 << l
		}
		return out
	}
	raw := []layerMask{m(0), m(0, 3), m(4, 5), m(0, 4, 5, 6), m(0, 4), m(0, 1, 2, 3, 4), m(1, 6), m(0, 1, 2, 3)}
	got := chooseLayerSets(raw, 7)
	want := map[layerMask]layerMask{
		m(0): m(0, 1, 2, 3), m(0, 3): m(0, 1, 2, 3), m(0, 1, 2, 3): m(0, 1, 2, 3), // inside 1–4
		m(4, 5):          m(4, 5, 6), // inside 5–7 (seven layers)
		m(0, 4, 5, 6):    m(0, 4, 5, 6),
		m(0, 4):          m(0, 4, 5, 6), // joins the mixed set holding it
		m(1, 6):          m(1, 6),       // no mixed set holds it
		m(0, 1, 2, 3, 4): 0,             // five: the union
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s → %s, want %s", k.tag(), got[k].tag(), v.tag())
		}
	}
	if m(0, 2, 5).tag() != "l136" {
		t.Errorf("tag %q", m(0, 2, 5).tag())
	}
}

func TestPresenceIgnoresWeightsTheShaderCuts(t *testing.T) {
	const n = 4
	w1, w2 := make([]uint8, 4*n*n), make([]uint8, 4*n*n)
	w1[4*(1*n+1)+2] = 1   // layer 3 at byte 1: below the shader's 0.004
	w2[4*(2*n+2)+1] = 2   // layer 6 at byte 2: in
	w1[4*(3*n+3)+0] = 255 // layer 1, outside the window below
	if got := presence(w1, w2, n, texelWindow{0, 2, 0, 2}); got != 1<<5 {
		t.Errorf("presence %s, want l6", got.tag())
	}
	if got := presence(w1, nil, n, texelWindow{-5, 9, -5, 9}); got != 1<<0 {
		t.Errorf("presence over the whole (clamped) map %s, want l1", got.tag())
	}
	// remapWeights puts a set's layers in its channels, ascending.
	r := remapWeights(w1, w2, n, 1<<0|1<<5)
	if r[4*(3*n+3)+0] != 255 || r[4*(2*n+2)+1] != 2 || r[4*(1*n+1)+2] != 0 {
		t.Errorf("remapped weights wrong")
	}
}

func TestTilePlacementWindows(t *testing.T) {
	g := &terrain.Grid{N: 256, Cell: 1, MinX: -128, MinZ: -128}
	tm := &terrain.TilesMeta{Size: 64, Count: 4}
	for _, c := range []struct {
		id   string
		want texelWindow
		ok   bool
	}{
		{"tile_1_2", texelWindow{64 - 3, 128 + 3, 128 - 3, 192 + 3}, true},
		{"strip_s_2", texelWindow{128 - 3, 192 + 3, -3, 64 + 3}, true},      // tile (2, 0)
		{"strip_e_1", texelWindow{192 - 3, 256 + 3, 64 - 3, 128 + 3}, true}, // tile (3, 1)
		{"tile_4_0", texelWindow{}, false},
		{"tile_01_2", texelWindow{}, false},
		{"rock_00001", texelWindow{}, false},
	} {
		got, ok := tilePlacementWindow(c.id, g, tm, 2)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %+v %v, want %+v %v", c.id, got, ok, c.want, c.ok)
		}
	}
	fm := &terrain.FarMeta{Cell: 16, Bounds: terrain.Bounds{MinX: -400, MinZ: -400}}
	segs := []float64{-400, -144, 144, 400}
	if got, ok := farPlacementWindow("far_2_0", fm, segs, segs, 1); !ok || got != (texelWindow{34 - 2, 50 + 2, 0 - 2, 16 + 2}) {
		t.Errorf("far_2_0: %+v %v", got, ok)
	}
}

func TestRegionMaterialPerTileMaterials(t *testing.T) {
	f := procgen.Float
	d := twoBiomeWorld(t, [4]float64{1, 5, 6, 7}, procgen.Params{"far": f(256), "far_cell": f(16), "far_blend": f(64), "far_tile": f(256),
		"cook": procgen.Bool(true), "cook_memory": procgen.Int(1000)})
	off, err := procgen.StageHashes(d)
	if err != nil {
		t.Fatal(err)
	}
	// Off by default and unhashed: the switch at its default is the stage
	// it always was.
	d.StageByID("ground_mat").Params["per_tile"] = procgen.Bool(false)
	if h, _ := procgen.StageHashes(d); h["ground_mat"] != off["ground_mat"] {
		t.Error("per_tile off changed the stage's hash")
	}
	d.StageByID("ground_mat").Params["per_tile"] = procgen.Bool(true)
	d.StageByID("ground_mat").Params["tile_margin"] = f(8)
	if h, _ := procgen.StageHashes(d); h["ground_mat"] == off["ground_mat"] || h["ground"] != off["ground"] {
		t.Error("per_tile on did not rerun the material alone")
	}
	r, a, tr, res := runRegionMaterialDoc(t, d)
	if len(a.Layers) != 7 || len(a.Tiles) == 0 {
		t.Fatalf("%d layers, per-tile materials %+v", len(a.Layers), a.Tiles)
	}
	union := map[string]bool{a.Material: true, a.FarMaterial: true}
	byName := map[string]RegionTileMaterial{}
	for _, tm := range a.Tiles {
		byName[tm.Material] = tm
	}
	// Every tile, strip and ring tile is placed once, by the terrain or
	// (re-dressed) by the material, its model and position untouched.
	type seen struct {
		stage string
		pl    procgen.Placement
	}
	got := map[string]seen{}
	for _, p := range r.Placements() {
		for _, st := range []string{"ground", "ground_mat"} {
			pre := procgen.StagePrefix(d.Name, st)
			if id, ok := strings.CutPrefix(p.ID, pre); ok && (strings.HasPrefix(id, "tile_") || strings.HasPrefix(id, "strip_") ||
				strings.HasPrefix(id, "far_")) {
				if _, dup := got[id]; dup {
					t.Fatalf("%s placed twice", id)
				}
				got[id] = seen{st, p}
			}
		}
	}
	orig := map[string]procgen.Placement{}
	for _, p := range r.Stage("ground").Placements {
		orig[p.ID] = p
	}
	if len(got) != len(orig) {
		t.Fatalf("%d tiles placed, the terrain made %d", len(got), len(orig))
	}
	redressed := map[string]int{}
	for id, s := range got {
		o := orig[id]
		if s.pl.Model != o.Model || s.pl.Position != o.Position || s.pl.Collision != o.Collision {
			t.Errorf("%s moved: %+v from %+v", id, s.pl, o)
		}
		if s.stage == "ground" {
			if !union[s.pl.Material] {
				t.Errorf("%s left wearing %q", id, s.pl.Material)
			}
			continue
		}
		tm, ok := byName[s.pl.Material]
		if !ok {
			t.Fatalf("%s re-dressed in %q, not a per-tile material", id, s.pl.Material)
		}
		if tm.Far != strings.HasPrefix(id, "far_") {
			t.Errorf("%s wears %s (far %v)", id, tm.Material, tm.Far)
		}
		redressed[tm.Material]++
	}
	// The hills' west tiles are four-layer, and so is some of the plain;
	// the band's are not.
	g := tr.Grid
	tsz := tr.Meta.Tiles.Size
	west, band := 0, 0
	for id, s := range got {
		var ti, tj int
		if _, err := fmt.Sscanf(id, "tile_%d_%d", &ti, &tj); err != nil || tileName(ti, tj) != id {
			continue
		}
		x0 := g.MinX + float64(ti)*tsz
		switch {
		case x0+tsz < -60 && s.stage == "ground_mat":
			west++
		case x0 >= -tsz && x0 < tsz && s.stage == "ground": // either side of the seam at x = 0
			band++
		}
	}
	if west == 0 || band == 0 {
		t.Errorf("%d west tiles four-layer, %d band tiles on the union; want some of each", west, band)
	}
	// Each per-tile material: a four-layer splat of its layers, with the
	// union's per-layer settings, weights that are the union's bytes for
	// those layers, and the union's atlas.
	ud, err := material.ReadMaterial(filepath.Join(res, "materials", a.Material+".fmat"))
	if err != nil {
		t.Fatal(err)
	}
	ufx := material.ResolveSurfaceDoc(ud).Fx
	var atlas [material.SplatAtlasMax]string
	for i, l := range a.Layers {
		atlas[i] = l.Name
	}
	uw1, uw2 := nrgbaOf(t, res, a.Weights), nrgbaOf(t, res, a.Weights2)
	for _, tm := range a.Tiles {
		if redressed[tm.Material] != tm.Placements {
			t.Errorf("%s: %d placements wear it, the manifest says %d", tm.Material, redressed[tm.Material], tm.Placements)
		}
		md, err := material.ReadMaterial(filepath.Join(res, "materials", tm.Material+".fmat"))
		if err != nil {
			t.Fatal(err)
		}
		fx := material.ResolveSurfaceDoc(md).Fx
		if !fx.HasSplat || fx.SplatEight || fx.SplatAtlas != atlas || fx.SplatWeights != tm.Weights {
			t.Fatalf("%s: not a four-layer splat on the union's atlas: %+v", tm.Material, fx)
		}
		if tm.Far {
			continue
		}
		if fx.SplatBlend != ufx.SplatBlend || fx.SplatStrata != ufx.SplatStrata || fx.SplatMin != ufx.SplatMin || fx.SplatMax != ufx.SplatMax {
			t.Errorf("%s: material-level knobs differ from the union's", tm.Material)
		}
		w := nrgbaOf(t, res, tm.Weights)
		for c, l := range tm.Layers {
			if fx.SplatLayers[c] != ufx.SplatLayers[l-1] {
				t.Errorf("%s layer %d: %+v, the union's layer %d %+v", tm.Material, c+1, fx.SplatLayers[c], l, ufx.SplatLayers[l-1])
			}
			src, ch := uw1, l-1
			if l > 4 {
				src, ch = uw2, l-5
			}
			for k := 0; k < len(w.Pix)/4; k += 97 {
				if w.Pix[4*k+c] != src.Pix[4*k+ch] {
					t.Fatalf("%s: weight %d of texel %d is %d, the union's layer %d %d", tm.Material, c, k, w.Pix[4*k+c], l, src.Pix[4*k+ch])
				}
			}
		}
	}
	t.Logf("per-tile: %+v", a.Tiles)
}
