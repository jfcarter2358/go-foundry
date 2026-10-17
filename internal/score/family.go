package score

// family.go — instrument families: the sections of a full score that a
// bracket joins (internal/score/engrave/bracket.go draws them; MusicXML
// writes them as part groups).

import "strings"

// Family is the instrument family a library instrument belongs to, by its
// category; "" for instruments no family bracket takes (keyboards,
// guitars, synthesisers).
func Family(instrument string) string {
	cat, _, ok := strings.Cut(instrument, "/")
	if !ok {
		return ""
	}
	switch cat {
	case "woodwinds", "winds":
		return "woodwinds"
	case "brass":
		return "brass"
	case "percussion", "drums":
		return "percussion"
	case "choir", "vocal":
		return "voices"
	case "strings":
		return "strings"
	}
	return ""
}

// BracketGroup is a run of lines a bracket joins: indexes into the lines
// engraved, first to last.
type BracketGroup struct {
	First, Last int
	Family      string
}

// BracketGroups are the family brackets of a score's lines, in the order
// given (the lines engraved; nil for every line): runs of two lines or more
// of one family in a row. None when the layout turns brackets off.
func BracketGroups(s *Score, lines []int) []BracketGroup {
	if s.Layout.NoBrackets {
		return nil
	}
	if lines == nil {
		for i := range s.Lines {
			lines = append(lines, i)
		}
	}
	var out []BracketGroup
	for i := 0; i < len(lines); {
		f := Family(s.Lines[lines[i]].Instrument)
		j := i
		for j+1 < len(lines) && f != "" && Family(s.Lines[lines[j+1]].Instrument) == f {
			j++
		}
		if f != "" && j > i {
			out = append(out, BracketGroup{First: lines[i], Last: lines[j], Family: f})
		}
		i = j + 1
	}
	return out
}
