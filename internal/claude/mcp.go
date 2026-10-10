package claude

// mcp.go — the editor as a tool server.
//
// This is what makes the panel worth having over a terminal running `claude` in
// the same folder. Through it Claude can ask what the editor is looking at, run
// the level's own checks, and see the viewport, rather than inferring all of it
// from the files.
//
// MCP over HTTP, written by hand. The protocol surface actually needed is three
// methods — initialize, tools/list, tools/call — and a hand-rolled JSON-RPC
// handler is a few hundred lines against a dependency this project would
// otherwise not have. (Foundry has four direct dependencies. That is a deliberate
// position, and one tool server is not the thing to spend the fifth on.)
//
// It listens on 127.0.0.1 on a port the OS picks, and every request must carry a
// bearer token generated at startup and written only into the config file handed
// to our own child process. The editor is not a public service and must not
// become one because a tool panel was added to it.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// mcpProtocolVersion is what the server reports when a client does not name one.
// The CLI sends its own and we echo it back, which is what the specification
// asks for and what keeps this working as the protocol moves.
const mcpProtocolVersion = "2025-06-18"

// mcpMaxBody bounds a request. Tool arguments are small; a megabyte is already
// far past anything legitimate.
const mcpMaxBody = 1 << 20

// Tool is one thing the editor can do for Claude.
type Tool struct {
	Name        string
	Description string
	// Schema is the JSON Schema of the arguments. Nil means "no arguments".
	Schema map[string]any
	// Run does the work. It is called on the server's own goroutine, so anything
	// that touches the editor must hand itself to the main thread and wait —
	// see editor_claude_tools.go.
	Run func(args json.RawMessage) (ToolResult, error)
}

// ToolResult is what a tool gives back. Text is the usual answer; Image carries
// a screenshot, which Claude can actually look at.
type ToolResult struct {
	Text      string
	Image     []byte
	ImageMIME string
	IsError   bool
}

// MCPServer is the running tool server.
type MCPServer struct {
	ln    net.Listener
	srv   *http.Server
	token string
	tools map[string]Tool
	order []string
}

// StartMCP starts the server on a loopback port.
func StartMCP(tools []Tool) (*MCPServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		ln.Close()
		return nil, err
	}
	s := &MCPServer{
		ln:    ln,
		token: base64.RawURLEncoding.EncodeToString(tok),
		tools: map[string]Tool{},
	}
	for _, t := range tools {
		s.tools[t.Name] = t
		s.order = append(s.order, t.Name)
	}
	s.srv = &http.Server{
		Handler:           http.HandlerFunc(s.handle),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go s.srv.Serve(ln)
	return s, nil
}

// URL is where the server is listening.
func (s *MCPServer) URL() string {
	return "http://" + s.ln.Addr().String() + "/"
}

// Close stops the server.
func (s *MCPServer) Close() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

// ConfigJSON is the --mcp-config document naming this server. Written to a file
// for the CLI; it carries the token, so it belongs somewhere private.
func (s *MCPServer) ConfigJSON() ([]byte, error) {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"foundry": map[string]any{
				"type":    "http",
				"url":     s.URL(),
				"headers": map[string]string{"Authorization": "Bearer " + s.token},
			},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// --- JSON-RPC ---------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *MCPServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// GET is how a client opens the server-sent-events stream for
		// server-initiated messages. This server never initiates anything, so
		// there is nothing to stream.
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, mcpMaxBody))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	result, rerr := s.dispatch(req)
	if len(req.ID) == 0 {
		// A notification. Nothing to answer, but the client is waiting on a
		// status code.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
	writeRPC(w, resp)
}

// authorized is a constant-time comparison of the bearer token. The port is
// loopback and the token is per-run, but a timing-safe compare costs nothing.
func (s *MCPServer) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h[len(prefix):]), []byte(s.token)) == 1
}

func (s *MCPServer) dispatch(req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = mcpProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "foundry", "version": "1"},
		}, nil

	case "tools/list":
		list := make([]map[string]any, 0, len(s.order))
		for _, name := range s.order {
			t := s.tools[name]
			schema := t.Schema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			list = append(list, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": schema,
			})
		}
		return map[string]any{"tools": list}, nil

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		t, ok := s.tools[p.Name]
		if !ok {
			return nil, &rpcError{Code: -32601, Message: "no such tool: " + p.Name}
		}
		res, err := t.Run(p.Arguments)
		if err != nil {
			// A tool that failed is reported as a tool result, not as a protocol
			// error: Claude can read it, and often act on it.
			return toolContent(ToolResult{Text: err.Error(), IsError: true}), nil
		}
		return toolContent(res), nil

	case "notifications/initialized", "notifications/cancelled":
		return map[string]any{}, nil
	}
	return nil, &rpcError{Code: -32601, Message: "no such method: " + req.Method}
}

// toolContent is a ToolResult as MCP content blocks.
func toolContent(r ToolResult) map[string]any {
	var content []map[string]any
	if r.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": r.Text})
	}
	if len(r.Image) > 0 {
		mime := r.ImageMIME
		if mime == "" {
			mime = "image/png"
		}
		content = append(content, map[string]any{
			"type":     "image",
			"data":     base64.StdEncoding.EncodeToString(r.Image),
			"mimeType": mime,
		})
	}
	if content == nil {
		content = []map[string]any{{"type": "text", "text": "(no output)"}}
	}
	return map[string]any{"content": content, "isError": r.IsError}
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprint(len(b)))
	_, _ = w.Write(b)
}
