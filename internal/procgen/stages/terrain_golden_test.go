package stages

// terrain_golden_test.go — the classic `terrain` stage's files pinned to the
// byte, recorded before the environment-regions split of procgen/terrain
// (docs/regions/PHASE_2.md).
//
// Two runs:
//
//   - desert_ground: the desert_canyon template's `ground` stage, its own
//     params (mesas, the stepped canyon, the UVs and material names) at a
//     reduced size — 256 m at 1 m in 128 m tiles, uncooked so the tiles are
//     .glb files whose bytes are their meshes;
//   - far_small: smallTerrainDoc with the far backdrop, cooked, so the ring,
//     the stitch strips and far_height.png (whose lattice and strip code the
//     union terrain generalised to an off-centre map) are pinned too.
//
// Every output file's bytes are hashed in name order. FOUNDRY_TERRAIN_GOLDEN_PRINT=1
// prints the current hashes.
//
// desert_ground is pinned in two parts since docs/regions/PHASE_4.md: its
// fields (heightmap, masks, albedo, relief, terrain.yaml), unchanged since
// the recording, and its tile meshes, re-recorded DELIBERATELY when the
// tiles' normals became the whole field's (meshout.HeightGrid.Normal) —
// the one change. Before re-recording, the old single hash was checked to
// still match with the field normals switched off.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"encoding/binary"
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// dirHash is a hash of every file in dir (names and bytes, sorted), less the
// cooked meshes and the placements (see the loop).
func dirHash(t *testing.T, dir string) string {
	return dirHashOf(t, dir, func(string) bool { return true })
}

// dirHashOf is dirHash over the files keep accepts.
func dirHashOf(t *testing.T, dir string, keep func(name string) bool) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		// Cooked .fnm bytes are not stable run to run (the cook's own
		// parallelism), and placements.yaml holds absolute paths: the far
		// meshes are pinned through farMeshHash instead.
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), ".tmp") ||
			strings.HasSuffix(e.Name(), ".fnm") || e.Name() == "placements.yaml" || !keep(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// desertGroundDoc is the desert canyon's ground stage, reduced.
func desertGroundDoc() *procgen.Doc {
	src := DesertCanyon("golden")
	g := *src.StageByID("ground")
	g.Params = g.Params.Clone()
	g.Params["size"], g.Params["resolution"], g.Params["tile_size"] = procgen.Float(256), procgen.Float(1), procgen.Float(128)
	g.Params["cook"] = procgen.Bool(false)
	g.Params["far"] = procgen.Float(0)
	return &procgen.Doc{Name: "golden", Seed: src.Seed, Stages: []procgen.Stage{g}}
}

func farSmallDoc() *procgen.Doc {
	d := smallTerrainDoc()
	p := d.Stages[0].Params
	p["far"], p["far_cell"], p["far_blend"], p["far_tile"] = procgen.Float(200), procgen.Float(16), procgen.Float(60), procgen.Float(128)
	return d
}

var terrainStageGolden = map[string]string{
	// Recorded with the region work's first phase (the fields and the tiles
	// together): b4cf8a1ec70c8c0693fe8b94702d784a6efc11eea25e8614f84a72e6eaab536d.
	"desert_ground_fields": "64cc1c03d5b83a6e8e938a02b436bad56f0781abeca9f1dcf789a9ca3333742a",
	"desert_ground_tiles":  "5dec6dfdec7d97a3e767d09204d00cf54a822b11a126ef61acae65a2438fe3df",
	"far_small":            "b6251d06a0a6c9db2677c4fbe8f26491f8e7cd1c6e3ecb73b41170eaa4d13860",
	"far_meshes":           "59afae844a0f9a4c5debc581fca9d0a1ed6a25f93bf12c42f36a33dc5d7a914b",
}

// farMeshHash rebuilds the far lattice and every stitch strip of a run's
// terrain the way writeFar does and hashes their heights, positions and
// indices: the ring and strip geometry, without the cook.
func farMeshHash(t *testing.T, d *procgen.Doc, tr *terrain.Terrain) string {
	t.Helper()
	k := procgen.KindByName(TerrainKind)
	env := procgen.Env{Ctx: context.Background(), Workers: 3, Params: procgen.Resolve(k.Params, d.Stages[0].Params),
		Seed: procgen.StageSeed(d.Seed, d.Stages[0].ID, k.Params, procgen.Resolve(k.Params, d.Stages[0].Params))}
	s := terrainSettings(&env)
	p := env.Params
	res, err := terrain.Generate(&s, terrain.NewPool(context.Background(), 3), nil)
	if err != nil {
		t.Fatal(err)
	}
	rc := &procgen.RunContext{Env: env}
	g := res.Grid
	tp := planTiles(p, g)
	ff := terrain.NewFarField(&s, res, p.Float("far_cell"), p.Float("far_blend"), p.Float("far"))
	l, err := goldenFarLattice(rc, ff, g, p)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	var b [4]byte
	put := func(v float32) {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
		h.Write(b[:])
	}
	for _, v := range l.h {
		put(v)
	}
	for _, sd := range []byte{'s', 'n', 'w', 'e'} {
		for k := 0; k < tp.count; k++ {
			ti, tj := k, 0
			switch sd {
			case 'n':
				tj = tp.count - 1
			case 'w':
				ti, tj = 0, k
			case 'e':
				ti, tj = tp.count-1, k
			}
			m, c := stripMesh(g, tp, l, sd, ti, tj, p.Str("texture"), float32(p.Float("uv_scale")))
			put(c.X)
			put(c.Z)
			for _, q := range m.Positions {
				put(q[0])
				put(q[1])
				put(q[2])
			}
			for _, ix := range m.Indices {
				binary.LittleEndian.PutUint32(b[:], ix)
				h.Write(b[:])
			}
		}
	}
	_ = tr
	return hex.EncodeToString(h.Sum(nil))
}

func TestTerrainStageGolden(t *testing.T) {
	for name, d := range map[string]*procgen.Doc{"desert_ground": desertGroundDoc(), "far_small": farSmallDoc()} {
		dir := t.TempDir()
		res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir, Workers: 6})
		if err != nil {
			t.Fatal(err)
		}
		sd := res.Stage(d.Stages[0].ID).Dir
		got := map[string]string{name: dirHash(t, sd)}
		if name == "desert_ground" {
			glb := func(n string) bool { return strings.HasSuffix(n, ".glb") }
			got = map[string]string{
				"desert_ground_fields": dirHashOf(t, sd, func(n string) bool { return !glb(n) }),
				"desert_ground_tiles":  dirHashOf(t, sd, glb),
			}
		}
		if name == "far_small" {
			got["far_meshes"] = farMeshHash(t, d, res.Stage("ground").Artifact.(*terrain.Terrain))
		}
		for n, v := range got {
			if os.Getenv("FOUNDRY_TERRAIN_GOLDEN_PRINT") != "" {
				t.Logf("%q: %q,", n, v)
				continue
			}
			if want := terrainStageGolden[n]; v != want {
				t.Errorf("%s: the terrain stage's output hashes %s, pinned %s — the classic stage's output changed", n, v, want)
			}
		}
	}
}

// goldenFarLattice is buildFarLattice as writeFar calls it.
func goldenFarLattice(rc *procgen.RunContext, ff *terrain.FarField, g *terrain.Grid, p procgen.Params) (*farLattice, error) {
	half := g.Size() / 2
	return buildFarLattice(rc, ff, g.MinX+half, g.MinZ+half, half, p.Float("far_cell"), p.Float("far"), p.Float("far_tile"))
}
