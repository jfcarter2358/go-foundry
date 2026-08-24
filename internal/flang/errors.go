package flang

// errors.go — diagnostics.
//
// Parsing collects every error it can rather than stopping at the first. The
// script editor underlines all of them at once, and an author who has just
// pasted in a block wants the whole list, not one error per save. That means the
// parser has to be able to recover, which it does by skipping to the next
// statement or definition boundary — see parser.sync.

import (
	"fmt"
	"sort"
	"strings"
)

// Error is one diagnostic: what went wrong, and where.
type Error struct {
	Pos Pos
	Msg string
}

// Error renders the diagnostic as file:line:col: message.
func (e Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Msg)
}

// ErrorList is a set of diagnostics from one parse.
type ErrorList []Error

// Error renders the whole list, one per line, capped so a file that went
// catastrophically wrong does not produce a wall of text nobody reads.
func (l ErrorList) Error() string {
	if len(l) == 0 {
		return "no errors"
	}
	const maxShown = 10
	var b strings.Builder
	for i, e := range l {
		if i == maxShown {
			fmt.Fprintf(&b, "\n… and %d more", len(l)-maxShown)
			break
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

// Err returns the list as an error, or nil when it is empty — so a caller can
// write `if err := errs.Err(); err != nil`.
func (l ErrorList) Err() error {
	if len(l) == 0 {
		return nil
	}
	return l
}

// sortAndDedupe puts diagnostics in source order and drops repeats at the same
// position. Recovery can produce two complaints about one mistake; the second
// tells the author nothing.
func (l ErrorList) sortAndDedupe() ErrorList {
	if len(l) < 2 {
		return l
	}
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].Pos.Line != l[j].Pos.Line {
			return l[i].Pos.Line < l[j].Pos.Line
		}
		return l[i].Pos.Col < l[j].Pos.Col
	})
	out := l[:0]
	var prev Error
	for i, e := range l {
		if i > 0 && e.Pos == prev.Pos {
			continue
		}
		out = append(out, e)
		prev = e
	}
	return out
}
