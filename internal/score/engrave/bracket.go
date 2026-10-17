package engrave

// bracket.go — the brackets of a full score.
//
// An orchestral score groups its lines by family: the woodwinds, the
// brass, the percussion, the voices and the strings each have a bracket
// down the system's left edge, a thick line with a hook at each end, and
// their barlines run through the gaps between the family's staves, so the
// eye takes each section in as one (Gould, "Brackets and braces"). A
// family needs two lines or more in a row to be bracketed; a grand staff
// keeps its brace inside it. The groups are score.BracketGroups (the
// family is the library instrument's category); the layout's
// `brackets: false` leaves them all out.

import "foundry/internal/score"

// bracketGap is a bracket's distance from the system's opening barline.
const bracketGap = 0.3

// drawBrackets draws the family brackets, and the barlines through the
// gaps between a family's lines.
func (e *engraver) drawBrackets() {
	if len(e.staves) < 2 {
		return
	}
	var lines []int
	seen := map[int]bool{}
	for _, sc := range e.staves {
		if !seen[sc.line] {
			seen[sc.line] = true
			lines = append(lines, sc.line)
		}
	}
	t := e.d.StaffLineThickness
	th := e.d.BracketThickness
	if th <= 0 {
		th = 0.5
	}
	for _, g := range score.BracketGroups(e.s, lines) {
		first, last := -1, -1
		for k, sc := range e.staves {
			if sc.line == g.First && first < 0 {
				first = k
			}
			if sc.line == g.Last {
				last = k
			}
		}
		if first < 0 || last <= first {
			continue
		}
		top := e.staves[first].top
		bot := e.staves[last].top + e.staves[last].height()
		ref := Ref{Line: g.First, Staff: -1, Voice: -1, Event: -1, Note: -1}
		x := -bracketGap - th/2
		e.items = append(e.items, Item{Kind: ItemLine, Role: RoleBrace, Ref: ref, P: Point{X: x, Y: top - 0.25}, Q: Point{X: x, Y: bot + 0.25}, Width: th})
		// The hooks: SMuFL's bracket ends, their left edge on the line's.
		bt, _ := e.f.Box("bracketTop")
		bb, _ := e.f.Box("bracketBottom")
		e.items = append(e.items, e.glyphItem(RoleBrace, ref, "bracketTop", x-th/2-bt.Min.X, top-0.25, 1))
		e.items = append(e.items, e.glyphItem(RoleBrace, ref, "bracketBottom", x-th/2-bb.Min.X, bot+0.25, 1))
		// Barlines through the gaps between the family's lines (a grand
		// staff's own are drawn already).
		for k := first; k < last; k++ {
			a, b := e.staves[k], e.staves[k+1]
			if a.line == b.line {
				continue
			}
			y0, y1 := a.top+a.height(), b.top
			bref := Ref{Line: g.First, Staff: -1, Voice: -1, Event: -1, Note: -1}
			if bc := e.bars[0]; e.repeatStarts(bc.bar) {
				for _, it := range e.barlineItems(barRepeatStart, bc.content+startRepeatGap, y0, y1, NoRef, false) {
					e.items = append(e.items, it)
				}
			}
			for bi, bc := range e.bars {
				bref.Bar = bc.bar
				for _, it := range e.barlineItems(e.barlineBefore(bi+1), bc.x1, y0-t/2, y1+t/2, bref, false) {
					e.items = append(e.items, it)
				}
			}
		}
	}
}
