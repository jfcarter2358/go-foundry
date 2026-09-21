package world

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/model"
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
