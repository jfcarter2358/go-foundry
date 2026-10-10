package vcs

// Against a real git, in scratch repositories: the directory as a repository of
// its own, and as a folder inside a bigger one.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needGit(t *testing.T) {
	t.Helper()
	if Available() != nil {
		t.Skip("git is not installed")
	}
	// A commit needs an identity, and the test's must not depend on whoever
	// runs it — or on their global config's hooks and signing.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// must fails the test on a call's error: must(t)(r.Stage(ctx)).
func must(t *testing.T) func(string, error) {
	t.Helper()
	return func(_ string, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func status(t *testing.T, r Repo) Status {
	t.Helper()
	s, err := r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func file(s Status, path string) (FileStatus, bool) {
	for _, f := range s.Files {
		if f.Path == path {
			return f, true
		}
	}
	return FileStatus{}, false
}

func TestNotARepository(t *testing.T) {
	needGit(t)
	s := status(t, Repo{Dir: t.TempDir()})
	if s.IsRepo {
		t.Fatalf("a bare folder read as a repository: %+v", s)
	}
}

// The whole round: init, stage, commit, change, discard, branch — in a
// directory that is its own repository.
func TestOwnRepository(t *testing.T) {
	needGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	r := Repo{Dir: dir}

	must(t)(r.Init(ctx))
	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !strings.Contains(string(ignore), "*.bak") {
		t.Fatalf(".gitignore = %q, %v", ignore, err)
	}

	write(t, filepath.Join(dir, "textures", "brick wall.png"), "png")
	write(t, filepath.Join(dir, "textures", "brick wall.png.bak"), "old")
	write(t, filepath.Join(dir, "game.yaml"), "title: x\n")

	s := status(t, r)
	if !s.IsRepo || s.Prefix != "" || s.Head != "" {
		t.Fatalf("fresh repository: %+v", s)
	}
	if _, ok := file(s, "textures/brick wall.png.bak"); ok {
		t.Fatal("a .bak was not ignored")
	}
	f, ok := file(s, "textures/brick wall.png")
	if !ok || !f.Untracked() || f.Staged() || f.Label() != "new" {
		t.Fatalf("untracked file: %+v, %v", f, ok)
	}
	if d, err := r.Diff(ctx, f, false); err != nil || !strings.Contains(d, "+png") {
		t.Fatalf("untracked diff = %q, %v", d, err)
	}

	// Staged and unstaged again before there is a commit to reset to.
	must(t)(r.Stage(ctx, f.Path))
	if f, _ = file(status(t, r), f.Path); !f.Staged() {
		t.Fatalf("after Stage: %+v", f)
	}
	must(t)(r.Unstage(ctx, f.Path))
	if f, _ = file(status(t, r), f.Path); !f.Untracked() {
		t.Fatalf("after Unstage with no commits: %+v", f)
	}

	if _, err := r.Commit(ctx, "  "); err == nil {
		t.Fatal("an empty message was committed")
	}
	must(t)(r.Stage(ctx))
	must(t)(r.SetIdentity(ctx, "Someone", "someone@example.com"))
	must(t)(r.Commit(ctx, "First textures"))
	s = status(t, r)
	if s.Head == "" || len(s.Files) != 0 || s.UserName != "Someone" {
		t.Fatalf("after commit: %+v", s)
	}
	log, err := r.Log(ctx, 10)
	if err != nil || len(log) != 1 || log[0].Subject != "First textures" || log[0].Author != "Test" {
		t.Fatalf("log = %+v, %v", log, err)
	}
	if out, err := r.Show(ctx, log[0].Hash); err != nil || !strings.Contains(out, "First textures") {
		t.Fatalf("show = %q, %v", out, err)
	}

	// A change, looked at and then thrown away.
	write(t, filepath.Join(dir, "game.yaml"), "title: y\n")
	f, _ = file(status(t, r), "game.yaml")
	if !f.Unstaged() || f.Staged() || f.Label() != "modified" {
		t.Fatalf("modified file: %+v", f)
	}
	if d, _ := r.Diff(ctx, f, false); !strings.Contains(d, "+title: y") {
		t.Fatalf("diff = %q", d)
	}
	must(t)(r.Discard(ctx, f))
	if b, _ := os.ReadFile(filepath.Join(dir, "game.yaml")); string(b) != "title: x\n" {
		t.Fatalf("after Discard: %q", b)
	}

	// Discarding a file the last commit does not have deletes it, staged or not.
	write(t, filepath.Join(dir, "a.txt"), "a")
	write(t, filepath.Join(dir, "b.txt"), "b")
	must(t)(r.Stage(ctx, "b.txt"))
	s = status(t, r)
	for _, p := range []string{"a.txt", "b.txt"} {
		f, _ := file(s, p)
		must(t)(r.Discard(ctx, f))
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Fatalf("%s survived Discard", p)
		}
	}

	// Branches.
	if _, err := r.CreateBranch(ctx, "bad name"); err == nil {
		t.Fatal("an invalid branch name was accepted")
	}
	must(t)(r.CreateBranch(ctx, "art"))
	s = status(t, r)
	if s.Branch != "art" || len(s.Branches) != 2 {
		t.Fatalf("after CreateBranch: %+v", s)
	}
	other := s.Branches[0]
	if other == "art" {
		other = s.Branches[1]
	}
	must(t)(r.Switch(ctx, other))
	if s = status(t, r); s.Branch != other {
		t.Fatalf("after Switch: %+v", s)
	}
}

// resources/ inside a bigger repository sees, stages and commits only its own
// files, and knows when something outside it is staged.
func TestFolderInsideARepository(t *testing.T) {
	needGit(t)
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "resources")
	must(t)(Repo{Dir: root}.Init(ctx))
	write(t, filepath.Join(root, "main.go"), "package main\n")
	write(t, filepath.Join(dir, "scripts", "door.flang"), "open\n")
	r := Repo{Dir: dir}

	s := status(t, r)
	if s.Prefix != "resources/" {
		t.Fatalf("prefix = %q", s.Prefix)
	}
	if len(s.Files) != 1 || s.Files[0].Path != "scripts/door.flang" {
		t.Fatalf("files = %+v", s.Files)
	}

	must(t)(r.Stage(ctx))
	s = status(t, r)
	if s.StagedOutside != 0 || s.Staged() != 1 {
		t.Fatalf("Stage reached outside the folder: %+v", s)
	}
	if _, err := (Repo{Dir: root}).Stage(ctx, "main.go"); err != nil {
		t.Fatal(err)
	}
	if s = status(t, r); s.StagedOutside != 1 {
		t.Fatalf("staged outside = %d", s.StagedOutside)
	}

	// A rename inside the folder is one entry, with its source.
	must(t)((Repo{Dir: root}).Unstage(ctx, "main.go"))
	must(t)(r.Commit(ctx, "door"))
	cmd := exec.Command("git", "mv", "scripts/door.flang", "scripts/gate.flang")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git mv: %v %s", err, out)
	}
	f, ok := file(status(t, r), "scripts/gate.flang")
	if !ok || f.OrigPath != "scripts/door.flang" || f.Label() != "renamed" {
		t.Fatalf("rename: %+v, %v", f, ok)
	}
	must(t)(r.Discard(ctx, f))
	if _, err := os.Stat(filepath.Join(dir, "scripts", "door.flang")); err != nil {
		t.Fatalf("Discard did not put the rename back: %v", err)
	}
}

// Push and pull against a bare repository on disk, with an upstream made by the
// first push.
func TestPushPull(t *testing.T) {
	needGit(t)
	ctx := context.Background()
	remote := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "--bare", remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}

	a := Repo{Dir: t.TempDir()}
	must(t)(a.Init(ctx))
	must(t)(a.AddRemote(ctx, "origin", remote))
	must(t)(a.Stage(ctx))
	must(t)(a.Commit(ctx, "one"))
	if _, err := a.Push(ctx, status(t, a)); err != nil {
		t.Fatal(err)
	}
	s := status(t, a)
	if s.Upstream == "" || s.Ahead != 0 {
		t.Fatalf("after first push: %+v", s)
	}

	bdir := t.TempDir()
	cmd = exec.Command("git", "clone", "-q", remote, bdir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	b := Repo{Dir: bdir}

	write(t, filepath.Join(a.Dir, "level.yaml"), "chunks: []\n")
	must(t)(a.Stage(ctx))
	must(t)(a.Commit(ctx, "two"))
	if s = status(t, a); s.Ahead != 1 {
		t.Fatalf("ahead = %d", s.Ahead)
	}
	must(t)(a.Push(ctx, s))

	must(t)(b.Fetch(ctx))
	if s = status(t, b); s.Behind != 1 {
		t.Fatalf("behind = %d", s.Behind)
	}
	must(t)(b.Pull(ctx))
	if _, err := os.Stat(filepath.Join(bdir, "level.yaml")); err != nil {
		t.Fatalf("pull did not bring the file: %v", err)
	}
}

func TestPushRemote(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"upstream"}, "upstream"},
		{[]string{"backup", "origin"}, "origin"},
	} {
		if got := PushRemote(c.in); got != c.want {
			t.Errorf("PushRemote(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Status parsing of each porcelain record, paths with spaces included.
func TestParseStatus(t *testing.T) {
	out := strings.Join([]string{
		"# branch.oid 0123abc",
		"# branch.head main",
		"# branch.upstream origin/main",
		"# branch.ab +2 -3",
		"1 M. N... 100644 100644 100644 aaa bbb resources/a b.txt",
		"1 .D N... 100644 100644 000000 aaa aaa resources/gone.txt",
		"2 R. N... 100644 100644 100644 aaa bbb R100 resources/new.txt",
		"resources/old.txt",
		"u UU N... 100644 100644 100644 100644 aaa bbb ccc resources/both.txt",
		"? resources/fresh.txt",
		"? engine.go",
		"",
	}, "\x00")
	var s Status
	parseStatus(out, "resources/", &s)
	if s.Head != "0123abc" || s.Branch != "main" || s.Upstream != "origin/main" || s.Ahead != 2 || s.Behind != 3 {
		t.Fatalf("branch: %+v", s)
	}
	want := []FileStatus{
		{Path: "a b.txt", Index: 'M', Work: ' '},
		{Path: "gone.txt", Index: ' ', Work: 'D'},
		{Path: "new.txt", OrigPath: "old.txt", Index: 'R', Work: ' '},
		{Path: "both.txt", Index: 'U', Work: 'U', Conflict: true},
		{Path: "fresh.txt", Index: '?', Work: '?'},
	}
	if len(s.Files) != len(want) {
		t.Fatalf("files = %+v", s.Files)
	}
	for i := range want {
		if s.Files[i] != want[i] {
			t.Errorf("file %d = %+v, want %+v", i, s.Files[i], want[i])
		}
	}
	labels := []string{"modified", "deleted", "renamed", "conflict", "new"}
	for i, l := range labels {
		if got := s.Files[i].Label(); got != l {
			t.Errorf("%s label = %q, want %q", s.Files[i].Path, got, l)
		}
	}
}
