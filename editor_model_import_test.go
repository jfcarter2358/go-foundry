package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// modelImportTestRoot points executablePath at a temp install root containing a
// models directory, so the catalog scan and the path helpers work on files this
// test controls.
func modelImportTestRoot(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	prev := executablePath
	executablePath = root
	t.Cleanup(func() { executablePath = prev })

	dir := filepath.Join(root, modelsDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir models: %v", err)
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func TestScanLevelModelFilesListsBothFormats(t *testing.T) {
	modelImportTestRoot(t, "rock"+modelExt, "car"+objExt, "car.mtl", "notes.txt")
	// A subdirectory must not be offered as a model, either.
	if err := os.MkdirAll(filepath.Join(modelsDir(), "wip"+modelExt), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got := ScanLevelModelFiles()
	want := []string{"car.obj", "rock.fmdl"} // sorted across both extensions
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("models[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestScanLevelModelFilesEmptyWhenNoModelsDir(t *testing.T) {
	prev := executablePath
	executablePath = t.TempDir()
	t.Cleanup(func() { executablePath = prev })

	if got := ScanLevelModelFiles(); len(got) != 0 {
		t.Errorf("models = %v, want none", got)
	}
}

func TestModelEntityBaseID(t *testing.T) {
	cases := map[string]string{
		"rock.fmdl":     "rock",
		"old_car.obj":   "old_car",
		"no_extension":  "no_extension",
		".fmdl":         "model", // nothing left once the extension is gone
		"two.dots.fmdl": "two.dots",
	}
	for in, want := range cases {
		if got := modelEntityBaseID(in); got != want {
			t.Errorf("modelEntityBaseID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueEntityIDAvoidsCollisions(t *testing.T) {
	lvl := &Level{Entities: []Entity{{ID: "rock"}, {ID: "rock_1"}, {ID: "barrel"}}}

	if got := uniqueEntityID(lvl, "crate"); got != "crate" {
		t.Errorf("free name: got %q, want %q", got, "crate")
	}
	// rock and rock_1 are taken, so the next placement has to skip both.
	if got := uniqueEntityID(lvl, "rock"); got != "rock_2" {
		t.Errorf("taken name: got %q, want %q", got, "rock_2")
	}
}

func TestModelEntityHalfExtentsContainMesh(t *testing.T) {
	// Origin-centred: the extents are the mesh's own half-size.
	centred := &ModelDoc{Verts: []mgl32.Vec3{{-1, -2, -3}, {1, 2, 3}}}
	hx, hy, hz := modelEntityHalfExtents(centred)
	if hx != 1 || hy != 2 || hz != 3 {
		t.Errorf("centred: got (%v,%v,%v), want (1,2,3)", hx, hy, hz)
	}

	// Base at the origin: the box has to reach the far side of the mesh, so it
	// is measured from the origin outward rather than from the mesh's own
	// centre — a half-height of 4, not 2.
	standing := &ModelDoc{Verts: []mgl32.Vec3{{-1, 0, -1}, {1, 4, 1}}}
	_, hy, _ = modelEntityHalfExtents(standing)
	if hy != 4 {
		t.Errorf("base-origin height: got %v, want 4", hy)
	}

	// Flat: a zero extent would read as "no bounds authored" in WorldAABB and
	// disable culling entirely, so it is floored.
	flat := &ModelDoc{Verts: []mgl32.Vec3{{-1, 0, -1}, {1, 0, 1}}}
	_, hy, _ = modelEntityHalfExtents(flat)
	if hy != modelImportMinHalfExtent {
		t.Errorf("flat model height: got %v, want %v", hy, modelImportMinHalfExtent)
	}
	// An empty mesh must still produce usable bounds rather than zeros.
	ex, ey, ez := modelEntityHalfExtents(&ModelDoc{})
	if ex != modelImportMinHalfExtent || ey != modelImportMinHalfExtent || ez != modelImportMinHalfExtent {
		t.Errorf("empty mesh: got (%v,%v,%v), want all %v", ex, ey, ez, modelImportMinHalfExtent)
	}
}

func TestModelEntityHalfExtentsGiveAWorldAABB(t *testing.T) {
	// The whole point of deriving bounds is that WorldAABB accepts them: an
	// entity whose extents it rejects is never culled and never bounded.
	doc := &ModelDoc{Verts: []mgl32.Vec3{{-1, 0, -1}, {1, 0, 1}}}
	e := newModelEntity("plane", "models/plane.fmdl", "grass", mgl32.Vec3{5, 0, 5}, doc)
	min, max, ok := e.WorldAABB()
	if !ok {
		t.Fatalf("WorldAABB rejected derived extents (%v,%v,%v)", e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ)
	}
	if min.X() > 4 || max.X() < 6 {
		t.Errorf("bounds %v..%v do not contain the mesh at (5,0,5)", min, max)
	}
}

func TestNewModelEntityDefaults(t *testing.T) {
	doc := &ModelDoc{Verts: []mgl32.Vec3{{-0.5, -0.5, -0.5}, {0.5, 0.5, 0.5}}}
	pos := mgl32.Vec3{1, 2, 3}
	e := newModelEntity("rock", "/install/models/rock.fmdl", "", pos, doc)

	if e.ID != "rock" || e.ModelPath != "/install/models/rock.fmdl" {
		t.Errorf("identity: got id=%q path=%q", e.ID, e.ModelPath)
	}
	if e.Position != pos {
		t.Errorf("position = %v, want %v", e.Position, pos)
	}
	if e.Scale != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("scale = %v, want unit", e.Scale)
	}
	// An empty texture would leave untextured faces with no image at all, so
	// placement falls back to the engine's default.
	if e.TexturePath != modelImportFallbackTexture {
		t.Errorf("texture = %q, want %q", e.TexturePath, modelImportFallbackTexture)
	}
	if !e.Visible {
		t.Error("placed model should be visible")
	}
	if e.Alpha != 1 {
		t.Errorf("alpha = %v, want 1 (opaque)", e.Alpha)
	}
	if e.BoundingKind != BoundingAABB {
		t.Errorf("bounding kind = %v, want AABB", e.BoundingKind)
	}
}

func TestNewModelEntityKeepsAnExplicitTexture(t *testing.T) {
	e := newModelEntity("rock", "models/rock.fmdl", "stone_floor", mgl32.Vec3{}, &ModelDoc{})
	if e.TexturePath != "stone_floor" {
		t.Errorf("texture = %q, want %q", e.TexturePath, "stone_floor")
	}
}

func TestAssetPathRoundTrip(t *testing.T) {
	root := t.TempDir()
	prev := executablePath
	executablePath = root
	t.Cleanup(func() { executablePath = prev })

	abs := filepath.Join(root, "models", "rock.fmdl")

	// What the level saves is relative...
	rel := relAssetPath(abs)
	if rel != "models/rock.fmdl" {
		t.Fatalf("relAssetPath = %q, want %q", rel, "models/rock.fmdl")
	}
	// ...and what it loads resolves against the install root, not the working
	// directory.
	if got := resolveAssetPath(rel); got != abs {
		t.Errorf("resolveAssetPath = %q, want %q", got, abs)
	}

	// A path outside the install root has no shorter portable form, so it is
	// left absolute rather than written as a pile of "..".
	outside := filepath.Join(t.TempDir(), "shared", "tree.obj")
	if got := relAssetPath(outside); got != outside {
		t.Errorf("outside path = %q, want it unchanged", got)
	}
	if got := resolveAssetPath(outside); got != outside {
		t.Errorf("absolute path resolved to %q, want it unchanged", got)
	}

	// An entity with no model stays empty in both directions.
	if relAssetPath("") != "" || resolveAssetPath("") != "" {
		t.Error("empty model path should stay empty")
	}
}

func TestEntityToRawWritesPortableModelPath(t *testing.T) {
	root := t.TempDir()
	prev := executablePath
	executablePath = root
	t.Cleanup(func() { executablePath = prev })

	e := Entity{
		ID:        "rock",
		ModelPath: filepath.Join(root, "models", "rock.fmdl"),
		Scale:     mgl32.Vec3{1, 1, 1},
	}
	if got := entityToRaw(&e).Model; got != "models/rock.fmdl" {
		t.Errorf("saved model path = %q, want %q", got, "models/rock.fmdl")
	}
}

func TestFitEntityBoundsToModelReadsTheFileOnDisk(t *testing.T) {
	modelImportTestRoot(t)

	// A unit cube centred on the origin: half-extents of 0.5 on every axis.
	path := modelPath("cube")
	if err := SaveModel(NewCubeDoc(), path); err != nil {
		t.Fatalf("save model: %v", err)
	}

	// Bounds someone typed by hand, or that a since-remodelled version left
	// behind, must be replaced by what the file actually measures.
	e := Entity{ID: "cube", ModelPath: path, BoundHalfX: 9, BoundHalfY: 9, BoundHalfZ: 9}
	if err := fitEntityBoundsToModel(&e); err != nil {
		t.Fatalf("fit bounds: %v", err)
	}
	if e.BoundHalfX != 0.5 || e.BoundHalfY != 0.5 || e.BoundHalfZ != 0.5 {
		t.Errorf("bounds = (%v,%v,%v), want 0.5 on every axis",
			e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ)
	}

	// An entity with no model has nothing to measure, and its bounds are left
	// exactly as they were rather than collapsed to the empty-mesh minimum.
	noModel := Entity{ID: "trigger", BoundHalfX: 2, BoundHalfY: 2, BoundHalfZ: 2}
	if err := fitEntityBoundsToModel(&noModel); err == nil {
		t.Error("fitting an entity with no model should report an error")
	}
	if noModel.BoundHalfX != 2 {
		t.Errorf("bounds changed to %v despite the error", noModel.BoundHalfX)
	}
}

func TestPlacedModelIsFoundByTheCatalogAfterSaving(t *testing.T) {
	// The end-to-end shape of the feature, minus the GPU upload: a model saved
	// by the model editor turns up in the catalog, loads through the same
	// reader placement uses, and yields an entity whose stored path is the
	// portable form the level writes.
	modelImportTestRoot(t)
	if err := SaveModel(NewCubeDoc(), modelPath("crate")); err != nil {
		t.Fatalf("save model: %v", err)
	}

	files := ScanLevelModelFiles()
	if len(files) != 1 || files[0] != "crate"+modelExt {
		t.Fatalf("catalog = %v, want [crate%s]", files, modelExt)
	}

	path := filepath.Join(modelsDir(), files[0])
	doc, err := loadModelDocument(path)
	if err != nil {
		t.Fatalf("load %s: %v", files[0], err)
	}
	lvl := &Level{Entities: []Entity{{ID: "crate"}}}
	e := newModelEntity(uniqueEntityID(lvl, modelEntityBaseID(files[0])), path,
		modelImportFallbackTexture, mgl32.Vec3{2, 0, 2}, doc)

	if e.ID != "crate_1" {
		t.Errorf("id = %q, want crate_1 (crate is taken)", e.ID)
	}
	if got := entityToRaw(&e).Model; got != "models/crate"+modelExt {
		t.Errorf("saved model path = %q, want models/crate%s", got, modelExt)
	}
	if e.BoundHalfY != 0.5 {
		t.Errorf("bounds came from %v, want the cube's 0.5", e.BoundHalfY)
	}
}
