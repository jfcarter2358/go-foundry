package editor

// editor_claude_biomes.go — the tools Claude authors terrain biomes with.
//
// A biome is a YAML file (world/foliage_file.go), so Claude could write one
// with its own file tools — and the editor would pick it up (editor_biomes.go).
// What that path cannot do is say, before anything is written, that a species
// names a shape that does not exist or asks for a spacing the placer would
// choke on; a file that does not parse is kept out and the terrain simply
// stays as it was, which from the outside looks like the edit did nothing.
// biome_save validates first and writes only a good file, and answers with
// what changed in the level. The other three are the lookups that make a
// good file likely on the first try: the format, what exists, and any
// biome's current definition as a file.

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"foundry/internal/claude"
	"foundry/internal/fileutil"
	"foundry/internal/world"
)

var biomeNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func claudeBiomeTools() []claude.Tool {
	nameArg := map[string]any{
		"type":        "string",
		"description": "The biome's name: its file name without .fbio, which a terrain palette writes as `preset: <name>`.",
	}
	return []claude.Tool{
		{
			Name: "biome_schema",
			Description: "The terrain biome file format (resources/biomes/<name>.fbio): every key with its " +
				"meaning and range, every plant shape with its variants and size, and how the ecology " +
				"reads the terrain. Read this before writing or changing a biome.",
			Run: func(json.RawMessage) (claude.ToolResult, error) {
				return claude.ToolResult{Text: world.BiomeSchemaText()}, nil
			},
		},
		{
			Name: "biome_list",
			Description: "Every terrain biome this project can paint: name, label, ground texture, " +
				"species, and whether it comes from a file or is built in — plus which biomes the " +
				"open level's terrains paint with.",
			Run: func(json.RawMessage) (claude.ToolResult, error) {
				text, err := onMainThread(claudeBiomeListText)
				return claude.ToolResult{Text: text}, err
			},
		},
		{
			Name: "biome_get",
			Description: "One biome's definition as the YAML of its .fbio file — the file itself if the " +
				"project has one, or the built-in written out as a file would be. Start an edit from this.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": nameArg},
				"required":   []any{"name"},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(args, &a)
				text, isErr := onMainThreadPair(func() (string, bool) { return claudeBiomeGet(a.Name) })
				return claude.ToolResult{Text: text, IsError: isErr}, nil
			},
		},
		{
			Name: "biome_save",
			Description: "Create or change a terrain biome: validates the YAML against the biome format and, " +
				"only if it is valid, writes resources/biomes/<name>.fbio and regrows every terrain in " +
				"the open level painted with it. A new name adds a biome the terrain panel can paint; a " +
				"built-in's name overrides it for this project. Invalid YAML writes nothing and every " +
				"problem is listed. Take a viewport screenshot afterwards to see the result.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": nameArg,
					"yaml": map[string]any{
						"type":        "string",
						"description": "The whole file. See biome_schema; biome_get gives an existing one to start from.",
					},
				},
				"required": []any{"name", "yaml"},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					Name string `json:"name"`
					YAML string `json:"yaml"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return claude.ToolResult{}, err
				}
				text, isErr := onMainThreadPair(func() (string, bool) { return claudeBiomeSave(a.Name, a.YAML) })
				return claude.ToolResult{Text: text, IsError: isErr}, nil
			},
		},
	}
}

// onMainThreadPair is onMainThread for a (text, isError) answer, a timeout
// being an error of its own.
func onMainThreadPair(fn func() (string, bool)) (string, bool) {
	type pair struct {
		s string
		e bool
	}
	p, err := onMainThread(func() pair { s, e := fn(); return pair{s, e} })
	if err != nil {
		return err.Error(), true
	}
	return p.s, p.e
}

func claudeBiomeListText() string {
	var b strings.Builder
	used := map[string][]string{}
	if lvl := Editor.level; lvl != nil {
		for ci := range lvl.Chunks {
			t := lvl.Chunks[ci].Terrain
			if t == nil {
				continue
			}
			for _, bi := range t.Biomes {
				used[bi.Preset] = append(used[bi.Preset], lvl.Chunks[ci].ID)
			}
		}
	}
	for _, p := range world.FoliagePresetsSorted() {
		src := world.BiomeSource(p.Name)
		if src != "built-in" {
			src = fileutil.RelAssetPath(src)
		}
		var shapes []string
		for _, sp := range p.Species {
			k := strings.ReplaceAll(sp.Shape.String(), " ", "_")
			if !containsStr(shapes, k) {
				shapes = append(shapes, k)
			}
		}
		fmt.Fprintf(&b, "%s — %s (%s)\n", p.Name, p.Label, src)
		if p.Doc != "" {
			fmt.Fprintf(&b, "  %s\n", p.Doc)
		}
		ground := p.GroundTexture
		if ground == "" {
			ground = "(the block's own)"
		} else if msg := world.BiomeTextureProblem(p); msg != "" {
			ground += "  [MISSING: " + msg + "]"
		}
		fmt.Fprintf(&b, "  ground: %s\n  grows: %s\n", ground, strings.Join(shapes, ", "))
		if chunks := used[p.Name]; len(chunks) > 0 {
			fmt.Fprintf(&b, "  painted in this level on: %s\n", strings.Join(chunks, ", "))
		}
	}
	if errs := biomeFileErrors(); len(errs) > 0 {
		b.WriteString("\nFiles that do not parse (the biome keeps its last good definition):\n")
		for _, n := range errs {
			fmt.Fprintf(&b, "  %s\n", biomePoll.errors[n])
		}
	}
	return b.String()
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func claudeBiomeGet(name string) (string, bool) {
	name = strings.TrimSuffix(strings.TrimSpace(name), world.BiomeExt)
	if data, err := os.ReadFile(world.BiomePath(name)); err == nil {
		return "# from " + fileutil.RelAssetPath(world.BiomePath(name)) + "\n" + string(data), false
	}
	p := world.FoliagePresetByName(name)
	if p == nil {
		return fmt.Sprintf("There is no biome called %q. biome_list names them all.", name), true
	}
	data, err := world.MarshalBiome(p)
	if err != nil {
		return err.Error(), true
	}
	return "# built in; no file yet — biome_save with this name writes one that overrides it\n" + string(data), false
}

func claudeBiomeSave(name, text string) (string, bool) {
	name = strings.TrimSuffix(strings.TrimSpace(name), world.BiomeExt)
	if !biomeNameRE.MatchString(name) {
		return fmt.Sprintf("%q is not a biome name: lower case letters, digits and _, starting with a letter.", name), true
	}
	p, err := world.ParseBiome(name, []byte(text))
	if err != nil {
		return "Not saved — the biome does not validate:\n" + err.Error(), true
	}
	if len(p.Species) == 0 {
		return "Not saved — a biome with no species grows nothing. Add at least one.", true
	}
	existed := world.BiomeSource(name)
	if err := os.MkdirAll(world.BiomesDir(), 0o755); err != nil {
		return err.Error(), true
	}
	if err := fileutil.WriteFileAtomic(world.BiomePath(name), []byte(text)); err != nil {
		return "Not saved: " + err.Error(), true
	}
	touched, err := reloadBiome(name)
	if err != nil {
		// Parsed a moment ago; anything here is the disk.
		return "Written, but reading it back failed: " + err.Error(), true
	}
	var b strings.Builder
	switch existed {
	case "":
		fmt.Fprintf(&b, "Created biome %q (%s).", name, fileutil.RelAssetPath(world.BiomePath(name)))
	case "built-in":
		fmt.Fprintf(&b, "Wrote %s, which now overrides the built-in %q for this project.", fileutil.RelAssetPath(world.BiomePath(name)), name)
	default:
		fmt.Fprintf(&b, "Updated %s.", fileutil.RelAssetPath(world.BiomePath(name)))
	}
	fmt.Fprintf(&b, " %d species.", len(p.Species))
	if touched > 0 {
		fmt.Fprintf(&b, " %d terrain(s) in the open level paint with it and are regrowing.", touched)
	} else {
		b.WriteString(" No terrain in the open level paints with it yet — add it to a terrain's Biomes palette" +
			" (or `biomes: [{preset: " + name + "}]` in the level file) and paint.")
	}
	if msg := world.BiomeTextureProblem(p); msg != "" {
		b.WriteString("\nWARNING: " + msg + " — the ground keeps the block's texture until that texture exists." +
			" Write a tiling PNG there, or add a recipe to cmd/genbiomeground.")
	}
	return b.String(), false
}
