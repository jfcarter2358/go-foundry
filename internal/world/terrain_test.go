package world

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func bumpyTerrain() *Terrain {
	t := NewTerrain(mgl32.Vec3{-4, 1, -6}, 1.5, 8, 10)
	r := rand.New(rand.NewSource(7))
	for i := range t.Heights {
		t.Heights[i] = r.Float32()*4 - 2
	}
	return t
}

func TestTerrainTrianglesFaceUp(t *testing.T) {
	tr := bumpyTerrain()
	for z := 0; z < tr.CellsZ; z++ {
		for x := 0; x < tr.CellsX; x++ {
			for _, tri := range tr.CellTriangles(x, z) {
				n := tri[1].Sub(tri[0]).Cross(tri[2].Sub(tri[0]))
				if n.Y() <= 0 {
					t.Fatalf("cell %d,%d triangle faces down: %v", x, z, n)
				}
			}
		}
	}
}

// HeightAt must lie on the same triangles CellTriangles draws — the property
// that keeps what a body stands on and what is drawn the same surface.
func TestTerrainHeightAtMatchesDrawnTriangles(t *testing.T) {
	tr := bumpyTerrain()
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 2000; i++ {
		lx := tr.Origin.X() + r.Float32()*float32(tr.CellsX)*tr.CellSize
		lz := tr.Origin.Z() + r.Float32()*float32(tr.CellsZ)*tr.CellSize
		y, n, ok := tr.HeightAt(lx, lz)
		if !ok {
			t.Fatalf("no ground at %v,%v", lx, lz)
		}
		d, hn, hit := tr.Raycast(mgl32.Vec3{lx, 100, lz}, mgl32.Vec3{0, -1, 0}, 1000)
		if !hit {
			t.Fatalf("ray missed at %v,%v", lx, lz)
		}
		if got := 100 - d; math.Abs(float64(got-y)) > 1e-3 {
			t.Fatalf("at %v,%v HeightAt %v but ray hit %v", lx, lz, y, got)
		}
		if n.Sub(hn).Len() > 1e-3 {
			t.Fatalf("at %v,%v HeightAt normal %v, ray normal %v", lx, lz, n, hn)
		}
	}
}

func TestTerrainRaycastSlantedMatchesBruteForce(t *testing.T) {
	tr := bumpyTerrain()
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 500; i++ {
		o := mgl32.Vec3{r.Float32()*30 - 15, r.Float32()*10 + 3, r.Float32()*30 - 15}
		d := mgl32.Vec3{r.Float32()*2 - 1, -r.Float32(), r.Float32()*2 - 1}.Normalize()
		got, _, gotOK := tr.Raycast(o, d, 100)

		best, bestOK := float32(math.Inf(1)), false
		for z := 0; z < tr.CellsZ; z++ {
			for x := 0; x < tr.CellsX; x++ {
				for _, tri := range tr.CellTriangles(x, z) {
					if h, ok := rayTriangle(o, d, tri[0], tri[1], tri[2]); ok && h >= 0 && h <= 100 && h < best {
						best, bestOK = h, true
					}
				}
			}
		}
		if gotOK != bestOK || (gotOK && math.Abs(float64(got-best)) > 1e-3) {
			t.Fatalf("ray %v %v: DDA %v,%v brute %v,%v", o, d, got, gotOK, best, bestOK)
		}
	}
}

func TestTerrainHolesAreNotGround(t *testing.T) {
	tr := NewTerrain(mgl32.Vec3{}, 1, 4, 4)
	tr.SetHole(1, 2, true)
	if _, _, ok := tr.HeightAt(1.5, 2.5); ok {
		t.Fatal("hole cell reported ground")
	}
	if _, _, ok := tr.Raycast(mgl32.Vec3{1.5, 5, 2.5}, mgl32.Vec3{0, -1, 0}, 10); ok {
		t.Fatal("ray hit a hole")
	}
	if _, _, ok := tr.HeightAt(2.5, 2.5); !ok {
		t.Fatal("neighbour of a hole lost its ground")
	}
}

func TestTerrainPaintKeepsWeightsSummingTo255(t *testing.T) {
	tr := NewTerrain(mgl32.Vec3{}, 1, 2, 2)
	r := rand.New(rand.NewSource(5))
	for i := 0; i < 200; i++ {
		tr.PaintWeight(1, 1, r.Intn(TerrainLayerCount), r.Float32())
		sum := 0
		for l := 0; l < TerrainLayerCount; l++ {
			sum += int(tr.Weight(1, 1, l))
		}
		if sum != 255 {
			t.Fatalf("weights sum to %d", sum)
		}
	}
	for i := 0; i < 50; i++ {
		tr.PaintWeight(1, 1, 2, 0.5)
	}
	if tr.Weight(1, 1, 2) != 255 {
		t.Fatalf("painting layer 2 to saturation left %d", tr.Weight(1, 1, 2))
	}
}

func TestTerrainNormalizeRepairsArrays(t *testing.T) {
	tr := &Terrain{CellSize: 1, CellsX: 3, CellsZ: 2, Heights: []float32{1, 2}, Weights: []uint8{0, 255, 0, 0}, Holes: []uint8{1}}
	tr.Normalize()
	if len(tr.Heights) != 12 || len(tr.Weights) != 48 || len(tr.Holes) != 6 {
		t.Fatalf("lengths %d %d %d", len(tr.Heights), len(tr.Weights), len(tr.Holes))
	}
	if tr.Weight(0, 0, 1) != 255 || tr.Weight(1, 0, 0) != 255 {
		t.Fatal("padding did not keep the given weights and default the rest")
	}
}

func TestTerrainRawRoundTrips(t *testing.T) {
	tr := bumpyTerrain()
	tr.Layers = [TerrainLayerCount]string{"grass", "", "dirt"}
	tr.TextureScale = 3
	tr.SetHole(2, 3, true)
	tr.PaintWeight(4, 4, 2, 0.7)
	back, err := TerrainFromRaw(TerrainToRaw(tr))
	if err != nil {
		t.Fatal(err)
	}
	if back.Origin != tr.Origin || back.CellSize != tr.CellSize || back.CellsX != tr.CellsX ||
		back.CellsZ != tr.CellsZ || back.Layers != tr.Layers || back.TextureScale != tr.TextureScale {
		t.Fatalf("header changed: %+v", back)
	}
	// Heights are saved to the millimetre (terrainHeightStep).
	for i := range tr.Heights {
		if d := back.Heights[i] - tr.Heights[i]; d > terrainHeightStep/2+1e-6 || d < -terrainHeightStep/2-1e-6 {
			t.Fatalf("height %d: %v != %v", i, back.Heights[i], tr.Heights[i])
		}
	}
	// ...and saved heights save again unchanged, so a level that is opened
	// and saved does not drift a step each time.
	again, _ := TerrainFromRaw(TerrainToRaw(back))
	for i := range back.Heights {
		if again.Heights[i] != back.Heights[i] {
			t.Fatalf("height %d moved on a second save: %v -> %v", i, back.Heights[i], again.Heights[i])
		}
	}
	if !back.IsHole(2, 3) || back.IsHole(3, 3) {
		t.Fatal("holes did not round-trip")
	}
	if back.Weight(4, 4, 2) != tr.Weight(4, 4, 2) || back.Weight(4, 4, 0) != tr.Weight(4, 4, 0) {
		t.Fatal("weights did not round-trip")
	}
}

func TestAFlatUnpaintedTerrainWritesNoArrays(t *testing.T) {
	r := TerrainToRaw(NewTerrain(mgl32.Vec3{}, 1, 64, 64))
	if r.Heights != "" || r.Holes != "" || r.Weights != "" || r.Layers != nil {
		t.Fatalf("empty terrain wrote data: %+v", r)
	}
}

func TestAMisSizedTerrainArrayIsRefused(t *testing.T) {
	r := TerrainToRaw(bumpyTerrain())
	r.CellsX++
	if _, err := TerrainFromRaw(r); err == nil {
		t.Fatal("heights for an 8-wide grid loaded into a 9-wide one")
	}
}

func TestResizingATerrainKeepsWhatStillFits(t *testing.T) {
	tr := bumpyTerrain() // 8 x 10 cells
	tr.SetHole(2, 2, true)
	tr.SetHole(7, 9, true)
	tr.PaintWeight(3, 3, 1, 1)
	h33 := tr.Height(3, 3)
	tr.Resize(12, 5)
	if tr.CellsX != 12 || tr.CellsZ != 5 || len(tr.Heights) != 13*6 {
		t.Fatalf("resized to %dx%d with %d heights", tr.CellsX, tr.CellsZ, len(tr.Heights))
	}
	if tr.Height(3, 3) != h33 || tr.Height(11, 2) != 0 {
		t.Fatal("heights did not stay put under a resize")
	}
	if !tr.IsHole(2, 2) || tr.IsHole(7, 4) || tr.Weight(3, 3, 1) != 255 || tr.Weight(10, 1, 0) != 255 {
		t.Fatal("holes or weights did not stay put under a resize")
	}
}
