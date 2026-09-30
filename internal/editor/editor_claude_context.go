package editor

// editor_claude_context.go — what Claude is told about this project, and about
// what the editor is looking at right now.
//
// Two different things, sent two different ways:
//
//   - The primer goes in --append-system-prompt and describes the project's
//     shape: what a .flang is, where levels live, what generates textures. It is
//     the same every turn, so the CLI's prompt cache keeps it cheap, and it is
//     deliberately short — the guide under docs/ is the real documentation and
//     Claude can read it when a question needs it.
//   - The context note is prepended to a message and says what is selected right
//     now. "Make this brush a door" is how anyone actually talks about the thing
//     under the cursor, and without the note that sentence has no referent.
//
// The note is not the editor's live state — a tool call is, and that is what
// mcp__foundry__editor_state is for. This is the cheap version that costs no
// round trip and is right often enough to be worth having on every message.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"foundry/internal/claude"
	"foundry/internal/fileutil"
	"foundry/internal/world"
)

// claudePrimer is the system prompt appended to every session.
func claudePrimer() string {
	var b strings.Builder
	b.WriteString(`You are working inside the Foundry game engine's editor, on the project at `)
	b.WriteString(fileutil.ProjectPath)
	b.WriteString(".\n\n")
	b.WriteString(`Content lives under resources/: levels/ (.yaml), scripts/ (.flang), models/ (.fmdl),
skeletons/ (.fskl — the armature and animation clips a model links, shared between
models), textures/ (.png and .ftex), entities/ (.yaml type definitions),
particles/ (.fpar), ui/ (.fui screens), sounds/. game.yaml is the project's manifest.

Reference:
  docs/guide/        the authoring guide, one chapter per subsystem. The docs tool
                     below serves it and lists its chapters — use that rather than
                     looking for the files, which are not always in the project.
                     Read the chapter for a subsystem BEFORE building in it: it has
                     the fields, the workflow and the traps, and it is quicker than
                     inferring the model from the level files.
  resources/levels/  existing levels are the best examples of level YAML
  SCRIPTING.md       the .flang language in prose, where the project has it; the
                     script_api tool is the same material and cannot drift

The editor is open on this project as you work, and answers questions about
itself and about the engine through the Foundry tools. Prefer them to inference:

  - script_api    every flang verb's signature and every object's properties,
                  from the engine's own tables. Check a verb here before writing
                  it; SCRIPTING.md is prose beside the same tables and can drift.
  - level_schema  every key a level file or entity type may contain. A key the
                  schema does not have is dropped SILENTLY on load, so a level
                  that loads is not evidence that the key was right.
  - list_assets   the names of this project's textures, materials, models,
                  scripts, entity types, effects, sounds and levels.
  - editor_state  the open level, the active chunk and the selection.
  - context_volume  what is inside a named region of the level. When the user
                  writes @something, that is a context volume: a region the
                  author placed around a room or an area and named. Call this
                  before changing anything in one — it gives the bounds and
                  every brush, entity, light and trigger inside, with positions
                  and textures. With no name it lists the ones this level has.
  - viewport      a screenshot of the 3D view — look at what you changed.
  - play          PLAY the level: press Play and drive the player from a short
                  script (walk, look, shoot, use), then read what the player can
                  see and a screenshot from their eyes. This is how you check
                  that something WORKS rather than that it is spelled right —
                  that the door opens, the enemy notices, the room is lit, the
                  jump is makeable. Reach for it after a change a player would
                  notice. play_stop puts the editor back.
  - lint_scripts  the level-aware checks, against the level actually loaded.
  - select_object selects a brush by id, so the user can see what you mean.
  - reload        makes the editor re-read a file, if it has not already. Name
                  the textures directory instead of a file to take every
                  texture at once.
  - docs          a chapter of the authoring guide, by number or by name, or a
                  search across all of them.
  - biome_schema, biome_list, biome_get, biome_save
                  terrain biomes (resources/biomes/<name>.fbio): what grows on
                  painted terrain and the ground texture under it. Read the
                  schema, start from biome_get, and write with biome_save — it
                  validates before writing and regrows the terrain painted with
                  it. Ground textures come from cmd/genbiomeground
                  (go run ./cmd/genbiomeground <name>) or any tiling PNG.

Rules that matter here:
  - Assets are named, not pathed. A level writes "texture: sf_panel", and the
    loader resolves it to resources/textures/sf_panel.png. Use list_assets rather
    than guessing at a name or writing a path.
  - Copy the shape of an existing file rather than inventing fields.
  - A texture is art; what a surface DOES with it — water, a scroll, no
    lighting, a second texture blended over it — is a material (.fmat under
    materials/). A face, sprite, particle layer or sky names one with
    "material:", and it is overlaid on that object's own fields rather than
    replacing them. Prefer naming an existing material to repeating its
    settings on every object.
  - Textures and models are generated by Python tools under tools/ rather than
    drawn by hand. To make a new texture, write or extend a generator there.
  - After you change a file the editor holds, it reloads it — textures
    included, whether you wrote one yourself or a generator you ran did. Do not
    ask the user to restart the editor.
  - The user may have unsaved changes the files do not show; editor_state says
    which editors do.`)
	return b.String()
}

// claudeContextNote is a line or two about what the editor is showing, prepended
// to the user's message. Empty when there is nothing worth saying.
func claudeContextNote() string {
	var parts []string
	if Editor.SavePath != "" {
		parts = append(parts, "the level "+fileutil.RelAssetPath(Editor.SavePath))
	}
	if sel := claudeSelectionNote(); sel != "" {
		parts = append(parts, sel)
	}
	if len(parts) == 0 {
		return ""
	}
	return "[Foundry editor: open on " + strings.Join(parts, ", ") + "]"
}

// claudeMentionedContextVolumes finds the @names in a message that are context
// volumes of the open level.
//
// A mention is a promise: the user wrote @mess_hall meaning "look at that
// place", and the message on its own is only a hint that Claude might call the
// tool. Naming them explicitly turns "it usually looks" into "it looks".
func claudeMentionedContextVolumes(text string) []string {
	lvl := Editor.level
	if lvl == nil || !strings.Contains(text, "@") {
		return nil
	}
	var found []string
	seen := map[string]bool{}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		// An id is letters, digits and underscores; everything else ends one.
		return !(r == '@' || r == '_' || r >= '0' && r <= '9' ||
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) {
		if !strings.HasPrefix(word, "@") {
			continue
		}
		id := strings.TrimPrefix(word, "@")
		if id == "" || seen[id] {
			continue
		}
		if v := world.FindContextVolume(lvl, id); v != nil {
			seen[id] = true
			found = append(found, v.ID)
		}
	}
	return found
}

// claudeContextVolumeHint is the line appended to a message that mentions one.
func claudeContextVolumeHint(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	at := make([]string, len(ids))
	for i, id := range ids {
		at[i] = "@" + id
	}
	return fmt.Sprintf("[%s %s a context volume in this level — call context_volume for what is inside %s.]",
		strings.Join(at, " and "), isAre(len(ids)), itThem(len(ids)))
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func itThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// claudeSelectionNote names what is selected, in the terms the level file uses:
// the ids, because an id is what a script or a YAML edit has to name.
func claudeSelectionNote() string {
	lvl := Editor.level
	if lvl == nil {
		return ""
	}
	var ids []string
	for _, s := range Editor.SelectedBrushes {
		if s.chunkIdx < 0 || s.chunkIdx >= len(lvl.Chunks) {
			continue
		}
		ch := &lvl.Chunks[s.chunkIdx]
		if s.brushIdx < 0 || s.brushIdx >= len(ch.Brushes) {
			continue
		}
		if id := ch.Brushes[s.brushIdx].ID; id != "" {
			ids = append(ids, fmt.Sprintf("%s (in chunk %s)", id, ch.ID))
		}
	}
	if len(ids) == 0 {
		return ""
	}
	if len(ids) > 4 {
		return fmt.Sprintf("%d brushes selected, including %s", len(ids), strings.Join(ids[:4], ", "))
	}
	return "selected: " + strings.Join(ids, ", ")
}

// claudeToolInputSummary is a tool's arguments as text worth reading. The whole
// JSON is right for an unfamiliar tool and wrong for the common ones: what
// matters about a Write is the content, and about a Bash the command.
func claudeToolInputSummary(name, inputJSON string) string {
	if inputJSON == "" {
		return ""
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &in); err != nil {
		return inputJSON
	}
	str := func(k string) string {
		s, _ := in[k].(string)
		return s
	}
	switch name {
	case "Write":
		return withPath(str("file_path"), str("content"))
	case "Edit":
		// Drawn as a diff, so it is built like one.
		var b strings.Builder
		if p := str("file_path"); p != "" {
			b.WriteString(filepath.Base(p) + "\n")
		}
		for _, l := range strings.Split(str("old_string"), "\n") {
			b.WriteString("-" + l + "\n")
		}
		for _, l := range strings.Split(str("new_string"), "\n") {
			b.WriteString("+" + l + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	case "Bash":
		return str("command")
	case "Read", "Glob", "Grep":
		return compactJSON(in)
	}
	return compactJSON(in)
}

func withPath(path, body string) string {
	if path == "" {
		return body
	}
	return filepath.Base(path) + "\n" + body
}

// compactJSON is a one-line-per-field rendering, which reads better in a narrow
// panel than the marshalled object.
func compactJSON(in map[string]any) string {
	var lines []string
	for k, v := range in {
		s := fmt.Sprint(v)
		if len(s) > 400 {
			s = s[:400] + "…"
		}
		lines = append(lines, k+": "+s)
	}
	// Map order is random; a tool call that reorders its own arguments every
	// frame is unreadable.
	sortStrings(lines)
	return strings.Join(lines, "\n")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// claudeAutoAllow says which tools never prompt: the editor's own, and nothing
// else. They are this process answering questions about itself.
func claudeAutoAllow(name string) bool {
	return strings.HasPrefix(name, "mcp__foundry__")
}

// claudeStatusLine is the one-line summary the main editor shows when the panel
// is closed but a turn is running.
func claudeStatusLine() string {
	if claudeState.sess == nil {
		return ""
	}
	switch snap := claudeState.sess.Snapshot(); snap.Status {
	case claude.StatusWorking:
		return "Claude is working…"
	case claude.StatusWaiting:
		return "Claude needs permission"
	}
	return ""
}
