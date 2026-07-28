package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func approxf(a, b, eps float32) bool { return absf(a-b) <= eps }

func testPalette() []BuildingTemplate {
	return []BuildingTemplate{
		{Name: "A", Width: 6, Depth: 6, Stories: 1, StoryHeight: 4, WallThick: 0.5, Parapet: 0.5, DoorWidth: 1.8, Enterable: true, WallTex: "stucco", RoofTex: "dirt", FloorTex: "stone_floor", Weight: 1},
		{Name: "B", Width: 10, Depth: 8, Stories: 3, StoryHeight: 4, WallThick: 0.6, Parapet: 0.7, DoorWidth: 2.0, Enterable: true, WallTex: "brick", RoofTex: "dirt", FloorTex: "stone_floor", Weight: 1},
	}
}

func paintPaletteCity(g *WorldGrid) {
	for cy := 0; cy < g.Rows; cy++ {
		g.CellType[g.cIdx(5, cy)] = TerrainRoad
	}
	for cy := 2; cy < 14; cy++ {
		for cx := 6; cx < 13; cx++ {
			g.CellCity[g.cIdx(cx, cy)] = 1
		}
	}
}

// Placement must stamp palette templates (matching dimensions & textures), be
// deterministic, and rotate road-adjacent buildings to face the road.
func TestPaletteDrivenPlacement(t *testing.T) {
	g := NewWorldGrid(16, 16, 6, mgl32.Vec2{0, 0}, 99, "world")
	paintPaletteCity(g)
	g.Density = 0.6
	g.FaceRoads = true
	g.Palette = testPalette()

	road := buildRoadRibbon(g)
	if road == nil {
		t.Fatal("expected road ribbon")
	}
	blds := generateBuildings(g, resolveBuildingStyle("desert"), road, map[string]bool{})
	if len(blds) == 0 {
		t.Fatal("palette placement produced no buildings")
	}

	for _, b := range blds {
		matched := false
		for _, tpl := range g.Palette {
			if approxf(b.hwOut, tpl.Width/2, 0.02) && approxf(b.hhOut, tpl.Depth/2, 0.02) {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("building %vx%v matches no template", b.hwOut*2, b.hhOut*2)
		}
		if b.wallTex != "stucco" && b.wallTex != "brick" {
			t.Fatalf("building wall texture %q not from palette", b.wallTex)
		}
	}

	// Determinism.
	blds2 := generateBuildings(g, resolveBuildingStyle("desert"), buildRoadRibbon(g), map[string]bool{})
	if len(blds2) != len(blds) {
		t.Fatalf("non-deterministic palette placement: %d vs %d", len(blds), len(blds2))
	}

	// Road-facing: buildings near the road must have a face aligned to it.
	faced := 0
	near := 0
	for _, b := range blds {
		if d, ok := nearestRoadDir(road, b.center, g.CellSize*2.5); ok {
			near++
			if absf(b.uy.Dot(d)) > 0.85 {
				faced++
			}
		}
	}
	if near == 0 {
		t.Fatal("no buildings placed near the road")
	}
	if faced == 0 {
		t.Fatal("no road-adjacent building was rotated to face the road")
	}
}

// An empty palette must fall back to procedural placement (backward compat).
func TestEmptyPaletteFallsBackToProcedural(t *testing.T) {
	g := NewWorldGrid(16, 16, 6, mgl32.Vec2{0, 0}, 7, "world")
	paintPaletteCity(g)
	// no palette
	blds := generateBuildings(g, resolveBuildingStyle("desert"), buildRoadRibbon(g), map[string]bool{})
	if len(blds) == 0 {
		t.Fatal("procedural fallback produced no buildings")
	}
}

// A diagonally-painted road stroke must produce genuinely diagonal road geometry
// (segments that follow the stroke direction), not a grid-aligned staircase.
func TestDiagonalRoadStroke(t *testing.T) {
	g := NewWorldGrid(20, 20, 6, mgl32.Vec2{-60, -60}, 1, "world")
	g.Roads = []RoadStroke{{
		Points: []mgl32.Vec2{{-40, -40}, {-20, -20}, {0, 0}, {20, 20}, {40, 40}},
		Width:  4,
	}}

	rib := buildRoadRibbon(g)
	if rib == nil || len(rib.segs) == 0 {
		t.Fatal("no ribbon from road stroke")
	}
	// The ribbon follows the diagonal: a point on the line is road, a point far
	// off it is not.
	if !rib.contains(mgl32.Vec2{10, 10}) {
		t.Fatal("diagonal midpoint is not on the road ribbon")
	}
	if rib.contains(mgl32.Vec2{40, -40}) {
		t.Fatal("off-diagonal point wrongly classified as road")
	}
	// At least one segment must be genuinely diagonal (both dx and dz non-trivial).
	diagonal := false
	for _, s := range rib.segs {
		d := s.b.Sub(s.a)
		if absf(d.X()) > 0.5 && absf(d.Y()) > 0.5 {
			diagonal = true
			break
		}
	}
	if !diagonal {
		t.Fatal("road ribbon has no diagonal segment — still grid-aligned")
	}
}

// Strokes take priority over legacy cell-road tags.
func TestStrokeRoadsOverrideCellRoads(t *testing.T) {
	g := NewWorldGrid(12, 12, 6, mgl32.Vec2{0, 0}, 1, "world")
	for cy := 0; cy < g.Rows; cy++ {
		g.CellType[g.cIdx(3, cy)] = TerrainRoad // legacy cell road
	}
	g.Roads = []RoadStroke{{Points: []mgl32.Vec2{{10, 10}, {40, 40}}, Width: 4}}
	rib := buildRoadRibbon(g)
	// Stroke ribbon should contain the diagonal, not the vertical cell column.
	if !rib.contains(mgl32.Vec2{25, 25}) {
		t.Fatal("stroke ribbon missing")
	}
}

// instantiateBuilding must clamp an over-thick wall so the interior stays walkable.
func TestInstantiateClampsWall(t *testing.T) {
	g := NewWorldGrid(8, 8, 6, mgl32.Vec2{0, 0}, 1, "world")
	tpl := BuildingTemplate{Width: 5, Depth: 5, Stories: 1, StoryHeight: 4, WallThick: 4, Parapet: 0.5, DoorWidth: 1.8, Enterable: true}
	b := instantiateBuilding(tpl, mgl32.Vec2{0, 0}, mgl32.Vec2{1, 0}, mgl32.Vec2{0, 1}, g)
	if b.hwIn < 1.0 || b.hhIn < 1.0 {
		t.Fatalf("interior collapsed after wall clamp: hwIn=%v hhIn=%v", b.hwIn, b.hhIn)
	}
}
