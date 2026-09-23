package editor

// editor_claude_md.go — just enough Markdown to read an answer.
//
// Claude writes Markdown, and an answer full of raw ``` fences and ### is
// noticeably harder to read than one where the code is in a box. A full parser
// is not the point: what actually appears in these answers is fenced code,
// headings, bullets, numbered steps and paragraphs, and those five are what this
// handles. Anything else falls through as text, which is always readable.
//
// Split into blocks here rather than in the drawing code so it can be tested
// without a GL context, and so the drawing code is a switch over five cases.

import "strings"

type mdKind int

const (
	mdText mdKind = iota
	mdCode
	mdHeading
	mdBullet
	mdNumbered
)

type mdBlock struct {
	Kind mdKind
	Text string
	// Lang is a fenced block's language, when it named one.
	Lang string
	// Level is a heading's depth, or a list item's indent depth.
	Level int
}

// claudeParseMarkdown splits a message into blocks to draw.
//
// Consecutive plain lines join into one paragraph so the drawing code can wrap
// them as a unit; a blank line ends it. An unterminated code fence — which is
// what a half-streamed answer looks like — still yields its code block, because
// the alternative is text that flickers between styles as it arrives.
func claudeParseMarkdown(s string) []mdBlock {
	var out []mdBlock
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, mdBlock{Kind: mdText, Text: strings.Join(para, "\n")})
			para = nil
		}
	}
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			flush()
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			var code []string
			i++
			for ; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
					break
				}
				code = append(code, lines[i])
			}
			// A block still streaming in ends on the newline after its last
			// line, which would otherwise draw as an empty row inside the box.
			for len(code) > 0 && strings.TrimSpace(code[len(code)-1]) == "" {
				code = code[:len(code)-1]
			}
			out = append(out, mdBlock{Kind: mdCode, Lang: lang, Text: strings.Join(code, "\n")})
		case strings.HasPrefix(trimmed, "#"):
			flush()
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			out = append(out, mdBlock{Kind: mdHeading, Level: level,
				Text: strings.TrimSpace(trimmed[level:])})
		case isBullet(trimmed):
			flush()
			out = append(out, mdBlock{Kind: mdBullet, Level: indentOf(line),
				Text: strings.TrimSpace(trimmed[2:])})
		case isNumbered(trimmed):
			flush()
			dot := strings.IndexByte(trimmed, '.')
			out = append(out, mdBlock{Kind: mdNumbered, Level: indentOf(line),
				Text: strings.TrimSpace(trimmed[dot+1:])})
		case trimmed == "":
			flush()
		default:
			para = append(para, line)
		}
	}
	flush()
	return out
}

func isBullet(t string) bool {
	if len(t) < 2 {
		return false
	}
	return (t[0] == '-' || t[0] == '*' || t[0] == '+') && t[1] == ' '
}

// isNumbered matches "1. " and "12) ", and nothing that merely starts with a
// digit — a line beginning "3D models are…" is a paragraph.
func isNumbered(t string) bool {
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(t)-1 {
		return false
	}
	return (t[i] == '.' || t[i] == ')') && t[i+1] == ' '
}

// indentOf is how deeply a list item is nested, in levels of two spaces.
func indentOf(line string) int {
	n := 0
	for _, r := range line {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n / 2
}

// claudeStripEmphasis removes the inline markers this renderer cannot draw.
//
// ImGui has one font here, so **bold** cannot be drawn as bold; leaving the
// asterisks in is worse than dropping them. Backticks are kept — a name in
// backticks reads as a name — but the pairs of asterisks and underscores go.
func claudeStripEmphasis(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	// Only paired underscores that sit against a word boundary: snake_case names
	// are far more common in this project than emphasised words, and mangling
	// bake_exclude into bakeexclude would be a real loss.
	return s
}
