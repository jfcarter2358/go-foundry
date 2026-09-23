package editor

// The editor's own MCP tools, called the way the CLI calls them: over the
// server's HTTP transport, from another goroutine, while the "frame loop" pumps
// the main-thread queue.
//
// The hand-off is the part worth testing. A tool that touched Editor from the
// server's goroutine would be a data race against the whole editor, and one that
// queued work nobody pumped would hang Claude for twenty seconds and then fail.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"foundry/internal/claude"
	"foundry/internal/world"
)

// pumpMainThread runs the queue the way devDrainWork does, until the test ends.
func pumpMainThread(t *testing.T) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				ClaudeRunQueuedTools()
				time.Sleep(time.Millisecond)
			}
		}
	}()
	t.Cleanup(func() { close(stop); <-done })
}

// callTool asks the server for a tool, as the CLI would.
func callTool(t *testing.T, srv *claude.MCPServer, cfgJSON []byte, name string, args map[string]any) map[string]any {
	t.Helper()
	var cfg struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		t.Fatal(err)
	}
	server := cfg.MCPServers["foundry"]
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", server.Headers["Authorization"])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("response %q: %v", raw, err)
	}
	res, _ := out["result"].(map[string]any)
	if res == nil {
		t.Fatalf("no result in %s", raw)
	}
	return res
}

func resultText(t *testing.T, res map[string]any) string {
	t.Helper()
	content, _ := res["content"].([]any)
	var b strings.Builder
	for _, c := range content {
		block, _ := c.(map[string]any)
		if block["type"] == "text" {
			b.WriteString(block["text"].(string))
		}
	}
	return b.String()
}

func TestEditorToolsAnswerOverMCPFromTheMainThread(t *testing.T) {
	withClaudePanel(t)
	withClaudeProject(t)
	defer saveEditorState()()
	pumpMainThread(t)

	Editor.SavePath = "/p/resources/levels/level1.yaml"
	Editor.level = &world.Level{Chunks: []world.Chunk{{
		ID: "hangar", Brushes: []world.Brush{{ID: "door_frame"}},
	}}}
	Editor.ActiveChunkIdx = 0
	Editor.SelectedBrushes = nil

	srv, err := claude.StartMCP(claudeTools())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cfg, err := srv.ConfigJSON()
	if err != nil {
		t.Fatal(err)
	}

	state := resultText(t, callTool(t, srv, cfg, "editor_state", nil))
	for _, want := range []string{"level1.yaml", "hangar", "Nothing is selected"} {
		if !strings.Contains(state, want) {
			t.Errorf("editor_state = %q, want it to mention %q", state, want)
		}
	}

	// A tool that changes the editor, from the far side of the socket.
	sel := resultText(t, callTool(t, srv, cfg, "select_object", map[string]any{"id": "door_frame"}))
	if !strings.Contains(sel, "door_frame") {
		t.Errorf("select_object = %q", sel)
	}
	if len(Editor.SelectedBrushes) != 1 {
		t.Fatalf("the selection was not made: %+v", Editor.SelectedBrushes)
	}
	// And the next call sees it, which is the whole point of going through the
	// editor rather than reading the file.
	if state := resultText(t, callTool(t, srv, cfg, "editor_state", nil)); !strings.Contains(state, "door_frame") {
		t.Errorf("editor_state after selecting = %q, want the selection", state)
	}

	// A miss is an answer, not a failure.
	miss := callTool(t, srv, cfg, "select_object", map[string]any{"id": "nope"})
	if miss["isError"] == true {
		t.Error("a brush that does not exist should be reported, not raised as an error")
	}
	// A missing argument is an error the model can correct.
	bad := callTool(t, srv, cfg, "select_object", map[string]any{})
	if bad["isError"] != true {
		t.Errorf("select_object with no id = %v, want an error", bad)
	}
}

func TestEditorToolsGiveUpRatherThanHangWhenNobodyPumps(t *testing.T) {
	// No pump: the editor is busy — loading a level, baking light. Claude has to
	// be told, not left waiting for ever.
	withClaudePanel(t)
	prev := claudeToolWait.Load()
	claudeToolWait.Store(int64(150 * time.Millisecond))
	t.Cleanup(func() { claudeToolWait.Store(prev) })

	done := make(chan error, 1)
	go func() {
		_, err := onMainThread(func() string { return "never" })
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("waiting for an editor that never answered returned no error")
		}
		if !strings.Contains(err.Error(), "did not answer") {
			t.Errorf("error = %v, want something a model can act on", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("onMainThread never gave up")
	}
	// The queued work must not be left behind to run against a later frame.
	ClaudeRunQueuedTools()
}
