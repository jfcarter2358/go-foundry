package world

// scenery_share_test.go — placements of one model share one built classic
// mesh (scenery_share.go): the arrays are the same arrays, the heap does not
// grow with the placement count, what a placement changes stays its own, a
// delete leaves the others whole, the last one empties the cache, and a
// re-cooked file builds afresh. With no GL; the buffers' half is
// render/scenery_share_gl_test.go.

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/nanitecook"
	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

// shareTestResources points the resources at a fresh directory and cooks
// the sample rock (subdivision level, seed) into models/rock.fnm in it.
//
// It also takes the cache's counts as they stand, which cached() reports
// against: another test's placement left loaded is not this test's.
func shareTestResources(t *testing.T, level int, seed float32) string {
	t.Helper()
	cacheBase[0], cacheBase[1] = sceneryClassicCached()
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	cookShareRock(t, dir, level, seed)
	return dir
}

func cookShareRock(t *testing.T, dir string, level int, seed float32) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "models", "rock.fnm")
	if _, err := nanitecook.CookMeshFile(nanitecook.SampleRock(level, seed), out, true, nanitecook.Options{}); err != nil {
		t.Fatal(err)
	}
}

// loadRocks loads n placements of the cooked rock in a row.
func loadRocks(t *testing.T, n int) []Scenery {
	t.Helper()
	out := make([]Scenery, n)
	for i := range out {
		out[i] = NewScenery("rock", "models/rock.fnm", mgl32.Vec3{float32(i), 0, 0})
		if err := out[i].Load(); err != nil {
			t.Fatal(err)
		}
		if out[i].NaniteGPU || len(out[i].Submeshes) == 0 {
			t.Fatalf("placement %d: GPU path %v, %d submeshes", i, out[i].NaniteGPU, len(out[i].Submeshes))
		}
	}
	t.Cleanup(func() {
		for i := range out {
			out[i].Delete()
		}
	})
	return out
}

var cacheBase [2]int

// cached is sceneryClassicCached since shareTestResources.
func cached() (meshes, claims int) {
	m, c := sceneryClassicCached()
	return m - cacheBase[0], c - cacheBase[1]
}

func sameArray[T any](a, b []T) bool {
	return len(a) > 0 && len(a) == len(b) && &a[0] == &b[0]
}

func TestPlacementsOfACookedMeshShareOneClassicMesh(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	shareTestResources(t, 4, 0.3)
	rocks := loadRocks(t, 8)
	if m, c := cached(); m != 1 || c != 8 {
		t.Fatalf("cache holds %d meshes with %d claims, want 1 with 8", m, c)
	}
	a := &rocks[0].Submeshes[0]
	if a.ShadowLOD == nil {
		t.Fatal("the sample rock has no shadow stand-in: the test does not cover it")
	}
	for i := 1; i < len(rocks); i++ {
		b := &rocks[i].Submeshes[0]
		if !sameArray(a.Vertices, b.Vertices) || !sameArray(a.Indices, b.Indices) || !sameArray(a.Normals, b.Normals) {
			t.Fatalf("placement %d built its own mesh", i)
		}
		if b.ShadowLOD == a.ShadowLOD || !sameArray(a.ShadowLOD.Vertices, b.ShadowLOD.Vertices) {
			t.Fatalf("placement %d: the shadow stand-in is not a copy over the shared arrays", i)
		}
	}

	// What a placement changes is its own: alpha (the editor ghost) and the
	// texture (a Materials override).
	rocks[1].SetAlpha(0.4)
	if a.Alpha != 1 || rocks[2].Submeshes[0].Alpha != 1 {
		t.Fatal("SetAlpha on one placement reached another")
	}
	slot := rocks[0].Nanite.Slots[0].Name
	moss := NewScenery("moss", "models/rock.fnm", mgl32.Vec3{})
	moss.Materials = map[string]string{slot: "moss"}
	if err := moss.Load(); err != nil {
		t.Fatal(err)
	}
	defer moss.Delete()
	if moss.Submeshes[0].Texture != "moss" || moss.Submeshes[0].ShadowLOD.Texture != "moss" || a.Texture == "moss" {
		t.Fatalf("textures: override %q (stand-in %q), plain %q", moss.Submeshes[0].Texture, moss.Submeshes[0].ShadowLOD.Texture, a.Texture)
	}
	if !sameArray(a.Vertices, moss.Submeshes[0].Vertices) {
		t.Fatal("a texture override built a mesh of its own: the texture is a draw-time name")
	}
}

// What a placement draws is what it drew when it built its own.
func TestASharedClassicMeshIsTheUnsharedOne(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	shareTestResources(t, 4, 0.3)
	load := func(share bool) Scenery {
		prev := shareSceneryMeshes
		shareSceneryMeshes = share
		defer func() { shareSceneryMeshes = prev }()
		s := NewScenery("rock", "models/rock.fnm", mgl32.Vec3{1, 2, 3})
		s.Materials = map[string]string{"moss": "snow"}
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		return s
	}
	plain, shared := load(false), load(true)
	defer plain.Delete()
	defer shared.Delete()
	if len(plain.Submeshes) == 0 || !reflect.DeepEqual(plain.Submeshes, shared.Submeshes) {
		t.Fatalf("the shared submeshes differ from the unshared:\n%+v\n%+v", plain.Submeshes, shared.Submeshes)
	}
	if m, c := cached(); m != 1 || c != 1 {
		t.Fatalf("cache holds %d meshes with %d claims: the unshared build went in it", m, c)
	}
}

// The heap a placement adds is its own few structs, not a mesh.
func TestClassicSceneryMemoryIsFlatInPlacements(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	shareTestResources(t, 5, 0.3)
	heap := func() uint64 {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	h0 := heap()
	one := loadRocks(t, 1)
	h1 := heap()
	many := loadRocks(t, 64)
	h2 := heap()
	mesh := int64(h1) - int64(h0)
	per := (int64(h2) - int64(h1)) / int64(len(many))
	t.Logf("first placement %d KB (the mesh), each further one %d B", mesh/1024, per)
	if mesh < 256<<10 {
		t.Fatalf("the first placement took %d B: the rock is too small to tell sharing apart", mesh)
	}
	if per*20 > mesh {
		t.Fatalf("each further placement adds %d B, over a twentieth of the mesh's %d B", per, mesh)
	}
	_ = one
}

func TestDeletingOnePlacementLeavesTheOthersWhole(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	shareTestResources(t, 4, 0.3)
	rocks := loadRocks(t, 3)
	verts := len(rocks[1].Submeshes[0].Vertices)
	first := rocks[1].Submeshes[0].Vertices[0]

	rocks[0].Delete()
	if m, c := cached(); m != 1 || c != 2 {
		t.Fatalf("after one delete: %d meshes, %d claims", m, c)
	}
	for _, s := range rocks[1:] {
		if !s.HasGeometry() || len(s.Submeshes[0].Vertices) != verts || s.Submeshes[0].Vertices[0] != first {
			t.Fatal("deleting one placement emptied another")
		}
	}

	// A copy of a placement shares its claim: deleting both counts once.
	cp := rocks[1]
	cp.Delete()
	rocks[1].Delete()
	if m, c := cached(); m != 1 || c != 1 {
		t.Fatalf("a value copy deleted twice: %d meshes, %d claims, want 1 and 1", m, c)
	}
	if !rocks[2].HasGeometry() {
		t.Fatal("the last placement lost its mesh")
	}

	rocks[2].Delete()
	if m, c := cached(); m != 0 || c != 0 {
		t.Fatalf("after the last delete the cache holds %d meshes, %d claims", m, c)
	}
	// And the next load builds afresh.
	again := loadRocks(t, 1)
	if !again[0].HasGeometry() {
		t.Fatal("a load after the cache emptied drew nothing")
	}
}

// A placement reloaded after its file was re-cooked builds the new mesh; the
// one still drawing the old keeps it until it reloads too.
func TestReloadAfterTheModelChangedBuildsAfresh(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	dir := shareTestResources(t, 3, 0.3)
	rocks := loadRocks(t, 2)
	old := rocks[1].Submeshes[0].Vertices

	cookShareRock(t, dir, 4, 0.6)
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "models", "rock.fnm"), later, later); err != nil {
		t.Fatal(err)
	}
	if err := rocks[0].Load(); err != nil {
		t.Fatal(err)
	}
	if sameArray(rocks[0].Submeshes[0].Vertices, old) || len(rocks[0].Submeshes[0].Vertices) == len(old) {
		t.Fatal("the reloaded placement still draws the old cook")
	}
	if !sameArray(rocks[1].Submeshes[0].Vertices, old) {
		t.Fatal("reloading one placement changed another's mesh")
	}
	if m, c := cached(); m != 2 || c != 2 {
		t.Fatalf("mid-change: %d meshes, %d claims, want 2 and 2", m, c)
	}
	if err := rocks[1].Load(); err != nil {
		t.Fatal(err)
	}
	if m, c := cached(); m != 1 || c != 2 {
		t.Fatalf("after both reloaded: %d meshes, %d claims, want 1 and 2", m, c)
	}
	if !sameArray(rocks[0].Submeshes[0].Vertices, rocks[1].Submeshes[0].Vertices) {
		t.Fatal("the two reloaded placements do not share the new mesh")
	}

	// A model path changed in the editor: the same, under the other file.
	rocks[1].Model = "models/other.fnm"
	if err := os.Rename(filepath.Join(dir, "models", "rock.fnm"), filepath.Join(dir, "models", "other.fnm")); err != nil {
		t.Fatal(err)
	}
	if err := rocks[1].Load(); err != nil {
		t.Fatal(err)
	}
	if !rocks[1].HasGeometry() || sameArray(rocks[0].Submeshes[0].Vertices, rocks[1].Submeshes[0].Vertices) {
		t.Fatal("the placement with the new path did not load its own file's mesh")
	}
}

// An uncooked model's placements share the mesh entity attachments of it
// build (model_mesh_cache.go).
func TestPlacementsOfAModelFileShareItsMesh(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	staticCube(t, dir)
	a := NewScenery("a", "box.fmdl", mgl32.Vec3{})
	b := NewScenery("b", "box.fmdl", mgl32.Vec3{3, 0, 0})
	for _, s := range []*Scenery{&a, &b} {
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		defer s.Delete()
	}
	if len(a.Submeshes) == 0 || !sameArray(a.Submeshes[0].Vertices, b.Submeshes[0].Vertices) {
		t.Fatal("two placements of one model file built two meshes")
	}
	if a.Submeshes[0].Texture != "crate" || !a.HasBounds() {
		t.Fatalf("texture %q, bounds %v", a.Submeshes[0].Texture, a.HasBounds())
	}
	b.SetAlpha(0.5)
	if a.Submeshes[0].Alpha != 1 {
		t.Fatal("SetAlpha on one placement reached the other")
	}
}
