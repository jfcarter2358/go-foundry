package world

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// The cache hands out clones, so one entity's edits cannot reach the next, and
// it notices a model saved since it was parsed.
func TestModelDocumentCacheClonesAndNoticesEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "box"+model.ModelExt)
	doc := model.NewCubeDoc()
	doc.Texture = "first"
	if err := model.SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}

	a, err := loadModelDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	a.Texture = "scribbled"
	a.Verts[0][0] = 99
	b, err := loadModelDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.Texture != "first" || b.Verts[0][0] == 99 {
		t.Fatalf("second load saw the first caller's edits: texture %q vert %v", b.Texture, b.Verts[0])
	}

	doc.Texture = "second"
	if err := model.SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}
	// Filesystems with coarse timestamps: make sure the edit is visible.
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	c, err := loadModelDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Texture != "second" {
		t.Fatalf("texture after re-save = %q, want %q", c.Texture, "second")
	}
}

// A skeleton edit changes no byte of the .fmdl that links it, so the cache's
// size-and-mtime stamp on the model alone would serve the old clips forever —
// re-saving a walk cycle would leave every character still walking the old way.
func TestModelDocumentCacheNoticesASkeletonEdit(t *testing.T) {
	root := t.TempDir()
	models := filepath.Join(root, model.ModelsDirName)
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}

	doc := model.NewCubeDoc()
	bi := doc.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	doc.RenameBone(bi, "root")
	doc.AssignVertsToBone(allVertIndices(doc), bi, 1)
	ci := doc.AddClip("walk")
	doc.Clip(ci).Duration = 1
	doc.Clip(ci).SetKey(bi, 0, model.IdentBonePose())

	skel := model.ExtractSkeleton(doc, "rig")
	// Written through SkeletonPathFor, so it lands beside the model rather than
	// in whatever resources directory the test binary inherited.
	skelPath := model.SkeletonPathFor(filepath.Join(models, "x"+model.ModelExt), "rig")
	if err := model.SaveSkeleton(skel, skelPath); err != nil {
		t.Fatal(err)
	}
	doc.LinkSkeleton(skel)

	path := filepath.Join(models, "box"+model.ModelExt)
	if err := model.SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadModelDocument(path); err != nil {
		t.Fatal(err)
	}

	// A second clip on the shared rig; the model file is untouched.
	skel.Animations = append(skel.Animations, model.ModelClip{Name: "run", Duration: 1})
	if err := model.SaveSkeleton(skel, skelPath); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(skelPath, later, later); err != nil {
		t.Fatal(err)
	}

	got, err := loadModelDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Animations) != 2 {
		t.Fatalf("after the skeleton was re-saved the cache served %d clips, want 2", len(got.Animations))
	}
}

func allVertIndices(m *model.ModelDoc) []int {
	out := make([]int, len(m.Verts))
	for i := range out {
		out[i] = i
	}
	return out
}
