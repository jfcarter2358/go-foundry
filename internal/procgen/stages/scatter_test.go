package stages

// scatter_test.go — the placement stages end to end over a small terrain:
// cliff walls, a scatter avoiding them, vegetation volumes; their
// placements and volumes, the footprints file and its Load on a rerun that
// skips them, determinism across worker counts, the splice into a level
// (volumes through the level's strict schema), the previews (a dot layer
// per rule, instanced proxies) and that preview-only parameters never make
// the run stale.

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/place"
	"foundry/internal/world"
)

// placementDoc is smallTerrainDoc with rocks and every placement stage.
func placementDoc() *procgen.Doc {
	d := smallTerrainDoc()
	f, i, e, ref, v2 := procgen.Float, procgen.Int, procgen.Enum, procgen.StageRef, procgen.Vec2
	d.Stages = append(d.Stages,
		procgen.Stage{Kind: RocksKind, ID: "boulders", Params: procgen.Params{"preset": e("boulder"), "variants": i(2), "triangles": i(600)}},
		procgen.Stage{Kind: RocksKind, ID: "cliffs", Params: procgen.Params{"preset": e("cliff_chunk"), "variants": i(1), "triangles": i(3000)}},
		procgen.Stage{Kind: CliffWallKind, ID: "walls", Params: procgen.Params{"terrain": ref("ground"), "chunks": ref("cliffs"),
			"ledges": ref("boulders"), "min_length": f(6), "scale": v2(0.4, 1)}},
		procgen.Stage{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "avoid": ref("walls"),
			"rules": procgen.List(
				procgen.Params{"name": procgen.String("talus"), "rocks": ref("boulders"), "density": f(20), "cliff_base": f(1),
					"cliff_base_distance": f(10), "slope": v2(0, 60)},
				procgen.Params{"name": procgen.String("Small Rocks"), "rocks": ref("boulders"), "density": f(10),
					"scale": v2(0.15, 0.3), "satellites": f(4), "slope": v2(0, 60)},
			)}},
		procgen.Stage{Kind: VegetationKind, ID: "plants", Params: procgen.Params{"terrain": ref("ground"), "cell": f(8), "max_size": f(16),
			"rules": procgen.List(procgen.Params{"biome": procgen.String("desert"), "slope": v2(0, 30), "threshold": f(0.3)})}},
	)
	return d
}

func TestPlacementStagesRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	d := placementDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	pls := res.Placements()
	count := map[string]int{}
	for _, p := range pls {
		parts := strings.SplitN(p.ID, ".", 4)
		if len(parts) == 4 {
			count[parts[2]]++
		}
		if parts[2] == "scatter" || parts[2] == "walls" {
			if p.Scale == nil || p.Scale.X <= 0 || p.Scale.X != p.Scale.Y || p.Chunk != procgen.ChunkAuto || !strings.HasSuffix(p.Model, ".fnm") {
				t.Fatalf("placement %+v", p)
			}
			for _, a := range []float32{p.Rotation.X, p.Rotation.Y, p.Rotation.Z, p.Position.X, p.Position.Y, p.Position.Z} {
				if math.IsNaN(float64(a)) {
					t.Fatalf("NaN in %+v", p)
				}
			}
		}
	}
	if count["walls"] < 4 || count["scatter"] < 40 {
		t.Fatalf("placements by stage: %v", count)
	}
	sawRule := map[string]bool{}
	for _, p := range pls {
		if strings.HasPrefix(p.ID, "gen.t.scatter.") {
			sawRule[strings.SplitN(strings.TrimPrefix(p.ID, "gen.t.scatter."), "_0", 2)[0]] = true
		}
	}
	if !sawRule["talus"] || !sawRule["small_rocks"] {
		t.Errorf("rules seen in the IDs: %v", sawRule)
	}
	vols := res.Volumes()
	if len(vols) == 0 {
		t.Fatal("no vegetation volumes")
	}
	// Into a level: the volumes decode through the level's strict schema.
	raw := &world.RawLevel{}
	st, err := genlevel.SpliceResult(raw, res, genlevel.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st.AddedVolumes != len(vols) || len(raw.Volumes) != len(vols) {
		t.Fatalf("spliced %+v", st)
	}
	for _, v := range raw.Volumes {
		if v.Scatter == nil || v.Scatter.Kind != "biome" || v.Scatter.Biome == nil || v.Scatter.Biome.Preset != "desert" ||
			v.Scatter.Biome.Amounts["rock"] != 0 || v.Half.Y <= 0 {
			t.Fatalf("volume %+v", v)
		}
	}
	// The scatter's footprints keep off the walls' bottom tier.
	walls := res.Stage("walls").Artifact.(*FootprintsArtifact)
	sc := res.Stage("scatter").Artifact.(*FootprintsArtifact)
	if len(walls.Discs) == 0 || len(sc.Discs) == 0 {
		t.Fatalf("footprints: walls %d, scatter %d", len(walls.Discs), len(sc.Discs))
	}
	for _, a := range sc.Discs {
		for _, b := range walls.Discs {
			if math.Hypot(a.X-b.X, a.Z-b.Z) < a.R+b.R-1e-6 {
				t.Fatalf("a rock at (%.1f, %.1f) r %.2f inside a wall chunk's footprint (%.1f, %.1f) r %.2f", a.X, a.Z, a.R, b.X, b.Z, b.R)
			}
		}
	}
	// A rerun skips every stage, and a downstream change reads the
	// skipped stages back through their Load.
	d.Stages[len(d.Stages)-2].Params["margin"] = procgen.Float(3)
	again, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Stage("walls").Skipped || again.Stage("scatter").Skipped {
		t.Fatalf("rerun: walls skipped %v, scatter skipped %v", again.Stage("walls").Skipped, again.Stage("scatter").Skipped)
	}
	if n := len(again.Placements()); n == 0 {
		t.Fatal("the rerun placed nothing")
	}
}

func TestPlacementStagesAreDeterministic(t *testing.T) {
	run := func(workers int) ([]procgen.Placement, []procgen.VolumeEntry) {
		dir := t.TempDir()
		res, err := procgen.Run(context.Background(), placementDoc(), procgen.Options{OutDir: dir, Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		pls := res.Placements()
		for k := range pls {
			pls[k].Model = strings.TrimPrefix(pls[k].Model, dir)
		}
		return pls, res.Volumes()
	}
	a, av := run(1)
	b, bv := run(7)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("1 worker placed %d, 7 workers %d (or the same count differently)", len(a), len(b))
	}
	if !reflect.DeepEqual(av, bv) {
		t.Fatal("the vegetation volumes differ with the worker count")
	}
}

func TestPlacementPreviews(t *testing.T) {
	d := placementDoc()
	cache := procgen.NewPreviewCache(0)
	p, err := procgen.Preview(context.Background(), d, "scatter", cache, procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Points) != 2 || len(p.Points[0].Points) == 0 || len(p.Points[0].Radii) != len(p.Points[0].Points) {
		t.Fatalf("scatter preview points: %d layers", len(p.Points))
	}
	if p.Points[0].Color == p.Points[1].Color {
		t.Error("two rules' dots share a colour")
	}
	inst := 0
	for _, m := range p.Meshes {
		if len(m.Instances) > 0 {
			inst++
			if m.Turntable || len(m.Indices) == 0 || len(m.Indices)/3 > 300 {
				t.Errorf("proxy %s: %d triangles", m.Name, len(m.Indices)/3)
			}
			if _, _, ok := m.InstanceBounds(); !ok {
				t.Error("no instance bounds")
			}
		}
	}
	if inst == 0 || len(p.Images) == 0 {
		t.Fatalf("scatter preview: %d instanced meshes, %d images", inst, len(p.Images))
	}
	w, err := procgen.Preview(context.Background(), d, "walls", cache, procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Lines) == 0 || len(w.Points) == 0 || len(w.Points[0].Points) == 0 {
		t.Fatalf("walls preview: %d lines, points %v", len(w.Lines), w.Points)
	}
	v, err := procgen.Preview(context.Background(), d, "plants", cache, procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Lines) == 0 {
		t.Fatal("vegetation preview: no rectangles")
	}
	// The preview-only parameters do not make the run stale.
	dir := t.TempDir()
	if _, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir}); err != nil {
		t.Fatal(err)
	}
	d.Stages[len(d.Stages)-2].Params["preview_max"] = procgen.Int(50)
	d.Stages[len(d.Stages)-2].Params["preview_triangles"] = procgen.Int(50)
	stale, err := procgen.StaleStages(d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("preview-only changes made %v stale", stale)
	}
}

func TestPlacementValidation(t *testing.T) {
	d := &procgen.Doc{Name: "v", Stages: []procgen.Stage{
		{Kind: ScatterKind, ID: "s", Params: procgen.Params{"rules": procgen.List(procgen.Params{"slope": procgen.Vec2(40, 10)})}},
		{Kind: CliffWallKind, ID: "w", Params: procgen.Params{"source": procgen.Enum("splines")}},
		{Kind: VegetationKind, ID: "p", Params: procgen.Params{"rules": procgen.List(procgen.Params{"amounts": procgen.String("rock=x")})}},
	}}
	msgs := ""
	for _, p := range d.Validate() {
		msgs += p.String() + "\n"
	}
	for _, want := range []string{"terrain stage", "names no rocks stage", "runs backwards", "cliff chunks", "Add a path", "rock=x"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("no problem mentioning %q in:\n%s", want, msgs)
		}
	}
	if amounts, err := parseAmounts("rock=0, grass:0.5 deadwood=2"); err != nil || amounts["grass"] != 0.5 || len(amounts) != 3 {
		t.Errorf("parseAmounts: %v %v", amounts, err)
	}
}

// A rule's max_triangles keeps the light variants (a scan set mixes
// hundred-thousand and million-triangle rocks), or the lightest when none
// is that light; and both placement stages take any stage whose product is
// a rocks manifest.
func TestScatterPicksLightVariants(t *testing.T) {
	vs := []place.Variant{{Name: "a", Triangles: 2400000}, {Name: "b", Triangles: 120000}, {Name: "c", Triangles: 300000}}
	if got := lightVariants(vs, 0); len(got) != 3 {
		t.Errorf("no cap kept %d", len(got))
	}
	if got := lightVariants(vs, 400000); len(got) != 2 || got[0].Name != "b" || got[1].Name != "c" {
		t.Errorf("a 400k cap kept %v", got)
	}
	if got := lightVariants(vs, 1000); len(got) != 1 || got[0].Name != "b" {
		t.Errorf("a cap nothing meets kept %v, want the lightest", got)
	}
	for _, k := range []string{ScatterKind, CliffWallKind} {
		kind := procgen.KindByName(k)
		for _, d := range kind.Params {
			refs := d.StageKinds
			if d.Type == procgen.ParamList {
				for _, f := range d.Fields {
					if f.Name == "rocks" {
						refs = f.StageKinds
					}
				}
			}
			if d.Name == "rocks" || d.Name == "chunks" || d.Name == "ledges" || (d.Type == procgen.ParamList && d.Name == "rules") {
				want := RockSources
				if k == ScatterKind {
					want = ScatterSources // and the plant scans (docs/canyon/PHASE_4.md)
				}
				if !reflect.DeepEqual(refs, want) {
					t.Errorf("%s.%s takes %v, want every rock source %v", k, d.Name, refs, want)
				}
			}
		}
	}
}
