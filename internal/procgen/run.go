package procgen

// run.go — the run engine.
//
// Run executes a document's enabled stages in document order:
//
//  1. Validate. Any problem refuses the run (see validate.go).
//  2. Hash every stage (hash.go). A hash depends only on parameters and on
//     upstream hashes, so every one is known before anything runs.
//  3. Decide what runs. Normally: every stage whose hash is not the one in
//     the manifest, or whose files are not all there. With Force: all of
//     them. With Only: those stages, forced, plus any upstream stage they
//     depend on that is stale (a stage cannot run against outputs that do not
//     match its inputs' hashes).
//  4. Run each in turn: forget its manifest entry and delete its old files
//     (an Incremental kind keeps them and is handed the old entry to reuse
//     its fresh tiles: incremental.go), call the kind's Run, write its
//     placements, record its new entry.
//     Skipped stages read their placements back from their file, so the
//     Result always describes the whole document.
//  5. Prune the entries (and files) of stages no longer in the document.
//
// Stages run one after another; the parallelism is inside each, through
// Env.Parallel. Downstream stages generally consume everything upstream, so
// running two stages side by side would rarely help and would make progress
// and memory much harder to reason about.
//
// The engine never touches a level. Result.Placements is what a caller
// (procgen/genlevel, from the editor or `foundry gen run`) splices.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"foundry/internal/fileutil"
)

// Options configure a run.
type Options struct {
	// OutDir overrides the output folder (absolute). Empty is the document's
	// OutputDir under fileutil.ResourcesPath.
	OutDir string
	// Force reruns every stage.
	Force bool
	// Only runs just these stages (forced), and whatever stale upstream they
	// need.
	Only []string
	// Workers bounds each stage's Parallel; 0 is GOMAXPROCS.
	Workers int
	// Progress is told the stage being run (its label: "id (kind)") and how
	// far through it is. Called from the run's goroutines.
	Progress func(stage string, done float32)
	// Log receives each log line. Called from the run's goroutines.
	Log func(line string)
}

// Result is what a run did.
type Result struct {
	Generator string
	OutDir    string
	// Stages is every enabled stage, in document order, run or skipped. A
	// failed or cancelled run stops at the stage that failed; later stages
	// are absent.
	Stages  []*StageResult
	Ran     []string
	Skipped []string
	Elapsed time.Duration
	// Warnings are the stages' Warn lines ("[stage] ..."), logged at the
	// start: things to fix that did not stop the run.
	Warnings []string
}

// StageResult is one stage's outputs.
type StageResult struct {
	ID, Kind, Hash string
	// Dir is the stage's output folder (absolute); RelDir is it under
	// resources/.
	Dir, RelDir string
	// Files are its outputs, relative to the run's output folder.
	Files []string
	// Placements and Volumes carry the stage's own IDs; Result.Placements
	// gives the prefixed ones.
	Placements []Placement
	Volumes    []VolumeEntry
	// Level is the level-wide settings the stage owns (LevelSettings).
	Level LevelSettings
	// Supersedes is the earlier stages' placements this one replaces (by
	// stage ID, their own IDs): Result.Placements leaves them out.
	Supersedes map[string][]string
	// Skipped: not run this time. Normally because its outputs were fresh;
	// Stale says they were not (a Run with Only left it alone), and Files
	// and Placements are then the last run's, or empty.
	Skipped, Stale bool
	Elapsed        time.Duration
	// Artifact is the in-memory product Run set; nil for a skipped stage
	// until something asks (StageResult.artifact through Load).
	Artifact any

	kind     *StageKind
	loadOnce sync.Once
	loadErr  error
}

// File is the absolute path of one of the stage's outputs, by its name in the
// stage's folder.
func (r *StageResult) File(name string) string { return filepath.Join(r.Dir, filepath.FromSlash(name)) }

func (r *StageResult) artifact() (any, error) {
	if r.Artifact != nil {
		return r.Artifact, nil
	}
	r.loadOnce.Do(func() {
		if r.kind == nil || r.kind.Load == nil {
			r.loadErr = fmt.Errorf("stage %s (%s) has no in-memory product to load; read its files", r.ID, r.Kind)
			return
		}
		r.Artifact, r.loadErr = r.kind.Load(r)
	})
	return r.Artifact, r.loadErr
}

// Stage is the result of the stage with the given ID, or nil.
func (r *Result) Stage(id string) *StageResult {
	for _, s := range r.Stages {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Placements is every stage's scenery with level IDs (StagePrefix + ID),
// less any a later stage superseded (RunContext.Supersede).
func (r *Result) Placements() []Placement {
	gone := map[string]bool{}
	for _, s := range r.Stages {
		for stage, ids := range s.Supersedes {
			pre := StagePrefix(r.Generator, stage)
			for _, id := range ids {
				gone[pre+id] = true
			}
		}
	}
	var out []Placement
	for _, s := range r.Stages {
		pre := StagePrefix(r.Generator, s.ID)
		for _, p := range s.Placements {
			if gone[pre+p.ID] {
				continue
			}
			p.ID = pre + p.ID
			out = append(out, p)
		}
	}
	return out
}

// Volumes is every stage's volumes with level IDs.
func (r *Result) Volumes() []VolumeEntry {
	var out []VolumeEntry
	for _, s := range r.Stages {
		pre := StagePrefix(r.Generator, s.ID)
		for _, v := range s.Volumes {
			c := make(VolumeEntry, len(v))
			for k, x := range v {
				c[k] = x
			}
			id, _ := v["id"].(string)
			c["id"] = pre + id
			out = append(out, c)
		}
	}
	return out
}

// LevelSettings is every stage's level-wide settings, merged in document
// order: a later stage's key replaces an earlier one's. Nil when no stage
// set any.
func (r *Result) LevelSettings() LevelSettings {
	var out LevelSettings
	for _, s := range r.Stages {
		for k, v := range s.Level {
			if out == nil {
				out = LevelSettings{}
			}
			out[k] = v
		}
	}
	return out
}

// OutDirFor is where a document's outputs go: opts.OutDir, or its OutputDir
// under resources/.
func OutDirFor(d *Doc, opts Options) string {
	if opts.OutDir != "" {
		return opts.OutDir
	}
	return filepath.Join(fileutil.ResourcesPath, filepath.FromSlash(d.OutputDir()))
}

// plannedStage is one enabled stage, hashed.
type plannedStage struct {
	stage  *Stage
	kind   *StageKind
	params Params
	seed   uint64
	hash   string
	refs   []string
	// region is set on a stage expanded for an environment region
	// (regional.go); such a stage is not in d.Stages.
	region *RegionContext
}

// plan hashes every enabled stage in order, then appends the stages expanded
// for environment regions (regional.go), if the document has any.
func plan(d *Doc) ([]*plannedStage, error) {
	out, _, err := planWithNotes(d)
	return out, err
}

// planWithNotes is plan, and the log lines the region expansion had to say
// (preset stages it skipped).
func planWithNotes(d *Doc) ([]*plannedStage, []string, error) {
	if probs := d.Validate(); len(probs) > 0 {
		return nil, nil, &ValidationError{Problems: probs}
	}
	byID := map[string]*plannedStage{}
	var out []*plannedStage
	for i := range d.Stages {
		s := &d.Stages[i]
		if s.Disabled {
			continue
		}
		k := KindByName(s.Kind)
		p := Resolve(k.Params, s.Params)
		ps := &plannedStage{stage: s, kind: k, params: p, refs: StageRefs(k.Params, p)}
		ps.seed = StageSeed(d.Seed, s.ID, k.Params, p)
		var up []string
		for _, r := range ps.refs {
			up = append(up, byID[r].hash) // Validate guarantees r is an enabled stage above
		}
		if k.HashExtra != nil {
			x, err := k.HashExtra(d, s, p)
			if err != nil {
				return nil, nil, fmt.Errorf("stage %s: %w", s.ID, err)
			}
			// An empty extra hashes as no extra at all, so a kind that
			// gains a HashExtra moves no hash until it has something to
			// say (the regions stage's pair widths).
			if x != "" {
				up = append(up, "extra:"+x)
			}
		}
		ps.hash = StageHash(k, ps.seed, p, up)
		byID[s.ID] = ps
		out = append(out, ps)
	}
	extra, notes, err := expandRegions(d, out, byID)
	if err != nil {
		return nil, notes, err
	}
	return append(out, extra...), notes, nil
}

// StageHashes is every enabled stage's hash, by ID — what the editor shows
// beside a stage to say whether it is stale.
func StageHashes(d *Doc) (map[string]string, error) {
	ps, err := plan(d)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(ps))
	for _, p := range ps {
		out[p.stage.ID] = p.hash
	}
	return out, nil
}

// StaleStages is the enabled stages a plain Run would run, in order.
func StaleStages(d *Doc, opts Options) ([]string, error) {
	ps, err := plan(d)
	if err != nil {
		return nil, err
	}
	dir := OutDirFor(d, opts)
	man := ReadManifest(dir)
	var out []string
	for _, p := range ps {
		if e, ok := man.Stages[p.stage.ID]; !ok || !e.fresh(dir, p.hash) {
			out = append(out, p.stage.ID)
		}
	}
	return out, nil
}

// Run executes the document (see the file comment). It returns the result so
// far even with an error, so a caller can report what did finish.
func Run(ctx context.Context, d *Doc, opts Options) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	began := time.Now()
	res := &Result{Generator: d.Name}
	if !ValidDocName(d.Name) {
		return res, fmt.Errorf("generator name %q: must be lower-case letters, digits and underscores", d.Name)
	}
	planned, notes, err := planWithNotes(d)
	if err != nil {
		return res, err
	}
	dir := OutDirFor(d, opts)
	res.OutDir = dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	logf := func(format string, args ...any) {
		if opts.Log != nil {
			opts.Log(fmt.Sprintf(format, args...))
		}
	}
	for _, n := range notes {
		logf("%s", n)
	}
	// Warnings never stop a run (StageKind.Warn).
	res.Warnings = Warnings(d)
	for _, w := range res.Warnings {
		logf("warning: %s", w)
	}
	relRoot := filepath.ToSlash(d.OutputDir())
	if opts.OutDir != "" {
		relRoot = fileutil.RelAssetPath(dir)
	}

	man := ReadManifest(dir)
	man.Generator = d.Name

	// What runs.
	run := map[string]bool{}
	only := map[string]bool{}
	for _, id := range opts.Only {
		only[id] = true
	}
	byID := map[string]*plannedStage{}
	for _, p := range planned {
		byID[p.stage.ID] = p
	}
	for id := range only {
		if d.StageByID(id) == nil && byID[id] == nil {
			return res, fmt.Errorf("no stage %q", id)
		}
	}
	stale := func(p *plannedStage) bool {
		e, ok := man.Stages[p.stage.ID]
		return !ok || !e.fresh(dir, p.hash)
	}
	if len(only) > 0 {
		var need func(id string)
		need = func(id string) {
			p := byID[id]
			if p == nil {
				return
			}
			for _, r := range p.refs {
				if stale(byID[r]) {
					run[r] = true
				}
				need(r)
			}
		}
		for id := range only {
			if byID[id] == nil {
				return res, fmt.Errorf("stage %q is disabled", id)
			}
			run[id] = true
			need(id)
		}
	} else {
		for _, p := range planned {
			if opts.Force || stale(p) {
				run[p.stage.ID] = true
			}
		}
	}

	results := map[string]*StageResult{}
	for _, p := range planned {
		id := p.stage.ID
		sr := &StageResult{ID: id, Kind: p.kind.Name, Hash: p.hash, kind: p.kind,
			Dir: filepath.Join(dir, id), RelDir: relRoot + "/" + id}
		if err := ctx.Err(); err != nil {
			res.Elapsed = time.Since(began)
			return res, ErrCancelled
		}
		if !run[id] {
			e := man.Stages[id]
			sr.Files, sr.Skipped, sr.Stale = e.Files, true, stale(p)
			if containsStr(e.Files, id+"/"+PlacementsFileName) {
				f, err := readPlacements(sr.File(PlacementsFileName))
				if err != nil {
					return res, fmt.Errorf("stage %s: %w", id, err)
				}
				sr.Placements, sr.Volumes, sr.Level, sr.Supersedes = f.Scenery, f.Volumes, f.Level, f.Supersedes
			}
			res.Skipped = append(res.Skipped, id)
			res.Stages = append(res.Stages, sr)
			results[id] = sr
			logf("[%s] up to date", id)
			continue
		}

		// Forget the old outputs before making new ones — or, for an
		// Incremental kind (incremental.go), keep them to reuse, with an
		// entry that claims nothing fresh until the stage finishes.
		var prev *ManifestStage
		if old, ok := man.Stages[id]; ok {
			if p.kind.Incremental && !opts.Force {
				o := old
				prev = &o
				man.Stages[id] = ManifestStage{Kind: p.kind.Name, Files: old.Files}
			} else {
				old.removeFiles(dir)
				delete(man.Stages, id)
			}
			if err := man.Write(dir); err != nil {
				return res, err
			}
		}
		if err := os.MkdirAll(sr.Dir, 0o755); err != nil {
			return res, err
		}
		label := id + " (" + p.kind.Name + ")"
		rc := &RunContext{
			Env: Env{Ctx: ctx, Doc: d, Stage: p.stage, Kind: p.kind, Params: p.params, Seed: p.seed,
				Workers: opts.Workers, Region: p.region, logf: opts.Log},
			Dir: sr.Dir, RelDir: sr.RelDir, upstream: results, files: map[string]bool{}, Previous: prev,
		}
		if opts.Progress != nil {
			rc.progress = func(f float32) { opts.Progress(label, f) }
		}
		rc.Progress(0)
		logf("[%s] running %s", id, p.kind.Name)
		t0 := time.Now()
		err := runStage(p.kind, rc)
		sr.Elapsed = time.Since(t0)
		if err == nil && ctx.Err() != nil {
			err = ErrCancelled
		}
		if err != nil && p.kind.Incremental && prev != nil {
			// Keep everything (the next run reuses what it can) under an
			// entry with no hash: only the tiles that are certainly whole.
			man.Stages[id] = ManifestStage{Kind: p.kind.Name, Files: unionFiles(prev.Files, prefixFiles(id, rc.sortedFiles())),
				Tiles: rc.survivingTiles()}
			if werr := man.Write(dir); werr != nil {
				logf("[%s] manifest: %v", id, werr)
			}
		} else if err != nil {
			// The half-made files are not in the manifest; delete what was
			// recorded so the folder does not fill with orphans.
			ManifestStage{Files: prefixFiles(id, rc.sortedFiles())}.removeFiles(dir)
		}
		if err != nil {
			res.Elapsed = time.Since(began)
			if errors.Is(err, ErrCancelled) || errors.Is(err, context.Canceled) {
				logf("[%s] cancelled", id)
				return res, ErrCancelled
			}
			logf("[%s] failed: %v", id, err)
			return res, fmt.Errorf("stage %s: %w", id, err)
		}
		sort.SliceStable(rc.scenery, func(a, b int) bool { return rc.scenery[a].ID < rc.scenery[b].ID })
		numberPlacements(rc.scenery, rc.volumes)
		for _, ids := range rc.supersedes {
			sort.Strings(ids)
		}
		if len(rc.scenery) > 0 || len(rc.volumes) > 0 || len(rc.level) > 0 || len(rc.supersedes) > 0 {
			f := placementsFile{Scenery: rc.scenery, Volumes: rc.volumes, Level: rc.level, Supersedes: rc.supersedes}
			if err := writePlacements(rc.Path(PlacementsFileName), f); err != nil {
				return res, fmt.Errorf("stage %s: placements: %w", id, err)
			}
		}
		sr.Files = prefixFiles(id, rc.sortedFiles())
		sr.Placements, sr.Volumes, sr.Level, sr.Artifact = rc.scenery, rc.volumes, rc.level, rc.Artifact
		sr.Supersedes = rc.supersedes
		man.Stages[id] = ManifestStage{Kind: p.kind.Name, Hash: p.hash, Files: sr.Files, Tiles: rc.tileHashes()}
		if err := man.Write(dir); err != nil {
			return res, err
		}
		if prev != nil {
			// What the previous run made and this one neither wrote nor
			// reused.
			removeStale(dir, prev.Files, sr.Files)
		}
		rc.Progress(1)
		logf("[%s] done in %s: %d files, %d placements", id, sr.Elapsed.Round(time.Millisecond), len(sr.Files), len(sr.Placements)+len(sr.Volumes))
		res.Ran = append(res.Ran, id)
		res.Stages = append(res.Stages, sr)
		results[id] = sr
	}

	// Stages that are no longer in the document take their outputs with them.
	// A disabled stage keeps its outputs (it may come back) but is not in the
	// result, so nothing splices it.
	pruned := false
	for id, e := range man.Stages {
		// An expanded region stage is not in d.Stages; it is in the plan
		// for as long as its region and preset stage are.
		if d.StageByID(id) == nil && byID[id] == nil {
			e.removeFiles(dir)
			delete(man.Stages, id)
			pruned = true
			logf("[%s] removed from the document; deleted its outputs", id)
		}
	}
	if pruned {
		if err := man.Write(dir); err != nil {
			return res, err
		}
	}
	res.Elapsed = time.Since(began)
	return res, nil
}

// Warnings is every enabled stage's Warn lines, prefixed "[stage] ", in
// document order — what `foundry gen check` prints and a run logs. A kind's
// panic is reported as a warning rather than crashing the caller.
func Warnings(d *Doc) []string {
	var out []string
	for i := range d.Stages {
		s := &d.Stages[i]
		if s.Disabled {
			continue
		}
		k := KindByName(s.Kind)
		if k == nil || k.Warn == nil {
			continue
		}
		p := Resolve(k.Params, s.Params)
		func() {
			defer func() {
				if r := recover(); r != nil {
					out = append(out, fmt.Sprintf("[%s] its warnings failed: %v", s.ID, r))
				}
			}()
			for _, w := range k.Warn(d, s, p) {
				out = append(out, "["+s.ID+"] "+w)
			}
		}()
	}
	return out
}

// runStage calls the kind's Run, turning a panic into an error: a broken
// stage fails its run, not the editor.
func runStage(k *StageKind, rc *RunContext) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return k.Run(rc)
}

func prefixFiles(id string, files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = id + "/" + strings.TrimPrefix(f, "./")
	}
	return out
}
