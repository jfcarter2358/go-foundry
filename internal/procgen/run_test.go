package procgen

// run_test.go — the run engine: incremental, forced, single-stage and
// cancelled runs, the manifest, placements and the preview cache.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func pipeline() *Doc {
	return &Doc{Name: "pipe", Seed: 5, Stages: []Stage{
		{Kind: "tsource", ID: "src", Params: Params{"count": Int(4)}},
		{Kind: "tsink", ID: "sink", Params: Params{"from": StageRef("src")}},
	}}
}

// A stage that supersedes an upstream stage's placements replaces them in
// the run's placements, and still does when it is skipped as fresh.
func TestSupersededPlacementsAreReplaced(t *testing.T) {
	dir := t.TempDir()
	d := pipeline()
	d.Stages[1].Params["replace"] = Int(2)
	for run := 0; run < 2; run++ {
		res, err := Run(context.Background(), d, Options{OutDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		if run == 1 && len(res.Ran) != 0 {
			t.Fatalf("second run ran %v", res.Ran)
		}
		got := map[string]float32{}
		for _, p := range res.Placements() {
			got[p.ID] = p.Position.Y
		}
		want := map[string]float32{"gen.pipe.src.r002": 0, "gen.pipe.src.r003": 0,
			"gen.pipe.sink.r000": 10, "gen.pipe.sink.r001": 10}
		if len(got) != len(want) {
			t.Fatalf("run %d: placements %v, want %v", run, got, want)
		}
		for id, y := range want {
			if g, ok := got[id]; !ok || g != y {
				t.Fatalf("run %d: placements %v, want %v", run, got, want)
			}
		}
	}
}

func resetCounts() {
	sourceRuns.Store(0)
	sinkRuns.Store(0)
}

func TestRunIsIncremental(t *testing.T) {
	dir := t.TempDir()
	d := pipeline()
	resetCounts()
	res, err := Run(context.Background(), d, Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if sourceRuns.Load() != 1 || sinkRuns.Load() != 1 || len(res.Ran) != 2 {
		t.Fatalf("first run: ran %v (source %d sink %d)", res.Ran, sourceRuns.Load(), sinkRuns.Load())
	}
	first, _ := os.ReadFile(filepath.Join(dir, "src", "data.txt"))

	// Unchanged: nothing runs, and the result still carries the placements.
	res, err = Run(context.Background(), d, Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Ran) != 0 || len(res.Skipped) != 2 {
		t.Fatalf("unchanged run ran %v", res.Ran)
	}
	if n := len(res.Placements()); n != 4 {
		t.Errorf("skipped run has %d placements, want 4", n)
	}
	if id := res.Placements()[0].ID; id != "gen.pipe.src.r000" {
		t.Errorf("placement ID %q, want the gen.<doc>.<stage>. prefix", id)
	}
	if v := res.Volumes(); len(v) != 1 || v[0]["id"] != "gen.pipe.sink.zone" {
		t.Errorf("volumes %v", v)
	}

	// The downstream stage reran once with its upstream SKIPPED: it read the
	// upstream's artifact through Load.
	d.Stages[1].Params["scale"] = Float(3)
	res, err = Run(context.Background(), d, Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Ran, ",") != "sink" {
		t.Fatalf("changing the sink ran %v", res.Ran)
	}
	copied, _ := os.ReadFile(filepath.Join(dir, "sink", "copy.txt"))
	if string(copied) != string(first) {
		t.Errorf("sink read %q through Load, want %q", copied, first)
	}

	// An upstream change reruns it and what depends on it.
	d.Stages[0].Params["count"] = Int(6)
	res, _ = Run(context.Background(), d, Options{OutDir: dir})
	if strings.Join(res.Ran, ",") != "src,sink" {
		t.Errorf("changing the source ran %v", res.Ran)
	}

	// A deleted output makes its stage stale.
	os.Remove(filepath.Join(dir, "sink", "copy.txt"))
	res, _ = Run(context.Background(), d, Options{OutDir: dir})
	if strings.Join(res.Ran, ",") != "sink" {
		t.Errorf("a deleted output ran %v", res.Ran)
	}

	// Force reruns everything.
	res, _ = Run(context.Background(), d, Options{OutDir: dir, Force: true})
	if len(res.Ran) != 2 {
		t.Errorf("Force ran %v", res.Ran)
	}
}

func TestRunIsDeterministic(t *testing.T) {
	// The same folder twice (placements name their models by the folder's
	// path), forced, with different worker counts.
	dir := t.TempDir()
	files := []string{"src/data.txt", "src/placements.yaml", "sink/copy.txt", "manifest.yaml"}
	read := func() []string {
		var out []string
		for _, f := range files {
			b, _ := os.ReadFile(filepath.Join(dir, f))
			out = append(out, string(b))
		}
		return out
	}
	if _, err := Run(context.Background(), pipeline(), Options{OutDir: dir}); err != nil {
		t.Fatal(err)
	}
	first := read()
	if _, err := Run(context.Background(), pipeline(), Options{OutDir: dir, Workers: 1, Force: true}); err != nil {
		t.Fatal(err)
	}
	for i, s := range read() {
		if s != first[i] || s == "" {
			t.Errorf("%s differs between runs (or is empty)", files[i])
		}
	}
}

func TestRunOnlyRunsThatStageAndStaleUpstream(t *testing.T) {
	dir := t.TempDir()
	d := pipeline()
	resetCounts()
	// Nothing has run: asking for the sink must run the source first.
	res, err := Run(context.Background(), d, Options{OutDir: dir, Only: []string{"sink"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Ran, ",") != "src,sink" {
		t.Fatalf("Only sink on a clean folder ran %v", res.Ran)
	}
	// Now fresh: Only reruns just it.
	res, _ = Run(context.Background(), d, Options{OutDir: dir, Only: []string{"sink"}})
	if strings.Join(res.Ran, ",") != "sink" {
		t.Errorf("Only sink ran %v", res.Ran)
	}
	if _, err := Run(context.Background(), d, Options{OutDir: dir, Only: []string{"nope"}}); err == nil {
		t.Error("Only with an unknown stage ran")
	}
}

func TestStaleStages(t *testing.T) {
	dir := t.TempDir()
	d := pipeline()
	stale, err := StaleStages(d, Options{OutDir: dir})
	if err != nil || strings.Join(stale, ",") != "src,sink" {
		t.Fatalf("clean folder stale = %v %v", stale, err)
	}
	Run(context.Background(), d, Options{OutDir: dir})
	if stale, _ := StaleStages(d, Options{OutDir: dir}); len(stale) != 0 {
		t.Errorf("after a run stale = %v", stale)
	}
}

func TestFailedStageKeepsEarlierWorkAndLeavesNoEntry(t *testing.T) {
	dir := t.TempDir()
	d := &Doc{Name: "f", Seed: 1, Stages: []Stage{
		{Kind: "tsource", ID: "good"},
		{Kind: "tsource", ID: "bad", Params: Params{"fail": Bool(true)}},
	}}
	res, err := Run(context.Background(), d, Options{OutDir: dir})
	if err == nil || !strings.Contains(err.Error(), "stage bad: asked to fail") {
		t.Fatalf("err = %v", err)
	}
	if len(res.Ran) != 1 {
		t.Errorf("ran %v", res.Ran)
	}
	man := ReadManifest(dir)
	if _, ok := man.Stages["good"]; !ok {
		t.Error("the finished stage lost its manifest entry")
	}
	if _, ok := man.Stages["bad"]; ok {
		t.Error("the failed stage has a manifest entry")
	}
}

func TestCancelMidStage(t *testing.T) {
	dir := t.TempDir()
	slowGate = make(chan struct{})
	defer func() { slowGate = nil }()
	resetCounts()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, pipeline(), Options{OutDir: dir})
		done <- err
	}()
	if !waitFor(func() bool { return sourceRuns.Load() == 1 }) {
		t.Fatal("the stage never started")
	}
	cancel()
	err := <-done
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	if sinkRuns.Load() != 0 {
		t.Error("a stage after the cancel ran")
	}
	if _, ok := ReadManifest(dir).Stages["src"]; ok {
		t.Error("the cancelled stage has a manifest entry")
	}
}

func TestRemovedStageOutputsArePruned(t *testing.T) {
	dir := t.TempDir()
	d := pipeline()
	Run(context.Background(), d, Options{OutDir: dir})
	d.Stages = d.Stages[:1]
	if _, err := Run(context.Background(), d, Options{OutDir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sink", "copy.txt")); !os.IsNotExist(err) {
		t.Error("a removed stage's output is still there")
	}
	if _, ok := ReadManifest(dir).Stages["sink"]; ok {
		t.Error("a removed stage is still in the manifest")
	}
}

func TestDisabledStageIsNotRunOrSpliced(t *testing.T) {
	dir := t.TempDir()
	d := pipeline()
	d.Stages[1].Disabled = true
	res, err := Run(context.Background(), d, Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stage("sink") != nil || len(res.Volumes()) != 0 {
		t.Error("a disabled stage is in the result")
	}
}

func TestParallelStopsOnErrorAndRecoversPanics(t *testing.T) {
	var ran atomic.Int64
	err := parallel(context.Background(), 4, 1000, func(i int) error {
		ran.Add(1)
		if i == 10 {
			return errors.New("ten")
		}
		return nil
	})
	if err == nil || err.Error() != "ten" {
		t.Errorf("err = %v", err)
	}
	if ran.Load() == 1000 {
		t.Error("Parallel did not stop at the error")
	}
	err = parallel(context.Background(), 2, 5, func(i int) error {
		if i == 3 {
			panic("boom")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("a panic came back as %v", err)
	}
}

func TestPreviewChainsAndCaches(t *testing.T) {
	d := pipeline()
	cache := NewPreviewCache(8)
	p, err := Preview(context.Background(), d, "sink", cache, PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Notes) == 0 || p.Notes[0] != "upstream count 4" {
		t.Errorf("sink preview %v: did not see its upstream's", p.Notes)
	}
	if cache.Len() != 2 {
		t.Errorf("cache holds %d, want both stages", cache.Len())
	}
	// Changing the sink alone reuses the source's preview.
	d.Stages[1].Params["scale"] = Float(9)
	if _, err := Preview(context.Background(), d, "sink", cache, PreviewOptions{}); err != nil {
		t.Fatal(err)
	}
	if cache.Len() != 3 {
		t.Errorf("cache holds %d, want the source once and the sink twice", cache.Len())
	}
	src, _ := Preview(context.Background(), d, "src", cache, PreviewOptions{})
	if src == nil || len(src.Images) != 1 || src.FrameBounds() != (Rect{0, 0, 10, 10}) {
		t.Errorf("source preview %+v", src)
	}
	if v, ok := src.Images[0].ValueAt(7, 7); !ok || v != 3 {
		t.Errorf("ValueAt(7,7) = %v %v, want the bottom-right pixel 3", v, ok)
	}
	// A problem elsewhere does not stop a preview.
	d.Stages = append(d.Stages, Stage{Kind: "tsink", ID: "broken", Params: Params{"from": StageRef("ghost")}})
	if _, err := Preview(context.Background(), d, "src", cache, PreviewOptions{}); err != nil {
		t.Errorf("an unrelated broken stage stopped the preview: %v", err)
	}
	if _, err := Preview(context.Background(), d, "broken", cache, PreviewOptions{}); err == nil {
		t.Error("a broken stage previewed")
	}
}
