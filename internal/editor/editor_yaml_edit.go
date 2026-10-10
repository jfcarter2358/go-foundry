package editor

// editor_yaml_edit.go — the level YAML editor's text operations, as functions
// from (text, selection) to (text, selection).
//
// Every change the window makes to the buffer — an indent, a comment toggle, an
// undo, a replace, a menu Cut — is one of these. Whether it then reaches the
// buffer through the text widget's callback (while the widget holds the
// keyboard and owns the text) or straight into the document (while it does
// not) is the window's business, decided in one place (yamlRunOp); the
// operations themselves are plain string functions a test can call.

import (
	"strings"

	"foundry/internal/imguibackend"
)

// yamlIndentUnit is one level of indentation. Two spaces: what the level writer
// emits, and YAML forbids tabs outright.
const yamlIndentUnit = "  "

// yamlRecordMode says how an operation's change enters the undo history.
type yamlRecordMode uint8

const (
	// yamlRecordTyping may merge with the typing around it.
	yamlRecordTyping yamlRecordMode = iota
	// yamlRecordSealed is a step of its own.
	yamlRecordSealed
	// yamlRecordNone is not recorded — an undo or redo, which moves through
	// the history rather than adding to it.
	yamlRecordNone
)

// yamlOp is one operation. ok false means it had nothing to do.
type yamlOp struct {
	Name   string
	Record yamlRecordMode
	// NeedsSel says the operation reads the widget's selection, so it has to
	// run in the widget's callback even when it could otherwise go straight
	// into the document.
	NeedsSel bool
	Fn       func(text string, sel scriptSel) (out string, nsel scriptSel, ok bool)
}

// yamlLineRange returns the byte range of the whole lines a selection touches:
// from the start of its first line to the end of its last. A selection ending
// at the very start of a line does not take that line — selecting three lines
// by dragging down to the start of the fourth means three.
func yamlLineRange(text string, sel scriptSel) (from, to int) {
	sel = sel.ordered(len(text))
	end := sel.End
	if end > sel.Start && end > 0 && text[end-1] == '\n' {
		end--
	}
	from = strings.LastIndexByte(text[:sel.Start], '\n') + 1
	to = len(text)
	if i := strings.IndexByte(text[end:], '\n'); i >= 0 {
		to = end + i
	}
	return from, to
}

// yamlIndent is Tab: with no selection, spaces to the next indent stop; with
// one, every line it touches moved in a level.
func yamlIndent(text string, sel scriptSel) (string, scriptSel, bool) {
	sel = sel.ordered(len(text))
	if sel.empty() {
		ls := strings.LastIndexByte(text[:sel.Start], '\n') + 1
		n := len(yamlIndentUnit) - (sel.Start-ls)%len(yamlIndentUnit)
		pad := strings.Repeat(" ", n)
		at := sel.Start + n
		return text[:sel.Start] + pad + text[sel.Start:], scriptSel{at, at}, true
	}
	from, to := yamlLineRange(text, sel)
	lines := strings.Split(text[from:to], "\n")
	added := 0
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue // blank lines stay blank rather than growing trailing spaces
		}
		lines[i] = yamlIndentUnit + l
		added += len(yamlIndentUnit)
	}
	block := strings.Join(lines, "\n")
	start := sel.Start
	if strings.TrimSpace(text[from:min(from+len(lines[0]), to)]) != "" {
		start += len(yamlIndentUnit)
	}
	return text[:from] + block + text[to:], scriptSel{start, sel.End + added}, true
}

// yamlDedent is Shift+Tab: every line the selection (or the caret) touches
// moved out a level, as far as its indentation allows.
func yamlDedent(text string, sel scriptSel) (string, scriptSel, bool) {
	sel = sel.ordered(len(text))
	from, to := yamlLineRange(text, sel)
	lines := strings.Split(text[from:to], "\n")
	removed, first := 0, 0
	for i, l := range lines {
		n := 0
		for n < len(yamlIndentUnit) && n < len(l) && l[n] == ' ' {
			n++
		}
		lines[i] = l[n:]
		removed += n
		if i == 0 {
			first = n
		}
	}
	if removed == 0 {
		return text, sel, false
	}
	block := strings.Join(lines, "\n")
	start := max(sel.Start-first, from)
	end := max(sel.End-removed, start)
	if sel.empty() {
		end = start
	}
	return text[:from] + block + text[to:], scriptSel{start, end}, true
}

// yamlToggleComment is Ctrl+/: comment out every line the selection touches,
// or — when every non-blank one already is — uncomment them. The `# ` goes at
// the shallowest line's indent, so a commented block keeps its shape and reads
// as the block it was.
func yamlToggleComment(text string, sel scriptSel) (string, scriptSel, bool) {
	sel = sel.ordered(len(text))
	from, to := yamlLineRange(text, sel)
	lines := strings.Split(text[from:to], "\n")
	all, minIndent := true, -1
	for _, l := range lines {
		t := strings.TrimLeft(l, " ")
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "#") {
			all = false
		}
		if in := len(l) - len(t); minIndent < 0 || in < minIndent {
			minIndent = in
		}
	}
	if minIndent < 0 {
		return text, sel, false
	}
	delta := 0
	for i, l := range lines {
		t := strings.TrimLeft(l, " ")
		if t == "" {
			continue
		}
		in := len(l) - len(t)
		if all {
			cut := 1
			if strings.HasPrefix(t, "# ") {
				cut = 2
			}
			lines[i] = l[:in] + t[cut:]
			delta -= cut
		} else {
			lines[i] = l[:minIndent] + "# " + l[minIndent:]
			delta += 2
		}
	}
	block := strings.Join(lines, "\n")
	out := text[:from] + block + text[to:]
	if sel.empty() {
		// The caret stays on the same character it was on.
		first := strings.TrimLeft(strings.Split(text[from:to], "\n")[0], " ")
		shift := 0
		if first != "" {
			shift = 2
			if all {
				shift = -1
				if strings.HasPrefix(first, "# ") {
					shift = -2
				}
			}
		}
		at := min(max(sel.Start+shift, from), from+len(strings.Split(block, "\n")[0]))
		return out, scriptSel{at, at}, true
	}
	return out, scriptSel{from, from + len(block)}, true
}

// yamlAutoIndent is the indent a new line should start with, given the line
// the newline was typed at the end of: the same as that line's content, and a
// level deeper after a key that opens a block. A `- key:` line's content is
// its key, so the next key of the same list item lines up under it.
func yamlAutoIndent(prev string) string {
	s := yamlShapeOf(prev)
	if s.Blank {
		return strings.Repeat(" ", s.Indent)
	}
	col := s.Indent
	if s.KeyCol >= 0 {
		col = s.KeyCol
		if s.BlockValue() || s.BlockScalar() {
			col += len(yamlIndentUnit)
		}
	} else if len(s.Dashes) > 0 {
		// A scalar list item: the next item is a sibling at the dash.
		col = s.Dashes[len(s.Dashes)-1]
	}
	return strings.Repeat(" ", col)
}

// yamlSelectOp selects [start, end) — a find match, or the line a problem is
// on.
func yamlSelectOp(start, end int) yamlOp {
	return yamlOp{Name: "select", Record: yamlRecordNone, NeedsSel: true,
		Fn: func(text string, _ scriptSel) (string, scriptSel, bool) {
			s := scriptSel{min(start, len(text)), min(end, len(text))}
			return text, s, true
		}}
}

// yamlClipboardOp wraps the script editor's cut/copy/paste/delete/select-all
// (scriptEditFor), so the menu and the shortcuts they list behave the same in
// both editors.
func yamlClipboardOp(op scriptEditOp) yamlOp {
	return yamlOp{Name: "edit", Record: yamlRecordSealed, NeedsSel: true,
		Fn: func(text string, sel scriptSel) (string, scriptSel, bool) {
			e, ok := scriptEditFor(op, text, sel, imguibackend.ClipboardText())
			if !ok {
				return text, sel, false
			}
			if e.SetClip {
				imguibackend.SdlClipboard{}.SetText(e.Clip)
			}
			return scriptApplyText(text, e), e.Sel, true
		}}
}

// yamlTextOp replaces the whole text — a Replace All, a reload, a pull from the
// level editor — leaving the caret at caret. The splice that reaches the widget
// is only the part that differs.
func yamlTextOp(name string, text string, caret int) yamlOp {
	return yamlOp{Name: name, Record: yamlRecordSealed,
		Fn: func(string, scriptSel) (string, scriptSel, bool) {
			c := min(max(caret, 0), len(text))
			return text, scriptSel{c, c}, true
		}}
}
