package editor

// editor_generator_job.go — the Generator editor's two background jobs.
//
// The RUN is the compile job's pattern (editor_compile_job.go): procgen.Run on
// a goroutine with recover, a mutex-guarded progress block it writes (the
// stage, how far, the stages already done with what each took, the log), a
// non-blocking step on the UI thread that notices it has finished, and a
// WithModalPopup modal with a progress bar and Cancel. The run works on a
// CLONE of the document, so editing on while it runs is safe — though the
// modal keeps the editor's own panels from being clicked, the level window
// stays live.
//
// The PREVIEW is the same idea without the modal. Every edit bumps genRev;
// genStepJobs notices a rev the current preview was not made from, waits for
// the edits to settle (genPreviewDebounce), cancels a preview still running
// for an older rev, and starts one for this one. The stage kinds' previews
// are fast by contract (a quarter resolution, a handful of iterations), and
// procgen.PreviewCache keeps upstream stages' previews, so an edit to a
// downstream stage re-previews only it.
//
// When a run finishes with placements and the document names a level, the
// splice follows (editor_generator_splice.go).

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/material"
	"foundry/internal/procgen"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const genRunPopup = "Running Generator"

// genPreviewDebounce is how long the document must hold still before a
// preview starts: long enough that a slider drag does not start forty, short
// enough that letting go of one shows the result at once.
const genPreviewDebounce = 180 * time.Millisecond

// genLogMax bounds the run log kept for the modal.
const genLogMax = 400

// genRunStage is one finished-or-current stage in the modal's list.
type genRunStage struct {
	label string
	start time.Duration
}

// genRunJob is the run in flight. Everything outside the mu block is the UI
// thread's alone.
var genRunJob struct {
	running bool
	began   time.Time
	title   string
	cancel  context.CancelFunc
	done    chan genRunOutput
	doc     *procgen.Doc // the clone being run

	openPop bool
	shown   bool

	mu       sync.Mutex
	stages   []genRunStage
	fraction float32
	logLines []string

	// last is the most recent run's outcome, kept for the footer.
	last string
}

type genRunOutput struct {
	res *procgen.Result
	err error
}

// GeneratorRunning reports whether a generator run is in flight.
func GeneratorRunning() bool { return genRunJob.running }

// genBeginRun starts a run of the current document: force reruns every
// stage, only (when set) runs just those stages. Refused while one runs or
// while the document has problems (which the footer lists).
func genBeginRun(force bool, only []string) {
	j := &genRunJob
	if j.running || genDoc == nil {
		return
	}
	d := genDoc.Clone()
	d.Name = genName()
	if probs := d.Validate(); len(probs) > 0 {
		genStatusf("Not run: %s", probs[0])
		return
	}
	if !procgen.ValidDocName(d.Name) {
		genStatusf("Not run: name the generator first (File > Save As)")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.running, j.cancel, j.doc = true, cancel, d
	j.began = time.Now()
	j.title = d.Name
	if len(only) > 0 {
		j.title += " — " + fmt.Sprint(only)
	} else if force {
		j.title += " (force)"
	}
	j.openPop = true
	j.mu.Lock()
	j.stages, j.fraction, j.logLines = nil, 0, nil
	j.mu.Unlock()

	done := make(chan genRunOutput, 1)
	j.done = done
	opts := procgen.Options{Force: force, Only: only, Progress: genRunProgress, Log: genRunLog}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- genRunOutput{err: fmt.Errorf("generator panicked: %v", r)}
			}
		}()
		res, err := procgen.Run(ctx, d, opts)
		done <- genRunOutput{res: res, err: err}
	}()
}

// genCancelRun asks the run to stop. Stages check often; the modal stays up
// until the stage in hand has wound down.
func genCancelRun() {
	if genRunJob.running && genRunJob.cancel != nil {
		genRunJob.cancel()
		genRunLog("cancelling...")
	}
}

func genRunProgress(stage string, done float32) {
	j := &genRunJob
	j.mu.Lock()
	defer j.mu.Unlock()
	if n := len(j.stages); n == 0 || j.stages[n-1].label != stage {
		j.stages = append(j.stages, genRunStage{label: stage, start: time.Since(j.began)})
	}
	j.fraction = done
}

func genRunLog(line string) {
	j := &genRunJob
	j.mu.Lock()
	defer j.mu.Unlock()
	j.logLines = append(j.logLines, line)
	if len(j.logLines) > genLogMax {
		j.logLines = j.logLines[len(j.logLines)-genLogMax:]
	}
}

// genStepJobs is the once-a-frame poll of both jobs. Never blocks.
func genStepJobs() {
	genStepRun()
	genStepPreview()
	genTrySplice() // a splice waiting for a compile or a load to finish
}

func genStepRun() {
	j := &genRunJob
	if !j.running {
		return
	}
	var out genRunOutput
	select {
	case out = <-j.done:
	default:
		return
	}
	j.running = false
	j.cancel()
	total := time.Since(j.began)
	if out.err != nil {
		j.last = fmt.Sprintf("Run failed after %s: %v", formatCompileDuration(total), out.err)
		if out.err == procgen.ErrCancelled {
			j.last = "Run cancelled after " + formatCompileDuration(total)
		}
		genStatus = j.last
		log.Printf("generator %s: %s", j.doc.Name, j.last)
		return
	}
	res := out.res
	j.last = fmt.Sprintf("Ran %d, up to date %d, %d placements in %s", len(res.Ran), len(res.Skipped),
		len(res.Placements())+len(res.Volumes()), formatCompileDuration(total))
	genStatus = j.last
	log.Printf("generator %s: %s", j.doc.Name, j.last)
	genStaleCache = nil
	// What the stages wrote outside their folders: imported textures and
	// the materials they wear (docs/canyon/PHASE_5.md), published before the
	// splice puts placements wearing them in the level.
	gfx.RescanTextures()
	material.LoadAll()
	if j.doc.Level != "" {
		genRequestSplice(j.doc, res)
	}
}

// genDrawRunModal draws the progress modal while a run is in flight.
func genDrawRunModal() {
	j := &genRunJob
	if !j.running && !j.shown {
		return
	}
	show := j.openPop
	if j.openPop {
		j.openPop, j.shown = false, true
	}
	imguibackend.WithModalPopup(genRunPopup, &show, func() {
		if !j.running {
			j.shown = false
			imgui.CloseCurrentPopup()
			return
		}
		elapsed := time.Since(j.began)
		j.mu.Lock()
		stages := append([]genRunStage(nil), j.stages...)
		frac := j.fraction
		lines := append([]string(nil), j.logLines...)
		j.mu.Unlock()

		imgui.Text(fmt.Sprintf("%s — %s", j.title, formatCompileDuration(elapsed)))
		label := "Starting"
		if n := len(stages); n > 0 {
			label = stages[n-1].label
		}
		imgui.Text(label)
		imgui.ProgressBarV(frac, imgui.Vec2{X: 460, Y: 0}, fmt.Sprintf("%d%%", int(frac*100+0.5)))
		if len(stages) > 1 {
			imgui.PushStyleColor(imgui.StyleColorText, releaseDoneColor)
			for i := 0; i < len(stages)-1; i++ {
				imgui.Text(fmt.Sprintf("%-40s %s", stages[i].label, formatCompileDuration(stages[i+1].start-stages[i].start)))
			}
			imgui.PopStyleColor()
		}
		imgui.Separator()
		imgui.BeginChildV("##genrunlog", imgui.Vec2{X: 460, Y: 160}, true, 0)
		for _, l := range lines {
			imgui.Text(l)
		}
		if imgui.ScrollY() >= imgui.ScrollMaxY() {
			imgui.SetScrollHereY(1)
		}
		imgui.EndChild()
		if imgui.Button("Cancel##genruncancel") {
			genCancelRun()
		}
	})
}

// ---------- the preview ----------

// genPreview is the preview state. data is the latest finished preview (for
// rev dataRev, of stage dataStage); a job is the one in flight.
var genPreview struct {
	cache *procgen.PreviewCache

	wantRev  uint64 // the rev a preview has been asked for
	askedAt  time.Time
	asked    bool
	jobRev   uint64
	jobStage string
	cancel   context.CancelFunc
	done     chan genPreviewOutput
	running  bool
	began    time.Time

	mu       sync.Mutex
	progress float32

	data      *procgen.PreviewData
	dataRev   uint64
	dataStage string
	err       error
	// uploaded is the rev the data's textures were made for.
	uploaded uint64
}

type genPreviewOutput struct {
	rev   uint64
	stage string
	data  *procgen.PreviewData
	err   error
}

// genRequestPreview asks for a preview of the current document as it now is.
func genRequestPreview() {
	genPreview.wantRev = genRev
	genPreview.askedAt = time.Now()
	genPreview.asked = true
}

func genStepPreview() {
	p := &genPreview
	if p.cache == nil {
		p.cache = procgen.NewPreviewCache(24)
	}
	// A finished job lands first.
	if p.running {
		select {
		case out := <-p.done:
			p.running = false
			p.cancel()
			if out.err != procgen.ErrCancelled {
				p.data, p.dataRev, p.dataStage, p.err = out.data, out.rev, out.stage, out.err
			}
		default:
		}
	}
	st := genSelectedStage()
	if st == nil || !p.asked {
		return
	}
	if time.Since(p.askedAt) < genPreviewDebounce {
		return
	}
	if p.running {
		if p.jobRev == p.wantRev && p.jobStage == st.ID {
			return // already making this one
		}
		p.cancel() // stale: it will land as cancelled and be dropped
		return
	}
	if p.dataRev == p.wantRev && p.dataStage == st.ID {
		p.asked = false
		return
	}
	d := genDoc.Clone()
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.running, p.jobRev, p.jobStage, p.began = cancel, true, p.wantRev, st.ID, time.Now()
	p.asked = false
	done := make(chan genPreviewOutput, 1)
	p.done = done
	rev, id, cache := p.wantRev, st.ID, p.cache
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- genPreviewOutput{rev: rev, stage: id, err: fmt.Errorf("preview panicked: %v", r)}
			}
		}()
		data, err := procgen.Preview(ctx, d, id, cache, procgen.PreviewOptions{Progress: genPreviewProgress})
		done <- genPreviewOutput{rev: rev, stage: id, data: data, err: err}
	}()
}

func genPreviewProgress(_ string, done float32) {
	genPreview.mu.Lock()
	genPreview.progress = done
	genPreview.mu.Unlock()
}

// genPreviewBusy reports a preview in flight, and how far through.
func genPreviewBusy() (bool, float32) {
	genPreview.mu.Lock()
	defer genPreview.mu.Unlock()
	return genPreview.running, genPreview.progress
}

// genCurrentPreview is the preview to draw: the latest one for the selected
// stage, even if it is a rev or two behind (the viewport would flicker
// between "nothing" and "something" on every drag otherwise).
func genCurrentPreview() *procgen.PreviewData {
	st := genSelectedStage()
	if st == nil || genPreview.dataStage != st.ID {
		return nil
	}
	return genPreview.data
}

// ---------- staleness, for the stage list ----------

// genStaleCache is the last StaleStages answer, the rev it was for and when
// it was taken; a run clears it.
var (
	genStaleCache map[string]bool
	genStaleRev   uint64
	genStaleAt    time.Time
	genStaleErr   error
)

// genStaleEvery bounds how often the stage list re-asks: StaleStages reads
// the manifest and stats every output, and a drag bumps the rev every frame.
const genStaleEvery = 400 * time.Millisecond

// genStageStale reports whether a plain Run would run the stage. Asked again
// when the document has changed, at most every genStaleEvery.
func genStageStale(id string) bool {
	if genStaleCache == nil || (genStaleRev != genRev && time.Since(genStaleAt) >= genStaleEvery) {
		genStaleCache, genStaleRev, genStaleAt = map[string]bool{}, genRev, time.Now()
		d := genDoc.Clone()
		d.Name = genName()
		stale, err := procgen.StaleStages(d, procgen.Options{})
		genStaleErr = err
		for _, s := range stale {
			genStaleCache[s] = true
		}
	}
	return genStaleCache[id]
}
