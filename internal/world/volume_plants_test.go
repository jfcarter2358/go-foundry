package world

import (
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// biomeVolume is a 40x40 forest volume over flat ground at y=0.
func biomeVolume() Volume {
	v := NewVolume("woods", mgl32.Vec3{10, 2, -5})
	v.HalfX, v.HalfY, v.HalfZ = 20, 4, 20
	s := DefaultVolumeScatter()
	s.Kind = ScatterBiome
	s.Biome = TerrainBiome{Preset: "forest"}
	v.Scatter = &s
	return v
}

func flatGround(x, z float32) (mgl32.Vec3, mgl32.Vec3, bool) {
	return mgl32.Vec3{x, 0, z}, mgl32.Vec3{0, 1, 0}, true
}

func countPlants(bs []FoliageBatch) int {
	n := 0
	for _, b := range bs {
		n += len(b.Instances)
	}
	return n
}

func TestVolumePlantsGrowInsideTheVolumeOnTheGround(t *testing.T) {
	v := biomeVolume()
	bs := GrowVolumePlants(&v, flatGround)
	if countPlants(bs) == 0 {
		t.Fatal("a forest volume over flat ground grew nothing")
	}
	inv := EulerMatrixYXZ(v.Rotation).Transpose()
	shapes := map[FoliageShape]bool{}
	for bi, b := range bs {
		if bi > 0 && (bs[bi-1].Shape > b.Shape || bs[bi-1].Shape == b.Shape && bs[bi-1].Variant >= b.Variant) {
			t.Fatalf("batches not sorted by (shape, variant) at %d", bi)
		}
		shapes[b.Shape] = true
		for i, in := range b.Instances {
			if _, ok := volumeInset(&v, inv, in.Pos.X(), in.Pos.Z()); !ok {
				t.Fatalf("%v at %v is outside the volume", b.Shape, in.Pos)
			}
			if in.Pos.Y() > 0.001 {
				t.Fatalf("%v at %v floats above the ground", b.Shape, in.Pos)
			}
			if i > 0 && b.Instances[i-1].Rank > in.Rank {
				t.Fatalf("%v instances not sorted by rank", b.Shape)
			}
		}
	}
	if !shapes[FoliageBroadleaf] {
		t.Errorf("a forest grew no broadleaf trees: %v", shapes)
	}
}

func TestVolumePlantsAreDeterministic(t *testing.T) {
	v := biomeVolume()
	a := GrowVolumePlants(&v, flatGround)
	b := GrowVolumePlants(&v, flatGround)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the same volume over the same ground grew different plants")
	}
	w := biomeVolume()
	w.ID = "other"
	if reflect.DeepEqual(a, GrowVolumePlants(&w, flatGround)) {
		t.Error("two volumes with different IDs grew identical plants")
	}
}

func TestVolumePlantsNeedGroundAndAmounts(t *testing.T) {
	v := biomeVolume()
	none := func(x, z float32) (mgl32.Vec3, mgl32.Vec3, bool) { return mgl32.Vec3{}, mgl32.Vec3{}, false }
	if n := countPlants(GrowVolumePlants(&v, none)); n != 0 {
		t.Errorf("%d plants grew with no ground under them", n)
	}
	for _, sh := range v.Scatter.Biome.Def().Shapes() {
		v.Scatter.Biome.SetAmount(sh, 0)
	}
	v.Scatter.Biome.SetAmount(FoliageBroadleaf, 1)
	for _, b := range GrowVolumePlants(&v, flatGround) {
		if b.Shape != FoliageBroadleaf {
			t.Fatalf("%v grew with its amount at 0", b.Shape)
		}
	}
	v.Scatter.Kind = ScatterGrass
	if GrowVolumePlants(&v, flatGround) != nil {
		t.Error("a lattice-kind volume grew biome plants")
	}
}

func TestVolumePlantsCapCandidates(t *testing.T) {
	v := biomeVolume()
	v.HalfX, v.HalfZ = 400, 400
	var casts atomic.Int64
	GrowVolumePlants(&v, func(x, z float32) (mgl32.Vec3, mgl32.Vec3, bool) {
		casts.Add(1)
		return flatGround(x, z)
	})
	// The cap is on the expected count over the footprint's box; jitter
	// lets a few more in at the edges.
	if n := casts.Load(); n > MaxVolumePlantCandidates*11/10 {
		t.Errorf("%d ground casts for a huge volume; the cap is %d", n, MaxVolumePlantCandidates)
	}
}

func TestVolumePlantsCapThinsGroundCoverNotTrees(t *testing.T) {
	trees := func(v *Volume) float64 {
		n := 0
		for _, b := range GrowVolumePlants(v, flatGround) {
			if b.Shape == FoliageBroadleaf {
				n += len(b.Instances)
			}
		}
		return float64(n) / float64(4*v.HalfX*v.HalfZ)
	}
	small := biomeVolume()
	small.HalfX, small.HalfZ = 100, 100
	huge := biomeVolume()
	huge.HalfX, huge.HalfZ = 400, 400
	a, b := trees(&small), trees(&huge)
	if a == 0 || b < a*0.75 || b > a*1.25 {
		t.Errorf("broadleaf per unit area: %.5f under the cap, %.5f over it", a, b)
	}
}

func TestVolumePlantsKeyMovesWithWhatGrows(t *testing.T) {
	v := biomeVolume()
	k := VolumePlantsKey(&v)
	if k != VolumePlantsKey(&v) {
		t.Fatal("key is not stable")
	}
	w := biomeVolume()
	w.Scatter.Biome.SetAmount(FoliageGrass, 0)
	if VolumePlantsKey(&w) == k {
		t.Error("changing an amount did not change the key")
	}
	w = biomeVolume()
	w.Position[0] += 1
	if VolumePlantsKey(&w) == k {
		t.Error("moving the volume did not change the key")
	}
	w = biomeVolume()
	w.Scatter.Color = mgl32.Vec3{1, 0, 0} // the lattice's tint: not the biome's
	if VolumePlantsKey(&w) != k {
		t.Error("a lattice-only field changed the key")
	}
}

func TestVolumeBiomeRoundTrips(t *testing.T) {
	v := biomeVolume()
	v.Scatter.Biome.Density = 1.5
	v.Scatter.Biome.Tint = mgl32.Vec3{0.9, 1, 0.8}
	v.Scatter.Biome.SetAmount(FoliageGrass, 0)
	raw := VolumeScatterToRaw(v.Scatter)
	data, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var back RawVolumeScatter
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	got := VolumeScatterFromRaw(&back)
	if got.Kind != ScatterBiome || !reflect.DeepEqual(got.Biome, v.Scatter.Biome) {
		t.Fatalf("round trip: got kind %d biome %+v, want %+v\n%s", got.Kind, got.Biome, v.Scatter.Biome, data)
	}

	// A lattice volume writes no biome block.
	g := DefaultVolumeScatter()
	if VolumeScatterToRaw(&g).Biome != nil {
		t.Error("a grass volume wrote a biome block")
	}
}
