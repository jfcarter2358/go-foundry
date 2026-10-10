// Package vcs drives git for one directory of a project — the editor's
// resources/ — by running the git program.
//
// The directory need not be a repository's root. resources/ is often its own
// repository, and just as often a folder inside a bigger one (the engine's own
// checkout, a game's project repository). Everything here that reads or writes
// files is scoped to the directory, so a folder inside a bigger repository shows
// and commits only its own changes. What cannot be scoped — the branch, pulling
// and pushing — is the whole repository's, and Status says which repository
// that is.
//
// Every call runs git as a child process and waits for it. None of it belongs
// on a frame: the editor calls it from a goroutine.
package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultIgnore is the .gitignore Init writes when the directory has none.
// fileutil.WriteFileAtomic leaves the previous version of every file it saves
// beside it as <name>.bak, and those are never worth committing.
const DefaultIgnore = "# Written by the Foundry editor.\n# Every save leaves the previous version of the file beside it.\n*.bak\n"

// waitDelay is how long a cancelled git gets to let go of its output before its
// pipes are closed under it. A push over ssh leaves ssh holding them after git
// itself is killed.
const waitDelay = 2 * time.Second

// Available reports why git cannot be run, or nil when it can.
func Available() error {
	_, err := exec.LookPath("git")
	return err
}

// Repo is the directory being managed.
type Repo struct {
	Dir string
}

// Error is git exiting with a failure, carrying what it printed.
type Error struct {
	Args   []string
	Output string
	Err    error
}

func (e *Error) Error() string {
	if out := strings.TrimSpace(e.Output); out != "" {
		return out
	}
	return fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// run runs git in the directory and returns its standard output. A failure's
// Error carries standard error, or standard output when git put its complaint
// there (commit does, for "nothing to commit").
func (r Repo) run(ctx context.Context, stdin string, args ...string) (string, error) {
	// --literal-pathspecs: a file named with a * or a ? is that file, not a
	// pattern. quotepath: names come back as they are, not octal-escaped.
	full := append([]string{"--literal-pathspecs", "-c", "core.quotepath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.Dir
	// Nothing can answer a prompt: git would sit waiting on the terminal the
	// editor was started from, or on nothing at all. A credential git cannot
	// get without asking is a failure the window reports.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	cmd.WaitDelay = waitDelay
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return stdout.String(), ctx.Err()
		}
		msg := stderr.String()
		if strings.TrimSpace(msg) == "" {
			msg = stdout.String()
		}
		return stdout.String(), &Error{Args: args, Output: msg, Err: err}
	}
	// Some commands (push, pull, fetch) report on standard error even when they
	// work, and what they said is worth showing.
	if stderr.Len() > 0 {
		return strings.TrimRight(stdout.String()+stderr.String(), "\n"), nil
	}
	return stdout.String(), nil
}

// FileStatus is one changed path under the directory.
type FileStatus struct {
	// Path is relative to the directory, slash-separated. It is what every
	// call here takes.
	Path string
	// OrigPath is a rename's or a copy's source, relative to the directory, or
	// "" when there is none or it is outside it.
	OrigPath string
	// Index and Work are git's two status letters: what is staged against the
	// last commit, and what the working tree has that is not staged. ' ' is no
	// change, '?' on both is untracked.
	Index, Work byte
	// Conflict is a file a merge left unresolved.
	Conflict bool
}

// Untracked reports a file git has never been told about.
func (f FileStatus) Untracked() bool { return f.Index == '?' }

// Conflicted reports a file a merge left unresolved.
func (f FileStatus) Conflicted() bool { return f.Conflict }

// Staged reports a file with changes the next commit takes.
func (f FileStatus) Staged() bool {
	return !f.Untracked() && !f.Conflicted() && f.Index != ' ' && f.Index != '.'
}

// Unstaged reports a file with changes the next commit does not take.
func (f FileStatus) Unstaged() bool {
	return f.Untracked() || f.Conflicted() || (f.Work != ' ' && f.Work != '.')
}

// Label is a word for what happened to the file, for a status column.
func (f FileStatus) Label() string {
	if f.Untracked() {
		return "new"
	}
	if f.Conflicted() {
		return "conflict"
	}
	code := f.Work
	if f.Staged() {
		code = f.Index
	}
	switch code {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type"
	}
	return "modified"
}

// Status is the repository as the window shows it.
type Status struct {
	// IsRepo is false when the directory is in no repository at all; nothing
	// else is filled in then.
	IsRepo bool
	// Root is the repository's top directory, and Prefix the directory's path
	// inside it with a trailing slash ("" when the directory is the root).
	Root, Prefix string

	// Branch is the branch checked out, "" when the head is detached.
	Branch string
	// Head is the commit checked out, "" before the first commit.
	Head string
	// Upstream is the branch's remote-tracking branch, "" when it has none.
	// Ahead and Behind count the commits each side has that the other lacks.
	Upstream      string
	Ahead, Behind int

	// Files is every changed path under the directory.
	Files []FileStatus
	// StagedOutside counts staged paths outside the directory. A commit would
	// take them with it, so the editor will not make one while there are any.
	StagedOutside int

	Branches []string
	Remotes  []string

	// UserName and UserEmail are the identity a commit is signed with. Git
	// refuses to commit without them.
	UserName, UserEmail string
}

// Staged counts the files the next commit takes.
func (s Status) Staged() int {
	n := 0
	for _, f := range s.Files {
		if f.Staged() {
			n++
		}
	}
	return n
}

// Status reads the repository. A directory in no repository is not an error.
func (r Repo) Status(ctx context.Context) (Status, error) {
	var s Status
	out, err := r.run(ctx, "", "rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && strings.Contains(strings.ToLower(ge.Output), "not a git repository") {
			return s, nil
		}
		return s, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	s.IsRepo = true
	s.Root = filepath.FromSlash(lines[0])
	if len(lines) > 1 {
		s.Prefix = lines[1]
	}

	// --no-optional-locks: status otherwise takes the index lock to refresh it,
	// and the window reads status while a stage or a commit may be running.
	out, err = r.run(ctx, "", "--no-optional-locks", "status", "--porcelain=v2", "--branch", "-z",
		"--untracked-files=all", "--", ".")
	if err != nil {
		return s, err
	}
	parseStatus(out, s.Prefix, &s)

	if s.Prefix != "" {
		out, err = r.run(ctx, "", "--no-optional-locks", "diff", "--cached", "--name-only", "-z")
		if err != nil && s.Head != "" {
			return s, err
		}
		for _, p := range splitNUL(out) {
			if !strings.HasPrefix(p, s.Prefix) {
				s.StagedOutside++
			}
		}
	}

	out, err = r.run(ctx, "", "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return s, err
	}
	s.Branches = splitLines(out)
	out, err = r.run(ctx, "", "remote")
	if err != nil {
		return s, err
	}
	s.Remotes = splitLines(out)
	// Exit status 1 is the key not being set, which is an answer, not a failure.
	out, _ = r.run(ctx, "", "config", "user.name")
	s.UserName = strings.TrimSpace(out)
	out, _ = r.run(ctx, "", "config", "user.email")
	s.UserEmail = strings.TrimSpace(out)
	return s, nil
}

// parseStatus reads `git status --porcelain=v2 --branch -z` into s. Paths are
// the repository's; prefix is taken off them, and a path outside it is left out.
func parseStatus(out, prefix string, s *Status) {
	recs := splitNUL(out)
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		switch {
		case strings.HasPrefix(rec, "# branch.oid "):
			if oid := strings.TrimPrefix(rec, "# branch.oid "); oid != "(initial)" {
				s.Head = oid
			}
		case strings.HasPrefix(rec, "# branch.head "):
			if b := strings.TrimPrefix(rec, "# branch.head "); b != "(detached)" {
				s.Branch = b
			}
		case strings.HasPrefix(rec, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(rec, "# branch.upstream ")
		case strings.HasPrefix(rec, "# branch.ab "):
			for _, f := range strings.Fields(strings.TrimPrefix(rec, "# branch.ab ")) {
				n, _ := strconv.Atoi(f[1:])
				if f[0] == '+' {
					s.Ahead = n
				} else {
					s.Behind = n
				}
			}
		case strings.HasPrefix(rec, "1 "), strings.HasPrefix(rec, "u "):
			// "1 XY sub mH mI mW hH hI path" and "u XY sub m1 m2 m3 mW h1 h2 h3
			// path": the path is whatever follows the fixed fields, and may
			// itself hold spaces.
			fields := 9
			if rec[0] == 'u' {
				fields = 11
			}
			parts := strings.SplitN(rec, " ", fields)
			if len(parts) == fields {
				addStatus(s, prefix, parts[1], parts[fields-1], "", rec[0] == 'u')
			}
		case strings.HasPrefix(rec, "2 "):
			// "2 XY sub mH mI mW hH hI Xscore path", then the source as a
			// record of its own.
			parts := strings.SplitN(rec, " ", 10)
			orig := ""
			if i+1 < len(recs) {
				orig = recs[i+1]
				i++
			}
			if len(parts) == 10 {
				addStatus(s, prefix, parts[1], parts[9], orig, false)
			}
		case strings.HasPrefix(rec, "? "):
			addStatus(s, prefix, "??", rec[2:], "", false)
		}
	}
}

func addStatus(s *Status, prefix, xy, path, orig string, conflict bool) {
	if len(xy) != 2 || !strings.HasPrefix(path, prefix) {
		return
	}
	f := FileStatus{Path: strings.TrimPrefix(path, prefix), Index: dotToSpace(xy[0]), Work: dotToSpace(xy[1]), Conflict: conflict}
	if orig != "" && strings.HasPrefix(orig, prefix) {
		f.OrigPath = strings.TrimPrefix(orig, prefix)
	}
	s.Files = append(s.Files, f)
}

// dotToSpace turns porcelain v2's "unchanged" into porcelain v1's.
func dotToSpace(c byte) byte {
	if c == '.' {
		return ' '
	}
	return c
}

func splitNUL(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Commit is one entry of the history.
type Commit struct {
	Hash, Short, Author, Subject string
	When                         time.Time
}

// Log is the last n commits that touched the directory, newest first. A
// repository with no commits yet has an empty history, not an error.
func (r Repo) Log(ctx context.Context, n int) ([]Commit, error) {
	out, err := r.run(ctx, "", "log", "-n", strconv.Itoa(n), "-z",
		"--format=%H%x1f%h%x1f%an%x1f%at%x1f%s", "--", ".")
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && strings.Contains(ge.Output, "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	var commits []Commit
	for _, rec := range splitNUL(out) {
		f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x1f", 5)
		if len(f) != 5 {
			continue
		}
		secs, _ := strconv.ParseInt(f[3], 10, 64)
		commits = append(commits, Commit{Hash: f[0], Short: f[1], Author: f[2], When: time.Unix(secs, 0), Subject: f[4]})
	}
	return commits, nil
}

// Diff is what changed in a file: staged against the last commit when staged
// is set, otherwise the working tree against what is staged. An untracked file
// is shown whole, as an addition.
func (r Repo) Diff(ctx context.Context, f FileStatus, staged bool) (string, error) {
	if f.Untracked() {
		out, err := r.run(ctx, "", "diff", "--no-index", "--", os.DevNull, f.Path)
		// --no-index exits 1 when the files differ, which they always do here.
		var ge *Error
		var ee *exec.ExitError
		if errors.As(err, &ge) && errors.As(ge.Err, &ee) && ee.ExitCode() == 1 {
			return out, nil
		}
		return out, err
	}
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", f.Path)
	if f.OrigPath != "" {
		args = append(args, f.OrigPath)
	}
	return r.run(ctx, "", args...)
}

// Show is one commit: its message and the part of its change under the
// directory.
func (r Repo) Show(ctx context.Context, hash string) (string, error) {
	return r.run(ctx, "", "show", "--stat", "--patch", "--format=commit %H%nAuthor: %an <%ae>%nDate:   %ad%n%n%w(0,4,4)%B", hash, "--", ".")
}

// Init makes the directory a repository of its own, and gives it DefaultIgnore
// when it has no .gitignore.
func (r Repo) Init(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "", "init")
	if err != nil {
		return out, err
	}
	ignore := filepath.Join(r.Dir, ".gitignore")
	if _, serr := os.Stat(ignore); os.IsNotExist(serr) {
		if werr := os.WriteFile(ignore, []byte(DefaultIgnore), 0644); werr != nil {
			return out, werr
		}
		out += "wrote .gitignore\n"
	}
	return out, nil
}

// Stage adds paths, deletions included, to what the next commit takes. No paths
// is everything under the directory.
func (r Repo) Stage(ctx context.Context, paths ...string) (string, error) {
	return r.run(ctx, "", append([]string{"add", "-A", "--"}, orAll(paths)...)...)
}

// Unstage takes paths back out of the next commit, leaving the files as they
// are. No paths is everything under the directory.
func (r Repo) Unstage(ctx context.Context, paths ...string) (string, error) {
	// reset rather than restore --staged: it also works before the first
	// commit, when there is no HEAD to restore from.
	out, err := r.run(ctx, "", append([]string{"reset", "-q", "--"}, orAll(paths)...)...)
	var ge *Error
	if errors.As(err, &ge) && strings.Contains(ge.Output, "ambiguous argument 'HEAD'") {
		return r.run(ctx, "", append([]string{"rm", "-r", "-q", "--cached", "--"}, orAll(paths)...)...)
	}
	return out, err
}

func orAll(paths []string) []string {
	if len(paths) == 0 {
		return []string{"."}
	}
	return paths
}

// Discard throws away every change to the file, staged or not, putting back
// what the last commit has — or, for a file the last commit does not have,
// deleting it.
func (r Repo) Discard(ctx context.Context, f FileStatus) (string, error) {
	switch {
	case f.Untracked():
		return r.run(ctx, "", "clean", "-f", "-q", "--", f.Path)
	case f.Index == 'A', f.Index == 'C':
		return r.forget(ctx, f.Path)
	case f.Index == 'R':
		if f.OrigPath != "" {
			if out, err := r.run(ctx, "", "checkout", "HEAD", "--", f.OrigPath); err != nil {
				return out, err
			}
		}
		return r.forget(ctx, f.Path)
	}
	return r.run(ctx, "", "checkout", "HEAD", "--", f.Path)
}

// forget takes a staged new file out of the index and deletes it. Not `git rm
// -f`, which also deletes every folder the file leaves empty — up to and
// including the directory itself, when it was the only file in it.
func (r Repo) forget(ctx context.Context, path string) (string, error) {
	out, err := r.run(ctx, "", "rm", "--cached", "-q", "--", path)
	if err != nil {
		return out, err
	}
	if err := os.Remove(filepath.Join(r.Dir, filepath.FromSlash(path))); err != nil && !os.IsNotExist(err) {
		return out, err
	}
	return out, nil
}

// Commit records what is staged.
func (r Repo) Commit(ctx context.Context, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("the commit needs a message")
	}
	return r.run(ctx, message, "commit", "-F", "-")
}

// SetIdentity sets the name and email this repository's commits are signed
// with. Written to the repository's own config, not the user's global one.
func (r Repo) SetIdentity(ctx context.Context, name, email string) (string, error) {
	if _, err := r.run(ctx, "", "config", "user.name", name); err != nil {
		return "", err
	}
	return r.run(ctx, "", "config", "user.email", email)
}

// AddRemote names a repository to push to and pull from.
func (r Repo) AddRemote(ctx context.Context, name, url string) (string, error) {
	return r.run(ctx, "", "remote", "add", "--", name, url)
}

// Fetch brings the remotes' branches up to date without touching the files.
func (r Repo) Fetch(ctx context.Context) (string, error) {
	return r.run(ctx, "", "fetch", "--all", "--prune")
}

// Pull brings in the upstream's commits. Fast-forward only: a merge of two
// people's textures is not something to resolve in a status window, so a
// branch that has diverged is reported rather than merged.
func (r Repo) Pull(ctx context.Context) (string, error) {
	return r.run(ctx, "", "pull", "--ff-only")
}

// Push sends the branch's commits to its upstream, making remote's branch of
// the same name the upstream when there is none yet.
func (r Repo) Push(ctx context.Context, s Status) (string, error) {
	if s.Upstream != "" {
		return r.run(ctx, "", "push")
	}
	remote := PushRemote(s.Remotes)
	if remote == "" {
		return "", errors.New("there is no remote to push to")
	}
	if s.Branch == "" {
		return "", errors.New("no branch is checked out")
	}
	return r.run(ctx, "", "push", "-u", remote, "HEAD")
}

// PushRemote is the remote a branch with no upstream is pushed to: origin when
// there is one, otherwise the first.
func PushRemote(remotes []string) string {
	for _, r := range remotes {
		if r == "origin" {
			return r
		}
	}
	if len(remotes) > 0 {
		return remotes[0]
	}
	return ""
}

// Switch checks out another branch.
func (r Repo) Switch(ctx context.Context, branch string) (string, error) {
	return r.run(ctx, "", "switch", "--", branch)
}

// CreateBranch starts a new branch where the head is and checks it out.
func (r Repo) CreateBranch(ctx context.Context, name string) (string, error) {
	if _, err := r.run(ctx, "", "check-ref-format", "--branch", name); err != nil {
		return "", fmt.Errorf("%q is not a valid branch name", name)
	}
	return r.run(ctx, "", "switch", "-c", name)
}
