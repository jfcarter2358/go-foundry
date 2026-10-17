package stages

// rocks_test.go — the rocks stage end to end: cooked variants that carry
// their colours, collision chosen by size (hulls for a boulder, the mesh
// alone for a cliff chunk, whose cook skips the decomposition), the manifest
// and its Load, the gallery's placements, determinism across worker counts,
// the preview (one coloured turntable mesh, its notes), and that the
// preview-only parameters never make a run stale.

import (
	"bytes"
	"context"
	"math"
	"os"
	"strings"
	"testing"

	"foundry/internal/meshcollide"
	"foundry/internal/nanite"
	"foundry/internal/procgen"
	"foundry/internal/procgen/rocks"
)

func rocksDoc(preset string, variants, tris int) *procgen.Doc {
	return &procgen.Doc{Name: "r", Seed: 11, Stages: []procgen.Stage{{Kind: RocksKind, ID: "stones", Params: procgen.Params{
		"preset": procgen.Enum(preset), "variants": procgen.Int(variants), "triangles": procgen.Int(tris),
	}}}}
}

func TestRocksRunsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	d := rocksDoc("boulder", 2, 3000)
	d.Stages[0].Params["gallery"] = procgen.Bool(true)
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stage("stones")
	man, err := rocks.LoadManifest(st.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if man.Version != rocks.ManifestVersion || man.Preset != "boulder" || len(man.Variants) != 2 || man.Frame == "" {
		t.Fatalf("manifest %+v", man)
	}
	for i, v := range man.Variants {
		if v.Name != variantName("boulder", i) || v.File != v.Name+".fnm" {
			t.Errorf("variant %d named %s, file %s", i, v.Name, v.File)
		}
		if v.Collision != "hulls" || v.PlaceCollision != "hulls" {
			t.Errorf("%s: collision %s / %s, want hulls for a 1.6 m boulder", v.Name, v.Collision, v.PlaceCollision)
		}
		if v.Triangles < 2000 || v.Triangles > 3600 || v.Height <= 0 || v.FootprintRadius <= 0 {
			t.Errorf("%s: %+v", v.Name, v)
		}
		m, err := nanite.Read(st.File(v.File))
		if err != nil {
			t.Fatal(err)
		}
		if !m.HasColors || m.Version() != nanite.FileVersion || len(m.FallbackColors) == 0 {
			t.Errorf("%s: cooked without its colours (version %d)", v.Name, m.Version())
		}
		if m.Collision == nil || m.Collision.Settings.Mode != meshcollide.ModeHulls || len(m.Collision.Hulls) == 0 {
			t.Errorf("%s: no hulls cooked", v.Name)
		}
		if !m.ClosedSolid {
			t.Errorf("%s: not a closed solid", v.Name)
		}
		// Every LOD error a number: a NaN is never under the threshold, and
		// the mesh would never draw.
		for ci := range m.Clusters {
			if c := &m.Clusters[ci]; math.IsNaN(float64(c.Error)) || math.IsNaN(float64(c.ParentError)) {
				t.Fatalf("%s: cluster %d's error is NaN", v.Name, ci)
			}
		}
	}
	// The gallery: both variants in a row along +X, colliding as hulls.
	pl := res.Placements()
	if len(pl) != 2 || pl[0].ID != "gen.r.stones.boulder_00" || pl[1].Position.X <= pl[0].Position.X ||
		pl[0].Collision != "hulls" || !strings.HasSuffix(pl[0].Model, "/boulder_00.fnm") {
		t.Fatalf("gallery placements %+v", pl)
	}
	// The artifact, and Load.
	art := st.Artifact.(*RocksArtifact)
	loaded, err := loadRocks(st)
	if err != nil {
		t.Fatal(err)
	}
	la := loaded.(*RocksArtifact)
	if la.Manifest.Variants[1].Triangles != art.Manifest.Variants[1].Triangles || la.ModelPath(&la.Manifest.Variants[0]) != pl[0].Model {
		t.Errorf("Load: %+v vs %+v", la.Manifest.Variants[1], art.Manifest.Variants[1])
	}
	// Rerun: up to date, skipped. Another variant to look at: still up to
	// date (preview only).
	d.Stages[0].Params["preview_variant"] = procgen.Int(1)
	d.Stages[0].Params["preview_triangles"] = procgen.Int(900)
	stale, err := procgen.StaleStages(d, procgen.Options{OutDir: dir})
	if err != nil || len(stale) != 0 {
		t.Fatalf("stale after a preview-only change: %v (%v)", stale, err)
	}
	res2, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Stage("stones").Skipped {
		t.Error("an unchanged rocks stage ran again")
	}
	d.Stages[0].Params["chips"] = procgen.Float(0.9)
	if stale, _ := procgen.StaleStages(d, procgen.Options{OutDir: dir}); len(stale) != 1 {
		t.Errorf("a changed shape parameter left the stage fresh: %v", stale)
	}
}

// A cliff chunk is placed as its mesh and cooked mesh-only (no
// decomposition), with its back recorded.
func TestRocksCliffChunkCollidesAsItsMesh(t *testing.T) {
	dir := t.TempDir()
	d := rocksDoc("cliff_chunk", 1, 20000)
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stage("stones")
	man, err := rocks.LoadManifest(st.Dir)
	if err != nil {
		t.Fatal(err)
	}
	v := man.Variants[0]
	if v.Collision != "mesh" || v.PlaceCollision != "mesh" || v.Back == nil || *v.Back != [3]float32{0, 0, -1} || v.Min[2] != 0 {
		t.Fatalf("cliff chunk %+v", v)
	}
	m, err := nanite.Read(st.File(v.File))
	if err != nil {
		t.Fatal(err)
	}
	if m.Collision == nil || m.Collision.Settings.Mode != meshcollide.ModeMesh || len(m.Collision.Hulls) != 0 {
		t.Errorf("the cliff chunk's cook built hulls")
	}
	if len(res.Placements()) != 0 {
		t.Error("placements without the gallery")
	}
}

// The same document on 1 and 7 workers cooks the same mesh: the same source
// hash and the same page bytes (the files differ only in the Source path
// they record, which is each run's own folder).
func TestRocksAreDeterministic(t *testing.T) {
	var meshes [2]*nanite.Mesh
	var mans [2][]byte
	for i, w := range []int{1, 7} {
		dir := t.TempDir()
		d := rocksDoc("rubble", 1, 1500)
		res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir, Workers: w})
		if err != nil {
			t.Fatal(err)
		}
		st := res.Stage("stones")
		m, err := nanite.Read(st.File("rubble_00.fnm"))
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Flat(); err != nil {
			t.Fatal(err)
		}
		meshes[i] = m
		data, err := os.ReadFile(st.File(rocks.ManifestFile))
		if err != nil {
			t.Fatal(err)
		}
		// The timings are the one thing allowed to differ.
		var keep [][]byte
		for _, l := range bytes.Split(data, []byte("\n")) {
			if !bytes.Contains(l, []byte("_ms:")) {
				keep = append(keep, l)
			}
		}
		mans[i] = bytes.Join(keep, []byte("\n"))
	}
	a, b := meshes[0], meshes[1]
	if a.SourceHash != b.SourceHash || a.PageBytes() != b.PageBytes() || len(a.Vertices) != len(b.Vertices) {
		t.Fatal("1 and 7 workers cooked different meshes")
	}
	for i := range a.Vertices {
		if a.Vertices[i] != b.Vertices[i] {
			t.Fatalf("vertex float %d differs", i)
		}
	}
	for i := range a.Colors {
		if a.Colors[i] != b.Colors[i] {
			t.Fatalf("colour %d differs", i)
		}
	}
	if !bytes.Equal(mans[0], mans[1]) {
		t.Errorf("the manifests differ:\n%s\n---\n%s", mans[0], mans[1])
	}
}

func TestRocksPreviewIsAColouredTurntable(t *testing.T) {
	d := rocksDoc("pebble", 3, 0)
	d.Stages[0].Params["preview_variant"] = procgen.Int(2)
	p, err := procgen.Preview(context.Background(), d, "stones", procgen.NewPreviewCache(2), procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Meshes) != 1 {
		t.Fatalf("%d preview meshes", len(p.Meshes))
	}
	m := p.Meshes[0]
	if !m.Turntable || m.Name != "pebble_02" || len(m.Colors) != len(m.Positions) || len(m.UVs) != len(m.Positions) || len(m.Indices) == 0 {
		t.Fatalf("preview mesh %s: turntable %v, %d colours, %d uvs for %d positions", m.Name, m.Turntable, len(m.Colors), len(m.UVs), len(m.Positions))
	}
	if len(p.Notes) < 3 || !strings.Contains(p.Notes[0], "triangles") || !strings.Contains(p.Notes[2], "bake") {
		t.Errorf("notes %q", p.Notes)
	}
	if len(p.Points) == 0 || p.FrameBounds().Empty() {
		t.Error("no footprint in the 2D view")
	}
	// The preview's variant is the run's: same seed.
	s := rocks.Defaults()
	s.Preset, s.Palette = "pebble", "auto"
	r, err := rocks.Generate(s, variantSeed(procgen.StageSeed(d.Seed, "stones", rocksParams(), procgen.Resolve(rocksParams(), d.Stages[0].Params)), 2))
	if err != nil {
		t.Fatal(err)
	}
	if r.Mesh.TriangleCount() != len(m.Indices)/3 {
		t.Errorf("the preview made %d triangles, the run's variant %d", len(m.Indices)/3, r.Mesh.TriangleCount())
	}
}

func TestRocksValidation(t *testing.T) {
	d := rocksDoc("boulder", 2, 0)
	d.Stages[0].Params["preview_variant"] = procgen.Int(5)
	probs := d.Validate()
	if len(probs) == 0 {
		t.Fatal("a preview variant past the last was accepted")
	}
	if got := rockCollision("auto", "boulder", [3]float32{1, 5, 1}, 4); got != "mesh" {
		t.Errorf("a 5 m boulder collides as %s", got)
	}
	if got := rockCollision("auto", "pebble", [3]float32{0.05, 0.02, 0.04}, 4); got != "none" {
		t.Errorf("a pebble collides as %s", got)
	}
	if got := rockCollision("hulls", "cliff_chunk", [3]float32{6, 8, 3}, 4); got != "hulls" {
		t.Errorf("an explicit choice was overridden: %s", got)
	}
}
