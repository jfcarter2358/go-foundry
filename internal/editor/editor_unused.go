package editor

// editor_unused.go — the Unused Assets window: the files in resources/ that
// nothing in the game uses, listed so they can be cleaned out of the project.
//
// The analysis is internal/pack's (pack.FindUnused): an asset is unused when a
// pruned release would leave it out, so this window and the Release window
// can never disagree about a file. A scan reads every level and script, which
// on a big project is seconds, so it runs on a goroutine and the window draws
// from what the last one left.
//
// Deleting is here because cleaning up is the point, but it is always a
// choice made file by file (or by a Select button) and confirmed: the one
// thing the analysis cannot see is a name a script builds in a way it does not
// recognise, and a deleted file is gone unless resources/ is under git.

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/pack"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const (
	unusedWindowW = 820
	unusedWindowH = 640
)

// unusedShared is what the scan goroutine writes and the window reads.
type unusedShared struct {
	mu sync.Mutex

	scanning bool
	loaded   bool // a scan has finished at least once
	report   pack.UnusedReport
	err      error
	at       time.Time
	// allLevels is the setting the report was made with, which the checkbox
	// may since have moved away from.
	allLevels bool
}

var (
	unusedWindow *uiwin.UIWindow
	unusedState  unusedShared
	// unusedPending lets a test wait for a scan before it looks.
	unusedPending sync.WaitGroup

	// What the window's own controls edit. Only the window touches these.
	unusedAllLevels   = true
	unusedFilter      string
	unusedSelected    = map[string]bool{}
	unusedShowConfirm bool
	unusedDeleteMsg   string
	unusedDeleteErr   error
)

// UnusedWindowOpen reports whether the window is up.
func UnusedWindowOpen() bool { return unusedWindow != nil }

// OpenUnusedWindow opens the Unused Assets window, or raises it, and scans.
func OpenUnusedWindow() {
	if unusedWindow != nil {
		unusedWindow.Win.Raise()
		return
	}
	w, err := uiwin.NewUIWindow("Unused Assets", unusedWindowW, unusedWindowH, drawUnusedWindow, onUnusedWindowClose)
	if err != nil {
		log.Printf("open unused assets window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.09, 0.09, 0.11, 1}
	unusedWindow = w
	unusedScan()
}

func onUnusedWindowClose() { unusedWindow = nil }

// unusedScan starts a scan with the current settings, unless one is running.
func unusedScan() {
	s := &unusedState
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return
	}
	s.scanning = true
	s.mu.Unlock()

	src, allLevels := fileutil.ResourcesPath, unusedAllLevels
	unusedPending.Add(1)
	go func() {
		defer unusedPending.Done()
		r, err := pack.FindUnused(pack.UnusedOptions{Src: src, AllLevels: allLevels})
		s.mu.Lock()
		defer s.mu.Unlock()
		s.scanning, s.loaded = false, true
		s.report, s.err, s.at, s.allLevels = r, err, time.Now(), allLevels
	}()
}

// unusedView is one frame's copy of the shared state.
type unusedView struct {
	scanning  bool
	loaded    bool
	report    pack.UnusedReport
	err       error
	at        time.Time
	allLevels bool
}

func unusedSnapshot() unusedView {
	s := &unusedState
	s.mu.Lock()
	defer s.mu.Unlock()
	return unusedView{s.scanning, s.loaded, s.report, s.err, s.at, s.allLevels}
}

func drawUnusedWindow() {
	w := unusedWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	if !imgui.BeginV("##unusedroot", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse) {
		imgui.End()
		return
	}
	drawUnusedBody(unusedSnapshot())
	imgui.End()
}

// drawUnusedBody is everything inside the window, split out so a test can draw
// it in a bare imgui frame.
func drawUnusedBody(v unusedView) {
	editorTextMutedWrapped("Files in resources/ that no level, script or other asset refers to. " +
		"A name a script builds at runtime is followed when its fixed part is " +
		"written down (format(\"ammo_{}\", n), \"pickup_\" + kind); check anything assembled " +
		"another way before deleting it.")
	imgui.Spacing()

	label := "Rescan"
	if !v.loaded {
		label = "Scan"
	}
	if v.scanning {
		imguibackend.EditorPushDisabled(true)
		imgui.Button("Scanning...")
		imguibackend.EditorPopDisabled()
	} else if imgui.Button(label) {
		unusedScan()
	}
	imgui.SameLine()
	if imgui.Checkbox("Count every level as used", &unusedAllLevels) && !v.scanning {
		unusedScan()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("On: only assets no level refers to at all.\n" +
			"Off: also levels the start level never leads to, and everything only they use.")
	}
	imgui.SameLine()
	imgui.SetNextItemWidth(-1)
	imgui.InputTextWithHint("##unusedfilter", "filter", &unusedFilter)

	if v.err != nil {
		editorTextColored(gitWarnColor, "Scan failed: "+v.err.Error())
		return
	}
	if !v.loaded {
		editorTextMuted("Scanning resources/...")
		return
	}

	// Selections the last scan no longer lists (deleted, or used again) go.
	listed := map[string]pack.UnusedAsset{}
	for _, a := range v.report.Unused {
		listed[a.Path] = a
	}
	var selN int
	var selBytes int64
	for p := range unusedSelected {
		a, ok := listed[p]
		if !ok {
			delete(unusedSelected, p)
			continue
		}
		selN++
		selBytes += a.Size
	}

	imgui.Text(fmt.Sprintf("%d of %d assets unused, %s", len(v.report.Unused), v.report.Scanned,
		pack.FormatSize(v.report.UnusedBytes)))
	imgui.SameLine()
	if v.allLevels != unusedAllLevels && !v.scanning {
		editorTextColored(gitWarnColor, "  (setting changed — Rescan)")
	} else {
		editorTextMuted("  scanned " + v.at.Format("15:04:05"))
	}
	if selN > 0 {
		imgui.SameLine()
		editorTextMuted(fmt.Sprintf("  %d selected, %s", selN, pack.FormatSize(selBytes)))
	}

	shown := unusedFiltered(v.report.Unused, unusedFilter)
	if imgui.Button("Select shown") {
		for _, a := range shown {
			unusedSelected[a.Path] = true
		}
	}
	imgui.SameLine()
	if imgui.Button("Select backups") {
		for _, a := range shown {
			if pack.IsDevelopmentFile(a.Path) {
				unusedSelected[a.Path] = true
			}
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The *.bak copies the editor leaves beside every file it saves.")
	}
	imgui.SameLine()
	if imgui.Button("Select none") {
		unusedSelected = map[string]bool{}
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(selN == 0 || v.scanning)
	if imgui.Button(fmt.Sprintf("Delete %d...", selN)) {
		unusedShowConfirm = true
	}
	imguibackend.EditorPopDisabled()
	imguibackend.WithModalPopup("Delete unused assets?", &unusedShowConfirm, drawUnusedDeleteConfirm)

	if unusedDeleteErr != nil {
		editorTextColored(gitWarnColor, unusedDeleteErr.Error())
	} else if unusedDeleteMsg != "" {
		editorTextMuted(unusedDeleteMsg)
	}

	imgui.Separator()
	imgui.BeginChildV("##unusedlist", imgui.Vec2{}, false, 0)
	if len(v.report.Unused) == 0 {
		editorTextMuted("Nothing unused. Every asset is reached from the game.")
	}
	for _, g := range unusedGroups(shown) {
		title := fmt.Sprintf("%s/  (%d, %s)##unusedgrp%s", g.dir, len(g.assets), pack.FormatSize(g.bytes), g.dir)
		if !imgui.CollapsingHeaderV(title, imgui.TreeNodeFlagsDefaultOpen) {
			continue
		}
		for _, a := range g.assets {
			drawUnusedRow(a)
		}
	}
	imgui.EndChild()
}

func drawUnusedRow(a pack.UnusedAsset) {
	on := unusedSelected[a.Path]
	if imgui.Checkbox(a.Path+"##unused", &on) {
		if on {
			unusedSelected[a.Path] = true
		} else {
			delete(unusedSelected, a.Path)
		}
	}
	imgui.SameLine()
	editorTextMuted(pack.FormatSize(a.Size))
	if len(a.ReferencedBy) > 0 {
		imgui.SameLine()
		editorTextMuted("— named only by unused " + unusedShortList(a.ReferencedBy))
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Also unused, so nothing reaches this through them:\n" + strings.Join(a.ReferencedBy, "\n"))
		}
	}
}

// unusedShortList names the first two of a list and counts the rest.
func unusedShortList(paths []string) string {
	if len(paths) <= 2 {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s, %s and %d more", paths[0], paths[1], len(paths)-2)
}

func drawUnusedDeleteConfirm() {
	paths := make([]string, 0, len(unusedSelected))
	for p := range unusedSelected {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	imgui.Text(fmt.Sprintf("Delete %d file(s) from resources/?", len(paths)))
	editorTextMuted("They cannot be got back unless resources/ is under version control (Window > Git).")
	imgui.BeginChildV("##unusedconfirmlist", imgui.Vec2{X: 520, Y: 180}, true, 0)
	for _, p := range paths {
		imgui.Text(p)
	}
	imgui.EndChild()
	if imgui.Button("Delete") {
		done, err := pack.DeleteUnused(fileutil.ResourcesPath, paths)
		unusedDeleteErr = err
		unusedDeleteMsg = fmt.Sprintf("Deleted %d file(s).", len(done))
		log.Printf("unused assets: deleted %d file(s) from %s", len(done), fileutil.ResourcesPath)
		unusedSelected = map[string]bool{}
		unusedScan()
		imgui.CloseCurrentPopup()
	}
	imgui.SameLine()
	if imgui.Button("Cancel") {
		imgui.CloseCurrentPopup()
	}
}

// unusedFiltered is the assets whose path contains the filter, ignoring case.
func unusedFiltered(all []pack.UnusedAsset, filter string) []pack.UnusedAsset {
	f := strings.ToLower(strings.TrimSpace(filter))
	if f == "" {
		return all
	}
	var out []pack.UnusedAsset
	for _, a := range all {
		if strings.Contains(strings.ToLower(a.Path), f) {
			out = append(out, a)
		}
	}
	return out
}

type unusedGroup struct {
	dir    string
	assets []pack.UnusedAsset
	bytes  int64
}

// unusedGroups splits a path-sorted list by its top directory, in order.
func unusedGroups(assets []pack.UnusedAsset) []unusedGroup {
	var out []unusedGroup
	for _, a := range assets {
		dir, _, _ := strings.Cut(a.Path, "/")
		if len(out) == 0 || out[len(out)-1].dir != dir {
			out = append(out, unusedGroup{dir: dir})
		}
		g := &out[len(out)-1]
		g.assets = append(g.assets, a)
		g.bytes += a.Size
	}
	return out
}
