package editor

// editor_region_adjacency.go — the adjacency rules in the level editor
// (docs/regions/SPEC.md, phase 5): the forbidden-pair warnings in a region
// volume's inspector, and "Insert intermediate region".
//
// The check (genlevel.LevelAdjacency: the regions' weights sampled on a
// coarse raster, every overlapping pair looked up in the project's adjacency
// table) takes tens of milliseconds for a handful of regions — too much for
// every frame, and pointless while a point is being dragged. So it runs OFF
// the UI thread, DEBOUNCED:
//
//   - each frame something wants the answer (a region's inspector is up, or
//     the Generator's region list), regionAdjacencyTick takes a signature
//     of the open level's regions (the synced items' text) and of the
//     adjacency file; when it differs from the last one checked and has
//     held still for regionAdjacencyDebounce, a goroutine checks a COPY of
//     the region volumes (taken on the UI thread) with the project's table
//     and the level's world generator's warp (stages.RegionWarp: the same
//     meander a run uses, so the warnings agree);
//   - the result lands under a mutex with the signature it answers; the
//     inspector shows the latest result, marked "checking…" while a newer
//     one is pending.
//
// "Insert intermediate region" is an ordinary level edit: one EditorPushUndo,
// then the new region volume(s) genlevel.InsertIntermediate planned are
// appended (IDs made unique against the whole level), loaded and the first
// selected. The next tick re-checks, and the warning clears.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"foundry/internal/levelio"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/stages"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// regionAdjacencyDebounce is how long the regions must hold still before
// they are checked.
const regionAdjacencyDebounce = 300 * time.Millisecond

// regionAdjacencyResult is one finished check.
type regionAdjacencyResult struct {
	sig   string
	pairs []regions.Adjacent
	err   error
}

// regionAdjChecker runs one kind of adjacency check off the UI thread,
// debounced (file comment). The level editor's (levelAdj) checks the open
// level; the Generator's (genAdj, editor_generator_region_list.go) checks
// the document's synced regions when the level is not open.
type regionAdjChecker struct {
	mu sync.Mutex
	// seen is the last signature looked at and when it first appeared;
	// asked the one a check was started for.
	seen      string
	seenAt    time.Time
	asked     string
	running   bool
	result    regionAdjacencyResult
	hasResult bool
}

// levelAdj is the open level's check.
var levelAdj regionAdjChecker

// tick starts check (on a goroutine) when sig changed and has held still for
// the debounce; cheap otherwise.
func (a *regionAdjChecker) tick(sig string, check func() regionAdjacencyResult) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if sig != a.seen {
		a.seen, a.seenAt = sig, now
	}
	if a.running || sig == a.asked || now.Sub(a.seenAt) < regionAdjacencyDebounce {
		return
	}
	a.asked, a.running = sig, true
	go func() {
		res := check()
		res.sig = sig
		a.mu.Lock()
		a.result, a.hasResult, a.running = res, true, false
		a.mu.Unlock()
	}()
}

// latest is the latest finished check, whether there is one, and whether it
// answers the last signature seen (false: a newer check is pending).
func (a *regionAdjChecker) latest() (regionAdjacencyResult, bool, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.result, a.hasResult, a.hasResult && a.result.sig == a.seen
}

// forbidden is the latest check's forbidden pairs involving region id
// (every one for "").
func (a *regionAdjChecker) forbidden(id string) []regions.Adjacent {
	res, ok, _ := a.latest()
	if !ok {
		return nil
	}
	var out []regions.Adjacent
	for _, p := range regions.ForbiddenPairs(res.pairs) {
		if id == "" || p.IDA == id || p.IDB == id {
			out = append(out, p)
		}
	}
	return out
}

// regionAdjacencyRaw is the open level's own region volumes, as raw entries
// (a copy the check goroutine may read).
func regionAdjacencyRaw() *world.RawLevel {
	if Editor.level == nil {
		return nil
	}
	return genOpenLevelVolumes()
}

// regionAdjacencySig is the signature of the regions and the table.
func regionAdjacencySig(raw *world.RawLevel) string {
	items, err := genlevel.RegionItems(raw)
	if err != nil {
		return "error:" + err.Error()
	}
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "%v\n", it)
	}
	return b.String() + adjacencyFileSig()
}

// adjacencyFileSig changes when the project's adjacency.yaml does.
func adjacencyFileSig() string {
	if st, err := os.Stat(stages.AdjacencyPath()); err == nil {
		return fmt.Sprintf("adj:%d:%d", st.Size(), st.ModTime().UnixNano())
	}
	return ""
}

// regionAdjacencyTick checks the open level's regions when they changed and
// have held still. Call it from whatever shows the answer.
func regionAdjacencyTick() {
	raw := regionAdjacencyRaw()
	if raw == nil {
		return
	}
	level := ""
	if Editor.SavePath != "" {
		level = levelio.LevelNameFromPath(Editor.SavePath)
	}
	levelAdj.tick(regionAdjacencySig(raw), func() regionAdjacencyResult { return regionAdjacencyCheck(raw, level) })
}

// regionAdjacencyCheck is the level check itself (off the UI thread).
func regionAdjacencyCheck(raw *world.RawLevel, level string) regionAdjacencyResult {
	adj, err := stages.ProjectAdjacency()
	if err != nil {
		return regionAdjacencyResult{err: err}
	}
	var doc *procgen.Doc
	if level != "" {
		if d, err := procgen.ReadDoc(procgen.DocPath(stages.LevelWorldDocName(level))); err == nil {
			doc = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, pairs, err := genlevel.LevelAdjacency(ctx, raw, adj, stages.RegionWarp(doc))
	return regionAdjacencyResult{pairs: pairs, err: err}
}

// regionAdjacencyWait blocks until the level check has answered the regions
// as they are now (tests; never the UI).
func regionAdjacencyWait(a *regionAdjChecker, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, ok, current := a.latest(); ok && current {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// editorRegionAdjacencyWarnings draws a region's forbidden neighbours in its
// inspector, each with its Insert button.
func editorRegionAdjacencyWarnings(v *world.Volume) {
	regionAdjacencyTick()
	res, ok, current := levelAdj.latest()
	if !ok {
		editorTextMuted("Adjacency: checking...")
		return
	}
	if res.err != nil {
		editorTextColored(scriptIssueColor, "Adjacency check: "+res.err.Error())
		return
	}
	forb := levelAdj.forbidden(v.ID)
	if len(forb) == 0 {
		msg := "Adjacency: no forbidden neighbours."
		if !current {
			msg += " (checking...)"
		}
		editorTextMuted(msg)
		return
	}
	for i, p := range forb {
		editorTextColored(scriptIssueColor, p.Warning())
		if c := p.Suggestion(); c != "" {
			if imgui.Button(fmt.Sprintf("Insert %s region##volregins%d", c, i)) {
				editorInsertIntermediate(p.IDA, p.IDB)
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Add a " + c + " region between " + p.IDA + " and " + p.IDB + ",\n" +
					"covering the band where they meet, with a priority above both.\nOne undo step.")
			}
		}
	}
	if !current {
		editorTextMuted("(checking...)")
	}
}

// editorInsertIntermediate adds the region volume(s) separating the
// forbidden pair (a, b) to the open level: one undo step. Reports the new
// IDs.
func editorInsertIntermediate(a, b string) []string {
	lvl := Editor.level
	if lvl == nil {
		return nil
	}
	adj, err := stages.ProjectAdjacency()
	if err != nil {
		editorSetStatus("Insert intermediate region: %v", err)
		return nil
	}
	raw := genOpenLevelVolumes()
	n := len(raw.Volumes)
	if _, err := genlevel.InsertIntermediate(raw, adj, a, b); err != nil {
		editorSetStatus("Insert intermediate region: %v", err)
		return nil
	}
	EditorPushUndo()
	var ids []string
	for _, rv := range raw.Volumes[n:] {
		v, err := world.VolumeFromRaw(rv)
		if err != nil {
			editorSetStatus("Insert intermediate region: %v", err)
			continue
		}
		v.ID = world.UniqueVolumeID(lvl, v.ID)
		if err := v.Load(); err != nil {
			editorSetStatus("Volume %s: %v", v.ID, err)
		}
		lvl.Volumes = append(lvl.Volumes, v)
		ids = append(ids, v.ID)
	}
	if len(ids) > 0 {
		editorSelectVolume(len(lvl.Volumes) - len(ids))
		env := lvl.Volumes[len(lvl.Volumes)-len(ids)].Region.Environment
		editorSetStatus("Inserted %s region %s between %s and %s", env, strings.Join(ids, ", "), a, b)
	}
	return ids
}
