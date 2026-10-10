package claude

// protocol.go — the stream-json protocol the CLI speaks.
//
// `claude -p --input-format stream-json --output-format stream-json` is a
// conversation in newline-delimited JSON: we write user messages and control
// responses on its stdin, it writes assistant messages, tool results, control
// requests and turn results on its stdout. Every shape here was taken from a
// live session against 2.1.278 and kept in testdata/, because none of it is in
// the CLI's --help and all of it can drift; when it drifts, re-record the
// fixtures and the parser tests will say exactly what moved.
//
// Two things are worth knowing before reading on:
//
//   - The stream carries far more than we use (rate limits, partial-message
//     deltas, hook events, status pings). Everything decodes through one struct
//     with the fields we care about, and anything unrecognised is ignored rather
//     than being an error — a new message type in a future CLI must not break
//     the panel.
//   - Permission prompts arrive as a control_request the *CLI* sends *us*, and
//     it blocks on the answer. That direction is the reverse of everything else
//     and is the whole reason the session keeps a writer goroutine.

import (
	"encoding/json"
	"strings"
)

// --- what we send -----------------------------------------------------------

// userInput is a turn typed by the user.
type userInput struct {
	Type    string      `json:"type"` // "user"
	Message userMessage `json:"message"`
}

type userMessage struct {
	Role    string `json:"role"` // "user"
	Content string `json:"content"`
}

// controlRequestOut is a request we make of the CLI: the initialize handshake,
// or an interrupt.
type controlRequestOut struct {
	Type      string          `json:"type"` // "control_request"
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// controlResponseOut answers a control_request the CLI sent us — in practice
// always a permission decision.
type controlResponseOut struct {
	Type     string            `json:"type"` // "control_response"
	Response controlResponseIn `json:"response"`
}

type controlResponseIn struct {
	Subtype   string      `json:"subtype"` // "success" | "error"
	RequestID string      `json:"request_id"`
	Response  interface{} `json:"response,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// permissionAllow and permissionDeny are the two answers to can_use_tool.
//
// UpdatedInput is required on an allow and is normally the input unchanged; the
// protocol allows editing a tool's arguments before it runs, which we do not do.
// UpdatedPermissions carries the request's own permission_suggestions back, and
// that is what makes an "Allow always" button work: hand back the suggestion and
// the CLI stops asking for that kind of call.
type permissionAllow struct {
	Behavior           string            `json:"behavior"` // "allow"
	UpdatedInput       json.RawMessage   `json:"updatedInput"`
	UpdatedPermissions []json.RawMessage `json:"updatedPermissions,omitempty"`
}

type permissionDeny struct {
	Behavior string `json:"behavior"` // "deny"
	Message  string `json:"message"`
}

// --- what we receive --------------------------------------------------------

// line is one line of the CLI's stdout. Every message type decodes into this;
// which fields are set depends on Type.
type line struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	UUID      string `json:"uuid"`

	// type: assistant | user
	Message         json.RawMessage `json:"message"`
	ParentToolUseID string          `json:"parent_tool_use_id"`

	// type: control_request (the CLI asking us)
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`

	// type: control_response (the CLI answering us)
	Response json.RawMessage `json:"response"`

	// type: system, subtype: init
	Tools      []string   `json:"tools"`
	Model      string     `json:"model"`
	CWD        string     `json:"cwd"`
	MCPServers []mcpState `json:"mcp_servers"`
	Version    string     `json:"claude_code_version"`

	// type: system, subtype: permission_denied
	ToolName string `json:"tool_name"`

	// type: result
	Result       string  `json:"result"`
	IsError      bool    `json:"is_error"`
	NumTurns     int     `json:"num_turns"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	Usage        *usage  `json:"usage"`
}

type mcpState struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
}

// ContextTokens is what the turn left in the model's context: everything it read
// this turn, however it was cached. This is the number worth showing next to a
// context limit.
func (u *usage) ContextTokens() int {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
}

// apiMessage is the Anthropic message inside an assistant or user line.
type apiMessage struct {
	Role    string         `json:"role"`
	Model   string         `json:"model"`
	Content []contentBlock `json:"content"`
}

// contentBlock is one block of an API message. A user line's blocks are tool
// results; an assistant line's are text, thinking, or tool calls.
type contentBlock struct {
	Type string `json:"type"`

	// type: text | thinking
	Text     string `json:"text"`
	Thinking string `json:"thinking"`

	// type: tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// type: tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ResultText flattens a tool_result's content, which is either a plain string or
// an array of blocks depending on the tool.
func (b contentBlock) ResultText() string {
	if len(b.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(b.Content, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(b.Content, &blocks); err != nil {
		return strings.TrimSpace(string(b.Content))
	}
	var sb strings.Builder
	for _, sub := range blocks {
		switch sub.Type {
		case "text":
			sb.WriteString(sub.Text)
		case "image":
			sb.WriteString("[image]")
		}
	}
	return sb.String()
}

// canUseTool is the body of the permission control_request.
type canUseTool struct {
	Subtype     string          `json:"subtype"` // "can_use_tool"
	ToolName    string          `json:"tool_name"`
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	ToolUseID   string          `json:"tool_use_id"`
	// Suggestions are the CLI's own offers — "switch to acceptEdits for this
	// session", "always allow this command". Handed back on an allow, they are
	// what stops it asking again.
	Suggestions []json.RawMessage `json:"permission_suggestions"`
}

// initializeRequest is the handshake. The SDKs send it before the first turn;
// without it the CLI does not treat us as a host, and hooks and commands are not
// reported. The hooks field is required even when empty.
type initializeRequest struct {
	Subtype string                 `json:"subtype"` // "initialize"
	Hooks   map[string]interface{} `json:"hooks"`
}

type interruptRequest struct {
	Subtype string `json:"subtype"` // "interrupt"
}

// setPermissionModeRequest changes the mode mid-session, so the panel's mode
// picker does not have to restart the process.
type setPermissionModeRequest struct {
	Subtype string `json:"subtype"` // "set_permission_mode"
	Mode    string `json:"mode"`
}
