package claude

// sessions.go — the conversations the CLI has already stored, so the panel can
// offer to resume one.
//
// Claude Code keeps every session as a .jsonl under
// ~/.claude/projects/<slug>/<uuid>.jsonl, where the slug is the project's path
// with every character that is not a letter or a digit turned into a dash. That
// is not a documented layout and could move; everything here treats a missing or
// surprising store as "no sessions to offer" rather than as an error, because
// the panel works perfectly well without the list — it is a convenience, and the
// CLI's own --resume picker remains the authority.
//
// Only the head of each file is read. A long conversation is megabytes and the
// only thing wanted from it is the first thing the user said.

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionInfo is one stored conversation.
type SessionInfo struct {
	ID       string
	Title    string
	Modified time.Time
}

// titleScanBytes is how much of a session file is read looking for its first
// real message. The opening lines are queue records and editor context; a
// hundred kilobytes is far more than enough to get past them.
const titleScanBytes = 128 << 10

// ProjectDir is where the CLI keeps this project's sessions.
func ProjectDir(projectPath string) string {
	home, err := os.UserHomeDir()
	if err != nil || projectPath == "" {
		return ""
	}
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		abs = projectPath
	}
	return filepath.Join(home, ".claude", "projects", ProjectSlug(abs))
}

// ProjectSlug is the CLI's name for a directory: every character that is not a
// letter or a digit becomes a dash, which on a Unix path turns the leading slash
// into a leading dash too.
func ProjectSlug(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// ListSessions is the project's stored conversations, most recently used first.
// limit bounds how many are returned; 0 means all of them.
func ListSessions(projectPath string, limit int) []SessionInfo {
	dir := ProjectDir(projectPath)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type candidate struct {
		path string
		id   string
		mod  time.Time
	}
	var cands []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		cands = append(cands, candidate{
			path: filepath.Join(dir, e.Name()),
			id:   strings.TrimSuffix(e.Name(), ".jsonl"),
			mod:  info.ModTime(),
		})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	if limit > 0 && len(cands) > limit {
		cands = cands[:limit]
	}
	// Titles are read only for the ones that will be shown: opening every file
	// in a store with hundreds of sessions is what would make this slow.
	out := make([]SessionInfo, 0, len(cands))
	for _, c := range cands {
		out = append(out, SessionInfo{ID: c.id, Title: sessionTitle(c.path), Modified: c.mod})
	}
	return out
}

// sessionTitle is the first thing the user actually said in a session.
func sessionTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, titleScanBytes))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var rec struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "user" || rec.IsSidechain {
			continue
		}
		if t := firstUserText(rec.Message.Content); t != "" {
			return t
		}
	}
	return ""
}

// firstUserText pulls readable text out of a stored user message, skipping the
// wrappers that are not something a person typed: the editor's file context, the
// harness's reminders, and the transcript of a slash command.
func firstUserText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return cleanTitle(s)
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "text" {
			if t := cleanTitle(b.Text); t != "" {
				return t
			}
		}
	}
	return ""
}

func cleanTitle(s string) string {
	// Strip the tagged blocks that wrap a real message, then judge what is left.
	for {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "<") {
			break
		}
		end := strings.Index(s, ">")
		if end < 0 {
			return ""
		}
		tag := s[1:end]
		if i := strings.Index(tag, " "); i >= 0 {
			tag = tag[:i]
		}
		closing := "</" + tag + ">"
		if j := strings.Index(s, closing); j >= 0 {
			s = s[j+len(closing):]
			continue
		}
		return ""
	}
	if s == "" || strings.HasPrefix(s, "Caveat:") {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 90
	if len(s) > max {
		s = strings.TrimSpace(s[:max]) + "…"
	}
	return s
}
