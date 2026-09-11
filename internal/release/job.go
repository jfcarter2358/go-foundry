package release

// job.go — a release build the editor is waiting on.
//
// The build runs in a child process — this same program, run as `build-release`
// — rather than on a goroutine inside the editor, for three reasons, any one of
// which would be enough:
//
//   - Compiling a level loads it through the engine's own loader, which fills
//     the entity, prefab and texture registries the editor is drawing from. On
//     a goroutine those would be rewritten under the editor's feet.
//   - A build that crashes — a compile that panics on a malformed brush —
//     should cost the build, not the editor and everything unsaved in it.
//   - Cancel has to stop a `go build` two processes down. Killing a process
//     tree is something an operating system does; stopping a goroutine is not.

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// JobState is where a job has got to.
type JobState int

const (
	JobRunning JobState = iota
	JobSucceeded
	JobFailed
	JobCancelled
)

// maxJobLines bounds how much output a job keeps. A verbose build names every
// file it packs, and the window only ever shows the end of it.
const maxJobLines = 20000

// Job is a release build running in a child process.
type Job struct {
	cmd  *exec.Cmd
	done chan struct{}

	mu        sync.Mutex
	lines     []string
	artifacts []string
	state     JobState
	err       error
	cancelled bool
	started   time.Time
	finished  time.Time
}

// JobSnapshot is a job as of one moment. The window draws from one of these, so
// it never holds the job's lock across a frame.
type JobSnapshot struct {
	Lines     []string
	Artifacts []string
	State     JobState
	Err       error
	Elapsed   time.Duration
}

// StartJob starts a release build of the project at root.
func StartJob(root string, c Config) (*Job, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return startProcess(exe, root, Args(root, c), nil)
}

// startProcess runs a program as a job. env is added to this process's own.
func startProcess(name, dir string, args, env []string) (*Job, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	// Both streams into the one pipe, so the output reads in the order it
	// happened.
	cmd.Stdout, cmd.Stderr = pw, pw
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	isolateProcessTree(cmd)
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	// The child has its own copy of the write end. Ours, left open, would keep
	// the reader below from ever reaching the end of the output.
	pw.Close()

	j := &Job{cmd: cmd, done: make(chan struct{}), started: time.Now()}
	read := make(chan struct{})
	go func() {
		defer close(read)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			j.addLine(sc.Text())
		}
	}()
	go func() {
		err := cmd.Wait()
		// A grandchild that outlived the build — a go build whose parent was
		// killed — can hold the pipe open after the build itself has gone.
		// Whatever it has left to say is not worth waiting on for ever.
		select {
		case <-read:
		case <-time.After(2 * time.Second):
		}
		pr.Close()
		j.finish(err)
	}()
	return j, nil
}

func (j *Job) addLine(line string) {
	line = strings.TrimRight(line, "\r")
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.lines) >= maxJobLines {
		// A fresh slice rather than a shift in place: a snapshot taken a frame
		// ago is still reading the old one.
		j.lines = append([]string(nil), j.lines[len(j.lines)-maxJobLines/2:]...)
	}
	j.lines = append(j.lines, line)
	if path, ok := strings.CutPrefix(line, ArtifactPrefix); ok {
		j.artifacts = append(j.artifacts, path)
	}
}

func (j *Job) finish(err error) {
	j.mu.Lock()
	j.finished = time.Now()
	switch {
	case j.cancelled:
		j.state = JobCancelled
	case err != nil:
		j.state, j.err = JobFailed, err
	default:
		j.state = JobSucceeded
	}
	j.mu.Unlock()
	close(j.done)
}

// Snapshot is the job as it stands.
func (j *Job) Snapshot() JobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := JobSnapshot{
		// Capped at their length, so nothing appended to a snapshot can land in
		// the job's own arrays.
		Lines:     j.lines[:len(j.lines):len(j.lines)],
		Artifacts: j.artifacts[:len(j.artifacts):len(j.artifacts)],
		State:     j.state,
		Err:       j.err,
	}
	if j.state == JobRunning {
		s.Elapsed = time.Since(j.started)
	} else {
		s.Elapsed = j.finished.Sub(j.started)
	}
	return s
}

// Running reports whether the build is still going.
func (j *Job) Running() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state == JobRunning
}

// Cancel stops the build and everything it started. Safe to call on a job that
// has already finished.
func (j *Job) Cancel() {
	j.mu.Lock()
	if j.state != JobRunning || j.cancelled {
		j.mu.Unlock()
		return
	}
	j.cancelled = true
	j.mu.Unlock()
	killProcessTree(j.cmd)
}

// Wait blocks until the build has finished.
func (j *Job) Wait() { <-j.done }
