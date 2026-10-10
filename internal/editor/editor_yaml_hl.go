package editor

// editor_yaml_hl.go — the level YAML editor's colouring.
//
// A line at a time, and only the lines on screen. The script editor colours
// its whole buffer on every edit and keeps the result; a level is a hundred
// thousand lines, and doing that per keystroke would be tens of milliseconds
// of stall for colour nobody can see. A YAML line can be coloured on its own —
// the one exception, a block scalar's text (`key: |`), is spotted by looking a
// few lines up — so there is nothing to keep.
//
// The chunks are the script editor's scriptHLChunk, classed with flang's
// classes, so the palette, the caret and the overlay drawing are shared rather
// than copied: a key is drawn in the colour a script draws an `@address`, a
// value in a string's, true/false/null in a keyword's.

import (
	"strconv"
	"strings"

	"foundry/internal/flang"
)

// The YAML roles, by the flang class whose colour they borrow.
const (
	yamlClassKey     = flang.ClassAddress
	yamlClassString  = flang.ClassString
	yamlClassNumber  = flang.ClassNumber
	yamlClassConst   = flang.ClassKeyword // true, false, null, ---
	yamlClassPunct   = flang.ClassOperator
	yamlClassComment = flang.ClassComment
	yamlClassAnchor  = flang.ClassParam // &anchor, *alias
	yamlClassTag     = flang.ClassType  // !!str
)

// yamlHLBuilder accumulates one line's chunks, merging neighbours of the same
// class so the overlay draws as few runs as it can.
type yamlHLBuilder struct {
	out []scriptHLChunk
}

func (b *yamlHLBuilder) add(text string, class flang.Class) {
	if text == "" {
		return
	}
	if n := len(b.out); n > 0 && b.out[n-1].Class == class {
		b.out[n-1].Text += text
		return
	}
	b.out = append(b.out, scriptHLChunk{Text: text, Class: class})
}

// yamlHighlightLine colours one line. inBlock says the line is a block
// scalar's text, which is all string whatever it looks like.
func yamlHighlightLine(line string, inBlock bool) []scriptHLChunk {
	var b yamlHLBuilder
	if inBlock {
		b.add(line, yamlClassString)
		return b.out
	}
	indent, blank := yamlIndentOf(line)
	b.add(line[:indent], flang.ClassPlain)
	rest := line[indent:]
	if blank {
		if strings.HasPrefix(rest, "#") {
			b.add(rest, yamlClassComment)
		} else {
			b.add(rest, yamlClassConst) // a document marker
		}
		return b.out
	}

	i := indent
	for i < len(line) && line[i] == '-' && (i+1 == len(line) || line[i+1] == ' ') {
		j := i + 1
		for j < len(line) && line[j] == ' ' {
			j++
		}
		b.add(line[i:i+1], yamlClassPunct)
		b.add(line[i+1:j], flang.ClassPlain)
		i = j
	}
	if _, keyEnd, colon, ok := yamlKeyAt(line, i); ok {
		b.add(line[i:keyEnd], yamlClassKey)
		b.add(line[keyEnd:colon], flang.ClassPlain)
		b.add(":", yamlClassPunct)
		i = colon + 1
	}
	yamlHighlightValue(&b, line, i, false)
	return b.out
}

// yamlHighlightValue colours the rest of a line from i: scalars, quoted
// strings, flow collections, anchors, tags and a trailing comment. flow says
// the text is inside `{}` or `[]`, where a comma ends a scalar and a scalar
// followed by a colon is a key.
func yamlHighlightValue(b *yamlHLBuilder, line string, i int, flow bool) {
	depth := 0
	if flow {
		depth = 1
	}
	for i < len(line) {
		c := line[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			j := i
			for j < len(line) && (line[j] == ' ' || line[j] == '\t' || line[j] == '\r') {
				j++
			}
			b.add(line[i:j], flang.ClassPlain)
			i = j
		case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			b.add(line[i:], yamlClassComment)
			return
		case c == '{' || c == '[':
			depth++
			b.add(line[i:i+1], yamlClassPunct)
			i++
		case (c == '}' || c == ']') && depth > 0:
			depth--
			b.add(line[i:i+1], yamlClassPunct)
			i++
		case c == ',' && depth > 0:
			b.add(",", yamlClassPunct)
			i++
		case c == ':' && (i+1 == len(line) || line[i+1] == ' ' || depth > 0):
			b.add(":", yamlClassPunct)
			i++
		case c == '"' || c == '\'':
			end := yamlQuoteEnd(line, i)
			if end < 0 {
				end = len(line)
			}
			class := yamlClassString
			if depth > 0 && yamlFollowedByColon(line, end) {
				class = yamlClassKey
			}
			b.add(line[i:end], class)
			i = end
		case c == '&' || c == '*':
			j := yamlScalarEnd(line, i+1, depth > 0)
			b.add(line[i:j], yamlClassAnchor)
			i = j
		case c == '!':
			j := i
			for j < len(line) && line[j] != ' ' {
				j++
			}
			b.add(line[i:j], yamlClassTag)
			i = j
		case (c == '|' || c == '>') && depth == 0 && strings.TrimSpace(line[:i]) != "" &&
			strings.HasSuffix(strings.TrimRight(line[:i], " "), ":"):
			// A block scalar's header: `|`, `>-`, `|2`.
			j := i + 1
			for j < len(line) && strings.IndexByte("+-0123456789", line[j]) >= 0 {
				j++
			}
			b.add(line[i:j], yamlClassPunct)
			i = j
		default:
			j := yamlScalarEnd(line, i, depth > 0)
			if j == i {
				j = i + 1
			}
			word := strings.TrimRight(line[i:j], " ")
			class := yamlScalarClass(word)
			if depth > 0 && yamlFollowedByColon(line, i+len(word)) {
				class = yamlClassKey
			}
			b.add(word, class)
			b.add(line[i+len(word):j], flang.ClassPlain)
			i = j
		}
	}
}

// yamlScalarEnd finds where a plain scalar starting at i stops: at a ` #`
// comment, at a `: ` that makes it a key, or — in a flow collection — at a
// comma or a closing bracket.
func yamlScalarEnd(line string, i int, flow bool) int {
	for j := i; j < len(line); j++ {
		switch c := line[j]; {
		case c == '#' && j > i && line[j-1] == ' ':
			return j
		case c == ':' && (j+1 == len(line) || line[j+1] == ' ' || flow):
			return j
		case flow && (c == ',' || c == '}' || c == ']' || c == '{' || c == '['):
			return j
		}
	}
	return len(line)
}

// yamlFollowedByColon reports whether the next non-space character from i is a
// colon — what makes a scalar inside `{}` a key.
func yamlFollowedByColon(line string, i int) bool {
	for i < len(line) && line[i] == ' ' {
		i++
	}
	return i < len(line) && line[i] == ':'
}

// yamlScalarClass says what a plain scalar reads as.
func yamlScalarClass(word string) flang.Class {
	if yamlBoolWords[word] {
		return yamlClassConst
	}
	switch strings.ToLower(word) {
	case ".inf", "-.inf", "+.inf", ".nan":
		return yamlClassNumber
	}
	if _, err := strconv.ParseFloat(word, 64); err == nil {
		return yamlClassNumber
	}
	if _, err := strconv.ParseInt(word, 0, 64); err == nil {
		return yamlClassNumber
	}
	return yamlClassString
}

// yamlInBlockScalar reports whether line is part of a block scalar's text.
//
// A block scalar's text is every following line indented further than its
// key, so the question is answered by looking up past deeper lines to the
// first shallower one that is structure: if that line is `key: |`, this is its
// text. Content lines shallower than this one are stepped past — a literal can
// indent however it likes below its first line.
func yamlInBlockScalar(ix *yamlLineIndex, line int) bool {
	indent, blank := yamlIndentOf(ix.Line(line))
	if blank {
		return false
	}
	for l := line - 1; l >= 0 && indent > 0; l-- {
		raw := ix.Line(l)
		in, bl := yamlIndentOf(raw)
		if bl || in >= indent {
			continue
		}
		s := yamlShapeOf(raw)
		if s.KeyCol >= 0 {
			return s.BlockScalar() && s.KeyCol < indent
		}
		if len(s.Dashes) > 0 {
			return false
		}
		indent = in // more of the literal, less indented; keep looking up
	}
	return false
}
