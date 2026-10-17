package terrain

// river_falls_test.go — phase W4's waterfall geometry (river_falls.go,
// docs/terrain-paint/PHASE_W4.md): the falling sheet follows the lip's
// width and a ballistic profile out over the pool, the notch keeps the
// ground clear under it, a long steep stretch becomes a cascade when asked,
// and a confluence trails foam down the main river.

import (
	"math"
	"testing"
)

// cliffGrid is TestSteepDropBecomesAWaterfall's valley: a 5 m cliff across
// it at x = 0, two metres wide.
func cliffGrid() *Grid {
	g := NewGrid(128, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			x, z := g.X(i), g.Z(j)
			h := 12 - 0.02*x + 0.1*math.Abs(z)
			h -= 5 * math.Min(math.Max((x+1)/2, 0), 1)
			g.H[j*s+i] = float32(h)
		}
	}
	return g
}

func TestFallsCurtainFollowsTheLipAndABallisticProfile(t *testing.T) {
	f := Waterfall{River: "r", X: 3, Z: -2, Lip: 10, Foot: 6, Drop: 4, DirX: 0, DirZ: 1, Width: 5, Speed: 1.5}
	c := f.Curtain(1)
	if c.Rows < 5 || c.Cols < 3 || len(c.P) != c.Rows*c.Cols || len(c.U) != len(c.P) || len(c.Aeration) != len(c.P) {
		t.Fatalf("grid %d×%d, %d verts", c.Rows, c.Cols, len(c.P))
	}
	// The lip row (row 1) spans the lip's width (a little narrower), centred
	// on the lip, at its level.
	lip := c.P[c.Cols : 2*c.Cols]
	w := math.Hypot(lip[0][0]-lip[c.Cols-1][0], lip[0][2]-lip[c.Cols-1][2])
	if w < 0.85*f.Width || w > f.Width {
		t.Fatalf("lip width %.2f for a %.2f m river", w, f.Width)
	}
	mid := lip[c.Cols/2]
	// The lip is uneven by a few centimetres (fixes after W5).
	if math.Abs(mid[0]-f.X) > 0.01 || math.Abs(mid[2]-f.Z) > 0.01 || math.Abs(mid[1]-f.Lip) > 0.06 {
		t.Fatalf("lip centre %v", mid)
	}
	// The lead-in reaches back upstream at the lip's level.
	if lead := c.P[c.Cols/2]; lead[2] >= f.Z || math.Abs(lead[1]-f.Lip) > 0.05 {
		t.Fatalf("lead-in %v", lead)
	}
	// Down the centre column: falling every row, moving out along the
	// heading ever faster (a parabola: along² ∝ drop), ending below the
	// pool's surface, out over the pool.
	prevY, prevA := math.Inf(1), -1.0
	for r := 1; r < c.Rows; r++ {
		p := c.P[r*c.Cols+c.Cols/2]
		along := p[2] - f.Z
		if p[1] >= prevY || along < prevA-1e-9 {
			t.Fatalf("row %d %v: not falling outward", r, p)
		}
		if r > 1 {
			// Each column on its own throw (fixes after W5), from the
			// lip's nominal level.
			fall := f.Lip + c.ColLip[c.Cols/2]*(1-0.5*c.Progress[r*c.Cols+c.Cols/2]) - p[1]
			want := c.ColVX[c.Cols/2] * math.Sqrt(2*fall/fallsGravity)
			if math.Abs(along-want) > 1e-6 {
				t.Fatalf("row %d: along %.3f, ballistic %.3f", r, along, want)
			}
		}
		prevY, prevA = p[1], along
	}
	if prevY > f.Foot-0.1 || prevA < 0.12*f.Drop || prevA > c.OutMax+1e-9 || c.OutMax > 1.25*(0.5*f.Drop+0.4) {
		t.Fatalf("foot at y %.2f out %.2f", prevY, prevA)
	}
	// It spreads as it falls; aerated toward the foot; frayed at the sides.
	last := c.P[(c.Rows-1)*c.Cols:]
	if wf := math.Hypot(last[0][0]-last[c.Cols-1][0], last[0][2]-last[c.Cols-1][2]); wf <= w {
		t.Fatalf("foot width %.2f, lip %.2f", wf, w)
	}
	if c.Aeration[(c.Rows-1)*c.Cols+c.Cols/2] < 0.9 || c.Aeration[c.Cols+c.Cols/2] > 0.6 {
		t.Fatalf("aeration lip %.2f foot %.2f", c.Aeration[c.Cols+c.Cols/2], c.Aeration[(c.Rows-1)*c.Cols+c.Cols/2])
	}
	if c.Edge[c.Cols] != 0 || c.Edge[c.Cols+c.Cols/2] < 0.99 {
		t.Fatalf("edge %v .. %v", c.Edge[c.Cols], c.Edge[c.Cols+c.Cols/2])
	}
	// A faster river throws its sheet further out.
	f2 := f
	f2.Speed = 3.5
	if c2 := f2.Curtain(1); c2.Out <= c.Out {
		t.Fatalf("out %.2f at 3.5 m/s, %.2f at 1.5", c2.Out, c.Out)
	}
}

func TestFallsNotchKeepsTheGroundUnderTheSheet(t *testing.T) {
	g := cliffGrid()
	sp := valleyRiver("falls", [2]float64{-55, 0}, [2]float64{55, 0})
	res := CarveRivers(g, []RiverSpec{sp}, nil, math.Inf(-1))
	if len(res.Falls) != 1 {
		t.Fatalf("falls %+v", res.Falls)
	}
	f := res.Falls[0]
	if f.Speed <= 0 {
		t.Fatalf("no speed over the lip: %+v", f)
	}
	c := f.Curtain(g.Cell)
	// Every sheet vertex past the lip and above the pool stands clear of
	// the ground under it.
	for r := 2; r < c.Rows; r++ {
		for k := 0; k < c.Cols; k++ {
			p := c.P[r*c.Cols+k]
			if p[1] < f.Foot {
				continue
			}
			if gy := float64(g.Sample(p[0], p[2])); gy > p[1]-0.05 {
				t.Fatalf("row %d col %d: sheet at %.2f, ground %.2f", r, k, p[1], gy)
			}
		}
	}
}

func TestCascadeTurnsALongSteepStretchIntoSteps(t *testing.T) {
	// TestLongSteepStretchIsRapidsNotAFall's 40% slope, with cascades on.
	g := NewGrid(128, 1)
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			x, z := g.X(i), g.Z(j)
			h := 30 - 0.02*x + 0.15*math.Abs(z)
			if x > -20 && x < 20 {
				h -= 0.4 * (x + 20)
			} else if x >= 20 {
				h -= 16
			}
			g.H[j*s+i] = float32(h)
		}
	}
	sp := valleyRiver("cascade", [2]float64{-55, 0}, [2]float64{55, 0})
	sp.Shape.CascadeStep = 1.5
	res := CarveRivers(g, []RiverSpec{sp}, nil, math.Inf(-1))
	if len(res.Falls) < 4 {
		t.Fatalf("%d falls down a 16 m slope at 1.5 m steps", len(res.Falls))
	}
	r := &res.Rivers[0]
	checkMonotone(t, r)
	for i, f := range res.Falls {
		if !f.Cascade || f.Drop < sp.Shape.MinFall-1e-9 || f.Drop > 2.5 {
			t.Fatalf("step %d %+v", i, f)
		}
		if i > 0 && f.S <= res.Falls[i-1].S {
			t.Fatalf("steps out of order")
		}
	}
	// Level between the steps: the surface holds just past each lip.
	for _, st := range r.Stations {
		for _, f := range res.Falls {
			if st.S > f.S && st.S < f.S+1 && math.Abs(st.Surface-f.Foot) > 1e-3 {
				t.Fatalf("station at S %.1f: %.3f, the step's pool %.3f", st.S, st.Surface, f.Foot)
			}
		}
	}
	// No step digs deeper than its own drop plus the channel and pool.
	for _, f := range res.Falls {
		if f.Lip-f.Foot > 2.5 {
			t.Fatalf("step %+v", f)
		}
	}
}

func TestConfluenceFoamTrailsDownTheMainRiver(t *testing.T) {
	g := valleyGrid()
	main := valleyRiver("main", [2]float64{-55, 0}, [2]float64{55, 0})
	main.Shape.Pools = 0 // no riffle foam to confuse the comparison
	trib := valleyRiver("trib", [2]float64{-5, 45}, [2]float64{0, 20}, [2]float64{2, 0.5})
	trib.Width = []float64{2, 2, 2}
	trib.Depth = []float64{0.5, 0.5, 0.5}
	trib.Shape.Pools = 0
	res := CarveRivers(g, []RiverSpec{trib, main}, nil, math.Inf(-1))
	if len(res.Confluences) != 1 {
		t.Fatalf("confluences %+v", res.Confluences)
	}
	c := res.Confluences[0]
	foamAt := func(x, z float64) float32 {
		i := int(math.Round((x - g.MinX) / g.Cell))
		j := int(math.Round((z - g.MinZ) / g.Cell))
		return res.Samples[int32(j*g.S()+i)].Foam
	}
	// Downstream of the mouth (the main river runs toward +X) the water is
	// foamy for a few widths; upstream as far, it is not; and the trail
	// fades with distance.
	down, up, far := foamAt(c.X+8, c.Z-0.5), foamAt(c.X-8, c.Z-0.5), foamAt(c.X+18, c.Z-0.5)
	if down < 0.2 || up > 0.1 || far >= down {
		t.Fatalf("confluence foam: 8 m down %.2f, 8 m up %.2f, 18 m down %.2f", down, up, far)
	}
}

// The sheet varies along its lip and nothing repeats evenly across it
// (fixes after W5): the columns are thrown different distances from an
// uneven lip, the aeration across a row is thick and thin, the sheet parts
// somewhere below the lip, and two falls elsewhere do not match.
func TestFallsCurtainVariesAlongTheLip(t *testing.T) {
	f := Waterfall{River: "r", X: 39.57, Z: 2.33, Lip: -6.5, Foot: -8.7, Drop: 2.2, DirX: 0.964, DirZ: 0.266, Width: 4.7, Speed: 1.4}
	c := f.Curtain(1)
	spread := func(v []float64) float64 {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, x := range v {
			lo, hi = math.Min(lo, x), math.Max(hi, x)
		}
		return hi - lo
	}
	if s := spread(c.ColVX); s < 0.08*c.VX {
		t.Errorf("throw varies by %.3f m/s of %.2f across the lip", s, c.VX)
	}
	if s := spread(c.ColLip); s < 0.02 {
		t.Errorf("the lip is level to %.3f m", s)
	}
	// A row a third of the way down, inside the frayed sides.
	r := c.Rows / 3
	row := c.Aeration[r*c.Cols+2 : (r+1)*c.Cols-2]
	if s := spread(row); s < 0.12 {
		t.Errorf("aeration across row %d spans only %.3f: one even curtain", r, s)
	}
	// Somewhere inside the sheet below the lip the edge share dips: a gap
	// or a ragged side well in from the edge.
	dip := false
	for rr := 2; rr < c.Rows; rr++ {
		for k := c.Cols / 4; k < c.Cols*3/4; k++ {
			if c.Edge[rr*c.Cols+k] < 0.7 {
				dip = true
			}
		}
	}
	if !dip {
		t.Errorf("the sheet never parts in its middle half")
	}
	g := f
	g.X, g.Z = -51.33, 35.03
	if c2 := g.Curtain(1); c2.Cols == c.Cols && math.Abs(c2.ColVX[c.Cols/2]/c2.VX-c.ColVX[c.Cols/2]/c.VX) < 1e-6 &&
		math.Abs(c2.Aeration[r*c.Cols+c.Cols/2]-c.Aeration[r*c.Cols+c.Cols/2]) < 1e-6 {
		t.Errorf("two falls in different places have the same variation")
	}
}
