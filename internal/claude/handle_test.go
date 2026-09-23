package claude

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// newForTest is a session with no process behind it: handle() is the whole
// protocol, and it does not need one. Anything that would write to the CLI goes
// into the out channel, which the test can read.
func newForTest() *Session {
	return &Session{
		out:      make(chan []byte, 64),
		done:     make(chan struct{}),
		pending:  map[string]*Permission{},
		tools:    map[string]int{},
		liveText: -1,
	}
}

func (s *Session) feed(t *testing.T, lines ...string) {
	t.Helper()
	for _, l := range lines {
		s.handle([]byte(l))
	}
}

// sent drains what the session wrote back to the CLI.
func (s *Session) sent() []map[string]any {
	var out []map[string]any
	for {
		select {
		case b := <-s.out:
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				out = append(out, m)
			}
		default:
			return out
		}
	}
}

func TestRecordedSessionParses(t *testing.T) {
	b, err := os.ReadFile("testdata/session_2.1.278.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	s := newForTest()
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		s.handle([]byte(l))
	}
	snap := s.Snapshot()
	if snap.SessionID == "" {
		t.Error("no session id was picked up from the init message")
	}
	if snap.Model == "" {
		t.Error("no model was picked up from the init message")
	}
	if snap.Cost <= 0 {
		t.Errorf("cost = %v, want the value from the result message", snap.Cost)
	}
	// The recording is a denied write, a follow-up question and an interrupt.
	var kinds []EntryKind
	for _, e := range snap.Entries {
		kinds = append(kinds, e.Kind)
	}
	if !hasKind(kinds, EntryTool) {
		t.Error("no tool row: the recording contains a Write call")
	}
	if !hasKind(kinds, EntryAssistant) {
		t.Error("no assistant text")
	}
	// Nothing may be left streaming once the finished messages have arrived.
	for i, e := range snap.Entries {
		if e.Live {
			t.Errorf("entry %d is still marked live after the stream ended: %q", i, e.Text)
		}
	}
}

func hasKind(ks []EntryKind, want EntryKind) bool {
	for _, k := range ks {
		if k == want {
			return true
		}
	}
	return false
}

func TestStreamingTextIsReplacedByTheFinishedMessage(t *testing.T) {
	s := newForTest()
	s.feed(t,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}}`,
	)
	snap := s.Snapshot()
	if len(snap.Entries) != 1 || snap.Entries[0].Text != "Hello" || !snap.Entries[0].Live {
		t.Fatalf("mid-stream entries = %+v, want one live \"Hello\"", snap.Entries)
	}
	s.feed(t, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello there"}]}}`)
	snap = s.Snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("after the finished message there are %d entries, want the streamed one replaced: %+v", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Text != "Hello there" || snap.Entries[0].Live {
		t.Errorf("entry = %+v, want the finished text and not live", snap.Entries[0])
	}
}

func TestToolCallAndResultAreOneRow(t *testing.T) {
	s := newForTest()
	s.feed(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Write","input":{"file_path":"/project/resources/scripts/door.flang","content":"on :start:"}}]}}`,
	)
	snap := s.Snapshot()
	if len(snap.Entries) != 1 || snap.Entries[0].Kind != EntryTool {
		t.Fatalf("entries = %+v, want one tool row", snap.Entries)
	}
	call := snap.Entries[0].Tool
	if call.Summary != "door.flang" {
		t.Errorf("summary = %q, want the file's name", call.Summary)
	}
	if call.Done {
		t.Error("the call is marked done before its result arrived")
	}
	s.feed(t, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"File created successfully"}]}}`)
	snap = s.Snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("the result added a row instead of filling the call in: %+v", snap.Entries)
	}
	call = snap.Entries[0].Tool
	if !call.Done || call.IsError || !strings.Contains(call.Result, "created") {
		t.Errorf("call = %+v, want it done with its result", call)
	}
	// Only now, with the result in, is the file really on disk.
	paths, ranCmd := s.DrainTouched()
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "door.flang") {
		t.Errorf("touched = %v, want the written script", paths)
	}
	if ranCmd {
		t.Error("ranCommand set without a Bash call")
	}
	if paths, _ := s.DrainTouched(); len(paths) != 0 {
		t.Errorf("draining twice returned %v, want nothing the second time", paths)
	}
}

func TestADeniedWriteIsNotTreatedAsTouched(t *testing.T) {
	s := newForTest()
	s.feed(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Write","input":{"file_path":"/project/resources/levels/level1.yaml","content":"x"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","is_error":true,"content":"permission denied"}]}}`,
	)
	if paths, _ := s.DrainTouched(); len(paths) != 0 {
		t.Errorf("touched = %v, want nothing: the write was refused", paths)
	}
}

func TestBashMarksTheProjectWorthRereading(t *testing.T) {
	s := newForTest()
	s.feed(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"python3 tools/level1_ground_gen.py\nwith more"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"ok"}]}}`,
	)
	snap := s.Snapshot()
	if got := snap.Entries[0].Tool.Summary; got != "python3 tools/level1_ground_gen.py …" {
		t.Errorf("summary = %q, want the command's first line", got)
	}
	if _, ranCmd := s.DrainTouched(); !ranCmd {
		t.Error("a Bash call should mark the project worth rereading")
	}
}

func TestPermissionRequestStallsTheTurnUntilAnswered(t *testing.T) {
	s := newForTest()
	s.feed(t, `{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool","tool_name":"Write","display_name":"Write","description":"level1.yaml","input":{"file_path":"/project/level1.yaml"},"permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}]}}`)
	snap := s.Snapshot()
	if snap.Status != StatusWaiting {
		t.Errorf("status = %v, want StatusWaiting", snap.Status)
	}
	if len(snap.Pending) != 1 {
		t.Fatalf("pending = %+v, want the one request", snap.Pending)
	}
	if got := snap.Pending[0].AlwaysLabel; got != "Allow all edits" {
		t.Errorf("always label = %q, want it from the CLI's own suggestion", got)
	}
	if len(s.sent()) != 0 {
		t.Fatal("something was answered before the user decided")
	}

	s.Answer("req_1", DecisionAllowAlways, "")
	sent := s.sent()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want one control response", len(sent))
	}
	resp, _ := sent[0]["response"].(map[string]any)
	inner, _ := resp["response"].(map[string]any)
	if inner["behavior"] != "allow" {
		t.Errorf("behaviour = %v, want allow", inner["behavior"])
	}
	if _, ok := inner["updatedPermissions"]; !ok {
		t.Error("an always-allow must hand the CLI's suggestions back, or it asks again next time")
	}
	if s.Snapshot().Status == StatusWaiting {
		t.Error("still waiting after the last request was answered")
	}
	// Answering twice must not write a second response: the CLI would treat the
	// unknown request id as an error.
	s.Answer("req_1", DecisionAllow, "")
	if len(s.sent()) != 0 {
		t.Error("answering an already-answered request wrote another response")
	}
}

func TestDenyCarriesAMessage(t *testing.T) {
	s := newForTest()
	s.feed(t, `{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"rm -rf /"}}}`)
	s.Answer("req_1", DecisionDeny, "no")
	sent := s.sent()
	resp, _ := sent[0]["response"].(map[string]any)
	inner, _ := resp["response"].(map[string]any)
	if inner["behavior"] != "deny" || inner["message"] != "no" {
		t.Errorf("response = %v, want a deny carrying the reason", inner)
	}
}

func TestTheEditorsOwnToolsAreAllowedWithoutAsking(t *testing.T) {
	s := newForTest()
	s.opts.AutoAllow = func(name string) bool { return strings.HasPrefix(name, "mcp__foundry__") }
	s.feed(t, `{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool","tool_name":"mcp__foundry__editor_state","input":{}}}`)
	if snap := s.Snapshot(); len(snap.Pending) != 0 {
		t.Error("the editor's own tool prompted the user")
	}
	if len(s.sent()) != 1 {
		t.Error("the editor's own tool was not answered automatically")
	}
}

func TestAnUnknownControlRequestIsAnsweredRatherThanIgnored(t *testing.T) {
	s := newForTest()
	s.feed(t, `{"type":"control_request","request_id":"req_9","request":{"subtype":"something_new"}}`)
	if len(s.sent()) != 1 {
		t.Error("an unhandled control request must still be answered, or the CLI waits for ever")
	}
}

func TestInterruptedTurnReadsAsStopped(t *testing.T) {
	s := newForTest()
	s.feed(t, `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"s1","total_cost_usd":0.01}`)
	snap := s.Snapshot()
	last := snap.Entries[len(snap.Entries)-1]
	if last.Kind != EntryNotice || last.Text != "Stopped." {
		t.Errorf("last entry = %+v, want a plain stopped notice", last)
	}
	if snap.Status != StatusIdle {
		t.Errorf("status = %v, want idle again after an interrupt", snap.Status)
	}
}

func TestNonJSONOutputBecomesANotice(t *testing.T) {
	s := newForTest()
	s.feed(t, `Invalid API key · Please run /login`)
	snap := s.Snapshot()
	if len(snap.Entries) != 1 || snap.Entries[0].Kind != EntryNotice {
		t.Fatalf("entries = %+v, want the message shown rather than dropped", snap.Entries)
	}
}

func TestToolSummary(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"Read", `{"file_path":"/a/b/level1.yaml"}`, "level1.yaml"},
		{"Grep", `{"pattern":"on :start:"}`, "on :start:"},
		{"WebFetch", `{"url":"https://example.com"}`, "https://example.com"},
		{"Task", `{"description":"Find the spawn"}`, "Find the spawn"},
		{"Unknown", `{"whatever":1}`, ""},
	}
	for _, c := range cases {
		if got := ToolSummary(c.name, json.RawMessage(c.input)); got != c.want {
			t.Errorf("ToolSummary(%s, %s) = %q, want %q", c.name, c.input, got, c.want)
		}
	}
}
