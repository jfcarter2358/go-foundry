package editor

// editor_claude_tools.go — what Claude can ask the editor to do.
//
// The MCP server runs on its own goroutine, and everything interesting about the
// editor — the level, the selection, the GL context — belongs to the main
// thread. So no tool here touches editor state directly. Each one queues a
// closure and waits for the frame loop to run it, which is the same bargain the
// rest of the editor's asynchronous work strikes, just in the other direction.
//
// The tools are deliberately few, and each one answers a question that is
// genuinely expensive to answer from the files:
//
//	editor_state   what is open and selected right now
//	lint_scripts   the level-aware checks, against the level actually loaded
//	viewport       a screenshot, so a change can be looked at rather than assumed
//	reload         take a file the editor is holding from disk again
//	select         put a named object in the selection, so the user can see it
//
// Everything else — reading a level, writing a script, listing the textures — is
// a file, and Claude already has excellent tools for files.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"foundry/internal/claude"
	"foundry/internal/fileutil"
	"foundry/internal/world"
)

// claudeToolWait bounds how long a tool waits for the main thread. A frame is
// 16 ms; anything approaching this means the editor is blocked on something
// else — a level load, a light bake — and Claude is better off told so than left
// hanging.
//
// A variable, and atomic, only so a test can shorten it: the waiting happens on
// the tool server's goroutine while the test sets it from its own.
var claudeToolWait atomic.Int64

func init() { claudeToolWait.Store(int64(20 * time.Second)) }

var (
	claudeMCP *claude.MCPServer
	// claudeMainQueue is work waiting for the frame loop.
	claudeMainQueue struct {
		sync.Mutex
		fns []func()
	}
)

// claudeMCPConfig starts the tool server if it is not already up and returns the
// path of the config file naming it.
//
// The config carries the bearer token, so it is written with owner-only
// permissions into the user's state directory rather than into the project,
// where it would be one `git add .` away from being published.
func claudeMCPConfig() (string, error) {
	if claudeMCP == nil {
		srv, err := claude.StartMCP(claudeTools())
		if err != nil {
			return "", err
		}
		claudeMCP = srv
	}
	data, err := claudeMCP.ConfigJSON()
	if err != nil {
		return "", err
	}
	path := fileutil.StatePath("claude-mcp.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ClaudeStopTools shuts the tool server down. Called with EndClaudeSession as
// the editor exits.
func ClaudeStopTools() {
	if claudeMCP != nil {
		claudeMCP.Close()
		claudeMCP = nil
	}
	// The config names a port that is now closed and carries a token that is now
	// meaningless. Leaving it on disk would only confuse the next run.
	_ = os.Remove(fileutil.StatePath("claude-mcp.json"))
}

// ClaudeRunQueuedTools runs the tool work waiting for the main thread. Called
// once a frame from the editor's loop, before drawing.
func ClaudeRunQueuedTools() {
	claudeMainQueue.Lock()
	fns := claudeMainQueue.fns
	claudeMainQueue.fns = nil
	claudeMainQueue.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// onMainThread runs fn on the frame loop and waits for its answer.
func onMainThread[T any](fn func() T) (T, error) {
	done := make(chan T, 1)
	claudeMainQueue.Lock()
	claudeMainQueue.fns = append(claudeMainQueue.fns, func() { done <- fn() })
	claudeMainQueue.Unlock()
	select {
	case v := <-done:
		return v, nil
	case <-time.After(time.Duration(claudeToolWait.Load())):
		var zero T
		return zero, errors.New("the editor did not answer in time — it may be loading a level or baking lighting")
	}
}

// claudeTools is the tool set handed to the server: the ones that look at the
// running editor, and the reference lookups in editor_claude_reference.go.
func claudeTools() []claude.Tool {
	tools := append(claudeEditorTools(), claudePlayTools()...)
	tools = append(tools, claudeBiomeTools()...)
	return append(tools, claudeReferenceTools()...)
}

// claudeEditorTools are the ones that read or change the editor itself, and so
// run on the main thread.
func claudeEditorTools() []claude.Tool {
	return []claude.Tool{
		{
			Name: "editor_state",
			Description: "What the Foundry editor is showing right now: the open level, the " +
				"current chunk, what is selected, which editors are open and whether they have " +
				"unsaved changes. Call this before acting on a request that says \"this\" or " +
				"\"the selected…\".",
			Run: func(json.RawMessage) (claude.ToolResult, error) {
				text, err := onMainThread(claudeEditorStateText)
				return claude.ToolResult{Text: text}, err
			},
		},
		{
			Name: "viewport",
			Description: "A screenshot of the editor's 3D viewport as it is right now. Use it to " +
				"check what something actually looks like after you change it, and to answer " +
				"questions about what is on screen. Move the editor camera first if you need a " +
				"different angle — ask the user, this tool does not move it.",
			Run: func(json.RawMessage) (claude.ToolResult, error) {
				png, err := claudeRequestShot()
				if err != nil {
					return claude.ToolResult{}, err
				}
				return claude.ToolResult{Image: png, ImageMIME: "image/png"}, nil
			},
		},
		{
			Name: "lint_scripts",
			Description: "Check the project's .flang scripts against the level that is actually " +
				"loaded: parse errors, and names, animations or textures the level does not have. " +
				"This catches what reading a script cannot — a script is only correct with respect " +
				"to a level.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "A single script to check, relative to resources/scripts. Omit to check all of them.",
					},
				},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(args, &a)
				text, err := onMainThread(func() string { return claudeLintText(a.Path) })
				return claude.ToolResult{Text: text}, err
			},
		},
		{
			Name: "reload",
			Description: "Make the editor re-read something you changed on disk: a texture, a " +
				"script, a model, a material, an entity type, or the open level. The editor scans " +
				"textures/ only when it starts, so a texture you have just written is invisible in " +
				"the pickers and in the level until this is called for it — name the file, or the " +
				"textures directory to take the lot. Only needed if the editor has not already " +
				"picked the change up.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type": "string",
						"description": "The file to reload, relative to the project or absolute. " +
							"A directory reloads everything in it that the editor knows how to reload.",
					},
				},
				"required": []any{"path"},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(args, &a); err != nil || a.Path == "" {
					return claude.ToolResult{}, errors.New("a path is required")
				}
				text, err := onMainThread(func() string { return claudeReloadPath(a.Path) })
				return claude.ToolResult{Text: text}, err
			},
		},
		{
			Name: "context_volume",
			Description: "Everything inside a context volume: a region the author placed and named " +
				"around a place in the level — a room, a corridor, a courtyard. Answers with the " +
				"region's bounds and every brush, entity, light, sprite, sound and trigger in it, " +
				"with positions and the textures in use. When the user writes @name, this is what " +
				"they are pointing at; call it before changing anything in that place. With no " +
				"name, lists the context volumes this level has.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The volume's id, with or without the leading @. Omit to list them all.",
					},
					"look": map[string]any{
						"type": "boolean",
						"description": "Also point the editor's camera at the region, so the next " +
							"viewport screenshot shows it. Use it when the user asks you to look at a place.",
					},
				},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					Name string `json:"name"`
					Look bool   `json:"look"`
				}
				_ = json.Unmarshal(args, &a)
				text, err := onMainThread(func() string {
					s := claudeContextVolumeText(a.Name)
					if a.Look {
						s += "\n" + claudeLookAtContextVolume(a.Name)
					}
					return s
				})
				return claude.ToolResult{Text: text}, err
			},
		},
		{
			Name: "select_object",
			Description: "Select a brush by id in the editor, so the user can see what you are " +
				"talking about. Use it when you refer to a specific piece of geometry.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "The brush's id, as the level file gives it."},
				},
				"required": []any{"id"},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
					return claude.ToolResult{}, errors.New("an id is required")
				}
				text, err := onMainThread(func() string { return claudeSelectByID(a.ID) })
				return claude.ToolResult{Text: text}, err
			},
		},
	}
}

func claudeAbs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	// A path relative to the project, or one relative to resources/ — both are
	// things a person or a model would write.
	if abs := filepath.Join(fileutil.ProjectPath, p); fileExists(abs) {
		return abs
	}
	return fileutil.ResolveAssetPath(p)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// claudeEditorStateText is the editor's state as prose. Runs on the main thread.
func claudeEditorStateText() string {
	var b strings.Builder
	if Editor.SavePath == "" {
		b.WriteString("No level is open.\n")
	} else {
		fmt.Fprintf(&b, "Level: %s\n", fileutil.RelAssetPath(Editor.SavePath))
	}
	if lvl := Editor.level; lvl != nil {
		fmt.Fprintf(&b, "Chunks: %d", len(lvl.Chunks))
		if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
			fmt.Fprintf(&b, " (editing %q)", lvl.Chunks[Editor.ActiveChunkIdx].ID)
		}
		fmt.Fprintf(&b, "; entities: %d, volumes: %d\n", len(lvl.Entities), len(lvl.Volumes))
	}
	if !Editor.Active {
		b.WriteString("A play session is running. The editor is holding the level as it was when Play " +
			"was pressed, so files you write now are NOT reloaded until the author leaves play — " +
			"reloading mid-session would restart every script under them. Write the change anyway; " +
			"it lands when they come back.\n")
	}
	if sel := claudeSelectionNote(); sel != "" {
		b.WriteString(sel + "\n")
	} else {
		b.WriteString("Nothing is selected.\n")
	}
	if names := unsavedEditorNames(); len(names) > 0 {
		fmt.Fprintf(&b, "Unsaved changes in: %s. These are NOT on disk — what you read is the saved version.\n",
			strings.Join(names, ", "))
	}
	if ScriptEditorOpen() && activeScript != nil {
		fmt.Fprintf(&b, "Script editor is open on %s.\n", activeScript.Name())
	}
	if modelDoc != nil {
		fmt.Fprintf(&b, "Model editor is open on %s.\n", modelDoc.ID)
	}
	return b.String()
}

// claudeLintText runs the level-aware script checks. Runs on the main thread,
// because the checks read the loaded level.
func claudeLintText(only string) string {
	lvl := Editor.level
	if lvl == nil {
		return "No level is open, so the level-aware checks cannot run. Open a level first."
	}
	var b strings.Builder
	issues := 0
	for _, d := range scriptDocs {
		if only != "" && !strings.EqualFold(d.Path, only) && !strings.EqualFold(d.Name(), only) {
			continue
		}
		d.Reparse(0, lvl)
		for _, e := range d.Diags {
			fmt.Fprintf(&b, "%s:%d: %s\n", d.Name(), e.Pos.Line, e.Msg)
			issues++
		}
		for _, is := range d.Issues {
			fmt.Fprintf(&b, "%s:%d: %s\n", d.Name(), is.Pos.Line, is.Msg)
			issues++
		}
	}
	if len(scriptDocs) == 0 {
		return "No scripts are open in the editor. Open one, or say which file to check."
	}
	if issues == 0 {
		return "No problems found in the open scripts against the loaded level."
	}
	return b.String()
}

// claudeContextVolumeText describes one context volume, or lists them. Runs on
// the main thread, because it reads the loaded level.
func claudeContextVolumeText(name string) string {
	lvl := Editor.level
	if lvl == nil {
		return "No level is open."
	}
	if strings.TrimSpace(name) == "" {
		return claudeContextVolumeList(lvl)
	}
	v := world.FindContextVolume(lvl, name)
	if v == nil {
		// A name that is a volume but not a context volume is the likely
		// mistake, and worth saying apart from "no such thing".
		if plain := world.FindVolume(lvl, strings.TrimPrefix(strings.TrimSpace(name), "@")); plain != nil {
			return fmt.Sprintf("@%s is a volume, but not a context volume — it has no context block, "+
				"so there is nothing to describe. The author can tick \"Describe this region to Claude\" "+
				"on it in the editor's volume panel.", plain.ID)
		}
		return fmt.Sprintf("This level has no context volume called %q.\n\n%s", name, claudeContextVolumeList(lvl))
	}
	return world.GatherContext(lvl, v).Describe()
}

// claudeLookAtContextVolume points the editor's camera at a region. Runs on the
// main thread, with the rest of the tool.
//
// The selection is deliberately left alone. Framing is a camera move and undoes
// itself the moment anyone drags the view; taking the selection as well would
// throw away what the person at the keyboard was in the middle of.
func claudeLookAtContextVolume(name string) string {
	v := world.FindContextVolume(Editor.level, name)
	if v == nil {
		return ""
	}
	min, max, ok := v.WorldAABB(Editor.level)
	if !ok {
		return "The region has no extents, so there is nothing to point the camera at."
	}
	editorFrameBounds(min, max)
	editorSetStatus("Claude is looking at %s", v.ID)
	return "The editor's camera is now looking at this region — take a viewport screenshot to see it."
}

// claudeContextVolumeList is what the level has to point at.
func claudeContextVolumeList(lvl *world.Level) string {
	vols := world.ContextVolumes(lvl)
	if len(vols) == 0 {
		return "This level has no context volumes. The author places one in the editor — a volume with " +
			"\"Describe this region to Claude\" ticked — around a place worth naming, and then refers to it as @its_id."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s in this level:\n", plural(len(vols), "context volume"))
	for _, v := range vols {
		fmt.Fprintf(&b, "  @%s", v.ID)
		if v.Context.Note != "" {
			fmt.Fprintf(&b, " — %s", v.Context.Note)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// claudeSelectByID puts a brush in the selection. Runs on the main thread.
func claudeSelectByID(id string) string {
	lvl := Editor.level
	if lvl == nil {
		return "No level is open."
	}
	for ci := range lvl.Chunks {
		for bi := range lvl.Chunks[ci].Brushes {
			if lvl.Chunks[ci].Brushes[bi].ID != id {
				continue
			}
			Editor.SelectedBrushes = []brushSel{{chunkIdx: ci, brushIdx: bi}}
			Editor.ActiveChunkIdx = ci
			editorSetStatus("Claude selected %s", id)
			return fmt.Sprintf("Selected %s, in chunk %s.", id, lvl.Chunks[ci].ID)
		}
	}
	return fmt.Sprintf("No brush called %q in this level.", id)
}
