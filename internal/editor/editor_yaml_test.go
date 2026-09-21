package editor

import (
	"reflect"
	"strings"
	"testing"

	"foundry/internal/flang"
	"foundry/internal/game"
	"foundry/internal/world"
)

// yamlTestLevel is a small level in the shape the level writer emits.
const yamlTestLevel = `chunks:
- id: hangar
  brushes:
  - id: box
    verts: [0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1]
    faces:
    - indices:
      - 0
      texture: sf_wall
      uv_offset:
        x: 0.5
        "y": 0.25
  - id: floor
    verts: [0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1]
    faces: []
  origin:
    x: 1
- id: yard
  brushes: []
volumes:
- id: door_trigger
  shape: box
scripts:
- scripts/level1/ship.flang
lights:
  global: {brightness: 0.1}
`

// yamlOffsetOf returns the byte offset of the n'th (0-based) occurrence of
// marker in src, plus delta.
func yamlOffsetOf(t *testing.T, src, marker string, n, delta int) int {
	t.Helper()
	at := 0
	for i := 0; ; i++ {
		j := strings.Index(src[at:], marker)
		if j < 0 {
			t.Fatalf("marker %q #%d not found", marker, n)
		}
		if i == n {
			return at + j + delta
		}
		at += j + len(marker)
	}
}

func TestYAMLShapeOf(t *testing.T) {
	cases := []struct {
		line       string
		indent     int
		dashes     []int
		key, value string
		block      bool
	}{
		{"chunks:", 0, nil, "chunks", "", true},
		{"- id: hangar", 0, []int{0}, "id", "hangar", false},
		{"  - - a", 2, []int{2, 4}, "", "", false},
		{`        "y": 0.25`, 8, nil, "y", "0.25", false},
		{"  shape: box # the trigger", 2, nil, "shape", "box", false},
		{"  anchor: &a", 2, nil, "anchor", "&a", true},
		{"  url: http://x", 2, nil, "url", "http://x", false},
		{"  # just a comment: here", 2, nil, "", "", false},
	}
	for _, c := range cases {
		s := yamlShapeOf(c.line)
		if s.Indent != c.indent || !reflect.DeepEqual(s.Dashes, c.dashes) || s.Key != c.key ||
			s.Value != c.value || s.BlockValue() != c.block {
			t.Errorf("%q: got indent %d dashes %v key %q value %q block %v", c.line,
				s.Indent, s.Dashes, s.Key, s.Value, s.BlockValue())
		}
	}
}

func TestYAMLBreadcrumb(t *testing.T) {
	ix := newYAMLLineIndex(yamlTestLevel)
	cases := []struct {
		marker string
		n      int
		want   string
	}{
		{"texture: sf_wall", 0, "chunks[0] hangar › brushes[0] box › faces[0] › texture"},
		{`"y": 0.25`, 0, "chunks[0] hangar › brushes[0] box › faces[0] › uv_offset › y"},
		{"- id: floor", 0, "chunks[0] hangar › brushes[1] floor › id"},
		{"- id: yard", 0, "chunks[1] yard › id"},
		{"x: 1", 0, "chunks[0] hangar › origin › x"},
		{"brushes: []", 0, "chunks[1] yard › brushes"},
		{"shape: box", 0, "volumes[0] door_trigger › shape"},
		{"global:", 0, "lights › global"},
	}
	for _, c := range cases {
		off := yamlOffsetOf(t, yamlTestLevel, c.marker, c.n, len(c.marker)-1)
		if got := yamlBreadcrumb(ix, off); got != c.want {
			t.Errorf("at %q: got %q, want %q", c.marker, got, c.want)
		}
	}
}

func TestYAMLResolve(t *testing.T) {
	path := []yamlPathSeg{{Key: "chunks"}, {Item: true}, {Key: "brushes"}, {Item: true}, {Key: "faces"}}
	got := yamlDeref(yamlResolve(yamlRootType, path))
	if got == nil || got.Kind() != reflect.Slice || got.Elem() != reflect.TypeOf(world.RawBrushFace{}) {
		t.Fatalf("faces resolved to %v", got)
	}
	if yamlResolve(yamlRootType, []yamlPathSeg{{Key: "no_such_key"}}) != nil {
		t.Error("an unknown key should leave the schema")
	}
	// Inline fields are flattened into the struct that embeds them.
	if _, ok := yamlFieldNamed(reflect.TypeOf(world.RawChunk{}), "script"); !ok {
		t.Error("RawChunk should have the inline RawScriptData's script key")
	}
}

// yamlComplAt builds the completion context and rows with the caret at the
// end of marker, after replacing marker's tail with typed.
func yamlComplAt(t *testing.T, src string) (yamlComplCtx, []scriptComplItem) {
	t.Helper()
	cursor := strings.Index(src, "|")
	if cursor < 0 {
		t.Fatal("no caret marker")
	}
	src = src[:cursor] + src[cursor+1:]
	ctx := yamlComplContext(newYAMLLineIndex(src), cursor)
	items := scriptComplFilter(yamlComplItems(ctx, yamlComplSource{
		Observed: func(key string) []string { return yamlObservedValues(src, key, cursor, 50) },
	}), ctx.Prefix)
	return ctx, items
}

func yamlItemTexts(items []scriptComplItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Text
	}
	return out
}

func TestYAMLComplKeys(t *testing.T) {
	// A key inside a chunk: the chunk's fields, minus the ones it has.
	src := strings.Replace(yamlTestLevel, "  origin:\n", "  ro|\n  origin:\n", 1)
	ctx, items := yamlComplAt(t, src)
	if ctx.Kind != yamlComplKey || ctx.Type != reflect.TypeOf(world.RawChunk{}) || ctx.Prefix != "ro" {
		t.Fatalf("ctx = %+v", ctx)
	}
	texts := yamlItemTexts(items)
	if len(texts) == 0 || texts[0] != "rotation" {
		t.Errorf("want rotation first, got %v", texts)
	}
	for _, s := range texts {
		if s == "origin" || s == "brushes" || s == "id" {
			t.Errorf("offered %q, which the chunk already has", s)
		}
	}

	// A key at the top level.
	ctx, items = yamlComplAt(t, yamlTestLevel+"wa|\n")
	if ctx.Kind != yamlComplKey || ctx.Type != yamlRootType {
		t.Fatalf("top-level ctx = %+v", ctx)
	}
	if texts := yamlItemTexts(items); len(texts) == 0 || texts[0] != "waypoint_animations" {
		t.Errorf("top level: %v", texts)
	}

	// A key on a new list item's dash.
	src = strings.Replace(yamlTestLevel, "scripts:\n", "- sh|\nscripts:\n", 1)
	ctx, _ = yamlComplAt(t, src)
	if ctx.Kind != yamlComplKey || ctx.Type != reflect.TypeOf(world.RawVolume{}) {
		t.Fatalf("new volume item ctx = %+v", ctx)
	}
}

func TestYAMLComplValues(t *testing.T) {
	// An enum from the schema's comment.
	src := strings.Replace(yamlTestLevel, "  shape: box", "  shape: sp|", 1)
	ctx, items := yamlComplAt(t, src)
	if ctx.Kind != yamlComplValue || ctx.Key != "shape" {
		t.Fatalf("ctx = %+v", ctx)
	}
	if texts := yamlItemTexts(items); len(texts) == 0 || texts[0] != "sphere" {
		t.Errorf("shape values: %v", texts)
	}

	// A bool.
	src = strings.Replace(yamlTestLevel, "  - id: floor\n", "  - id: floor\n    solid: |\n", 1)
	_, items = yamlComplAt(t, src)
	if texts := yamlItemTexts(items); !reflect.DeepEqual(texts, []string{"true", "false"}) {
		t.Errorf("bool values: %v", texts)
	}

	// A string with no provider: what the file already says for the key.
	src = strings.Replace(yamlTestLevel, "      texture: sf_wall", "      texture: sf_wall\n      blend_texture: sf|", 1)
	src = strings.Replace(src, "  - id: floor\n", "  - id: floor\n    blend_texture: sf_grass\n", 1)
	ctx, items = yamlComplAt(t, src)
	if ctx.Kind != yamlComplValue || ctx.Key != "blend_texture" {
		t.Fatalf("ctx = %+v", ctx)
	}
	if texts := yamlItemTexts(items); !reflect.DeepEqual(texts, []string{"sf_grass"}) {
		t.Errorf("observed values: %v", texts)
	}

	// Nothing inside a comment.
	ctx, _ = yamlComplAt(t, "chunks: # sh|\n")
	if ctx.Kind != yamlComplNone {
		t.Errorf("completion inside a comment: %+v", ctx)
	}
}

func TestYAMLComplAccept(t *testing.T) {
	src := "chunks:\n- id: a\n  ori|\n"
	ctx, items := yamlComplAt(t, src)
	if len(items) == 0 || items[0].Text != "origin" {
		t.Fatalf("items %v", yamlItemTexts(items))
	}
	e := yamlComplAccept(ctx, items[0])
	plain := strings.Replace(src, "|", "", 1)
	got := plain[:e.Start] + e.Text + plain[e.End:]
	// RawChunkOrigin is a small all-number struct: written the flow way.
	if !strings.Contains(got, "  origin: {x: 0, \"y\": 0, z: 0}") {
		t.Errorf("accepting origin gave:\n%s", got)
	}

	// A list-valued key brings its first dash, at the key's own indent.
	ctx, items = yamlComplAt(t, "chunks:\n- id: a\n  bru|\n")
	e = yamlComplAccept(ctx, items[0])
	if e.Text != "brushes:\n  - " {
		t.Errorf("brushes accepted as %q", e.Text)
	}

	// A key that already has its colon is only renamed.
	ctx, items = yamlComplAt(t, "chunks:\n- id: a\n  rot|: 1\n")
	e = yamlComplAccept(ctx, items[0])
	if e.Text != "rotation" {
		t.Errorf("renaming a key gave %q", e.Text)
	}
}

func TestYAMLNeedsQuote(t *testing.T) {
	for s, want := range map[string]bool{
		"y": true, "no": true, "1.5": true, "": true, "- x": true, "a: b": true,
		"sf_wall": false, "scripts/a.flang": false, "x": false,
	} {
		if yamlNeedsQuote(s) != want {
			t.Errorf("yamlNeedsQuote(%q) = %v", s, !want)
		}
	}
}

func TestYAMLHoverUnknownKey(t *testing.T) {
	src := "chunks:\n- id: a\n  colour: red\n"
	ix := newYAMLLineIndex(src)
	info, ok := yamlHoverAt(ix, strings.Index(src, "colour")+1)
	if !ok || !strings.Contains(info.Doc, "ignores") {
		t.Errorf("hover on an unknown key: %+v %v", info, ok)
	}
	info, ok = yamlHoverAt(ix, strings.Index(src, "id")+1)
	if !ok || info.Type != "string" || info.Path == "" {
		t.Errorf("hover on id: %+v %v", info, ok)
	}
}

func TestYAMLHighlight(t *testing.T) {
	classOf := func(chunks []scriptHLChunk, text string) flang.Class {
		for _, c := range chunks {
			if strings.TrimSpace(c.Text) == text {
				return c.Class
			}
		}
		t.Fatalf("no chunk %q in %+v", text, chunks)
		return 0
	}
	line := `  - id: hangar # the ship`
	ch := yamlHighlightLine(line, false)
	if classOf(ch, "-") != yamlClassPunct || classOf(ch, "id") != yamlClassKey ||
		classOf(ch, "hangar") != yamlClassString || classOf(ch, "# the ship") != yamlClassComment {
		t.Errorf("chunks: %+v", ch)
	}
	// Every byte is accounted for, in order.
	var b strings.Builder
	for _, c := range ch {
		b.WriteString(c.Text)
	}
	if b.String() != line {
		t.Errorf("chunks rebuild %q", b.String())
	}

	ch = yamlHighlightLine(`ambient: {r: 0.1, "g": true, b: null}`, false)
	if classOf(ch, "r") != yamlClassKey || classOf(ch, `"g"`) != yamlClassKey ||
		classOf(ch, "0.1") != yamlClassNumber || classOf(ch, "true") != yamlClassConst {
		t.Errorf("flow chunks: %+v", ch)
	}

	src := "note: |\n  - not: a key\n  text\nnext: 1\n"
	ix := newYAMLLineIndex(src)
	if !yamlInBlockScalar(ix, 1) || !yamlInBlockScalar(ix, 2) || yamlInBlockScalar(ix, 3) {
		t.Error("block scalar lines misjudged")
	}
}

func TestYAMLFindReplace(t *testing.T) {
	text := "texture: sf_wall\ntexture: SF_WALL\nblend_texture: sf_wall2\n"
	ms, _, err := yamlFindAll(text, yamlFindOpts{Query: "sf_wall"})
	if err != nil || len(ms) != 3 {
		t.Fatalf("case-insensitive: %v %v", ms, err)
	}
	ms, _, _ = yamlFindAll(text, yamlFindOpts{Query: "sf_wall", Case: true, Word: true})
	if len(ms) != 1 {
		t.Fatalf("case + word: %v", ms)
	}
	if i := yamlFindNext(ms, 100, false); i != 0 {
		t.Errorf("wrap forward: %d", i)
	}

	o := yamlFindOpts{Query: `sf_(\w+)`, Regex: true, Case: true}
	ms, _, _ = yamlFindAll(text, o)
	out, n := yamlReplaceAll(text, ms, o, "cn_$1")
	if n != 2 || out != "texture: cn_wall\ntexture: SF_WALL\nblend_texture: cn_wall2\n" {
		t.Errorf("regex replace all: %d %q", n, out)
	}
	if r := yamlReplacement(text, ms[1], o, "x_${1}"); r != "x_wall2" {
		t.Errorf("one replacement: %q", r)
	}
	if _, _, err := yamlFindAll(text, yamlFindOpts{Query: "(", Regex: true}); err == nil {
		t.Error("a bad pattern should report an error")
	}
	// The literal path and the regexp path agree on every option.
	sample := "Foo foo_bar foo FOO xfoo foo\n"
	for _, o := range []yamlFindOpts{
		{Query: "foo"}, {Query: "foo", Case: true}, {Query: "foo", Word: true},
		{Query: "foo", Word: true, Case: true}, {Query: "o f"},
	} {
		lit, _, _ := yamlFindAll(sample, o)
		re, _ := o.compile()
		var want []yamlFindMatch
		for _, m := range re.FindAllStringIndex(sample, -1) {
			want = append(want, yamlFindMatch{m[0], m[1]})
		}
		if !reflect.DeepEqual(lit, want) {
			t.Errorf("%+v: literal %v, regexp %v", o, lit, want)
		}
	}
	// A plain query with regex characters in it finds them literally.
	ms, _, _ = yamlFindAll("a.b axb", yamlFindOpts{Query: "a.b"})
	if len(ms) != 1 {
		t.Errorf("literal dot: %v", ms)
	}
}

func TestYAMLUndo(t *testing.T) {
	var u yamlUndo
	text := "abc"
	step := func(next string, now uint32, sealed bool) {
		u.Record(text, next, now, sealed)
		text = next
	}
	step("abcd", 10, false)
	step("abcde", 20, false) // merges: typing on
	step("abcdeX", 3000, false)
	if len(u.done) != 2 {
		t.Fatalf("history %+v", u.done)
	}
	var ok bool
	text, _, ok = u.Undo(text)
	if !ok || text != "abcde" {
		t.Fatalf("undo 1: %q", text)
	}
	text, _, _ = u.Undo(text)
	if text != "abc" {
		t.Fatalf("undo 2: %q", text)
	}
	text, caret, _ := u.Redo(text)
	if text != "abcde" || caret != 5 {
		t.Fatalf("redo: %q %d", text, caret)
	}
	// A new edit drops what was undone.
	step("abcdeZ", 4000, true)
	if u.CanRedo() {
		t.Error("redo survived a new edit")
	}
	// Backspacing merges into one step.
	step("abcde", 5000, false)
	step("abcd", 5100, false)
	step("abc", 5200, false)
	text, _, _ = u.Undo(text)
	if text != "abcdeZ" {
		t.Errorf("backspace run undone to %q", text)
	}
	// An undo against text it does not describe refuses rather than corrupts.
	if _, _, ok := u.Undo("something else entirely"); ok {
		t.Error("undo applied to the wrong text")
	}
}

func TestYAMLEditOps(t *testing.T) {
	text := "a:\nb: 1\nc: 2\n"
	sel := scriptSel{Start: 3, End: 12} // lines b and c
	out, nsel, ok := yamlIndent(text, sel)
	if !ok || out != "a:\n  b: 1\n  c: 2\n" {
		t.Fatalf("indent: %q", out)
	}
	back, _, ok := yamlDedent(out, nsel)
	if !ok || back != text {
		t.Fatalf("dedent: %q", back)
	}
	// Tab with no selection pads to the next stop.
	out, nsel, _ = yamlIndent("x", scriptSel{1, 1})
	if out != "x " || nsel.Start != 2 {
		t.Errorf("tab at column 1: %q %v", out, nsel)
	}

	c, _, _ := yamlToggleComment("  a: 1\n    b: 2\n", scriptSel{0, 15})
	if c != "  # a: 1\n  #   b: 2\n" {
		t.Errorf("comment: %q", c)
	}
	u, _, _ := yamlToggleComment(c, scriptSel{0, len(c) - 1})
	if u != "  a: 1\n    b: 2\n" {
		t.Errorf("uncomment: %q", u)
	}

	for prev, want := range map[string]string{
		"chunks:":       "  ",
		"- id: hangar":  "  ",
		"  - id: box":   "    ",
		"    brushes:":  "      ",
		"  rotation: 1": "  ",
		"    - 0.5":     "    ",
		"  note: |":     "    ",
		"":              "",
	} {
		if got := yamlAutoIndent(prev); got != want {
			t.Errorf("yamlAutoIndent(%q) = %q, want %q", prev, got, want)
		}
	}
}

func TestYAMLCheck(t *testing.T) {
	r := yamlCheck(yamlTestLevel, 1)
	if r.Errors {
		t.Fatalf("a good level reported errors: %+v", r.Diags)
	}
	if len(r.Raw.Chunks) != 2 || r.Raw.Chunks[0].ID != "hangar" {
		t.Errorf("decoded %+v", r.Raw.Chunks)
	}

	// An unknown key is a warning with its line, and does not stop a save.
	bad := strings.Replace(yamlTestLevel, "  shape: box", "  shape: box\n  colour: red", 1)
	r = yamlCheck(bad, 2)
	if r.Errors || len(r.Diags) != 1 || !r.Diags[0].Warn || r.Diags[0].Line != 23 {
		t.Errorf("unknown key: %+v", r.Diags)
	}

	// A type error is an error.
	bad = strings.Replace(yamlTestLevel, "x: 1", "x: [1]", 1)
	r = yamlCheck(bad, 3)
	if !r.Errors || r.Diags[0].Line != 17 {
		t.Errorf("type error: %+v", r.Diags)
	}

	// A syntax error stops everything, with its line.
	r = yamlCheck("chunks:\n- id: a\n  bad: [\n", 4)
	if !r.Errors || len(r.Diags) != 1 || r.Diags[0].Line == 0 {
		t.Errorf("syntax error: %+v", r.Diags)
	}
}

func TestYAMLObservedValues(t *testing.T) {
	src := "a:\n  texture: x\n- texture: \"y\"\n  subtexture: z\n  texture: {bad: 1}\n"
	got := yamlObservedValues(src, "texture", -1, 10)
	if !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("observed %v", got)
	}
}

func TestYAMLCheckGameManifest(t *testing.T) {
	good := "title: Foundry\nstart_level: intro\nwindow:\n  width: 1600\n"
	if r := yamlCheckKind(good, 1, yamlKindGame); r.Errors || len(r.Diags) != 0 {
		t.Fatalf("a good manifest reported %+v", r.Diags)
	}

	// The game decodes game.yaml strictly and refuses to boot on an unknown
	// key, so here it is an error rather than a level's warning.
	r := yamlCheckKind(good+"colour: red\n", 2, yamlKindGame)
	if !r.Errors || len(r.Diags) != 1 || r.Diags[0].Warn || r.Diags[0].Line != 5 {
		t.Errorf("unknown key: %+v", r.Diags)
	}

	// What game.Parse validates is checked too.
	r = yamlCheckKind("title: Foundry\n", 3, yamlKindGame)
	if !r.Errors || len(r.Diags) != 1 || !strings.Contains(r.Diags[0].Msg, "start_level") {
		t.Errorf("missing start level: %+v", r.Diags)
	}
}

func TestYAMLComplGameManifest(t *testing.T) {
	prev := yamlEd.doc
	t.Cleanup(func() { yamlEd.doc = prev })
	yamlEd.doc = &yamlDoc{Kind: yamlKindGame}

	ctx, items := yamlComplAt(t, "title: Foundry\nwindow:\n  fu|\n")
	if ctx.Kind != yamlComplKey || ctx.Type != reflect.TypeOf(game.Window{}) {
		t.Fatalf("window ctx = %+v", ctx)
	}
	if texts := yamlItemTexts(items); len(texts) == 0 || texts[0] != "fullscreen" {
		t.Errorf("window keys: %v", texts)
	}
	ctx, items = yamlComplAt(t, "title: Foundry\nst|\n")
	if ctx.Type != yamlGameRootType {
		t.Fatalf("top-level ctx = %+v", ctx)
	}
	if texts := yamlItemTexts(items); len(texts) == 0 || texts[0] != "start_level" {
		t.Errorf("top level: %v", texts)
	}
}
