package claude

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func testServer(t *testing.T, tools ...Tool) (*MCPServer, func(method string, params any) map[string]any) {
	t.Helper()
	if len(tools) == 0 {
		tools = []Tool{{
			Name:        "editor_state",
			Description: "what the editor is showing",
			Run: func(json.RawMessage) (ToolResult, error) {
				return ToolResult{Text: "level1.yaml"}, nil
			},
		}}
	}
	s, err := StartMCP(tools)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	call := func(method string, params any) map[string]any {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
		})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, s.URL(), bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+s.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("response %q: %v", b, err)
		}
		return out
	}
	return s, call
}

func TestMCPHandshakeAndToolList(t *testing.T) {
	_, call := testServer(t)

	init := call("initialize", map[string]any{"protocolVersion": "2025-11-25"})
	res, _ := init["result"].(map[string]any)
	if res["protocolVersion"] != "2025-11-25" {
		t.Errorf("protocolVersion = %v, want the client's own echoed back", res["protocolVersion"])
	}
	if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
		t.Error("the server did not advertise tools")
	}

	list, _ := call("tools/list", nil)["result"].(map[string]any)
	tools, _ := list["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want one", tools)
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "editor_state" {
		t.Errorf("tool name = %v", tool["name"])
	}
	// A tool with no arguments must still carry a schema, or some clients will
	// not call it.
	if _, ok := tool["inputSchema"].(map[string]any); !ok {
		t.Error("a tool without arguments was listed with no inputSchema")
	}
}

func TestMCPToolCallReturnsContent(t *testing.T) {
	_, call := testServer(t)
	res, _ := call("tools/call", map[string]any{"name": "editor_state"})["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %v", content)
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "level1.yaml" {
		t.Errorf("block = %v, want the tool's text", block)
	}
	if res["isError"] != false {
		t.Errorf("isError = %v, want false", res["isError"])
	}
}

func TestMCPToolFailureIsAResultNotAProtocolError(t *testing.T) {
	// Claude can read and act on a failed tool result; a JSON-RPC error just
	// looks like the server is broken.
	_, call := testServer(t, Tool{
		Name: "reload",
		Run: func(json.RawMessage) (ToolResult, error) {
			return ToolResult{}, errors.New("no level is open")
		},
	})
	out := call("tools/call", map[string]any{"name": "reload"})
	if _, isErr := out["error"]; isErr {
		t.Fatalf("the failure came back as a protocol error: %v", out)
	}
	res, _ := out["result"].(map[string]any)
	if res["isError"] != true {
		t.Errorf("isError = %v, want true", res["isError"])
	}
	content, _ := res["content"].([]any)
	block, _ := content[0].(map[string]any)
	if !strings.Contains(block["text"].(string), "no level is open") {
		t.Errorf("content = %v, want the error's message", block)
	}
}

func TestMCPImageResultIsBase64(t *testing.T) {
	pixels := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	_, call := testServer(t, Tool{
		Name: "viewport",
		Run: func(json.RawMessage) (ToolResult, error) {
			return ToolResult{Image: pixels, ImageMIME: "image/png"}, nil
		},
	})
	res, _ := call("tools/call", map[string]any{"name": "viewport"})["result"].(map[string]any)
	content, _ := res["content"].([]any)
	block, _ := content[0].(map[string]any)
	if block["type"] != "image" || block["mimeType"] != "image/png" {
		t.Fatalf("block = %v, want an image", block)
	}
	got, err := base64.StdEncoding.DecodeString(block["data"].(string))
	if err != nil || !bytes.Equal(got, pixels) {
		t.Errorf("data = %v (%v), want the bytes back", got, err)
	}
}

func TestMCPUnknownToolAndMethod(t *testing.T) {
	_, call := testServer(t)
	if _, ok := call("tools/call", map[string]any{"name": "nope"})["error"]; !ok {
		t.Error("calling a tool that does not exist should be an error")
	}
	if _, ok := call("no/such/method", nil)["error"]; !ok {
		t.Error("an unknown method should be an error")
	}
}

func TestMCPRefusesRequestsWithoutTheToken(t *testing.T) {
	s, _ := testServer(t)
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	// No header at all.
	resp, err := http.Post(s.URL(), "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: the editor must not answer anything on this port", resp.StatusCode)
	}
	// And a wrong one.
	req, _ := http.NewRequest(http.MethodPost, s.URL(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer not-the-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("status with a wrong token = %d, want 401", resp2.StatusCode)
	}
}

func TestMCPConfigNamesTheServerAndCarriesTheToken(t *testing.T) {
	s, _ := testServer(t)
	data, err := s.ConfigJSON()
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	f, ok := cfg.MCPServers["foundry"]
	if !ok {
		t.Fatalf("config = %s, want a foundry server", data)
	}
	if f.Type != "http" || !strings.HasPrefix(f.URL, "http://127.0.0.1:") {
		t.Errorf("server = %+v, want http on loopback only", f)
	}
	if f.Headers["Authorization"] != "Bearer "+s.token {
		t.Error("the config does not carry the token, so the CLI could not authenticate")
	}
}

func TestMCPNotificationsAreAccepted(t *testing.T) {
	s, _ := testServer(t)
	req, _ := http.NewRequest(http.MethodPost, s.URL(),
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202 for a notification", resp.StatusCode)
	}
}
