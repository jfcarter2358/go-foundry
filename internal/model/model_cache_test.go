package model

// model_cache_test.go — a model read through the cache is the model read from
// its YAML, and the cache can never serve a changed file.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withCache points the cache at a fresh directory for the test.
func withCache(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	prev := CacheDir
	CacheDir = dir
	t.Cleanup(func() { CacheDir = prev })
	return dir
}

// loadThreeWays loads path with no cache, through a cold cache and through the
// warm one, and fails t unless all three agree exactly.
func loadThreeWays(t *testing.T, path string) {
	t.Helper()
	prev := CacheDir
	CacheDir = ""
	want, err := LoadModel(path)
	CacheDir = prev
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, pass := range []string{"cold", "warm"} {
		got, err := LoadModel(path)
		if err != nil {
			t.Fatalf("%s (%s cache): %v", path, pass, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s: the %s-cache load differs from the YAML one", path, pass)
		}
	}
}

func riggedTestModel(t *testing.T, dir string) string {
	t.Helper()
	models := filepath.Join(dir, ModelsDirName)
	doc := NewCubeDoc()
	doc.Texture = "crate"
	bi := doc.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	doc.RenameBone(bi, "root")
	var all []int
	for i := range doc.Verts {
		all = append(all, i)
	}
	doc.AssignVertsToBone(all, bi, 1)
	ci := doc.AddClip("walk")
	doc.Clip(ci).Duration = 1
	doc.Clip(ci).SetKey(bi, 0, IdentBonePose())
	path := filepath.Join(models, "rig"+ModelExt)
	if err := SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}
	// And one linked to a skeleton file, so the .fskl goes through it too.
	linked := doc.Clone()
	linked.Skeleton = "rig"
	lpath := filepath.Join(models, "linked"+ModelExt)
	if err := SaveModel(linked, lpath); err != nil {
		t.Fatal(err)
	}
	return lpath
}

func TestTheModelCacheReadsWhatYAMLDid(t *testing.T) {
	cache := withCache(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, "box"+ModelExt)
	if err := SaveModel(NewCubeDoc(), plain); err != nil {
		t.Fatal(err)
	}
	loadThreeWays(t, plain)
	loadThreeWays(t, riggedTestModel(t, dir))
	entries, _ := os.ReadDir(cache)
	if len(entries) < 3 {
		t.Fatalf("%d cache entries, want one per model and skeleton", len(entries))
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// A changed file is a different entry: the cache keys on the bytes.
func TestTheModelCacheNeverServesAChangedFile(t *testing.T) {
	withCache(t)
	path := filepath.Join(t.TempDir(), "box"+ModelExt)
	doc := NewCubeDoc()
	if err := SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadModel(path); err != nil {
		t.Fatal(err)
	}
	doc.Verts[0] = mgl32.Vec3{7, 7, 7}
	doc.Texture = "changed"
	if err := SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadModel(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verts[0] != (mgl32.Vec3{7, 7, 7}) || got.Texture != "changed" {
		t.Fatalf("the cache served the old model: %v %q", got.Verts[0], got.Texture)
	}
}

// A damaged entry is ignored, not trusted.
func TestADamagedCacheEntryFallsBackToTheYAML(t *testing.T) {
	cache := withCache(t)
	path := filepath.Join(t.TempDir(), "box"+ModelExt)
	if err := SaveModel(NewCubeDoc(), path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var raw RawModel
	if err := os.WriteFile(filepath.Join(cache, CacheEntryName(data, &raw)), []byte{9, 9, 9}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadModel(path); err != nil {
		t.Fatalf("a damaged entry broke the load: %v", err)
	}
	loadThreeWays(t, path)
}

// A type that changes shape is a different fingerprint, so an engine that
// grows a field never reads an entry written without it.
func TestTheFingerprintFollowsTheType(t *testing.T) {
	type a struct {
		X []float32 `yaml:"x"`
	}
	type b struct {
		X []float32 `yaml:"x"`
		Y int       `yaml:"y"`
	}
	type c struct {
		X []float32 `yaml:"xx"`
	}
	fa, fb, fc := typeFingerprint(reflect.TypeOf(a{})), typeFingerprint(reflect.TypeOf(b{})), typeFingerprint(reflect.TypeOf(c{}))
	if fa == fb || fa == fc {
		t.Error("different types share a fingerprint")
	}
}

// Every model and skeleton of a real project, when FOUNDRY_MODEL_DIR names its
// resources directory.
func TestRealModelsReadTheSameThroughTheCache(t *testing.T) {
	res := os.Getenv("FOUNDRY_MODEL_DIR")
	if res == "" {
		t.Skip("set FOUNDRY_MODEL_DIR to a project's resources directory")
	}
	withCache(t)
	files, _ := filepath.Glob(filepath.Join(res, ModelsDirName, "*"+ModelExt))
	for _, f := range files {
		loadThreeWays(t, f)
	}
	t.Logf("%d models compared", len(files))
}

// BenchmarkLoadModel reads FOUNDRY_MODEL_BENCH (or a test cube) from YAML and
// through a warm cache.
func BenchmarkLoadModel(b *testing.B) {
	path := os.Getenv("FOUNDRY_MODEL_BENCH")
	if path == "" {
		path = filepath.Join(b.TempDir(), "box"+ModelExt)
		if err := SaveModel(NewCubeDoc(), path); err != nil {
			b.Fatal(err)
		}
	}
	dir := b.TempDir()
	for _, mode := range []struct {
		name, dir string
	}{{"yaml", ""}, {"cached", dir}} {
		b.Run(mode.name, func(b *testing.B) {
			prev := CacheDir
			CacheDir = mode.dir
			defer func() { CacheDir = prev }()
			if _, err := LoadModel(path); err != nil { // warms the cache
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := LoadModel(path); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
