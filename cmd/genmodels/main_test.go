package main

import (
	"testing"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// Every face has to wind counter-clockwise seen from outside, and nothing in
// the generator checks it: a part built with its ends the wrong way round, or a
// mirrored half whose reflection was not compensated for, produces a hull that
// looks correct in wireframe and is lit inside out.
//
// Parts are laid down eight vertices and six faces at a time, so each face's
// own part is the eight vertices its indices fall in — which gives a centroid
// to test "outward" against. Every part is convex, so the test is exact.
func TestGeneratedModelsWindOutward(t *testing.T) {
	for _, doc := range []*model.ModelDoc{dropshipHull(), dropshipGlass()} {
		if len(doc.Verts)%8 != 0 || len(doc.Faces) != len(doc.Verts)/8*6 {
			t.Fatalf("%s: %d vertices and %d faces are not whole parts",
				doc.ID, len(doc.Verts), len(doc.Faces))
		}
		for fi := range doc.Faces {
			f := &doc.Faces[fi]
			base := (fi / 6) * 8
			var centre mgl32.Vec3
			for i := 0; i < 8; i++ {
				centre = centre.Add(doc.Verts[base+i])
			}
			centre = centre.Mul(1.0 / 8.0)

			var face mgl32.Vec3
			for _, vi := range f.Verts {
				if vi < base || vi >= base+8 {
					t.Fatalf("%s: face %d reaches outside its own part", doc.ID, fi)
				}
				face = face.Add(doc.Verts[vi])
			}
			face = face.Mul(1 / float32(len(f.Verts)))

			n := faceNormal(doc.Verts, f.Verts)
			if out := face.Sub(centre); n.Dot(out) <= 0 {
				t.Errorf("%s: face %d (part %d) winds inward: normal %v, outward %v",
					doc.ID, fi, fi/6, n, out)
			}
		}
	}
}

// The hull's shell has to sit outside the hold it covers, or the deck the
// player walks on pokes through the belly. The two files share the constants
// this asserts; the point is that they are asserted at all.
func TestHullEnclosesTheHold(t *testing.T) {
	doc := dropshipHull()
	var minY, maxY, maxAbsX float32 = 1e9, -1e9, 0
	for _, v := range doc.Verts {
		if v.Y() < minY {
			minY = v.Y()
		}
		if v.Y() > maxY {
			maxY = v.Y()
		}
		if a := abs32(v.X()); a > maxAbsX {
			maxAbsX = a
		}
	}
	if minY > hullBottom+1e-4 {
		t.Errorf("nothing reaches the belly: lowest vertex %v, want %v or below", minY, hullBottom)
	}
	if maxY < hullTop {
		t.Errorf("nothing reaches the roof: highest vertex %v, want at least %v", maxY, hullTop)
	}
	if maxAbsX < hullHalf {
		t.Errorf("the hull is narrower than the hold: %v, want at least %v", maxAbsX, hullHalf)
	}
}

// No face may lie inside the hold, the doorway or the vestibule. Those three
// volumes are the chunk's, drawn by its own floors, walls and ceilings, and the
// hull is only the skin over them — a face in between is a plate hanging in the
// middle of a room the player is standing in, lit and solid-looking and
// attached to nothing.
//
// The test is on each face's extent rather than on its corners, because the
// mistake this catches is a face that CROSSES the room rather than one contained
// in it: the fuselage is built as three stacked bands, and the pair of faces
// where two bands meet spans the whole hull. Put that seam at deck level and
// every room gets a plate through it, with all four corners comfortably outside.
//
// So: a face lying wholly within a room's extent on any ONE axis, while
// overlapping it on the other two, is a face across that room — a plate over the
// deck, a plate down the doorway, a plate along a wall. A face reaching past the
// room on every axis is the hull's own skin going by outside it, which is what
// every flank and every bulkhead does.
func TestNoHullFaceIsInsideTheHold(t *testing.T) {
	// The three interior volumes of resources/chunks/dropship_hold.yaml, each
	// shrunk by a centimetre so a face lying exactly on a boundary — the deck,
	// the ceiling, a wall — is outside rather than in.
	const e = 0.01
	rooms := []struct {
		name                   string
		x0, x1, y0, y1, z0, z1 float32
	}{
		{"the hold", -bayHalf + e, bayHalf - e, groundLine + e, bayCeil - e, 0 + e, bayFore - e},
		{"the doorway", -1.4 + e, 1.4 - e, -0.06 + e, 2.4 - e, -0.3 + e, 0 - e},
		{"the vestibule", -vestHalf + e, vestHalf - e, vestDeck + e, vestCeil - e, vestAft + e, -0.3 - e},
	}

	overlaps := func(a0, a1, b0, b1 float32) bool { return a0 < b1 && a1 > b0 }
	within := func(a0, a1, b0, b1 float32) bool { return a0 >= b0 && a1 <= b1 }

	doc := dropshipHull()
	for fi := range doc.Faces {
		f := &doc.Faces[fi]
		lo, hi := doc.Verts[f.Verts[0]], doc.Verts[f.Verts[0]]
		for _, vi := range f.Verts[1:] {
			v := doc.Verts[vi]
			lo = mgl32.Vec3{minf(lo.X(), v.X()), minf(lo.Y(), v.Y()), minf(lo.Z(), v.Z())}
			hi = mgl32.Vec3{maxf(hi.X(), v.X()), maxf(hi.Y(), v.Y()), maxf(hi.Z(), v.Z())}
		}
		for _, r := range rooms {
			box := [3][2]float32{{r.x0, r.x1}, {r.y0, r.y1}, {r.z0, r.z1}}
			for a := 0; a < 3; a++ {
				b, c := (a+1)%3, (a+2)%3
				if within(lo[a], hi[a], box[a][0], box[a][1]) &&
					overlaps(lo[b], hi[b], box[b][0], box[b][1]) &&
					overlaps(lo[c], hi[c], box[c][0], box[c][1]) {
					t.Errorf("face %d (part %d) cuts across %s on axis %d: %v..%v",
						fi, fi/6, r.name, a, lo, hi)
				}
			}
		}
	}
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// The nose and the engine pods have to CLEAR the ground the ship parks on. The
// underfloor is meant to be buried — the deck is at ground level and the
// structure under it is simply hidden by the floor the ship stands on — but a
// chin turret or a nacelle down there reads as a ship that has sunk into the
// dirt.
//
// Stated as a rule about regions rather than about part numbers, which shift
// the moment anything is added to the hull.
func TestTheNoseAndEnginesClearTheGround(t *testing.T) {
	doc := dropshipHull()
	for _, r := range []struct {
		name string
		in   func(v mgl32.Vec3) bool
	}{
		{"forward of the cockpit", func(v mgl32.Vec3) bool { return v.Z() >= 8.5 }},
		{"outboard on the pylons", func(v mgl32.Vec3) bool { return abs32(v.X()) >= 3.5 }},
	} {
		for i, v := range doc.Verts {
			if r.in(v) && v.Y() <= groundLine {
				t.Errorf("vertex %d %s is at %v, at or below the ground at %v",
					i, r.name, v.Y(), groundLine)
			}
		}
	}
}
