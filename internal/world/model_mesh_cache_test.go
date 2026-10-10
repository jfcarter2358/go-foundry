package world

// model_mesh_cache_test.go — instances of a model share one built mesh, and an
// instance built that way is the attachment the unshared path builds.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/gfx"
	"foundry/internal/model"
)

func withoutUploads(t *testing.T) {
	t.Helper()
	prev := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	t.Cleanup(func() { gfx.GeometryUploadsEnabled = prev })
}

// riggedCube is a cube with a bone, a weight on every vertex and a clip: a
// model an instance animates, so the skinned build is exercised.
func riggedCube(t *testing.T, dir string) string {
	t.Helper()
	models := filepath.Join(dir, model.ModelsDirName)
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := model.NewCubeDoc()
	doc.Texture = "crate"
	bi := doc.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	doc.RenameBone(bi, "root")
	doc.AssignVertsToBone(allVertIndices(doc), bi, 1)
	ci := doc.AddClip("walk")
	doc.Clip(ci).Duration = 1
	doc.Clip(ci).SetKey(bi, 0, model.IdentBonePose())
	path := filepath.Join(models, "rig"+model.ModelExt)
	if err := model.SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func staticCube(t *testing.T, dir string) string {
	t.Helper()
	doc := model.NewCubeDoc()
	doc.Texture = "crate"
	path := filepath.Join(dir, "box"+model.ModelExt)
	if err := model.SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}
	return path
}

// loadAttachment loads one attachment with mesh sharing on or off.
func loadAttachment(t *testing.T, m EntityModel, share bool) EntityModel {
	t.Helper()
	prev := shareModelMeshes
	shareModelMeshes = share
	defer func() { shareModelMeshes = prev }()
	if err := m.Load("e"); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestASharedModelMeshIsTheUnsharedOne(t *testing.T) {
	withoutUploads(t)
	dir := t.TempDir()
	for _, path := range []string{staticCube(t, dir), riggedCube(t, dir)} {
		for _, m := range []EntityModel{
			{Name: "a", Path: path},
			{Name: "b", Path: path, Texture: "painted", Alpha: 0.5, Unlit: true, Material: "glow", Offset: mgl32.Vec3{1, 2, 3}},
		} {
			plain := loadAttachment(t, m, false)
			shared := loadAttachment(t, m, true)
			if len(plain.Submeshes) == 0 || len(plain.Submeshes) != len(shared.Submeshes) {
				t.Fatalf("%s: %d submeshes unshared, %d shared", path, len(plain.Submeshes), len(shared.Submeshes))
			}
			for i := range plain.Submeshes {
				if !reflect.DeepEqual(plain.Submeshes[i], shared.Submeshes[i]) {
					t.Errorf("%s %s: submesh %d differs:\n%+v\n%+v", path, m.Name, i, plain.Submeshes[i], shared.Submeshes[i])
				}
			}
			if (plain.Anim == nil) != (shared.Anim == nil) || (plain.Doc == nil) != (shared.Doc == nil) {
				t.Errorf("%s %s: animation state differs", path, m.Name)
			}
			if plain.Doc != nil && !reflect.DeepEqual(plain.Doc, shared.Doc) {
				t.Errorf("%s %s: the kept document differs", path, m.Name)
			}
		}
	}
}

// Two instances of one model draw from one set of arrays; an instance that
// overrides the texture changes only its own name for it.
func TestInstancesOfAModelShareItsMesh(t *testing.T) {
	withoutUploads(t)
	path := staticCube(t, t.TempDir())
	a := loadAttachment(t, EntityModel{Name: "a", Path: path}, true)
	b := loadAttachment(t, EntityModel{Name: "b", Path: path, Texture: "other"}, true)
	if &a.Submeshes[0].Vertices[0] != &b.Submeshes[0].Vertices[0] {
		t.Error("two instances of one model built two meshes")
	}
	if a.Submeshes[0].Texture != "crate" || b.Submeshes[0].Texture != "other" {
		t.Errorf("textures %q and %q", a.Submeshes[0].Texture, b.Submeshes[0].Texture)
	}
	b.SetTexture("third")
	if a.Submeshes[0].Texture != "crate" {
		t.Error("retexturing one instance retextured the other")
	}
}

// A model saved again builds afresh rather than serving the old mesh.
func TestASavedModelBuildsANewMesh(t *testing.T) {
	withoutUploads(t)
	path := staticCube(t, t.TempDir())
	a := loadAttachment(t, EntityModel{Name: "a", Path: path}, true)
	doc := model.NewCubeDoc()
	doc.Texture = "crate"
	doc.Verts[0] = mgl32.Vec3{5, 5, 5}
	if err := model.SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	b := loadAttachment(t, EntityModel{Name: "b", Path: path}, true)
	if &a.Submeshes[0].Vertices[0] == &b.Submeshes[0].Vertices[0] {
		t.Fatal("the re-saved model reused the old mesh")
	}
}

// BenchmarkLoadModelInstances loads twenty instances of one model, as a level
// places a squad: with the mesh built once and shared, and built per instance.
// FOUNDRY_MODEL_BENCH names a real .fmdl to use instead of the test cube.
func BenchmarkLoadModelInstances(b *testing.B) {
	prev := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	defer func() { gfx.GeometryUploadsEnabled = prev }()
	path := os.Getenv("FOUNDRY_MODEL_BENCH")
	if path == "" {
		doc := model.NewCubeDoc()
		path = filepath.Join(b.TempDir(), "box"+model.ModelExt)
		if err := model.SaveModel(doc, path); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := loadModelDocument(path); err != nil {
		b.Fatal(err)
	}
	for _, share := range []bool{false, true} {
		name := "per-instance"
		if share {
			name = "shared"
		}
		b.Run(name, func(b *testing.B) {
			prevShare := shareModelMeshes
			shareModelMeshes = share
			defer func() { shareModelMeshes = prevShare }()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ForgetUnusedModelMeshes()
				for k := 0; k < 20; k++ {
					m := EntityModel{Name: "m", Path: path}
					if err := m.Load("e"); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// The prefetch fills the cache the entity loop reads, from every core, and
// reports each document's texture.
func TestPrefetchModelDocumentsParsesEachOnce(t *testing.T) {
	dir := t.TempDir()
	box, rig := staticCube(t, dir), riggedCube(t, dir)
	tex := PrefetchModelDocuments([]string{box, rig, box, "", filepath.Join(dir, "missing.fmdl")})
	if tex[box] != "crate" || tex[rig] != "crate" {
		t.Errorf("textures %v", tex)
	}
	info, _ := os.Stat(box)
	if cachedModelDocument(box, info) == nil {
		t.Error("the prefetch left the document uncached")
	}
}
