package editor

// The Git window, drawn for real with no window and no GL in each state the
// repository can be in, and the goroutine machinery behind it run against a
// scratch repository.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/vcs"
)

func withGitResources(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prevRes, prevAvail := fileutil.ResourcesPath, gitAvailErr
	t.Cleanup(func() {
		gitPending.Wait()
		fileutil.ResourcesPath, gitAvailErr = prevRes, prevAvail
		gitState = gitShared{}
		gitSelected, gitSelectedRev, gitCommitMsg = "", "", ""
	})
	fileutil.ResourcesPath = dir
	gitAvailErr = nil
	gitState = gitShared{}
	return dir
}

func TestGitWindowDraws(t *testing.T) {
	withGitResources(t)
	repoStatus := vcs.Status{
		IsRepo: true, Root: "/p", Prefix: "resources/", Branch: "main", Head: "abc",
		Upstream: "origin/main", Ahead: 1, Behind: 2,
		Files: []vcs.FileStatus{
			{Path: "levels/intro.yaml", Index: 'M', Work: 'M'},
			{Path: "textures/new.png", Index: '?', Work: '?'},
			{Path: "scripts/b.flang", OrigPath: "scripts/a.flang", Index: 'R', Work: ' '},
			{Path: "ui/hud.fui", Index: 'U', Work: 'U', Conflict: true},
		},
		Branches: []string{"main", "art"},
	}
	views := []gitView{
		{},
		{loaded: true},
		{loaded: true, statusErr: errors.New("fatal: bad object")},
		{loaded: true, status: repoStatus, op: &gitOperation{Label: "Push", Started: time.Now(), cancel: func() {}}},
		{loaded: true, status: repoStatus, filesChanged: true,
			output: []gitOutput{{Label: "Pull", Err: errors.New("fatal: Not possible to fast-forward")}}},
		{loaded: true, status: vcs.Status{IsRepo: true, Remotes: []string{"origin"}, UserName: "a", UserEmail: "b"},
			output: []gitOutput{{Label: "Commit", Text: "[main abc] one\n 1 file changed"}}},
	}
	for _, v := range views {
		withImgui(func() { drawReleaseFrame(func() { drawGitBody(v) }) })
	}

	// With a file selected and its diff loaded, and a commit selected on the
	// history side.
	gitSelected = "levels/intro.yaml"
	v := gitView{loaded: true, status: repoStatus,
		diffKey:  gitFileDiffKey(repoStatus.Files[0]),
		diffText: "## Staged\ndiff --git a/x b/x\n@@ -1 +1 @@\n-old\n+new\n"}
	withImgui(func() { drawReleaseFrame(func() { drawGitBody(v) }) })
	v.history = []vcs.Commit{{Hash: "abc123", Short: "abc", Subject: "One", Author: "A", When: time.Now()}}
	gitSelectedRev = "abc123"
	withImgui(func() { drawReleaseFrame(func() { drawGitHistory(v) }) })

	gitAvailErr = errors.New("executable file not found")
	withImgui(func() { drawReleaseFrame(func() { drawGitBody(gitView{}) }) })
}

func TestGitCommitBlocked(t *testing.T) {
	ok := vcs.Status{UserName: "a", UserEmail: "b", Files: []vcs.FileStatus{{Path: "x", Index: 'M', Work: ' '}}}
	if r := gitCommitBlocked(ok, "msg"); r != "" {
		t.Fatalf("blocked: %s", r)
	}
	for name, c := range map[string]struct {
		st  vcs.Status
		msg string
	}{
		"no message":     {ok, " \n"},
		"nothing staged": {vcs.Status{UserName: "a", UserEmail: "b", Files: []vcs.FileStatus{{Path: "x", Index: ' ', Work: 'M'}}}, "msg"},
		"no identity":    {vcs.Status{Files: ok.Files}, "msg"},
		"staged outside": {vcs.Status{UserName: "a", UserEmail: "b", Files: ok.Files, StagedOutside: 1}, "msg"},
	} {
		if gitCommitBlocked(c.st, c.msg) == "" {
			t.Errorf("%s: not blocked", name)
		}
	}
}

// waitGit waits for the operation and the refreshes behind it to finish.
func waitGit(t *testing.T) gitView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gitState.mu.Lock()
		idle := gitState.op == nil && !gitState.refreshing && gitState.loaded
		gitState.mu.Unlock()
		if idle {
			gitState.mu.Lock()
			defer gitState.mu.Unlock()
			return gitView{status: gitState.status, statusErr: gitState.statusErr, history: gitState.history,
				output: gitState.output, filesChanged: gitState.filesChanged}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("git did not finish")
	return gitView{}
}

// Initialise, stage, commit and discard through the window's own runner.
func TestGitRunner(t *testing.T) {
	if vcs.Available() != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := withGitResources(t)

	gitRefresh()
	if v := waitGit(t); v.status.IsRepo {
		t.Fatalf("a bare folder read as a repository: %+v", v.status)
	}

	run := func(label string, rewrites bool, fn func(context.Context, vcs.Repo) (string, error)) gitView {
		t.Helper()
		gitRun(label, rewrites, fn)
		v := waitGit(t)
		if last := v.output[len(v.output)-1]; last.Err != nil {
			t.Fatalf("%s: %v", label, last.Err)
		}
		return v
	}
	run("init", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Init(ctx) })
	if err := os.WriteFile(filepath.Join(dir, "game.yaml"), []byte("title: x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("identity", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.SetIdentity(ctx, "A", "a@example.com") })
	run("stage", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Stage(ctx) })
	v := run("commit", false, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Commit(ctx, "first") })
	if len(v.history) != 1 || v.history[0].Subject != "first" || len(v.status.Files) != 0 {
		t.Fatalf("after commit: %+v %+v", v.history, v.status.Files)
	}

	if err := os.WriteFile(filepath.Join(dir, "game.yaml"), []byte("title: y\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRefresh()
	v = waitGit(t)
	if len(v.status.Files) != 1 {
		t.Fatalf("change not seen: %+v", v.status.Files)
	}
	f := v.status.Files[0]
	diff, err := gitFileDiff(context.Background(), gitRepo(), f)
	if err != nil || !strings.Contains(diff, "+title: y") {
		t.Fatalf("diff = %q, %v", diff, err)
	}
	v = run("discard", true, func(ctx context.Context, r vcs.Repo) (string, error) { return r.Discard(ctx, f) })
	if !v.filesChanged || len(v.status.Files) != 0 {
		t.Fatalf("after discard: changed=%v files=%+v", v.filesChanged, v.status.Files)
	}
}
