package editor

// editor_compile_job.go — Build > Compile and Preview Compiled behind a
// progress bar rather than a frozen window.
//
// A compile is a pure read of the level plus minutes of light bake, so the slow
// half (editorCompileRun) runs on a goroutine and the main thread keeps
// drawing. What makes that safe is that nothing writes the level meanwhile: the
// modal takes the main window's UI, EditorCompileSwallowsInput keeps clicks and
// keys away from the 3D view and every secondary window, and the per-frame work
// that moves things (a waypoint preview, a terrain stroke, Claude's tools) waits
// for it. The camera still flies — it is not part of the level.
//
// The finish (editorCompileFinish) comes back to the main thread, because a
// preview uploads GL buffers and installs them on the level.

import (
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"

	"foundry/internal/imguibackend"
)

const editorCompilePopup = "Compiling Level"

// compileStage is one step of the compile and when it began.
type compileStage struct {
	name  string
	start time.Duration // since the compile began
}

// editorCompileJob is the compile in flight. Everything but the progress block
// is the main thread's alone.
var editorCompileJob struct {
	running bool
	kind    compileKind
	title   string
	began   time.Time
	done    chan compileOutput
	// repose puts the waypoint preview back where it was posed: the compile
	// reads the level as authored, for as long as it runs.
	repose func()

	openPop bool
	shown   bool // the modal is up; it outlives running by a frame

	// The progress, written by the compile's own goroutines.
	mu       sync.Mutex
	stages   []compileStage
	fraction float32
}

// EditorCompiling reports whether a compile is running in the background.
func EditorCompiling() bool { return editorCompileJob.running }

// editorBeginCompile starts a compile behind the progress modal. The result
// lands in the status line, and the log, when it finishes. A request while one
// is running, or while a level is being opened, is ignored.
func editorBeginCompile(kind compileKind) {
	j := &editorCompileJob
	lvl := Editor.level
	if j.running || lvl == nil || EditorLevelLoading() {
		return
	}
	j.running, j.kind = true, kind
	j.title = filepath.Base(Editor.SavePath)
	if kind == compileToPreview {
		j.title += " (preview)"
	}
	j.began = time.Now()
	j.openPop = true
	j.mu.Lock()
	j.stages, j.fraction = nil, 0
	j.mu.Unlock()

	j.repose = waypointPreviewToRest()
	done := make(chan compileOutput, 1)
	j.done = done
	savePath := Editor.SavePath
	go func() {
		defer func() {
			// A panic in the compiler is a failed compile, not a dead editor.
			if r := recover(); r != nil {
				done <- compileOutput{err: fmt.Errorf("compiler panicked: %v", r)}
			}
		}()
		done <- editorCompileRun(lvl, kind, savePath, editorCompileProgress)
	}()
}

// editorCompileProgress is the compile's compile.Progress.
func editorCompileProgress(stage string, done float32) {
	j := &editorCompileJob
	j.mu.Lock()
	defer j.mu.Unlock()
	if n := len(j.stages); n == 0 || j.stages[n-1].name != stage {
		j.stages = append(j.stages, compileStage{name: stage, start: time.Since(j.began)})
	}
	j.fraction = done
}

// editorStepCompile finishes the compile on the frame after it is done. Called
// once per frame from EditorRender, before the UI.
func editorStepCompile() {
	j := &editorCompileJob
	if !j.running {
		return
	}
	var res compileOutput
	select {
	case res = <-j.done:
	default:
		return
	}
	j.running = false
	j.repose()
	j.repose = nil

	total := time.Since(j.began)
	j.mu.Lock()
	stages := j.stages
	j.mu.Unlock()
	for i, s := range stages {
		end := total
		if i+1 < len(stages) {
			end = stages[i+1].start
		}
		log.Printf("compile: %-40s %s", s.name, formatCompileDuration(end-s.start))
	}

	what := "Compile"
	if j.kind == compileToPreview {
		what = "Preview"
	}
	msg, err := editorCompileFinish(Editor.level, j.kind, res)
	if err != nil {
		editorSetStatus("%s failed: %v", what, err)
		return
	}
	editorSetStatus("%s (%s)", msg, formatCompileDuration(total))
}

// editorDrawCompile draws the progress modal while a compile runs: the step it
// is on, how far through it, and the steps already taken with what each cost.
func editorDrawCompile() {
	j := &editorCompileJob
	if !j.running && !j.shown {
		return
	}
	show := j.openPop
	if j.openPop {
		j.openPop, j.shown = false, true
	}
	imguibackend.WithModalPopup(editorCompilePopup, &show, func() {
		if !j.running {
			j.shown = false
			imgui.CloseCurrentPopup()
			return
		}
		elapsed := time.Since(j.began)
		j.mu.Lock()
		stages := append([]compileStage(nil), j.stages...)
		frac := j.fraction
		j.mu.Unlock()

		imgui.Text(fmt.Sprintf("%s — %s", j.title, formatCompileDuration(elapsed)))
		label, stageStart := "Starting", time.Duration(0)
		if n := len(stages); n > 0 {
			label, stageStart = stages[n-1].name, stages[n-1].start
		}
		bar := fmt.Sprintf("%d%%", int(frac*100+0.5))
		if frac <= 0 {
			// A step that does not count its way through still shows that
			// it is alive.
			bar = formatCompileDuration(elapsed - stageStart)
		}
		imgui.Text(label)
		imgui.ProgressBarV(frac, imgui.Vec2{X: 420, Y: 0}, bar)

		if len(stages) > 1 {
			imgui.Separator()
			imgui.BeginChildV("##compilestages", imgui.Vec2{X: 420, Y: 150}, false, 0)
			imgui.PushStyleColor(imgui.StyleColorText, releaseDoneColor)
			for i := 0; i < len(stages)-1; i++ {
				took := stages[i+1].start - stages[i].start
				imgui.Text(fmt.Sprintf("%-40s %s", stages[i].name, formatCompileDuration(took)))
			}
			imgui.PopStyleColor()
			if imgui.ScrollY() >= imgui.ScrollMaxY() {
				imgui.SetScrollHereY(1)
			}
			imgui.EndChild()
		}
	})
}

// EditorCompileSwallowsInput reports whether event is input to keep from the
// editor while a compile reads the level: every press, click, wheel and
// motion. Releases still go through, so a key held when the compile began is
// not left stuck down after it.
//
// Asked by the app's event loop ahead of everything else, so a secondary
// window — the popped-out sector map edits the same level — is covered too.
func EditorCompileSwallowsInput(event sdl.Event) bool {
	if !EditorCompiling() {
		return false
	}
	switch e := event.(type) {
	case sdl.KeyboardEvent:
		return e.Type == sdl.KEYDOWN
	case sdl.MouseButtonEvent:
		return e.Type == sdl.MOUSEBUTTONDOWN
	case sdl.TextInputEvent, sdl.TextEditingEvent, sdl.MouseWheelEvent, sdl.MouseMotionEvent:
		return true
	}
	return false
}

func formatCompileDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
