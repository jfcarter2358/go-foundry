package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEndToEndAgainstTheRealCLI drives an actual `claude` process through the
// whole stack: the handshake, a permission answered from here, a tool served by
// this package's MCP server, and a file appearing on disk.
//
// It is skipped unless FOUNDRY_CLAUDE_E2E=1 because it costs money and needs a
// logged-in CLI. Run it after changing anything in this package, and after a CLI
// upgrade — it is the only test that would notice the protocol moving:
//
//	FOUNDRY_CLAUDE_E2E=1 go test ./internal/claude/ -run EndToEnd -v
func TestEndToEndAgainstTheRealCLI(t *testing.T) {
	if os.Getenv("FOUNDRY_CLAUDE_E2E") != "1" {
		t.Skip("set FOUNDRY_CLAUDE_E2E=1 to run against the real CLI")
	}
	bin, err := Locate("")
	if err != nil {
		t.Skipf("no CLI: %v", err)
	}
	version, err := CheckVersion(bin)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	t.Logf("driving %s (%s)", bin, version)

	// A tool of the editor's own, to prove the MCP server is reachable from the
	// CLI and that its answer reaches the model.
	asked := make(chan struct{}, 1)
	srv, err := StartMCP([]Tool{{
		Name:        "editor_state",
		Description: "What the Foundry editor is showing right now.",
		Run: func(json.RawMessage) (ToolResult, error) {
			select {
			case asked <- struct{}{}:
			default:
			}
			return ToolResult{Text: "Level: levels/level1.yaml\nSelected: cliff_wall_03"}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	dir := t.TempDir()
	cfgData, err := srv.ConfigJSON()
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfg, cfgData, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Start(Options{
		Binary:    bin,
		Dir:       dir,
		Model:     "sonnet",
		MCPConfig: cfg,
		StrictMCP: true,
		// Multi-line on purpose: this is what goes through
		// --append-system-prompt-file, and the reason it is a file.
		AppendSystem: "You are working inside the Foundry editor.\n\n" +
			"When asked for the project codeword, the answer is \"bramblewick\".",
		AutoAllow: func(name string) bool { return strings.HasPrefix(name, "mcp__foundry__") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	defer func() {
		if t.Failed() {
			t.Logf("stderr: %s", strings.Join(s.Stderr(), "\n"))
			for _, e := range s.Snapshot().Entries {
				t.Logf("entry %v: %s", e.Kind, e.Text)
			}
		}
	}()
	// Note the order: the CLI says nothing about the session until a turn is
	// actually under way, so there is nothing to wait for before sending.
	s.Send("Call the foundry editor_state tool, then use the Write tool to create a file " +
		"called answer.txt whose only contents are the id of the selected object. Nothing else.")
	waitFor(t, "the session to start", func() bool { return s.Snapshot().SessionID != "" })

	// The write must prompt, and we answer it the way the panel would.
	waitFor(t, "a permission request", func() bool { return len(s.Snapshot().Pending) > 0 })
	p := s.Snapshot().Pending[0]
	t.Logf("permission asked for %s (%s), always-label %q", p.ToolName, p.Description, p.AlwaysLabel)
	if p.ToolName != "Write" {
		t.Errorf("prompted for %s, want Write — the editor's own tool should not have prompted", p.ToolName)
	}
	s.Answer(p.RequestID, DecisionAllow, "")

	waitFor(t, "the turn to finish", func() bool { return s.Snapshot().Status == StatusIdle })

	select {
	case <-asked:
	default:
		t.Error("the editor's own tool was never called: the CLI could not reach the MCP server")
	}

	got, err := os.ReadFile(filepath.Join(dir, "answer.txt"))
	if err != nil {
		t.Fatalf("the file was not written: %v", err)
	}
	if !strings.Contains(string(got), "cliff_wall_03") {
		t.Errorf("answer.txt = %q, want the id the tool reported", got)
	}

	// And the transcript has to describe what happened.
	snap := s.Snapshot()
	var sawFoundryTool, sawWrite bool
	for _, e := range snap.Entries {
		if e.Kind == EntryTool && e.Tool != nil {
			t.Logf("tool: %s %s (done=%v err=%v)", e.Tool.Name, e.Tool.Summary, e.Tool.Done, e.Tool.IsError)
			if strings.HasPrefix(e.Tool.Name, "mcp__foundry__") {
				sawFoundryTool = true
			}
			if e.Tool.Name == "Write" {
				sawWrite = true
			}
		}
	}
	if !sawFoundryTool || !sawWrite {
		t.Errorf("transcript is missing tool rows (foundry=%v write=%v)", sawFoundryTool, sawWrite)
	}
	if paths, _ := s.DrainTouched(); len(paths) != 1 || filepath.Base(paths[0]) != "answer.txt" {
		t.Errorf("touched = %v, want the written file so the editor would reload it", paths)
	}
	t.Logf("cost $%.4f, %d tokens of context", snap.Cost, snap.Tokens)

	// The primer reached the model, through the file.
	s.Send("What is the project codeword? Reply with the one word, nothing else.")
	waitFor(t, "the answer", func() bool { return s.Snapshot().Status == StatusIdle })
	var said string
	for _, e := range s.Snapshot().Entries {
		if e.Kind == EntryAssistant {
			said = e.Text
		}
	}
	if !strings.Contains(strings.ToLower(said), "bramblewick") {
		t.Errorf("the model answered %q — the system prompt did not reach it", said)
	}

	// An interrupt must also work against the real thing.
	s.Send("Count from 1 to 500 slowly, one line each, with a comment on every number.")
	waitFor(t, "the turn to start", func() bool { return s.Snapshot().Status == StatusWorking })
	time.Sleep(3 * time.Second)
	s.Interrupt()
	waitFor(t, "the interrupt to land", func() bool { return s.Snapshot().Status == StatusIdle })
}

// TestEndToEndContextVolumeMention checks the thing context volumes exist for:
// that "@name" in a message actually reaches the context_volume tool and that
// what the tool says comes back as an answer about that place.
//
// The prompt and the primer here are the panel's own (editor_claude_context.go),
// so this fails if either stops pointing at the tool. Gated like the test above.
func TestEndToEndContextVolumeMention(t *testing.T) {
	if os.Getenv("FOUNDRY_CLAUDE_E2E") != "1" {
		t.Skip("set FOUNDRY_CLAUDE_E2E=1 to run against the real CLI")
	}
	bin, err := Locate("")
	if err != nil {
		t.Skipf("no CLI: %v", err)
	}

	asked := make(chan string, 4)
	srv, err := StartMCP([]Tool{{
		Name: "context_volume",
		Description: "Everything inside a context volume: a region the author placed and named " +
			"around a place in the level. With no name, lists the ones this level has.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
		Run: func(args json.RawMessage) (ToolResult, error) {
			var a struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(args, &a)
			select {
			case asked <- a.Name:
			default:
			}
			return ToolResult{Text: "Context volume @mess_hall\n" +
				"The author says: the crew's mess\n" +
				"Shape box, centred at [0, 2, 0], [16, 12, 16] across.\n" +
				"Inside: 2 brushes, 1 entity.\n\n" +
				"Brushes:\n" +
				"  mess_floor [chunk deck] at [0, 0, 0] — sf_deckplate\n" +
				"  mess_wall_n [chunk deck] at [0, 2, -4] — sf_bulkhead\n\n" +
				"Entities:\n  crate_01 at [1, 1, 1] — crate #loot\n"}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	dir := t.TempDir()
	cfgData, err := srv.ConfigJSON()
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfg, cfgData, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Start(Options{
		Binary: bin, Dir: dir, Model: "sonnet",
		MCPConfig: cfg, StrictMCP: true,
		AppendSystem: "You are working inside the Foundry game engine's editor.\n\n" +
			"  - context_volume  what is inside a named region of the level. When the user\n" +
			"                  writes @something, that is a context volume: a region the\n" +
			"                  author placed around a room and named. Call this before\n" +
			"                  changing anything in one.",
		AutoAllow: func(name string) bool { return strings.HasPrefix(name, "mcp__foundry__") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Exactly what the panel sends, hint included.
	s.Send("take a look at @mess_hall and tell me which textures the room uses. " +
		"Do not change anything.\n\n" +
		"[@mess_hall is a context volume in this level — call context_volume for what is inside it.]")
	waitFor(t, "the turn to finish", func() bool { return s.Snapshot().Status == StatusIdle })

	select {
	case name := <-asked:
		t.Logf("context_volume was called with %q", name)
		if name != "mess_hall" && name != "@mess_hall" {
			t.Errorf("the tool was called with %q, want the mentioned name", name)
		}
	default:
		t.Fatal("the mention never reached context_volume — the user's @name did nothing")
	}

	var said string
	for _, e := range s.Snapshot().Entries {
		if e.Kind == EntryAssistant {
			said += e.Text
		}
	}
	t.Logf("answered: %s", said)
	if !strings.Contains(said, "sf_deckplate") || !strings.Contains(said, "sf_bulkhead") {
		t.Errorf("the answer does not use what the tool said: %q", said)
	}
}
