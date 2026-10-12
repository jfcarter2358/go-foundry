package place

// backprofile_test.go — a chunk's back measured (backprofile.go) and the
// silhouette seating it drives (wall.go's seatChunk).

import (
	"math"
	"testing"
)

// shellMesh is an open, curved sheet across chunks()'s 6 × 8 m box: its back
// on z = 0 down the middle and curling forward to z = curl at its sides, its
// normals (facing) toward +Z — the inside of an open-backed scan seen from
// behind — or toward −Z, a closed back.
func shellMesh(curl, facing float64) (pos, nrm [][3]float64, tris [][3]int) {
	const nx, ny = 24, 16
	for j := 0; j <= ny; j++ {
		for i := 0; i <= nx; i++ {
			x := -3 + 6*float64(i)/nx
			pos = append(pos, [3]float64{x, 8 * float64(j) / ny, curl * (x / 3) * (x / 3)})
			nrm = append(nrm, [3]float64{0, 0, facing})
		}
	}
	for j := 0; j < ny; j++ {
		for i := 0; i < nx; i++ {
			a := j*(nx+1) + i
			tris = append(tris, [3]int{a, a + 1, a + nx + 1}, [3]int{a + 1, a + nx + 2, a + nx + 1})
		}
	}
	return
}

func TestMeasureBackFindsTheOpenRim(t *testing.T) {
	v := chunks(1)[0]
	pos, nrm, tris := shellMesh(1.2, 1)
	bp := MeasureBack(&v, pos, nrm, tris, 16, 16)
	if bp == nil || len(bp.Outline) == 0 {
		t.Fatal("no profile")
	}
	var side, middle []float64
	for _, c := range bp.Outline {
		switch {
		case math.Abs(c.Lateral) > 0.9:
			side = append(side, c.Gap)
		case math.Abs(c.Lateral) < 0.1:
			middle = append(middle, c.Gap)
		}
	}
	for _, g := range side {
		if math.Abs(g-1.1) > 0.2 {
			t.Errorf("a side rim cell's gap is %.2f m, want about 1.1 (the sheet's curl there)", g)
		}
	}
	for _, g := range middle {
		if g > 0.05 {
			t.Errorf("a middle rim cell's gap is %.2f m, want about 0", g)
		}
	}
	if len(side) == 0 || len(middle) == 0 {
		t.Fatalf("outline %d cells, %d at the sides and %d in the middle", len(bp.Outline), len(side), len(middle))
	}
	// The same sheet facing the wall is a closed back: nothing to bury.
	_, back, _ := shellMesh(1.2, -1)
	for _, c := range MeasureBack(&v, pos, back, tris, 16, 16).Outline {
		if c.Gap != 0 {
			t.Fatalf("a closed back's outline cell has a gap of %.2f m", c.Gap)
		}
	}
}

func TestSilhouetteSeatingBuriesTheRim(t *testing.T) {
	g := stepGround(t, 128, 0.5, 25)
	lines := TraceCliffFeet(g, TraceSettings{Threshold: 0.5, Cell: 2, MinLength: 20, Smooth: 2})
	run := func(box bool, cover float64) (mean float64, n int) {
		s := DefaultWall()
		s.Chunks = chunks(1)
		pos, nrm, tris := shellMesh(1.2, 1)
		s.Chunks[0].BackProfile = MeasureBack(&s.Chunks[0], pos, nrm, tris, 16, 16)
		s.SeatBox, s.Seed, s.YawJitter, s.RollJitter, s.BackCover = box, 3, 0, 0, cover
		res, err := Walls(g, lines, s, pool(1))
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range res.Instances {
			if in.Kind == "chunk" {
				mean += in.Pos[0] // the wall is toward +X
				n++
			}
		}
		return mean / math.Max(float64(n), 1), n
	}
	boxX, nb := run(true, 1)
	silX, ns := run(false, DefaultWall().BackCover)
	allX, na := run(false, 1)
	if nb == 0 || ns == 0 || na == 0 {
		t.Fatalf("chunks placed: box %d, silhouette %d, whole rim %d", nb, ns, na)
	}
	t.Logf("mean x: box %.2f, silhouette %.2f, whole rim %.2f", boxX, silX, allX)
	// The rim curls 1.1 m forward at the sides (more, scaled with the
	// chunk). Burying it raises the least the back may go in by about that;
	// the seat is the middle of that least and the most the front's
	// protrusion allows, so it moves by about half of it. The default cover
	// (the rim's 60th percentile) goes part of the way.
	if allX-boxX < 0.4 {
		t.Fatalf("the whole-rim seat (mean x %.2f) is not deeper in the wall than the box seat (%.2f)", allX, boxX)
	}
	if !(silX > boxX+0.2 && silX <= allX+1e-9) {
		t.Fatalf("the default silhouette seat (%.2f) is not between the box's (%.2f) and the whole rim's (%.2f)", silX, boxX, allX)
	}
}
