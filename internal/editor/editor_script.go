package editor

// editor_script.go — the script editor's document: the buffer, its file, and
// what the compiler thinks of it.
//
// Kept apart from the ImGui code (editor_script_ui.go) and from the window
// lifecycle (editor_script_window.go) for the reason internal/texdoc was split
// out of the texture editor: the part that has opinions about text is worth
// being able to test without a window, and the part that talks to GL is not.
//
// Diagnostics are recomputed on a debounce rather than on every keystroke.
// Parsing is fast, but re-parsing a file on each character typed would mean the
// error list flickering through every half-written state, which is noise rather
// than feedback.

import (
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/script"
	"foundry/internal/world"
)

// scriptDocDebounceMS is how long the buffer must go unedited before it is
// re-parsed. Long enough not to fight the typist, short enough to feel live.
const scriptDocDebounceMS = 300

// scriptDoc is one open script.
type scriptDoc struct {
	// id names this document to ImGui, and never changes. The buffer's widget
	// state — its cursor, its selection, its undo stack — is keyed by widget
	// ID, so two tabs sharing one would swap cursors as you switched between
	// them, and Ctrl+Z in one would undo an edit made in the other. Path
	// cannot do this job: Save As renames a document, and a tab whose ID
	// changed would lose exactly that state at exactly the wrong moment.
	id int

	// Path is relative to the scripts root, as a level would write it. Empty
	// means the buffer has never been saved.
	Path string
	Text string
	// saved is the text as it was last written, so Dirty is a comparison rather
	// than a flag that can drift out of step with the buffer.
	saved string

	// Diags is the last parse's diagnostics, and diagLines indexes them by line
	// so the gutter can look one up without scanning.
	Diags     flang.ErrorList
	diagLines map[int][]flang.Error

	// Issues are the level-aware checks: names the level does not have,
	// animations an entity never declared, textures with no file. Kept apart
	// from Diags because they are a different kind of answer — a parse error
	// means the script cannot run, an issue means it will run and do nothing —
	// and because they are only as true as whichever level happens to be open.
	// See script_lint.go.
	Issues     []script.ScriptIssue
	issueLines map[int][]script.ScriptIssue

	// dirtyAt is when the buffer last changed, for the debounce. Zero means the
	// diagnostics are current.
	dirtyAt uint32

	// scopeFuncs and scopeParams are the names this file's imports bring into
	// scope, kept from the last parse. The completion list reads them: walking
	// the import graph means opening other files, which is not work to do on a
	// keystroke, and a name an import declares does not change while this
	// buffer is being typed into.
	scopeFuncs  []string
	scopeParams []string

	// hlSrc, hlLines and hlWidth are the colouring cache: the text it was made
	// from, the classified lines, and the width of the longest one. Keyed on
	// the text rather than on the debounce, because colour that lagged the
	// cursor would read as a bug. See editor_script_hl.go.
	hlSrc   string
	hlLines [][]scriptHLChunk
	hlWidth float32
}

// scriptDocSeq hands out document IDs. Monotonic and never reused, so a
// closed tab's widget state cannot be inherited by the next document opened.
var scriptDocSeq int

// NewScriptDoc returns an empty, unsaved document.
func NewScriptDoc() *scriptDoc {
	scriptDocSeq++
	return &scriptDoc{
		id:         scriptDocSeq,
		Text:       scriptStarterText,
		diagLines:  map[int][]flang.Error{},
		issueLines: map[int][]script.ScriptIssue{},
	}
}

// scriptStarterText is what a new script opens on. A working handler rather than
// an empty file: the shape of a clause is the one thing a new author cannot
// guess, and this is cheaper to read than the documentation.
const scriptStarterText = `-- A new script.
--
-- A handler's first parameter is the atom naming the message. See SCRIPTING.md
-- for the full list; :spawn:, :tick:, :enter: and :interact: are the common ones.
--
-- A top-level ` + "`param name type`" + ` declares a value the level supplies, per
-- object, and reads back as @name — which is what lets one file drive many
-- objects. Uncomment the line below and the Script panel grows a picker for it.

-- param target ref

def on_spawn(:spawn:) -> null
    log("hello from ", @self.id)
end
`

// Name returns the document's display name.
func (d *scriptDoc) Name() string {
	if d.Path == "" {
		return "(unsaved)"
	}
	return d.Path
}

// TabName is the document's name with its folders dropped — what a tab shows,
// since a tab strip has room for `door.flang` and not for
// `scripts/props/doors/door.flang`.
func (d *scriptDoc) TabName() string {
	if d.Path == "" {
		return "(unsaved)"
	}
	if i := strings.LastIndex(d.Path, "/"); i >= 0 {
		return d.Path[i+1:]
	}
	return d.Path
}

// Dirty reports whether the buffer differs from what was last saved.
func (d *scriptDoc) Dirty() bool { return d.Text != d.saved }

// SetText replaces the buffer and starts the debounce.
func (d *scriptDoc) SetText(text string, nowMS uint32) {
	if d.Text == text {
		return
	}
	d.Text = text
	d.dirtyAt = nowMS
	if d.dirtyAt == 0 {
		// Zero means "nothing pending", so a clock that genuinely reads zero
		// must not look like a document with no edits waiting.
		d.dirtyAt = 1
	}
}

// Reparse recomputes the diagnostics if the debounce has elapsed.
//
// Only the file itself is parsed, not its imports: an import that fails to
// resolve is a runtime concern, and pulling other files in here would attribute
// their errors to the buffer being edited.
//
// lvl is what the level-aware checks run against, and may be nil — the script
// editor can be open with no level, in which case they are simply skipped.
func (d *scriptDoc) Reparse(nowMS uint32, lvl *world.Level) {
	if d.dirtyAt == 0 || nowMS-d.dirtyAt < scriptDocDebounceMS {
		return
	}
	d.dirtyAt = 0
	d.reparseNow(lvl)
}

// reparseNow parses unconditionally, for a save or an explicit request.
func (d *scriptDoc) reparseNow(lvl *world.Level) {
	name := d.Path
	if name == "" {
		name = "(unsaved)"
	}
	f, errs := flang.Parse(name, []byte(d.Text))
	d.Diags = errs
	d.diagLines = map[int][]flang.Error{}
	for _, e := range errs {
		d.diagLines[e.Pos.Line] = append(d.diagLines[e.Pos.Line], e)
	}

	// Linting a file that did not parse would report on whatever fragment the
	// recovery managed to build, which is noise on top of the real errors.
	d.Issues = nil
	d.issueLines = map[int][]script.ScriptIssue{}
	scope, params := scriptScopeNames(f, name)
	// The parameters a decision tree beside this script declares count as this
	// script's — see internal/script/script_aitree.go. Without this, a script
	// whose numbers live in a .fai is underlined from end to end.
	params = script.AddAITreeParams(params, d.Path)
	d.scopeFuncs, d.scopeParams = sortedKeys(scope), sortedKeys(params)
	if len(errs) == 0 {
		d.Issues = script.LintScript(lvl, f, scope, params)
		for _, s := range d.Issues {
			d.issueLines[s.Pos.Line] = append(d.issueLines[s.Pos.Line], s)
		}
	}
}

// ScopeFuncs returns the function names this file's imports bring into scope,
// as of the last parse.
func (d *scriptDoc) ScopeFuncs() []string { return d.scopeFuncs }

// ScopeParams returns the parameter names this file's imports declare, as of
// the last parse. A script's own are read live from the buffer instead — see
// scriptComplScanFile.
func (d *scriptDoc) ScopeParams() []string { return d.scopeParams }

// sortedKeys renders a name set as a stable list.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DiagnosticsOn returns the diagnostics for one 1-based line.
func (d *scriptDoc) DiagnosticsOn(line int) []flang.Error { return d.diagLines[line] }

// IssuesOn returns the level-aware issues for one 1-based line.
func (d *scriptDoc) IssuesOn(line int) []script.ScriptIssue { return d.issueLines[line] }

// HasErrors reports whether the last parse found anything.
func (d *scriptDoc) HasErrors() bool { return len(d.Diags) > 0 }

// HasIssues reports whether the last lint found anything.
func (d *scriptDoc) HasIssues() bool { return len(d.Issues) > 0 }

// Lines splits the buffer for the gutter. Cheap enough at the size a gameplay
// script reaches that caching it would be more code than it saves.
func (d *scriptDoc) Lines() []string { return strings.Split(d.Text, "\n") }

// Save writes the buffer to path, which is relative to the scripts root.
func (d *scriptDoc) Save(path string) error {
	if path == "" {
		path = d.Path
	}
	if path == "" {
		return errScriptNoPath
	}
	if !strings.HasSuffix(path, fileutil.ScriptExt) {
		path += fileutil.ScriptExt
	}
	full := fileutil.ScriptPath(path)
	if err := fileutil.WriteFileAtomic(full, []byte(d.Text)); err != nil {
		return err
	}
	d.Path = fileutil.ScriptsDirName + "/" + strings.TrimPrefix(filepath.ToSlash(path), fileutil.ScriptsDirName+"/")
	d.saved = d.Text
	d.reparseNow(Editor.level)
	// The saved file is what every object panel reads its `param` rows from, and
	// this edit may have added, renamed or retyped one.
	script.ScriptParamsInvalidate(d.Path)
	return nil
}

// OpenScriptDoc reads a script into a new document. name is as a level writes
// it — with or without the scripts/ prefix and the extension.
func OpenScriptDoc(name string) (*scriptDoc, error) {
	src, err := script.ReadScriptFile(fileutil.ScriptPath(name))
	if err != nil {
		return nil, err
	}
	scriptDocSeq++
	d := &scriptDoc{
		id:         scriptDocSeq,
		Path:       fileutil.ScriptsDirName + "/" + strings.TrimPrefix(strings.TrimSuffix(filepath.ToSlash(name), fileutil.ScriptExt), fileutil.ScriptsDirName+"/") + fileutil.ScriptExt,
		Text:       string(src),
		diagLines:  map[int][]flang.Error{},
		issueLines: map[int][]script.ScriptIssue{},
	}
	d.saved = d.Text
	d.reparseNow(Editor.level)
	return d, nil
}

// scriptScopeNames returns every name the buffer brings into scope: the function
// names it may call, and the parameters `@name` may address — its own of each,
// plus everything its imports merge in.
//
// A nil funcs map means "do not check calls at all", which is what an unreadable
// import produces. The alternative — checking against a partial set — would
// report every function from the missing file as undefined, and a linter that is
// wrong when a path is wrong is a linter nobody trusts when it is right. The
// params map is returned regardless: an unknown param is never *reported*, only
// used to suppress a report, so a partial set can only under-suppress.
func scriptScopeNames(f *flang.File, fromPath string) (funcs, params map[string]bool) {
	if f == nil {
		return nil, nil
	}
	out := map[string]bool{}
	params = map[string]bool{}
	seen := map[string]bool{}
	ok := true

	var walk func(file *flang.File, path string)
	walk = func(file *flang.File, path string) {
		for _, g := range file.Funcs {
			out[g.Name] = true
		}
		for _, d := range file.Params {
			params[d.Name] = true
		}
		for _, imp := range file.Imports {
			resolved, err := script.ResolveScriptImport(fileutil.ScriptPath(path), imp.Path)
			if err != nil {
				ok = false
				continue
			}
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			src, err := script.ReadScriptFile(resolved)
			if err != nil {
				ok = false
				continue
			}
			// Errors are discarded on purpose: an import that does not parse is
			// the *other* file's problem, reported when it is opened. What is
			// wanted here is only the names it defines.
			imported, _ := flang.Parse(resolved, src)
			if imported == nil {
				ok = false
				continue
			}
			walk(imported, resolved)
		}
	}
	walk(f, fromPath)

	if !ok {
		return nil, params
	}
	return out, params
}

// errScriptNoPath is what saving an unnamed buffer without a path reports.
var errScriptNoPath = scriptError("this script has no name yet; use Save As")

type scriptError string

func (e scriptError) Error() string { return string(e) }
