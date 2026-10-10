package editor

import (
	"strings"
	"testing"

	"foundry/internal/flang"
)

// at splits a fixture on the caret marker `|`, returning the source without it
// and the byte offset it stood at. Writing the caret into the string is the
// only way these cases stay readable — an offset counted by hand is a test that
// is wrong for a reason nobody can see.
func at(src string) (string, int) {
	i := strings.Index(src, "|")
	if i < 0 {
		panic("fixture has no caret")
	}
	return src[:i] + src[i+1:], i
}

func TestScriptComplContextKinds(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		want   scriptComplKind
		prefix string
		base   string
		path   []string
		sigil  bool
	}{
		{name: "bare name", src: "x = lo|", want: complIdent, prefix: "lo"},
		{name: "address", src: "@do|", want: complAddr, prefix: "do"},
		{name: "address, nothing typed", src: "@|", want: complAddr, sigil: true},
		{name: "tag", src: "#lam|", want: complTag, prefix: "lam"},
		{name: "param", src: "?dro|", want: complParam, prefix: "dro"},
		{name: "param, nothing typed", src: "?|", want: complParam, sigil: true},
		{name: "property through a param", src: "?door.floor.prop.|",
			want: complProp, base: "door", path: []string{"floor"}, sigil: true},
		{name: "atom being typed", src: "def on(:spa|", want: complAtom, prefix: "spa"},
		{name: "atom, nothing typed", src: "after(500, :|", want: complAtom, sigil: true},
		{name: "member", src: "@door.|", want: complMember, base: "door", sigil: true},
		{name: "property", src: "@self.prop.vis|", want: complProp, prefix: "vis", base: "self"},
		{name: "property through a floor", src: "@sec.floor.prop.|",
			want: complProp, base: "sec", path: []string{"floor"}, sigil: true},
		{name: "attribute", src: "@barrel.attr.he|", want: complAttr, prefix: "he", base: "barrel"},
		{name: "property on a tag", src: "#lamps.prop.br|", want: complProp, prefix: "br", base: "lamps"},
		{name: "property on a variable", src: "src.prop.he|", want: complProp, prefix: "he"},
		{name: "return type", src: "def f(a int) -> nu|", want: complType, prefix: "nu"},
		{name: "param type", src: "param speed fl|", want: complParamType, prefix: "fl"},
		{name: "handler param type", src: "def on_hit(:hit:, src re|", want: complType, prefix: "re"},
		{name: "in a comment", src: "-- @se|", want: complNone},
		{name: "in a string", src: "log(\"@se|", want: complNone},
		{name: "in a number", src: "x = 1.|", want: complNone},
		{name: "a dict key's colon", src: "{\"speed\": va|", want: complIdent, prefix: "va"},
		{name: "a dict key's colon, unspaced", src: "{\"speed\":va|", want: complIdent, prefix: "va"},
		{name: "an atom's colon, unspaced", src: "@self <- (:op|", want: complAtom, prefix: "op"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, cursor := at(c.src)
			got := scriptComplContext(src, cursor)
			if got.Kind != c.want {
				t.Fatalf("kind = %v, want %v", got.Kind, c.want)
			}
			if got.Prefix != c.prefix {
				t.Errorf("prefix = %q, want %q", got.Prefix, c.prefix)
			}
			if got.Base != c.base {
				t.Errorf("base = %q, want %q", got.Base, c.base)
			}
			if strings.Join(got.Path, ".") != strings.Join(c.path, ".") {
				t.Errorf("path = %v, want %v", got.Path, c.path)
			}
			if got.Sigil != c.sigil {
				t.Errorf("sigil = %v, want %v", got.Sigil, c.sigil)
			}
			if c.want != complNone && c.prefix != "" {
				if src[got.Start:cursor] != c.prefix {
					t.Errorf("Start %d does not span the prefix: %q", got.Start, src[got.Start:cursor])
				}
			}
		})
	}
}

// A tag's base is carried the same way an address's is, so that `#lamps.prop.`
// can say which tag it hangs off even though the kind is not knowable.
func TestScriptComplContextCarriesTheSigil(t *testing.T) {
	src, cursor := at("#lamps.prop.br|")
	ctx := scriptComplContext(src, cursor)
	if ctx.BaseSigil != '#' {
		t.Fatalf("BaseSigil = %q, want '#'", ctx.BaseSigil)
	}
	src, cursor = at("?door.prop.he|")
	if ctx = scriptComplContext(src, cursor); ctx.BaseSigil != '?' {
		t.Fatalf("BaseSigil = %q, want '?'", ctx.BaseSigil)
	}
	// Which object a param names is the level's answer, so the kind behind a
	// `?` chain is not knowable here — and a guess would narrow the property
	// list to the wrong kind.
	if _, ok := scriptComplBaseID(ctx); ok {
		t.Error("a param base should not resolve to a level id")
	}
	if k := scriptComplBaseKind(ctx); k != flang.RefNone {
		t.Errorf("base kind = %v, want RefNone", k)
	}
}

// The two lists are the two namespaces: `?` offers what the file declared and
// `@` offers what the level holds, and neither carries the other's names.
func TestScriptComplParamsAreOnTheirOwnSigil(t *testing.T) {
	src := scriptComplSource{
		Scan:           scriptComplScanFile(scanFixture),
		ImportedParams: []string{"shared_speed"},
	}
	params := scriptComplItems(scriptComplCtx{Kind: complParam}, src)
	for _, want := range []string{"target", "drop", "shared_speed"} {
		if !containsItem(params, want) {
			t.Errorf("? offered %v, missing %q", itemTexts(params), want)
		}
	}
	if got := params[0].Label; got != "?target" {
		t.Errorf("label = %q, want the sigil on it", got)
	}

	addrs := scriptComplItems(scriptComplCtx{Kind: complAddr}, src)
	for _, unwanted := range []string{"target", "drop", "shared_speed"} {
		if containsItem(addrs, unwanted) {
			t.Errorf("@ offered the param %q", unwanted)
		}
	}
	if !containsItem(addrs, "self") {
		t.Errorf("@ offered %v, missing self", itemTexts(addrs))
	}
}

func containsItem(items []scriptComplItem, want string) bool {
	for _, it := range items {
		if it.Text == want {
			return true
		}
	}
	return false
}

func itemTexts(items []scriptComplItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Text)
	}
	return out
}

const scanFixture = `-- a door
param target ref
param drop float = 3.0

def on_interact(:interact:, src ref) -> null
    total = 0
    for step in range(3)
        total = total + step
    end
    if total > 0
        @self <- (:open_done:)
    end
end

def helper(a int, b int) -> int
    return a + b
end
`

func TestScriptComplScanFile(t *testing.T) {
	got := scriptComplScanFile(scanFixture)

	var funcs []string
	for _, f := range got.Funcs {
		funcs = append(funcs, f.Name)
	}
	if strings.Join(funcs, ",") != "on_interact,helper" {
		t.Errorf("funcs = %v", funcs)
	}
	if len(got.Params) != 2 || got.Params[0].Name != "target" || got.Params[0].Type != "ref" ||
		got.Params[1].Name != "drop" || got.Params[1].Type != "float" {
		t.Errorf("params = %+v", got.Params)
	}
	// The atoms a file mentions are completable whether the engine posts them
	// or not: `:open_done:` is this script's own and exists nowhere else.
	if !contains(got.Atoms, "open_done") || !contains(got.Atoms, "interact") {
		t.Errorf("atoms = %v", got.Atoms)
	}
}

func TestScriptComplLocalsAreScopedToTheirDefinition(t *testing.T) {
	// Inside on_interact: its parameter, its assignment and its loop variable.
	inside := strings.Index(scanFixture, "total = total + step")
	locals := scriptComplScanFile(scanFixture).LocalsAt(inside)
	for _, want := range []string{"src", "total", "step"} {
		if !contains(locals, want) {
			t.Errorf("locals %v missing %q", locals, want)
		}
	}
	// `helper`'s parameters belong to `helper`, and a completion offered in one
	// function for a name bound in another is a name that does not exist.
	if contains(locals, "a") || contains(locals, "b") {
		t.Errorf("locals %v leaked from helper", locals)
	}
	// The atom in the head is a pattern, not a binding.
	if contains(locals, "interact") {
		t.Errorf("locals %v bound an atom", locals)
	}
}

func TestScriptComplLocalsSkipTypeNames(t *testing.T) {
	locals := scriptComplScanFile(scanFixture).LocalsAt(strings.Index(scanFixture, "return a + b"))
	for _, want := range []string{"a", "b"} {
		if !contains(locals, want) {
			t.Errorf("locals %v missing %q", locals, want)
		}
	}
	if contains(locals, "int") {
		t.Errorf("locals %v took a parameter's type for a name", locals)
	}
}

func TestScriptComplHeadTextIsTheSignature(t *testing.T) {
	f := scriptComplScanFile(scanFixture).Funcs[0]
	if f.Sig != "on_interact(:interact:, src ref) -> null" {
		t.Errorf("Sig = %q", f.Sig)
	}
}

func TestScriptComplRankOrdersByHowWellItMatches(t *testing.T) {
	cases := []struct {
		prefix, name string
		want         int
	}{
		{"log", "log", rankPrefixExact},
		{"lo", "log", rankPrefixExact},
		{"LO", "log", rankPrefixFold},
		{"og", "log", rankContains},
		{"stp", "stop_anim", rankSubsequence},
		{"zzz", "log", rankNone},
	}
	for _, c := range cases {
		if got := scriptComplRank(c.prefix, c.name); got != c.want {
			t.Errorf("rank(%q, %q) = %d, want %d", c.prefix, c.name, got, c.want)
		}
	}
}

// The order the items were built in is the tie-break, so a local beats a
// builtin that matches exactly as well.
func TestScriptComplFilterKeepsBuildOrderWithinARank(t *testing.T) {
	items := []scriptComplItem{
		{Text: "position", Group: "var"},
		{Text: "pos_of", Group: "fn"},
		{Text: "unrelated", Group: "fn"},
	}
	got := scriptComplFilter(items, "pos")
	if len(got) != 2 || got[0].Text != "position" || got[1].Text != "pos_of" {
		t.Fatalf("filter = %+v", got)
	}
}

func TestScriptComplAcceptSplices(t *testing.T) {
	cases := []struct {
		name string
		src  string
		item scriptComplItem
		want string
		// caret is the offset the caret should land on, as an index into want.
		caret int
	}{
		{
			name: "a value takes no punctuation",
			src:  "x = pos|", item: scriptComplItem{Text: "position"},
			want: "x = position", caret: 12,
		},
		{
			name: "a function brings its parentheses",
			src:  "lo|", item: scriptComplItem{Text: "log", Call: true},
			want: "log()", caret: 4,
		},
		{
			name: "parentheses already there are not doubled",
			src:  "lo|()", item: scriptComplItem{Text: "log", Call: true},
			want: "log()", caret: 3,
		},
		{
			name: "the whole word is replaced, not spliced into",
			src:  "pos|xxxx = 1", item: scriptComplItem{Text: "position"},
			want: "position = 1", caret: 8,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, cursor := at(c.src)
			ctx := scriptComplContext(src, cursor)
			e := scriptComplAccept(ctx, c.item, src, cursor)
			got := src[:e.Start] + e.Text + src[e.End:]
			if got != c.want {
				t.Fatalf("result = %q, want %q", got, c.want)
			}
			if e.Caret != c.caret {
				t.Errorf("caret = %d, want %d", e.Caret, c.caret)
			}
		})
	}
}

// An atom's closing colon is added by the completion, and not added twice when
// the author has already typed it.
func TestScriptComplAcceptClosesAnAtom(t *testing.T) {
	src, cursor := at("after(500, :sp|")
	ctx := scriptComplContext(src, cursor)
	e := scriptComplAccept(ctx, scriptComplItem{Text: "spawn"}, src, cursor)
	if got := src[:e.Start] + e.Text + src[e.End:]; got != "after(500, :spawn:" {
		t.Fatalf("result = %q", got)
	}

	src, cursor = at("after(500, :sp|:)")
	ctx = scriptComplContext(src, cursor)
	e = scriptComplAccept(ctx, scriptComplItem{Text: "spawn"}, src, cursor)
	got := src[:e.Start] + e.Text + src[e.End:]
	if got != "after(500, :spawn:)" {
		t.Fatalf("result = %q", got)
	}
	if got[e.Caret] != ')' {
		t.Errorf("caret at %d landed on %q, want past the colon", e.Caret, got[e.Caret])
	}
}

// The lists that come from somewhere else really do come from there: a builtin
// added to flang, or a keyword, is offered without this package being touched.
func TestScriptComplIdentItemsReadTheLanguagesOwnTables(t *testing.T) {
	items := scriptComplItems(scriptComplCtx{Kind: complIdent}, scriptComplSource{})
	var names []string
	for _, it := range items {
		names = append(names, it.Text)
	}
	for _, want := range flang.BuiltinNames() {
		if !contains(names, want) {
			t.Fatalf("builtin %q is not offered", want)
		}
	}
	for _, want := range flang.KeywordNames() {
		if !contains(names, want) {
			t.Fatalf("keyword %q is not offered", want)
		}
	}
}

// Proximity, not the alphabet: what this function binds comes before what this
// file defines, which comes before the standard library.
func TestScriptComplIdentItemsAreOrderedByProximity(t *testing.T) {
	src := scriptComplSource{
		Locals: []string{"zzz_local"},
		Scan:   scriptComplScan{Funcs: []scriptComplFunc{{Name: "aaa_func"}}},
	}
	items := scriptComplItems(scriptComplCtx{Kind: complIdent}, src)
	if items[0].Text != "zzz_local" || items[1].Text != "aaa_func" {
		t.Fatalf("order = %q, %q", items[0].Text, items[1].Text)
	}
}

func TestScriptComplStructuralNarrowsWithTheKind(t *testing.T) {
	if len(scriptComplStructuralOf(flang.RefBrush)) != 1 {
		t.Errorf("a brush should offer only its chunk")
	}
	var names []string
	for _, m := range scriptComplStructuralOf(flang.RefChunk) {
		names = append(names, m.Name)
	}
	for _, want := range []string{"brushes"} {
		if !contains(names, want) {
			t.Errorf("a chunk should offer %q, got %v", want, names)
		}
	}
	// An unknown kind is most refs a script holds, so it gets the union rather
	// than nothing.
	if len(scriptComplStructuralOf(flang.RefNone)) == 0 {
		t.Errorf("an unknown kind offered nothing")
	}
}

func TestScriptComplPathKindWalksToTheChunk(t *testing.T) {
	if got := scriptComplPathKind(flang.RefBrush, []string{"chunk"}); got != flang.RefChunk {
		t.Errorf("kind = %v, want chunk", got)
	}
	// A step that is not structural gives up rather than guessing.
	if got := scriptComplPathKind(flang.RefBrush, []string{"nonsense"}); got != flang.RefNone {
		t.Errorf("kind = %v, want none", got)
	}
}

// `@guard.model.body.prop.` has to offer an attachment's properties, which
// means the walk has to survive the attachment name — the step that is an id
// rather than a word.
func TestScriptComplPathKindWalksToAModelAttachment(t *testing.T) {
	if got := scriptComplPathKind(flang.RefEntity, []string{"model", "body"}); got != flang.RefModel {
		t.Errorf("kind = %v, want model", got)
	}
	// And back out again, the way `@sec.edge.<id>.sector` does.
	if got := scriptComplPathKind(flang.RefEntity, []string{"model", "body", "entity"}); got != flang.RefEntity {
		t.Errorf("kind = %v, want entity", got)
	}
}

// A chain that has already gone through `.prop.` is holding a value, not an
// object, so the only thing left to offer is a vector's components.
func TestScriptComplMemberOnAPropertyIsAVector(t *testing.T) {
	src, cursor := at("@self.prop.position.|")
	ctx := scriptComplContext(src, cursor)
	if ctx.Kind != complMember || !ctx.Value {
		t.Fatalf("ctx = %+v", ctx)
	}
	var names []string
	for _, it := range scriptComplItems(ctx, scriptComplSource{}) {
		names = append(names, it.Text)
	}
	if strings.Join(names, "") != "xyz" {
		t.Fatalf("items = %v", names)
	}
}

// The access modes and the members every ref answers come first, because they
// are the ones that are right whatever the object turns out to be.
func TestScriptComplMemberOffersTheAccessModes(t *testing.T) {
	src, cursor := at("@door.|")
	items := scriptComplItems(scriptComplContext(src, cursor), scriptComplSource{})
	var names []string
	for _, it := range items {
		names = append(names, it.Text)
	}
	for _, want := range []string{"prop", "attr", "state", "id", "kind"} {
		if !contains(names, want) {
			t.Errorf("member list %v is missing %q", names, want)
		}
	}
}

// typeInto drives scriptComplUpdate the way a frame does: the document holds
// the text, the caret is where the widget last reported it.
func typeInto(d *scriptDoc, text string, cursor int) {
	d.Text = text
	scriptBufCursor = cursor
	scriptComplUpdate(d)
}

// The list is opened by typing and by nothing else. A caret arriving by arrow
// key must leave the arrow keys alone, or moving between lines costs an Escape
// on every line that happens to end in a word.
func TestScriptComplOpensOnlyOnTyping(t *testing.T) {
	scriptComplClose()
	scriptComplState.docID, scriptComplState.src, scriptComplState.cursor = 0, "", -1
	d := &scriptDoc{id: 4242}

	// The buffer as the editor first sees it, caret at the end of a word.
	typeInto(d, "x = lo", 6)
	if scriptComplState.Open {
		t.Fatalf("a list opened without a keystroke: %+v", scriptComplState.Items)
	}

	// An arrow key away, and an arrow key back onto the end of that word.
	// Neither is a keystroke, and neither may take the arrow keys away.
	typeInto(d, "x = lo", 2)
	typeInto(d, "x = lo", 6)
	if scriptComplState.Open {
		t.Fatalf("moving the caret onto a word opened the list: %+v", scriptComplState.Items)
	}

	// One character typed, and the list is there.
	typeInto(d, "x = log", 7)
	if !scriptComplState.Open {
		t.Fatal("typing did not open the list")
	}

	// Moving off the word puts it away, rather than leaving rows filtered
	// against a caret that has gone.
	typeInto(d, "x = log", 3)
	if scriptComplState.Open {
		t.Fatal("moving the caret left the list up")
	}
	scriptComplClose()
}

// Ctrl+Space is the way to ask for a list without typing, and it still works
// when nothing has changed but the request itself.
func TestScriptComplForceOpensWithoutTyping(t *testing.T) {
	scriptComplClose()
	scriptComplState.docID, scriptComplState.src, scriptComplState.cursor = 0, "", -1
	d := &scriptDoc{id: 4243}

	typeInto(d, "x = lo", 6)
	if scriptComplState.Open {
		t.Fatal("a list opened without a keystroke")
	}
	scriptComplState.force = true
	typeInto(d, "x = lo", 6)
	if !scriptComplState.Open {
		t.Fatal("Ctrl+Space did not open the list")
	}
	scriptComplClose()
}

func contains(list []string, s string) bool {
	for _, c := range list {
		if c == s {
			return true
		}
	}
	return false
}
