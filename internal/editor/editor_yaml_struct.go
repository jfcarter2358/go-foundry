package editor

// editor_yaml_struct.go — reading a level file's shape out of its text.
//
// The YAML editor never parses the buffer into a tree to answer "where am I".
// A level is routinely two megabytes and a hundred thousand lines, the buffer is
// half-typed most of the time it is asked, and a YAML parser handed a
// half-typed document answers with an error rather than with a tree. What it
// reads instead is indentation: every question the completion list, the hover
// and the breadcrumb ask is "which keys and list items enclose this column",
// and in block YAML that is decided entirely by which lines above start further
// to the left.
//
// So the scans here walk *backwards* from the caret and look at a line only far
// enough to learn its indent — the overwhelming majority of a level's lines are
// deeper than anything being asked about and are skipped on that one number.
// That keeps a question about line 120,000 of level1.yaml well under a
// millisecond, where a forward parse to reach it would not be.
//
// Flow collections (`{x: 1, "y": 2}`) and block scalars (`key: |`) are read as
// values, not as structure: the level writer never emits either across lines,
// and nothing here has to see inside them.

import (
	"reflect"
	"sort"
	"strings"
)

// yamlLineIndex is where each line of a buffer starts. Built once per edit and
// shared by everything that turns a byte offset into a line or back.
type yamlLineIndex struct {
	src    string
	starts []int
}

// newYAMLLineIndex indexes src.
func newYAMLLineIndex(src string) *yamlLineIndex {
	starts := make([]int, 1, strings.Count(src, "\n")+1)
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &yamlLineIndex{src: src, starts: starts}
}

// Count is the number of lines. A buffer ending in a newline has an empty last
// line, the same way an editor shows one.
func (x *yamlLineIndex) Count() int { return len(x.starts) }

// Start is the byte offset line i begins at.
func (x *yamlLineIndex) Start(i int) int { return x.starts[i] }

// End is the byte offset just past line i's last character, before its newline.
func (x *yamlLineIndex) End(i int) int {
	if i+1 < len(x.starts) {
		return x.starts[i+1] - 1
	}
	return len(x.src)
}

// Line returns line i without its newline.
func (x *yamlLineIndex) Line(i int) string { return x.src[x.starts[i]:x.End(i)] }

// LineOf returns the line holding byte offset off.
func (x *yamlLineIndex) LineOf(off int) int {
	// The last start at or before off.
	return sort.Search(len(x.starts), func(i int) bool { return x.starts[i] > off }) - 1
}

// ---------- one line ----------

// yamlLineShape is what one line contributes to the document's structure.
type yamlLineShape struct {
	// Indent is the column of the first non-space character.
	Indent int
	// Blank is a line with nothing structural on it: empty, a comment, or a
	// document marker.
	Blank bool
	// Dashes are the columns of the line's `- ` sequence indicators, outermost
	// first. `- - a` has two.
	Dashes []int
	// ContentCol is where the line's content starts after its dashes.
	ContentCol int
	// KeyCol is the column the line's mapping key starts at, or -1 when the
	// content is not a key — a scalar list item, or a flow collection.
	KeyCol int
	// Key is the key, unquoted, and KeyEnd the column just past it as written
	// (its closing quote included).
	Key    string
	KeyEnd int
	// Value is the inline value after `key: `, its comment stripped, and
	// ValueCol the column it starts at.
	Value    string
	ValueCol int
}

// BlockValue reports whether the key opens a nested block rather than holding a
// value on its own line — nothing after the colon, or only an anchor or a tag.
func (s yamlLineShape) BlockValue() bool {
	if s.KeyCol < 0 {
		return false
	}
	v := s.Value
	return v == "" || (strings.IndexByte(v, ' ') < 0 && (v[0] == '&' || v[0] == '!'))
}

// BlockScalar reports whether the value is a `|` or `>` literal whose text
// follows on the lines below.
func (s yamlLineShape) BlockScalar() bool {
	return s.KeyCol >= 0 && s.Value != "" && (s.Value[0] == '|' || s.Value[0] == '>')
}

// yamlIndentOf is the column of line's first non-space character, and whether
// the line has anything structural on it at all. The cheap half of
// yamlShapeOf, which is all the backward scans need for most lines.
func yamlIndentOf(line string) (indent int, blank bool) {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	if i == len(line) || line[i] == '#' || line[i] == '\r' {
		return i, true
	}
	if i == 0 && (strings.HasPrefix(line, "---") || strings.HasPrefix(line, "...")) &&
		(len(line) == 3 || line[3] == ' ' || line[3] == '\r') {
		return 0, true
	}
	return i, false
}

// yamlShapeOf reads one line.
func yamlShapeOf(line string) yamlLineShape {
	line = strings.TrimSuffix(line, "\r")
	indent, blank := yamlIndentOf(line)
	s := yamlLineShape{Indent: indent, Blank: blank, KeyCol: -1, ContentCol: indent, ValueCol: len(line)}
	if blank {
		return s
	}
	i := indent
	for i < len(line) && line[i] == '-' && (i+1 == len(line) || line[i+1] == ' ') {
		s.Dashes = append(s.Dashes, i)
		i++
		for i < len(line) && line[i] == ' ' {
			i++
		}
	}
	s.ContentCol = i
	if i >= len(line) || line[i] == '#' {
		return s
	}
	key, keyEnd, colon, ok := yamlKeyAt(line, i)
	if !ok {
		return s
	}
	s.KeyCol, s.Key, s.KeyEnd = i, key, keyEnd
	v := colon + 1
	for v < len(line) && line[v] == ' ' {
		v++
	}
	s.ValueCol = v
	s.Value = strings.TrimRight(yamlStripComment(line[v:]), " ")
	return s
}

// yamlKeyAt reads a mapping key starting at column i: a quoted or plain scalar
// followed by a colon that ends the line or is followed by a space. keyEnd is
// just past the key as written, colon is the colon's column.
func yamlKeyAt(line string, i int) (key string, keyEnd, colon int, ok bool) {
	if i >= len(line) {
		return "", 0, 0, false
	}
	switch c := line[i]; c {
	case '"', '\'':
		end := yamlQuoteEnd(line, i)
		if end < 0 {
			return "", 0, 0, false
		}
		j := end
		for j < len(line) && line[j] == ' ' {
			j++
		}
		if j < len(line) && line[j] == ':' && (j+1 == len(line) || line[j+1] == ' ') {
			return yamlUnquote(line[i:end]), end, j, true
		}
		return "", 0, 0, false
	case '{', '[', '#', '&', '*', '!', '|', '>', '%', '@', '`':
		return "", 0, 0, false
	}
	for j := i; j < len(line); j++ {
		switch line[j] {
		case ':':
			if j+1 == len(line) || line[j+1] == ' ' {
				k := strings.TrimRight(line[i:j], " ")
				if k == "" {
					return "", 0, 0, false
				}
				return k, i + len(k), j, true
			}
		case '#':
			if j > i && line[j-1] == ' ' {
				return "", 0, 0, false
			}
		}
	}
	return "", 0, 0, false
}

// yamlQuoteEnd returns the column just past the quoted scalar opening at i, or
// -1 when it does not close on this line.
func yamlQuoteEnd(line string, i int) int {
	q := line[i]
	for j := i + 1; j < len(line); j++ {
		switch {
		case q == '"' && line[j] == '\\':
			j++
		case line[j] == q:
			if q == '\'' && j+1 < len(line) && line[j+1] == '\'' {
				j++ // '' is an escaped quote inside a single-quoted scalar
				continue
			}
			return j + 1
		}
	}
	return -1
}

// yamlUnquote strips a scalar's quotes. Only the escapes a level file can
// actually contain are undone; this is for naming keys, not for reading values
// a runtime depends on.
func yamlUnquote(s string) string {
	if len(s) < 2 {
		return s
	}
	switch s[0] {
	case '"':
		if s[len(s)-1] == '"' {
			return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(s[1 : len(s)-1])
		}
	case '\'':
		if s[len(s)-1] == '\'' {
			return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
		}
	}
	return s
}

// yamlStripComment drops a trailing ` # comment` from a value, leaving any `#`
// inside quotes alone.
func yamlStripComment(v string) string {
	for j := 0; j < len(v); j++ {
		switch v[j] {
		case '"', '\'':
			if end := yamlQuoteEnd(v, j); end > 0 {
				j = end - 1
			}
		case '#':
			if j == 0 || v[j-1] == ' ' {
				return v[:j]
			}
		}
	}
	return v
}

// ---------- where a position sits ----------

// yamlPathSeg is one step from the document root towards a position: a mapping
// key, or an item of a sequence.
type yamlPathSeg struct {
	Key  string // "" for a sequence item
	Item bool
	// Line and Col are where the step's key or dash is written.
	Line, Col int
	// Label is a sequence item's `id:` (or `name:`), when the line that starts
	// the item gives one — what the breadcrumb shows beside its index.
	Label string
}

// yamlParentPath returns the path from the root to whatever encloses an
// element at (line, col) — a key when dash is false, a `- ` when it is true.
//
// See the file's header for why this walks backwards: an element's parent is
// the nearest line above that starts further left, and a line that starts at
// or right of the element can be dismissed on its indent alone.
func yamlParentPath(ix *yamlLineIndex, line, col int, dash bool) []yamlPathSeg {
	var rev []yamlPathSeg
	want, wantDash := col, dash
	for l := line - 1; l >= 0 && want >= 0; l-- {
		raw := ix.Line(l)
		indent, blank := yamlIndentOf(raw)
		if blank || indent > want {
			continue
		}
		s := yamlShapeOf(raw)
		// The line's elements, innermost (rightmost) first: its key, then its
		// dashes. An ancestor found on this line hands the question on to the
		// elements left of it, which are *its* ancestors.
		type elem struct {
			col  int
			dash bool
		}
		elems := make([]elem, 0, len(s.Dashes)+1)
		if s.KeyCol >= 0 {
			elems = append(elems, elem{s.KeyCol, false})
		}
		for d := len(s.Dashes) - 1; d >= 0; d-- {
			elems = append(elems, elem{s.Dashes[d], true})
		}
		for _, e := range elems {
			switch {
			case e.col > want:
				continue
			case e.col == want && e.dash == wantDash:
				// A sibling. Whatever is left of it on the line is an
				// ancestor of both.
				continue
			case e.col == want && !e.dash && wantDash:
				// A sequence written at its parent key's own indent:
				//   chunks:
				//   - id: hangar
				if !s.BlockValue() {
					continue
				}
			case e.col == want && e.dash && !wantDash:
				// A sequence at the indent of a key that came before this
				// one — the previous sibling's value, not a parent.
				continue
			}
			seg := yamlPathSeg{Line: l, Col: e.col, Item: e.dash}
			if !e.dash {
				seg.Key = s.Key
			} else if e.col == s.Dashes[len(s.Dashes)-1] && (s.Key == "id" || s.Key == "name") {
				// The item's first key, written on its dash's line.
				seg.Label = yamlUnquote(s.Value)
			}
			rev = append(rev, seg)
			want, wantDash = e.col, e.dash
		}
		if want == 0 && !wantDash {
			// A top-level key: its parent is the root, and every line above is a
			// sibling or a sibling's content.
			break
		}
	}
	out := make([]yamlPathSeg, len(rev))
	for i, s := range rev {
		out[len(rev)-1-i] = s
	}
	return out
}

// yamlItemIndex returns which item of its sequence the dash at seg is, by
// counting the sibling dashes between it and the sequence's parent.
func yamlItemIndex(ix *yamlLineIndex, seg yamlPathSeg, parentLine int) int {
	n := 0
	for l := seg.Line - 1; l > parentLine; l-- {
		raw := ix.Line(l)
		indent, blank := yamlIndentOf(raw)
		if blank || indent > seg.Col {
			continue
		}
		if indent == seg.Col && strings.HasPrefix(raw[indent:], "-") &&
			(len(raw) == indent+1 || raw[indent+1] == ' ') {
			n++
		}
	}
	return n
}

// yamlSiblingKeys returns the keys already written in the mapping whose keys
// sit at column col, enclosed by parent — the list the completion leaves out,
// since a mapping cannot say the same key twice. skipLine is the line being
// typed on, whose own half-written key is not yet a sibling.
func yamlSiblingKeys(ix *yamlLineIndex, parent *yamlPathSeg, col, from, skipLine int) map[string]bool {
	out := map[string]bool{}
	start := 0
	if parent != nil {
		start = parent.Line
	}
	// Backwards to the parent, then forwards to where the mapping ends.
	add := func(l int) bool {
		raw := ix.Line(l)
		indent, blank := yamlIndentOf(raw)
		if blank {
			return true
		}
		if indent < col {
			// The item's own first line (`- id: a`) starts left of its keys
			// and still holds one.
			if parent != nil && parent.Item && l == parent.Line {
				if s := yamlShapeOf(raw); s.KeyCol == col {
					out[s.Key] = true
				}
			}
			return false
		}
		if indent == col && l != skipLine {
			if s := yamlShapeOf(raw); s.KeyCol == col && len(s.Dashes) == 0 {
				out[s.Key] = true
			}
		}
		return true
	}
	for l := from - 1; l >= start; l-- {
		if !add(l) {
			break
		}
	}
	for l := from; l < ix.Count(); l++ {
		if !add(l) {
			break
		}
	}
	return out
}

// yamlBreadcrumb renders a position's path — `chunks[3] hangar › brushes[12] ›
// faces[0] › texture` — for the status line under the buffer.
func yamlBreadcrumb(ix *yamlLineIndex, off int) string {
	line := ix.LineOf(off)
	col := off - ix.Start(line)
	s := yamlShapeOf(ix.Line(line))
	var path []yamlPathSeg
	var tail string
	switch {
	case len(s.Dashes) > 0:
		path = yamlParentPath(ix, line, s.Dashes[0], true)
		for _, d := range s.Dashes {
			path = append(path, yamlPathSeg{Item: true, Line: line, Col: d})
		}
		if s.KeyCol >= 0 && (s.Key == "id" || s.Key == "name") {
			path[len(path)-1].Label = yamlUnquote(s.Value)
		}
	case s.KeyCol >= 0:
		path = yamlParentPath(ix, line, s.KeyCol, false)
	default:
		path = yamlParentPath(ix, line, s.Indent, false)
	}
	if s.KeyCol >= 0 && col >= s.KeyCol {
		tail = s.Key
	}
	var b strings.Builder
	for i, seg := range path {
		if seg.Item {
			parentLine := -1
			if i > 0 {
				parentLine = path[i-1].Line
			}
			b.WriteString("[")
			b.WriteString(itoa(yamlItemIndex(ix, seg, parentLine)))
			b.WriteString("]")
			if seg.Label != "" {
				b.WriteString(" ")
				b.WriteString(seg.Label)
			}
			continue
		}
		if i > 0 {
			b.WriteString(" › ")
		}
		b.WriteString(seg.Key)
	}
	if tail != "" {
		if b.Len() > 0 {
			b.WriteString(" › ")
		}
		b.WriteString(tail)
	}
	return b.String()
}

// itoa is strconv.Itoa without the import in a file that otherwise needs none.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---------- the schema ----------

// yamlField is one key a mapping may hold, as the Go type behind it reads it.
type yamlField struct {
	Name string
	Type reflect.Type
	// Owner is the struct that declares the field — for a field pulled up
	// through `,inline`, the embedded struct rather than the one it lands in.
	// What the documentation table is keyed by.
	Owner reflect.Type
}

// yamlFieldCache holds each struct's flattened key list, which reflection
// would otherwise rebuild on every keystroke.
var yamlFieldCache = map[reflect.Type][]yamlField{}

// yamlFieldsOf returns the keys a struct type decodes, in declaration order,
// with inline structs flattened into it the way yaml.v2 flattens them.
func yamlFieldsOf(t reflect.Type) []yamlField {
	t = yamlDeref(t)
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	if f, ok := yamlFieldCache[t]; ok {
		return f
	}
	var out []yamlField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue // unexported: yaml.v2 never sees it
		}
		tag := f.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if strings.Contains(","+opts+",", ",inline,") {
			out = append(out, yamlFieldsOf(f.Type)...)
			continue
		}
		if f.PkgPath != "" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		out = append(out, yamlField{Name: name, Type: f.Type, Owner: t})
	}
	yamlFieldCache[t] = out
	return out
}

// yamlFieldNamed finds one key of a struct type.
func yamlFieldNamed(t reflect.Type, name string) (yamlField, bool) {
	for _, f := range yamlFieldsOf(t) {
		if f.Name == name {
			return f, true
		}
	}
	return yamlField{}, false
}

// yamlDeref looks through pointers, which a YAML document cannot tell apart
// from what they point at.
func yamlDeref(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// yamlResolve walks a path down from root and returns the type of the value it
// arrives at, or nil when the path leaves the schema — a key the type does not
// have, or a step into a scalar.
func yamlResolve(root reflect.Type, path []yamlPathSeg) reflect.Type {
	t := root
	for _, seg := range path {
		t = yamlDeref(t)
		if t == nil {
			return nil
		}
		switch {
		case seg.Item:
			if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
				return nil
			}
			t = t.Elem()
		case t.Kind() == reflect.Map:
			t = t.Elem()
		case t.Kind() == reflect.Struct:
			f, ok := yamlFieldNamed(t, seg.Key)
			if !ok {
				return nil
			}
			t = f.Type
		default:
			return nil
		}
	}
	return t
}

// yamlOwnerOf returns the struct a path's last key belongs to and that key's
// field, for looking up its documentation.
func yamlOwnerOf(root reflect.Type, path []yamlPathSeg) (yamlField, bool) {
	if len(path) == 0 || path[len(path)-1].Item {
		return yamlField{}, false
	}
	parent := yamlDeref(yamlResolve(root, path[:len(path)-1]))
	if parent == nil || parent.Kind() != reflect.Struct {
		return yamlField{}, false
	}
	return yamlFieldNamed(parent, path[len(path)-1].Key)
}

// yamlTypeLabel is how a type reads in the completion list and the hover: the
// shape an author has to write, rather than the Go spelling.
func yamlTypeLabel(t reflect.Type) string {
	t = yamlDeref(t)
	if t == nil {
		return ""
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return "list of " + yamlTypeLabel(t.Elem())
	case reflect.Map:
		return "map of " + yamlTypeLabel(t.Elem())
	case reflect.Struct:
		return strings.TrimPrefix(t.Name(), "Raw")
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Interface:
		return "any"
	}
	return t.Kind().String()
}

// yamlIsScalar reports whether a type is written on its key's own line.
func yamlIsScalar(t reflect.Type) bool {
	t = yamlDeref(t)
	if t == nil {
		return true
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array:
		return false
	}
	return true
}

// yamlFieldDocKey is how the generated documentation tables name a field.
func yamlFieldDocKey(f yamlField) string {
	if f.Owner == nil {
		return ""
	}
	return f.Owner.String() + "." + f.Name
}

// yamlFieldDoc returns a field's documentation, from its Go comment.
func yamlFieldDoc(f yamlField) string { return yamlFieldDocs[yamlFieldDocKey(f)] }

// yamlFieldEnum returns the values a string field's comment lists — the
// `box (default) | sphere | cylinder` convention the schema's comments follow.
func yamlFieldEnum(f yamlField) []string { return yamlFieldEnums[yamlFieldDocKey(f)] }
