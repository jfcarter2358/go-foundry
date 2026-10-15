package stages

// ruins_test.go — the ruins stage end to end on a small sloping terrain
// (fake Poly Haven, coarse detail): it registers and validates, places
// every building it cooked with mesh collision, its cooked terraces and
// stairs carry the walking surface the layout promised (a ray down onto
// each tread lands on it), no floor is buried by the terrain, and its
// manifest loads back.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"
)

func TestRuinsStageRegisters(t *testing.T) {
	k := procgen.KindByName(RuinsKind)
	if k == nil {
		t.Fatal("no ruins stage kind")
	}
	if k.Preview == nil || k.Load == nil || k.Run == nil {
		t.Fatal("the ruins kind lacks a run, a preview or a load")
	}
	p := procgen.Resolve(k.Params, nil)
	if probs := validateRuins(nil, nil, p); len(probs) < 2 {
		t.Fatalf("an empty ruins stage validated: %v", probs)
	}
	p["terrain"] = procgen.StageRef("ground")
	p["path"] = procgen.Spline([]float64{0, 0, 20}, []float64{0, 50, 20})
	if probs := validateRuins(nil, nil, p); len(probs) != 0 {
		t.Fatalf("a good ruins stage has problems: %v", probs)
	}
	p["site_cell"] = procgen.Float(0.3)
	if probs := validateRuins(nil, nil, p); len(probs) != 1 {
		t.Fatalf("a terrace cell over the riser was not caught: %v", probs)
	}
}

// ruinsTestDoc is a 160 m terrain falling north to south (a plateau stamp
// on the high side, a ramp down), ruins down the ramp.
func ruinsTestDoc() *procgen.Doc {
	f, i, s, ref, v2 := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2
	return &procgen.Doc{Name: "t", Seed: 5, Stages: []procgen.Stage{
		{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{
			"layers": procgen.List(procgen.Params{"name": s("sand"), "id": s("sand")})}},
		{Kind: TerrainKind, ID: "ground", Params: procgen.Params{
			"size": f(160), "resolution": f(1), "tile_size": f(40), "mesa_height": f(0), "relief": f(0.3),
			"drainage_iterations": i(4), "droplets": f(0.05), "talus_iterations": i(4), "detail": f(0),
			"material": s("gen_t_ground_mat"),
			"stamps": procgen.List(procgen.Params{"kind": procgen.Enum("ramp"),
				"points": procgen.Spline([]float64{0, -60, 10, 40}, []float64{0, 60, 10, 4}), "falloff": f(40), "strength": f(1), "hold": f(1)}),
		}},
		{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{
			"terrain": ref("ground"), "textures": ref("tex"), "sand": s("sand"), "cracked": s("sand"), "gravel": s("sand"),
			"rock": s("sand")}},
		{Kind: RuinsKind, ID: "ruins", Params: procgen.Params{
			"terrain": ref("ground"), "ground": ref("ground_mat"), "textures": ref("tex"), "temple_layer": s("sand"),
			"path":   procgen.Spline([]float64{0, -55, 22}, []float64{2, 0, 24}, []float64{0, 55, 22}),
			"detail": f(0.2), "site_cell": f(0.15), "outliers": i(3), "spread": f(10), "terrace_length": v2(12, 18),
			"weathering": f(0.5)}},
	}}
}

func TestRuinsStageEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("cooks a terrain and the ruins")
	}
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	d := ruinsTestDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: filepath.Join(res, "generated", "t"), Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	rs := r.Stage("ruins")
	a, ok := rs.Artifact.(*RuinsArtifact)
	if !ok || len(a.Terraces) < 3 || len(a.Instances) == 0 || len(a.Variants) == 0 {
		t.Fatalf("ruins artifact: %d terraces, %d instances, %d variants", len(a.Terraces), len(a.Instances), len(a.Variants))
	}
	g := r.Stage("ground").Artifact.(*terrain.Terrain).Grid
	// Placements: every library model exists, every placement collides as a
	// mesh and has a draw distance; the site's sections are there.
	var site [][3]sdf.Vec3
	inst := 0
	for _, pl := range r.Placements() {
		if !strings.HasPrefix(pl.ID, "gen.t.ruins.") {
			continue
		}
		if pl.Collision != "mesh" || pl.DrawDistance <= 0 || pl.Material == "" {
			t.Errorf("%s: collision %q, draw %g, material %q", pl.ID, pl.Collision, pl.DrawDistance, pl.Material)
		}
		if _, err := os.Stat(fileutil.ResolveAssetPath(pl.Model)); err != nil {
			t.Errorf("%s: %v", pl.ID, err)
		}
		if strings.HasPrefix(pl.ID, "gen.t.ruins.site_") {
			site = append(site, meshTris(t, pl)...)
		} else {
			inst++
		}
	}
	if inst != len(a.Instances) || len(site) == 0 {
		t.Fatalf("%d instances placed of %d; %d site triangles", inst, len(a.Instances), len(site))
	}
	// Walkable: a ray down onto the middle of every tread and every floor
	// lands on the cooked site within a few centimetres of the layout's
	// height, and that height is over the terrain.
	down := sdf.Vec3{0, -1, 0}
	check := func(what string, x, y, z float64) {
		o := sdf.Vec3{x, y + 2, z}
		tt, hit := rayHit(o, down, 6, site)
		if !hit {
			t.Errorf("%s: nothing under %.1f,%.1f", what, x, z)
			return
		}
		if got := o[1] - tt; math.Abs(got-y) > 0.08 {
			t.Errorf("%s: the surface is at %.2f, the layout says %.2f", what, got, y)
		}
		if gh := float64(g.Sample(x, z)); gh > y+0.02 {
			t.Errorf("%s: the terrain (%.2f) is over the floor (%.2f)", what, gh, y)
		}
	}
	for i, tr := range a.Terraces {
		c := tr.Corners
		x, z := (c[0][0]+c[2][0])/2, (c[0][1]+c[2][1])/2
		check("terrace "+itoa(i), x, tr.Floor, z)
	}
	for i, s := range a.Stairs {
		if s.Rise > 0.18+1e-9 {
			t.Errorf("stair %d: riser %.3f", i, s.Rise)
		}
		dx, dz := s.To[0]-s.From[0], s.To[2]-s.From[2]
		for k := 1; k < s.Steps-1; k += max(1, s.Steps/6) {
			f := (float64(k) + 0.5) / float64(s.Steps)
			check("stair "+itoa(i)+" step "+itoa(k), s.From[0]+dx*f, s.From[1]-float64(k+1)*s.Rise, s.From[2]+dz*f)
		}
	}
	// The manifest loads back.
	la, err := loadRuins(rs)
	if err != nil {
		t.Fatal(err)
	}
	if lb := la.(*RuinsArtifact); len(lb.Instances) != len(a.Instances) || len(lb.Terraces) != len(a.Terraces) {
		t.Fatal("the loaded manifest differs")
	}
	var tris int
	for _, v := range a.Variants {
		tris += v.Triangles
	}
	t.Logf("%d terraces, %d stairs, %d plinths, %d instances of %d variants (%d triangles), site %d sections %d triangles",
		len(a.Terraces), len(a.Stairs), len(a.Plinths), len(a.Instances), len(a.Variants), tris, a.Site.Sections, a.Site.Triangles)
}

func itoa(i int) string { return strconv.Itoa(i) }
