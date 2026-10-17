package stages

// cave_test.go — the cave stage end to end on a small terrain (fake Poly
// Haven): it registers, it cuts the terrain where it comes through the
// ground and nowhere it does not, the mouth is open (a ray from outside to
// inside crosses no terrain or rock), the interior is closed (rays from a
// chamber hit rock in every direction but the mouth's), no terrain triangle
// is left in the void, and its manifest loads back.

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/nanite"
	"foundry/internal/procgen"
	"foundry/internal/procgen/cave"
	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"
)

func TestCaveStageRegisters(t *testing.T) {
	k := procgen.KindByName(CaveKind)
	if k == nil {
		t.Fatal("no cave stage kind")
	}
	if k.Preview == nil || k.Load == nil || k.Run == nil {
		t.Fatal("the cave kind lacks a run, a preview or a load")
	}
	p := procgen.Resolve(k.Params, nil)
	if probs := validateCave(nil, nil, p); len(probs) == 0 {
		t.Fatal("an empty cave validated")
	}
	if procgen.KindByName(TerrainKind).Params[0].Name == "" {
		t.Fatal("terrain kind broken")
	}
}

// caveTestDoc is a 128 m terrain: flat ground at about 0 with a 20 m
// plateau from x ≈ -14 east (held flat), a cave from the flat ground into
// the plateau.
func caveTestDoc() *procgen.Doc {
	f, i, s, ref := procgen.Float, procgen.Int, procgen.String, procgen.StageRef
	return &procgen.Doc{Name: "t", Seed: 11, Stages: []procgen.Stage{
		{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{
			"layers": procgen.List(procgen.Params{"name": s("sand"), "id": s("sand")})}},
		{Kind: TerrainKind, ID: "ground", Params: procgen.Params{
			"size": f(128), "resolution": f(0.5), "tile_size": f(32), "mesa_height": f(0), "relief": f(0.3),
			"drainage_iterations": i(5), "droplets": f(0.05), "talus_iterations": i(4), "detail": f(0),
			"material": s("gen_t_ground_mat"),
			"stamps": procgen.List(procgen.Params{"kind": procgen.Enum("plateau"),
				"points": procgen.Spline([]float64{25, 0, 34, 20}), "falloff": f(5), "rough": f(0), "hold": f(1)}),
		}},
		{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{
			"terrain": ref("ground"), "textures": ref("tex"), "sand": s("sand"), "cracked": s("sand"), "gravel": s("sand"),
			"rock": s("sand")}},
		{Kind: CaveKind, ID: "cave", Params: procgen.Params{
			"terrain": ref("ground"), "ground": ref("ground_mat"),
			"path": procgen.Spline(
				[]float64{-30, 0, 0, 7, 6}, []float64{-12, 0.3, 0, 7, 6}, []float64{8, 0.8, 1, 12, 9}, []float64{30, 1.2, -2, 7, 6}),
			"wander": f(0), "blocks": f(0.3), "cell": f(0.4), "sky_reach": f(10), "rock_density": f(0)}},
	}}
}

// meshTris is a placed mesh's world triangles.
func meshTris(t *testing.T, pl procgen.Placement) [][3]sdf.Vec3 {
	t.Helper()
	nm, err := nanite.Load(fileutil.ResolveAssetPath(pl.Model))
	if err != nil {
		t.Fatalf("%s: %v", pl.Model, err)
	}
	if err := nm.Flat(); err != nil {
		t.Fatal(err)
	}
	// The full detail only: level 0's clusters (Flat holds every level).
	dm := &driftMesh{pos: make([][3]float64, len(nm.Vertices)/5)}
	for k := range dm.pos {
		dm.pos[k] = [3]float64{float64(nm.Vertices[5*k]), float64(nm.Vertices[5*k+1]), float64(nm.Vertices[5*k+2])}
	}
	for _, c := range nm.Clusters {
		if c.Level != 0 {
			continue
		}
		for q := c.TriFirst; q < c.TriFirst+c.TriCount; q++ {
			dm.tris = append(dm.tris, [3]int{int(nm.Indices[3*q]), int(nm.Indices[3*q+1]), int(nm.Indices[3*q+2])})
		}
	}
	pos := dm.placed(pl)
	out := make([][3]sdf.Vec3, len(dm.tris))
	for k, tr := range dm.tris {
		for c := 0; c < 3; c++ {
			p := pos[tr[c]]
			out[k][c] = sdf.Vec3{p[0], p[1], p[2]}
		}
	}
	return out
}

// rayHit is the nearest hit of the ray o + t·d (0 < t < far) on tris.
func rayHit(o, d sdf.Vec3, far float64, tris [][3]sdf.Vec3) (float64, bool) {
	best, hit := far, false
	for _, tr := range tris {
		e1, e2 := tr[1].Sub(tr[0]), tr[2].Sub(tr[0])
		p := d.Cross(e2)
		det := e1.Dot(p)
		if math.Abs(det) < 1e-12 {
			continue
		}
		inv := 1 / det
		s := o.Sub(tr[0])
		u := s.Dot(p) * inv
		if u < 0 || u > 1 {
			continue
		}
		q := s.Cross(e1)
		v := d.Dot(q) * inv
		if v < 0 || u+v > 1 {
			continue
		}
		if tt := e2.Dot(q) * inv; tt > 1e-4 && tt < best {
			best, hit = tt, true
		}
	}
	return best, hit
}

func TestCaveCutsTheTerrainAndOpensItsMouth(t *testing.T) {
	if testing.Short() {
		t.Skip("cooks a terrain and a cave")
	}
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	d := caveTestDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: filepath.Join(res, "generated", "t")})
	if err != nil {
		t.Fatal(err)
	}
	cs := r.Stage("cave")
	a, ok := cs.Artifact.(*CaveArtifact)
	if !ok || a.Holes == 0 || len(a.Tiles) == 0 {
		t.Fatalf("cave artifact %+v: no holes or no tiles", a)
	}
	if len(a.Mouths) == 0 || len(a.Interior) == 0 {
		t.Fatalf("no mouths (%d) or interior boxes (%d)", len(a.Mouths), len(a.Interior))
	}
	if sup := cs.Supersedes["ground"]; len(sup) != len(a.Tiles) {
		t.Fatalf("supersedes %v, recooked %v", sup, a.Tiles)
	}
	// The run's placements: the terrain's tiles where the cave is, the cave's
	// own; the terrain's own copies of those tiles left out.
	var tiles, rock [][3]sdf.Vec3
	parts := map[string]int{}
	for _, pl := range r.Placements() {
		id := pl.ID
		switch {
		case strings.HasPrefix(id, "gen.t.ground.tile_"):
			for _, n := range a.Tiles {
				if id == "gen.t.ground."+n {
					t.Fatalf("the terrain's own %s was not superseded", n)
				}
			}
			tiles = append(tiles, meshTris(t, pl)...)
		case strings.HasPrefix(id, "gen.t.cave.tile_"):
			tiles = append(tiles, meshTris(t, pl)...)
		case strings.HasPrefix(id, "gen.t.cave."):
			if pl.Collision != "mesh" || pl.DrawDistance <= 0 {
				t.Errorf("%s: collision %q, draw %g", id, pl.Collision, pl.DrawDistance)
			}
			parts[strings.SplitN(strings.TrimPrefix(id, "gen.t.cave."), "_", 2)[0]]++
			rock = append(rock, meshTris(t, pl)...)
		}
	}
	if parts["rock"] == 0 || parts["sand"] == 0 {
		t.Fatalf("cave parts %v: want rock and sand", parts)
	}
	v := a.VoidOn(r.Stage("ground").Artifact.(*terrain.Terrain).Grid)
	// No terrain triangle is left in the void.
	for _, tr := range tiles {
		c := tr[0].Add(tr[1]).Add(tr[2]).Mul(1.0 / 3)
		if l, ok := v.Local(c); ok && l.B > cave.FloorKeep+0.2 && v.At(c) < -0.15 {
			t.Fatalf("a terrain triangle at %v is inside the void (%.2f)", c, v.At(c))
		}
	}
	// The mouth is open: from the flat ground outside, along the passage at
	// head height, nothing is in the way.
	o := sdf.Vec3{-26, 1.8, 0}
	in := sdf.Vec3{2, 2.3, 0.4}
	dir := in.Sub(o)
	far := dir.Len()
	dir = dir.Mul(1 / far)
	if tt, hit := rayHit(o, dir, far, tiles); hit {
		t.Errorf("the walk in hits the terrain %.1f m in, at %v", tt, o.Add(dir.Mul(tt)))
	}
	if tt, hit := rayHit(o, dir, far, rock); hit {
		t.Errorf("the walk in hits the cave's rock %.1f m in, at %v", tt, o.Add(dir.Mul(tt)))
	}
	// The interior is closed: from the chamber, every ray away from the
	// mouth meets the cave's rock (a gap would show the terrain's underside,
	// which is not drawn, and so the sky).
	c := sdf.Vec3{8.13, 3.07, 1.11}
	esc := 0
	for yaw := 0.0; yaw < 360; yaw += 10 {
		for pitch := -60.0; pitch <= 80; pitch += 10 {
			cy, sy := math.Cos(yaw*math.Pi/180), math.Sin(yaw*math.Pi/180)
			cp, sp := math.Cos(pitch*math.Pi/180), math.Sin(pitch*math.Pi/180)
			d := sdf.Vec3{cy * cp, sp, sy * cp}
			if d[0] < -0.6 {
				continue // toward the mouth
			}
			if _, hit := rayHit(c, d, 40, rock); !hit {
				esc++
				if esc < 5 {
					t.Errorf("a ray from the chamber at yaw %.0f pitch %.0f leaves through the rock", yaw, pitch)
				}
			}
		}
	}
	// It loads back from its manifest.
	la, err := loadCave(cs)
	if err != nil {
		t.Fatal(err)
	}
	if got := la.(*CaveArtifact).VoidOn(r.Stage("ground").Artifact.(*terrain.Terrain).Grid).At(c); math.Abs(got-v.At(c)) > 1e-9 {
		t.Errorf("the loaded void differs: %.4f against %.4f", got, v.At(c))
	}
}
