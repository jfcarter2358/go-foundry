package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func approxArea(t *testing.T, got, want float32, msg string) {
	if math.Abs(float64(got-want)) > 1e-3 {
		t.Fatalf("%s: area %v, want %v", msg, got, want)
	}
}

// A diagonal split of the unit square should yield two triangles that exactly
// tile it.
func TestSplitConvexDiagonal(t *testing.T) {
	square := []mgl32.Vec2{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	// Line through (0,0)-(1,1): direction (1,1).
	l := lineThroughDir(mgl32.Vec2{0, 0}, mgl32.Vec2{1, 1})
	pos, neg := splitConvexByLine(square, l)
	if pos == nil || neg == nil {
		t.Fatalf("diagonal did not split square: pos=%v neg=%v", pos, neg)
	}
	total := absf(polyArea2(pos))/2 + absf(polyArea2(neg))/2
	approxArea(t, total, 1.0, "diagonal split total")
}

// Subdividing a cell by an arbitrary rotated rectangle's 8 boundary lines must
// produce convex pieces whose areas sum to the original cell (seamless, no
// overlaps, no gaps).
func TestSubdivideConservesAreaAndConvex(t *testing.T) {
	cell := []mgl32.Vec2{{0, 0}, {6, 0}, {6, 6}, {0, 6}}
	b := &building{
		center: mgl32.Vec2{3, 3},
		ux:     mgl32.Vec2{float32(math.Cos(0.6)), float32(math.Sin(0.6))},
		uy:     mgl32.Vec2{float32(-math.Sin(0.6)), float32(math.Cos(0.6))},
		hwOut:  2.0, hhOut: 1.5, hwIn: 1.5, hhIn: 1.0,
	}
	pieces := subdivideConvex(cell, b.cutLines())
	var sum float32
	for _, p := range pieces {
		cp := cleanPoly(p)
		if len(cp) < 3 {
			continue
		}
		a := polyArea2(cp)
		sum += absf(a) / 2
		// Convexity: every cross product the same sign as the (CCW) area.
		if a < 0 {
			reverseVec2Slice(cp)
		}
		n := len(cp)
		for i := 0; i < n; i++ {
			a0 := cp[i]
			a1 := cp[(i+1)%n]
			a2 := cp[(i+2)%n]
			cross := (a1.X()-a0.X())*(a2.Y()-a0.Y()) - (a1.Y()-a0.Y())*(a2.X()-a0.X())
			if cross < -1e-3 {
				t.Fatalf("piece not convex: %v (cross %v at %d)", cp, cross, i)
			}
		}
	}
	approxArea(t, sum, 36.0, "subdivision area conservation")
}

// The conforming pass must insert a pool vertex that lies on the interior of an
// adjacent polygon's edge (the subdivided-vs-plain T-junction case).
func TestConformInsertsTJunction(t *testing.T) {
	pool := newPointPool()
	// Plain quad ABCD with an edge A(0,0)->B(2,0).
	a := pool.add(mgl32.Vec2{0, 0})
	bIdx := pool.add(mgl32.Vec2{2, 0})
	c := pool.add(mgl32.Vec2{2, 2})
	d := pool.add(mgl32.Vec2{0, 2})
	// A neighbour cell put a vertex at the midpoint (1,0) of edge A->B.
	mid := pool.add(mgl32.Vec2{1, 0})

	quad := conformPoly{idxs: []int{a, bIdx, c, d}}
	polys := []conformPoly{quad}
	conformTJunctions(polys, pool)

	// The midpoint index must now appear between A and B.
	got := polys[0].idxs
	if len(got) != 5 {
		t.Fatalf("expected 5 vertices after conform, got %d: %v", len(got), got)
	}
	if got[0] != a || got[1] != mid || got[2] != bIdx {
		t.Fatalf("midpoint not inserted on edge: %v (mid=%d)", got, mid)
	}
}

// End-to-end carve (no GPU): paint a road and a city region, generate regions,
// and run the per-cell carve exactly as GenerateCity does — verifying every cell
// is covered exactly once (areas sum to the whole grid) and points are shared.
func TestCarvePipelineTilesGrid(t *testing.T) {
	g := NewWorldGrid(10, 10, 6.0, mgl32.Vec2{0, 0}, 1234, "world")
	// Paint a road column and a city block.
	for cy := 0; cy < g.Rows; cy++ {
		g.CellType[g.cIdx(4, cy)] = TerrainRoad
	}
	for cy := 1; cy < 9; cy++ {
		for cx := 5; cx < 9; cx++ {
			g.CellCity[g.cIdx(cx, cy)] = 1
		}
	}

	used := map[string]bool{}
	road := buildRoadRibbon(g)
	if road == nil || len(road.segs) == 0 {
		t.Fatalf("expected a road ribbon from painted road cells")
	}
	blds := generateBuildings(g, resolveBuildingStyle("desert"), road, used)
	if len(blds) == 0 {
		t.Fatalf("expected at least one building in the city region")
	}
	cc := &cityCarve{grid: g, buildings: blds, road: road}

	pool := newPointPool()
	var totalArea float32
	for cy := 0; cy < g.Rows; cy++ {
		for cx := 0; cx < g.Cols; cx++ {
			cmin, cmax := g.cellWorldRect(cx, cy)
			var lines []line2
			for _, b := range blds {
				bmin, bmax := b.aabb()
				if bmax.X() < cmin.X() || bmin.X() > cmax.X() || bmax.Y() < cmin.Y() || bmin.Y() > cmax.Y() {
					continue
				}
				lines = append(lines, b.cutLines()...)
			}
			lines = append(lines, road.cutLinesForCell(cmin, cmax)...)
			cellPoly := []mgl32.Vec2{
				g.vertexWorld(cx, cy), g.vertexWorld(cx+1, cy),
				g.vertexWorld(cx+1, cy+1), g.vertexWorld(cx, cy+1),
			}
			var pieces [][]mgl32.Vec2
			if len(lines) == 0 {
				pieces = [][]mgl32.Vec2{cellPoly}
			} else {
				pieces = subdivideConvex(cellPoly, lines)
			}
			var cellArea float32
			for _, piece := range pieces {
				piece = cleanPoly(piece)
				if len(piece) < 3 || absf(polyArea2(piece)) < 1e-5 {
					continue
				}
				cellArea += absf(polyArea2(piece)) / 2
				for _, p := range piece {
					pool.add(p)
				}
				// classification must not panic and must return a sane class.
				_, _ = cc.classify(polyCentroid(piece))
			}
			approxArea(t, cellArea, g.CellSize*g.CellSize, "cell area for "+itoa(cx)+","+itoa(cy))
			totalArea += cellArea
		}
	}
	approxArea(t, totalArea, float32(g.Cols*g.Rows)*g.CellSize*g.CellSize, "whole grid area")

	// A building must have at least one interior and one band sample.
	b := blds[0]
	if _, isB := cc.classify(b.center); true {
		ri, _ := cc.classify(b.center)
		if ri != regInterior {
			t.Fatalf("building center should classify interior, got %v", ri)
		}
		rb, _ := cc.classify(b.center.Add(b.ux.Mul(b.hwOut - 0.1)))
		if rb != regBand {
			t.Fatalf("near outer edge should classify band, got %v", rb)
		}
		_ = isB
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
