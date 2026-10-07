package nav

// tiles_test.go — the tiled heightfield is the whole-level one, cut up and
// cached.

import (
	"fmt"
	"reflect"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// tiledLevel is a slab 100 m across — several tiles — with ramps and pillars
// straddling tile boundaries, in two chunks that overlap.
func tiledLevel() *world.Level {
	a := world.Chunk{ID: "a"}
	a.Brushes = append(a.Brushes, world.NewBoxBrush("ground", mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{50, 0.5, 50}, "c"))
	for i := -3; i <= 3; i++ {
		// Centred on multiples of 32 m, so each straddles a tile edge.
		a.Brushes = append(a.Brushes, world.NewBoxBrush(fmt.Sprintf("p%d", i),
			mgl32.Vec3{float32(i) * 32, 1.5, float32(i) * 7}, mgl32.Vec3{1.3, 1.5, 2.1}, "c"))
	}
	ramp := world.NewBoxBrush("ramp", mgl32.Vec3{31, 0.5, -20}, mgl32.Vec3{4, 0.5, 3}, "c")
	for i := range ramp.Verts {
		if ramp.Verts[i].Y() > 0.5 && ramp.Verts[i].X() > 31 {
			ramp.Verts[i] = ramp.Verts[i].Add(mgl32.Vec3{0, 1.2, 0})
		}
	}
	a.Brushes = append(a.Brushes, ramp)
	b := world.Chunk{ID: "b"}
	b.Brushes = append(b.Brushes, world.NewBoxBrush("deck", mgl32.Vec3{30, 3, 30}, mgl32.Vec3{10, 0.25, 10}, "c"))
	return &world.Level{Chunks: []world.Chunk{a, b}}
}

func TestTheTiledHeightfieldIsTheWholeOne(t *testing.T) {
	Invalidate()
	t.Cleanup(Invalidate)
	solids := gatherSolids(tiledLevel())
	tiled := buildHeightfield(solids, DefaultCellSize, MaxSlopeCos)
	if tiled.W <= tileColumns || tiled.D <= tileColumns {
		t.Fatalf("the fixture is %dx%d columns; it has to span several tiles", tiled.W, tiled.D)
	}
	whole := rasterise(solids, tiled.Origin, tiled.Cell, tiled.W, tiled.D, MaxSlopeCos)
	nonEmpty := 0
	for c := 0; c < whole.Columns(); c++ {
		a, b := whole.column(c), tiled.column(c)
		if len(a) > 0 {
			nonEmpty++
		}
		if len(a) != len(b) {
			t.Fatalf("column %d: %d spans whole, %d tiled", c, len(a), len(b))
		}
		for k := range a {
			if a[k] != b[k] {
				t.Fatalf("column %d span %d: whole %+v, tiled %+v", c, k, a[k], b[k])
			}
		}
	}
	if nonEmpty == 0 {
		t.Fatal("nothing was rasterised")
	}
}

func TestSwitchingAChunkOffRebuildsOnlyTheTilesItTouches(t *testing.T) {
	Invalidate()
	t.Cleanup(Invalidate)
	lvl := tiledLevel()
	buildHeightfield(gatherSolids(lvl), DefaultCellSize, MaxSlopeCos)
	first := tileCache.built

	buildHeightfield(gatherSolids(lvl), DefaultCellSize, MaxSlopeCos)
	if n := tileCache.built - first; n != 0 {
		t.Errorf("an unchanged level rasterised %d tiles again", n)
	}

	// The deck is 20 m square at (30, 30): it reaches one tile, or at most the
	// four around a tile corner, of the many the slab covers.
	lvl.Chunks[1].Disabled = true
	before := tileCache.built
	hf := buildHeightfield(gatherSolids(lvl), DefaultCellSize, MaxSlopeCos)
	rebuilt := tileCache.built - before
	if rebuilt == 0 || rebuilt > 4 {
		t.Errorf("switching the deck's chunk off rasterised %d tiles, want the 1-4 it covers (of %d built at first)", rebuilt, first)
	}
	c := hf.ColumnAt(30, 30)
	for _, sp := range hf.column(c) {
		if sp.max > 2 {
			t.Error("the switched-off deck is still in the heightfield")
		}
	}
}

// Rasterising the tiles across CPUs gives the serial build's heightfield,
// span for span.
func TestParallelTilesMatchSerial(t *testing.T) {
	t.Cleanup(Invalidate)
	t.Cleanup(func() { parallelTiles = true })
	for _, lvl := range []*world.Level{tiledLevel(), bigLevel()} {
		solids := gatherSolids(lvl)
		build := func(par bool) *Heightfield {
			parallelTiles = par
			resetTiles()
			return buildHeightfield(solids, DefaultCellSize, MaxSlopeCos)
		}
		ser, par := build(false), build(true)
		if ser.W != par.W || ser.D != par.D || ser.Origin != par.Origin || ser.Cell != par.Cell {
			t.Fatalf("shape differs: serial %dx%d at %v, parallel %dx%d at %v", ser.W, ser.D, ser.Origin, par.W, par.D, par.Origin)
		}
		if !reflect.DeepEqual(ser.colStart, par.colStart) || !reflect.DeepEqual(ser.spans, par.spans) {
			t.Fatal("parallel tiles rasterised a different heightfield")
		}
		if len(ser.spans) == 0 {
			t.Fatal("nothing was rasterised")
		}
	}
}
