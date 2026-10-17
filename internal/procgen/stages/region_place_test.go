package stages

// region_place_test.go — phase 4 of the environment regions
// (docs/regions/PHASE_4.md): the placement stages expanded per region.
//
//   - GATE: two regions, each preset with Poly Haven scans and plants (the
//     fake library), generated rocks, a scatter over the region material
//     and vegetation volumes. Editing region B's preset placement and
//     re-running — a plain run, a forced run and a run of region B alone —
//     leaves every file of region A's expanded stages byte-identical, and a
//     scoped splice leaves A's placements in the level as they were.
//   - Density: one scatter rule of the same density in both regions, over
//     flat ground: across the band each region's share of the rocks follows
//     its weight, and their sum stays level — the rocks thin, they do not
//     stop at a line.
//   - The rocks cache: two regions of one preset make its rocks once.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image/color"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/world"
)

// placePreset writes a preset that grows things: textures, a flat terrain
// and its ground material (for the region material), Poly Haven scans and
// plants and generated pebbles (all uncooked: .glb bytes are stable), a
// scatter over the ground material and a vegetation rule. density is the
// pebbles' scatter density.
func placePreset(t *testing.T, dir, name string, base, density float64) string {
	t.Helper()
	f, i, s, ref, v2, b := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2, procgen.Bool
	var items []procgen.Params
	keys := procgen.Params{"terrain": ref("ground"), "textures": ref("tex"), "rock_scale": f(0)}
	for k, key := range splatLayerKeys {
		n := name + "_" + key
		items = append(items, procgen.Params{"name": s(n), "id": s("sand"), "scale": f(float64(k + 1))})
		keys[key] = s(n)
	}
	d := &procgen.Doc{Name: name, Seed: 5, Stages: []procgen.Stage{
		{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{"layers": procgen.List(items...)}},
		{Kind: TerrainKind, ID: "ground", Params: procgen.Params{"base_height": f(base), "relief": f(1.5), "mesa_height": f(0),
			"mesa_tiers": i(0), "terrace": f(0), "droplets": f(0), "talus_iterations": i(4), "canyons": procgen.List()}},
		{Kind: TerrainMaterialKind, ID: "ground_mat", Params: keys},
		{Kind: PolyHavenRocksKind, ID: "scans", Params: procgen.Params{"assets": procgen.List(procgen.Params{"id": s("t1")}),
			"lite_triangles": i(100), "cook": b(false)}},
		{Kind: PolyHavenPlantsKind, ID: "flora", Params: procgen.Params{"assets": procgen.List(procgen.Params{"id": s("p1")}),
			"cook": b(false)}},
		{Kind: RocksKind, ID: "pebbles", Params: procgen.Params{"preset": procgen.Enum("pebble"), "variants": i(2),
			"triangles": i(200), "cook": b(false)}},
		{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
			"rules": procgen.List(
				procgen.Params{"name": s("stones"), "rocks": ref("pebbles"), "density": f(density), "scale": v2(0.8, 1.2),
					"slope": v2(0, 60), "cluster": f(0), "match_ground": f(0.5), "ground_band": f(0.3)},
				procgen.Params{"name": s("boulders"), "rocks": ref("scans"), "density": f(0.3), "scale": v2(0.1, 0.2),
					"slope": v2(0, 60), "cluster": f(0)},
				procgen.Params{"name": s("trees"), "rocks": ref("flora"), "density": f(0.4), "scale": v2(0.15, 0.25),
					"slope": v2(0, 60), "align": f(0), "tilt": f(0), "spacing": f(1.5), "cluster": f(0)},
			)}},
		{Kind: VegetationKind, ID: "plants", Params: procgen.Params{"terrain": ref("ground"), "cell": f(8), "rules": procgen.List(
			procgen.Params{"name": s("scrub"), "biome": s("desert"), "threshold": f(0.3), "cluster": f(0), "slope": v2(0, 60)})}},
	}}
	p := filepath.Join(dir, name+procgen.Ext)
	if err := procgen.WriteDoc(p, d); err != nil {
		t.Fatal(err)
	}
	return p
}

// setPresetDensity rewrites a placePreset's pebble density.
func setPresetDensity(t *testing.T, path string, density float64) {
	t.Helper()
	d, err := procgen.ReadDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	st := d.StageByID("scatter")
	rules := st.Params["rules"]
	rules.Items[0]["density"] = procgen.Float(density)
	st.Params["rules"] = rules
	if err := procgen.WriteDoc(path, d); err != nil {
		t.Fatal(err)
	}
}

// placeWorld is a level doc of region "hills" (x −160..0) and "plain"
// (x 0..128), z ±96, an 80 m band, region_terrain and region_material.
func placeWorld(t *testing.T, a, b string) *procgen.Doc {
	t.Helper()
	f := procgen.Float
	d := worldDoc("placeworld", []procgen.Params{
		regionItem("hills", DesertCanyonEnvironment, a, -160, -96, 0, 96, 80),
		regionItem("plain", SavannahEnvironment, b, 0, -96, 128, 96, 80),
	}, procgen.Params{"resolution": f(1), "tile_size": f(64), "warp_amount": f(10)}, nil)
	d.Stages = append(d.Stages, procgen.Stage{Kind: RegionMaterialKind, ID: "ground_mat",
		Params: procgen.Params{"terrain": procgen.StageRef("ground")}})
	return d
}

// regionFiles is every file under the doc's stage folders of one region's
// expanded stages (r_<key>_*), by path relative to the output folder.
func regionFiles(t *testing.T, out, key string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	ents, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), procgen.RegionStageID(key, "")) {
			continue
		}
		root := filepath.Join(out, e.Name())
		err := filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(out, p)
			files[rel] = data
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func sameFiles(t *testing.T, what string, before, after map[string][]byte) {
	t.Helper()
	if len(before) == 0 {
		t.Fatalf("%s: no files to compare", what)
	}
	var names []string
	for n := range before {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		got, ok := after[n]
		switch {
		case !ok:
			t.Errorf("%s: %s is gone", what, n)
		case string(got) != string(before[n]):
			t.Errorf("%s: %s changed", what, n)
		}
	}
	for n := range after {
		if _, ok := before[n]; !ok {
			t.Errorf("%s: %s is new", what, n)
		}
	}
}

func ranAny(res *procgen.Result, prefix string) []string {
	var out []string
	for _, id := range res.Ran {
		if strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	return out
}

// sceneryOf is the level's scenery under prefix, by ID.
func sceneryOf(raw *world.RawLevel, prefix string) map[string]world.RawScenery {
	out := map[string]world.RawScenery{}
	for _, s := range raw.Scenery {
		if strings.HasPrefix(s.ID, prefix) {
			out[s.ID] = s
		}
	}
	return out
}

// GATE.
func TestGateRerunningOneRegionLeavesTheOtherAlone(t *testing.T) {
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	dir := t.TempDir()
	a := placePreset(t, dir, "hills", 0, 4)
	b := placePreset(t, dir, "plain", 0, 4)
	d := placeWorld(t, a, b)
	out := filepath.Join(res, "generated", d.Name)
	run := func(opts procgen.Options) *procgen.Result {
		t.Helper()
		opts.OutDir, opts.Workers = out, 8
		r, err := procgen.Run(context.Background(), d, opts)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r1 := run(procgen.Options{})
	for _, want := range []string{"r_hills_scatter", "r_hills_plants", "r_hills_pebbles", "r_hills_scans", "r_hills_flora",
		"r_plain_scatter", "r_plain_plants"} {
		if r1.Stage(want) == nil {
			t.Fatalf("no expanded stage %s; ran %v", want, r1.Ran)
		}
	}
	// The textures stage is on demand, and nothing of the region reads it.
	if r1.Stage("r_hills_tex") != nil {
		t.Errorf("an unread asset stage was expanded")
	}
	// Every hills placement carries its region's prefix, and the level
	// takes them all.
	pre := procgen.RegionStagePrefix(d.Name, "hills")
	nHills := 0
	for _, p := range r1.Placements() {
		if strings.HasPrefix(p.ID, pre) {
			nHills++
		}
	}
	if nHills < 50 {
		t.Fatalf("only %d placements under %s", nHills, pre)
	}
	raw := world.RawLevel{}
	if _, err := genlevel.SpliceResult(&raw, r1, genlevel.Options{}); err != nil {
		t.Fatal(err)
	}
	hillsLevel := sceneryOf(&raw, pre)
	before := regionFiles(t, out, "hills")
	plainBefore := string(regionFiles(t, out, "plain")[filepath.Join("r_plain_scatter", procgen.PlacementsFileName)])

	// 1. Edit plain's placement, plain run.
	setPresetDensity(t, b, 7)
	r2 := run(procgen.Options{})
	if got := ranAny(r2, "r_hills_"); len(got) > 0 {
		t.Errorf("editing plain's preset reran hills' stages %v", got)
	}
	if r2.Stage("r_plain_scatter").Skipped {
		t.Fatalf("plain's scatter did not rerun")
	}
	if string(regionFiles(t, out, "plain")[filepath.Join("r_plain_scatter", procgen.PlacementsFileName)]) == plainBefore {
		t.Errorf("plain's placements did not change with its density")
	}
	sameFiles(t, "plain run", before, regionFiles(t, out, "hills"))

	// 2. A forced run regenerates hills' stages: the same bytes.
	r3 := run(procgen.Options{Force: true})
	if len(ranAny(r3, "r_hills_")) == 0 {
		t.Fatalf("the forced run did not rerun hills")
	}
	sameFiles(t, "forced run", before, regionFiles(t, out, "hills"))

	// 3. A run of region plain alone (foundry gen run -region plain), and
	// its scoped splice.
	setPresetDensity(t, b, 2)
	only, err := procgen.RegionStages(d, "plain")
	if err != nil || len(only) == 0 {
		t.Fatalf("RegionStages: %v %v", only, err)
	}
	for _, id := range only {
		if !strings.HasPrefix(id, "r_plain_") {
			t.Errorf("RegionStages(plain) lists %s", id)
		}
	}
	r4 := run(procgen.Options{Only: only})
	if got := ranAny(r4, "r_hills_"); len(got) > 0 {
		t.Errorf("a run of plain ran %v", got)
	}
	sameFiles(t, "region run", before, regionFiles(t, out, "hills"))
	scope, err := genlevel.RegionScope(d, r4, []string{"plain"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := genlevel.SpliceResult(&raw, r4, genlevel.Options{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if st.AddedScenery == 0 || st.RemovedScenery == 0 {
		t.Errorf("the scoped splice changed nothing: %s", st)
	}
	after := sceneryOf(&raw, pre)
	if len(after) != len(hillsLevel) {
		t.Fatalf("the scoped splice left %d hills placements of %d", len(after), len(hillsLevel))
	}
	for id, s := range hillsLevel {
		if a, ok := after[id]; !ok || a.Model != s.Model || a.Position != s.Position || a.Material != s.Material {
			t.Errorf("hills placement %s changed in the level", id)
		}
	}
	plainN := 0
	for _, p := range r4.Placements() {
		if strings.HasPrefix(p.ID, procgen.RegionStagePrefix(d.Name, "plain")) {
			plainN++
		}
	}
	if got := len(sceneryOf(&raw, procgen.RegionStagePrefix(d.Name, "plain"))); got != plainN {
		t.Errorf("the level holds %d plain placements, the run made %d", got, plainN)
	}
}

// Density: each region's rocks follow its weight across the band, and the
// two together stay level.
func TestRegionScatterThinsAcrossTheBand(t *testing.T) {
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	dir := t.TempDir()
	a := placePreset(t, dir, "hills", 0, 6)
	b := placePreset(t, dir, "plain", 0, 6)
	d := placeWorld(t, a, b)
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: filepath.Join(res, "generated", d.Name), Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	// Bins of 10 m in x from −150 to 120 (the 2 m edge margin aside), over
	// the footprints' rows only (|z| < 90): the union's square padding
	// beyond them is a gap the regions share by distance (place/region.go).
	const lo, w = -150.0, 10.0
	n := 27
	ca, cb := make([]float64, n), make([]float64, n)
	for _, p := range r.Placements() {
		k := int(math.Floor((float64(p.Position.X) - lo) / w))
		if k < 0 || k >= n || math.Abs(float64(p.Position.Z)) >= 90 || !strings.Contains(p.ID, "_scatter.stones_") {
			continue
		}
		switch {
		case strings.HasPrefix(p.ID, procgen.RegionStagePrefix(d.Name, "hills")):
			ca[k]++
		case strings.HasPrefix(p.ID, procgen.RegionStagePrefix(d.Name, "plain")):
			cb[k]++
		}
	}
	mean := 0.0
	for k := 0; k < n; k++ {
		mean += ca[k] + cb[k]
	}
	mean /= float64(n)
	share := func(x float64) float64 {
		k := int((x - lo) / w)
		return ca[k] / math.Max(ca[k]+cb[k], 1)
	}
	for k := 0; k < n; k++ {
		x := lo + (float64(k)+0.5)*w
		tot := ca[k] + cb[k]
		t.Logf("x %6.0f  hills %4.0f  plain %4.0f  hills share %.2f", x, ca[k], cb[k], ca[k]/math.Max(tot, 1))
		if tot < 0.7*mean || tot > 1.3*mean {
			t.Errorf("x %.0f: %0.f rocks in the bin, the mean is %.0f: the band is not level", x, tot, mean)
		}
	}
	if s := share(-125); s < 0.97 {
		t.Errorf("deep in hills its share is %.2f", s)
	}
	if s := share(105); s > 0.03 {
		t.Errorf("deep in plain hills' share is %.2f", s)
	}
	if s := share(-5); s < 0.3 || s > 0.8 {
		t.Errorf("at the footprints' edge hills' share is %.2f, want about a half", s)
	}
	if !(share(-35) > share(-5) && share(-5) > share(25)) {
		t.Errorf("hills' share does not fall across the band: %.2f, %.2f, %.2f", share(-35), share(-5), share(25))
	}
	// Hills places nothing where it has no share: past the half band and
	// the boundary's meander (x 0 + 40 + 10).
	for k := 0; k < n; k++ {
		if x := lo + float64(k)*w; x >= 55 && ca[k] > 0 {
			t.Errorf("hills placed %0.f rocks at x %.0f, past its band", ca[k], x)
		}
	}
}

// Two regions of one preset make its generated rocks once, into the shared
// cache, and both manifests point there.
func TestRegionRocksAreMadeOnce(t *testing.T) {
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	dir := t.TempDir()
	a := placePreset(t, dir, "same", 0, 2)
	d := placeWorld(t, a, a)
	out := filepath.Join(res, "generated", d.Name)
	var logs []string
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: out, Workers: 8,
		Log: func(l string) { logs = append(logs, l) }})
	if err != nil {
		t.Fatal(err)
	}
	ha, hb := r.Stage("r_hills_pebbles"), r.Stage("r_plain_pebbles")
	if ha == nil || hb == nil {
		t.Fatalf("no expanded rocks stages")
	}
	ma, mb := ha.Artifact.(*RocksArtifact), hb.Artifact.(*RocksArtifact)
	if len(ma.Manifest.Variants) != 2 || len(mb.Manifest.Variants) != 2 {
		t.Fatalf("variants %d and %d", len(ma.Manifest.Variants), len(mb.Manifest.Variants))
	}
	for k := range ma.Manifest.Variants {
		pa, pb := ma.ModelPath(&ma.Manifest.Variants[k]), mb.ModelPath(&mb.Manifest.Variants[k])
		if pa != pb || !strings.Contains(pa, "/_shared/") {
			t.Errorf("variant %d: %s and %s, want one shared file", k, pa, pb)
		}
		if _, err := os.Stat(filepath.Join(res, filepath.FromSlash(pa))); err != nil {
			t.Errorf("the shared file is missing: %v", err)
		}
	}
	made, reused := 0, 0
	for _, l := range logs {
		if strings.Contains(l, "into the shared cache") {
			made++
		}
		if strings.Contains(l, "from the shared cache") {
			reused++
		}
	}
	if made != 1 || reused != 1 {
		t.Errorf("made %d, reused %d (want 1 and 1)", made, reused)
	}
	// The plants: the second region's import is the first's.
	reusedImport := false
	for _, l := range logs {
		reusedImport = reusedImport || strings.Contains(l, "the import another region's stage made")
	}
	if !reusedImport {
		t.Errorf("the second region imported its scans again")
	}
}

// A region's wall_sheet over an eight-layer region material wears its own
// environment's rock layer: pure rock in the right channel of the right
// weights map. (rockLayerIndex on its own, then end to end.)
func TestRegionWallsWearTheirOwnRock(t *testing.T) {
	g := &TerrainMaterialArtifact{Weights2: "w2", Layers: []RegionMaterialLayer{
		{Role: "sand", Environments: []string{"a", "b"}}, {Role: "cracked", Environments: []string{"a"}},
		{Role: "gravel", Environments: []string{"a"}}, {Role: "rock", Environments: []string{"a"}},
		{Role: "cracked", Environments: []string{"b"}}, {Role: "gravel", Environments: []string{"b"}},
		{Role: "rock", Environments: []string{"b"}},
	}}
	for env, want := range map[string]int{"a": 3, "b": 6} {
		e := &procgen.Env{Region: &procgen.RegionContext{Environment: env}}
		if got := rockLayerIndex(e, g); got != want {
			t.Errorf("%s: rock layer %d, want %d", env, got, want)
		}
	}
	if got := rockLayerIndex(&procgen.Env{}, &TerrainMaterialArtifact{}); got != 3 {
		t.Errorf("a four-layer ground's rock is layer %d", got)
	}

	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	dir := t.TempDir()
	f, ref := procgen.Float, procgen.StageRef
	walls := procgen.Params{"rock_slope": procgen.Vec2(20, 30), "rock_scale": f(0)}
	a := groundPreset(t, dir, "hillsw", procgen.Params{"relief": f(20), "mesa_height": f(0), "canyons": procgen.List()}, [4]float64{1, 2, 3, 4}, walls)
	b := groundPreset(t, dir, "plainw", procgen.Params{"relief": f(4), "mesa_height": f(0), "canyons": procgen.List()}, [4]float64{5, 6, 7, 8}, walls)
	for _, p := range []string{a, b} {
		d, err := procgen.ReadDoc(p)
		if err != nil {
			t.Fatal(err)
		}
		d.Stages = append(d.Stages, procgen.Stage{Kind: WallSheetKind, ID: "rockface",
			Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"), "cell": f(1)}})
		if err := procgen.WriteDoc(p, d); err != nil {
			t.Fatal(err)
		}
	}
	d := placeWorld(t, a, b)
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: filepath.Join(res, "generated", d.Name), Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	gm := r.Stage("ground_mat").Artifact.(*TerrainMaterialArtifact)
	if gm.Weights2 == "" || len(gm.Layers) != 8 {
		t.Fatalf("the region material has %d layers (weights2 %q), want eight", len(gm.Layers), gm.Weights2)
	}
	for key, env := range map[string]string{"hills": DesertCanyonEnvironment, "plain": SavannahEnvironment} {
		st := r.Stage(procgen.RegionStageID(key, "rockface"))
		if st == nil {
			t.Fatalf("no %s rockface", key)
		}
		want := rockLayerIndex(&procgen.Env{Region: &procgen.RegionContext{Environment: env}}, gm)
		chans := func(file string) [4]uint8 {
			img := readPNG(t, filepath.Join(res, filepath.FromSlash(st.RelDir), file))
			c := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
			return [4]uint8{c.R, c.G, c.B, c.A}
		}
		w1, w2 := chans("rock_weights.png"), chans("rock_weights2.png")
		var got [8]uint8
		copy(got[:4], w1[:])
		copy(got[4:], w2[:])
		for k := range got {
			if (k == want) != (got[k] == 255) || (k != want && got[k] != 0) {
				t.Errorf("%s: rock weights %v, want pure layer %d", key, got, want+1)
				break
			}
		}
		m := readMaterialDoc(genMaterialName(&procgen.RunContext{Env: procgen.Env{Doc: d, Stage: &procgen.Stage{ID: st.ID}}}, ""))
		if m == nil {
			t.Fatalf("%s: no material", key)
		}
		if e := m.Find("splat"); e == nil || !strings.HasSuffix(e.Params["weights2"].Str, "rock_weights2") {
			t.Errorf("%s: the walls' splat does not read rock_weights2", key)
		}
	}
}

// classicPlacementsGolden pins a classic (no region) document's placement
// outputs: placePreset run as an ordinary generator. Recorded with phase 4
// — AFTER the per-region paths were added beside the classic ones, so it
// pins the classic output from here on; that the classic output did not
// change in phase 4 rests on the classic paths being the old code (every
// region branch is behind a nil check) and on the existing placement tests.
// FOUNDRY_PLACE_GOLDEN_PRINT=1 prints the current hashes.
var classicPlacementsGolden = map[string]string{
	"scatter/placements.yaml": "b69c6dee0b5970b4a8f8af9563d3877184304e47a7a25de89999a68a27eedc17",
	"plants/placements.yaml":  "b856fc2078068719b8bb5a700c36cb4bf85b412371f8f2089b6c5f86bc069cdf",
	// (rocks.yaml records each variant's timings: not byte-stable.)
}

func TestClassicPlacementsPinned(t *testing.T) {
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	p := placePreset(t, t.TempDir(), "classic", 0, 4)
	d, err := procgen.ReadDoc(p)
	if err != nil {
		t.Fatal(err)
	}
	g := d.StageByID("ground")
	g.Params["size"], g.Params["resolution"], g.Params["tile_size"], g.Params["cook"] =
		procgen.Float(128), procgen.Float(1), procgen.Float(64), procgen.Bool(false)
	out := filepath.Join(res, "generated", d.Name)
	if _, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: out, Workers: 8}); err != nil {
		t.Fatal(err)
	}
	for name, want := range classicPlacementsGolden {
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if os.Getenv("FOUNDRY_PLACE_GOLDEN_PRINT") != "" {
			t.Logf("%q: %q,", name, got)
			continue
		}
		if got != want {
			t.Errorf("%s hashes %s, pinned %s: a classic placement's output changed", name, got, want)
		}
	}
}
