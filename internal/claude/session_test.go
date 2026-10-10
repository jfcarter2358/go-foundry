package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildFake compiles the stand-in CLI once per run. A test that cannot build it
// skips rather than fails: the toolchain is there on a developer's machine and
// in CI, but this package's other tests are worth running without it.
var (
	fakeOnce sync.Once
	fakePath string
	fakeErr  error
)

func fakeCLI(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		if _, err := exec.LookPath("go"); err != nil {
			fakeErr = err
			return
		}
		dir, err := os.MkdirTemp("", "fakeclaude")
		if err != nil {
			fakeErr = err
			return
		}
		out := filepath.Join(dir, "fakeclaude")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, "./testdata/fakeclaude")
		if b, err := cmd.CombinedOutput(); err != nil {
			fakeErr = err
			t.Logf("building the fake CLI: %s", b)
			return
		}
		fakePath = out
	})
	if fakeErr != nil {
		t.Skipf("cannot build the fake CLI: %v", fakeErr)
	}
	return fakePath
}

// waitFor polls until cond holds. The session is driven by goroutines reading a
// pipe, so every assertion about it is eventually-true.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestArgsCarryWhatTheProtocolNeeds(t *testing.T) {
	got := strings.Join(Args(Options{Model: "opus", PermissionMode: "acceptEdits", Resume: "abc",
		MCPConfig: "/tmp/mcp.json", StrictMCP: true, appendSystemFile: "/tmp/primer.md"}), " ")
	for _, want := range []string{
		"-p", "--input-format stream-json", "--output-format stream-json", "--verbose",
		"--permission-prompt-tool stdio", "--model opus", "--permission-mode acceptEdits",
		"--resume abc", "--mcp-config /tmp/mcp.json", "--strict-mcp-config",
		// The primer goes as a path, never as the text: see Options.AppendSystem.
		"--append-system-prompt-file /tmp/primer.md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q are missing %q", got, want)
		}
	}
	// An empty option must not turn into an empty flag value, which the CLI
	// rejects outright.
	if bare := strings.Join(Args(Options{}), " "); strings.Contains(bare, "--model") || strings.Contains(bare, "--resume") {
		t.Errorf("args with no options set = %q, want no empty flags", bare)
	}
}

func TestSessionRunsATurnAndAnswersAPermission(t *testing.T) {
	s, err := Start(Options{Binary: fakeCLI(t), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	waitFor(t, "the init message", func() bool { return s.Snapshot().SessionID == "fake-session" })
	if got := s.Snapshot().Version; got != "2.1.278" {
		t.Errorf("version = %q, want the one the CLI reported", got)
	}

	s.Send("write the door script")
	waitFor(t, "the permission request", func() bool { return s.Snapshot().Status == StatusWaiting })

	snap := s.Snapshot()
	if len(snap.Pending) != 1 {
		t.Fatalf("pending = %+v", snap.Pending)
	}
	// The user's own turn, the streamed text, the tool call and the request.
	if !hasEntry(snap, EntryUser, "write the door script") {
		t.Error("the user's message is not in the transcript")
	}
	if !hasKindIn(snap, EntryTool) {
		t.Error("the tool call is not in the transcript")
	}

	s.Answer(snap.Pending[0].RequestID, DecisionAllow, "")
	waitFor(t, "the turn to finish", func() bool { return s.Snapshot().Status == StatusIdle })

	snap = s.Snapshot()
	if snap.Cost != 0.25 {
		t.Errorf("cost = %v, want the value from the result message", snap.Cost)
	}
	if snap.Tokens != 100 {
		t.Errorf("tokens = %d, want input plus cache reads", snap.Tokens)
	}
	if !hasEntry(snap, EntryAssistant, "Done.") {
		t.Error("the final assistant text is missing")
	}
	paths, _ := s.DrainTouched()
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "door.flang") {
		t.Errorf("touched = %v, want the script the tool wrote", paths)
	}
}

func TestDenyingAPermissionLetsTheTurnFinish(t *testing.T) {
	s, err := Start(Options{Binary: fakeCLI(t), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waitFor(t, "init", func() bool { return s.Snapshot().SessionID != "" })
	s.Send("write the door script")
	waitFor(t, "the permission request", func() bool { return s.Snapshot().Status == StatusWaiting })
	s.Answer(s.Snapshot().Pending[0].RequestID, DecisionDeny, "not this one")
	waitFor(t, "the turn to finish", func() bool { return s.Snapshot().Status == StatusIdle })
	if paths, _ := s.DrainTouched(); len(paths) != 0 {
		t.Errorf("touched = %v, want nothing after a denial", paths)
	}
}

func TestAutoAllowSkipsThePrompt(t *testing.T) {
	s, err := Start(Options{
		Binary:    fakeCLI(t),
		Dir:       t.TempDir(),
		AutoAllow: func(string) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waitFor(t, "init", func() bool { return s.Snapshot().SessionID != "" })
	s.Send("write the door script")
	// It should run through without ever waiting on the user.
	waitFor(t, "the turn to finish", func() bool { return s.Snapshot().Status == StatusIdle })
	if n := len(s.Snapshot().Pending); n != 0 {
		t.Errorf("%d permissions were shown, want none", n)
	}
}

func TestInterruptEndsATurnInFlight(t *testing.T) {
	s, err := Start(Options{Binary: fakeCLI(t), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waitFor(t, "init", func() bool { return s.Snapshot().SessionID != "" })
	s.Send("count slowly to 400")
	waitFor(t, "the turn to start streaming", func() bool {
		for _, e := range s.Snapshot().Entries {
			if e.Live {
				return true
			}
		}
		return false
	})
	s.Interrupt()
	waitFor(t, "the turn to stop", func() bool { return s.Snapshot().Status == StatusIdle })
	if !hasEntry(s.Snapshot(), EntryNotice, "Stopped.") {
		t.Error("no sign in the transcript that the turn was interrupted")
	}
}

func TestCloseStopsTheProcess(t *testing.T) {
	s, err := Start(Options{Binary: fakeCLI(t), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "init", func() bool { return s.Snapshot().SessionID != "" })
	s.Close()
	waitFor(t, "the process to exit", func() bool { return !s.Running() })
	// Twice is safe: the editor closes the window and then shuts down.
	s.Close()
}

func TestStartRejectsAMissingBinary(t *testing.T) {
	if _, err := Start(Options{Binary: filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Error("starting with a binary that does not exist should fail")
	}
	if _, err := Start(Options{}); err == nil {
		t.Error("starting with no binary should fail")
	}
}

func hasEntry(s Snapshot, k EntryKind, text string) bool {
	for _, e := range s.Entries {
		if e.Kind == k && strings.Contains(e.Text, text) {
			return true
		}
	}
	return false
}

func hasKindIn(s Snapshot, k EntryKind) bool {
	for _, e := range s.Entries {
		if e.Kind == k {
			return true
		}
	}
	return false
}

func TestThePrimerIsPassedAsAFileAndCleanedUp(t *testing.T) {
	s, err := Start(Options{Binary: fakeCLI(t), Dir: t.TempDir(), AppendSystem: "# Foundry\nmulti\nline\nprimer"})
	if err != nil {
		t.Fatal(err)
	}
	path := s.opts.appendSystemFile
	if path == "" {
		t.Fatal("no file was written for the system prompt")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the primer file was not written: %v", err)
	}
	if string(got) != "# Foundry\nmulti\nline\nprimer" {
		t.Errorf("primer file = %q", got)
	}
	s.Close()
	waitFor(t, "the process to exit", func() bool { return !s.Running() })
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the primer file was left behind at %s", path)
	}
}

func TestAProcessThatDiesSaysWhy(t *testing.T) {
	// A CLI that refuses a flag, or is not logged in, writes to stderr and
	// exits. That message is the only thing that explains the panel doing
	// nothing, so it has to reach the transcript.
	bin := fakeCLI(t)
	s, err := Start(Options{Binary: bin, Dir: t.TempDir(), Env: []string{"FAKECLAUDE_FAIL=1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waitFor(t, "the process to exit", func() bool { return !s.Running() })
	waitFor(t, "the reason to reach the transcript", func() bool {
		for _, e := range s.Snapshot().Entries {
			if e.Kind == EntryNotice && strings.Contains(e.Text, "not logged in") {
				return true
			}
		}
		return false
	})
}
