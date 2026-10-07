package levelio

// level_load_job.go — a level load split into steps small enough to run one per
// frame, so the loading screen over it can animate and show real progress.
//
// It is a state machine the frame loop pumps, not a goroutine. It cannot be a
// goroutine: loading a level ends in gl.* calls, and the GL context belongs to
// the main thread — the same constraint DrainScriptWork exists for. And it
// cannot be "draw one frame, then load", because that gives exactly one static
// frame with a bar that never moves.
//
// What is chunked is the chunk loop and the texture loads, which is where the
// time on a large level actually goes: building sector geometry and decoding
// PNGs. The YAML parse and the level-scoped objects are one step each.
//
// A compiled level's READ is the exception to "not a goroutine": decoding the
// file is pure CPU over bytes nothing else touches, and on a large level it
// was most of a second with nothing on screen, because it ran before the job —
// and so the loading screen — existed. So BeginLoadCompiled checks only the
// header, returns at once, and the decode runs on its own goroutine while the
// job sits in its first stage ("Reading level") polling for it. Everything
// after that is the main-thread state machine as before.
//
// The load runs the same four functions rawToLevel does, in the same order.
// That is deliberate and it is the whole safety argument: there is one loader,
// driven two ways, so a level cannot load differently depending on which drove
// it.

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/world"
)

// Per-step budgets, tuned so a step lands near four milliseconds on the levels
// that ship. Chunks are the expensive one; textures are a PNG decode each.
//
// A step is the unit of work, not the unit of a frame: StepFor runs as many as
// fit in the frame's budget. One step per frame put a floor under the load
// that had nothing to do with the work — level1's 71k compiled surfaces are
// 1,100 steps of 64, which is nineteen seconds of loading screen at 60 fps for
// about a tenth of a second of building.
const (
	chunksPerStep   = 1
	texturesPerStep = 4
	// A compiled surface is a handful of triangles rather than a whole chunk's
	// worth, so a step gets many of them.
	staticSurfacesPerStep = 64
)

type loadStage int

const (
	// loadStageReading is a compiled load waiting for its file to decode off
	// the main thread. An authored load starts at loadStageChunks.
	loadStageReading loadStage = iota
	loadStageChunks
	loadStageObjects
	loadStageTextures
	loadStageStatic
	loadStageIndexes
	loadStageDone
)

// loadStageNames is what the loading screen's label shows.
var loadStageNames = [...]string{
	loadStageReading:  "Reading level",
	loadStageChunks:   "Building geometry",
	loadStageObjects:  "Placing objects",
	loadStageTextures: "Loading textures",
	loadStageStatic:   "Building compiled geometry",
	loadStageIndexes:  "Indexing",
	loadStageDone:     "Ready",
}

// LoadJob is a level load in progress, of either kind.
//
// A compiled load sets compiled and leaves raw zero; an authored load does the
// reverse. One job rather than two because everything after the geometry is
// identical, and because the loading screen, the progress hook and
// installLevel should not have to know which kind they are pumping.
type LoadJob struct {
	path  string
	raw   world.RawLevel
	level world.Level
	// compiled is the artefact for a compiled load, nil for an authored one.
	// Nil too while a compiled load is still reading; see read.
	compiled *CompiledLevel
	// read delivers the decoded file to a compiled load in loadStageReading.
	read chan compiledRead

	texNeeded map[string]bool
	texNames  []string

	stage    loadStage
	chunkAt  int
	texAt    int
	staticAt int
	// staticDone is the parallel surface build's completion, nil until it
	// starts; staticBuilt counts its surfaces for the bar. See loadStageStatic.
	staticDone  chan error
	staticBuilt atomic.Int64

	err error
}

// BeginLoad parses a level file and returns a job that will build it.
//
// The parse is synchronous, and deliberately so: a file that will not parse is
// a failure the caller should see now, rather than one frame into a loading
// screen it has already put up.
func BeginLoad(levelPath string) (*LoadJob, error) {
	raw, err := readRawLevelFile(levelPath)
	if err != nil {
		return nil, err
	}
	return BeginLoadRaw(levelPath, raw), nil
}

// BeginLoadRaw returns a job that builds a level already parsed — the level
// YAML editor's, which has the file's RawLevel in hand from checking it and
// would otherwise have the job parse the same two megabytes a second time.
// levelPath is only what the job reports as its path.
func BeginLoadRaw(levelPath string, raw world.RawLevel) *LoadJob {
	j := &LoadJob{path: levelPath, raw: raw, texNeeded: map[string]bool{}, stage: loadStageChunks}
	j.publish()
	return j
}

// BeginLoadCompiled starts reading a compiled level and returns a job that will
// build it.
//
// Only the header is read here — enough to refuse, now, a file that is
// missing, is not a compiled level, or was compiled by a version this build
// does not read, which is a failure the caller should see before it has put a
// loading screen up. The rest of the file decodes on another goroutine while
// the job's first stage waits for it; a file that turns out to be damaged
// fails the job there, through Err, exactly as a failed build step does.
func BeginLoadCompiled(levelPath string) (*LoadJob, error) {
	if err := CheckCompiledLevel(levelPath); err != nil {
		return nil, err
	}
	j := &LoadJob{path: levelPath, texNeeded: map[string]bool{}, stage: loadStageReading,
		read: make(chan compiledRead, 1)}
	go func(ch chan<- compiledRead) { ch <- readCompiledForLoad(levelPath) }(j.read)
	j.publish()
	return j, nil
}

// compiledRead is what the reading goroutine hands the job.
type compiledRead struct {
	c         *CompiledLevel
	texNeeded map[string]bool
	err       error
}

// readCompiledForLoad is the off-thread half of BeginLoadCompiled: the decode,
// and the other work that needs only the decoded file and no GL — which
// textures the surfaces want, so that list is ready the moment the main
// thread takes the file.
func readCompiledForLoad(path string) compiledRead {
	c, err := ReadCompiledLevel(path)
	if err != nil {
		return compiledRead{err: err}
	}
	tex := map[string]bool{}
	// The compiled surfaces' textures are known from the file alone, so they
	// join the list up front rather than being discovered a stage later.
	for i := range c.Faces {
		noteCompiledFaceTextures(tex, &c.Faces[i])
	}
	return compiledRead{c: c, texNeeded: tex}
}

// adoptCompiled takes the decoded file into the job, on the main thread.
func (j *LoadJob) adoptCompiled(r compiledRead) {
	j.compiled, j.raw, j.read = r.c, r.c.Raw, nil
	for t := range r.texNeeded {
		j.texNeeded[t] = true
	}
	// So they decode on the other cores while this one places the objects.
	gfx.PrefetchTextures(textureLoadOrder(j.texNeeded))
	j.stage = loadStageChunks
}

// waitForRead blocks until a compiled load's file has decoded. For the
// synchronous callers (LoadCompiledLevel), which have no frame to keep and
// would otherwise spin on Step.
func (j *LoadJob) waitForRead() {
	if j.stage != loadStageReading || j.read == nil {
		return
	}
	r := <-j.read
	if r.err != nil {
		j.fail(r.err)
		return
	}
	j.adoptCompiled(r)
	j.publish()
}

// Step does a bounded slice of work and reports whether the load has finished.
//
// A failed step ends the job: Err says what went wrong, and Level is not usable.
// Failing rather than skipping matches the synchronous loader, which refuses a
// level rather than opening a broken one.
func (j *LoadJob) Step() bool {
	if j.stage == loadStageDone || j.err != nil {
		return true
	}

	switch j.stage {
	case loadStageReading:
		select {
		case r := <-j.read:
			if r.err != nil {
				return j.fail(r.err)
			}
			j.adoptCompiled(r)
		default:
			// Still decoding: nothing to do this step.
		}

	case loadStageChunks:
		next, total, err := j.stepChunks()
		if err != nil {
			return j.fail(err)
		}
		j.chunkAt = next
		if j.chunkAt >= total {
			j.stage = loadStageObjects
			// An authored level's textures are known only once its chunks
			// are built. Already-started ones are skipped.
			gfx.PrefetchTextures(textureLoadOrder(j.texNeeded))
		}

	case loadStageObjects:
		if err := loadLevelObjects(j.raw, &j.level, j.texNeeded); err != nil {
			return j.fail(err)
		}
		// The object pass discovers textures of its own — a sprite's, an
		// entity model's — so the list can only be taken once it has run.
		j.texNames = textureLoadOrder(j.texNeeded)
		gfx.PrefetchTextures(j.texNames)
		j.stage = loadStageTextures

	case loadStageTextures:
		next, err := loadTextureRange(j.texNames, j.texAt, texturesPerStep)
		if err != nil {
			return j.fail(err)
		}
		j.texAt = next
		if j.texAt >= len(j.texNames) {
			j.stage = loadStageStatic
			gfx.DropPrefetchedTextures()
		}

	case loadStageStatic:
		if j.compiled == nil {
			j.stage = loadStageIndexes
			break
		}
		if parallelStaticBuild {
			// Every surface at once, on the other cores (buildStaticAll),
			// while this one keeps the loading screen drawing. Nothing on
			// the main thread touches the level until the build reports
			// back, and the build touches nothing but the level's surfaces.
			if j.staticDone == nil {
				j.staticDone = make(chan error, 1)
				go func(c *CompiledLevel, lvl *world.Level, done chan<- error) {
					done <- buildStaticAll(c, lvl, &j.staticBuilt)
				}(j.compiled, &j.level, j.staticDone)
			}
			select {
			case err := <-j.staticDone:
				if err != nil {
					return j.fail(err)
				}
				j.staticAt = len(j.compiled.Faces)
				j.stage = loadStageIndexes
			default:
				j.staticAt = int(j.staticBuilt.Load())
			}
			break
		}
		next, err := buildStaticRange(j.compiled, &j.level, j.staticAt, staticSurfacesPerStep)
		if err != nil {
			return j.fail(err)
		}
		j.staticAt = next
		if j.staticAt >= len(j.compiled.Faces) {
			j.stage = loadStageIndexes
		}

	case loadStageIndexes:
		if err := finishLevel(&j.level); err != nil {
			return j.fail(err)
		}
		j.stage = loadStageDone
	}

	j.publish()
	return j.stage == loadStageDone
}

// StepFor runs steps until the load finishes or budget has been spent, and
// reports whether it has finished. The budget is checked between steps, so one
// long step still overruns it — a frame late rather than a load slow.
func (j *LoadJob) StepFor(budget time.Duration) bool {
	start := time.Now()
	for {
		if j.Step() {
			return true
		}
		if j.waiting() {
			// Waiting on the decode or the surface build, which is other
			// goroutines' work: the rest of the budget is better spent
			// drawing the frame.
			return false
		}
		if time.Since(start) >= budget {
			return false
		}
	}
}

// waiting is whether the job's current stage is another goroutine's work that
// has not finished.
func (j *LoadJob) waiting() bool {
	return j.stage == loadStageReading || (j.stage == loadStageStatic && j.staticDone != nil)
}

// waitForStatic blocks until a parallel surface build has finished, for the
// synchronous callers.
func (j *LoadJob) waitForStatic() {
	if j.stage != loadStageStatic || j.staticDone == nil {
		return
	}
	if err := <-j.staticDone; err != nil {
		j.fail(err)
		return
	}
	j.staticAt = len(j.compiled.Faces)
	j.stage = loadStageIndexes
	j.publish()
}

// stepChunks builds one slice of chunks, from whichever source this job has,
// and reports where it got to and how many there are in all.
func (j *LoadJob) stepChunks() (next, total int, err error) {
	if j.compiled != nil {
		next, err = loadCompiledChunkRange(j.compiled, &j.level, j.texNeeded, j.chunkAt, chunksPerStep)
		return next, len(j.compiled.Chunks), err
	}
	next, err = loadChunkRange(j.raw, &j.level, j.texNeeded, j.chunkAt, chunksPerStep)
	return next, len(j.raw.Chunks), err
}

func (j *LoadJob) fail(err error) bool {
	gfx.DropPrefetchedTextures()
	j.err = err
	j.stage = loadStageDone
	j.publish()
	return true
}

// Progress is how far along the load is, from 0 to 1, and a one-line
// description of what it is doing.
//
// The fractions are weighted by where the time goes rather than by stage count:
// a bar that sat at 25% through the whole chunk build and then leapt to 100%
// would be worse than no bar.
func (j *LoadJob) Progress() (float32, string) {
	label := loadStageNames[j.stage]
	switch j.stage {
	case loadStageReading:
		return 0, label
	case loadStageChunks:
		return 0.50 * fraction(j.chunkAt, j.chunkTotal()), label
	case loadStageObjects:
		return 0.55, label
	case loadStageTextures:
		return 0.55 + 0.25*fraction(j.texAt, len(j.texNames)), label
	case loadStageStatic:
		if j.compiled == nil {
			return 0.90, label
		}
		return 0.80 + 0.17*fraction(j.staticAt, len(j.compiled.Faces)), label
	case loadStageIndexes:
		return 0.97, label
	}
	return 1, label
}

// chunkTotal is how many chunks this job has to build, from whichever source.
func (j *LoadJob) chunkTotal() int {
	if j.compiled != nil {
		return len(j.compiled.Chunks)
	}
	return len(j.raw.Chunks)
}

// Done reports whether there is no more work.
func (j *LoadJob) Done() bool { return j.stage == loadStageDone }

// Err is what went wrong, or nil.
func (j *LoadJob) Err() error { return j.err }

// Path is the file being loaded.
func (j *LoadJob) Path() string { return j.path }

// Level is the built level. Only meaningful once Done and Err is nil.
func (j *LoadJob) Level() *world.Level {
	j.level.Name = LevelNameFromPath(j.path)
	return &j.level
}

// publish makes the progress readable from a script — and therefore from a
// widget bound to `@level.prop.load_progress`.
//
// Through hooks because internal/script sits below this package and cannot
// import it. It is the same inversion the collision pairs use, with one
// difference: this is a level rather than an event, so it is a value that is
// read rather than a buffer that is drained.
func (j *LoadJob) publish() {
	p, label := j.Progress()
	hooks.SetLoadProgress(p, label)
}

// fraction is done/total, and 1 for a stage with nothing in it — a level with
// no chunks has finished building them.
func fraction(done, total int) float32 {
	if total <= 0 {
		return 1
	}
	if done > total {
		done = total
	}
	return float32(done) / float32(total)
}

// LevelsDirName is the folder level files live in, beside particles/ and ui/.
const LevelsDirName = "levels"

// LevelExt is the extension a level carries. YAML rather than a private
// extension because a level is the one document people hand-edit.
const LevelExt = ".yaml"

// LevelsDir is the absolute levels folder.
func LevelsDir() string { return filepath.Join(fileutil.ResourcesPath, LevelsDirName) }

// LevelNameFromPath is LevelPath (or CompiledPath) run backwards: the name
// load_level would take for the file at path. A file outside the levels folder
// is named by its base name.
func LevelNameFromPath(path string) string {
	if path == "" {
		return ""
	}
	name := filepath.Base(path)
	if rel, err := filepath.Rel(LevelsDir(), path); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		name = filepath.ToSlash(rel)
	}
	for _, ext := range []string{LevelExt, CompiledExt} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}

// LevelPath resolves a bare level name to a full path. A name that already
// carries the extension is left alone, so both spellings work from a script.
func LevelPath(name string) string {
	if strings.HasSuffix(name, LevelExt) {
		return filepath.Join(LevelsDir(), name)
	}
	return filepath.Join(LevelsDir(), name+LevelExt)
}
