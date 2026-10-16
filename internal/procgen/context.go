package procgen

// context.go — what a stage's Run and Preview are handed.
//
// Both get an Env: the stage, its resolved parameters, its seed, a log, a
// progress callback, cancellation and a worker pool. Run's RunContext adds the
// output folder, the outputs it has written, its placements and the upstream
// stages' results; Preview's PreviewContext adds the upstream stages'
// previews.
//
// Cancellation is cooperative. A stage checks Cancelled() (or the error from
// Parallel) in its loops — every row of a heightfield, every variant of a
// rock — and returns ErrCancelled, or the context's error, promptly. The run
// engine discards a cancelled stage's partial outputs from the manifest, so a
// cancelled run never leaves a stage that looks fresh and is not.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"foundry/internal/procgen/noise"
)

// ErrCancelled is what a cancelled run or preview returns.
var ErrCancelled = errors.New("cancelled")

// Env is the part of a stage's context Run and Preview share.
type Env struct {
	Ctx    context.Context
	Doc    *Doc
	Stage  *Stage
	Kind   *StageKind
	Params Params
	// Seed is StageSeed for this stage. RNG() is a fresh stream from it.
	Seed uint64
	// Workers is how many goroutines Parallel may use.
	Workers int
	// Region is the environment region an expanded stage (r_<region>_<stage>,
	// regional.go) works for: its key, environment, weight lookup and
	// bounds. Nil for every stage a document lists itself.
	Region *RegionContext
	// BaseHash is the stage's hash with no paint in it: its parameters,
	// seed and its upstream stages' base hashes. Equal to the stage hash
	// when nothing above it reads paint. Per-tile reruns key their pieces
	// by it and by the paint each piece reads (paint_incremental.go).
	// Empty in a preview.
	BaseHash string
	// paintOwn and paintUp are the layers this stage's params name and the
	// ones its upstream stages read; lineage is how its upstream outputs
	// came to be this run (paint_incremental.go).
	paintOwn, paintUp []string
	paintCovered      []string
	lineage           string
	// coveredUp hashes the RectCovered params of the stages above
	// (paint_incremental.go).
	coveredUp string
	// rects are the RectKeyed stages above this one (paint_incremental.go).
	rects *rectKeys

	progress func(float32)
	logf     func(string)
}

// RNG is a new stream from the stage's seed. Each call starts the same
// stream; fork it (RNG().Fork(item)) for parallel work.
func (e *Env) RNG() *noise.RNG { return noise.NewRNG(e.Seed) }

// Cancelled reports whether the run or preview has been called off.
func (e *Env) Cancelled() bool { return e.Ctx != nil && e.Ctx.Err() != nil }

// Err is ErrCancelled once cancelled, else nil — for `if err := rc.Err();
// err != nil { return err }` in a loop.
func (e *Env) Err() error {
	if e.Cancelled() {
		return ErrCancelled
	}
	return nil
}

// Progress reports how far through the stage is, 0..1.
func (e *Env) Progress(done float32) {
	if e.progress != nil {
		e.progress(min(max(done, 0), 1))
	}
}

// Logf writes a line to the run's log, prefixed with the stage's ID.
func (e *Env) Logf(format string, args ...any) {
	if e.logf != nil {
		e.logf(fmt.Sprintf("[%s] %s", e.Stage.ID, fmt.Sprintf(format, args...)))
	}
}

// Parallel runs fn(i) for every i in [0, n) on up to Workers goroutines,
// handing out indices in order. It stops handing out work at the first error
// or panic (returned) or on cancellation (ErrCancelled); items already
// started finish. fn must not depend on which goroutine runs it — seed any
// randomness from i.
func (e *Env) Parallel(n int, fn func(i int) error) error {
	return parallel(e.Ctx, e.Workers, n, fn)
}

// ParallelProgress is Parallel that reports Progress as items finish.
func (e *Env) ParallelProgress(n int, fn func(i int) error) error {
	var done atomic.Int64
	return e.Parallel(n, func(i int) error {
		err := fn(i)
		e.Progress(float32(done.Add(1)) / float32(max(n, 1)))
		return err
	})
}

func parallel(ctx context.Context, workers, n int, fn func(i int) error) error {
	if n <= 0 {
		return nil
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, n)
	var (
		next    atomic.Int64
		stop    atomic.Bool
		errOnce sync.Once
		first   error
		wg      sync.WaitGroup
	)
	fail := func(err error) {
		errOnce.Do(func() { first = err })
		stop.Store(true)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					fail(fmt.Errorf("panic: %v", r))
				}
			}()
			for !stop.Load() {
				if ctx != nil && ctx.Err() != nil {
					fail(ErrCancelled)
					return
				}
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				if err := fn(i); err != nil {
					fail(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	return first
}

// ---------- the run ----------

// RunContext is a stage's Run's whole world.
type RunContext struct {
	Env
	// Dir is the stage's own output folder (absolute), created before Run.
	Dir string
	// RelDir is Dir under resources/, slash-separated: what a placement's
	// Model is written relative to.
	RelDir string
	// Artifact is the stage's in-memory product, for downstream stages of
	// this run. Set it from Run; StageKind.Load rebuilds it when a later run
	// skips this stage.
	Artifact any
	// ArtifactAsLoaded says Artifact is exactly what Load would rebuild
	// from the files the run wrote (a tiled scatter rounds its footprints as
	// its file does), so a downstream piece made against it is the same as
	// one made against a later run's loaded copy (paint_incremental.go's
	// upstreamLineage).
	ArtifactAsLoaded bool

	upstream   map[string]*StageResult
	mu         sync.Mutex
	files      map[string]bool
	scenery    []Placement
	volumes    []VolumeEntry
	emitters   []Emitter
	sounds     []Sound
	level      LevelSettings
	supersedes map[string][]string

	// Previous is an Incremental kind's last manifest entry (files and
	// per-tile hashes), or nil (incremental.go).
	Previous *ManifestStage
	tiles    map[string]string
	touched  map[string]bool

	// The previous run's volumes, read on demand (paint_incremental.go).
	prevOnce    sync.Once
	prevVolumes map[string]VolumeEntry
}

// Path is the absolute path of a file named rel inside the stage's folder,
// recorded as one of its outputs. Write the file there yourself (a library
// that writes its own files, like the Nanite cook, needs the path); WriteFile
// does both.
func (rc *RunContext) Path(rel string) string {
	rel = path.Clean(filepathToSlash(rel))
	rc.mu.Lock()
	rc.files[rel] = true
	rc.mu.Unlock()
	return filepath.Join(rc.Dir, filepath.FromSlash(rel))
}

// AssetPath is rel inside the stage's folder as a resources-relative path —
// what a placement's Model says.
func (rc *RunContext) AssetPath(rel string) string {
	return rc.RelDir + "/" + path.Clean(filepathToSlash(rel))
}

// WriteFile writes an output file into the stage's folder (atomically) and
// records it. Safe from Parallel workers.
func (rc *RunContext) WriteFile(rel string, data []byte) error {
	return writeFileAtomicNoBackup(rc.Path(rel), data)
}

// Place adds scenery placements. Safe from Parallel workers; the order they
// are written in is sorted by ID before writing, so concurrency cannot
// reorder the file.
func (rc *RunContext) Place(p ...Placement) {
	rc.mu.Lock()
	rc.scenery = append(rc.scenery, p...)
	rc.mu.Unlock()
}

// PlaceVolume adds volume entries (see VolumeEntry).
func (rc *RunContext) PlaceVolume(v ...VolumeEntry) {
	rc.mu.Lock()
	rc.volumes = append(rc.volumes, v...)
	rc.mu.Unlock()
}

// PlaceEmitter adds particle emitters (see Emitter). Safe from Parallel
// workers; they are written sorted by ID.
func (rc *RunContext) PlaceEmitter(e ...Emitter) {
	rc.mu.Lock()
	rc.emitters = append(rc.emitters, e...)
	rc.mu.Unlock()
}

// PlaceSound adds positional sounds (see Sound). Safe from Parallel
// workers; they are written sorted by ID.
func (rc *RunContext) PlaceSound(s ...Sound) {
	rc.mu.Lock()
	rc.sounds = append(rc.sounds, s...)
	rc.mu.Unlock()
}

// SetLevel sets a level-wide setting the stage owns in its target level, by
// top-level level key (see LevelSettings).
func (rc *RunContext) SetLevel(key string, value interface{}) {
	rc.mu.Lock()
	if rc.level == nil {
		rc.level = LevelSettings{}
	}
	rc.level[key] = value
	rc.mu.Unlock()
}

// Supersede replaces placements an upstream stage made: the ones with the
// given IDs (that stage's own) are left out of the run's placements
// (Result.Placements), so this stage can put its own in their place — a
// terrain tile recooked with sand piled against the rocks, a rock re-placed
// wearing a material that knows the new ground. Safe from Parallel workers.
func (rc *RunContext) Supersede(stage string, ids ...string) {
	rc.mu.Lock()
	if rc.supersedes == nil {
		rc.supersedes = map[string][]string{}
	}
	rc.supersedes[stage] = append(rc.supersedes[stage], ids...)
	rc.mu.Unlock()
}

// Upstream is the result of the stage with the given ID, which must be one
// this stage refers to (or any stage above it); nil if there is none.
func (rc *RunContext) Upstream(id string) *StageResult { return rc.upstream[id] }

// UpstreamArtifact is an upstream stage's in-memory product: the one its Run
// set this run, or — when the engine skipped it as fresh — the one its
// kind's Load rebuilds from its files (once; it is kept for the rest of the
// run). An error when the stage is unknown or its kind has no Load.
func (rc *RunContext) UpstreamArtifact(id string) (any, error) {
	res := rc.upstream[id]
	if res == nil {
		return nil, fmt.Errorf("no upstream stage %q", id)
	}
	return res.artifact()
}

// sortedFiles is the outputs recorded, sorted.
func (rc *RunContext) sortedFiles() []string {
	out := make([]string, 0, len(rc.files))
	for f := range rc.files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func filepathToSlash(p string) string { return filepath.ToSlash(p) }

// fileExists is a stat.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---------- the preview ----------

// PreviewContext is a stage's Preview's whole world.
type PreviewContext struct {
	Env
	upstream map[string]*PreviewData
}

// Upstream is the preview of an upstream stage this one refers to, or nil
// (it has no Preview, or it failed).
func (pc *PreviewContext) Upstream(id string) *PreviewData { return pc.upstream[id] }
