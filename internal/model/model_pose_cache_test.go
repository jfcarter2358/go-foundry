package model

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// referencePoseMatrices is PoseMatrices exactly as it was before the rig cache
// (model_pose_cache.go): everything recomputed per call. Kept verbatim so the
// cached path can be held to bit-identical output.
func referencePoseMatrices(m *ModelDoc, p ModelPose) []mgl32.Mat4 {
	n := len(m.Bones)
	posed := make([]mgl32.Mat4, n)
	rest := make([]mgl32.Mat4, n)
	for i := range m.Bones {
		posed[i] = mgl32.Ident4()
		rest[i] = BoneRestBasis(m.Bones[i])
	}
	for _, bi := range m.boneOrder() {
		parent := m.Bones[bi].Parent
		local := rest[bi]
		base := mgl32.Ident4()
		if m.ValidBone(parent) {
			local = rest[parent].Inv().Mul4(rest[bi])
			base = posed[parent]
		}
		posed[bi] = base.Mul4(local).Mul4(p.Bone(bi).matrix())
	}
	return posed
}

// referencePoseDeltas is the pre-cache PoseDeltas.
func referencePoseDeltas(m *ModelDoc, p ModelPose) []mgl32.Mat4 {
	posed := referencePoseMatrices(m, p)
	out := make([]mgl32.Mat4, len(posed))
	for i := range posed {
		out[i] = posed[i].Mul4(BoneRestBasis(m.Bones[i]).Inv())
	}
	return out
}

// randomPose returns a pose with every channel populated, so no bone takes the
// identity shortcut by accident. Zero-valued entries are mixed in too: the
// zero value is a real state (it reads as rest) and must agree as well.
func randomPose(r *rand.Rand, n int) ModelPose {
	p := NewModelPose(n)
	for i := range p.Bones {
		if r.Intn(8) == 0 {
			p.Bones[i] = ModelBonePose{}
			continue
		}
		axis := mgl32.Vec3{r.Float32()*2 - 1, r.Float32()*2 - 1, r.Float32()*2 - 1}
		if axis.Len() < 1e-3 {
			axis = mgl32.Vec3{0, 1, 0}
		}
		p.Bones[i] = ModelBonePose{
			Trans: mgl32.Vec3{r.Float32() - 0.5, r.Float32() - 0.5, r.Float32() - 0.5},
			Rot:   mgl32.QuatRotate(r.Float32()*6.28, axis.Normalize()),
			Scale: mgl32.Vec3{0.5 + r.Float32(), 0.5 + r.Float32(), 0.5 + r.Float32()},
		}
	}
	return p
}

// shippedRigs loads the rigged models the game ships, from the user's project
// when it is present, plus whatever rigged models the repo's own resources hold.
func shippedRigs(t testing.TB) map[string]*ModelDoc {
	out := map[string]*ModelDoc{}
	dirs := []string{
		"/mnt/c/Users/jcart/OneDrive/Documents/Foundry/resources/models",
		filepath.Join("..", "..", "resources", "models"),
	}
	for _, dir := range dirs {
		for _, name := range []string{"human", "soldier", "marine", "mechanic", "fp_arms", "banner", "flag", "dropship"} {
			path := filepath.Join(dir, name+ModelExt)
			if _, err := os.Stat(path); err != nil {
				continue
			}
			m, err := LoadModel(path)
			if err != nil || len(m.Bones) == 0 {
				continue
			}
			out[path] = m
		}
	}
	return out
}

// TestPoseCacheIsBitIdentical holds the cached posing path to the uncached one
// on random poses, over the shipped rigs and a synthetic armature whose array
// order puts children before parents.
func TestPoseCacheIsBitIdentical(t *testing.T) {
	rigs := shippedRigs(t)
	chain, _ := boneChain(t)
	rigs["chain"] = chain
	// Reverse a copy of the chain so children precede parents in the array,
	// exercising the topological walk.
	rev := chain.Clone()
	n := len(rev.Bones)
	perm := make([]int, n)
	for i := range perm {
		perm[i] = n - 1 - i
	}
	bones := make([]ModelBone, n)
	for i, b := range chain.Bones {
		if b.Parent >= 0 {
			b.Parent = perm[b.Parent]
		}
		bones[perm[i]] = b
	}
	rev.Bones = bones
	rigs["chain-reversed"] = rev

	r := rand.New(rand.NewSource(1))
	for name, m := range rigs {
		var scratch []mgl32.Mat4
		dst := make([]mgl32.Mat4, len(m.Bones))
		for trial := 0; trial < 200; trial++ {
			p := randomPose(r, len(m.Bones))
			want := referencePoseDeltas(m, p)
			got := m.PoseDeltas(p)
			dst, scratch = m.PoseDeltasInto(dst, scratch, p)
			wantM := referencePoseMatrices(m, p)
			gotM := m.PoseMatrices(p)
			for i := range want {
				if got[i] != want[i] || dst[i] != want[i] {
					t.Fatalf("%s trial %d bone %d: cached delta differs\nwant %v\ngot  %v\ninto %v", name, trial, i, want[i], got[i], dst[i])
				}
				if gotM[i] != wantM[i] {
					t.Fatalf("%s trial %d bone %d: cached matrix differs", name, trial, i)
				}
			}
		}
	}
	t.Logf("compared %d rigs", len(rigs))
}

// TestPoseCacheFollowsBoneEdits proves the cache notices every field posing
// reads, however the bones were written: in place, by reslicing, by replacing
// the slice.
func TestPoseCacheFollowsBoneEdits(t *testing.T) {
	m, idx := boneChain(t)
	r := rand.New(rand.NewSource(2))
	p := randomPose(r, len(m.Bones))
	check := func(what string) {
		t.Helper()
		want := referencePoseDeltas(m, p)
		got := m.PoseDeltas(p)
		if len(got) != len(want) {
			t.Fatalf("%s: %d deltas, want %d", what, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: bone %d posed against a stale rig", what, i)
			}
		}
	}
	check("initial")
	m.Bones[idx[1]].Head = m.Bones[idx[1]].Head.Add(mgl32.Vec3{0.1, 0, 0})
	check("head moved")
	m.Bones[idx[1]].Tail = m.Bones[idx[1]].Tail.Add(mgl32.Vec3{0, 0.2, 0})
	check("tail moved")
	m.Bones[idx[2]].Roll += 0.3
	check("rolled")
	m.Bones[idx[2]].Parent = ModelBoneNoParent
	check("unparented")
	m.Bones = m.Bones[:len(m.Bones)-1]
	p.Resize(len(m.Bones))
	check("bone removed")
	m.Bones = append(CloneBones(m.Bones), ModelBone{Name: "extra", Parent: idx[0], Head: mgl32.Vec3{0, 1, 0}, Tail: mgl32.Vec3{0, 2, 0}})
	p.Resize(len(m.Bones))
	check("bone added")
}

// BenchmarkPoseDeltas measures posing human.fmdl — the per-entity, per-frame
// cost of an animated body — cached, cached without allocation, and the
// pre-cache reference.
func BenchmarkPoseDeltas(b *testing.B) {
	var m *ModelDoc
	for path, d := range shippedRigs(b) {
		if filepath.Base(path) == "human"+ModelExt {
			m = d
		}
	}
	if m == nil {
		b.Skip("human.fmdl not available")
	}
	p := randomPose(rand.New(rand.NewSource(3)), len(m.Bones))
	b.Run("PoseDeltas", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = m.PoseDeltas(p)
		}
	})
	b.Run("PoseDeltasInto", func(b *testing.B) {
		b.ReportAllocs()
		dst := make([]mgl32.Mat4, len(m.Bones))
		var scratch []mgl32.Mat4
		for i := 0; i < b.N; i++ {
			dst, scratch = m.PoseDeltasInto(dst, scratch, p)
		}
	})
	b.Run("reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = referencePoseDeltas(m, p)
		}
	})
}
