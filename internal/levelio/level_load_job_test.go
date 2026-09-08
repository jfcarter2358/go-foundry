package levelio

// level_load_job_test.go — the stepped load must build exactly what the
// synchronous one does.
//
// That is the whole safety argument for having two drivers over one loader, and
// it is the only thing standing between "the loading screen animates" and "the
// loading screen animates and the level is subtly different".

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/testenv"
	"foundry/internal/world"
)

// withResidentTextures marks every shipped texture as already uploaded, so a
// load can be exercised with no GL context. The round-trip test does the same;
// what is under test here is the loader's sequencing, not its texture uploads.
func withResidentTextures(t *testing.T) {
	t.Helper()
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	entries, _ := os.ReadDir(filepath.Join(testenv.RepoRoot(t), "resources", "textures"))
	names := []string{"", "default"}
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
	}
	for _, n := range names {
		gfx.Textures[n] = 1
		gfx.TextureNames[n] = n
	}
}

// withResourcesRoot points the install root at resources/, so a level that
// places an entity finds the type's definition, its chunk attachments and its
// script where a real run would. Without it a shipped level with entities in it
// fails to load here and nowhere else.
func withResourcesRoot(t *testing.T) {
	t.Helper()
	prev := fileutil.ExecutablePath
	fileutil.ExecutablePath = filepath.Join(testenv.RepoRoot(t), "resources")
	world.ReloadEntityDefs()
	t.Cleanup(func() {
		fileutil.ExecutablePath = prev
		world.ReloadEntityDefs()
	})
}

// shippedLevels are the level files that come with the engine.
func shippedLevels(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(testenv.RepoRoot(t), "resources", LevelsDirName)
	names := ScanLevelFiles(dir)
	if len(names) == 0 {
		t.Skip("no levels in resources/levels")
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(dir, n)
	}
	return out
}

func TestSteppedLoadMatchesTheSynchronousOne(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	withResidentTextures(t)
	withResourcesRoot(t)

	for _, path := range shippedLevels(t) {
		t.Run(strings.TrimSuffix(filepath.Base(path), LevelExt), func(t *testing.T) {
			want, err := LoadLevel(path)
			if err != nil {
				t.Fatalf("synchronous load: %v", err)
			}

			job, err := BeginLoad(path)
			if err != nil {
				t.Fatalf("BeginLoad: %v", err)
			}
			steps := 0
			for !job.Step() {
				steps++
				if steps > 100000 {
					t.Fatal("the stepped load never finished")
				}
			}
			if err := job.Err(); err != nil {
				t.Fatalf("stepped load: %v", err)
			}
			got := job.Level()

			// Compare what a level *is* rather than the whole struct: geometry
			// carries GL handles that differ between two builds by design.
			if len(got.Chunks) != len(want.Chunks) {
				t.Errorf("%d chunks, want %d", len(got.Chunks), len(want.Chunks))
			}
			if len(got.Entities) != len(want.Entities) {
				t.Errorf("%d entities, want %d", len(got.Entities), len(want.Entities))
			}
			for i := range got.Chunks {
				if i >= len(want.Chunks) {
					break
				}
				if got.Chunks[i].ID != want.Chunks[i].ID {
					t.Errorf("chunk %d is %q, want %q", i, got.Chunks[i].ID, want.Chunks[i].ID)
				}
				if a, b := len(got.Chunks[i].Brushes), len(want.Chunks[i].Brushes); a != b {
					t.Errorf("chunk %q has %d brushes, want %d", got.Chunks[i].ID, a, b)
				}
			}
			if !reflect.DeepEqual(got.UI, want.UI) {
				t.Errorf("UI list is %v, want %v", got.UI, want.UI)
			}
			if !reflect.DeepEqual(got.Scripts, want.Scripts) {
				t.Errorf("scripts list is %v, want %v", got.Scripts, want.Scripts)
			}
			if (got.Spawn == nil) != (want.Spawn == nil) {
				t.Errorf("spawn presence differs: %v vs %v", got.Spawn != nil, want.Spawn != nil)
			}
			if got.Spatial == nil {
				t.Error("the stepped load produced no spatial hash; the final stage did not run")
			}
		})
	}
}

func TestSteppedLoadTakesMoreThanOneStep(t *testing.T) {
	// A job that finished in one step would draw exactly one frame of loading
	// screen, which is the thing this whole mechanism exists to avoid.
	testenv.WithoutGeometryUploads(t)
	withResidentTextures(t)

	job, err := BeginLoad(shippedLevels(t)[0])
	if err != nil {
		t.Fatalf("BeginLoad: %v", err)
	}
	steps := 0
	for !job.Step() {
		steps++
	}
	if steps < 3 {
		t.Errorf("the load took %d steps; nothing would be visible", steps)
	}
}

func TestProgressRisesToOne(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	withResidentTextures(t)

	job, err := BeginLoad(shippedLevels(t)[0])
	if err != nil {
		t.Fatalf("BeginLoad: %v", err)
	}

	prev, label := job.Progress()
	if prev < 0 || prev > 1 {
		t.Fatalf("initial progress is %v", prev)
	}
	if label == "" {
		t.Error("no stage label to show")
	}

	for !job.Step() {
		p, l := job.Progress()
		if p < prev {
			t.Errorf("progress went backwards: %v then %v", prev, p)
		}
		if p > 1 {
			t.Errorf("progress exceeded 1: %v", p)
		}
		if l == "" {
			t.Error("a step reported no label")
		}
		prev = p
	}
	if p, _ := job.Progress(); p != 1 {
		t.Errorf("finished at %v, want 1", p)
	}
}

func TestProgressIsPublishedForScripts(t *testing.T) {
	// A bar bound to @level.prop.load_progress reads this, and nothing else
	// connects the two.
	testenv.WithoutGeometryUploads(t)
	withResidentTextures(t)
	hooks.ClearLoadProgress()

	job, err := BeginLoad(shippedLevels(t)[0])
	if err != nil {
		t.Fatalf("BeginLoad: %v", err)
	}
	job.Step()

	if got := hooks.LoadProgress(); got <= 0 || got > 1 {
		t.Errorf("published progress is %v", got)
	}
	if hooks.LoadStage() == "" {
		t.Error("no stage was published")
	}

	for !job.Step() {
	}
	hooks.ClearLoadProgress()
	if hooks.LoadProgress() != 1 || hooks.LoadStage() != "" {
		t.Error("clearing did not read as a finished load")
	}
}

func TestBeginLoadReportsAMissingFileImmediately(t *testing.T) {
	// A file that will not parse is a failure the caller should see before it
	// has put a loading screen up over nothing.
	if _, err := BeginLoad(filepath.Join(t.TempDir(), "nope"+LevelExt)); err == nil {
		t.Error("BeginLoad accepted a file that does not exist")
	}
}

func TestSteppedLoadOfAnEmptyLevelStillFinishes(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	withResidentTextures(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "blank"+LevelExt)
	if err := WriteBlankLevel(path); err != nil {
		t.Fatal(err)
	}

	job, err := BeginLoad(path)
	if err != nil {
		t.Fatalf("BeginLoad: %v", err)
	}
	for i := 0; !job.Step(); i++ {
		if i > 1000 {
			t.Fatal("never finished")
		}
	}
	if job.Err() != nil {
		t.Fatalf("blank level: %v", job.Err())
	}
	if job.Level().Spatial == nil {
		t.Error("no spatial hash")
	}
}

func TestLevelPathAcceptsBothSpellings(t *testing.T) {
	if a, b := LevelPath("canyon"), LevelPath("canyon"+LevelExt); a != b {
		t.Errorf("LevelPath disagreed with itself: %q vs %q", a, b)
	}
}
