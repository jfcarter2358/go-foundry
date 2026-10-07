package levelio

// compiled_load_job_test.go — a compiled load reads its file off the main
// thread, and still fails the way it did when the read was synchronous.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"foundry/internal/compile"
)

func savedFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture"+CompiledExt)
	// Without the ship: a frame owner names an entity a load would need.
	c, err := CompileLevel(sealedLevel(), compile.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveCompiledLevel(c, path); err != nil {
		t.Fatal(err)
	}
	return path
}

// BeginLoadCompiled returns before the file has decoded, in a stage of its own,
// and the job pumped by StepFor — as the frame loop pumps it — builds the same
// level the synchronous loader does.
func TestACompiledLoadReadsOffTheMainThread(t *testing.T) {
	withResidentTextures(t)
	defer withHeadlessGFX()()
	path := savedFixture(t)

	job, err := BeginLoadCompiled(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, label := job.Progress(); label != loadStageNames[loadStageReading] {
		t.Errorf("a compiled load began in %q, not reading", label)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !job.StepFor(12 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the load never finished")
		}
		time.Sleep(time.Millisecond) // the rest of a frame
	}
	if err := job.Err(); err != nil {
		t.Fatal(err)
	}
	want, err := LoadCompiledLevel(path)
	if err != nil {
		t.Fatal(err)
	}
	got := job.Level()
	if d := firstDiff(reflect.ValueOf(want.Static), reflect.ValueOf(got.Static), "static"); d != "" {
		t.Errorf("the stepped load built different surfaces: %s", d)
	}
	if len(got.Chunks) != len(want.Chunks) {
		t.Errorf("%d chunks, want %d", len(got.Chunks), len(want.Chunks))
	}
}

// A file that is plainly not going to load is refused by BeginLoadCompiled
// itself, before a caller puts a loading screen up — as it was when the whole
// read happened there.
func TestACompiledLoadRefusesAnUnloadableFileAtOnce(t *testing.T) {
	dir := t.TempDir()
	if _, err := BeginLoadCompiled(filepath.Join(dir, "missing"+CompiledExt)); err == nil {
		t.Error("a missing file began loading")
	}
	yamlFile := filepath.Join(dir, "level"+CompiledExt)
	os.WriteFile(yamlFile, []byte("chunks: []\n"), 0o644)
	if _, err := BeginLoadCompiled(yamlFile); err == nil {
		t.Error("a file that is not a compiled level began loading")
	}
	c := compileFixture(t)
	c.Version = CompiledVersion + 1
	stale := filepath.Join(dir, "stale"+CompiledExt)
	if err := SaveCompiledLevel(c, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginLoadCompiled(stale); err == nil {
		t.Error("a file from another compiler version began loading")
	}
}

// A file whose header is fine and whose body is not fails the JOB, through
// Err, like any other step that fails — and not with a panic or a level built
// from half a file.
func TestADamagedCompiledFileFailsTheJob(t *testing.T) {
	withResidentTextures(t)
	defer withHeadlessGFX()()
	path := savedFixture(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-5] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	job, err := BeginLoadCompiled(path)
	if err != nil {
		t.Fatalf("the header check should have passed: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !job.StepFor(12 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the load never finished")
		}
	}
	if job.Err() == nil {
		t.Fatal("a damaged file loaded")
	}
	if _, err := LoadCompiledLevel(path); err == nil {
		t.Fatal("a damaged file loaded synchronously")
	}
}
