package editor

// editor_claude_docs.go — the authoring guide, handed over rather than hunted
// for.
//
// docs/guide/ is the real documentation for everything the editor does, and
// until now the primer did no more than mention it. That left Claude to find it:
// a glob, a directory listing, a guess at which chapter covers frames, and often
// enough a decision to work from the level files instead. All of it avoidable —
// the chapter list is a fixed, cheap thing to say up front.
//
// So the index goes in the docs tool's own description, which is part of the
// prompt and costs nothing per call, and the tool hands over a chapter by name or
// by number. The index is read from the guide's README rather than repeated here,
// because a list of chapters kept in Go would be a second copy to forget to
// update.
//
// The guide is looked for in two places. A checkout has it in the project
// directory, which is also the repository; an installed editor opened on a game
// somewhere else has it beside the executable (internal/pack stages it there).
// Neither is guaranteed, and the tool says so plainly rather than pretending the
// guide is empty.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"foundry/internal/claude"
	"foundry/internal/fileutil"
)

// claudeGuideRel is where the guide sits under a root that has one.
var claudeGuideRel = filepath.Join("docs", "guide")

// claudeGuideDir is the guide's directory, or "" when this install has none.
//
// Resolved once: it cannot move while the editor is up, and every tool call and
// every primer would otherwise stat the same two paths again.
var claudeGuideDir = sync.OnceValue(func() string {
	for _, root := range []string{fileutil.ProjectPath, fileutil.ExecutablePath} {
		if root == "" {
			continue
		}
		dir := filepath.Join(root, claudeGuideRel)
		if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
			return dir
		}
	}
	return ""
})

// claudeDocChapter is one file of the guide.
type claudeDocChapter struct {
	File    string // "18-scripting.md"
	Title   string // "Scripting"
	Summary string // the line the README's table gives it
}

// claudeGuideChapters is the guide's contents, in the order the README lists
// them: the table in docs/guide/README.md is the index the guide maintains for
// itself, and rows of "| [Title](file.md) | what it covers |" are what it is
// made of. Chapters the table does not mention are appended, so a new one is
// still reachable before anyone remembers to list it.
var claudeGuideChapters = sync.OnceValue(func() []claudeDocChapter {
	dir := claudeGuideDir()
	if dir == "" {
		return nil
	}
	var out []claudeDocChapter
	listed := map[string]bool{}
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err == nil {
		for _, line := range strings.Split(string(readme), "\n") {
			ch, ok := claudeParseIndexRow(line)
			if !ok || listed[ch.File] {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, ch.File)); err != nil {
				continue
			}
			listed[ch.File] = true
			out = append(out, ch)
		}
	}
	for _, name := range fileutil.ScanFilesByExt(dir, ".md") {
		if name == "README.md" || listed[name] {
			continue
		}
		listed[name] = true
		out = append(out, claudeDocChapter{File: name, Title: claudeDocTitle(filepath.Join(dir, name))})
	}
	return out
})

// claudeParseIndexRow reads one row of the README's table of contents.
func claudeParseIndexRow(line string) (claudeDocChapter, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "| [") {
		return claudeDocChapter{}, false
	}
	cells := strings.Split(strings.Trim(line, "|"), "|")
	if len(cells) < 2 {
		return claudeDocChapter{}, false
	}
	link := strings.TrimSpace(cells[0])
	open, end := strings.Index(link, "]("), strings.LastIndex(link, ")")
	if !strings.HasPrefix(link, "[") || open < 0 || end < open {
		return claudeDocChapter{}, false
	}
	file := link[open+2 : end]
	// Only the guide's own chapters: a row pointing out of the folder is not one.
	if !strings.HasSuffix(file, ".md") || strings.ContainsAny(file, "/:") {
		return claudeDocChapter{}, false
	}
	return claudeDocChapter{
		File:    file,
		Title:   strings.TrimSpace(link[1:open]),
		Summary: strings.TrimSpace(cells[1]),
	}, true
}

// claudeDocTitle is a chapter's own heading, for one the README has not listed.
func claudeDocTitle(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return strings.TrimSuffix(filepath.Base(path), ".md")
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return strings.TrimSuffix(filepath.Base(path), ".md")
}

// claudeDocsTool serves the guide.
func claudeDocsTool() claude.Tool {
	return claude.Tool{
		Name: "docs",
		Description: "The Foundry authoring guide: how each of the engine's subsystems is " +
			"actually used, with the fields and the workflow for each one. Read the chapter for " +
			"a subsystem before building in it — it is quicker than inferring the model from the " +
			"level files, and it is where the traps are written down.\n\n" +
			claudeDocsChapterList() +
			"\nWith no arguments it lists the chapters and what each one covers.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"chapter": map[string]any{
					"type": "string",
					"description": "Which chapter to read: its number (\"18\"), its file name " +
						"(\"18-scripting.md\") or a word from its title (\"scripting\").",
				},
				"search": map[string]any{
					"type":        "string",
					"description": "Instead of a chapter, every line of the guide containing this.",
				},
			},
		},
		Run: func(args json.RawMessage) (claude.ToolResult, error) {
			var a struct {
				Chapter string `json:"chapter"`
				Search  string `json:"search"`
			}
			_ = json.Unmarshal(args, &a)
			return claude.ToolResult{Text: claudeDocsText(a.Chapter, a.Search)}, nil
		},
	}
}

// claudeDocsChapterList is the index as it goes into the tool's description:
// every chapter with its one-line summary, so that choosing one needs no call at
// all. Empty when this install has no guide, which keeps the description honest.
func claudeDocsChapterList() string {
	chapters := claudeGuideChapters()
	if len(chapters) == 0 {
		return "This install has no copy of the guide, so there is nothing to read.\n"
	}
	var b strings.Builder
	b.WriteString("Chapters:\n")
	for _, c := range chapters {
		b.WriteString("  " + c.File)
		if c.Summary != "" {
			b.WriteString(" — " + c.Summary)
		} else if c.Title != "" {
			b.WriteString(" — " + c.Title)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// claudeDocsText answers a call: a chapter, a search, or the index.
func claudeDocsText(chapter, search string) string {
	dir := claudeGuideDir()
	if dir == "" {
		return "This Foundry install has no copy of docs/guide. Work from the project's own " +
			"files and the script_api and level_schema tools."
	}
	switch {
	case search != "":
		return claudeDocsSearchText(dir, search)
	case chapter != "":
		c, ok := claudeFindChapter(chapter)
		if !ok {
			return fmt.Sprintf("No chapter matches %q.\n\n%s", chapter, claudeDocsChapterList())
		}
		data, err := os.ReadFile(filepath.Join(dir, c.File))
		if err != nil {
			return fmt.Sprintf("%s could not be read: %v", c.File, err)
		}
		return "# " + c.File + "\n\n" + string(data)
	default:
		return claudeDocsChapterList()
	}
}

// claudeFindChapter resolves what a caller wrote to a chapter.
//
// A number, a file name and a word from the title are all tried, because all
// three are how the guide refers to itself: the README links files, the chapters
// link each other by title, and a number is the fastest thing to type.
func claudeFindChapter(q string) (claudeDocChapter, bool) {
	return claudeFindChapterIn(claudeGuideChapters(), q)
}

// claudeFindChapterIn is claudeFindChapter against a given list, which is what
// makes the matching testable without a guide on disk.
func claudeFindChapterIn(chapters []claudeDocChapter, q string) (claudeDocChapter, bool) {
	q = strings.ToLower(strings.TrimSpace(q))
	q = strings.TrimSuffix(q, ".md")
	if q == "" {
		return claudeDocChapter{}, false
	}
	for _, c := range chapters {
		if strings.ToLower(strings.TrimSuffix(c.File, ".md")) == q {
			return c, true
		}
	}
	// A bare number: "18", or "8" for 08-sky-and-fog.md.
	for _, c := range chapters {
		num, _, ok := strings.Cut(c.File, "-")
		if ok && strings.TrimLeft(num, "0") == strings.TrimLeft(q, "0") {
			return c, true
		}
	}
	for _, c := range chapters {
		if strings.Contains(strings.ToLower(c.Title), q) ||
			strings.Contains(strings.ToLower(c.File), q) {
			return c, true
		}
	}
	return claudeDocChapter{}, false
}

// claudeDocsSearchMax is how many hits a search comes back with. Enough to see
// which chapter the answer is in, which is what a search of documentation is
// for; the chapter itself is one more call.
const claudeDocsSearchMax = 40

// claudeDocsSearchText is every line of the guide containing q, with the chapter
// and line number it came from.
func claudeDocsSearchText(dir, q string) string {
	needle := strings.ToLower(q)
	var b strings.Builder
	hits, cut := 0, false
	for _, c := range claudeGuideChapters() {
		data, err := os.ReadFile(filepath.Join(dir, c.File))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			if hits >= claudeDocsSearchMax {
				cut = true
				break
			}
			hits++
			fmt.Fprintf(&b, "%s:%d: %s\n", c.File, i+1, strings.TrimSpace(line))
		}
		if cut {
			break
		}
	}
	if hits == 0 {
		return fmt.Sprintf("Nothing in the guide contains %q.", q)
	}
	if cut {
		fmt.Fprintf(&b, "\n(stopped at %d hits — search for something narrower)\n", claudeDocsSearchMax)
	}
	return b.String()
}
