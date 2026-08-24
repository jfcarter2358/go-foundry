package model

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// skinnedCube returns a cube with one bone running up through it and every
// vertex bound rigidly to that bone.
func skinnedCube(t *testing.T) (*ModelDoc, int) {
	t.Helper()
	m := NewCubeDoc()
	bi := m.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	if bi < 0 {
		t.Fatal("AddBone refused")
	}
	m.AssignVertsToBone(modelAllVerts(m), bi, 1)
	return m, bi
}

func modelAllVerts(m *ModelDoc) []int {
	out := make([]int, len(m.Verts))
	for i := range out {
		out[i] = i
	}
	return out
}

func TestAssignSplitsAVertexBetweenTwoBones(t *testing.T) {
	m := NewCubeDoc()
	a := m.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})
	b := m.AddBone(mgl32.Vec3{}, mgl32.Vec3{1, 0, 0})

	m.AssignVertsToBone([]int{0}, a, 1)
	m.AssignVertsToBone([]int{0}, b, 1)

	if got := m.WeightOf(0, a); math.Abs(float64(got-0.5)) > 1e-6 {
		t.Errorf("weight on the first bone = %v, want 0.5", got)
	}
	if got := m.WeightOf(0, b); math.Abs(float64(got-0.5)) > 1e-6 {
		t.Errorf("weight on the second bone = %v, want 0.5", got)
	}
}

func TestNormalizeKeepsTheStrongestInfluences(t *testing.T) {
	var raw []ModelVertexWeight
	for i := 0; i < ModelMaxInfluences+2; i++ {
		raw = append(raw, ModelVertexWeight{Bone: i, Weight: float32(10 - i)})
	}

	ws := normalizeWeights(raw)

	if len(ws) != ModelMaxInfluences {
		t.Fatalf("kept %d influences, want %d", len(ws), ModelMaxInfluences)
	}
	var total float32
	for _, w := range ws {
		total += w.Weight
		if w.Bone >= ModelMaxInfluences {
			t.Errorf("a weaker influence (bone %d) survived the prune", w.Bone)
		}
	}
	if math.Abs(float64(total-1)) > 1e-6 {
		t.Errorf("survivors sum to %v, want the whole vertex", total)
	}
}

func TestAssignStaysInsideTheInfluenceBudget(t *testing.T) {
	m := NewCubeDoc()
	for i := 0; i < ModelMaxInfluences+2; i++ {
		bi := m.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, float32(i + 1), 0})
		m.AssignVertsToBone([]int{0}, bi, 1)
	}
	ws := m.VertexWeights(0)
	if len(ws) != ModelMaxInfluences {
		t.Fatalf("vertex kept %d influences, want %d", len(ws), ModelMaxInfluences)
	}
	var total float32
	for _, w := range ws {
		total += w.Weight
	}
	if math.Abs(float64(total-1)) > 1e-6 {
		t.Errorf("weights sum to %v, want 1", total)
	}
}

func TestSkinFromMovesBoundVertsAndLeavesTheRest(t *testing.T) {
	m := NewCubeDoc()
	bi := m.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	// Bind the top four corners only.
	top := []int{4, 5, 6, 7}
	m.AssignVertsToBone(top, bi, 1)

	rest := CloneBones(m.Bones)
	restVerts := append([]mgl32.Vec3(nil), m.Verts...)
	m.MoveBone(bi, mgl32.Vec3{2, 0, 0})
	m.SkinFrom(rest, restVerts)

	for _, vi := range top {
		want := restVerts[vi].Add(mgl32.Vec3{2, 0, 0})
		if !vec3Close(m.Verts[vi], want, 1e-5) {
			t.Errorf("bound vertex %d at %v, want %v", vi, m.Verts[vi], want)
		}
	}
	for _, vi := range []int{0, 1, 2, 3} {
		if !vec3Close(m.Verts[vi], restVerts[vi], 1e-6) {
			t.Errorf("unbound vertex %d moved to %v", vi, m.Verts[vi])
		}
	}
}

func TestSkinFromBlendsPartialWeights(t *testing.T) {
	m := NewCubeDoc()
	moving := m.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	still := m.AddBone(mgl32.Vec3{1, -0.5, 0}, mgl32.Vec3{1, 0.5, 0})
	m.AssignVertsToBone([]int{0}, moving, 1)
	m.AssignVertsToBone([]int{0}, still, 1) // 50/50

	rest := CloneBones(m.Bones)
	restVerts := append([]mgl32.Vec3(nil), m.Verts...)
	m.MoveBone(moving, mgl32.Vec3{4, 0, 0})
	m.SkinFrom(rest, restVerts)

	// Half of one bone's translation, because the other half of the vertex
	// belongs to a bone that did not move.
	want := restVerts[0].Add(mgl32.Vec3{2, 0, 0})
	if !vec3Close(m.Verts[0], want, 1e-5) {
		t.Errorf("half-weighted vertex at %v, want %v", m.Verts[0], want)
	}
}

func TestSkinFromStretchesWithTheBone(t *testing.T) {
	m := NewCubeDoc()
	bi := m.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	m.Verts = append(m.Verts, mgl32.Vec3{0, 1, 0}) // sits exactly on the tail
	m.EnsureWeights()
	vi := len(m.Verts) - 1
	m.AssignVertsToBone([]int{vi}, bi, 1)

	rest := CloneBones(m.Bones)
	restVerts := append([]mgl32.Vec3(nil), m.Verts...)
	m.SetBoneTail(bi, mgl32.Vec3{0, 3, 0}) // three times as long
	m.SkinFrom(rest, restVerts)

	if !vec3Close(m.Verts[vi], mgl32.Vec3{0, 3, 0}, 1e-5) {
		t.Errorf("vertex on the tail ended at %v, want it carried to {0 3 0}", m.Verts[vi])
	}
}

func TestSkinFromIsAbsoluteNotIncremental(t *testing.T) {
	m, bi := skinnedCube(t)
	rest := CloneBones(m.Bones)
	restVerts := append([]mgl32.Vec3(nil), m.Verts...)

	// Two applications from the same snapshot, as a gizmo drag does across
	// motion events: the second must replace the first, not compound it.
	m.MoveBone(bi, mgl32.Vec3{1, 0, 0})
	m.SkinFrom(rest, restVerts)
	m.Bones[bi] = rest[bi]
	m.MoveBone(bi, mgl32.Vec3{3, 0, 0})
	m.SkinFrom(rest, restVerts)

	want := restVerts[0].Add(mgl32.Vec3{3, 0, 0})
	if !vec3Close(m.Verts[0], want, 1e-5) {
		t.Errorf("vertex at %v after two applications, want %v", m.Verts[0], want)
	}
}

func TestExtrudeFacesInheritsWeights(t *testing.T) {
	m, bi := skinnedCube(t)
	top := cubeTopFace(t, m)
	before := len(m.Verts)

	m.ExtrudeFaces([]int{top}, 1)

	if len(m.Weights) != len(m.Verts) {
		t.Fatalf("weights (%d) and verts (%d) fell out of step", len(m.Weights), len(m.Verts))
	}
	for vi := before; vi < len(m.Verts); vi++ {
		if got := m.WeightOf(vi, bi); math.Abs(float64(got-1)) > 1e-6 {
			t.Errorf("extruded vertex %d has weight %v on the bone, want 1", vi, got)
		}
	}
}

func TestSubdivideBlendsWeightsAcrossTwoBones(t *testing.T) {
	m := twoQuadStrip()
	a := m.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	b := m.AddBone(mgl32.Vec3{2, 0, 0}, mgl32.Vec3{2, 1, 0})
	// Bound either side of the edge {0,1} / {4,5}, so a midpoint created along
	// it has one endpoint on each bone.
	m.AssignVertsToBone([]int{0, 5}, a, 1)
	m.AssignVertsToBone([]int{1, 4}, b, 1)
	before := len(m.Verts)

	m.SubdivideFaces([]int{0, 1})

	if len(m.Weights) != len(m.Verts) {
		t.Fatalf("weights (%d) and verts (%d) fell out of step", len(m.Weights), len(m.Verts))
	}
	// Somewhere in the new vertices there must be one the two bones share:
	// a midpoint between an a-bound and a b-bound corner.
	shared := false
	for vi := before; vi < len(m.Verts); vi++ {
		if m.WeightOf(vi, a) > 0 && m.WeightOf(vi, b) > 0 {
			shared = true
			break
		}
	}
	if !shared {
		t.Error("no subdivided vertex blends the two bones; the boundary would crease")
	}
}

func TestCatmullClarkKeepsTheSkin(t *testing.T) {
	m, bi := skinnedCube(t)

	m.CatmullClark()

	if len(m.Weights) != len(m.Verts) {
		t.Fatalf("weights (%d) and verts (%d) fell out of step", len(m.Weights), len(m.Verts))
	}
	for vi := range m.Verts {
		if got := m.WeightOf(vi, bi); math.Abs(float64(got-1)) > 1e-6 {
			t.Fatalf("vertex %d lost its binding: weight %v", vi, got)
		}
	}
}

func TestDeleteFacesCompactsWeightsWithVerts(t *testing.T) {
	m := NewCubeDoc()
	bi := m.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	top := cubeTopFace(t, m)
	// Bind only the top face's corners, so deleting a *different* face has to
	// remap the surviving weights onto their new indices.
	topVerts := append([]int(nil), m.Faces[top].Verts...)
	m.AssignVertsToBone(topVerts, bi, 1)
	positions := map[mgl32.Vec3]bool{}
	for _, vi := range topVerts {
		positions[m.Verts[vi]] = true
	}

	bottom := -1
	for fi := range m.Faces {
		if m.FaceNormal(fi).Y() < -0.9 {
			bottom = fi
		}
	}
	m.DeleteFaces([]int{bottom})

	if len(m.Weights) != len(m.Verts) {
		t.Fatalf("weights (%d) and verts (%d) fell out of step", len(m.Weights), len(m.Verts))
	}
	bound := 0
	for vi := range m.Verts {
		if m.WeightOf(vi, bi) > 0 {
			bound++
			if !positions[m.Verts[vi]] {
				t.Errorf("vertex %d at %v is bound but was not one of the top corners", vi, m.Verts[vi])
			}
		}
	}
	if bound != len(topVerts) {
		t.Errorf("%d vertices still bound, want %d", bound, len(topVerts))
	}
}

func TestDeleteBonesDropsAndReindexesWeights(t *testing.T) {
	m := NewCubeDoc()
	doomed := m.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})
	keeper := m.AddBone(mgl32.Vec3{1, 0, 0}, mgl32.Vec3{1, 1, 0})
	m.AssignVertsToBone([]int{0}, doomed, 1)
	m.AssignVertsToBone([]int{1}, keeper, 1)

	m.DeleteBones([]int{doomed})

	if len(m.Bones) != 1 {
		t.Fatalf("bones = %d, want 1", len(m.Bones))
	}
	if ws := m.VertexWeights(0); len(ws) != 0 {
		t.Errorf("vertex 0 still bound to something: %v", ws)
	}
	if got := m.WeightOf(1, 0); math.Abs(float64(got-1)) > 1e-6 {
		t.Errorf("survivor's binding = %v at its new index, want 1", got)
	}
}

func TestAutoWeightPicksTheNearestBone(t *testing.T) {
	m := NewCubeDoc()
	near := m.AddBone(mgl32.Vec3{-0.5, -0.5, 0}, mgl32.Vec3{-0.5, 0.5, 0})
	far := m.AddBone(mgl32.Vec3{20, -0.5, 0}, mgl32.Vec3{20, 0.5, 0})

	if n := m.AutoWeightVerts(modelAllVerts(m)); n != len(m.Verts) {
		t.Fatalf("auto-weighted %d of %d vertices", n, len(m.Verts))
	}
	for vi := range m.Verts {
		if m.WeightOf(vi, near) <= 0 {
			t.Errorf("vertex %d picked up nothing from the bone beside it", vi)
		}
		if got := m.WeightOf(vi, far); got != 0 {
			t.Errorf("vertex %d picked up %v from a bone 20 units away", vi, got)
		}
	}
}

func TestMergeBringsBonesAndWeightsAcross(t *testing.T) {
	base := NewCubeDoc()
	baseBone := base.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})
	base.AssignVertsToBone([]int{0}, baseBone, 1)

	other := NewCubeDoc()
	oa := other.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})
	ob := other.ExtrudeBone(oa)
	other.AssignVertsToBone([]int{0}, ob, 1)

	base.Merge(other, mgl32.Vec3{10, 0, 0})

	if len(base.Bones) != 3 {
		t.Fatalf("bones = %d, want 3", len(base.Bones))
	}
	names := map[string]bool{}
	for _, b := range base.Bones {
		if names[b.Name] {
			t.Errorf("merge produced a duplicate name %q", b.Name)
		}
		names[b.Name] = true
	}
	// The merged chain keeps its own parenting, shifted onto the new indices.
	if got := base.Bones[2].Parent; got != 1 {
		t.Errorf("merged child's parent = %d, want 1", got)
	}
	if got := base.Bones[1].Head; !vec3Close(got, mgl32.Vec3{10, 0, 0}, 1e-6) {
		t.Errorf("merged bone head = %v, want it offset to {10 0 0}", got)
	}
	// Its vertex binding points at the merged bone, not the one already here.
	merged := len(NewCubeDoc().Verts)
	if got := base.WeightOf(merged, 2); math.Abs(float64(got-1)) > 1e-6 {
		t.Errorf("merged vertex weight on bone 2 = %v, want 1", got)
	}
	if got := base.WeightOf(0, baseBone); math.Abs(float64(got-1)) > 1e-6 {
		t.Errorf("the original binding was disturbed: %v", got)
	}
}

// ---------- persistence ----------

func TestFmdlRoundTripsTheArmature(t *testing.T) {
	m, idx := boneChain(t)
	m.Bones[idx[1]].Roll = 0.75
	m.AssignVertsToBone([]int{0, 1}, idx[0], 1)
	m.AssignVertsToBone([]int{1}, idx[2], 1)

	data, err := yaml.Marshal(modelDocToRaw(m))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw RawModel
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := validateRawModel(raw); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := rawToModelDoc(raw)

	if len(got.Bones) != len(m.Bones) {
		t.Fatalf("bones = %d, want %d", len(got.Bones), len(m.Bones))
	}
	for i := range m.Bones {
		a, b := m.Bones[i], got.Bones[i]
		if a.Name != b.Name || a.Parent != b.Parent || a.Connected != b.Connected {
			t.Errorf("bone %d: %+v -> %+v", i, a, b)
		}
		if !vec3Close(a.Head, b.Head, 1e-6) || !vec3Close(a.Tail, b.Tail, 1e-6) {
			t.Errorf("bone %d moved: %v..%v -> %v..%v", i, a.Head, a.Tail, b.Head, b.Tail)
		}
		if math.Abs(float64(a.Roll-b.Roll)) > 1e-6 {
			t.Errorf("bone %d roll: %v -> %v", i, a.Roll, b.Roll)
		}
	}
	if len(got.Weights) != len(got.Verts) {
		t.Fatalf("weights (%d) and verts (%d) fell out of step", len(got.Weights), len(got.Verts))
	}
	for vi := range m.Verts {
		for _, w := range m.VertexWeights(vi) {
			if diff := math.Abs(float64(w.Weight - got.WeightOf(vi, w.Bone))); diff > 1e-6 {
				t.Errorf("vertex %d bone %d weight: %v -> %v",
					vi, w.Bone, w.Weight, got.WeightOf(vi, w.Bone))
			}
		}
	}
}

func TestFmdlOmitsAnEmptyArmature(t *testing.T) {
	data, err := yaml.Marshal(modelDocToRaw(NewCubeDoc()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"bones", "weights"} {
		if _, present := raw[key]; present {
			t.Errorf("%q written for a model with no armature", key)
		}
	}
}

func TestValidateRejectsBrokenArmatures(t *testing.T) {
	parent := func(i int) *int { return &i }
	cases := []struct {
		name string
		mut  func(*RawModel)
	}{
		{"parent out of range", func(r *RawModel) { r.Bones[0].Parent = parent(7) }},
		{"self parent", func(r *RawModel) { r.Bones[0].Parent = parent(0) }},
		{"cycle", func(r *RawModel) {
			r.Bones[0].Parent = parent(1)
			r.Bones[1].Parent = parent(0)
		}},
		{"zero length", func(r *RawModel) { r.Bones[0].Tail = r.Bones[0].Head }},
		{"duplicate name", func(r *RawModel) { r.Bones[1].Name = r.Bones[0].Name }},
		{"weight on a missing bone", func(r *RawModel) {
			r.Weights = []RawVertexBinds{{Vert: 0, Bones: []int{9}, Weights: []float32{1}}}
		}},
		{"weight on a missing vertex", func(r *RawModel) {
			r.Weights = []RawVertexBinds{{Vert: 999, Bones: []int{0}, Weights: []float32{1}}}
		}},
		{"mismatched weight lists", func(r *RawModel) {
			r.Weights = []RawVertexBinds{{Vert: 0, Bones: []int{0, 1}, Weights: []float32{1}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := boneChain(t)
			raw := modelDocToRaw(m)
			tc.mut(&raw)
			if err := validateRawModel(raw); err == nil {
				t.Error("accepted a model that should have been refused")
			}
		})
	}
}

func TestSaveAndLoadModelWithBones(t *testing.T) {
	m, idx := boneChain(t)
	m.AssignVertsToBone([]int{0, 1, 2}, idx[2], 1)
	path := filepath.Join(t.TempDir(), "rig"+ModelExt)

	if err := SaveModel(m, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadModel(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Bones) != 3 {
		t.Fatalf("bones = %d, want 3", len(got.Bones))
	}
	if len(got.VertsBoundTo(idx[2])) != 3 {
		t.Errorf("%d vertices bound to the tip bone, want 3", len(got.VertsBoundTo(idx[2])))
	}
}
