package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"

	"github.com/go-gl/mathgl/mgl32"
)

// useTempResources points the asset path resolvers at a fresh directory for the
// duration of one test. SkeletonPath is resolved through fileutil.ResourcesPath,
// so a test that saves a skeleton would otherwise write into the real project.
func useTempResources(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	return dir
}

// riggedCube is a cube with a two-bone chain and one clip that turns the upper
// bone — enough that every part of the split (bones, weights, tracks) has
// something to lose.
func riggedCube(t *testing.T) *ModelDoc {
	t.Helper()
	m := NewCubeDoc()
	m.ID = "dummy"
	lower := m.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0, 0})
	upper := m.ExtrudeBone(lower)
	if lower < 0 || upper < 0 {
		t.Fatal("AddBone/ExtrudeBone refused")
	}
	m.RenameBone(lower, "lower")
	m.RenameBone(upper, "upper")

	// Bottom half to the lower bone, top half to the upper, so a dropped bone
	// shows up as half the mesh going unbound rather than all of it.
	var bottom, top []int
	for i, p := range m.Verts {
		if p.Y() < 0 {
			bottom = append(bottom, i)
		} else {
			top = append(top, i)
		}
	}
	m.AssignVertsToBone(bottom, lower, 1)
	m.AssignVertsToBone(top, upper, 1)

	ci := m.AddClip("wave")
	c := m.Clip(ci)
	c.Duration = 1
	c.SetKey(upper, 0, IdentBonePose())
	c.SetKey(upper, 0.5, ModelBonePose{Rot: mgl32.QuatRotate(1, mgl32.Vec3{0, 0, 1})})
	return m
}

func TestSkeletonRoundTripSplitsBonesOutOfTheModel(t *testing.T) {
	dir := useTempResources(t)
	m := riggedCube(t)

	skel := ExtractSkeleton(m, "biped")
	if err := SaveSkeleton(skel, SkeletonPath("biped")); err != nil {
		t.Fatalf("SaveSkeleton: %v", err)
	}
	if dropped := m.LinkSkeleton(skel); dropped != 0 {
		t.Fatalf("LinkSkeleton dropped %d influences from its own armature", dropped)
	}

	path := filepath.Join(dir, ModelsDirName, "dummy"+ModelExt)
	if err := SaveModel(m, path); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}

	// The .fmdl must not carry the armature any more — that is the whole point
	// of the split, and a leftover copy is the stale copy it exists to remove.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, banned := range []string{"\nbones:", "\nanimations:"} {
		if strings.Contains(text, banned) {
			t.Errorf("linked .fmdl still writes %q", strings.TrimSpace(banned))
		}
	}
	if !strings.Contains(text, "skeleton: biped") {
		t.Error("linked .fmdl does not name its skeleton")
	}
	if !strings.Contains(text, "bn:") {
		t.Error("linked .fmdl writes its weights by index; they must be by bone name")
	}

	got, err := LoadModel(path)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	if got.Skeleton != "biped" {
		t.Errorf("Skeleton = %q, want biped", got.Skeleton)
	}
	if len(got.Bones) != len(m.Bones) {
		t.Fatalf("loaded %d bones, want %d", len(got.Bones), len(m.Bones))
	}
	if len(got.Animations) != 1 || got.Animations[0].Name != "wave" {
		t.Fatalf("loaded clips %v, want [wave]", got.ClipNames())
	}
	for vi := range m.Verts {
		want, have := m.WeightsFor(vi), got.WeightsFor(vi)
		if len(want) != len(have) {
			t.Fatalf("vertex %d: %d influences, want %d", vi, len(have), len(want))
		}
		for i := range want {
			if want[i].Bone != have[i].Bone {
				t.Errorf("vertex %d influence %d: bone %d, want %d", vi, i, have[i].Bone, want[i].Bone)
			}
		}
	}
}

// A skeleton's bones may be reordered by an edit in another model. The linked
// model's weights key on name, so they must follow rather than repoint at
// whatever bone inherited the index.
func TestLinkedWeightsFollowReorderedSkeletonBones(t *testing.T) {
	dir := useTempResources(t)
	m := riggedCube(t)
	skel := ExtractSkeleton(m, "biped")
	m.LinkSkeleton(skel)

	upperFirst := m.BoneIndexByName("upper")
	path := filepath.Join(dir, ModelsDirName, "dummy"+ModelExt)
	if err := SaveModel(m, path); err != nil {
		t.Fatal(err)
	}

	// Reverse the skeleton's bone array behind the model's back, fixing the
	// parent links so it still validates.
	reversed := &SkeletonDoc{ID: "biped"}
	n := len(skel.Bones)
	for i := n - 1; i >= 0; i-- {
		b := skel.Bones[i]
		if b.Parent >= 0 {
			b.Parent = n - 1 - b.Parent
		}
		reversed.Bones = append(reversed.Bones, b)
	}
	reversed.Animations = cloneClips(skel.Animations)
	for ci := range reversed.Animations {
		for ti := range reversed.Animations[ci].Tracks {
			tr := &reversed.Animations[ci].Tracks[ti]
			tr.Bone = n - 1 - tr.Bone
		}
	}
	if err := SaveSkeleton(reversed, SkeletonPath("biped")); err != nil {
		t.Fatal(err)
	}

	got, err := LoadModel(path)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	upperNow := got.BoneIndexByName("upper")
	if upperNow == upperFirst {
		t.Fatal("the bone array did not actually move; the test proves nothing")
	}
	for vi, p := range got.Verts {
		if p.Y() < 0 {
			continue
		}
		ws := got.WeightsFor(vi)
		if len(ws) != 1 || ws[0].Bone != upperNow {
			t.Fatalf("vertex %d is bound to %v, want bone %d (upper)", vi, ws, upperNow)
		}
	}
}

func TestLinkSkeletonDropsInfluencesWithNoBone(t *testing.T) {
	useTempResources(t)
	m := riggedCube(t)

	// A skeleton that has only the lower bone: every top-half vertex loses its
	// influence, and the caller has to be told.
	partial := &SkeletonDoc{ID: "stub", Bones: CloneBones(m.Bones[:1])}
	dropped := m.LinkSkeleton(partial)
	if dropped == 0 {
		t.Fatal("LinkSkeleton reported no dropped influences")
	}
	for vi, p := range m.Verts {
		if p.Y() < 0 {
			continue
		}
		if ws := m.WeightsFor(vi); len(ws) != 0 {
			t.Fatalf("vertex %d kept %v after its bone left the rig", vi, ws)
		}
	}
}

func TestUnlinkSkeletonKeepsTheBonesInline(t *testing.T) {
	dir := useTempResources(t)
	m := riggedCube(t)
	skel := ExtractSkeleton(m, "biped")
	if err := SaveSkeleton(skel, SkeletonPath("biped")); err != nil {
		t.Fatal(err)
	}
	m.LinkSkeleton(skel)
	m.UnlinkSkeleton()

	path := filepath.Join(dir, ModelsDirName, "dummy"+ModelExt)
	if err := SaveModel(m, path); err != nil {
		t.Fatal(err)
	}
	// Removing the skeleton file must not stop the model loading now.
	if err := os.Remove(SkeletonPath("biped")); err != nil {
		t.Fatal(err)
	}
	got, err := LoadModel(path)
	if err != nil {
		t.Fatalf("LoadModel after unlink: %v", err)
	}
	if got.Skeleton != "" {
		t.Errorf("Skeleton = %q, want empty", got.Skeleton)
	}
	if len(got.Bones) != len(m.Bones) || len(got.Animations) != 1 {
		t.Errorf("unlinked model kept %d bones and %d clips, want %d and 1",
			len(got.Bones), len(got.Animations), len(m.Bones))
	}
}

func TestLoadModelRefusesAMissingSkeleton(t *testing.T) {
	dir := useTempResources(t)
	m := riggedCube(t)
	skel := ExtractSkeleton(m, "biped")
	if err := SaveSkeleton(skel, SkeletonPath("biped")); err != nil {
		t.Fatal(err)
	}
	m.LinkSkeleton(skel)
	path := filepath.Join(dir, ModelsDirName, "dummy"+ModelExt)
	if err := SaveModel(m, path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(SkeletonPath("biped")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadModel(path); err == nil {
		t.Fatal("LoadModel accepted a model whose skeleton is gone")
	} else if !strings.Contains(err.Error(), "biped") {
		t.Errorf("error does not name the missing skeleton: %v", err)
	}
}

func TestValidateRawModelRejectsInconsistentSkeletonLinks(t *testing.T) {
	skel := &SkeletonDoc{ID: "biped", Bones: []ModelBone{
		{Name: "root", Parent: ModelBoneNoParent, Tail: mgl32.Vec3{0, 1, 0}},
	}}
	base := func() RawModel {
		return RawModel{
			ID:       "m",
			Skeleton: "biped",
			Vertices: [][]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}},
			Faces:    []RawModelFace{{Verts: []int{0, 1, 2}}},
		}
	}
	cases := []struct {
		name string
		mut  func(*RawModel)
		skel *SkeletonDoc
	}{
		{"bones as well as a link", func(r *RawModel) {
			r.Bones = []RawModelBone{{Name: "x", Head: []float32{0, 0, 0}, Tail: []float32{0, 1, 0}}}
		}, skel},
		{"skeleton not loaded", func(r *RawModel) {}, nil},
		{"weight names a bone the rig lacks", func(r *RawModel) {
			r.Weights = []RawVertexBinds{{Vert: 0, Names: []string{"elbow"}, Weights: []float32{1}}}
		}, skel},
		{"weight indexes instead of naming", func(r *RawModel) {
			r.Weights = []RawVertexBinds{{Vert: 0, Bones: []int{0}, Weights: []float32{1}}}
		}, skel},
	}
	for _, c := range cases {
		raw := base()
		c.mut(&raw)
		if err := validateRawModel(raw, c.skel); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func TestSaveSkeletonRefusesAnEmptyRig(t *testing.T) {
	useTempResources(t)
	if err := SaveSkeleton(&SkeletonDoc{ID: "empty"}, SkeletonPath("empty")); err == nil {
		t.Fatal("SaveSkeleton accepted a skeleton with no bones")
	}
}

// A keyframe added to a linked document changes no byte of the .fmdl, so the
// model-only fingerprint would report it as saved. The editor's save chip reads
// this, and reporting unsaved animation work as saved loses it.
func TestFingerprintCoversTheLinkedSkeleton(t *testing.T) {
	useTempResources(t)
	m := riggedCube(t)
	m.LinkSkeleton(ExtractSkeleton(m, "biped"))

	before := Fingerprint(m)
	m.Clip(0).SetKey(m.BoneIndexByName("lower"), 0.25, ModelBonePose{Trans: mgl32.Vec3{0, 0.1, 0}})
	if after := Fingerprint(m); after == before {
		t.Fatal("Fingerprint did not move when a clip on the shared skeleton was keyed")
	}
}

func TestModelsUsingSkeletonReadsTheLink(t *testing.T) {
	dir := useTempResources(t)
	m := riggedCube(t)
	skel := ExtractSkeleton(m, "biped")
	if err := SaveSkeleton(skel, SkeletonPath("biped")); err != nil {
		t.Fatal(err)
	}
	m.LinkSkeleton(skel)
	for _, id := range []string{"marine", "mechanic"} {
		m.ID = id
		if err := SaveModel(m, ModelPath(id)); err != nil {
			t.Fatal(err)
		}
	}
	// An unlinked model in the same folder must not be listed.
	plain := riggedCube(t)
	plain.ID = "crate"
	if err := SaveModel(plain, ModelPath("crate")); err != nil {
		t.Fatal(err)
	}

	got := ModelsUsingSkeleton("biped", ModelPath("marine"))
	if len(got) != 1 || got[0] != "mechanic" {
		t.Errorf("ModelsUsingSkeleton = %v, want [mechanic]", got)
	}
	if link := SkeletonLinkOf(filepath.Join(dir, ModelsDirName, "crate"+ModelExt)); link != "" {
		t.Errorf("SkeletonLinkOf(crate) = %q, want empty", link)
	}
}

// rawBonesFrom and rawClipsFrom return allocated slices rather than nil, so the
// unrigged case leans entirely on omitempty. A cube that started writing
// "bones: []" would still load, but every model in the project would grow a
// line saying nothing.
func TestUnriggedSaveOmitsTheRigKeysEntirely(t *testing.T) {
	useTempResources(t)
	path := filepath.Join(t.TempDir(), "cube"+ModelExt)
	if err := SaveModel(NewCubeDoc(), path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"bones:", "animations:", "weights:", "skeleton:"} {
		if strings.Contains(string(data), key) {
			t.Errorf("an unrigged model writes %q:\n%s", key, data)
		}
	}
}
