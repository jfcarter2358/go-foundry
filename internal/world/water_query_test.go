package world

import (
	"testing"

	"foundry/internal/cluster"
	"foundry/internal/material"
	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

// waterTestMaterial registers a water material (flow-mapped when flow > 0)
// for the test's life.
func waterTestMaterial(t *testing.T, name string, flow float32) {
	t.Helper()
	d := &material.Doc{Name: name, Domain: material.DomainSurface}
	e := d.AddEffect(material.KindWater)
	e.Set("flow_map", material.FloatValue(flow))
	material.Put(d)
	t.Cleanup(func() { material.Remove(name) })
}

// waterTestMesh is a cooked-mesh stand-in: one quad from (0,0) to (s,s) at
// height y, every vertex carrying the flow colour (g, b).
func waterTestMesh(s, y float32, g, b uint8) *nanite.Mesh {
	col := uint32(255) | uint32(g)<<8 | uint32(b)<<16 | 255<<24
	return &nanite.Mesh{
		Slots:     []nanite.Slot{{Name: "water", First: 0, BaseCount: 1}},
		Clusters:  []nanite.Cluster{{Cluster: cluster.Cluster{TriFirst: 0, TriCount: 2}}},
		Vertices:  []float32{0, y, 0, 0, 0, s, y, 0, 1, 0, s, y, s, 1, 1, 0, y, s, 0, 1},
		Normals:   []float32{0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0},
		Indices:   []uint32{0, 1, 2, 0, 2, 3},
		HasColors: true,
		Colors:    []uint32{col, col, col, col},
		Lo:        mgl32.Vec3{0, y, 0}, Hi: mgl32.Vec3{s, y, s},
	}
}

func TestWaterAtFindsAGeneratedSurfaceAndItsFlow(t *testing.T) {
	waterTestMaterial(t, "wq_river", 1)
	lvl := &Level{}
	// A 10 m tile at y = 2 placed at (100, 0, 50); flow colour g = 255
	// (full +X), b = 128 (still Z).
	lvl.Scenery = append(lvl.Scenery, Scenery{ID: "pond", Material: "wq_river",
		Position: mgl32.Vec3{100, 0, 50}, Scale: mgl32.Vec3{1, 1, 1}, Nanite: waterTestMesh(10, 2, 255, 128)})
	InvalidateWaterQuery()

	w := WaterAt(lvl, mgl32.Vec3{105, 1.25, 55})
	if !w.Found || !w.In {
		t.Fatalf("under the tile: %+v, want in the water", w)
	}
	if d := w.Depth - 0.75; d*d > 1e-6 || w.Surface != 2 {
		t.Errorf("depth %v surface %v, want 0.75 under a surface at 2", w.Depth, w.Surface)
	}
	if w.Flow.X() < 3.9 || w.Flow.Y()*w.Flow.Y() > 0.01 {
		t.Errorf("flow %v, want ~(4, 0) m/s", w.Flow)
	}
	if w.Material != "wq_river" {
		t.Errorf("material %q", w.Material)
	}
	if a := WaterAt(lvl, mgl32.Vec3{105, 3, 55}); !a.Found || a.In || a.Depth >= 0 {
		t.Errorf("above the surface: %+v, want found, not in", a)
	}
	if o := WaterAt(lvl, mgl32.Vec3{90, 1, 55}); o.Found {
		t.Errorf("beside the tile: %+v, want nothing", o)
	}
	if deep := WaterAt(lvl, mgl32.Vec3{105, 2 - WaterQueryMaxDepth - 1, 55}); deep.In {
		t.Errorf("far below: %+v, want not in", deep)
	}
}

func TestWaterAtPicksTheSurfaceJustAboveAndReadsBrushWater(t *testing.T) {
	waterTestMaterial(t, "wq_still", 0)
	lvl := &Level{}
	// A lake tile at y = 10 and, under it, a brush pool whose top is at 0.
	lvl.Scenery = append(lvl.Scenery, Scenery{ID: "lake", Material: "wq_still",
		Scale: mgl32.Vec3{1, 1, 1}, Nanite: waterTestMesh(20, 10, 128, 128)})
	pool := NewBoxBrush("pool", mgl32.Vec3{10, -1, 10}, mgl32.Vec3{4, 1, 4}, "")
	for i := range pool.Faces {
		pool.Faces[i].Material = "wq_still"
	}
	lvl.Chunks = []Chunk{{ID: "c", Brushes: []Brush{pool}}}
	InvalidateWaterQuery()

	if w := WaterAt(lvl, mgl32.Vec3{10, -0.5, 10}); !w.In || w.Surface != 0 {
		t.Errorf("in the pool: %+v, want under its top at 0", w)
	}
	if w := WaterAt(lvl, mgl32.Vec3{10, 5, 10}); !w.In || w.Surface != 10 {
		t.Errorf("between: %+v, want under the lake at 10", w)
	}
	if w := WaterAt(lvl, mgl32.Vec3{18, 5, 18}); !w.In || w.Surface != 10 || w.Flow != (mgl32.Vec2{}) {
		t.Errorf("beside the pool: %+v, want the lake, still", w)
	}
	// A shot from above the lake down into it crosses at its surface.
	x, ok := WaterCrossing(lvl, mgl32.Vec3{2, 20, 2}, mgl32.Vec3{12, 0.5, 12})
	if !ok || x.Y() != 10 {
		t.Errorf("crossing %v %v, want at y 10", x, ok)
	}
	if _, ok := WaterCrossing(lvl, mgl32.Vec3{30, 20, 2}, mgl32.Vec3{40, 15, 2}); ok {
		t.Errorf("a shot beside the water crossed it")
	}
}

func TestWaterQueryLeavesFallingSheetsOut(t *testing.T) {
	d := &material.Doc{Name: "wq_falls", Domain: material.DomainSurface}
	d.AddEffect(material.KindWater).Set("falls", material.FloatValue(1))
	material.Put(d)
	t.Cleanup(func() { material.Remove(d.Name) })
	lvl := &Level{}
	lvl.Scenery = append(lvl.Scenery, Scenery{ID: "sheet", Material: "wq_falls",
		Scale: mgl32.Vec3{1, 1, 1}, Nanite: waterTestMesh(5, 1, 128, 128)})
	InvalidateWaterQuery()
	if LevelHasWater(lvl) {
		t.Fatal("a falls sheet counted as a water surface")
	}
}

// waterTestStrip is a ribbon running along +X across z ∈ [0, w]: one quad
// per pair of (x, y) stations, every vertex still (flow colour 128, 128).
func waterTestStrip(w float32, xy ...[2]float32) *nanite.Mesh {
	col := uint32(255) | uint32(128)<<8 | uint32(128)<<16 | 255<<24
	m := &nanite.Mesh{Slots: []nanite.Slot{{Name: "water", First: 0, BaseCount: 1}}, HasColors: true}
	lo := mgl32.Vec3{xy[0][0], xy[0][1], 0}
	hi := lo
	for i, p := range xy {
		for _, z := range [2]float32{0, w} {
			m.Vertices = append(m.Vertices, p[0], p[1], z, 0, 0)
			m.Normals = append(m.Normals, 0, 1, 0)
			m.Colors = append(m.Colors, col)
			lo = mgl32.Vec3{min(lo.X(), p[0]), min(lo.Y(), p[1]), 0}
			hi = mgl32.Vec3{max(hi.X(), p[0]), max(hi.Y(), p[1]), w}
		}
		if i > 0 {
			a := uint32(2 * (i - 1))
			m.Indices = append(m.Indices, a, a+1, a+3, a, a+3, a+2)
		}
	}
	n := uint32(len(m.Indices) / 3)
	m.Clusters = []nanite.Cluster{{Cluster: cluster.Cluster{TriFirst: 0, TriCount: n}}}
	m.Lo, m.Hi = lo, hi
	return m
}

// Standing in the air below a fall must not count as under water: the
// ribbon's step down the fall (lip at 5, foot at 2 over a metre) is not a
// surface over the plunge pool (fixed after phase W5).
func TestWaterAtBelowAFallIsNotUnderTheLip(t *testing.T) {
	waterTestMaterial(t, "wq_fall_river", 1)
	lvl := &Level{}
	lvl.Scenery = append(lvl.Scenery, Scenery{ID: "ribbon", Material: "wq_fall_river", Scale: mgl32.Vec3{1, 1, 1},
		Nanite: waterTestStrip(6, [2]float32{0, 5}, [2]float32{10, 5}, [2]float32{11, 2}, [2]float32{30, 1.8})})
	InvalidateWaterQuery()
	// An eye 1.6 m over the pool, right at the foot of the step (under where
	// the step's strip passes, 4.1 m up) and a little further out.
	for _, x := range []float32{10.3, 10.8, 11.5, 14} {
		if w := WaterAt(lvl, mgl32.Vec3{x, 3.6, 3}); w.In {
			t.Errorf("eye at x %.1f, 1.6 m over the pool: %+v, want not under water", x, w)
		}
	}
	// Still in the river above the fall and in the pool below it.
	if w := WaterAt(lvl, mgl32.Vec3{5, 4, 3}); !w.In || w.Surface != 5 {
		t.Errorf("in the river above the lip: %+v", w)
	}
	if w := WaterAt(lvl, mgl32.Vec3{14, 1, 3}); !w.In || w.Surface < 1.8 || w.Surface > 2 {
		t.Errorf("in the plunge pool: %+v", w)
	}
}
