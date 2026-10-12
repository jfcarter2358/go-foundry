package rocks

import (
	"math"
	"testing"
)

// closedByPosition reports whether every edge of m, matching vertices by
// position (the box UVs split them), is used by two triangles running along
// it in opposite directions.
func closedByPosition(m *Mesh) bool {
	ids := map[[3]float32]uint32{}
	id := make([]uint32, len(m.Positions))
	for i, p := range m.Positions {
		x, ok := ids[p]
		if !ok {
			x = uint32(len(ids))
			ids[p] = x
		}
		id[i] = x
	}
	type e struct{ uses, dir int32 }
	edges := map[uint64]e{}
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, b, c := id[m.Indices[t]], id[m.Indices[t+1]], id[m.Indices[t+2]]
		if a == b || b == c || a == c {
			continue
		}
		for _, ed := range [3][2]uint32{{a, b}, {b, c}, {c, a}} {
			lo, hi, d := ed[0], ed[1], int32(1)
			if lo > hi {
				lo, hi, d = hi, lo, -1
			}
			k := uint64(lo)<<32 | uint64(hi)
			x := edges[k]
			x.uses++
			x.dir += d
			edges[k] = x
		}
	}
	for _, x := range edges {
		if x.uses != 2 || x.dir != 0 {
			return false
		}
	}
	return len(edges) > 0
}

// Every preset makes a closed, coloured, box-mapped rock in its placement
// frame, near its triangle budget.
func TestEveryPresetMakesAPlaceableRock(t *testing.T) {
	for _, name := range PresetNames() {
		s := Defaults()
		s.Preset = name
		_, size, _, tris := PresetDoc(name)
		s.Triangles = min(tris, 12000)
		r, err := Generate(s, 7)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		m, v := r.Mesh, r.Meta
		t.Logf("%-11s %6d tris (meshed %6d at %.4f m), %.2fx%.2fx%.2f m, footprint %.2f, top %.2f, %v",
			name, m.TriangleCount(), r.Timings.MeshTriangles, r.Timings.Cell, v.Extent[0], v.Extent[1], v.Extent[2],
			v.FootprintRadius, v.Top, r.Timings.Total.Round(1e6))
		if n := m.TriangleCount(); n > s.Triangles*12/10 || n < s.Triangles/2 {
			t.Errorf("%s: %d triangles for a budget of %d", name, n, s.Triangles)
		}
		if !closedByPosition(m) {
			t.Errorf("%s: the mesh is not closed", name)
		}
		if len(m.Colors) != len(m.Positions) || len(m.UVs) != len(m.Positions) || len(m.Normals) != len(m.Positions) {
			t.Fatalf("%s: ragged arrays", name)
		}
		// The frame: base on y = 0, footprint centred; a back on z = 0.
		if v.Min[1] != 0 {
			t.Errorf("%s: base at y = %g", name, v.Min[1])
		}
		if name == "cliff_chunk" {
			if v.Back == nil || math.Abs(float64(v.Min[2])) > 1e-5 {
				t.Errorf("cliff_chunk: back %v, min z %g", v.Back, v.Min[2])
			}
		} else if c := (v.Min[0] + v.Max[0]) / 2; math.Abs(float64(c)) > 1e-3*float32ToF(v.Extent[0]) {
			t.Errorf("%s: footprint centred at x = %g", name, c)
		}
		if v.Volume <= 0 || v.FootprintRadius > v.Radius+1e-4 || v.Top > v.Height+1e-4 {
			t.Errorf("%s: volume %g, footprint %g of %g, top %g of %g", name, v.Volume, v.FootprintRadius, v.Radius, v.Top, v.Height)
		}
		// About the preset's size.
		big := float64(max(v.Extent[0], v.Extent[1], v.Extent[2]))
		if big < 0.5*size || big > 2*size {
			t.Errorf("%s: largest extent %g for a %g m preset", name, big, size)
		}
		// The bake varies: occlusion somewhere, colour somewhere.
		minA, maxA := 255, 0
		minR, maxR := 255, 0
		for _, c := range m.Colors {
			minA, maxA = min(minA, int(c[3])), max(maxA, int(c[3]))
			minR, maxR = min(minR, int(c[0])), max(maxR, int(c[0]))
		}
		if maxA-minA < 20 || maxR-minR < 20 {
			t.Errorf("%s: a flat bake (alpha %d..%d, red %d..%d)", name, minA, maxA, minR, maxR)
		}
	}
}

func float32ToF(v float32) float64 { return float64(v) }

// The same settings and seed make the same mesh on any number of workers;
// another seed makes another.
func TestGenerateIsDeterministic(t *testing.T) {
	s := Defaults()
	s.Triangles = 4000
	s.Workers = 1
	a, err := Generate(s, 3)
	if err != nil {
		t.Fatal(err)
	}
	s.Workers = 13
	b, err := Generate(s, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Mesh.Positions) != len(b.Mesh.Positions) || len(a.Mesh.Indices) != len(b.Mesh.Indices) {
		t.Fatalf("%d/%d vertices", len(a.Mesh.Positions), len(b.Mesh.Positions))
	}
	for i := range a.Mesh.Positions {
		if a.Mesh.Positions[i] != b.Mesh.Positions[i] || a.Mesh.Colors[i] != b.Mesh.Colors[i] || a.Mesh.UVs[i] != b.Mesh.UVs[i] {
			t.Fatalf("vertex %d differs", i)
		}
	}
	for i := range a.Mesh.Indices {
		if a.Mesh.Indices[i] != b.Mesh.Indices[i] {
			t.Fatalf("index %d differs", i)
		}
	}
	c, err := Generate(s, 4)
	if err != nil {
		t.Fatal(err)
	}
	if c.Meta.Extent == a.Meta.Extent {
		t.Error("another seed made the same rock")
	}
}

// Granite has no beds and weathers rounder than sandstone; the palettes
// differ.
func TestGraniteAndPalettes(t *testing.T) {
	s := Defaults()
	s.Triangles = 4000
	sand, err := Generate(s, 5)
	if err != nil {
		t.Fatal(err)
	}
	s.Lithology = "granite"
	gr, err := Generate(s, 5)
	if err != nil {
		t.Fatal(err)
	}
	mean := func(m *Mesh) (r, g, b float64) {
		for _, c := range m.Colors {
			r += float64(c[0])
			g += float64(c[1])
			b += float64(c[2])
		}
		n := float64(len(m.Colors))
		return r / n, g / n, b / n
	}
	sr, _, sb := mean(sand.Mesh)
	gr2, _, gb := mean(gr.Mesh)
	if !(sr-sb > 60) || !(math.Abs(gr2-gb) < 40) {
		t.Errorf("sandstone mean red-blue %.0f, granite %.0f: want warm and grey", sr-sb, gr2-gb)
	}
	if paletteFor(Settings{}, "navajo") == paletteFor(Settings{}, "wingate") {
		t.Error("two palettes are one")
	}
	c := paletteFor(Settings{Color: [3]float64{0.1, 0.2, 0.3}}, "custom")
	if c.base[2] != 0.3 {
		t.Errorf("custom palette base %v", c.base)
	}
}

func TestUnknownPresetIsAnError(t *testing.T) {
	s := Defaults()
	s.Preset = "menhir"
	if _, err := Generate(s, 1); err == nil {
		t.Fatal("an unknown preset made a rock")
	}
}
