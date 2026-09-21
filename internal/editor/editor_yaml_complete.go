package editor

// editor_yaml_complete.go — what the level YAML editor offers at the caret.
//
// Two kinds of question, told apart by which side of a colon the caret is on:
//
//   - a key: the fields of the struct the enclosing mapping decodes into, less
//     the ones the mapping already has. Read by reflection off world.RawLevel,
//     so a field added to the schema is offered the day it is added, with the
//     comment above it as its documentation (editor_yaml_docs_gen.go).
//   - a value: `true`/`false` for a bool, the `a (default) | b | c` list a
//     string field's comment spells out, the files a name field can point at
//     (scripts, textures, entity types, prefabs, sounds, effects, screens), and
//     failing all of those the values the same key already has elsewhere in
//     the file — which is what makes an id-like field such as `frame:` useful
//     without anything having to know what it refers to.
//
// The rows reuse the script editor's scriptComplItem and its ranking
// (scriptComplFilter), so the two lists order a half-typed name the same way.

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"foundry/internal/game"
	"foundry/internal/world"
)

// yamlRootType is what a level file decodes into, and yamlGameRootType what
// game.yaml does.
var (
	yamlRootType     = reflect.TypeOf(world.RawLevel{})
	yamlGameRootType = reflect.TypeOf(game.Manifest{})
)

// yamlRoot is the schema of the document on screen.
func yamlRoot() reflect.Type {
	if yamlEd.doc != nil && yamlEd.doc.Kind == yamlKindGame {
		return yamlGameRootType
	}
	return yamlRootType
}

// yamlComplKind is which side of a colon the caret is on.
type yamlComplKind uint8

const (
	yamlComplNone yamlComplKind = iota
	yamlComplKey
	yamlComplValue
)

// yamlComplCtx is the question the caret is asking.
type yamlComplCtx struct {
	Kind yamlComplKind
	// Prefix is what is typed of the word so far, and [Start, End) the bytes
	// accepting a row replaces — End runs past the caret to the end of the word,
	// so completing in the middle of a name replaces all of it.
	Prefix     string
	Start, End int
	// Col is the column a key is being written at, which is what a nested
	// block the completion opens is indented from.
	Col int
	// Type is, for a key, the struct being filled in; for a value, the value's
	// own type.
	Type reflect.Type
	// Field is the key a value belongs to (for a list item, the list's key), so
	// its documentation and enum can be read.
	Field    yamlField
	HasField bool
	// Key is that key's name, which the providers and the file scan go by.
	Key string
	// Siblings are the keys the mapping already has.
	Siblings map[string]bool
	// HasColon says the key being completed already has its colon written.
	HasColon bool
	// Item says the value is a list item rather than a key's value.
	Item bool
}

// yamlWordByte reports whether c can be part of a key or value being typed.
func yamlWordByte(c byte) bool {
	return c == '_' || c == '-' || c == '.' || c == '/' ||
		c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// yamlComplContext works out what the caret at byte cursor is asking for.
func yamlComplContext(ix *yamlLineIndex, cursor int) yamlComplCtx {
	none := yamlComplCtx{}
	if cursor < 0 || cursor > len(ix.src) {
		return none
	}
	line := ix.LineOf(cursor)
	start := ix.Start(line)
	raw := ix.Line(line)
	col := cursor - start
	before := raw[:col]

	// Nothing is offered inside a comment, a quoted scalar or a flow
	// collection: none of them is somewhere the schema has an answer for.
	if yamlStripComment(before) != before {
		return none
	}

	// The word the caret is in.
	ws := col
	for ws > 0 && yamlWordByte(raw[ws-1]) {
		ws--
	}
	// A quote the word opened is part of it: `"y` is the start of the key `y`.
	if ws > 0 && (raw[ws-1] == '"' || raw[ws-1] == '\'') {
		ws--
	}
	we := col
	for we < len(raw) && yamlWordByte(raw[we]) {
		we++
	}
	if we < len(raw) && ws < col && (raw[we] == '"' || raw[we] == '\'') && (raw[ws] == raw[we]) {
		we++
	}
	prefix := strings.TrimLeft(raw[ws:col], `"'`)

	// Everything left of the word decides which kind of word it is.
	lead := raw[:ws]
	indent := 0
	for indent < len(lead) && lead[indent] == ' ' {
		indent++
	}
	var dashes []int
	i := indent
	for i < len(lead) && lead[i] == '-' && (i+1 == len(lead) || lead[i+1] == ' ') {
		dashes = append(dashes, i)
		i++
		for i < len(lead) && lead[i] == ' ' {
			i++
		}
	}
	rest := lead[i:]

	// The path to the mapping or list the word sits in.
	var path []yamlPathSeg
	if len(dashes) > 0 {
		path = yamlParentPath(ix, line, dashes[0], true)
		for _, d := range dashes {
			path = append(path, yamlPathSeg{Item: true, Line: line, Col: d})
		}
	}

	switch {
	case rest == "":
		// A word at the start of a line's content: a key, unless it is a list
		// item and the list holds scalars.
		var parentType reflect.Type
		var parent *yamlPathSeg
		if len(dashes) > 0 {
			parentType = yamlResolve(yamlRoot(), path)
			parent = &path[len(path)-1]
		} else {
			path = yamlParentPath(ix, line, ws, false)
			parentType = yamlResolve(yamlRoot(), path)
			if len(path) > 0 {
				parent = &path[len(path)-1]
			}
		}
		pt := yamlDeref(parentType)
		if pt == nil {
			return none
		}
		if len(dashes) > 0 && pt.Kind() != reflect.Struct && pt.Kind() != reflect.Map {
			// `- scr|` in a list of strings: a value.
			ctx := yamlComplCtx{Kind: yamlComplValue, Prefix: prefix, Start: ws, End: we,
				Col: ws, Type: parentType, Item: true}
			ctx.Start += start
			ctx.End += start
			if f, ok := yamlOwnerOf(yamlRoot(), path[:len(path)-1]); ok {
				ctx.Field, ctx.HasField, ctx.Key = f, true, f.Name
			}
			return ctx
		}
		if pt.Kind() != reflect.Struct {
			return none
		}
		ctx := yamlComplCtx{Kind: yamlComplKey, Prefix: prefix, Start: start + ws, End: start + we,
			Col: ws, Type: pt}
		ctx.HasColon = strings.HasPrefix(strings.TrimLeft(raw[we:], " "), ":")
		ctx.Siblings = yamlSiblingKeys(ix, parent, ws, line, line)
		return ctx

	default:
		// `key: val|` — a value, provided what is left of it is exactly one key
		// and its colon.
		key, _, colon, ok := yamlKeyAt(lead, i)
		if !ok || strings.TrimSpace(lead[colon+1:]) != "" {
			return none
		}
		if len(dashes) == 0 {
			path = yamlParentPath(ix, line, i, false)
		}
		path = append(path, yamlPathSeg{Key: key, Line: line, Col: i})
		f, hasField := yamlOwnerOf(yamlRoot(), path)
		t := yamlResolve(yamlRoot(), path)
		if t == nil || !yamlIsScalar(t) {
			return none
		}
		return yamlComplCtx{Kind: yamlComplValue, Prefix: prefix, Start: start + ws, End: start + we,
			Col: i, Type: t, Field: f, HasField: hasField, Key: key}
	}
}

// yamlComplSource is everything outside the buffer a value list draws on,
// gathered by the caller so this file stays testable without a disk.
type yamlComplSource struct {
	// Providers answers a field with the names it may hold — see
	// yamlValueProviders.
	Providers func(ctx yamlComplCtx) []string
	// Observed answers a key with the values the file already gives it,
	// leaving out the line being typed on.
	Observed func(key string) []string
}

// yamlComplItems builds the unfiltered rows for one question.
func yamlComplItems(ctx yamlComplCtx, src yamlComplSource) []scriptComplItem {
	switch ctx.Kind {
	case yamlComplKey:
		return yamlKeyItems(ctx)
	case yamlComplValue:
		return yamlValueItems(ctx, src)
	}
	return nil
}

// yamlKeyItems is a struct's keys, in the order the struct declares them —
// which is the order the level writer emits them in, and so the order an
// author reading the file is used to.
func yamlKeyItems(ctx yamlComplCtx) []scriptComplItem {
	var out []scriptComplItem
	for _, f := range yamlFieldsOf(ctx.Type) {
		if ctx.Siblings[f.Name] {
			continue
		}
		out = append(out, scriptComplItem{
			Text:   f.Name,
			Label:  f.Name,
			Detail: yamlTypeLabel(f.Type),
			Group:  "yamlkey",
		})
	}
	return out
}

// yamlValueItems is what a value may be.
func yamlValueItems(ctx yamlComplCtx, src yamlComplSource) []scriptComplItem {
	seen := map[string]bool{}
	var out []scriptComplItem
	add := func(v, detail, group string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, scriptComplItem{Text: v, Label: v, Detail: detail, Group: group})
	}

	t := yamlDeref(ctx.Type)
	if t != nil && t.Kind() == reflect.Bool {
		add("true", "bool", "yamlbool")
		add("false", "bool", "yamlbool")
		return out
	}
	if ctx.HasField {
		for _, v := range yamlFieldEnum(ctx.Field) {
			add(v, "option", "yamlenum")
		}
	}
	if src.Providers != nil {
		for _, v := range src.Providers(ctx) {
			add(v, "file", "yamlfile")
		}
	}
	// A number is not something a list of other numbers in the file helps to
	// type; every other scalar is.
	if t != nil && t.Kind() == reflect.String && src.Observed != nil && ctx.Key != "" {
		for _, v := range src.Observed(ctx.Key) {
			add(v, "used in this file", "yamlseen")
		}
	}
	return out
}

// yamlObservedValues returns the distinct values key is given anywhere in src,
// sorted, at most limit of them. The line holding byte skip is left out: it is
// the one being typed, and its half-written value is not a suggestion.
//
// A text search rather than a walk of the structure: it only has to find
// `key: value` pairs, and strings.Index over two megabytes is far quicker than
// looking at a hundred thousand lines one by one.
func yamlObservedValues(src, key string, skip, limit int) []string {
	needle := key + ": "
	seen := map[string]bool{}
	for at := 0; ; {
		i := strings.Index(src[at:], needle)
		if i < 0 {
			break
		}
		i += at
		at = i + len(needle)
		// The key has to be the whole key, at the start of its line's content.
		ls := strings.LastIndexByte(src[:i], '\n') + 1
		if strings.Trim(src[ls:i], " -") != "" {
			continue
		}
		le := strings.IndexByte(src[at:], '\n')
		if le < 0 {
			le = len(src) - at
		}
		if skip >= ls && skip <= at+le {
			continue
		}
		v := strings.TrimSpace(yamlStripComment(src[at : at+le]))
		if v == "" || v[0] == '{' || v[0] == '[' || v[0] == '|' || v[0] == '>' {
			continue
		}
		seen[yamlUnquote(v)] = true
		if len(seen) >= limit {
			break
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// yamlBoolWords are the plain scalars YAML 1.1 — which yaml.v2 speaks — reads as
// a bool or a null. A key or a string that is one of them has to be quoted, or
// `y:` stops being the key `y` and becomes the key `true`; the level writer
// quotes `"y"` for exactly this reason.
var yamlBoolWords = map[string]bool{
	"y": true, "Y": true, "yes": true, "Yes": true, "YES": true,
	"n": true, "N": true, "no": true, "No": true, "NO": true,
	"true": true, "True": true, "TRUE": true, "false": true, "False": true, "FALSE": true,
	"on": true, "On": true, "ON": true, "off": true, "Off": true, "OFF": true,
	"null": true, "Null": true, "NULL": true, "~": true,
}

// yamlNeedsQuote reports whether s would read back as something other than the
// string s if written plain.
func yamlNeedsQuote(s string) bool {
	if s == "" || yamlBoolWords[s] {
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	if strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@` ") {
		return true
	}
	return strings.Contains(s, ": ") || strings.Contains(s, " #") ||
		strings.HasSuffix(s, " ") || strings.HasSuffix(s, ":")
}

// yamlQuoted writes s as a scalar that reads back as the string s.
func yamlQuoted(s string) string {
	if !yamlNeedsQuote(s) {
		return s
	}
	return strconv.Quote(s)
}

// yamlComplEdit is what accepting a row does: replace [Start, End) with Text
// and put the caret at Caret.
type yamlComplEdit struct {
	Start, End int
	Text       string
	Caret      int
}

// yamlComplAccept works out the splice for one row.
//
// A key brings its colon, and a key whose value is a block brings the start of
// that block — the newline, the indent, and for a list the dash — because
// those are the parts of YAML that are easy to get subtly wrong and tedious to
// get right. A small all-scalar struct (a position, a colour) is written the
// flow way instead, `{x: 0, "y": 0, z: 0}`, which is how an author reads one.
func yamlComplAccept(ctx yamlComplCtx, it scriptComplItem) yamlComplEdit {
	e := yamlComplEdit{Start: ctx.Start, End: ctx.End}
	switch ctx.Kind {
	case yamlComplKey:
		name := yamlQuoted(it.Text)
		if ctx.HasColon {
			e.Text = name
			break
		}
		f, _ := yamlFieldNamed(ctx.Type, it.Text)
		e.Text = name + yamlKeyTail(f.Type, ctx.Col)
	case yamlComplValue:
		e.Text = yamlQuoted(it.Text)
		if t := yamlDeref(ctx.Type); t != nil && t.Kind() != reflect.String {
			e.Text = it.Text
		}
	default:
		return e
	}
	e.Caret = e.Start + len(e.Text)
	return e
}

// yamlKeyTail is what follows a completed key: its colon, and whatever opens
// its value.
func yamlKeyTail(t reflect.Type, col int) string {
	if yamlIsScalar(t) {
		return ": "
	}
	d := yamlDeref(t)
	pad := strings.Repeat(" ", col)
	switch d.Kind() {
	case reflect.Slice, reflect.Array:
		return ":\n" + pad + "- "
	case reflect.Struct:
		if flow, ok := yamlFlowSnippet(d); ok {
			return ": " + flow
		}
	}
	return ":\n" + pad + "  "
}

// yamlFlowSnippet renders a small struct of scalars as a flow mapping of its
// zero values, or reports that it is not one.
func yamlFlowSnippet(t reflect.Type) (string, bool) {
	fields := yamlFieldsOf(t)
	if len(fields) == 0 || len(fields) > 4 {
		return "", false
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		ft := yamlDeref(f.Type)
		var zero string
		switch ft.Kind() {
		case reflect.Float32, reflect.Float64, reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint8:
			zero = "0"
		case reflect.Bool:
			zero = "false"
		default:
			return "", false
		}
		parts = append(parts, yamlQuoted(f.Name)+": "+zero)
	}
	return "{" + strings.Join(parts, ", ") + "}", true
}

// ---------- hover ----------

// yamlHoverInfo is what the buffer says about the key under the mouse.
type yamlHoverInfo struct {
	Key, Type, Doc string
	Enum           []string
	// Path is the breadcrumb to the key, so a hover over one of a hundred
	// `texture:` keys says which one it is.
	Path string
}

// yamlHoverAt returns the documentation for the key at byte off, if off is on
// one.
func yamlHoverAt(ix *yamlLineIndex, off int) (yamlHoverInfo, bool) {
	if off < 0 || off > len(ix.src) {
		return yamlHoverInfo{}, false
	}
	line := ix.LineOf(off)
	col := off - ix.Start(line)
	s := yamlShapeOf(ix.Line(line))
	if s.KeyCol < 0 || col < s.KeyCol || col >= s.KeyEnd {
		return yamlHoverInfo{}, false
	}
	var path []yamlPathSeg
	if len(s.Dashes) > 0 {
		path = yamlParentPath(ix, line, s.Dashes[0], true)
		for _, d := range s.Dashes {
			path = append(path, yamlPathSeg{Item: true, Line: line, Col: d})
		}
	} else {
		path = yamlParentPath(ix, line, s.KeyCol, false)
	}
	path = append(path, yamlPathSeg{Key: s.Key, Line: line, Col: s.KeyCol})
	info := yamlHoverInfo{Key: s.Key, Path: yamlBreadcrumb(ix, ix.Start(line)+s.KeyCol)}
	f, ok := yamlOwnerOf(yamlRoot(), path)
	if !ok {
		// A key the schema does not have is worth saying so about: it is the
		// commonest way a hand-edited level silently loses a setting.
		if parent := yamlDeref(yamlResolve(yamlRoot(), path[:len(path)-1])); parent != nil &&
			parent.Kind() == reflect.Struct {
			info.Doc = "Not a key " + strings.TrimPrefix(parent.Name(), "Raw") +
				" has — the loader ignores it."
			if yamlRoot() == yamlGameRootType {
				info.Doc = "Not a key " + parent.Name() + " has — the game refuses to start with it."
			}
			return info, true
		}
		return yamlHoverInfo{}, false
	}
	info.Type = yamlTypeLabel(f.Type)
	info.Doc = yamlFieldDoc(f)
	info.Enum = yamlFieldEnum(f)
	return info, true
}
