package stages

// polyhaven_test.go — the Poly Haven stages end to end against the fake
// Poly Haven (polyhaven/phtest, never the live API): a polyhaven_rocks stage
// feeding cliff_wall and a scatter rule, a polyhaven_plants stage feeding a
// scatter rule, a polyhaven_textures stage; the manifests (variants point at
// the shared import under models/polyhaven/), the placements naming those
// meshes, a rerun downloading nothing, the query, offline mode, validation
// and the previews.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/polyhaven"
	"foundry/internal/polyhaven/phtest"
	"foundry/internal/procgen"
)

func withFakePolyHaven(t *testing.T) (*phtest.Server, string) {
	srv := phtest.New(t)
	c := srv.Client(t)
	res := t.TempDir()
	oc, or := PolyHavenClient, PolyHavenResources
	PolyHavenClient = func() *polyhaven.Client { return c }
	PolyHavenResources = func() string { return res }
	t.Cleanup(func() { PolyHavenClient, PolyHavenResources = oc, or })
	return srv, res
}

func polyHavenDoc() *procgen.Doc {
	d := smallTerrainDoc()
	f, ref, v2, s := procgen.Float, procgen.StageRef, procgen.Vec2, procgen.String
	d.Stages = append(d.Stages,
		procgen.Stage{Kind: PolyHavenRocksKind, ID: "scans", Params: procgen.Params{
			"assets": procgen.List(procgen.Params{"id": s("t1")}), "lite_triangles": procgen.Int(100)}},
		procgen.Stage{Kind: PolyHavenPlantsKind, ID: "flora", Params: procgen.Params{
			"assets": procgen.List(), "category": s("plants")}},
		procgen.Stage{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{
			"layers": procgen.List(procgen.Params{"name": s("sand"), "id": s("sand")})}},
		procgen.Stage{Kind: CliffWallKind, ID: "walls", Params: procgen.Params{"terrain": ref("ground"), "chunks": ref("scans"),
			"min_length": f(6), "scale": v2(0.4, 1)}},
		procgen.Stage{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "avoid": ref("walls"),
			"rules": procgen.List(
				procgen.Params{"name": s("boulders"), "rocks": ref("scans"), "max_triangles": procgen.Int(200), "density": f(2),
					"scale": v2(0.1, 0.2), "slope": v2(0, 60)},
				procgen.Params{"name": s("trees"), "rocks": ref("flora"), "density": f(3), "align": f(0), "tilt": f(0),
					"spacing": f(1.5), "scale": v2(0.15, 0.25), "slope": v2(0, 40)},
			)}},
	)
	return d
}

func TestPolyHavenStagesFeedThePlacementStages(t *testing.T) {
	srv, res := withFakePolyHaven(t)
	dir := t.TempDir()
	d := polyHavenDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	scans := r.Stage("scans").Artifact.(*RocksArtifact)
	if len(scans.Manifest.Variants) != 2 {
		t.Fatalf("scan variants %d, want the scan and its lite copy", len(scans.Manifest.Variants))
	}
	full := &scans.Manifest.Variants[0]
	if full.Back == nil || !strings.HasPrefix(scans.ModelPath(full), "models/polyhaven/t1/") {
		t.Fatalf("variant %+v at %s", full, scans.ModelPath(full))
	}
	if _, err := os.Stat(filepath.Join(res, filepath.FromSlash(scans.ModelPath(full)))); err != nil {
		t.Fatal(err)
	}
	flora := r.Stage("flora").Artifact.(*RocksArtifact)
	if len(flora.Manifest.Variants) != 1 || flora.Manifest.Variants[0].Back != nil || flora.Manifest.Variants[0].PlaceCollision != "none" ||
		!strings.Contains(flora.Manifest.Variants[0].Model, "/p1/") {
		t.Fatalf("plants %+v", flora.Manifest.Variants)
	}
	tex := r.Stage("tex").Artifact.(*TextureSetManifest)
	if l := tex.Layer("sand"); l == nil || l.Albedo != "ph_sand" || l.Height != "ph_sand_height" || l.Scale != 3 {
		t.Fatalf("texture layer %+v", tex.Layers)
	}
	count := map[string]int{}
	for _, p := range r.Placements() {
		parts := strings.SplitN(p.ID, ".", 4)
		key := parts[2]
		if key == "ground" {
			continue
		}
		if key == "scatter" {
			key += "." + strings.SplitN(parts[3], "_", 2)[0]
		}
		count[key]++
		if !strings.HasPrefix(p.Model, "models/polyhaven/") {
			t.Fatalf("placement %s names %s", p.ID, p.Model)
		}
		if strings.HasPrefix(parts[3], "boulders") && !strings.Contains(p.Model, "_t100") {
			t.Fatalf("a scatter rule capped at 200 triangles placed %s", p.Model)
		}
		if strings.HasPrefix(parts[3], "trees") && (p.Collision != "none" || p.Rotation.X != 0 || p.Rotation.Z != 0) {
			t.Fatalf("a tree %+v is not upright without collision", p)
		}
	}
	if count["walls"] == 0 || count["scatter.boulders"] == 0 || count["scatter.trees"] == 0 {
		t.Fatalf("placements %v", count)
	}
	if _, err := os.Stat(filepath.Join(dir, "scans", CreditsFile)); err != nil {
		t.Fatal(err)
	}
	// Loaded back from the files, the manifests are the same.
	a, err := loadRocks(r.Stage("scans"))
	if err != nil || a.(*RocksArtifact).ModelPath(&a.(*RocksArtifact).Manifest.Variants[0]) != scans.ModelPath(full) {
		t.Fatalf("load %v", err)
	}
	// Forced again: nothing downloaded.
	hits := srv.Hits()
	if _, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir, Force: true}); err != nil {
		t.Fatal(err)
	}
	if srv.Hits() != hits {
		t.Fatalf("a forced rerun made %d requests", srv.Hits()-hits)
	}
	// Offline with a fresh cache fails cleanly.
	PolyHavenClient = func() *polyhaven.Client { return srv.Client(t) }
	d.Stages[1].Params["offline"] = procgen.Bool(true)
	if _, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: t.TempDir(), Only: []string{"scans"}}); err == nil ||
		!strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline with nothing cached: %v", err)
	}
}

func TestPolyHavenValidationAndPreview(t *testing.T) {
	_, _ = withFakePolyHaven(t)
	kind := procgen.KindByName(PolyHavenRocksKind)
	p := procgen.Resolve(kind.Params, procgen.Params{"assets": procgen.List(procgen.Params{"id": procgen.String("bad id")}),
		"back": procgen.String("sideways")})
	probs := kind.Validate(nil, nil, p)
	if len(probs) != 2 {
		t.Fatalf("problems %v", probs)
	}
	empty := procgen.Resolve(kind.Params, procgen.Params{"assets": procgen.List()})
	if probs := kind.Validate(nil, nil, empty); len(probs) != 1 {
		t.Fatalf("an empty stage: %v", probs)
	}
	for _, k := range []string{PolyHavenRocksKind, PolyHavenPlantsKind, PolyHavenTexturesKind} {
		found := false
		for _, d := range procgen.KindByName(k).Params {
			if d.AssetList != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no asset list for the editor", k)
		}
	}
	// The preview reads only what is imported: nothing yet, no request.
	d := polyHavenDoc()
	pd, err := procgen.Preview(context.Background(), d, "scans", procgen.NewPreviewCache(8), procgen.PreviewOptions{})
	if err != nil || len(pd.Meshes) != 0 || !strings.Contains(strings.Join(pd.Notes, "\n"), "not imported") {
		t.Fatalf("preview %+v %v", pd, err)
	}
	// Imported, it turns the cooked mesh.
	if _, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: t.TempDir(), Only: []string{"scans"}}); err != nil {
		t.Fatal(err)
	}
	pd, err = procgen.Preview(context.Background(), d, "scans", procgen.NewPreviewCache(8), procgen.PreviewOptions{})
	if err != nil || len(pd.Meshes) != 1 || !pd.Meshes[0].Turntable || len(pd.Meshes[0].Indices) == 0 {
		t.Fatalf("preview after import %+v %v", pd, err)
	}
	if a, ok := pd.Artifact.(*RocksArtifact); !ok || len(a.Manifest.Variants) == 0 {
		t.Fatalf("preview artifact %T", pd.Artifact)
	}
}
