package editor

// The Claude Code panel, drawn for real with no window and no GL in each state a
// conversation can be in, and the file routing behind it run against a scratch
// project.
//
// Drawing it is the important half. The transcript is a pile of nested children
// and style pushes whose balance depends on which kind of entry is in the list —
// and imgui answers an unbalanced stack with an assert that aborts the process,
// on whichever frame the offending entry first appears. A permission prompt that
// crashed the editor the first time Claude asked for one is exactly the failure
// this prevents.

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/claude"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

func withClaudePanel(t *testing.T) {
	t.Helper()
	prev := claudeState
	prevReload := claudeReload
	t.Cleanup(func() {
		claudeState = prev
		claudeReload = prevReload
	})
	claudeState.expanded = map[string]bool{}
	claudeState.sess = nil
	claudeState.binErr = nil
	claudeState.input = ""
	claudeReload.levelChanged = false
	claudeReload.conflicts = nil
}

// drawClaudeFrame runs one imgui frame with the panel's body in it.
func drawClaudeFrame(snap claude.Snapshot) {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 900})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()
	imgui.Begin("claude")
	claudeCodeCounter = 0
	claudeDrawHeader(snap)
	claudeDrawTranscript(snap)
	claudeDrawFooter(snap)
	claudeDrawSessionPicker()
	imgui.End()
	imgui.Render()
}

// claudeTestTranscript is one of every kind of row, including the awkward ones:
// a tool still running, a tool that failed, an unanswered permission, and text
// still streaming.
func claudeTestTranscript() []claude.Entry {
	now := time.Now()
	return []claude.Entry{
		{Kind: claude.EntryUser, Text: "add a warning light over the hangar door", At: now},
		{Kind: claude.EntryThinking, Text: "The hangar door is a sector ceiling…", At: now},
		{Kind: claude.EntryAssistant, At: now, Text: "" +
			"I'll add it in two steps.\n\n" +
			"## Plan\n" +
			"- Place a **point light** above the door\n" +
			"- Drive it from `hangar.flang`\n\n" +
			"1. First the light\n" +
			"2. Then the script\n\n" +
			"```yaml\nlights:\n  - id: warn_light\n    color: [1, 0.2, 0.2]\n```\n"},
		{Kind: claude.EntryTool, At: now, Tool: &claude.ToolCall{
			ID: "t1", Name: "Read", Summary: "level1.yaml",
			InputJSON: `{"file_path":"/p/resources/levels/level1.yaml"}`,
			Result:    "chunks: []", Done: true,
		}},
		{Kind: claude.EntryTool, At: now, Tool: &claude.ToolCall{
			ID: "t2", Name: "Edit", Summary: "hangar.flang",
			InputJSON: `{"file_path":"/p/resources/scripts/hangar.flang","old_string":"on :start:","new_string":"on :start:\n  light_on warn_light"}`,
		}},
		{Kind: claude.EntryTool, At: now, Tool: &claude.ToolCall{
			ID: "t3", Name: "Bash", Summary: "python3 tools/gen.py",
			InputJSON: `{"command":"python3 tools/gen.py"}`,
			Result:    "Traceback…", IsError: true, Done: true,
		}},
		{Kind: claude.EntryTool, At: now, Tool: &claude.ToolCall{
			ID: "t4", Name: "mcp__foundry__editor_state", Done: true,
			InputJSON: `{}`, Result: "Level: levels/level1.yaml",
		}},
		{Kind: claude.EntryPermission, At: now, Perm: &claude.Permission{
			RequestID: "r1", ToolName: "Write", DisplayName: "Write",
			Description: "level1.yaml", InputJSON: `{"file_path":"/p/level1.yaml","content":"x"}`,
			AlwaysLabel: "Allow all edits",
		}},
		{Kind: claude.EntryPermission, At: now, Perm: &claude.Permission{
			RequestID: "r0", ToolName: "Bash", DisplayName: "Bash",
			Answered: true, Decision: claude.DecisionDeny,
		}},
		{Kind: claude.EntryAssistant, Text: "Writing the scr", Live: true, At: now},
		{Kind: claude.EntryNotice, Text: "Stopped.", At: now},
	}
}

func TestClaudePanelDrawsInEveryState(t *testing.T) {
	withClaudePanel(t)
	states := []struct {
		name string
		snap claude.Snapshot
		prep func()
	}{
		{name: "empty", snap: claude.Snapshot{Status: claude.StatusIdle}},
		{name: "no CLI", snap: claude.Snapshot{}, prep: func() {
			claudeState.binErr = &claude.ErrNotFound{Tried: []string{"/a", "/b"}}
		}},
		{name: "working", snap: claude.Snapshot{
			Status: claude.StatusWorking, Entries: claudeTestTranscript(),
			Model: "claude-opus-5", Cost: 1.25, Tokens: 42000,
		}},
		{name: "waiting", snap: claude.Snapshot{
			Status: claude.StatusWaiting, Entries: claudeTestTranscript(),
		}},
		{name: "exited", snap: claude.Snapshot{Status: claude.StatusExited}},
		{name: "expanded tools", snap: claude.Snapshot{
			Status: claude.StatusIdle, Entries: claudeTestTranscript(),
		}, prep: func() {
			// Every collapsible thing open at once, so the bodies are drawn
			// rather than skipped.
			for _, k := range []string{"toolt1", "toolt2", "toolt3", "toolt4", "think1"} {
				claudeState.expanded[k] = true
			}
		}},
		{name: "warnings", snap: claude.Snapshot{Status: claude.StatusIdle}, prep: func() {
			claudeReload.levelChanged = true
			claudeReload.conflicts = []string{"scripts/hangar.flang"}
		}},
		{name: "editor context", snap: claude.Snapshot{
			Status: claude.StatusIdle, Entries: claudeTestTranscript(),
		}, prep: func() {
			Editor.SavePath = "/p/resources/levels/level1.yaml"
			Editor.level = &world.Level{Chunks: []world.Chunk{{
				ID: "hangar", Brushes: []world.Brush{{ID: "door_frame"}},
			}}}
			Editor.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}}
		}},
		{name: "context switched off", snap: claude.Snapshot{Status: claude.StatusIdle}, prep: func() {
			Editor.SavePath = "/p/resources/levels/level1.yaml"
			claudeState.noContext = true
		}},
		{name: "session picker", snap: claude.Snapshot{Status: claude.StatusIdle}, prep: func() {
			claudeShowSessions = true
			claudeSessionList = []claude.SessionInfo{
				{ID: "abc", Title: "make the door open", Modified: time.Now().Add(-2 * time.Hour)},
				{ID: "def", Title: "", Modified: time.Now().Add(-100 * time.Hour)},
			}
		}},
	}
	for _, s := range states {
		t.Run(s.name, func(t *testing.T) {
			withClaudePanel(t)
			defer saveEditorState()()
			t.Cleanup(func() { claudeShowSessions = false })
			if s.prep != nil {
				s.prep()
			}
			withImgui(func() { drawClaudeFrame(s.snap) })
		})
	}
}

func TestClaudeMarkdownBlocks(t *testing.T) {
	blocks := claudeParseMarkdown("" +
		"Intro line\nsecond line\n\n" +
		"## A heading\n" +
		"- a bullet\n" +
		"  - a nested one\n" +
		"1. a step\n" +
		"```go\nfmt.Println(1)\n```\n" +
		"3D models are not a list\n")
	var kinds []mdKind
	for _, b := range blocks {
		kinds = append(kinds, b.Kind)
	}
	want := []mdKind{mdText, mdHeading, mdBullet, mdBullet, mdNumbered, mdCode, mdText}
	if len(kinds) != len(want) {
		t.Fatalf("blocks = %+v, want %v kinds", blocks, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("block %d is kind %v, want %v (%q)", i, kinds[i], want[i], blocks[i].Text)
		}
	}
	if blocks[0].Text != "Intro line\nsecond line" {
		t.Errorf("paragraph = %q, want the lines joined", blocks[0].Text)
	}
	if blocks[1].Level != 2 || blocks[1].Text != "A heading" {
		t.Errorf("heading = %+v", blocks[1])
	}
	if blocks[3].Level != 1 {
		t.Errorf("nested bullet level = %d, want 1", blocks[3].Level)
	}
	if blocks[5].Lang != "go" || blocks[5].Text != "fmt.Println(1)" {
		t.Errorf("code = %+v", blocks[5])
	}
}

func TestClaudeMarkdownHandlesHalfStreamedCode(t *testing.T) {
	// What a fenced block looks like while it is still arriving.
	blocks := claudeParseMarkdown("Here:\n```yaml\nlights:\n")
	if len(blocks) != 2 || blocks[1].Kind != mdCode {
		t.Fatalf("blocks = %+v, want the unterminated fence drawn as code", blocks)
	}
	if blocks[1].Text != "lights:" {
		t.Errorf("code = %q", blocks[1].Text)
	}
}

func TestClaudeEmphasisStripped(t *testing.T) {
	if got := claudeStripEmphasis("a **bold** word"); got != "a bold word" {
		t.Errorf("got %q", got)
	}
	// Names in this project are full of underscores and must survive.
	if got := claudeStripEmphasis("bake_exclude and uv_scroll"); got != "bake_exclude and uv_scroll" {
		t.Errorf("got %q, want snake_case left alone", got)
	}
}

// withClaudeProject is a scratch project with resources/, for the routing tests.
func withClaudeProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	res := fileutil.ResourcesDir(root)
	for _, d := range []string{"scripts", "models", "entities", "levels", "textures"} {
		if err := os.MkdirAll(filepath.Join(res, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prevProj, prevRes, prevSave, prevLevel := fileutil.ProjectPath, fileutil.ResourcesPath, Editor.SavePath, Editor.level
	t.Cleanup(func() {
		fileutil.ProjectPath, fileutil.ResourcesPath = prevProj, prevRes
		Editor.SavePath, Editor.level = prevSave, prevLevel
	})
	fileutil.ProjectPath, fileutil.ResourcesPath = root, res
	return root
}

func TestClaudeRoutesTheOpenLevelToABannerRatherThanAReload(t *testing.T) {
	withClaudePanel(t)
	root := withClaudeProject(t)
	Editor.SavePath = filepath.Join(fileutil.ResourcesPath, "levels", "level1.yaml")

	claudeApplyFileChange(Editor.SavePath)
	if !claudeReload.levelChanged {
		t.Error("a rewrite of the open level should raise the banner")
	}
	// Reloading a level throws away its undo history, so it must never happen
	// on its own.
	if EditorLevelLoading() {
		t.Error("the level was reloaded without being asked for")
	}
	_ = root
}

func TestClaudeKeepsUnsavedScriptEditsOverAReload(t *testing.T) {
	withClaudePanel(t)
	withClaudeProject(t)
	prevDocs := scriptDocs
	t.Cleanup(func() { scriptDocs = prevDocs })

	path := filepath.Join(fileutil.ResourcesPath, "scripts", "hangar.flang")
	if err := os.WriteFile(path, []byte("on :start:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A tab with edits of its own on that very file.
	scriptDocs = []*scriptDoc{{Path: "hangar.flang", Text: "mine", saved: "theirs"}}

	claudeApplyFileChange(path)
	if len(claudeReload.conflicts) != 1 || !strings.Contains(claudeReload.conflicts[0], "hangar.flang") {
		t.Errorf("conflicts = %v, want the file named", claudeReload.conflicts)
	}
	if scriptDocs[0].Text != "mine" {
		t.Error("the unsaved buffer was overwritten by the reload")
	}

	// With no unsaved edits, the same change goes through without asking.
	claudeReload.conflicts = nil
	scriptDocs[0].saved = scriptDocs[0].Text
	claudeApplyFileChange(path)
	if len(claudeReload.conflicts) != 0 {
		t.Errorf("a clean buffer should reload silently, got %v", claudeReload.conflicts)
	}
}

// A texture Claude writes has to become a name the editor knows, or it is
// invisible in every picker until the editor is restarted — the gap this
// routing was added to close.
func TestClaudeRegistersATextureItWrote(t *testing.T) {
	withClaudePanel(t)
	withClaudeProject(t)
	prevNames, prevUploads := gfx.TextureNames, gfx.TextureUploadsEnabled
	gfx.TextureNames = map[string]string{}
	gfx.TextureUploadsEnabled = false
	t.Cleanup(func() { gfx.TextureNames, gfx.TextureUploadsEnabled = prevNames, prevUploads })

	path := filepath.Join(fileutil.TexturesDir(), "rust.png")
	writeClaudeTestPNG(t, path)

	claudeApplyFileChange(path)
	if _, ok := gfx.TextureNames["rust"]; !ok {
		t.Error("the texture Claude wrote is still not a name any picker offers")
	}

	// A PNG anywhere else is a screenshot or a document's image, and is
	// nothing to do with the texture pickers.
	other := filepath.Join(fileutil.ResourcesPath, "levels", "shot.png")
	writeClaudeTestPNG(t, other)
	claudeApplyFileChange(other)
	if _, ok := gfx.TextureNames["shot"]; ok {
		t.Error("a PNG outside textures/ was registered as a texture")
	}
}

func writeClaudeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSelectionNoteNamesWhatIsSelected(t *testing.T) {
	withClaudePanel(t)
	defer saveEditorState()()
	lvl := &world.Level{Chunks: []world.Chunk{{
		ID: "hangar", Brushes: []world.Brush{{ID: "door_frame"}, {ID: "floor"}},
	}}}
	Editor.level = lvl
	Editor.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}}

	note := claudeSelectionNote()
	if !strings.Contains(note, "door_frame") || !strings.Contains(note, "hangar") {
		t.Errorf("note = %q, want the brush and its chunk", note)
	}
	// A stale selection must not panic or invent a name.
	Editor.SelectedBrushes = []brushSel{{chunkIdx: 9, brushIdx: 9}}
	if got := claudeSelectionNote(); got != "" {
		t.Errorf("note for a stale selection = %q, want nothing", got)
	}
}

func TestClaudeSelectByIDFindsABrushAndReportsAMiss(t *testing.T) {
	withClaudePanel(t)
	defer saveEditorState()()
	Editor.level = &world.Level{Chunks: []world.Chunk{{
		ID: "hangar", Brushes: []world.Brush{{ID: "door_frame"}},
	}}}
	Editor.SelectedBrushes = nil

	if got := claudeSelectByID("door_frame"); !strings.Contains(got, "door_frame") {
		t.Errorf("select said %q", got)
	}
	if len(Editor.SelectedBrushes) != 1 || Editor.SelectedBrushes[0].brushIdx != 0 {
		t.Errorf("selection = %+v, want the brush selected", Editor.SelectedBrushes)
	}
	if got := claudeSelectByID("nope"); !strings.Contains(got, "No brush") {
		t.Errorf("missing brush said %q, want a plain answer rather than a silent failure", got)
	}
}

func TestClaudeToolInputSummaryReadsAsAnEdit(t *testing.T) {
	got := claudeToolInputSummary("Edit", `{"file_path":"/p/a.flang","old_string":"a","new_string":"b"}`)
	if !strings.Contains(got, "-a") || !strings.Contains(got, "+b") {
		t.Errorf("edit summary = %q, want a diff", got)
	}
	if got := claudeToolInputSummary("Bash", `{"command":"ls -la"}`); got != "ls -la" {
		t.Errorf("bash summary = %q", got)
	}
	// Argument order must be stable, or the row rewrites itself every frame.
	args := `{"b":2,"a":1,"c":3}`
	if claudeToolInputSummary("Other", args) != claudeToolInputSummary("Other", args) {
		t.Error("summaries of the same input differ between calls")
	}
}

func TestClaudeAutoAllowIsOnlyTheEditorsOwnTools(t *testing.T) {
	if !claudeAutoAllow("mcp__foundry__editor_state") {
		t.Error("the editor's own tool should not prompt")
	}
	for _, name := range []string{"Bash", "Write", "mcp__other__thing", "mcp__foundry", ""} {
		if claudeAutoAllow(name) {
			t.Errorf("%q must still ask", name)
		}
	}
}

func TestClaudePrimerNamesTheProject(t *testing.T) {
	withClaudeProject(t)
	primer := claudePrimer()
	for _, want := range []string{fileutil.ProjectPath, "SCRIPTING.md", "docs/guide", ".flang"} {
		if !strings.Contains(primer, want) {
			t.Errorf("the primer does not mention %q", want)
		}
	}
}

func TestClaudeRefreshesACleanScriptTabFromDisk(t *testing.T) {
	withClaudePanel(t)
	withClaudeProject(t)
	prevDocs := scriptDocs
	t.Cleanup(func() { scriptDocs = prevDocs })

	path := filepath.Join(fileutil.ResourcesPath, "scripts", "door.flang")
	if err := os.WriteFile(path, []byte("on :start:\n  say \"old\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptDocs = []*scriptDoc{{Path: "door.flang", Text: "on :start:\n  say \"old\"\n", saved: "on :start:\n  say \"old\"\n"}}

	// Claude rewrites it while the tab sits there clean.
	if err := os.WriteFile(path, []byte("on :start:\n  say \"new\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeApplyFileChange(path)

	if !strings.Contains(scriptDocs[0].Text, "new") {
		t.Errorf("the tab still shows %q — it would write the old text back over Claude's", scriptDocs[0].Text)
	}
	if scriptDocs[0].Dirty() {
		t.Error("the refreshed tab reads as unsaved, so the save chip would lie")
	}
}

func TestClaudeLeavesADirtyScriptTabAlone(t *testing.T) {
	withClaudePanel(t)
	withClaudeProject(t)
	prevDocs := scriptDocs
	t.Cleanup(func() { scriptDocs = prevDocs })

	path := filepath.Join(fileutil.ResourcesPath, "scripts", "door.flang")
	if err := os.WriteFile(path, []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptDocs = []*scriptDoc{{Path: "door.flang", Text: "mine", saved: "was"}}
	claudeApplyFileChange(path)

	if scriptDocs[0].Text != "mine" {
		t.Errorf("unsaved edits were replaced by %q", scriptDocs[0].Text)
	}
	if len(claudeReload.conflicts) == 0 {
		t.Error("the conflict was not reported, so the change would pass unnoticed")
	}
}

func TestClaudeDrawsOnlyTheTailOfALongConversation(t *testing.T) {
	withClaudePanel(t)
	// Every row is laid out whether or not it is visible, so a long conversation
	// has to be cut off somewhere.
	var entries []claude.Entry
	for i := 0; i < claudeTailRows*3; i++ {
		entries = append(entries, claude.Entry{Kind: claude.EntryAssistant, Text: "line", At: time.Now()})
	}
	snap := claude.Snapshot{Status: claude.StatusIdle, Entries: entries}

	if got := claudeState.shown(); got != claudeTailRows {
		t.Errorf("shown() = %d, want the default tail %d", got, claudeTailRows)
	}
	withImgui(func() { drawClaudeFrame(snap) })

	// "Show earlier" raises it, and a new conversation puts it back.
	claudeState.showMore = 500
	if claudeState.shown() <= claudeTailRows {
		t.Error("showing earlier messages did not raise the limit")
	}
	withImgui(func() { drawClaudeFrame(snap) })
}

func TestClaudeComposeAttachesTheEditorsContext(t *testing.T) {
	withClaudePanel(t)
	defer saveEditorState()()
	Editor.SavePath = "/p/resources/levels/level1.yaml"
	Editor.level = &world.Level{Chunks: []world.Chunk{{
		ID: "hangar", Brushes: []world.Brush{{ID: "door_frame"}},
	}}}
	Editor.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}}

	got := claudeCompose("make this a door")
	if !strings.Contains(got, "door_frame") || !strings.Contains(got, "level1.yaml") {
		t.Errorf("composed = %q, want the level and the selection", got)
	}
	// The instruction is last, so it is the thing read most recently.
	if !strings.HasSuffix(got, "make this a door") {
		t.Errorf("composed = %q, want the message at the end", got)
	}

	claudeState.noContext = true
	if got := claudeCompose("a general question"); got != "a general question" {
		t.Errorf("with context off, composed = %q, want the message alone", got)
	}

	// Nothing open: nothing to attach, and no stray blank lines.
	claudeState.noContext = false
	Editor.SavePath, Editor.level, Editor.SelectedBrushes = "", nil, nil
	if got := claudeCompose("hello"); got != "hello" {
		t.Errorf("with nothing open, composed = %q", got)
	}
}

// A play session is a live thing the author is inside of. Claude finishing a
// file must not restart the scripts under them — see claudeDeferredChanges.
func TestClaudeHoldsChangesUntilPlayEnds(t *testing.T) {
	withClaudePanel(t)
	withClaudeProject(t)
	prevDocs, prevActive, prevDeferred := scriptDocs, Editor.Active, claudePlayDeferred
	t.Cleanup(func() {
		scriptDocs, Editor.Active, claudePlayDeferred = prevDocs, prevActive, prevDeferred
	})
	claudePlayDeferred = claudeDeferredChanges{}

	path := filepath.Join(fileutil.ResourcesPath, "scripts", "door.flang")
	old := "on :start:\n  say \"old\"\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptDocs = []*scriptDoc{{Path: "door.flang", Text: old, saved: old}}

	// Play is running, and Claude rewrites the script the player is running.
	Editor.Active = false
	if err := os.WriteFile(path, []byte("on :start:\n  say \"new\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	answer := claudeReloadPath(path)
	if strings.Contains(scriptDocs[0].Text, "new") {
		t.Error("the change was applied mid-play — the script runtime has just been reset under the player")
	}
	if claudePlayDeferCount() != 1 {
		t.Errorf("deferred count = %d, want the change parked", claudePlayDeferCount())
	}
	if !strings.Contains(answer, "play") {
		t.Errorf("the tool answered %q — Claude has no way to know the reload is only waiting", answer)
	}
	// Asked twice for the same file, it is still one change to make.
	claudeReloadPath(path)
	if claudePlayDeferCount() != 1 {
		t.Errorf("deferred count = %d after a repeat, want no duplicate", claudePlayDeferCount())
	}

	// Leaving play is what EditorToggle does, and then the change lands.
	Editor.Active = true
	ClaudeApplyDeferredFileChanges()
	if !strings.Contains(scriptDocs[0].Text, "new") {
		t.Errorf("the tab still shows %q — the held change was dropped rather than deferred", scriptDocs[0].Text)
	}
	if claudePlayDeferCount() != 0 {
		t.Errorf("deferred count = %d after leaving play, want the queue emptied", claudePlayDeferCount())
	}
}
