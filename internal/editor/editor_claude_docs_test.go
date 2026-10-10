package editor

// The guide's index, as the docs tool reads it. The README's table is the index
// the guide keeps for itself, so parsing it is the one thing here that can break
// silently when someone reformats a row.

import "testing"

func TestClaudeParseIndexRow(t *testing.T) {
	cases := []struct {
		line        string
		ok          bool
		file, title string
		summary     string
	}{
		{
			line:    "| [Scripting](18-scripting.md) | flang: events, messages, properties, parameters |",
			ok:      true,
			file:    "18-scripting.md",
			title:   "Scripting",
			summary: "flang: events, messages, properties, parameters",
		},
		{
			line:    "| [Materials](28-materials.md) | `.fmat`: the effects a surface wears |",
			ok:      true,
			file:    "28-materials.md",
			title:   "Materials",
			summary: "`.fmat`: the effects a surface wears",
		},
		// The table's own header, and the prose around it.
		{line: "| | |", ok: false},
		{line: "| --- | --- |", ok: false},
		{line: "Start the editor:", ok: false},
		// A link out of the guide is not a chapter of it.
		{line: "| [Shipping](../../README.md) | not a chapter |", ok: false},
		{line: "| [Home](https://example.com) | not a chapter |", ok: false},
	}
	for _, c := range cases {
		got, ok := claudeParseIndexRow(c.line)
		if ok != c.ok {
			t.Errorf("%q: ok = %v, want %v", c.line, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.File != c.file || got.Title != c.title || got.Summary != c.summary {
			t.Errorf("%q: got %+v", c.line, got)
		}
	}
}

// A chapter is found the three ways the guide refers to itself.
func TestClaudeFindChapter(t *testing.T) {
	chapters := []claudeDocChapter{
		{File: "08-sky-and-fog.md", Title: "Sky and fog"},
		{File: "12-vehicles-and-frames.md", Title: "Vehicles and frames"},
		{File: "18-scripting.md", Title: "Scripting"},
	}
	cases := map[string]string{
		"18":              "18-scripting.md",   // the number
		"18-scripting":    "18-scripting.md",   // the file, with or without .md
		"18-scripting.md": "18-scripting.md",   //
		"8":               "08-sky-and-fog.md", // a number without its zero
		"08":              "08-sky-and-fog.md", //
		"scripting":       "18-scripting.md",   // a word from the title
		"Vehicles":        "12-vehicles-and-frames.md",
		"frames":          "12-vehicles-and-frames.md",
		"fog":             "08-sky-and-fog.md",
	}
	for q, want := range cases {
		got, ok := claudeFindChapterIn(chapters, q)
		if !ok || got.File != want {
			t.Errorf("%q: got %q (%v), want %q", q, got.File, ok, want)
		}
	}
	for _, q := range []string{"", "   ", "99", "nothing-like-this"} {
		if got, ok := claudeFindChapterIn(chapters, q); ok {
			t.Errorf("%q matched %q", q, got.File)
		}
	}
}
