package world

import (
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// hillyTerrain is 40x16x36 blocks of generated hills with a cave cut
// through them and an overhang — two sections along X and Z, so faces near
// the section borders are built by both sides' neighbours.
func hillyTerrain(smooth bool) *Terrain {
	t := NewTerrain(mgl32.Vec3{-4, 1, -6}, 1.5, 40, 16, 36)
	t.Generate(TerrainFill{Height: 7, Hills: 4, HillSize: 9, Seed: 3, Top: 1, Under: 2, Deep: 3, UnderDepth: 2})
	for x := 5; x < 30; x++ {
		for y := 2; y < 4; y++ {
			t.SetBlock(x, y, 17, 0)
			t.SetBlock(x, y, 18, 0)
		}
	}
	for x := 20; x < 26; x++ {
		t.SetBlock(x, 13, 10, 3)
	}
	t.Smooth = smooth
	t.Touch()
	return t
}

func allFaces(t *Terrain) []*TerrainFace {
	var out []*TerrainFace
	for _, s := range t.Sections() {
		for i := range s.Faces {
			out = append(out, &s.Faces[i])
		}
	}
	return out
}

func eachMode(t *testing.T, fn func(t *testing.T, smooth bool)) {
	for _, smooth := range []bool{false, true} {
		name := "blocks"
		if smooth {
			name = "smooth"
		}
		t.Run(name, func(t *testing.T) { fn(t, smooth) })
	}
}

func TestTerrainSingleBlockHasSixOutwardFaces(t *testing.T) {
	tr := NewTerrain(mgl32.Vec3{}, 1, 3, 3, 3)
	tr.SetBlock(1, 1, 1, 2)
	faces := allFaces(tr)
	if len(faces) != 6 {
		t.Fatalf("%d faces, want 6", len(faces))
	}
	for _, f := range faces {
		_, _, _, d := tr.FaceBlock(f.Key)
		tri := f.Triangle(0)
		n := tri[1].Sub(tri[0]).Cross(tri[2].Sub(tri[0])).Normalize()
		want := mgl32.Vec3{float32(TerrainDirs[d][0]), float32(TerrainDirs[d][1]), float32(TerrainDirs[d][2])}
		if n.Sub(want).Len() > 1e-5 {
			t.Fatalf("face %d winds to %v, looks %v", d, n, want)
		}
		if f.Weights[0] != 255<<8 {
			t.Fatalf("material 2 should be all layer 1, got %08x", f.Weights[0])
		}
	}
}

// Every edge of the surface is shared by exactly two faces: the surface is
// closed, in both views, including across section borders — which is only
// true if a face's corner comes out the same whichever section builds it.
func TestTerrainSurfaceIsClosed(t *testing.T) {
	eachMode(t, func(t *testing.T, smooth bool) {
		tr := hillyTerrain(smooth)
		type edge struct{ a, b [3]int32 }
		q := func(p mgl32.Vec3) [3]int32 {
			return [3]int32{int32(math.Round(float64(p[0]) * 4096)), int32(math.Round(float64(p[1]) * 4096)), int32(math.Round(float64(p[2]) * 4096))}
		}
		count := map[edge]int{}
		for _, f := range allFaces(tr) {
			for i := 0; i < 4; i++ {
				a, b := q(f.Corners[i]), q(f.Corners[(i+1)%4])
				if a == b {
					continue
				}
				// Directed: a closed, consistently wound surface uses each
				// edge once each way.
				count[edge{a, b}]++
			}
		}
		for e, n := range count {
			back := count[edge{e.b, e.a}]
			if n != back {
				t.Fatalf("edge %v used %d times one way and %d the other", e, n, back)
			}
		}
	})
}

func TestTerrainSmoothSurfaceFacesOutward(t *testing.T) {
	tr := hillyTerrain(true)
	bad, total, badShade := 0, 0, 0
	faces := allFaces(tr)
	for _, f := range faces {
		_, _, _, d := tr.FaceBlock(f.Key)
		want := mgl32.Vec3{float32(TerrainDirs[d][0]), float32(TerrainDirs[d][1]), float32(TerrainDirs[d][2])}
		for k := 0; k < 2; k++ {
			tri := f.Triangle(k)
			n := tri[1].Sub(tri[0]).Cross(tri[2].Sub(tri[0]))
			if n.Len() < 1e-9 {
				continue
			}
			total++
			if n.Dot(want) <= 0 {
				bad++
			}
		}
		// A corner is shared with faces looking other ways, and the side of a
		// one-block bump is smoothed nearly flat, so a face's normals may
		// lean a little away from it; they must not point back into it.
		var avg mgl32.Vec3
		for _, n := range f.Normals {
			avg = avg.Add(n)
		}
		if avg.Normalize().Dot(want) < -0.5 {
			badShade++
		}
	}
	// Thin features (the one-block ledge) fold their normals; the rest of
	// the ground must not.
	if badShade*100 > len(faces) {
		t.Fatalf("%d of %d faces shade as if facing into their solid", badShade, len(faces))
	}
	if bad*50 > total {
		t.Fatalf("%d of %d smooth triangles face into their solid", bad, total)
	}
}

// Rebuilding only the dirtied sections must give what a rebuild from
// scratch gives.
func TestTerrainIncrementalRebuildMatchesFull(t *testing.T) {
	eachMode(t, func(t *testing.T, smooth bool) {
		tr := hillyTerrain(smooth)
		tr.Sections()
		r := rand.New(rand.NewSource(11))
		for i := 0; i < 60; i++ {
			x, y, z := r.Intn(tr.SizeX), r.Intn(tr.SizeY), r.Intn(tr.SizeZ)
			tr.SetBlock(x, y, z, uint8(r.Intn(5)))
		}
		// And paint, which rebuilds less than a change of shape does: a
		// biome that changes the ground's material and tint.
		tr.Biomes = []TerrainBiome{{Preset: "forest", GroundLayer: 4}}
		tr.Touch()
		tr.Sections()
		for _, f := range allFaces(tr) {
			if r.Intn(7) == 0 {
				tr.SetPaint(f.Key, 1)
			}
		}
		got := allFaces(tr)
		fresh := tr.Clone()
		want := allFaces(fresh)
		if len(got) != len(want) {
			t.Fatalf("incremental %d faces, full %d", len(got), len(want))
		}
		byKey := map[int32]*TerrainFace{}
		for _, f := range want {
			byKey[f.Key] = f
		}
		for _, f := range got {
			w := byKey[f.Key]
			if w == nil {
				t.Fatalf("face %d only in the incremental build", f.Key)
			}
			if f.Corners != w.Corners || f.Weights != w.Weights || f.Cover != w.Cover || f.Normals != w.Normals {
				t.Fatalf("face %d differs: %v vs %v", f.Key, f.Corners, w.Corners)
			}
		}
	})
}

func TestTerrainRaycastMatchesBruteForce(t *testing.T) {
	eachMode(t, func(t *testing.T, smooth bool) {
		tr := hillyTerrain(smooth)
		faces := allFaces(tr)
		lo, hi := tr.LocalBounds()
		r := rand.New(rand.NewSource(5))
		rp := func() mgl32.Vec3 {
			return mgl32.Vec3{
				lo[0] + r.Float32()*(hi[0]-lo[0]),
				lo[1] + r.Float32()*(hi[1]-lo[1])*1.4,
				lo[2] + r.Float32()*(hi[2]-lo[2]),
			}
		}
		hits := 0
		for i := 0; i < 1500; i++ {
			o := rp()
			if i%3 == 0 {
				o[1] = hi[1] + 5
			}
			d := rp().Sub(o).Normalize()
			got, ok := tr.Raycast(o, d, 200)
			best := float32(math.Inf(1))
			for _, f := range faces {
				for k := 0; k < 2; k++ {
					tri := f.Triangle(k)
					if tt, hit := rayTriangle(o, d, tri[0], tri[1], tri[2]); hit && tt >= 0 && tt <= 200 && tt < best {
						best = tt
					}
				}
			}
			if math.IsInf(float64(best), 1) {
				if ok {
					t.Fatalf("ray %v %v hit at %v; brute force misses", o, d, got.T)
				}
				continue
			}
			hits++
			if !ok || math.Abs(float64(got.T-best)) > 1e-3 {
				t.Fatalf("ray %v %v: got %v (%v), brute force %v", o, d, got.T, ok, best)
			}
		}
		if hits < 300 {
			t.Fatalf("only %d rays hit; the test is not exercising much", hits)
		}
	})
}

func TestTerrainGroundBelowSeesThroughCaveRoof(t *testing.T) {
	tr := NewTerrain(mgl32.Vec3{}, 1, 4, 10, 4)
	for x := 0; x < 4; x++ {
		for z := 0; z < 4; z++ {
			tr.SetBlock(x, 0, z, 1)
			tr.SetBlock(x, 5, z, 1)
		}
	}
	// From above the roof: the roof's top.
	if y, n, ok := tr.GroundBelow(mgl32.Vec3{2, 9, 2}, 20); !ok || math.Abs(float64(y-6)) > 1e-4 || n.Y() < 0.99 {
		t.Fatalf("above roof: %v %v %v", y, n, ok)
	}
	// Inside the cave: the floor, not the roof overhead.
	if y, _, ok := tr.GroundBelow(mgl32.Vec3{2, 3, 2}, 20); !ok || math.Abs(float64(y-1)) > 1e-4 {
		t.Fatalf("in cave: %v %v", y, ok)
	}
}

func TestTerrainPaintFollowsFaces(t *testing.T) {
	tr := NewTerrain(mgl32.Vec3{}, 1, 4, 4, 4)
	tr.Biomes = []TerrainBiome{{Preset: "meadow"}}
	tr.SetBlock(1, 1, 1, 1)
	top := tr.FaceKey(1, 1, 1, FaceYPos)
	side := tr.FaceKey(1, 1, 1, FaceXPos)
	if !tr.SetPaint(top, 1) || !tr.SetPaint(side, 1) {
		t.Fatal("painting an existing face failed")
	}
	if tr.SetPaint(tr.FaceKey(0, 0, 0, FaceYPos), 1) {
		t.Fatal("painted a face of an air block")
	}
	// A block placed against the side covers it, and takes the paint.
	tr.SetBlock(2, 1, 1, 1)
	if tr.PaintOf(side) != 0 {
		t.Fatal("a covered face kept its paint")
	}
	if tr.PaintOf(top) != 1 {
		t.Fatal("an uncovered face lost its paint")
	}
	// Mining the block takes its paint with it.
	tr.SetBlock(1, 1, 1, 0)
	if len(tr.Paint) != 0 {
		t.Fatalf("paint left behind: %v", tr.Paint)
	}
}

func TestTerrainRawRoundTrip(t *testing.T) {
	tr := hillyTerrain(true)
	tr.Layers = [4]string{"grass", "dirt", "stone", ""}
	tr.Biomes = []TerrainBiome{{Preset: "meadow"}, {Preset: "forest", Density: 1.4, Tint: mgl32.Vec3{1, 0.9, 0.8}, GroundLayer: 2,
		Amounts: map[string]float32{"broadleaf_tree": 0, "grass": 2.5}}}
	tr.FoliageSeed = 77
	for _, f := range allFaces(tr) {
		if f.Key%5 == 0 {
			tr.SetPaint(f.Key, uint8(1+f.Key%2))
		}
	}
	back, err := TerrainFromRaw(TerrainToRaw(tr))
	if err != nil {
		t.Fatal(err)
	}
	if string(back.Blocks) != string(tr.Blocks) || back.Smooth != tr.Smooth || back.Layers != tr.Layers ||
		back.FoliageSeed != tr.FoliageSeed || len(back.Biomes) != 2 || !reflect.DeepEqual(back.Biomes[1], tr.Biomes[1]) {
		t.Fatalf("terrain did not round-trip: %+v vs %+v", back.Biomes, tr.Biomes)
	}
	if len(back.Paint) != len(tr.Paint) || len(tr.Paint) == 0 {
		t.Fatalf("paint %d -> %d", len(tr.Paint), len(back.Paint))
	}
	for k, v := range tr.Paint {
		if back.Paint[k] != v {
			t.Fatalf("face %d painted %d, came back %d", k, v, back.Paint[k])
		}
	}
}

func paintedMeadow(smooth bool) *Terrain {
	tr := NewTerrain(mgl32.Vec3{}, 1, 32, 16, 32)
	tr.Biomes = []TerrainBiome{{Preset: "meadow"}, {Preset: "forest"}}
	tr.Generate(TerrainFill{Height: 6, Hills: 3, HillSize: 10, Seed: 9, Top: 1, Under: 2, Deep: 3, UnderDepth: 2, Biome: 1})
	tr.Smooth = smooth
	tr.Touch()
	return tr
}

func TestFoliageGrowsAndIsDeterministic(t *testing.T) {
	eachMode(t, func(t *testing.T, smooth bool) {
		tr := paintedMeadow(smooth)
		s := tr.Sections()[0]
		a := tr.BuildFoliage(s)
		if a.Count < 500 {
			t.Fatalf("a 16x16 meadow grew only %d plants", a.Count)
		}
		shapes := map[FoliageShape]int{}
		for _, b := range a.Batches {
			shapes[b.Shape] += len(b.Instances)
			for i := 1; i < len(b.Instances); i++ {
				if b.Instances[i].Rank < b.Instances[i-1].Rank {
					t.Fatal("batch not sorted by rank")
				}
			}
			for _, in := range b.Instances {
				if in.Pos[0] < a.Lo[0] || in.Pos[1] < a.Lo[1] || in.Pos[2] < a.Lo[2] ||
					in.Pos[0] > a.Hi[0] || in.Pos[1] > a.Hi[1] || in.Pos[2] > a.Hi[2] {
					t.Fatal("instance outside the section's foliage box")
				}
			}
		}
		if shapes[FoliageGrass] == 0 || shapes[FoliageFlower] == 0 {
			t.Fatalf("meadow is missing grass or flowers: %v", shapes)
		}
		// An edit far away changes nothing here.
		tr.SetBlock(30, 12, 30, 1)
		b := tr.BuildFoliage(tr.Sections()[0])
		if b.Count != a.Count {
			t.Fatalf("a far edit changed the plant count %d -> %d", a.Count, b.Count)
		}
		for i := range a.Batches {
			for j := range a.Batches[i].Instances {
				if a.Batches[i].Instances[j] != b.Batches[i].Instances[j] {
					t.Fatal("a far edit moved a plant")
				}
			}
		}
	})
}

// A build with a rank cut is the full build with the cut ranks left out:
// every plant it keeps stands exactly where the full build put it, and a
// shape cut to 0 is not placed at all. The renderer builds distant ground
// this way and relies on the two agreeing.
func TestFoliageRankCutIsASubsetOfTheFullBuild(t *testing.T) {
	tr := paintedMeadow(false)
	s := tr.Sections()[0]
	full := tr.BuildFoliage(s)
	cut := tr.BuildFoliageFor(s, func(sh FoliageShape) float32 {
		if sh == FoliageGrass {
			return 0
		}
		return 0.3
	})
	want := map[[2]int][]FoliageInstance{}
	for _, b := range full.Batches {
		for _, in := range b.Instances {
			if b.Shape != FoliageGrass && in.Rank < 0.3 {
				k := [2]int{int(b.Shape), b.Variant}
				want[k] = append(want[k], in)
			}
		}
	}
	got := 0
	for _, b := range cut.Batches {
		if b.Shape == FoliageGrass {
			t.Fatal("a shape cut to 0 was placed")
		}
		w := want[[2]int{int(b.Shape), b.Variant}]
		if len(w) != len(b.Instances) {
			t.Fatalf("%v variant %d: cut build has %d, full build below the cut %d", b.Shape, b.Variant, len(b.Instances), len(w))
		}
		for i := range w {
			if w[i] != b.Instances[i] {
				t.Fatalf("%v: the cut build placed a plant differently", b.Shape)
			}
		}
		got += len(b.Instances)
	}
	if got == 0 || got >= full.Count/2 {
		t.Fatalf("cut build kept %d of %d", got, full.Count)
	}
}

// Ground plants grow on ground, not out of the side of a wall; and nothing
// grows on bare faces.
func TestFoliageRespectsSurfaceAndPaint(t *testing.T) {
	tr := NewTerrain(mgl32.Vec3{}, 1, 16, 16, 16)
	tr.Biomes = []TerrainBiome{{Preset: "meadow"}}
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			h := 4
			if x >= 8 {
				h = 10
			}
			for y := 0; y < h; y++ {
				tr.SetBlock(x, y, z, 1)
			}
		}
	}
	// Paint only the cliff face (x = 7 looks at the tall side's -X face).
	for y := 4; y < 10; y++ {
		for z := 0; z < 16; z++ {
			tr.SetPaint(tr.FaceKey(8, y, z, FaceXNeg), 1)
		}
	}
	f := tr.BuildFoliage(tr.Sections()[0])
	if f.Count == 0 {
		t.Fatal("a painted cliff grew nothing (no moss, no vines)")
	}
	for _, b := range f.Batches {
		switch b.Shape {
		case FoliageGrass, FoliageTallGrass, FoliageFlower, FoliageShrub:
			t.Fatalf("ground plant %v grew on a wall", b.Shape)
		}
	}
}

// A gentle hill built of blocks is one slope in smooth view, not a flight of
// flat shelves: the ground climbs steadily along it, never levelling off for
// a few blocks and then jumping — whether the steps are four columns apart
// or eight.
func TestSmoothTerrainHasNoTerraces(t *testing.T) {
	for _, period := range []int{4, 8} {
		tr := NewTerrain(mgl32.Vec3{}, 1, 64, 24, 32)
		for x := 0; x < 64; x++ {
			for z := 0; z < 32; z++ {
				for y := 0; y < 4+x/period; y++ {
					tr.SetBlock(x, y, z, 1)
				}
			}
		}
		tr.Smooth = true
		tr.Touch()
		ideal := 1 / float32(period)
		prev := float32(0)
		for x := float32(16); x <= 48; x += 0.5 {
			y, _, ok := tr.GroundBelow(mgl32.Vec3{x, 40, 16.3}, 60)
			if !ok {
				t.Fatalf("no ground at x=%v", x)
			}
			if x > 16 {
				if rise := (y - prev) / 0.5; rise < ideal*0.6 || rise > ideal*1.4 {
					t.Fatalf("steps %d apart: at x=%v the ground rises %.3f per block, want about %.3f — a terrace", period, x, rise, ideal)
				}
			}
			prev = y
		}
	}
}

// A tunnel mined one block wide and two high stays open in smooth view: the
// smoothing must not fill in what is narrower than itself.
func TestASmoothTunnelStaysOpen(t *testing.T) {
	for _, smoothing := range []float32{0, TerrainSmoothingMax} {
		tr := NewTerrain(mgl32.Vec3{}, 1, 24, 16, 24)
		tr.Generate(TerrainFill{Height: 12, Top: 1, Under: 1, Deep: 1})
		for x := 2; x < 22; x++ {
			tr.SetBlock(x, 5, 12, 0)
			tr.SetBlock(x, 6, 12, 0)
		}
		tr.Smooth, tr.Smoothing = true, smoothing
		tr.Touch()
		// Down the tunnel's length from inside it, nothing in the way...
		if h, ok := tr.Raycast(mgl32.Vec3{3, 6, 12.5}, mgl32.Vec3{1, 0, 0}, 17); ok {
			t.Fatalf("smoothing %v: the tunnel is blocked %v along", smoothing, h.T)
		}
		// ...and a floor to stand on inside it, under a roof.
		if y, _, ok := tr.GroundBelow(mgl32.Vec3{12, 6.5, 12.5}, 3); !ok || y < 4.5 || y > 5.5 {
			t.Fatalf("smoothing %v: tunnel floor at %v (%v)", smoothing, y, ok)
		}
	}
}

// Growing the grid towards negative coordinates must keep every block, and
// the paint on it, at the same place in the chunk.
func TestTerrainGrowToIncludeKeepsBlocksInPlace(t *testing.T) {
	ter := NewTerrain(mgl32.Vec3{1, 2, 3}, 0.5, 4, 3, 5)
	ter.Biomes = []TerrainBiome{{}}
	ter.SetBlock(1, 0, 2, 2)
	ter.SetPaint(ter.FaceKey(1, 0, 2, FaceYPos), 1)
	was := ter.BlockMin(1, 0, 2)

	shift, ok := ter.GrowToInclude([3]int{-2, 0, -1}, [3]int{5, 6, 2})
	if !ok {
		t.Fatal("grow refused")
	}
	if shift != [3]int{2, 0, 1} {
		t.Fatalf("shift %v", shift)
	}
	if ter.SizeX != 8 || ter.SizeY != 7 || ter.SizeZ != 6 {
		t.Fatalf("size %d %d %d", ter.SizeX, ter.SizeY, ter.SizeZ)
	}
	x, y, z := 1+shift[0], 0+shift[1], 2+shift[2]
	if ter.Block(x, y, z) != 2 {
		t.Fatalf("block moved: %d", ter.Block(x, y, z))
	}
	if got := ter.BlockMin(x, y, z); got.Sub(was).Len() > 1e-5 {
		t.Fatalf("block min %v, was %v", got, was)
	}
	if ter.PaintOf(ter.FaceKey(x, y, z, FaceYPos)) != 1 {
		t.Fatal("paint lost")
	}
	solid := 0
	for _, v := range ter.Blocks {
		if v != 0 {
			solid++
		}
	}
	if solid != 1 {
		t.Fatalf("%d solid blocks, want 1", solid)
	}

	if _, ok := ter.GrowToInclude([3]int{0, 0, 0}, [3]int{TerrainMaxSize, 0, 0}); ok {
		t.Fatal("grow past TerrainMaxSize accepted")
	}
}

// FillBox fills and carves a box, clips it to the grid, and drops paint
// from faces it covers.
func TestTerrainFillBox(t *testing.T) {
	ter := NewTerrain(mgl32.Vec3{}, 1, 6, 6, 6)
	ter.Biomes = []TerrainBiome{{}}
	ter.SetBlock(0, 0, 0, 1)
	ter.SetPaint(ter.FaceKey(0, 0, 0, FaceXPos), 1)
	ter.SetPaint(ter.FaceKey(0, 0, 0, FaceYPos), 1)

	if !ter.FillBox([3]int{1, 0, 0}, [3]int{9, 2, 1}, 3) {
		t.Fatal("fill changed nothing")
	}
	for z := 0; z < 6; z++ {
		for y := 0; y < 6; y++ {
			for x := 1; x < 6; x++ {
				want := uint8(0)
				if y <= 2 && z <= 1 {
					want = 3
				}
				if got := ter.Block(x, y, z); got != want {
					t.Fatalf("block %d,%d,%d = %d, want %d", x, y, z, got, want)
				}
			}
		}
	}
	if ter.PaintOf(ter.FaceKey(0, 0, 0, FaceXPos)) != 0 {
		t.Fatal("covered face kept its paint")
	}
	if ter.PaintOf(ter.FaceKey(0, 0, 0, FaceYPos)) != 1 {
		t.Fatal("open face lost its paint")
	}
	if ter.FillBox([3]int{1, 0, 0}, [3]int{5, 2, 1}, 3) {
		t.Fatal("refill reported a change")
	}
	if !ter.FillBox([3]int{2, 1, 0}, [3]int{3, 1, 0}, 0) || ter.Solid(2, 1, 0) || !ter.Solid(2, 0, 0) {
		t.Fatal("carve")
	}
	if ter.FillBox([3]int{7, 7, 7}, [3]int{9, 9, 9}, 1) {
		t.Fatal("box outside the grid changed something")
	}
}

// Each biome grows what it is known by on open, gently rolling ground: a
// preset whose signature species never takes is a tuning mistake, not a
// style.
func TestBiomesGrowTheirSignatureSpecies(t *testing.T) {
	want := map[string][]FoliageShape{
		"jungle":   {FoliageBroadleaf, FoliagePalm, FoliageJunglePlant, FoliageFern},
		"desert":   {FoliageCactus, FoliageDeadBush, FoliageRock},
		"beach":    {FoliagePalm, FoliageTallGrass, FoliagePebbles},
		"tundra":   {FoliageGrass, FoliageMoss, FoliageShrub},
		"badlands": {FoliageRock, FoliageDeadBush, FoliageCactus},
		"volcanic": {FoliageMoss, FoliageRock, FoliageFlower},
		"savannah": {FoliageTallGrass, FoliageAcacia, FoliageShrub},
	}
	for name, shapes := range want {
		t.Run(name, func(t *testing.T) {
			if FoliagePresetByName(name) == nil {
				t.Fatalf("no preset %q", name)
			}
			tr := NewTerrain(mgl32.Vec3{}, 1, 96, 16, 96)
			tr.Biomes = []TerrainBiome{{Preset: name}}
			tr.Generate(TerrainFill{Height: 6, Hills: 2, HillSize: 16, Seed: 4, Top: 1, Under: 2, Deep: 3, UnderDepth: 2, Biome: 1})
			tr.Touch()
			got := map[FoliageShape]int{}
			for _, s := range tr.Sections() {
				for _, b := range tr.BuildFoliage(s).Batches {
					got[b.Shape] += len(b.Instances)
				}
			}
			for _, sh := range shapes {
				if got[sh] == 0 {
					t.Errorf("%s grew no %v: %v", name, sh, got)
				}
			}
		})
	}
}
