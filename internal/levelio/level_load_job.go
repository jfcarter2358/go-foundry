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
// PNGs. The YAML parse and the level-scoped objects are one step each. Moving
// the parse off-thread is a later change and a safe one — it is pure CPU over
// data nothing else touches — but it is not where the stall is.
//
// The load runs the same four functions rawToLevel does, in the same order.
// That is deliberate and it is the whole safety argument: there is one loader,
// driven two ways, so a level cannot load differently depending on which drove
// it.

import (
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/hooks"
	"foundry/internal/world"
)

// Per-step budgets, tuned so a step lands near four milliseconds on the levels
// that ship. Chunks are the expensive one; textures are a PNG decode each.
const (
	chunksPerStep   = 1
	texturesPerStep = 4
	// A compiled surface is a handful of triangles rather than a whole chunk's
	// worth, so a step gets many of them.
	staticSurfacesPerStep = 64
)

type loadStage int

const (
	loadStageChunks loadStage = iota
	loadStageObjects
	loadStageTextures
	loadStageStatic
	loadStageIndexes
	loadStageDone
)

// loadStageNames is what the loading screen's label shows.
var loadStageNames = [...]string{
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
	compiled *CompiledLevel

	texNeeded map[string]bool
	texNames  []string

	stage    loadStage
	chunkAt  int
	texAt    int
	staticAt int

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
	j := &LoadJob{path: levelPath, raw: raw, texNeeded: map[string]bool{}}
	j.publish()
	return j, nil
}

// BeginLoadCompiled reads a compiled level and returns a job that will build
// it.
//
// The read is synchronous for the same reason BeginLoad's parse is: a file that
// will not decode is a failure the caller should see now, rather than one frame
// into a loading screen it has already put up.
func BeginLoadCompiled(levelPath string) (*LoadJob, error) {
	c, err := ReadCompiledLevel(levelPath)
	if err != nil {
		return nil, err
	}
	j := &LoadJob{path: levelPath, compiled: c, raw: c.Raw, texNeeded: map[string]bool{}}
	// The compiled surfaces' textures are known from the file alone, so they
	// join the list up front rather than being discovered a stage later.
	for i := range c.Faces {
		noteCompiledFaceTextures(j.texNeeded, &c.Faces[i])
	}
	j.publish()
	return j, nil
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
	case loadStageChunks:
		next, total, err := j.stepChunks()
		if err != nil {
			return j.fail(err)
		}
		j.chunkAt = next
		if j.chunkAt >= total {
			j.stage = loadStageObjects
		}

	case loadStageObjects:
		if err := loadLevelObjects(j.raw, &j.level, j.texNeeded); err != nil {
			return j.fail(err)
		}
		// The object pass discovers textures of its own — a sprite's, an
		// entity model's — so the list can only be taken once it has run.
		j.texNames = textureLoadOrder(j.texNeeded)
		j.stage = loadStageTextures

	case loadStageTextures:
		next, err := loadTextureRange(j.texNames, j.texAt, texturesPerStep)
		if err != nil {
			return j.fail(err)
		}
		j.texAt = next
		if j.texAt >= len(j.texNames) {
			j.stage = loadStageStatic
		}

	case loadStageStatic:
		if j.compiled == nil {
			j.stage = loadStageIndexes
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
func (j *LoadJob) Level() *world.Level { return &j.level }

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

// LevelPath resolves a bare level name to a full path. A name that already
// carries the extension is left alone, so both spellings work from a script.
func LevelPath(name string) string {
	if strings.HasSuffix(name, LevelExt) {
		return filepath.Join(LevelsDir(), name)
	}
	return filepath.Join(LevelsDir(), name+LevelExt)
}
