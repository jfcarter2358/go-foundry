package stages

// terrain_normals_test.go — the tiles' normals come from the whole field, so
// neighbouring tiles shade their shared rim alike (docs/regions/PHASE_4.md,
// part A1). Before, each tile's vertex normals were the area-weighted
// normals of its own triangles: at the rim they saw one side of the seam
// only, and a headless render showed a straight shading line along every
// 128 m tile edge with effects, LOD and the visibility buffer all off.
//
// The test builds two neighbouring tiles of one field (with a ridge and a
// crease running across their seam), finds every top vertex the two share,
// and wants identical normals — and checks that the old per-tile normals
// differed there, so the test can tell the two apart. It does the same for
// the stitch strip that joins an edge tile to the far backdrop.

import (
	"context"
	"math"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
)

// normalsTestGrid is 64 m at 1 m: a sharp diagonal ridge and rolling
// ground, nothing symmetric about the tile seam at x = 0.
func normalsTestGrid() *terrain.Grid {
	g := terrain.NewGrid(64, 1)
	for j := 0; j <= g.N; j++ {
		for i := 0; i <= g.N; i++ {
			x, z := g.X(i), g.Z(j)
			ridge := 6 - 0.9*math.Abs(0.6*x-z+3)
			h := 2*math.Sin(x*0.21)*math.Cos(z*0.13) + 0.04*x*z/8 + math.Max(ridge, 0)
			g.H[j*g.S()+i] = float32(h)
		}
	}
	return g
}

// topVertices is a tile's top vertices by world XZ (skirt vertices share
// the rim's positions; they are told apart by their normals' Y).
func topVertices(t *testing.T, hg meshout.HeightGrid, c procgen.Point3) map[[2]float32]mgl32.Vec3 {
	t.Helper()
	var b meshout.Builder
	b.Slot("ground", "")
	hg.Build(&b)
	m := b.Mesh()
	out := map[[2]float32]mgl32.Vec3{}
	for k, p := range m.Positions {
		n := m.Normals[k]
		if n[1] < 0.3 || p[1] == hg.Base {
			continue
		}
		out[[2]float32{p[0] + c.X, p[2] + c.Z}] = n
	}
	return out
}

func TestNeighbouringTilesShareRimNormals(t *testing.T) {
	g := normalsTestGrid()
	k := procgen.KindByName(TerrainKind)
	for _, step := range []int{1, 2} {
		p := procgen.Resolve(k.Params, procgen.Params{"tile_size": procgen.Float(32), "mesh_step": procgen.Int(step)})
		tp := planTiles(p, g)
		if tp.count != 2 {
			t.Fatalf("want 2 tiles a side, got %d", tp.count)
		}
		for _, pair := range [][4]int{{0, 0, 1, 0}, {0, 0, 0, 1}, {1, 0, 1, 1}} {
			for _, old := range []bool{false, true} {
				ha, ca := tileGridAt(g, tp, pair[0], pair[1], 0.1, false)
				hb, cb := tileGridAt(g, tp, pair[2], pair[3], 0.1, false)
				if old {
					ha.Normal, hb.Normal = nil, nil
				}
				va, vb := topVertices(t, ha, ca), topVertices(t, hb, cb)
				shared, worst := 0, 0.0
				for pos, na := range va {
					nb, ok := vb[pos]
					if !ok {
						continue
					}
					shared++
					worst = math.Max(worst, float64(na.Sub(nb).Len()))
				}
				if shared < 32/step {
					t.Fatalf("step %d tiles %v: only %d shared rim vertices", step, pair, shared)
				}
				if !old && worst != 0 {
					t.Errorf("step %d tiles %v: shared rim normals differ by up to %.4f (want identical)", step, pair, worst)
				}
				if old && worst < 1e-3 {
					t.Errorf("step %d tiles %v: the per-tile normals agreed too (%.5f): the fixture cannot tell the two apart", step, pair, worst)
				}
			}
		}
	}
}

// farTestHeights is a backdrop height function for the strip test.
type farTestHeights struct{}

func (farTestHeights) Height(x, z float64) float64 { return 3 * math.Sin(x*0.05) * math.Cos(z*0.04) }

func TestStitchStripShadesLikeTheTileAndTheRing(t *testing.T) {
	g := normalsTestGrid()
	k := procgen.KindByName(TerrainKind)
	p := procgen.Resolve(k.Params, procgen.Params{"tile_size": procgen.Float(32)})
	tp := planTiles(p, g)
	rc := &procgen.RunContext{Env: procgen.Env{Ctx: context.Background(), Workers: 2}}
	half := g.Size() / 2
	l, err := buildFarLattice(rc, farTestHeights{}, g.MinX+half, g.MinZ+half, half, 4, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	hg, c := tileGridAt(g, tp, 1, 0, 0.1, false)
	tile := topVertices(t, hg, c)
	m, sc := stripMesh(g, tp, l, 's', 1, 0, "", 0.1)
	inner := 0
	for k, q := range m.Positions {
		pos := [2]float32{q[0] + sc.X, q[2] + sc.Z}
		n := m.Normals[k]
		if tn, ok := tile[pos]; ok && q[1] == float32(g.Sample(float64(pos[0]), float64(pos[1]))) {
			inner++
			if d := n.Sub(tn).Len(); d != 0 {
				t.Errorf("strip vertex at %v: normal %v, the tile's %v", pos, n, tn)
			}
			continue
		}
		// An outer vertex: the lattice's own normal.
		want := l.normal(l.ix(float64(pos[0])), l.iz(float64(pos[1])))
		if float64(pos[1]) < g.MinZ-1e-3 {
			if d := n.Sub(want).Len(); d > 1e-6 {
				t.Errorf("outer strip vertex at %v: normal %v, the lattice's %v", pos, n, want)
			}
		}
	}
	if inner < 32 {
		t.Fatalf("only %d strip vertices on the tile's rim", inner)
	}
}
