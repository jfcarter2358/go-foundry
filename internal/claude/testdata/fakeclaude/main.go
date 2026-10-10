// Command fakeclaude stands in for the Claude Code CLI in tests.
//
// It speaks just enough of the stream-json protocol to exercise the session: the
// initialize handshake, a turn with streamed text and a tool call, a permission
// request it blocks on, and a result. It is a separate program rather than a
// goroutine because the thing being tested is the process plumbing — pipes,
// ordering, and shutdown — and none of that is exercised by feeding bytes to
// handle() directly.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func emit(s string) { fmt.Println(s) }

func main() {
	// The real CLI is given these; a test asserts they arrive.
	// A CLI that cannot run says so on stderr and exits, which is what the panel
	// has to surface.
	if os.Getenv("FAKECLAUDE_FAIL") != "" {
		fmt.Fprintln(os.Stderr, "Invalid API key · you are not logged in")
		os.Exit(1)
	}
	args := strings.Join(os.Args[1:], " ")
	if !strings.Contains(args, "--permission-prompt-tool stdio") {
		fmt.Fprintln(os.Stderr, "fakeclaude: expected --permission-prompt-tool stdio")
		os.Exit(2)
	}
	emit(`{"type":"system","subtype":"init","session_id":"fake-session","model":"claude-fake","claude_code_version":"2.1.278","cwd":"/project","mcp_servers":[{"name":"foundry","status":"connected"}]}`)

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m["type"] {
		case "control_request":
			req, _ := m["request"].(map[string]any)
			id, _ := m["request_id"].(string)
			switch req["subtype"] {
			case "initialize":
				emit(fmt.Sprintf(`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"commands":[]}}}`, id))
			case "interrupt":
				emit(fmt.Sprintf(`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{}}}`, id))
				emit(`{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"fake-session"}`)
			}
		case "control_response":
			// The answer to our permission request: run the tool, or report that
			// it was refused, exactly as the real CLI does.
			resp, _ := m["response"].(map[string]any)
			inner, _ := resp["response"].(map[string]any)
			if inner["behavior"] == "allow" {
				emit(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"File created successfully"}]}}`)
			} else {
				emit(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","is_error":true,"content":"denied"}]}}`)
			}
			emit(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}`)
			emit(`{"type":"result","subtype":"success","is_error":false,"session_id":"fake-session","total_cost_usd":0.25,"num_turns":1,"usage":{"input_tokens":10,"cache_read_input_tokens":90}}`)
		case "user":
			msg, _ := m["message"].(map[string]any)
			text, _ := msg["content"].(string)
			if strings.Contains(text, "slowly") {
				// A turn that does not finish on its own, for the interrupt test.
				emit(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text"}}}`)
				emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"1..."}}}`)
				continue
			}
			emit(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text"}}}`)
			emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Writing"}}}`)
			emit(`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`)
			emit(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Write","input":{"file_path":"/project/resources/scripts/door.flang","content":"on :start:"}}]}}`)
			emit(`{"type":"control_request","request_id":"perm_1","request":{"subtype":"can_use_tool","tool_name":"Write","display_name":"Write","description":"door.flang","input":{"file_path":"/project/resources/scripts/door.flang","content":"on :start:"},"permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}]}}`)
		}
	}
}
