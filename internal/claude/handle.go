package claude

// handle.go — one line of the CLI's output, turned into transcript rows.
//
// Split out of session.go because this is where the protocol's quirks live and
// they are worth reading on their own:
//
//   - Assistant text arrives twice: first as partial deltas while the model is
//     generating, then as one finished message. The deltas are what makes the
//     panel feel live, and the finished message is the one to trust, so a
//     streaming row is marked Live and replaced when the real one lands.
//   - Tool calls and their results arrive as separate messages, the result
//     coming back as a *user* message. They are stitched back together by
//     tool_use id so the UI can draw one row that fills in.
//   - A tool_result for a file write is the only reliable signal that the file
//     is actually on disk; the tool_use alone means Claude intended to write it
//     and may still have been denied. So touched files are recorded from the
//     result, not from the call.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (s *Session) handle(b []byte) {
	var ln line
	if err := json.Unmarshal(b, &ln); err != nil {
		// Not JSON: the CLI printed something unexpected. Worth showing rather
		// than dropping — it is usually an auth or config error.
		if t := strings.TrimSpace(string(b)); t != "" {
			s.notice(t)
		}
		return
	}
	switch ln.Type {
	case "system":
		s.handleSystem(ln)
	case "assistant":
		s.handleAssistant(ln)
	case "user":
		s.handleToolResults(ln)
	case "control_request":
		s.handleControlRequest(ln)
	case "stream_event":
		s.handleStreamEvent(b)
	case "result":
		s.handleResult(ln)
	}
}

func (s *Session) handleSystem(ln line) {
	switch ln.Subtype {
	case "init":
		s.mu.Lock()
		s.sessionID, s.model, s.version, s.mcp = ln.SessionID, ln.Model, ln.Version, ln.MCPServers
		s.seq++
		s.mu.Unlock()
		// An MCP server that failed to start is worth saying out loud: the
		// editor's own tools silently missing would look like Claude ignoring
		// the editor.
		for _, m := range ln.MCPServers {
			if m.Status != "connected" && m.Status != "pending" {
				s.notice(fmt.Sprintf("MCP server %q is %s — the editor's tools may be unavailable.", m.Name, m.Status))
			}
		}
	case "permission_denied":
		// Reached when something denies a call without asking us — a settings
		// rule, or plan mode. Without this the turn just stops and the panel
		// looks broken.
		s.notice(fmt.Sprintf("%s was not allowed to run.", ln.ToolName))
	}
}

func (s *Session) handleAssistant(ln line) {
	var msg apiMessage
	if err := json.Unmarshal(ln.Message, &msg); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropLiveLocked()
	for _, c := range msg.Content {
		switch c.Type {
		case "text":
			if strings.TrimSpace(c.Text) == "" {
				continue
			}
			s.entries = append(s.entries, Entry{Kind: EntryAssistant, Text: c.Text, At: time.Now()})
		case "thinking":
			if strings.TrimSpace(c.Thinking) == "" {
				continue
			}
			s.entries = append(s.entries, Entry{Kind: EntryThinking, Text: c.Thinking, At: time.Now()})
		case "tool_use":
			call := &ToolCall{
				ID:        c.ID,
				Name:      c.Name,
				Summary:   ToolSummary(c.Name, c.Input),
				InputJSON: string(c.Input),
				At:        time.Now(),
			}
			s.tools[c.ID] = len(s.entries)
			s.entries = append(s.entries, Entry{Kind: EntryTool, Tool: call, At: time.Now()})
		}
	}
	s.seq++
}

// handleToolResults fills in the rows the tool calls left open.
func (s *Session) handleToolResults(ln line) {
	var msg apiMessage
	if err := json.Unmarshal(ln.Message, &msg); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range msg.Content {
		if c.Type != "tool_result" {
			continue
		}
		idx, ok := s.tools[c.ToolUseID]
		if !ok || idx >= len(s.entries) || s.entries[idx].Tool == nil {
			continue
		}
		call := s.entries[idx].Tool
		call.Result = c.ResultText()
		call.IsError = c.IsError
		call.Done = true
		if !c.IsError {
			s.recordTouchedLocked(call)
		}
		s.seq++
	}
}

// recordTouchedLocked notes what a finished tool did to the project's files, so
// the editor can reload what it holds. Called with the lock held.
func (s *Session) recordTouchedLocked(call *ToolCall) {
	switch call.Name {
	case "Write", "Edit", "NotebookEdit":
		var in struct {
			FilePath string `json:"file_path"`
			Path     string `json:"path"`
		}
		if err := json.Unmarshal([]byte(call.InputJSON), &in); err != nil {
			return
		}
		p := in.FilePath
		if p == "" {
			p = in.Path
		}
		if p != "" {
			s.touched = append(s.touched, filepath.Clean(p))
		}
	case "Bash":
		// A command can write anything — a texture generator, a git checkout, a
		// whole level regenerated. There is no path to record, only the fact
		// that the editor should look again.
		s.ranCommand = true
	}
}

func (s *Session) handleControlRequest(ln line) {
	var req canUseTool
	if err := json.Unmarshal(ln.Request, &req); err != nil {
		return
	}
	if req.Subtype != "can_use_tool" {
		// Anything else the CLI asks of us is answered as an unhandled success,
		// so it is never left waiting on a subtype we do not implement.
		s.write(controlResponseOut{
			Type:     "control_response",
			Response: controlResponseIn{Subtype: "success", RequestID: ln.RequestID, Response: map[string]interface{}{}},
		})
		return
	}
	// The editor's own tools do not prompt. They are this process's code, on a
	// socket with a token only this process knows, and asking about them would
	// mean a dialog for every question Claude asks the editor.
	if s.opts.AutoAllow != nil && s.opts.AutoAllow(req.ToolName) {
		s.write(controlResponseOut{
			Type: "control_response",
			Response: controlResponseIn{Subtype: "success", RequestID: ln.RequestID,
				Response: permissionAllow{Behavior: "allow", UpdatedInput: req.Input}},
		})
		return
	}
	p := &Permission{
		RequestID:   ln.RequestID,
		ToolName:    req.ToolName,
		DisplayName: req.DisplayName,
		Description: req.Description,
		InputJSON:   string(req.Input),
		Suggestions: req.Suggestions,
		AlwaysLabel: alwaysLabel(req.Suggestions),
		At:          time.Now(),
	}
	if p.DisplayName == "" {
		p.DisplayName = req.ToolName
	}
	s.mu.Lock()
	s.pending[ln.RequestID] = p
	s.entries = append(s.entries, Entry{Kind: EntryPermission, Perm: p, At: time.Now()})
	s.status = StatusWaiting
	s.seq++
	s.mu.Unlock()
}

// alwaysLabel turns the CLI's permission suggestions into a button label. Only
// the shapes we can describe honestly get one; an unknown suggestion means no
// button, because a button that says something vague about permissions is worse
// than none.
func alwaysLabel(suggestions []json.RawMessage) string {
	for _, raw := range suggestions {
		var s struct {
			Type      string `json:"type"`
			Mode      string `json:"mode"`
			Behavior  string `json:"behavior"`
			ToolName  string `json:"toolName"`
			RuleValue string `json:"ruleValue"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		switch s.Type {
		case "setMode":
			if s.Mode == "acceptEdits" {
				return "Allow all edits"
			}
			return "Switch to " + s.Mode
		case "addRules":
			if s.ToolName != "" {
				return "Always allow " + s.ToolName
			}
			return "Always allow this"
		}
	}
	return ""
}

// handleStreamEvent is the live text. Only text and thinking deltas are used;
// the block starts and stops tell us when a row begins and ends.
func (s *Session) handleStreamEvent(b []byte) {
	var ev struct {
		Event struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
			} `json:"content_block"`
			Delta struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		} `json:"event"`
	}
	if err := json.Unmarshal(b, &ev); err != nil {
		return
	}
	e := ev.Event
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e.Type {
	case "content_block_start":
		kind := EntryAssistant
		if e.ContentBlock.Type == "thinking" {
			kind = EntryThinking
		} else if e.ContentBlock.Type != "text" {
			// A tool_use block streams its arguments in; there is nothing
			// useful to show until it is complete.
			return
		}
		s.liveText = len(s.entries)
		s.entries = append(s.entries, Entry{Kind: kind, Live: true, At: time.Now()})
		s.seq++
	case "content_block_delta":
		if s.liveText < 0 || s.liveText >= len(s.entries) {
			return
		}
		text := e.Delta.Text
		if text == "" {
			text = e.Delta.Thinking
		}
		if text == "" {
			return
		}
		s.entries[s.liveText].Text += text
		s.seq++
	case "content_block_stop":
		s.liveText = -1
	}
}

// dropLiveLocked removes the rows the deltas built. The finished message that
// follows carries the same text, so keeping both would show everything twice.
func (s *Session) dropLiveLocked() {
	keep := s.entries[:0]
	for _, e := range s.entries {
		if e.Live {
			continue
		}
		keep = append(keep, e)
	}
	if len(keep) != len(s.entries) {
		// Tool rows are found by index, so the map has to move with them.
		s.entries = keep
		s.tools = map[string]int{}
		for i, e := range s.entries {
			if e.Kind == EntryTool && e.Tool != nil {
				s.tools[e.Tool.ID] = i
			}
		}
	}
	s.liveText = -1
}

func (s *Session) handleResult(ln line) {
	s.mu.Lock()
	s.dropLiveLocked()
	if ln.SessionID != "" {
		s.sessionID = ln.SessionID
	}
	s.cost = ln.TotalCostUSD
	if n := ln.Usage.ContextTokens(); n > 0 {
		s.tokens = n
	}
	if s.status != StatusExited {
		s.status = StatusIdle
	}
	s.seq++
	s.mu.Unlock()

	if ln.IsError {
		switch ln.Subtype {
		case "error_during_execution":
			// What an interrupt looks like from here. Not an error worth
			// colouring red.
			s.notice("Stopped.")
		default:
			msg := ln.Result
			if msg == "" {
				msg = ln.Subtype
			}
			s.notice("Turn ended: " + msg)
		}
	}
}

// ToolSummary is the one line a tool call shows before it is expanded. Exported
// so the panel and its tests agree on what a call looks like.
func ToolSummary(name string, input json.RawMessage) string {
	var in struct {
		FilePath    string `json:"file_path"`
		Path        string `json:"path"`
		Command     string `json:"command"`
		Description string `json:"description"`
		Pattern     string `json:"pattern"`
		URL         string `json:"url"`
		Prompt      string `json:"prompt"`
		Query       string `json:"query"`
	}
	_ = json.Unmarshal(input, &in)
	switch {
	case in.FilePath != "":
		return filepath.Base(in.FilePath)
	case in.Path != "":
		return in.Path
	case in.Command != "":
		return firstLine(in.Command)
	case in.Pattern != "":
		return in.Pattern
	case in.URL != "":
		return in.URL
	case in.Query != "":
		return in.Query
	case in.Description != "":
		return in.Description
	case in.Prompt != "":
		return firstLine(in.Prompt)
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return strings.TrimSpace(s)
}
