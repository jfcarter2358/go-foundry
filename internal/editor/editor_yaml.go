package editor

// editor_yaml.go — the level YAML editor's document: the file, its text, and
// what the loader will make of it.
//
// A level is normally edited through the level editor, which writes the YAML
// for you. This is for the times the file itself is the quicker tool: a value
// changed on forty entities at once, a block pasted from another level, a key
// the panels do not expose yet. So saving is not only a write — when the file
// is the one the level editor has open, the level on screen is rebuilt from it
// (editorBeginReloadLevel), as a single step the level editor's Ctrl+Z takes
// back.
//
// What the loader will make of the text is checked off the main thread. level1
// is two megabytes, and yaml.v2 takes a noticeable fraction of a second over
// it; a check run on the frame would stall the window every time the typist
// paused. It runs on a copy of the text in a goroutine, and its answer is
// adopted only if the text has not moved on since.
//
// The same window also edits the project's game.yaml (internal/game), toggled
// from the toolbar. It is held to a stricter rule than a level: the game reads
// it with UnmarshalStrict and refuses to boot on a key it does not know, so
// what is a warning in a level is an error there.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/game"
	"foundry/internal/levelio"
	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

// yamlCheckDebounceMS is how long the buffer must sit still before it is
// checked. Longer than the script editor's, because a check here costs more.
const yamlCheckDebounceMS = 500

// yamlDiag is one thing the check found. Line is 1-based, and 0 for a problem
// with no one place — the whole-level validation.
type yamlDiag struct {
	Line int
	Msg  string
	// Warn marks something the loader tolerates — a key it does not know, a
	// key given twice — as against something that stops the file loading.
	Warn bool
}

// yamlDocKind is which schema a document is written against.
type yamlDocKind uint8

const (
	yamlKindLevel yamlDocKind = iota
	yamlKindGame
)

// yamlCheckResult is one finished check.
type yamlCheckResult struct {
	gen int
	// of is the document the check was started for: the window toggles
	// between two, and a check of one must not land on the other.
	of    *yamlDoc
	Diags []yamlDiag
	// Raw is what the text decodes to, and Errors whether anything stops it
	// loading. Raw is only meaningful when Errors is false.
	Raw    world.RawLevel
	Errors bool
	// rawTaken says Raw has been handed to the level editor to build, which
	// may change it as it does; a second save re-checks rather than hand the
	// same RawLevel out again.
	rawTaken bool
}

// yamlDoc is the open file.
type yamlDoc struct {
	Kind yamlDocKind
	// Path is the file on disk, as the level editor names levels. Empty for a
	// buffer that has never been saved.
	Path  string
	Text  string
	saved string
	// modTime is the file's modification time as of the last read or write,
	// for noticing when something else — the level editor's Save, a generator
	// script — writes it underneath.
	modTime time.Time
	// diskChanged is raised when that happens to a buffer with unsaved edits,
	// which cannot simply be reloaded.
	diskChanged bool

	// gen counts edits. A check is for one generation; the text has moved on
	// when the numbers differ.
	gen     int
	dirtyAt uint32

	// ix is the line index for the current text, rebuilt lazily.
	ix *yamlLineIndex

	check    *yamlCheckResult
	diagLine map[int]*yamlDiag

	undo yamlUndo

	// caret is where the caret was when the window last showed another
	// document, so toggling back returns to it.
	caret int
}

// Name is the file's name for the title and the tab.
func (d *yamlDoc) Name() string {
	if d.Path == "" {
		return "(unsaved)"
	}
	return filepath.Base(d.Path)
}

// Dirty reports whether the buffer differs from the file.
func (d *yamlDoc) Dirty() bool { return d.Text != d.saved }

// Index returns the line index for the current text.
func (d *yamlDoc) Index() *yamlLineIndex {
	if d.ix == nil || d.ix.src != d.Text {
		d.ix = newYAMLLineIndex(d.Text)
	}
	return d.ix
}

// setText replaces the buffer and starts the debounce. It does not touch the
// undo history — the caller records the edit, knowing what kind it was.
func (d *yamlDoc) setText(text string, nowMS uint32) {
	if d.Text == text {
		return
	}
	d.Text = text
	d.gen++
	d.dirtyAt = max(nowMS, 1)
}

// DiagOn returns the problem on a 1-based line, errors ahead of warnings.
func (d *yamlDoc) DiagOn(line int) *yamlDiag { return d.diagLine[line] }

// Diags returns the last check's findings.
func (d *yamlDoc) Diags() []yamlDiag {
	if d.check == nil {
		return nil
	}
	return d.check.Diags
}

// Checked reports whether the findings are for the text as it is now.
func (d *yamlDoc) Checked() bool { return d.check != nil && d.check.gen == d.gen }

// adopt installs a finished check.
func (d *yamlDoc) adopt(r *yamlCheckResult) {
	d.check = r
	d.diagLine = map[int]*yamlDiag{}
	for i := range r.Diags {
		dg := &r.Diags[i]
		if dg.Line <= 0 {
			continue
		}
		if prev, ok := d.diagLine[dg.Line]; !ok || (prev.Warn && !dg.Warn) {
			d.diagLine[dg.Line] = dg
		}
	}
}

// ---------- reading and writing ----------

// openYAMLDoc reads a level file.
func openYAMLDoc(path string) (*yamlDoc, error) {
	return openYAMLDocKind(path, yamlKindLevel)
}

// openYAMLDocKind reads a file written against the given schema.
func openYAMLDocKind(path string, kind yamlDocKind) (*yamlDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := &yamlDoc{Path: path, Kind: kind}
	d.Text = imguiSafeText(string(data))
	d.saved = d.Text
	if fi, err := os.Stat(path); err == nil {
		d.modTime = fi.ModTime()
	}
	return d, nil
}

// imguiSafeText strips carriage returns, which the text widget would show as
// glyphs and which a level written on Windows is full of. The file keeps
// whichever line endings it is saved with — this buffer's, which are \n.
func imguiSafeText(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// errYAMLHasErrors is what saving a file that would not load reports.
var errYAMLHasErrors = errors.New("the file has errors the loader would refuse; fix them first (see the problems list)")

// Save checks the buffer and writes it. The check is the reason this is not a
// plain write: SaveLevel refuses to write a level that would not load back,
// and a file saved from here is held to the same rule — otherwise the next
// Open of it fails, and the last good version is gone. Warnings do not stop
// it; they describe a file that loads.
//
// The check that ran in the background is reused when it is for this text,
// which on a large level is most of the save's time saved.
func (d *yamlDoc) Save() (*yamlCheckResult, error) {
	if d.Path == "" {
		return nil, errors.New("this buffer has no file to save to — open a level first")
	}
	r := d.check
	if !d.Checked() || r.rawTaken {
		r = yamlCheckKind(d.Text, d.gen, d.Kind)
		d.adopt(r)
	}
	if r.Errors {
		return r, errYAMLHasErrors
	}
	if err := fileutil.WriteFileAtomic(d.Path, []byte(d.Text)); err != nil {
		return r, err
	}
	d.saved = d.Text
	d.diskChanged = false
	if fi, err := os.Stat(d.Path); err == nil {
		d.modTime = fi.ModTime()
	}
	return r, nil
}

// diskModTime is the file's modification time now, zero when it cannot be read.
func (d *yamlDoc) diskModTime() time.Time {
	if d.Path == "" {
		return time.Time{}
	}
	fi, err := os.Stat(d.Path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// sameLevelPath reports whether two paths name the same file.
func sameLevelPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return aa == bb
}

// ---------- the check ----------

// yamlLineRe finds the line number yaml.v2 puts in its messages.
var yamlLineRe = regexp.MustCompile(`line (\d+)`)

// yamlCheck decodes text the way the level loader does and reports everything
// that would go wrong, gen being the generation it was taken at.
//
// Decoded strictly, which is stricter than the loader: a key the schema does
// not have, or a key written twice, loads fine and is silently lost. Those are
// reported as warnings — the loader would open the file, but the author almost
// certainly meant something by the key — and everything else strict decoding
// reports is an error, because the loader would refuse it too.
func yamlCheck(text string, gen int) *yamlCheckResult {
	return yamlCheckKind(text, gen, yamlKindLevel)
}

// yamlCheckKind is yamlCheck against either schema. game.yaml is read strictly
// by the game itself, so there an unknown or repeated key is an error.
func yamlCheckKind(text string, gen int, kind yamlDocKind) *yamlCheckResult {
	r := &yamlCheckResult{gen: gen}
	var err error
	if kind == yamlKindGame {
		m := game.Default()
		err = yaml.UnmarshalStrict([]byte(text), &m)
	} else {
		err = yaml.UnmarshalStrict([]byte(text), &r.Raw)
	}
	var te *yaml.TypeError
	switch {
	case err == nil:
	case errors.As(err, &te):
		for _, msg := range te.Errors {
			dg := yamlDiagFrom(msg)
			dg.Warn = kind == yamlKindLevel && (strings.Contains(msg, "not found in type") ||
				strings.Contains(msg, "already set in map") ||
				strings.Contains(msg, "already defined"))
			r.Errors = r.Errors || !dg.Warn
			r.Diags = append(r.Diags, dg)
		}
	default:
		// A syntax error: nothing past it was read, so there is nothing more to
		// say about the file than this.
		dg := yamlDiagFrom(strings.TrimPrefix(err.Error(), "yaml: "))
		r.Diags = append(r.Diags, dg)
		r.Errors = true
		return r
	}
	if !r.Errors && kind == yamlKindGame {
		// Parse, not Validate alone: it trims the fields first, as the game does.
		if _, err := game.Parse([]byte(text)); err != nil {
			r.Diags = append(r.Diags, yamlDiag{Msg: err.Error()})
			r.Errors = true
		}
		return r
	}
	if !r.Errors {
		if err := levelio.ValidateRawLevel(&r.Raw); err != nil {
			r.Diags = append(r.Diags, yamlDiag{Msg: err.Error()})
			r.Errors = true
		}
	}
	return r
}

// yamlDiagFrom reads the line number out of one of yaml.v2's messages.
func yamlDiagFrom(msg string) yamlDiag {
	dg := yamlDiag{Msg: msg}
	if m := yamlLineRe.FindStringSubmatch(msg); m != nil {
		dg.Line, _ = strconv.Atoi(m[1])
		dg.Msg = strings.TrimSpace(strings.TrimPrefix(strings.Replace(msg, m[0], "", 1), ":"))
	}
	return dg
}

// yamlChecker runs one check at a time in the background.
type yamlChecker struct {
	mu      sync.Mutex
	running bool
	done    *yamlCheckResult
}

// start begins checking text unless a check is already running. The text is a
// Go string, so the goroutine's copy cannot change under it.
func (c *yamlChecker) start(d *yamlDoc) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return false
	}
	c.running = true
	text, gen, kind := d.Text, d.gen, d.Kind
	go func() {
		r := yamlCheckKind(text, gen, kind)
		r.of = d
		c.mu.Lock()
		c.done, c.running = r, false
		c.mu.Unlock()
	}()
	return true
}

// take returns a finished check, once.
func (c *yamlChecker) take() *yamlCheckResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.done
	c.done = nil
	return r
}

// Update runs the debounce: adopts a finished check, and starts one when the
// text has sat still long enough and is not already checked.
func (d *yamlDoc) Update(c *yamlChecker, nowMS uint32) {
	if r := c.take(); r != nil && r.of == d {
		// A check of text that has since changed is still adopted — stale
		// markers are better than none while the next check runs — and the
		// debounce below sees the generation mismatch and runs another.
		d.adopt(r)
		if r.gen != d.gen && d.dirtyAt == 0 {
			d.dirtyAt = max(nowMS, 1)
		}
	}
	if d.dirtyAt == 0 && d.check != nil {
		return
	}
	if d.dirtyAt != 0 && nowMS-d.dirtyAt < yamlCheckDebounceMS {
		return
	}
	if c.start(d) {
		d.dirtyAt = 0
	}
}

// ---------- pulling from the level editor ----------

// yamlLevelEditorText serialises the level the level editor has open, as its
// own Save would write it — the way to see edits made there that are not on
// disk yet.
func yamlLevelEditorText() (string, error) {
	if Editor.level == nil {
		return "", errors.New("no level is open in the level editor")
	}
	raw, err := editorLevelToRaw()
	if err != nil {
		return "", err
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return "", fmt.Errorf("yaml marshal: %w", err)
	}
	return string(data), nil
}
