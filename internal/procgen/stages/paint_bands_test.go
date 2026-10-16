package stages

// paint_bands_test.go — paint layers read by the stages: a scatter rule and
// a grass rule restricted to a painted disc, a grass stage kept out of a
// painted exclude strip, the material stages' overrides and bands, and the
// defaults changing nothing.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/grassfield"
	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/terrain"
)

// paintDisc paints a hard-edged disc of full value into the doc's layer,
// declaring it.
func paintDisc(d *procgen.Doc, layer, kind string, x, z, r float64) {
	d.EnsurePaintLayer(paint.LayerDef{Name: layer, Kind: kind}).Apply(paint.Stroke{X: x, Z: z, Radius: r, Strength: 1})
}

func TestScatterKeepsToAPaintedDisc(t *testing.T) {
	d := smallTerrainDoc()
	f, i, e, ref, v2 := procgen.Float, procgen.Int, procgen.Enum, procgen.StageRef, procgen.Vec2
	d.Stages = append(d.Stages,
		procgen.Stage{Kind: RocksKind, ID: "boulders", Params: procgen.Params{"preset": e("boulder"), "variants": i(1), "triangles": i(300)}},
		procgen.Stage{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"),
			"rules": procgen.List(procgen.Params{"name": procgen.String("rubble"), "rocks": ref("boulders"), "density": f(300), "max_bury": f(2),
				"scale": v2(0.15, 0.3), "slope": v2(0, 90), "cliff": v2(0, 1), "cluster": f(0),
				"paint": procgen.Mask("rubble_here"), "paint_band": v2(0.5, 0.6)})}},
	)
	const cx, cz, r = 6.0, -4.0, 9.0
	paintDisc(d, "rubble_here", paint.KindDensity, cx, cz, r)
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range res.Placements() {
		if !strings.HasPrefix(p.ID, "gen.t.scatter.") {
			continue
		}
		n++
		if dd := math.Hypot(float64(p.Position.X)-cx, float64(p.Position.Z)-cz); dd > r+0.5 {
			t.Fatalf("rock %s at %.1f m from the disc's centre, outside its %g m", p.ID, dd, r)
		}
	}
	if n < 20 {
		t.Fatalf("only %d rocks in the painted disc", n)
	}
}

func grassFieldClumps(t *testing.T, r *procgen.Result, stage string) []grassfield.Instance {
	t.Helper()
	st := r.Stage(stage)
	var out []grassfield.Instance
	for _, name := range st.Artifact.(*GrassFieldArtifact).Fields {
		data, err := os.ReadFile(filepath.Join(st.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		fl, err := grassfield.DecodeField(data)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fl.Instances()...)
	}
	return out
}

func TestGrassFieldKeepsToAPaintedDiscAndOutOfAnExcludeStrip(t *testing.T) {
	withFakePolyHaven(t)
	d := grassDoc()
	g := d.StageByID("grass")
	rule := g.Params["rules"].Items[0]
	rule["paint"] = procgen.Mask("meadow")
	rule["cliff"], rule["slope"] = procgen.Vec2(0, 1), procgen.Vec2(0, 90)
	g.Params["exclude"] = procgen.List(procgen.Params{"layer": procgen.Mask("path")})
	const cx, cz, r = -8.0, 5.0, 12.0
	paintDisc(d, "meadow", paint.KindDensity, cx, cz, r)
	// A strip 4 m wide down x = -8, through the disc.
	path := d.EnsurePaintLayer(paint.LayerDef{Name: "path", Kind: paint.KindExclude})
	path.ApplyLine(paint.Stroke{Radius: 2, Strength: 1}, cx, -30, cx, 30)
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	clumps := grassFieldClumps(t, res, "grass")
	if len(clumps) < 100 {
		t.Fatalf("only %d clumps in the meadow", len(clumps))
	}
	for _, c := range clumps {
		x, z := float64(c.Pos[0]), float64(c.Pos[2])
		if dd := math.Hypot(x-cx, z-cz); dd > r+0.5 {
			t.Fatalf("clump at (%.1f, %.1f), %.1f m from the meadow's centre (radius %g)", x, z, dd, r)
		}
		if math.Abs(x-cx) < 1.5 {
			t.Fatalf("clump at (%.1f, %.1f) on the excluded path", x, z)
		}
	}
	// Painting the path wider reruns the grass; the terrain stays fresh.
	path.ApplyLine(paint.Stroke{Radius: 4, Strength: 1}, cx, -30, cx, 30)
	stale, err := procgen.StaleStages(d, procgen.Options{OutDir: res.OutDir})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(stale, ",") != "grass" {
		t.Errorf("after painting: stale %v, want just grass", stale)
	}
}

// TestPaintDefaultsAreOff: a rule naming no layer gets no mask, and the
// stages' hashes do not see the new params at their defaults.
func TestPaintDefaultsAreOff(t *testing.T) {
	d := grassDoc()
	env := &procgen.Env{Doc: d}
	g := d.StageByID("grass")
	k := procgen.KindByName(GrassFieldKind)
	p := procgen.Resolve(k.Params, g.Params)
	if m := paintRuleMask(env, p, p.Items("rules")[0]); m != nil {
		t.Error("a rule without paint got a mask")
	}
	h0, err := procgen.StageHashes(d)
	if err != nil {
		t.Fatal(err)
	}
	d.Paint = []paint.LayerDef{{Name: "unused", Kind: paint.KindDensity}}
	paintDisc(d, "unused", paint.KindDensity, 0, 0, 10)
	h1, _ := procgen.StageHashes(d)
	for id, h := range h0 {
		if h1[id] != h {
			t.Errorf("a painted layer nobody reads changed stage %s", id)
		}
	}
}

func TestPaintRamp(t *testing.T) {
	for _, c := range []struct {
		v      float64
		band   [2]float64
		invert bool
		want   float64
	}{
		{0.3, [2]float64{0, 1}, false, 0.3},
		{0.3, [2]float64{0.2, 0.4}, false, 0.5},
		{0.9, [2]float64{0.2, 0.4}, false, 1},
		{0.1, [2]float64{0.2, 0.4}, true, 1},
		{0, [2]float64{0, 0}, false, 0},
		{0.01, [2]float64{0, 0}, false, 1},
	} {
		if got := paintRamp(c.v, c.band, c.invert); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("paintRamp(%g, %v, %v) = %g, want %g", c.v, c.band, c.invert, got, c.want)
		}
	}
}

// materialPaintEnv is a doc holding a painted material layer (a disc) and a
// density layer (another disc), as an Env.
func materialPaintEnv() *procgen.Env {
	d := &procgen.Doc{Name: "m"}
	paintDisc(d, "trail", paint.KindMaterial, -5, 0, 4)
	paintDisc(d, "lush", paint.KindDensity, 6, 0, 4)
	return &procgen.Env{Doc: d}
}

func TestTerrainMaterialPaintOverridesAndBands(t *testing.T) {
	env := materialPaintEnv()
	g := terrain.NewGrid(32, 1) // samples from -16 to 16
	sz := g.S()
	// Every sample all sand, but a band of cracked down the right half.
	w := make([]float32, 4*sz*sz)
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			k := 4 * (j*sz + i)
			if g.X(i) > 0 {
				w[k], w[k+1] = 0.5, 0.5
			} else {
				w[k] = 1
			}
		}
	}
	p := procgen.Resolve(terrainMaterialParams(), procgen.Params{"paint": procgen.List(
		procgen.Params{"layer": procgen.Mask("trail"), "target": procgen.String("gravel")},
		procgen.Params{"layer": procgen.Mask("lush"), "target": procgen.String("cracked"), "mode": procgen.Enum("band")},
	)})
	if err := terrainMaterialPaint(env, p, g, w, 4); err != nil {
		t.Fatal(err)
	}
	at := func(x, z float64) []float32 {
		i, j := int(x-g.MinX), int(z-g.MinZ)
		k := 4 * (j*sz + i)
		return w[k : k+4]
	}
	if s := at(-5, 0); s[2] < 0.99 || s[0] > 0.01 {
		t.Errorf("in the trail: %v, want all gravel", s)
	}
	if s := at(-12, 0); s[0] != 1 || s[2] != 0 {
		t.Errorf("outside the trail: %v, want the sand it had", s)
	}
	if s := at(6, 0); math.Abs(float64(s[1])-0.5) > 1e-3 {
		t.Errorf("inside the lush disc the cracked band keeps cracked: %v", s)
	}
	if s := at(12, 0); s[1] > 1e-4 || math.Abs(float64(s[0])-1) > 1e-3 {
		t.Errorf("outside the lush disc cracked should give way to sand: %v", s)
	}
	for k := 0; k < sz*sz; k++ {
		sum := w[4*k] + w[4*k+1] + w[4*k+2] + w[4*k+3]
		if math.Abs(float64(sum)-1) > 1e-3 {
			t.Fatalf("sample %d sums to %g", k, sum)
		}
	}
	bad := procgen.Resolve(terrainMaterialParams(), procgen.Params{"paint": procgen.List(
		procgen.Params{"layer": procgen.Mask("trail"), "target": procgen.String("lava")})})
	if err := terrainMaterialPaint(env, bad, g, w, 4); err == nil {
		t.Error("an unknown target was accepted")
	}
}

func TestRegionMaterialPaintTargetsRolesAndLayers(t *testing.T) {
	env := materialPaintEnv()
	g := terrain.NewGrid(32, 1)
	sz := g.S()
	layers := []*regionLayer{
		{layer: &TextureLayer{ID: "desert_sand", Albedo: "ph_desert_sand"}, role: 0},
		{layer: &TextureLayer{ID: "desert_gravel", Albedo: "ph_desert_gravel"}, role: 2},
		{layer: &TextureLayer{ID: "grass", Albedo: "ph_grass"}, role: 0},
		{layer: &TextureLayer{ID: "savannah_gravel", Albedo: "ph_savannah_gravel"}, role: 2},
	}
	w := make([]float32, 4*sz*sz)
	for k := 0; k < sz*sz; k++ {
		// Desert on the left with a little of its gravel, savannah on the
		// right with a little of its own.
		if g.X(k%sz) < 0 {
			w[4*k], w[4*k+1] = 0.9, 0.1
		} else {
			w[4*k+2], w[4*k+3] = 0.9, 0.1
		}
	}
	p := procgen.Resolve(regionMaterialParams(), procgen.Params{"paint": procgen.List(
		procgen.Params{"layer": procgen.Mask("trail"), "target": procgen.String("gravel")},
		procgen.Params{"layer": procgen.Mask("lush"), "target": procgen.String("ph_desert_gravel")},
	)})
	if err := regionMaterialPaint(env, p, g, w, layers); err != nil {
		t.Fatal(err)
	}
	at := func(x, z float64) []float32 {
		k := 4 * (int(z-g.MinZ)*sz + int(x-g.MinX))
		return w[k : k+4]
	}
	// The trail is on the desert side: the role goes to the desert's gravel.
	if s := at(-5, 0); s[1] < 0.99 {
		t.Errorf("a gravel trail on desert ground: %v, want the desert's gravel", s)
	}
	// The lush disc names the desert gravel by its albedo, on savannah
	// ground.
	if s := at(6, 0); s[1] < 0.99 || s[3] > 0.01 {
		t.Errorf("a named layer on savannah ground: %v", s)
	}
}
