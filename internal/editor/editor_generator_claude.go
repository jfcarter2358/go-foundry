package editor

// editor_generator_claude.go — what the Claude panel knows about generators.
//
//   - generator_stages: a read-only reference tool (registered in
//     claudeReferenceTools), the stage kinds with their parameters as the
//     schema states them — the same text `foundry gen list -kind` prints,
//     from procgen.KindReference — and the templates. A .fgen Claude writes
//     is decoded strictly, so an invented parameter is an error; this is
//     what saves it from inventing one.
//   - list_assets lists generators/*.fgen (a row in claudeAssetKinds).
//   - claudeApplyFileChange routes a written .fgen here: the Generator
//     editor, when it holds that document unedited, re-reads it; when it
//     holds edits of its own, Claude is told of the conflict.
//
// Running a generator is `foundry gen run <name>`, which Claude can call
// from its shell like the other headless commands; the primer says so.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"foundry/internal/claude"
	"foundry/internal/procgen"
)

// claudeGeneratorTool is the generator_stages reference tool.
func claudeGeneratorTool() claude.Tool {
	return claude.Tool{
		Name: "generator_stages",
		Description: "The procedural generator (.fgen) stage kinds this build has, with every parameter's " +
			"type, default, range and meaning, and the document templates. Generators live in " +
			"resources/generators/<name>.fgen; params not in a kind's schema are refused when the file " +
			"loads. Run one with `foundry gen run <name>` (it splices placements into its `level:`).",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"description": "One stage kind to show in full. Omit for the list of kinds and templates.",
				},
			},
		},
		Run: func(args json.RawMessage) (claude.ToolResult, error) {
			var a struct {
				Kind string `json:"kind"`
			}
			_ = json.Unmarshal(args, &a)
			return claude.ToolResult{Text: claudeGeneratorText(a.Kind)}, nil
		},
	}
}

// claudeGeneratorText is the tool's answer.
func claudeGeneratorText(kind string) string {
	if kind != "" {
		k := procgen.KindByName(kind)
		if k == nil {
			return fmt.Sprintf("No stage kind %q. Call without a kind for the list.", kind)
		}
		return procgen.KindReference(k)
	}
	var b strings.Builder
	b.WriteString("# Generator stage kinds\n")
	for _, k := range procgen.Kinds() {
		fmt.Fprintf(&b, "  %s (%s): %s\n", k.Name, k.Category, k.Doc)
	}
	b.WriteString("\n# Templates (File > New, `foundry gen new <name> -template <t>`)\n")
	for _, t := range procgen.Templates() {
		fmt.Fprintf(&b, "  %s: %s\n", t.Name, t.Doc)
	}
	b.WriteString("\nA document:\n  doc: <note>\n  seed: 1\n  level: <level name>   # optional\n" +
		"  stages:\n    - stage: <kind>\n      id: <unique lower_case id>\n      params: {...}\n" +
		"Placements land in the level as gen.<generator>.<stage>.<id>; a rerun replaces exactly those.\n")
	return b.String()
}

// claudeGeneratorFileChanged handles a .fgen Claude wrote. Reports false when
// the open editor holds edits of its own (a conflict for the caller to note).
func claudeGeneratorFileChanged(abs string) bool {
	if genDoc == nil || genPath == "" || filepath.Clean(genPath) != filepath.Clean(abs) {
		return true // not the open one: nothing holds it
	}
	if generatorEditorDirty() {
		return false
	}
	if err := genLoad(abs); err != nil {
		genStatusf("Claude's change to %s does not load: %v", filepath.Base(abs), err)
		return false
	}
	return true
}
