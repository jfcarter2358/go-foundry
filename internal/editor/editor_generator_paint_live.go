package editor

// editor_generator_paint_live.go — live painting: a stroke shows in the open
// level about a second after the mouse comes up (docs/terrain-paint/SPEC.md
// "Editor", docs/terrain-paint/PHASE_P2.md, docs/guide/36-terrain-paint.md).
//
// When the generator's target level is the one open in the level editor and
// the generator has already placed into it (its output folder has a
// manifest), every committed stroke — mouse-up in the 2D view or the 3D
// viewport, an undo, a redo — arms a DEBOUNCE (genLiveDebounce, 300 ms):
// strokes in quick succession make one rerun. When it fires:
//
//  1. THE RUN. procgen.Run on a CLONE of the document (the paint frozen
//     copy-on-write, so painting on is safe), off the UI thread, with no
//     modal. Only the stages that read the painted layer (and what reads
//     them) are stale, and they rerun per tile (procgen/paint_incremental.
//     go): a grass_field rewrites the fields of the tiles the stroke
//     reached, a scatter re-scatters those tiles, a material reapplies its
//     paint to cached unpainted weights. A stroke made while a run is in
//     flight waits for it and runs again after.
//  2. THE LIVE SPLICE (genLiveSplice), on the main window's context once no
//     compile, load or play session is in the way (genSpliceBlocked): the
//     open level is changed IN PLACE — no reload, no loading bar, no
//     level-editor undo step (paint has its own undo, and undoing a stroke
//     reruns this). Only the generator's entries under the prefixes of the
//     stages that ran (and of stages they supersede) are compared with the
//     run's: an entry whose raw form is unchanged is left alone, a changed
//     one is replaced at its index, a new one appended, a gone one removed
//     and released. Files the run rewrote are refreshed: grass fields
//     (render.ReloadGrassFields), textures (gfx.ReloadTexture by name),
//     and when a material stage ran its materials (material.LoadAll) and
//     the splat's data maps (gfx.RefreshFxDataMaps: the weights re-read on
//     their next bind instead of within the second). The collision index is
//     invalidated for the brush's raycasts.
//
// TIMES. Each live update records mouse-up → run start (the debounce), the
// run, and the splice; the status line and the log say them, and
// genPaintLive.last keeps them for the footer and the tests.

import (
	"context"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"foundry/internal/collision"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/levelio"
	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/render"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// genLiveDebounce is how long after the last commit the rerun starts.
const genLiveDebounce = 300 * time.Millisecond

// genLiveTimes is one live update's timings.
type genLiveTimes struct {
	// Wait is mouse-up to run start; Run the generator; Splice the level
	// edit (and file refreshes); Total mouse-up to the level edited.
	Wait, Run, Splice, Total time.Duration
	Ran                      []string
	Stats                    genLiveStats
}

func (t genLiveTimes) String() string {
	return fmt.Sprintf("%s after mouse-up (wait %s, run %s, splice %s; %s; %s)", t.Total.Round(time.Millisecond),
		t.Wait.Round(time.Millisecond), t.Run.Round(time.Millisecond), t.Splice.Round(time.Millisecond),
		strings.Join(t.Ran, ", "), t.Stats)
}

// genPaintLive is the live rerun's state; the UI thread's alone (the run's
// goroutine only sends on done and writes genPaintLiveLog).
var genPaintLive = struct {
	off bool // the bar's Live checkbox, on by default

	pending  bool      // a commit is waiting for its rerun
	due      time.Time // when the debounce fires
	strokeAt time.Time // the first waiting commit's mouse-up

	running bool
	began   time.Time
	from    time.Time // the mouse-up the running rerun answers
	cancel  context.CancelFunc
	done    chan genLiveOutput
	doc     *procgen.Doc

	// A finished run waiting for its splice.
	res      *procgen.Result
	resDoc   *procgen.Doc
	resTimes genLiveTimes
	resStart time.Time
	queued   bool

	last *genLiveTimes
}{}

// genPaintLiveLog is the live run's log, written from its goroutine.
var genPaintLiveLog struct {
	mu    sync.Mutex
	lines []string
}

type genLiveOutput struct {
	res *procgen.Result
	err error
}

// genLiveTarget is the open level's path when live painting can update it,
// or "" and why not.
func genLiveTarget() (string, string) {
	switch {
	case genDoc == nil:
		return "", "no generator"
	case genPaintLive.off:
		return "", "live updates are off"
	case genDoc.Level == "":
		return "", "the generator names no level"
	}
	p := levelio.LevelPath(genDoc.Level)
	if !genLevelIsOpen(p) {
		return "", "its level " + genDoc.Level + " is not open in the level editor"
	}
	d := &procgen.Doc{Name: genName()}
	if _, err := os.Stat(filepath.Join(procgen.OutDirFor(d, procgen.Options{}), procgen.ManifestFileName)); err != nil {
		return "", "the generator has not run yet (run it once; strokes then update the level live)"
	}
	return p, ""
}

// genPaintLiveKick arms the debounce: a stroke (or its undo) was committed.
func genPaintLiveKick() {
	l := &genPaintLive
	if _, why := genLiveTarget(); why != "" {
		return
	}
	now := time.Now()
	if !l.pending {
		l.strokeAt = now
	}
	l.pending, l.due = true, now.Add(genLiveDebounce)
}

// genPaintLiveStep is the once-a-frame poll: start a due rerun, collect a
// finished one, land a waiting splice. Never blocks.
func genPaintLiveStep() {
	l := &genPaintLive
	if l.running {
		select {
		case out := <-l.done:
			genPaintLiveFinished(out)
		default:
		}
	}
	if l.pending && !l.running && l.res == nil && time.Now().After(l.due) && !genRunJob.running && !EditorCompiling() {
		genPaintLiveStart()
	}
	genPaintLiveTrySplice()
}

// genPaintLiveStart starts the rerun on a clone of the document.
func genPaintLiveStart() {
	l := &genPaintLive
	l.pending = false
	if _, why := genLiveTarget(); why != "" {
		return
	}
	d := genDoc.Clone()
	d.Name = genName()
	if probs := d.Validate(); len(probs) > 0 || !procgen.ValidDocName(d.Name) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.running, l.cancel, l.doc, l.began, l.from = true, cancel, d, time.Now(), l.strokeAt
	genPaintLiveLog.mu.Lock()
	genPaintLiveLog.lines = nil
	genPaintLiveLog.mu.Unlock()
	done := make(chan genLiveOutput, 1)
	l.done = done
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- genLiveOutput{err: fmt.Errorf("generator panicked: %v", r)}
			}
		}()
		res, err := procgen.Run(ctx, d, procgen.Options{Log: func(line string) {
			genPaintLiveLog.mu.Lock()
			genPaintLiveLog.lines = append(genPaintLiveLog.lines, line)
			genPaintLiveLog.mu.Unlock()
		}})
		done <- genLiveOutput{res: res, err: err}
	}()
	genStatusf("Live: rerunning the tiles the stroke reached…")
}

// genPaintLiveFinished takes the run's result.
func genPaintLiveFinished(out genLiveOutput) {
	l := &genPaintLive
	l.running = false
	l.cancel()
	runTime := time.Since(l.began)
	if out.err != nil {
		genStatusf("Live update failed: %v", out.err)
		log.Printf("generator %s: live update failed: %v", l.doc.Name, out.err)
		return
	}
	l.res, l.resDoc, l.resStart = out.res, l.doc, l.began
	l.resTimes = genLiveTimes{Wait: l.began.Sub(l.from), Run: runTime, Ran: out.res.Ran}
	genStaleCache = nil
}

// genPaintLiveTrySplice lands a finished run in the open level when nothing
// is in the way.
func genPaintLiveTrySplice() {
	l := &genPaintLive
	if l.res == nil || l.queued {
		return
	}
	if genSpliceBlocked() != "" {
		return
	}
	l.queued = true
	uiwin.RequestWindowOpen(func() {
		l.queued = false
		if l.res == nil || genSpliceBlocked() != "" {
			return
		}
		res, d, times, began := l.res, l.resDoc, l.resTimes, l.resStart
		l.res, l.resDoc = nil, nil
		path := levelio.LevelPath(d.Level)
		if !genLevelIsOpen(path) {
			return
		}
		t0 := time.Now()
		st, err := genLiveSplice(Editor.level, d, res, began)
		times.Splice = time.Since(t0)
		times.Total = time.Since(l.from)
		times.Stats = st
		if err != nil {
			genStatusf("Live update failed: %v", err)
			return
		}
		l.last = &times
		genStatusf("Live: %s", times)
		log.Printf("generator %s: live update %s", d.Name, times)
	})
}

// genLiveStats is what a live splice changed.
type genLiveStats struct {
	Scenery, Volumes [3]int // replaced, added, removed
	Kept             int
	Grass, Textures  int
	Materials        bool
}

func (s genLiveStats) String() string {
	out := fmt.Sprintf("scenery ~%d +%d -%d, volumes ~%d +%d -%d, %d kept, %d grass fields and %d textures reloaded",
		s.Scenery[0], s.Scenery[1], s.Scenery[2], s.Volumes[0], s.Volumes[1], s.Volumes[2], s.Kept, s.Grass, s.Textures)
	if s.Materials {
		out += ", materials refreshed"
	}
	return out
}

// genLiveSplice edits lvl in place to hold res's entries for the stages that
// ran (file comment), and refreshes the files they rewrote since began.
func genLiveSplice(lvl *world.Level, d *procgen.Doc, res *procgen.Result, began time.Time) (genLiveStats, error) {
	var st genLiveStats
	if lvl == nil {
		return st, fmt.Errorf("no level open")
	}
	// The prefixes this run may have changed: its stages that ran and the
	// stages they supersede.
	var pre []string
	matStage := false
	for _, s := range res.Stages {
		if s.Skipped {
			continue
		}
		pre = append(pre, procgen.StagePrefix(res.Generator, s.ID))
		for sup := range s.Supersedes {
			pre = append(pre, procgen.StagePrefix(res.Generator, sup))
		}
		matStage = matStage || s.Kind == "terrain_material" || s.Kind == "region_material"
	}
	if len(pre) == 0 {
		return st, nil
	}
	under := func(id string) bool {
		for _, p := range pre {
			if strings.HasPrefix(id, p) {
				return true
			}
		}
		return false
	}
	opt := genlevel.Options{ChunkAt: func(x, y, z float32) string { return world.SceneryChunkAt(lvl, mgl32.Vec3{x, y, z}) }}

	// Materials and textures first, so a replaced placement loads with them.
	st.Textures = genLiveReloadTextures(res, began)
	if matStage {
		material.LoadAll()
		// The splat's weights are a data map the material keeps by name;
		// it re-reads a rewritten file on its next bind.
		gfx.RefreshFxDataMaps()
		st.Materials = true
	}

	// Scenery.
	want := map[string]world.RawScenery{}
	var order []string
	for _, p := range res.Placements() {
		if under(p.ID) {
			want[p.ID] = world.SceneryToRaw(ptrScenery(world.SceneryFromRaw(genlevel.ToRawScenery(p, opt))))
			order = append(order, p.ID)
		}
	}
	have := map[string]bool{}
	out := lvl.Scenery[:0:0]
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if !under(s.ID) {
			out = append(out, *s)
			continue
		}
		raw, ok := want[s.ID]
		have[s.ID] = true
		switch {
		case !ok:
			s.Delete()
			st.Scenery[2]++
		case reflect.DeepEqual(world.SceneryToRaw(s), raw):
			out = append(out, *s)
			st.Kept++
		default:
			s.Delete()
			n := world.SceneryFromRaw(raw)
			if err := n.Load(); err != nil {
				log.Printf("live update: scenery %s: %v", n.ID, err)
			}
			out = append(out, n)
			st.Scenery[0]++
		}
	}
	for _, id := range order {
		if have[id] {
			continue
		}
		n := world.SceneryFromRaw(want[id])
		if err := n.Load(); err != nil {
			log.Printf("live update: scenery %s: %v", n.ID, err)
		}
		out = append(out, n)
		st.Scenery[1]++
	}
	if st.Scenery != [3]int{} {
		lvl.Scenery = out
		Editor.ActiveSceneryIdx = -1
		collision.InvalidateScenery()
	}

	// Volumes; grass fields rewritten since the run began are reloaded.
	wantV := map[string]world.Volume{}
	var orderV []string
	for _, e := range res.Volumes() {
		id, _ := e["id"].(string)
		if !under(id) {
			continue
		}
		raw, err := genlevel.ToRawVolume(e)
		if err != nil {
			return st, err
		}
		v, err := world.VolumeFromRaw(raw)
		if err != nil {
			return st, err
		}
		wantV[id] = v
		orderV = append(orderV, id)
	}
	var reload []string
	note := func(v *world.Volume) {
		if v.IsGrass() && fileNewer(fileutil.ResolveAssetPath(v.Grass.File), began) {
			reload = append(reload, v.Grass.File)
		}
	}
	haveV := map[string]bool{}
	outV := lvl.Volumes[:0:0]
	changedV := false
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if !under(v.ID) {
			outV = append(outV, *v)
			continue
		}
		haveV[v.ID] = true
		nv, ok := wantV[v.ID]
		switch {
		case !ok:
			st.Volumes[2]++
			changedV = true
		case reflect.DeepEqual(world.VolumeToRaw(v), world.VolumeToRaw(&nv)):
			outV = append(outV, *v)
			note(v)
		default:
			outV = append(outV, nv)
			note(&nv)
			st.Volumes[0]++
			changedV = true
		}
	}
	for _, id := range orderV {
		if !haveV[id] {
			nv := wantV[id]
			outV = append(outV, nv)
			note(&nv)
			st.Volumes[1]++
			changedV = true
		}
	}
	if changedV {
		lvl.Volumes = outV
		Editor.ActiveVolumeIdx = -1
	}
	render.ReloadGrassFields(reload)
	st.Grass = len(reload)
	return st, nil
}

// genLiveReloadTextures reloads every .png the ran stages list that was
// written since began, by its texture name; how many.
func genLiveReloadTextures(res *procgen.Result, began time.Time) int {
	n := 0
	for _, s := range res.Stages {
		if s.Skipped {
			continue
		}
		for _, f := range s.Files {
			if !strings.HasSuffix(f, ".png") {
				continue
			}
			abs := filepath.Join(res.OutDir, filepath.FromSlash(f))
			if !fileNewer(abs, began) {
				continue
			}
			name := strings.TrimSuffix(path.Join(path.Dir(s.RelDir), f), ".png")
			if _, known := gfx.TextureFile(name); !known {
				continue // never drawn: nothing resident to refresh
			}
			if err := gfx.ReloadTexture(name); err == nil {
				n++
			}
		}
	}
	return n
}

// fileNewer reports whether the file at p was modified at or after t.
func fileNewer(p string, t time.Time) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.ModTime().Before(t.Add(-10*time.Millisecond))
}

func ptrScenery(s world.Scenery) *world.Scenery { return &s }
