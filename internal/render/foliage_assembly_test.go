package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"sort"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// triangleKeys is a triangle list as a sorted list of byte strings, one per
// triangle, so two lists can be compared regardless of order.
func triangleKeys(v []float32) []string {
	per := 3 * foliageVertexFloats
	keys := make([]string, 0, len(v)/per)
	for t := 0; t+per <= len(v); t += per {
		var b bytes.Buffer
		binary.Write(&b, binary.LittleEndian, v[t:t+per])
		keys = append(keys, b.String())
	}
	sort.Strings(keys)
	return keys
}

// Every variant's assembly — core plus parts — is exactly the merged plant
// the classic path draws: the same triangles, bit for bit.
func TestAnAssemblyFlattensToTheMergedPlant(t *testing.T) {
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		for v := 0; v < world.FoliageVariants[s]; v++ {
			a := recordFoliageAssembly(s, v)
			want := triangleKeys(buildFoliageTemplate(s, v, 0))
			got := triangleKeys(a.flatten())
			if len(got) != len(want) {
				t.Fatalf("%v %d: %d triangles flattened, %d merged", s, v, len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%v %d: the flattened plant differs from the merged one", s, v)
				}
			}
			if len(a.coreBind)*foliageVertexFloats != len(a.core) {
				t.Fatalf("%v %d: %d bindings for %d core vertices", s, v, len(a.coreBind), len(a.core)/foliageVertexFloats)
			}
		}
	}
}

// Trees are mostly parts; every swaying plant has bones; every bone's parent
// comes before it; and the parts a tree is made of are its leaf cards.
func TestAssembliesHavePartsAndBones(t *testing.T) {
	cases := []struct {
		s        world.FoliageShape
		minParts int
		minBones int
	}{
		{world.FoliageBroadleaf, 100, 10},
		{world.FoliageConifer, 100, 1},
		{world.FoliageGrass, 0, 5},
		{world.FoliagePalm, 0, 10},
		{world.FoliageFern, 0, 6},
		{world.FoliageRock, 0, 0},
	}
	for _, c := range cases {
		a := recordFoliageAssembly(c.s, 0)
		if len(a.parts) < c.minParts || len(a.bones)-1 < c.minBones {
			t.Errorf("%v: %d parts, %d bones; want at least %d and %d", c.s, len(a.parts), len(a.bones)-1, c.minParts, c.minBones)
		}
		for i, b := range a.bones {
			if i > 0 && (b.parent < 0 || int(b.parent) >= i) {
				t.Fatalf("%v: bone %d's parent is %d", c.s, i, b.parent)
			}
		}
	}
	// Grass blades all grow from the root, not from each other.
	g := recordFoliageAssembly(world.FoliageGrass, 1)
	for i, b := range g.bones[1:] {
		if b.parent != 0 {
			t.Fatalf("grass blade %d hangs off bone %d", i+1, b.parent)
		}
	}
	// A broadleaf's branches hang off its trunk, and some twigs off them.
	tr := recordFoliageAssembly(world.FoliageBroadleaf, 0)
	deep := 0
	for _, b := range tr.bones[1:] {
		if b.parent > 1 {
			deep++
		}
	}
	if tr.bones[1].parent != 0 || deep == 0 {
		t.Fatalf("broadleaf: the trunk's parent is %d and %d bones hang below a branch", tr.bones[1].parent, deep)
	}
}

// skinVertex poses a vertex bound to bone b at place w for poses.
func skinVertex(poses []mgl32.Mat4, b int, w float32, p mgl32.Vec3) mgl32.Vec3 {
	return mulAffine(poses[2*b], p).Mul(1 - w).Add(mulAffine(poses[2*b+1], p).Mul(w))
}

// The bones bend a plant about as far as the classic sway did: in a full
// gust, the tips of a tree's crown, a grass blade's tip and a fern frond's
// move within a factor of the classic displacement (0.35·sway²), the roots
// stay put, and a child bone's base never leaves its parent.
func TestBonesBendLikeTheSway(t *testing.T) {
	for _, s := range []world.FoliageShape{world.FoliageGrass, world.FoliageBroadleaf, world.FoliageFern, world.FoliageConifer, world.FoliagePalm} {
		a := recordFoliageAssembly(s, 0)
		poses := poseFoliageBones(a.bones, 1, 0, 0)
		var sumWant, sumGot float64
		for i, bt := range a.coreBind {
			v := a.core[i*foliageVertexFloats : (i+1)*foliageVertexFloats]
			p := mgl32.Vec3{v[0], v[1], v[2]}
			moved := skinVertex(poses, int(bt[0]), bt[1], p).Sub(p).Len()
			want := 0.35 * v[8] * v[8]
			if v[8] == 0 && moved > 1e-3 && p.Y() < 0.05 {
				t.Fatalf("%v: a rooted vertex at %v moved %v", s, p, moved)
			}
			sumWant += float64(want)
			sumGot += float64(moved)
		}
		for _, pt := range a.parts {
			for k := 0; k < 4; k++ {
				cv := cardVertex(pt.card, k)
				p := mgl32.Vec3{cv[0], cv[1], cv[2]}
				w := pt.w0
				if cardTemplateCorners[k][1] > 0 {
					w = pt.w1
				}
				moved := skinVertex(poses, int(pt.bone), w, p).Sub(p).Len()
				sumWant += float64(0.35 * cv[8] * cv[8])
				sumGot += float64(moved)
			}
		}
		t.Logf("%v: bones %.3f sway %.3f (%d bones, %d parts)", s, sumGot, sumWant, len(a.bones), len(a.parts))
		if sumWant > 0 && (sumGot < 0.4*sumWant || sumGot > 2.5*sumWant) {
			t.Errorf("%v: the bones move the plant %.3f in all, the sway %.3f", s, sumGot, sumWant)
		}
		for i, b := range a.bones {
			if i == 0 || b.parent <= 0 {
				continue
			}
			pb := &a.bones[b.parent]
			parentAt := skinVertex(poses, int(b.parent), pb.t(b.pivot), b.pivot)
			childAt := skinVertex(poses, i, 0, b.pivot)
			if d := parentAt.Sub(childAt).Len(); d > 1e-4 {
				t.Fatalf("%v: bone %d's base leaves its parent by %v", s, i, d)
			}
		}
	}
}

// With no wind nothing moves.
func TestStillAirLeavesThePlantAlone(t *testing.T) {
	a := recordFoliageAssembly(world.FoliageBroadleaf, 1)
	for _, m := range poseFoliageBones(a.bones, 0, 1, 0) {
		if !m.ApproxEqualThreshold(mgl32.Ident4(), 1e-6) {
			t.Fatalf("a still pose is %v", m)
		}
	}
	_ = math.Pi
}
