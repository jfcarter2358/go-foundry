package editor

// editor_yaml_find.go — find and replace over the level YAML editor's buffer.
//
// Every search is compiled to a regexp, the plain ones included: case folding
// and whole-word matching are both one flag or one `\b` away in a pattern, and
// one code path for four combinations of options is four fewer places for them
// to disagree. A plain query is QuoteMeta'd first, so `a.b` still finds only
// `a.b`.
//
// The matches are kept as byte ranges over the buffer and are recomputed only
// when the text or the query changes — the overlay draws the visible ones every
// frame, and the count beside the find box reads the list's length.

import (
	"regexp"
	"strings"
)

// yamlFindMaxMatches caps how many matches one search collects. A single
// letter in a two-megabyte level matches most of it; past this many the count
// says "10000+" and nothing is lost that anybody would page through.
const yamlFindMaxMatches = 10000

// yamlFindOpts is one search.
type yamlFindOpts struct {
	Query string
	Case  bool // match case
	Word  bool // whole words only
	Regex bool // Query is a regular expression
}

// yamlFindMatch is one hit: the bytes [Start, End).
type yamlFindMatch struct{ Start, End int }

// compile turns the options into a pattern. An empty query compiles to nil.
func (o yamlFindOpts) compile() (*regexp.Regexp, error) {
	if o.Query == "" {
		return nil, nil
	}
	pat := o.Query
	if !o.Regex {
		pat = regexp.QuoteMeta(pat)
	}
	if o.Word {
		pat = `\b(?:` + pat + `)\b`
	}
	if !o.Case {
		pat = `(?i)` + pat
	}
	// Multi-line so that ^ and $ mean a line's ends, which is what anyone
	// writing them in an editor's find box means.
	return regexp.Compile(`(?m)` + pat)
}

// yamlFindAll returns every match of o in text, in order, up to
// yamlFindMaxMatches, and whether the list was cut short.
func yamlFindAll(text string, o yamlFindOpts) ([]yamlFindMatch, bool, error) {
	return yamlFindAllLimit(text, o, yamlFindMaxMatches)
}

// yamlFindAllLimit is yamlFindAll with the cap given; -1 for none. Empty
// matches — `a*` against a line of b's — are skipped: there is nothing to
// select or replace in one.
func yamlFindAllLimit(text string, o yamlFindOpts, limit int) ([]yamlFindMatch, bool, error) {
	if !o.Regex && o.Query != "" && yamlIsASCII(o.Query) {
		ms, capped := yamlFindLiteral(text, o, limit)
		return ms, capped, nil
	}
	re, err := o.compile()
	if err != nil || re == nil {
		return nil, false, err
	}
	n := -1
	if limit >= 0 {
		n = limit + 1
	}
	var out []yamlFindMatch
	for _, m := range re.FindAllStringIndex(text, n) {
		if m[1] > m[0] {
			out = append(out, yamlFindMatch{m[0], m[1]})
		}
	}
	if limit >= 0 && len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// yamlFindLiteral is the search without a regexp, for a plain ASCII query —
// which is nearly every search. Go's regexp cannot use its fast literal scan
// once a pattern is case-folded, and a case-insensitive search of level1 went
// from sixty milliseconds a keystroke to a few by doing without it.
//
// Case is folded by lowering ASCII letters only, which keeps every byte where
// it was, so an offset into the lowered copy is an offset into the text.
func yamlFindLiteral(text string, o yamlFindOpts, limit int) ([]yamlFindMatch, bool) {
	hay, needle := text, o.Query
	if !o.Case {
		hay, needle = yamlASCIILower(text), yamlASCIILower(needle)
	}
	var out []yamlFindMatch
	for at := 0; at <= len(hay)-len(needle); {
		i := strings.Index(hay[at:], needle)
		if i < 0 {
			break
		}
		start, end := at+i, at+i+len(needle)
		at = start + 1
		if o.Word && (start > 0 && yamlIsWordByte(text[start-1]) && yamlIsWordByte(text[start]) ||
			end < len(text) && yamlIsWordByte(text[end]) && yamlIsWordByte(text[end-1])) {
			continue
		}
		out = append(out, yamlFindMatch{start, end})
		at = end
		if limit >= 0 && len(out) > limit {
			return out[:limit], true
		}
	}
	return out, false
}

// yamlIsWordByte is what `\b` counts as a word character.
func yamlIsWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func yamlIsASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// yamlASCIILower lowers A-Z and nothing else.
func yamlASCIILower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// yamlFindNext returns the index of the match to go to from byte offset at: the
// first starting at or after it going forwards, the last ending at or before it
// going backwards, wrapping at either end. -1 when there are none.
func yamlFindNext(matches []yamlFindMatch, at int, backwards bool) int {
	if len(matches) == 0 {
		return -1
	}
	if backwards {
		for i := len(matches) - 1; i >= 0; i-- {
			if matches[i].End <= at {
				return i
			}
		}
		return len(matches) - 1
	}
	for i, m := range matches {
		if m.Start >= at {
			return i
		}
	}
	return 0
}

// yamlReplacement is what one match is replaced with: the replacement text
// literally for a plain search, with $1-style groups expanded for a regex.
func yamlReplacement(text string, m yamlFindMatch, o yamlFindOpts, repl string) string {
	if !o.Regex {
		return repl
	}
	re, err := o.compile()
	if err != nil || re == nil {
		return repl
	}
	return yamlExpand(re, text, m, repl)
}

// yamlExpand expands repl's groups against one match of re.
//
// The pattern is re-run from the match's start to recover its groups. The
// text left of the match is lost to it, so a `\b` or `^` at the very start
// is judged afresh — which agrees with the original search, since that found
// a match beginning exactly here. A disagreement falls back to the literal.
func yamlExpand(re *regexp.Regexp, text string, m yamlFindMatch, repl string) string {
	loc := re.FindStringSubmatchIndex(text[m.Start:])
	if loc == nil || loc[0] != 0 {
		return repl
	}
	for i := range loc {
		if loc[i] >= 0 {
			loc[i] += m.Start
		}
	}
	return string(re.ExpandString(nil, repl, text, loc))
}

// yamlReplaceAll replaces every match, returning the new text and how many
// were replaced. Built end to end rather than by repeated splicing, which on a
// large file would be quadratic.
func yamlReplaceAll(text string, matches []yamlFindMatch, o yamlFindOpts, repl string) (string, int) {
	if len(matches) == 0 {
		return text, 0
	}
	var re *regexp.Regexp
	if o.Regex {
		var err error
		if re, err = o.compile(); err != nil || re == nil {
			return text, 0
		}
	}
	var b strings.Builder
	b.Grow(len(text))
	at := 0
	for _, m := range matches {
		b.WriteString(text[at:m.Start])
		if re != nil {
			b.WriteString(yamlExpand(re, text, m, repl))
		} else {
			b.WriteString(repl)
		}
		at = m.End
	}
	b.WriteString(text[at:])
	return b.String(), len(matches)
}
