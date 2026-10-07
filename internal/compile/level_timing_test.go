package compile_test

// level_timing_test.go — how long each stage of a real level's compile takes.
//
// Skipped unless FOUNDRY_COMPILE_LEVEL names a level .yaml inside a project
// (…/resources/levels/<name>.yaml): the levels worth timing are the user's,
// not anything small enough to keep in the repo. Run it with
//
//	FOUNDRY_COMPILE_LEVEL=/path/to/proj/resources/levels/level1.yaml \
//	    go test ./internal/compile -run TestCompileStageTimes -v -cpuprofile cpu.prof
//
// It prints the wall time of every stage the compile reports through Progress,
// which is the breakdown the CLI's one-line summary does not give.

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/compile"
	"foundry/internal/fileutil"
	"foundry/internal/levelio"
)

func TestCompileStageTimes(t *testing.T) {
	path := os.Getenv("FOUNDRY_COMPILE_LEVEL")
	if path == "" {
		t.Skip("FOUNDRY_COMPILE_LEVEL not set")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	// …/<root>/resources/levels/x.yaml → <root>
	fileutil.ResourcesPath = fileutil.ResourcesDir(filepath.Dir(filepath.Dir(filepath.Dir(abs))))
	lvl, err := levelio.LoadLevelHeadless(abs)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	stage, began := "", time.Now()
	start := began
	opt := compile.DefaultOptions()
	opt.Progress = func(s string, _ float32) {
		mu.Lock()
		defer mu.Unlock()
		if s == stage {
			return
		}
		now := time.Now()
		if stage != "" {
			t.Logf("%8.2fs  %s", now.Sub(began).Seconds(), stage)
		}
		stage, began = s, now
	}
	res, err := compile.Compile(&lvl, opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%8.2fs  %s", time.Since(began).Seconds(), stage)
	t.Logf("total %.2fs: %s", time.Since(start).Seconds(), res.Stats.Summary())
}

// TestCompiledFilesMatch compares two .fbsp files of one level by what they
// hold rather than byte for byte — the file's encoding of the level's maps is
// not in a fixed order, so two compiles of one level differ as bytes even when
// they agree on everything. Faces (light to the bit), tree, PVS, stats,
// clusters and chunks are compared. Skipped unless FOUNDRY_FBSP_A and
// FOUNDRY_FBSP_B name the files.
func TestCompiledFilesMatch(t *testing.T) {
	pa, pb := os.Getenv("FOUNDRY_FBSP_A"), os.Getenv("FOUNDRY_FBSP_B")
	if pa == "" || pb == "" {
		t.Skip("FOUNDRY_FBSP_A / FOUNDRY_FBSP_B not set")
	}
	a, err := levelio.ReadCompiledLevel(pa)
	if err != nil {
		t.Fatal(err)
	}
	b, err := levelio.ReadCompiledLevel(pb)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Faces) != len(b.Faces) {
		t.Fatalf("%d faces against %d", len(a.Faces), len(b.Faces))
	}
	bitsOf := func(vs []mgl32.Vec3) []uint32 {
		out := make([]uint32, 0, 3*len(vs))
		for _, v := range vs {
			out = append(out, math.Float32bits(v[0]), math.Float32bits(v[1]), math.Float32bits(v[2]))
		}
		return out
	}
	lit := 0
	for fi := range a.Faces {
		fa, fb := &a.Faces[fi], &b.Faces[fi]
		for k, pair := range [][2][]mgl32.Vec3{
			{fa.Points, fb.Points}, {fa.Light, fb.Light}, {fa.BackLight, fb.BackLight},
			{fa.SmoothLight, fb.SmoothLight}, {fa.SmoothBackLight, fb.SmoothBackLight},
			{fa.Indirect, fb.Indirect}, {fa.BackIndirect, fb.BackIndirect},
			{fa.SmoothIndirect, fb.SmoothIndirect}, {fa.SmoothBackIndirect, fb.SmoothBackIndirect},
		} {
			if !reflect.DeepEqual(bitsOf(pair[0]), bitsOf(pair[1])) {
				t.Fatalf("face %d differs in field %d", fi, k)
			}
		}
		lit += len(fa.Light)
	}
	if !reflect.DeepEqual(a.Faces, b.Faces) {
		t.Fatal("the faces differ")
	}
	for name, eq := range map[string]bool{
		"tree":     reflect.DeepEqual(a.Tree, b.Tree),
		"pvs":      reflect.DeepEqual(a.PVS, b.PVS),
		"stats":    reflect.DeepEqual(a.Stats, b.Stats),
		"clusters": reflect.DeepEqual(a.Clusters, b.Clusters),
		"chunks":   reflect.DeepEqual(a.Chunks, b.Chunks),
	} {
		if !eq {
			t.Errorf("the %s differ", name)
		}
	}
	t.Logf("%d faces, %d lit points: identical", len(a.Faces), lit)
}
