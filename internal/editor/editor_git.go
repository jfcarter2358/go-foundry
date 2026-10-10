package editor

// editor_git.go — the Git window: resources/ under version control, from inside
// the editor.
//
// What it manages is fileutil.ResourcesPath, the folder every asset is read from
// and saved to. That folder may be a repository of its own, a folder inside a
// bigger one, or in none yet — internal/vcs covers all three, and the window
// says which it is looking at.
//
// A window of its own, like Release: a push takes as long as the network does,
// and the level stays in reach meanwhile. Every git call runs on a goroutine
// and the window draws from what the last one left; nothing here blocks a
// frame.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/uiwin"
	"foundry/internal/vcs"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const (
	gitWindowW = 980
	gitWindowH = 720

	// gitRefreshEvery is how often an idle window rereads the repository, so a
	// level saved in the editor shows up as a change without a click.
	gitRefreshEvery = 2 * time.Second
	// gitHistoryLen is how much history the History tab lists.
	gitHistoryLen = 100
	// gitShownDiffLines bounds the diff drawn. imgui lays out every line it is
	// handed, and a regenerated level is tens of thousands.
	gitShownDiffLines = 4000
	// gitKeptOutput is how many operations' output the window remembers.
	gitKeptOutput = 50
)

var (
	gitAddColor    = imgui.Vec4{X: 0.55, Y: 0.85, Z: 0.60, W: 1}
	gitDelColor    = imgui.Vec4{X: 1.00, Y: 0.50, Z: 0.50, W: 1}
	gitHunkColor   = imgui.Vec4{X: 0.45, Y: 0.75, Z: 1.00, W: 1}
	gitWarnColor   = imgui.Vec4{X: 1.00, Y: 0.80, Z: 0.35, W: 1}
	gitStagedColor = imgui.Vec4{X: 0.55, Y: 0.85, Z: 0.60, W: 1}
)

// gitOutput is what one finished operation said.
type gitOutput struct {
	Label string
	Text  string
	Err   error
	At    time.Time
}

// gitOperation is the one mutating git command allowed at a time.
type gitOperation struct {
	Label   string
	Started time.Time
	cancel  context.CancelFunc
}

// gitShared is everything the goroutines write and the window reads. Held
// under mu; the window copies what it needs at the top of a frame.
type gitShared struct {
	mu sync.Mutex

	status    vcs.Status
	statusErr error
	history   []vcs.Commit
	loaded    bool // a refresh has finished at least once

	refreshing     bool
	refreshAgain   bool
	lastRefresh    time.Time
	opsFinished    int // bumped as each operation ends; a refresh begun before is stale
	op             *gitOperation
	output         []gitOutput
	filesChanged   bool // an operation rewrote files the open level may have loaded
	diffKey        string
	diffText       string
	diffErr        error
	diffWantedKey  string
	diffLoadingKey string
}

var (
	gitWindow *uiwin.UIWindow
	gitState  gitShared
	// gitPending counts the goroutines still running a git command, so a test
	// can wait for them before it resets the state they write.
	gitPending sync.WaitGroup

	// What the window's own controls edit. Only the window touches these.
	gitCommitMsg   string
	gitNewBranch   string
	gitRemoteURL   string
	gitIdentName   string
	gitIdentEmail  string
	gitSelected    string // selected file's Path on the Changes tab
	gitSelectedRev string // selected commit's Hash on the History tab
	gitShowDiscard bool
	gitDiscardFile vcs.FileStatus
	gitAvailErr    error
)

// gitRepo is the directory the window manages.
func gitRepo() vcs.Repo { return vcs.Repo{Dir: fileutil.ResourcesPath} }

// GitWindowOpen reports whether the window is up.
func GitWindowOpen() bool { return gitWindow != nil }

// GitOperationRunning reports whether a git command (other than a status read)
// is in progress.
func GitOperationRunning() bool {
	gitState.mu.Lock()
	defer gitState.mu.Unlock()
	return gitState.op != nil
}

// OpenGitWindow opens the Git window, or raises it if it is already up.
func OpenGitWindow() {
	if gitWindow != nil {
		gitWindow.Win.Raise()
		return
	}
	gitAvailErr = vcs.Available()
	w, err := uiwin.NewUIWindow("Git — resources", gitWindowW, gitWindowH, drawGitWindow, onGitWindowClose)
	if err != nil {
		log.Printf("open git window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.09, 0.09, 0.11, 1}
	gitWindow = w
	gitRefresh()
}

func onGitWindowClose() {
	gitWindow = nil
}

// CancelGitOperation stops a git command still running. Called as the editor
// shuts down, as CancelReleaseBuild is.
func CancelGitOperation() {
	gitState.mu.Lock()
	defer gitState.mu.Unlock()
	if gitState.op != nil {
		gitState.op.cancel()
	}
}

// gitRefresh rereads status and history on a goroutine. One at a time: asked
// for while one is running, it runs again once that one is done.
func gitRefresh() {
	if gitAvailErr != nil {
		return
	}
	s := &gitState
	s.mu.Lock()
	if s.refreshing {
		s.refreshAgain = true
		s.mu.Unlock()
		return
	}
	s.refreshing = true
	seq := s.opsFinished
	s.mu.Unlock()

	repo := gitRepo()
	gitPending.Add(1)
	go func() {
		defer gitPending.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		st, err := repo.Status(ctx)
		var hist []vcs.Commit
		if err == nil && st.IsRepo && st.Head != "" {
			hist, err = repo.Log(ctx, gitHistoryLen)
		}

		s.mu.Lock()
		again := s.refreshAgain || s.opsFinished != seq
		// A refresh that started before an operation ended read the repository
		// as it was before; the one that operation asks for replaces it.
		if s.opsFinished == seq {
			s.status, s.statusErr, s.history, s.loaded = st, err, hist, true
		}
		s.refreshing, s.refreshAgain = false, false
		s.lastRefresh = time.Now()
		s.mu.Unlock()
		if again {
			gitRefresh()
		}
	}()
}

// gitRun starts an operation, unless one is already running. rewrites says the
// operation can change files on disk under the level the editor has open, and
// earns the window's offer to reload it.
func gitRun(label string, rewrites bool, fn func(ctx context.Context, repo vcs.Repo) (string, error)) {
	s := &gitState
	s.mu.Lock()
	if s.op != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.op = &gitOperation{Label: label, Started: time.Now(), cancel: cancel}
	s.mu.Unlock()

	repo := gitRepo()
	gitPending.Add(1)
	go func() {
		defer gitPending.Done()
		defer cancel()
		out, err := fn(ctx, repo)
		if ctx.Err() == context.Canceled {
			err = fmt.Errorf("cancelled")
		}
		s.mu.Lock()
		s.op = nil
		s.opsFinished++
		if rewrites && err == nil {
			s.filesChanged = true
		}
		s.output = append(s.output, gitOutput{Label: label, Text: strings.TrimSpace(out), Err: err, At: time.Now()})
		if n := len(s.output) - gitKeptOutput; n > 0 {
			s.output = s.output[n:]
		}
		// Whatever was on the diff pane may no longer be what is on disk.
		s.diffKey = ""
		s.mu.Unlock()
		if err != nil {
			log.Printf("git: %s: %v", label, err)
		}
		gitRefresh()
	}()
}

// gitWantDiff asks for the diff pane to show key, loaded by fn. It is loaded
// once, on a goroutine, and again only when an operation ends or key changes.
func gitWantDiff(key string, fn func(ctx context.Context, repo vcs.Repo) (string, error)) {
	s := &gitState
	s.mu.Lock()
	s.diffWantedKey = key
	if key == "" || s.diffKey == key || s.diffLoadingKey == key {
		s.mu.Unlock()
		return
	}
	s.diffLoadingKey = key
	s.mu.Unlock()

	repo := gitRepo()
	gitPending.Add(1)
	go func() {
		defer gitPending.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := fn(ctx, repo)
		s.mu.Lock()
		if s.diffLoadingKey == key {
			s.diffLoadingKey = ""
		}
		if s.diffWantedKey == key {
			s.diffKey, s.diffText, s.diffErr = key, out, err
		}
		s.mu.Unlock()
	}()
}

// gitView is one frame's copy of gitShared.
type gitView struct {
	status       vcs.Status
	statusErr    error
	history      []vcs.Commit
	loaded       bool
	op           *gitOperation
	output       []gitOutput
	filesChanged bool
	diffKey      string
	diffText     string
	diffErr      error
}

func gitSnapshot() gitView {
	s := &gitState
	s.mu.Lock()
	v := gitView{
		status: s.status, statusErr: s.statusErr, history: s.history, loaded: s.loaded,
		output: s.output, filesChanged: s.filesChanged,
		diffKey: s.diffKey, diffText: s.diffText, diffErr: s.diffErr,
	}
	if s.op != nil {
		op := *s.op
		v.op = &op
	}
	due := s.op == nil && !s.refreshing && time.Since(s.lastRefresh) > gitRefreshEvery
	s.mu.Unlock()
	if due {
		gitRefresh()
	}
	return v
}

// drawGitWindow renders one frame, with this window's contexts current.
func drawGitWindow() {
	w := gitWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	if !imgui.BeginV("##gitroot", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse) {
		imgui.End()
		return
	}
	drawGitBody(gitSnapshot())
	imgui.End()
}

// drawGitBody is everything inside the window, split out so a test can draw it
// in a bare imgui frame.
func drawGitBody(v gitView) {
	editorTextMuted("Folder   " + fileutil.ResourcesPath)
	switch {
	case gitAvailErr != nil:
		editorTextColored(releaseErrorColor, "git is not installed, or not on the PATH: "+gitAvailErr.Error())
		return
	case !v.loaded:
		editorTextMuted("Reading the repository…")
		return
	case v.statusErr != nil:
		editorTextColored(releaseErrorColor, v.statusErr.Error())
		drawGitOperation(v)
		return
	case !v.status.IsRepo:
		drawGitInit(v)
		return
	}

	st := v.status
	if st.Prefix != "" {
		editorTextMuted(fmt.Sprintf("Inside the repository at %s — branches, pull and push are that repository's.", st.Root))
	}
	busy := v.op != nil
	imguibackend.EditorPushDisabled(busy)
	drawGitBranch(st)
	drawGitSetup(st)
	imguibackend.EditorPopDisabled()
	drawGitFilesChanged(v)
	imgui.Separator()

	drawGitOperation(v)
	imgui.Separator()

	if imgui.BeginTabBar("##gittabs") {
		if imgui.BeginTabItem(fmt.Sprintf("Changes (%d)###gitchanges", len(st.Files))) {
			drawGitChanges(v)
			imgui.EndTabItem()
		}
		if imgui.BeginTabItem("History###githistory") {
			drawGitHistory(v)
			imgui.EndTabItem()
		}
		imgui.EndTabBar()
	}

	imguibackend.WithModalPopup("Discard changes?", &gitShowDiscard, drawGitDiscardConfirm)
}

// drawGitInit is the window for a folder in no repository.
func drawGitInit(v gitView) {
	imgui.Text("resources/ is not under version control.")
	editorTextMuted("Initialising makes it a git repository of its own, with a .gitignore that\n" +
		"leaves out the *.bak copies every save keeps. Nothing is committed yet.")
	imguibackend.EditorPushDisabled(v.op != nil)
	if imgui.Button("Initialise repository") {
		gitRun("Initialise", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Init(ctx) })
	}
	imguibackend.EditorPopDisabled()
	drawGitOperation(v)
}

// drawGitBranch is the branch, where it stands against its upstream, and the
// commands that move it.
func drawGitBranch(st vcs.Status) {
	imgui.Text("Branch")
	imgui.SameLine()
	imgui.SetNextItemWidth(220)
	current := st.Branch
	if current == "" {
		current = "(detached)"
	}
	if imguibackend.BeginCombo("##gitbranch", current) {
		for _, b := range st.Branches {
			if imguibackend.SelectableV(b, b == st.Branch, 0, imgui.Vec2{}) && b != st.Branch {
				name := b
				gitRun("Switch to "+name, true, func(ctx context.Context, r vcs.Repo) (string, error) {
					return r.Switch(ctx, name)
				})
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Check out another branch. Uncommitted changes that would be\noverwritten stop the switch; commit or discard them first.")
	}
	imgui.SameLine()
	imgui.SetNextItemWidth(160)
	imgui.InputTextWithHint("##gitnewbranch", "new branch", &gitNewBranch)
	imgui.SameLine()
	imguibackend.EditorPushDisabled(strings.TrimSpace(gitNewBranch) == "")
	if imgui.Button("Create") && strings.TrimSpace(gitNewBranch) != "" {
		name := strings.TrimSpace(gitNewBranch)
		gitNewBranch = ""
		gitRun("Create branch "+name, false, func(ctx context.Context, r vcs.Repo) (string, error) {
			return r.CreateBranch(ctx, name)
		})
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Start a branch here and switch to it. Changes not yet committed come along.")
	}

	imgui.SameLine()
	switch {
	case st.Head == "":
		editorTextMuted("   no commits yet")
	case st.Upstream == "":
		editorTextMuted("   not pushed anywhere yet")
	case st.Ahead == 0 && st.Behind == 0:
		editorTextMuted("   up to date with " + st.Upstream)
	default:
		editorTextColored(gitWarnColor, fmt.Sprintf("   %d to push, %d to pull  (%s)", st.Ahead, st.Behind, st.Upstream))
	}

	hasRemote := len(st.Remotes) > 0
	imguibackend.EditorPushDisabled(!hasRemote)
	if imgui.Button("Fetch") {
		gitRun("Fetch", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Fetch(ctx) })
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("See what the remote has, without changing any files.")
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(st.Upstream == "")
	if imgui.Button("Pull") {
		gitRun("Pull", true, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Pull(ctx) })
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Bring in the upstream's commits. Fast-forward only: when both sides\n" +
			"have new commits, git reports it rather than merging.")
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(st.Head == "" || (st.Upstream != "" && st.Ahead == 0))
	if imgui.Button("Push") {
		s := st
		gitRun("Push", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Push(ctx, s) })
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		if st.Upstream == "" {
			imgui.SetTooltip(fmt.Sprintf("Push this branch to %s and track it there.", vcs.PushRemote(st.Remotes)))
		} else {
			imgui.SetTooltip("Send this branch's commits to " + st.Upstream + ".")
		}
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if imgui.Button("Refresh") {
		gitRefresh()
	}
	if hasRemote {
		imgui.SameLine()
		editorTextMuted("   remotes: " + strings.Join(st.Remotes, ", "))
	}
}

// drawGitSetup asks for what git needs and does not have: somewhere to push,
// and a name to sign commits with.
func drawGitSetup(st vcs.Status) {
	if len(st.Remotes) == 0 {
		editorTextMuted("No remote. Add one to push and pull:")
		imgui.SameLine()
		imgui.SetNextItemWidth(420)
		imgui.InputTextWithHint("##gitremote", "https://… or git@host:owner/repo.git", &gitRemoteURL)
		imgui.SameLine()
		url := strings.TrimSpace(gitRemoteURL)
		imguibackend.EditorPushDisabled(url == "")
		if imgui.Button("Add remote") && url != "" {
			gitRemoteURL = ""
			gitRun("Add remote origin", false, func(ctx context.Context, r vcs.Repo) (string, error) {
				return r.AddRemote(ctx, "origin", url)
			})
		}
		imguibackend.EditorPopDisabled()
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Saved as \"origin\". A remote that needs a password needs a credential\n" +
				"helper or an SSH key: the editor cannot answer git's prompts.")
		}
	}
	if st.UserName == "" || st.UserEmail == "" {
		if gitIdentName == "" {
			gitIdentName = st.UserName
		}
		if gitIdentEmail == "" {
			gitIdentEmail = st.UserEmail
		}
		editorTextColored(gitWarnColor, "Commits need a name and email:")
		imgui.SameLine()
		imgui.SetNextItemWidth(180)
		imgui.InputTextWithHint("##gitname", "name", &gitIdentName)
		imgui.SameLine()
		imgui.SetNextItemWidth(220)
		imgui.InputTextWithHint("##gitemail", "email", &gitIdentEmail)
		imgui.SameLine()
		name, email := strings.TrimSpace(gitIdentName), strings.TrimSpace(gitIdentEmail)
		imguibackend.EditorPushDisabled(name == "" || email == "")
		if imgui.Button("Save##gitident") && name != "" && email != "" {
			gitRun("Set identity", false, func(ctx context.Context, r vcs.Repo) (string, error) {
				return r.SetIdentity(ctx, name, email)
			})
		}
		imguibackend.EditorPopDisabled()
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Saved in this repository's own config, not your global one.")
		}
	}
}

// drawGitFilesChanged offers to reload the level after git rewrote files.
func drawGitFilesChanged(v gitView) {
	if !v.filesChanged {
		return
	}
	editorTextColored(gitWarnColor, "Files in resources/ changed on disk.")
	imgui.SameLine()
	imguibackend.EditorPushDisabled(Editor.SavePath == "" || EditorLevelLoading())
	if imgui.Button("Reload level") {
		EditorBeginLoadLevel(Editor.SavePath)
		gitClearFilesChanged()
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Reopen the level from disk. Unsaved edits to it are lost.\n" +
			"Textures and models already loaded keep what they had\nuntil the editor restarts.")
	}
	imgui.SameLine()
	if imgui.Button("Dismiss##gitchanged") {
		gitClearFilesChanged()
	}
}

func gitClearFilesChanged() {
	gitState.mu.Lock()
	gitState.filesChanged = false
	gitState.mu.Unlock()
}

// drawGitOperation is the command running, or what the last one said.
func drawGitOperation(v gitView) {
	if v.op != nil {
		if imgui.Button("Cancel##gitop") {
			v.op.cancel()
		}
		imgui.SameLine()
		editorTextColored(gitWarnColor, fmt.Sprintf("%s… %s", v.op.Label, time.Since(v.op.Started).Round(time.Second)))
		return
	}
	if len(v.output) == 0 {
		editorTextMuted("Ready")
		return
	}
	last := v.output[len(v.output)-1]
	if last.Err != nil {
		editorTextColored(releaseErrorColor, last.Label+" failed:")
		imgui.PushTextWrapPos()
		editorTextColored(releaseErrorColor, last.Err.Error())
		imgui.PopTextWrapPos()
		return
	}
	text := last.Text
	if text == "" {
		text = "done"
	}
	if lines := strings.Split(text, "\n"); len(lines) > 4 {
		text = strings.Join(lines[len(lines)-4:], "\n")
	}
	editorTextColored(releaseDoneColor, last.Label+": "+text)
}

// drawGitChanges is the changed files, what changed in the one selected, and
// the commit box.
func drawGitChanges(v gitView) {
	st := v.status
	busy := v.op != nil
	avail := imgui.ContentRegionAvail()
	commitH := float32(128)
	listW := avail.X * 0.42

	imgui.BeginChildV("##gitfiles", imgui.Vec2{X: listW, Y: avail.Y - commitH}, true, 0)
	imguibackend.EditorPushDisabled(busy || len(st.Files) == 0)
	if imgui.Button("Stage all") {
		gitRun("Stage all", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Stage(ctx) })
	}
	imgui.SameLine()
	if imgui.Button("Unstage all") {
		gitRun("Unstage all", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Unstage(ctx) })
	}
	imguibackend.EditorPopDisabled()
	if len(st.Files) == 0 {
		editorTextMuted("Nothing has changed since the last commit.")
	}
	var selected *vcs.FileStatus
	for i := range st.Files {
		f := st.Files[i]
		if f.Path == gitSelected {
			selected = &st.Files[i]
		}
		on := f.Staged()
		imguibackend.EditorPushDisabled(busy)
		if imgui.Checkbox("##gitstage"+f.Path, &on) {
			path := f.Path
			if on {
				gitRun("Stage "+path, false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Stage(ctx, path) })
			} else {
				paths := []string{path}
				if f.OrigPath != "" {
					paths = append(paths, f.OrigPath)
				}
				gitRun("Unstage "+path, false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Unstage(ctx, paths...) })
			}
		}
		imguibackend.EditorPopDisabled()
		if imgui.IsItemHovered() {
			switch {
			case f.Staged() && f.Unstaged():
				imgui.SetTooltip("Partly staged: the file changed again after it was staged.\nTick to stage the rest.")
			case f.Staged():
				imgui.SetTooltip("In the next commit. Untick to leave it out.")
			default:
				imgui.SetTooltip("Tick to put it in the next commit.")
			}
		}
		imgui.SameLine()
		col := imgui.Vec4{X: 0.8, Y: 0.8, Z: 0.8, W: 1}
		switch {
		case f.Conflicted():
			col = releaseErrorColor
		case f.Staged():
			col = gitStagedColor
		}
		label := fmt.Sprintf("%-9s %s", f.Label(), f.Path)
		if f.OrigPath != "" {
			label += "  ← " + f.OrigPath
		}
		imgui.PushStyleColor(imgui.StyleColorText, col)
		if imguibackend.SelectableV(label+"##gitfile", f.Path == gitSelected, 0, imgui.Vec2{}) {
			gitSelected = f.Path
		}
		imgui.PopStyleColor()
	}
	imgui.EndChild()

	imgui.SameLine()
	imgui.BeginChildV("##gitdiffpane", imgui.Vec2{X: 0, Y: avail.Y - commitH}, true, 0)
	if selected == nil {
		gitWantDiff("", nil)
		editorTextMuted("Select a file to see what changed in it.")
	} else {
		f := *selected
		imguibackend.EditorPushDisabled(busy)
		if imgui.Button("Discard changes…") {
			gitDiscardFile = f
			gitShowDiscard = true
		}
		imguibackend.EditorPopDisabled()
		imgui.SameLine()
		editorTextMuted(f.Path)
		gitWantDiff(gitFileDiffKey(f), func(ctx context.Context, r vcs.Repo) (string, error) {
			return gitFileDiff(ctx, r, f)
		})
		drawGitDiff(v, gitFileDiffKey(f))
	}
	imgui.EndChild()

	drawGitCommit(st, busy)
}

// gitFileDiffKey names a file's diff by everything that decides what it is, so
// staging the file asks for it again.
func gitFileDiffKey(f vcs.FileStatus) string {
	return fmt.Sprintf("file:%s:%c%c", f.Path, f.Index, f.Work)
}

// gitFileDiff is a file's staged change and its unstaged one, each under a
// heading when it has both.
func gitFileDiff(ctx context.Context, r vcs.Repo, f vcs.FileStatus) (string, error) {
	if !f.Staged() || !f.Unstaged() {
		return r.Diff(ctx, f, f.Staged())
	}
	staged, err := r.Diff(ctx, f, true)
	if err != nil {
		return staged, err
	}
	unstaged, err := r.Diff(ctx, f, false)
	return "## Staged\n" + staged + "\n## Not staged\n" + unstaged, err
}

// drawGitDiff draws the diff pane's text, coloured by line.
func drawGitDiff(v gitView, key string) {
	imgui.BeginChildV("##gitdiff", imgui.Vec2{}, false, imgui.WindowFlagsHorizontalScrollbar)
	defer imgui.EndChild()
	if v.diffKey != key {
		editorTextMuted("Loading…")
		return
	}
	if v.diffErr != nil {
		editorTextColored(releaseErrorColor, v.diffErr.Error())
	}
	text := strings.TrimRight(v.diffText, "\n")
	if text == "" && v.diffErr == nil {
		editorTextMuted("No difference in content (a mode or line-ending change).")
		return
	}
	lines := strings.Split(text, "\n")
	hidden := 0
	if len(lines) > gitShownDiffLines {
		hidden = len(lines) - gitShownDiffLines
		lines = lines[:gitShownDiffLines]
	}
	for _, line := range lines {
		if col, ok := gitDiffLineColor(line); ok {
			editorTextColored(col, line)
		} else {
			imgui.Text(line)
		}
	}
	if hidden > 0 {
		editorTextMuted(fmt.Sprintf("… %d more lines", hidden))
	}
}

// gitDiffLineColor is the colour a line of diff is drawn in, if it is not the
// default.
func gitDiffLineColor(line string) (imgui.Vec4, bool) {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"),
		strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "new file"), strings.HasPrefix(line, "deleted file"),
		strings.HasPrefix(line, "similarity"), strings.HasPrefix(line, "rename "):
		return imgui.Vec4{X: 0.6, Y: 0.6, Z: 0.65, W: 1}, true
	case strings.HasPrefix(line, "## "), strings.HasPrefix(line, "commit "):
		return gitWarnColor, true
	case strings.HasPrefix(line, "@@"):
		return gitHunkColor, true
	case strings.HasPrefix(line, "+"):
		return gitAddColor, true
	case strings.HasPrefix(line, "-"):
		return gitDelColor, true
	}
	return imgui.Vec4{}, false
}

// gitCommitBlocked is why Commit is disabled, or "".
func gitCommitBlocked(st vcs.Status, msg string) string {
	switch {
	case st.StagedOutside > 0:
		return fmt.Sprintf("%d file(s) outside resources/ are staged in %s, and a commit would take them too. Unstage them there first.",
			st.StagedOutside, st.Root)
	case st.UserName == "" || st.UserEmail == "":
		return "Set a name and email above first."
	case st.Staged() == 0:
		return "Tick the files to commit."
	case strings.TrimSpace(msg) == "":
		return "Say what the commit changes."
	}
	return ""
}

// drawGitCommit is the message box and the Commit button.
func drawGitCommit(st vcs.Status, busy bool) {
	imgui.InputTextMultilineV("##gitmsg", &gitCommitMsg, imgui.Vec2{X: -1, Y: 64}, 0, nil)
	blocked := gitCommitBlocked(st, gitCommitMsg)
	imguibackend.EditorPushDisabled(busy || blocked != "")
	if imgui.Button(fmt.Sprintf("Commit %d file(s)", st.Staged())) && !busy && blocked == "" {
		msg := gitCommitMsg
		gitCommitMsg = ""
		gitRun("Commit", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Commit(ctx, msg) })
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if blocked != "" {
		editorTextMuted(blocked)
	} else {
		// The same surprise the Release window warns of: git sees the files,
		// and the level on screen may not match them.
		editorTextMuted("Commits what is saved on disk. Save the level first.")
	}
}

// drawGitDiscardConfirm is the body of the "Discard changes?" modal.
func drawGitDiscardConfirm() {
	f := gitDiscardFile
	if f.Untracked() || f.Index == 'A' {
		imgui.Text("Delete " + f.Path + "?")
		editorTextMuted("It has never been committed, so it cannot be got back.")
	} else {
		imgui.Text("Put " + f.Path + " back the way the last commit has it?")
		editorTextMuted("Every change to it since, staged or not, is lost.")
	}
	if imgui.Button("Discard") {
		gitRun("Discard "+f.Path, true, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Discard(ctx, f) })
		imgui.CloseCurrentPopup()
	}
	imgui.SameLine()
	if imgui.Button("Keep") {
		imgui.CloseCurrentPopup()
	}
}

// drawGitHistory is the commits that touched resources/, and the one selected.
func drawGitHistory(v gitView) {
	avail := imgui.ContentRegionAvail()
	imgui.BeginChildV("##gitlog", imgui.Vec2{X: avail.X * 0.42, Y: 0}, true, 0)
	if len(v.history) == 0 {
		editorTextMuted("No commits yet.")
	}
	for _, c := range v.history {
		label := fmt.Sprintf("%s  %s##gitrev%s", c.Short, c.Subject, c.Hash)
		if imguibackend.SelectableV(label, c.Hash == gitSelectedRev, 0, imgui.Vec2{}) {
			gitSelectedRev = c.Hash
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(fmt.Sprintf("%s\n%s, %s", c.Subject, c.Author, c.When.Format("2 Jan 2006 15:04")))
		}
	}
	imgui.EndChild()

	imgui.SameLine()
	imgui.BeginChildV("##gitshowpane", imgui.Vec2{}, true, 0)
	if gitSelectedRev == "" {
		gitWantDiff("", nil)
		editorTextMuted("Select a commit to see what it changed.")
	} else {
		rev := gitSelectedRev
		key := "rev:" + rev
		gitWantDiff(key, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Show(ctx, rev) })
		drawGitDiff(v, key)
	}
	imgui.EndChild()
}
