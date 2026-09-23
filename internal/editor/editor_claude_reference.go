package editor

// editor_claude_reference.go — the engine's own reference tables, as tools.
//
// These are the three things Claude gets wrong when it has only the files to go
// on, and all three are already answered inside this binary:
//
//   - **Which verbs exist.** `flang.BuiltinDocs()` is the table the F1 reference
//     is drawn from, so it is exactly current. SCRIPTING.md is prose written
//     beside it and can drift; a signature invented from prose is a script that
//     does not compile.
//   - **What a level file may contain.** `yamlFieldDocs` is generated from the
//     schema's own comments. Level YAML is decoded strictly in the checker and
//     leniently by the loader — an invented key is dropped in silence — which is
//     the single easiest way to produce a level that loads and does nothing.
//   - **What assets exist, by name.** Names in a level file are names, not paths.
//     A texture named `sf_panel` is `resources/textures/sf_panel.png`, and
//     nothing in the level file says so; listing them is far cheaper and more
//     reliable than having Claude glob the tree and infer the rule.
//
// All three are read-only and cheap, so unlike the tools in
// editor_claude_tools.go they do not need the main thread — none of them touches
// the level, the selection or GL. They are separate for that reason as much as
// for tidiness.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"foundry/internal/audio"
	"foundry/internal/claude"
	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/levelio"
	"foundry/internal/model"
	"foundry/internal/particle"
	"foundry/internal/script"
	"foundry/internal/world"
)

// claudeReferenceTools are the read-only lookups.
func claudeReferenceTools() []claude.Tool {
	return []claude.Tool{
		{
			Name: "script_api",
			Description: "The flang scripting API as the engine itself defines it: every builtin " +
				"function with its signature, and the properties readable and writable on each kind " +
				"of object. Authoritative — check a verb here before writing it, rather than " +
				"copying from documentation.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filter": map[string]any{
						"type":        "string",
						"description": "Only entries whose name or summary contains this. Omit for all of them.",
					},
				},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					Filter string `json:"filter"`
				}
				_ = json.Unmarshal(args, &a)
				return claude.ToolResult{Text: claudeScriptAPIText(a.Filter)}, nil
			},
		},
		{
			Name: "level_schema",
			Description: "The keys a level file, entity type or game.yaml may contain, with what " +
				"each one means, generated from the engine's own schema. A key that is not in here " +
				"is dropped silently when the level loads, so check before inventing one.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filter": map[string]any{
						"type":        "string",
						"description": "Only keys whose name or description contains this — \"light\", \"chunk\", \"bake\". Omit for all of them.",
					},
				},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					Filter string `json:"filter"`
				}
				_ = json.Unmarshal(args, &a)
				return claude.ToolResult{Text: claudeLevelSchemaText(a.Filter)}, nil
			},
		},
		{
			Name: "list_assets",
			Description: "The names of the assets this project has: textures, models, scripts, " +
				"entity types, particle effects, sounds and levels. A level file names assets by " +
				"name, not by path, and these are those names.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind": map[string]any{
						"type": "string",
						"enum": []any{"textures", "models", "scripts", "entities",
							"particles", "sounds", "levels", "all"},
						"description": "Which kind to list. Defaults to all of them.",
					},
					"filter": map[string]any{
						"type":        "string",
						"description": "Only names containing this.",
					},
				},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					Kind   string `json:"kind"`
					Filter string `json:"filter"`
				}
				_ = json.Unmarshal(args, &a)
				return claude.ToolResult{Text: claudeListAssetsText(a.Kind, a.Filter)}, nil
			},
		},
	}
}

// claudeScriptAPIText is the builtins and the per-kind properties.
func claudeScriptAPIText(filter string) string {
	filter = strings.ToLower(filter)
	match := func(parts ...string) bool {
		if filter == "" {
			return true
		}
		for _, p := range parts {
			if strings.Contains(strings.ToLower(p), filter) {
				return true
			}
		}
		return false
	}

	// Unfiltered, this is the whole API and summaries make it ten thousand
	// tokens of reading. Signatures alone are enough to find the verb wanted;
	// asking again with a filter then gives the detail.
	detailed := filter != ""

	var b strings.Builder
	b.WriteString("# flang builtins\n")
	if !detailed {
		b.WriteString("Signatures only. Call again with a filter for what each one does.\n")
	}
	n := 0
	for _, group := range flang.BuiltinGroups() {
		var rows []string
		for _, d := range flang.BuiltinDocsIn(group) {
			if !match(d.Name, d.Sig, d.Summary) {
				continue
			}
			ret := d.Ret
			if ret != "" {
				ret = " -> " + ret
			}
			if !detailed {
				rows = append(rows, fmt.Sprintf("  %s%s", d.Sig, ret))
				continue
			}
			rows = append(rows, fmt.Sprintf("  %s%s — %s", d.Sig, ret, d.Summary))
		}
		if len(rows) == 0 {
			continue
		}
		n += len(rows)
		b.WriteString("\n## " + group + "\n")
		b.WriteString(strings.Join(rows, "\n") + "\n")
	}

	b.WriteString("\n# Properties, by kind\n")
	b.WriteString("Read as @id.prop.name; those marked read-only are reported rather than obeyed if assigned.\n")
	for _, k := range claudeRefKinds() {
		var rows []string
		for _, p := range script.PropertyDocs(k) {
			if !match(p.Name, p.Doc) {
				continue
			}
			access := ""
			if !p.Writable {
				access = " (read-only)"
			}
			if !detailed {
				rows = append(rows, "  "+p.Name+access)
				continue
			}
			doc := p.Doc
			if i := strings.IndexByte(doc, '\n'); i >= 0 {
				doc = doc[:i]
			}
			rows = append(rows, fmt.Sprintf("  %s%s — %s", p.Name, access, doc))
		}
		if len(rows) == 0 {
			continue
		}
		n += len(rows)
		b.WriteString("\n## " + k.String() + "\n")
		b.WriteString(strings.Join(rows, "\n") + "\n")
	}
	if n == 0 {
		return fmt.Sprintf("Nothing in the scripting API matches %q.", filter)
	}
	return b.String()
}

// claudeRefKinds is every kind that has properties, in the order the reference
// lists them.
func claudeRefKinds() []flang.Kind_ {
	return []flang.Kind_{
		flang.RefLevel, flang.RefChunk, flang.RefBrush, flang.RefEntity,
		flang.RefVolume, flang.RefLight, flang.RefSprite, flang.RefEmitter,
		flang.RefSound, flang.RefScreen, flang.RefWidget, flang.RefModel,
		flang.RefWaypoints, flang.RefEffect,
	}
}

// claudeLevelSchemaText is the generated field documentation, filtered.
//
// The keys are "pkg.Type.key"; the type is what a person would have to know to
// use the answer, so it is kept rather than reduced to bare key names.
func claudeLevelSchemaText(filter string) string {
	filter = strings.ToLower(filter)
	keys := make([]string, 0, len(yamlFieldDocs))
	for k := range yamlFieldDocs {
		if filter != "" &&
			!strings.Contains(strings.ToLower(k), filter) &&
			!strings.Contains(strings.ToLower(yamlFieldDocs[k]), filter) {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return fmt.Sprintf("No schema key matches %q.", filter)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "%d keys.\n\n", len(keys))
	for _, k := range keys {
		doc := yamlFieldDocs[k]
		// One line each: the whole table with full paragraphs is a great deal
		// of text, and the first sentence is what decides whether the key is
		// the one being looked for.
		if i := strings.Index(doc, ". "); i > 0 {
			doc = doc[:i+1]
		}
		fmt.Fprintf(&b, "%s: %s\n", k, doc)
	}
	return b.String()
}

// claudeAssetKinds is what list_assets can list, and where each lives.
func claudeAssetKinds() []struct {
	Kind string
	Dir  func() string
	Ext  string
} {
	return []struct {
		Kind string
		Dir  func() string
		Ext  string
	}{
		{"textures", fileutil.TexturesDir, ".png"},
		{"models", func() string { return fileutil.ResolveAssetPath(model.ModelsDirName) }, ".fmdl"},
		{"entities", func() string { return fileutil.ResolveAssetPath(world.EntitiesDirName) }, ".yaml"},
		{"particles", func() string { return fileutil.ResolveAssetPath(particle.ParticlesDirName) }, ".fpar"},
		{"sounds", func() string { return fileutil.ResolveAssetPath(audio.SoundsDirName) }, ".wav"},
		{"levels", levelio.LevelsDir, ".yaml"},
	}
}

// claudeListAssetsText lists asset names, without their extensions, because the
// name is what a level file writes.
func claudeListAssetsText(kind, filter string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	filter = strings.ToLower(filter)
	var b strings.Builder
	total := 0

	list := func(name string, names []string) {
		var kept []string
		for _, n := range names {
			if filter == "" || strings.Contains(strings.ToLower(n), filter) {
				kept = append(kept, n)
			}
		}
		if len(kept) == 0 {
			return
		}
		total += len(kept)
		fmt.Fprintf(&b, "\n%s (%d):\n  %s\n", name, len(kept), strings.Join(kept, ", "))
	}

	for _, k := range claudeAssetKinds() {
		if kind != "" && kind != "all" && kind != k.Kind {
			continue
		}
		names := fileutil.ScanFilesByExt(k.Dir(), k.Ext)
		for i := range names {
			names[i] = strings.TrimSuffix(names[i], k.Ext)
		}
		list(k.Kind, names)
	}
	// Scripts are scanned rather than listed: they nest in folders, and a level
	// names them with the folder included.
	if kind == "" || kind == "all" || kind == "scripts" {
		list("scripts", fileutil.ScanScriptFiles())
	}
	if total == 0 {
		if filter != "" {
			return fmt.Sprintf("No assets match %q.", filter)
		}
		return "This project has no assets of that kind yet."
	}
	return strings.TrimLeft(b.String(), "\n")
}
