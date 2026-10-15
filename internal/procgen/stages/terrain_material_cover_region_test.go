package stages

// terrain_material_cover_region_test.go — the PRESETS_6 cover rules: a
// snow line measured on the environment's own relief (`snow_reference:
// region`), lava on the terrain's lobes (`lava_lobes`) and an ash apron
// with a hole round the vent (`ash_hole`). Each is off by default and a
// world-referenced snow line is untouched by a range.

import (
	"math"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

func coverParamsWith(over procgen.Params) procgen.Params {
	return procgen.Resolve(procgen.KindByName(TerrainMaterialKind).Params, over)
}

func TestSnowReferenceRegionReadsFractionsOfTheEnvironmentsRange(t *testing.T) {
	p := coverParamsWith(procgen.Params{"snow": procgen.String("s"), "snow_reference": procgen.Enum("region"),
		"snow_height": procgen.Vec2(0.5, 0.66), "snow_slope": procgen.Vec2(36, 50), "snow_breakup": procgen.Float(0)})
	cr := coverRulesOf(p, nil, 1).withSnowRange([2]float64{100, 300})
	at := func(h float64) float64 { return cr.shares(&coverSample{h: h, slope: 10})[2] }
	// 0.25 of the range: bare; 0.95: snow; the same massif lifted 500 m
	// (its range lifted with it) snows at the same share of its relief.
	if s := at(150); s != 0 {
		t.Errorf("a quarter of the way up: snow %.3f, want 0", s)
	}
	if s := at(290); math.Abs(s-1) > 1e-9 {
		t.Errorf("near the summits: snow %.3f, want 1", s)
	}
	lifted := coverRulesOf(p, nil, 1).withSnowRange([2]float64{600, 800})
	for _, h := range []float64{150, 220, 290} {
		a, b := at(h), lifted.shares(&coverSample{h: h + 500, slope: 10})[2]
		if math.Abs(a-b) > 1e-9 {
			t.Errorf("h %.0f: %.4f, lifted %.4f — the line moved with the base", h, a, b)
		}
	}
	// A world-referenced line ignores any range: 0.5–0.66 m is world metres.
	w := coverParamsWith(procgen.Params{"snow": procgen.String("s"), "snow_height": procgen.Vec2(100, 120),
		"snow_breakup": procgen.Float(0)})
	cw := coverRulesOf(w, nil, 1)
	before := cw.shares(&coverSample{h: 110, slope: 10})[2]
	cw.withSnowRange([2]float64{0, 1000})
	if after := cw.shares(&coverSample{h: 110, slope: 10})[2]; after != before {
		t.Errorf("a world line changed with a range: %.4f -> %.4f", before, after)
	}
	if r := snowRangeOf(w, nil, nil); r != ([2]float64{}) {
		t.Errorf("a world line asked for a range: %v", r)
	}
}

func TestSnowRangeOfIsTheCoresPercentiles(t *testing.T) {
	g := terrain.NewGrid(127, 1)
	s := g.S()
	core := make([]float32, len(g.H))
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			k := j*s + i
			if i < s/2 {
				g.H[k] = float32(1000 + j) // another environment's high ground
				continue
			}
			g.H[k] = float32(j) // 0..127 on this one's core
			core[k] = 1
		}
	}
	tr := &terrain.Terrain{Grid: g}
	p := coverParamsWith(procgen.Params{"snow": procgen.String("s"), "snow_reference": procgen.Enum("region")})
	r := snowRangeOf(p, tr, core)
	if r[0] > 8 || r[1] < 116 || r[1] > 128 {
		t.Errorf("core range %v, want about 2nd..98th percentile of 0..127", r)
	}
	if all := snowRangeOf(p, tr, nil); all[1] < 1000 {
		t.Errorf("whole-terrain range %v should reach the other ground", all)
	}
}

func TestLavaLobesAndAshApron(t *testing.T) {
	p := coverParamsWith(procgen.Params{"ash": procgen.String("a"), "ash_radius": procgen.Vec2(1.25, 1.9),
		"ash_hole": procgen.Float(1), "lava_lobes": procgen.Float(0.9), "breakup": procgen.Float(0)})
	cr := coverRulesOf(p, []terrain.VentMeta{{X: 0, Z: 0, Radius: 100}}, 1)
	// The apron: none on the cone (0.5 radii), full past its foot (1.1),
	// gone far out (2.5).
	ash := func(x float64) float64 { return cr.shares(&coverSample{x: x, slope: 10})[1] }
	if a := ash(50); a != 0 {
		t.Errorf("ash on the cone %.3f, want 0 (the hole)", a)
	}
	if a := ash(110); a < 0.99 {
		t.Errorf("ash at the foot %.3f, want 1", a)
	}
	if a := ash(250); a != 0 {
		t.Errorf("ash far out %.3f, want 0", a)
	}
	// Lava: a lobe running +x from (100, 0); on its top the cracked layer
	// takes 0.9 of the four, off it nothing changes.
	cr.lobes = []terrain.LobeMeta{{X: 100, Z: 0, DX: 1, DZ: 0, Length: 200, HalfWidth: 20, Seed: 9}}
	on, off := cr.lavaAt(180, 0), cr.lavaAt(180, 150)
	if on < 0.8 || off != 0 {
		t.Fatalf("lava on the lobe %.3f, beside it %.3f", on, off)
	}
	out := make([]float32, groundRoles)
	cr.stack([]float32{0.7, 0.1, 0.1, 0.1}, [3]float64{}, out, on)
	sum := float32(0)
	for _, v := range out {
		sum += v
	}
	want := 0.1*(1-on) + on
	if math.Abs(float64(sum)-1) > 1e-5 || math.Abs(float64(out[1])-want) > 1e-5 {
		t.Errorf("stacked with lava %.3f: %v (cracked want %.3f)", on, out, want)
	}
	// Off by default: no lava share and no hole.
	d := coverRulesOf(coverParamsWith(procgen.Params{"ash": procgen.String("a"), "breakup": procgen.Float(0)}),
		[]terrain.VentMeta{{X: 0, Z: 0, Radius: 100}}, 1)
	d.lobes = cr.lobes
	if d.lavaAt(180, 0) != 0 || d.shares(&coverSample{x: 10, slope: 10})[1] < 0.99 {
		t.Error("lava or the ash hole is on by default")
	}
}
