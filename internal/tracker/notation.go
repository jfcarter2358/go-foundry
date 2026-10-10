package tracker

// notation.go — cells as text.
//
// A pattern is saved as one line per row, channels separated by " | ", each
// cell written the way a tracker shows it:
//
//	C-4 01 40 V48 ...
//	^   ^  ^  ^---^--- effect columns: letter + two hex digits, ... empty
//	|   |  +--------- volume 00..40, .. empty
//	|   +------------ instrument, two hex digits, .. empty
//	+---------------- note: C-4, C#4, === note off, ^^^ note cut, ... empty
//
// The same text is what the editor copies to the clipboard and what Claude
// reads and writes, which is the reason the file format is text at all.

import (
	"fmt"
	"strconv"
	"strings"
)

var noteNames = [12]string{"C-", "C#", "D-", "D#", "E-", "F-", "F#", "G-", "G#", "A-", "A#", "B-"}

// NoteName spells a note value: "C-4", "===", "^^^", "...".
func NoteName(n uint8) string {
	switch {
	case n == NoteNone:
		return "..."
	case n == NoteOff:
		return "==="
	case n == NoteCut:
		return "^^^"
	case n >= NoteMin && n <= NoteMax:
		i := int(n - 1)
		return fmt.Sprintf("%s%d", noteNames[i%12], i/12)
	}
	return "???"
}

// ParseNote reads a note as NoteName writes it. Flats are accepted ("Db4"),
// and so is a lower-case letter.
func ParseNote(s string) (uint8, error) {
	switch s {
	case "...", "---", "":
		return NoteNone, nil
	case "===", "OFF", "off":
		return NoteOff, nil
	case "^^^", "CUT", "cut":
		return NoteCut, nil
	}
	if len(s) < 2 {
		return 0, fmt.Errorf("bad note %q", s)
	}
	semis := map[byte]int{'C': 0, 'D': 2, 'E': 4, 'F': 5, 'G': 7, 'A': 9, 'B': 11}
	base, ok := semis[upper(s[0])]
	if !ok {
		return 0, fmt.Errorf("bad note %q", s)
	}
	rest := s[1:]
	switch rest[0] {
	case '#':
		base++
		rest = rest[1:]
	case 'b':
		base--
		rest = rest[1:]
	case '-':
		rest = rest[1:]
	}
	oct, err := strconv.Atoi(rest)
	if err != nil || oct < 0 || oct > 9 {
		return 0, fmt.Errorf("bad octave in note %q", s)
	}
	v := oct*12 + base + 1
	if v < int(NoteMin) || v > int(NoteMax) {
		return 0, fmt.Errorf("note %q out of range", s)
	}
	return uint8(v), nil
}

func upper(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 32
	}
	return b
}

// FormatCell writes a cell with fx effect columns.
func FormatCell(c Cell, fx int) string {
	var b strings.Builder
	b.WriteString(NoteName(c.Note))
	b.WriteByte(' ')
	if c.Instr == 0 {
		b.WriteString("..")
	} else {
		fmt.Fprintf(&b, "%02X", c.Instr)
	}
	b.WriteByte(' ')
	if c.Vol == VolNone {
		b.WriteString("..")
	} else {
		fmt.Fprintf(&b, "%02X", c.Vol)
	}
	for i := 0; i < fx && i < MaxFXColumns; i++ {
		b.WriteByte(' ')
		b.WriteString(FormatEffect(c.FX[i]))
	}
	return b.String()
}

// FormatEffect writes one effect column: "V48" or "...".
func FormatEffect(e Effect) string {
	if e.Cmd == 0 {
		return "..."
	}
	return fmt.Sprintf("%c%02X", e.Cmd, e.Param)
}

// ParseCell reads a cell as FormatCell writes it. Missing trailing fields
// read as empty, so "C-4 01" is a whole cell.
func ParseCell(s string) (Cell, error) {
	c := EmptyCell
	f := strings.Fields(s)
	if len(f) == 0 {
		return c, nil
	}
	var err error
	if c.Note, err = ParseNote(f[0]); err != nil {
		return c, err
	}
	if len(f) > 1 && f[1] != ".." {
		v, err := strconv.ParseUint(f[1], 16, 8)
		if err != nil {
			return c, fmt.Errorf("bad instrument %q", f[1])
		}
		c.Instr = uint8(v)
	}
	if len(f) > 2 && f[2] != ".." {
		v, err := strconv.ParseUint(f[2], 16, 8)
		if err != nil || v > uint64(VolMax) {
			return c, fmt.Errorf("bad volume %q", f[2])
		}
		c.Vol = uint8(v)
	}
	for i := 3; i < len(f) && i-3 < MaxFXColumns; i++ {
		e, err := ParseEffect(f[i])
		if err != nil {
			return c, err
		}
		c.FX[i-3] = e
	}
	return c, nil
}

// ParseEffect reads "V48" or "...".
func ParseEffect(s string) (Effect, error) {
	if s == "..." || s == "" {
		return Effect{}, nil
	}
	if len(s) != 3 {
		return Effect{}, fmt.Errorf("bad effect %q", s)
	}
	cmd := upper(s[0])
	if EffectFor(cmd) == nil {
		return Effect{}, fmt.Errorf("unknown effect %q", s)
	}
	v, err := strconv.ParseUint(s[1:], 16, 8)
	if err != nil {
		return Effect{}, fmt.Errorf("bad effect value %q", s)
	}
	return Effect{Cmd: cmd, Param: uint8(v)}, nil
}

// FormatRow writes one row of a pattern.
func FormatRow(row []Cell, fx int) string {
	parts := make([]string, len(row))
	for i, c := range row {
		parts[i] = FormatCell(c, fx)
	}
	return strings.Join(parts, " | ")
}

// FormatPattern writes a pattern as text, trailing empty rows dropped.
func FormatPattern(p *Pattern, fx int) string {
	last := -1
	for r, row := range p.Cells {
		for _, c := range row {
			if !c.IsEmpty() {
				last = r
				break
			}
		}
	}
	var b strings.Builder
	for r := 0; r <= last; r++ {
		b.WriteString(FormatRow(p.Cells[r], fx))
		b.WriteByte('\n')
	}
	return b.String()
}

// ParsePatternText fills p (already sized) from text. Lines past the
// pattern's length and channels past its width are an error; fewer are fine.
// A blank line is an empty row.
func ParsePatternText(p *Pattern, text string) error {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		return nil
	}
	if len(lines) > p.Rows() {
		return fmt.Errorf("%d rows of data in a %d-row pattern", len(lines), p.Rows())
	}
	for r, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells := strings.Split(line, "|")
		width := len(p.Cells[r])
		if len(cells) > width {
			return fmt.Errorf("row %d has %d cells; the song has %d channels", r, len(cells), width)
		}
		for ch, cs := range cells {
			c, err := ParseCell(cs)
			if err != nil {
				return fmt.Errorf("row %d channel %d: %w", r, ch+1, err)
			}
			p.Cells[r][ch] = c
		}
	}
	return nil
}
