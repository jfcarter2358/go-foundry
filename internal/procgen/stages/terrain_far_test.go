package stages

// terrain_far_test.go — the terrain's far backdrop: the run's ring and
// stitch, the far material, and the stitch's inner edge being the map's own
// rim (the only thing that keeps the map's edge from showing).

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

func TestFarBackdropRingStitchAndMaterial(t *testing.T) {
	dir := t.TempDir()
	d := smallTerrainDoc()
	p := d.Stages[0].Params
	p["far"], p["far_cell"], p["far_blend"], p["far_tile"] = procgen.Float(200), procgen.Float(16), procgen.Float(60), procgen.Float(128)
	p["far_material"] = procgen.String("gen_t_mat_far")
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stage("ground")
	tr, err := terrain.Load(st.Dir)
	if err != nil {
		t.Fatal(err)
	}
	fm := tr.Meta.Far
	if fm == nil {
		t.Fatal("terrain.yaml has no far record")
	}
	var rings, strips int
	for _, pl := range res.Placements() {
		switch {
		case strings.Contains(pl.ID, ".far_"):
			rings++
			if pl.Collision != "none" || pl.Material != "gen_t_mat_far" {
				t.Errorf("%s: collision %q material %q", pl.ID, pl.Collision, pl.Material)
			}
		case strings.Contains(pl.ID, ".strip_"):
			strips++
		}
	}
	if rings != len(fm.Tiles) || rings == 0 || strips != 8 {
		t.Errorf("%d ring tiles (meta %d), %d strips (want 8: four sides of two edge tiles)", rings, len(fm.Tiles), strips)
	}
	for _, f := range append(append([]string{terrain.FarHeightFile}, fm.Tiles...), fm.Strips...) {
		if _, err := os.Stat(st.File(f)); err != nil {
			t.Errorf("no %s: %v", f, err)
		}
	}
	h, err := tr.FarHeights()
	if err != nil {
		t.Fatal(err)
	}
	// The lattice at the map's edge is the map: sample on the edge.
	g := tr.Grid
	at := func(x, z float64) float32 {
		i := int(math.Round((x - fm.Bounds.MinX) / fm.Cell))
		j := int(math.Round((z - fm.Bounds.MinZ) / fm.Cell))
		return h[j*fm.Samples+i]
	}
	for _, x := range []float64{g.MinX, g.MinX + 16, g.MinX + 48} {
		if got, want := at(x, g.MinZ), g.Sample(x, g.MinZ); math.Abs(float64(got-want)) > 0.01*float64(fm.Height.Hi-fm.Height.Lo)+0.05 {
			t.Errorf("far lattice at the edge (%g, %g): %.2f, the map %.2f", x, g.MinZ, got, want)
		}
	}
}

// The stitch's inner edge is the edge tile's own rim — the same samples, so
// the same heights — and every triangle faces up.
func TestStitchStripMeetsTheTileRim(t *testing.T) {
	g := terrain.NewGrid(64, 1)
	for j := 0; j <= g.N; j++ {
		for i := 0; i <= g.N; i++ {
			g.H[j*g.S()+i] = float32(10 + 5*math.Sin(g.X(i)*0.3) + 3*math.Cos(g.Z(j)*0.2))
		}
	}
	tp := tilePlan{count: 2, per: 32, step: 1, size: 32, base: -10, quant: 1.0 / 1024}
	half, cell := 32.0, 8.0
	l := &farLattice{cell: cell, o: half + cell, r: half + 3*cell}
	l.minX = -l.r
	l.n = int(math.Round(2*l.r/cell)) + 1
	l.h = make([]float32, l.n*l.n)
	for k := range l.h {
		l.h[k] = 7
	}
	for _, side := range []byte{'s', 'n', 'w', 'e'} {
		ti, tj := 0, 0
		switch side {
		case 'n':
			tj = 1
		case 'e':
			ti = 1
		}
		m, c := stripMesh(g, tp, l, side, ti, tj, "", 0.1)
		rim := 0
		for _, q := range m.Positions {
			x, z := float64(q[0]+c.X), float64(q[2]+c.Z)
			onEdge := (side == 's' && math.Abs(z-g.MinZ) < 1e-4) || (side == 'n' && math.Abs(z+g.MinZ) < 1e-4) ||
				(side == 'w' && math.Abs(x-g.MinX) < 1e-4) || (side == 'e' && math.Abs(x+g.MinX) < 1e-4)
			// (The corner quads' outer corners lie on the edge's line,
			// outside the map: not the rim.)
			if !onEdge || math.Abs(x) > half+1e-4 || math.Abs(z) > half+1e-4 {
				continue
			}
			rim++
			want := g.Sample(x, z)
			if math.Abs(float64(q[1]-want)) > 1e-4 {
				t.Fatalf("side %c: rim vertex at (%.1f, %.1f) is %.4f, the tile's %.4f", side, x, z, q[1], want)
			}
		}
		if rim < tp.per+1 {
			t.Errorf("side %c: %d rim vertices, want at least %d", side, rim, tp.per+1)
		}
		for k := 0; k+2 < len(m.Indices); k += 3 {
			a, b, cc := m.Positions[m.Indices[k]], m.Positions[m.Indices[k+1]], m.Positions[m.Indices[k+2]]
			e1, e2 := b.Sub(a), cc.Sub(a)
			if e1.Cross(e2)[1] <= 0 {
				t.Fatalf("side %c: triangle %d faces down", side, k/3)
			}
		}
	}
}
