package uifont

// uifont_fallback.go — the face used when a .ttf will not load, and the notdef
// box used for a rune a face does not have.
//
// A missing font must not take a screen down, so Face never fails; it hands
// back this instead. The letterforms are deliberately crude and integer-scaled,
// because a fallback that looked *almost* right would read as a font rendering
// bug rather than as a missing asset — and the whole point is that an author
// can tell at a glance which one they are looking at.
//
// It is a compiled-in bitmap rather than an embedded .ttf on purpose: no
// licence to audit, no 200KB in every binary, and no dependency on the font
// machinery working in order to report that the font machinery has no font.
//
// The cell is 5 wide and 8 tall. Rows 0-6 sit above the baseline and row 7
// below it, so descenders have somewhere to go.

import (
	"image"
	"image/color"
)

const (
	fallbackCols     = 5
	fallbackRows     = 8
	fallbackBaseline = 7 // rows above the baseline; row 7 is the descender row
	fallbackAdvance  = 6 // one blank column of side bearing
)

// opaque is full coverage, named so the blit loops below read as intent rather
// than as a magic 255.
var opaque = color.Alpha{A: 255}

// fallbackGlyphs is one string per printable ASCII rune: eight rows of five,
// separated by slashes, '#' for ink. Laid out this way so that a wrong pixel is
// visible in the source rather than buried in a packed bit array.
var fallbackGlyphs = map[rune]string{
	' ':  "...../...../...../...../...../...../...../.....",
	'!':  "..#../..#../..#../..#../..#../...../..#../.....",
	'"':  ".#.#./.#.#./...../...../...../...../...../.....",
	'#':  ".#.#./.#.#./#####/.#.#./#####/.#.#./.#.#./.....",
	'$':  "..#../.####/#.#../.###./..#.#/####./..#../.....",
	'%':  "##..#/##..#/...#./..#../.#.../#..##/#..##/.....",
	'&':  ".##../#..#./#.#../.#.../#.#.#/#..#./.##.#/.....",
	'\'': "..#../..#../...../...../...../...../...../.....",
	'(':  "...#./..#../.#.../.#.../.#.../..#../...#./.....",
	')':  ".#.../..#../...#./...#./...#./..#../.#.../.....",
	'*':  "...../#.#.#/.###./#####/.###./#.#.#/...../.....",
	'+':  "...../..#../..#../#####/..#../..#../...../.....",
	',':  "...../...../...../...../...../..#../..#../.#...",
	'-':  "...../...../...../#####/...../...../...../.....",
	'.':  "...../...../...../...../...../...../..#../.....",
	'/':  "....#/....#/...#./..#../.#.../#..../#..../.....",
	'0':  ".###./#...#/#..##/#.#.#/##..#/#...#/.###./.....",
	'1':  "..#../.##../..#../..#../..#../..#../.###./.....",
	'2':  ".###./#...#/....#/...#./..#../.#.../#####/.....",
	'3':  "#####/...#./..#../...#./....#/#...#/.###./.....",
	'4':  "...#./..##./.#.#./#..#./#####/...#./...#./.....",
	'5':  "#####/#..../####./....#/....#/#...#/.###./.....",
	'6':  "..##./.#.../#..../####./#...#/#...#/.###./.....",
	'7':  "#####/....#/...#./..#../.#.../.#.../.#.../.....",
	'8':  ".###./#...#/#...#/.###./#...#/#...#/.###./.....",
	'9':  ".###./#...#/#...#/.####/....#/...#./.##../.....",
	':':  "...../...../..#../...../...../..#../...../.....",
	';':  "...../...../..#../...../...../..#../..#../.#...",
	'<':  "...#./..#../.#.../#..../.#.../..#../...#./.....",
	'=':  "...../...../#####/...../#####/...../...../.....",
	'>':  ".#.../..#../...#./....#/...#./..#../.#.../.....",
	'?':  ".###./#...#/....#/...#./..#../...../..#../.....",
	'@':  ".###./#...#/#.###/#.#.#/#.###/#..../.###./.....",
	'A':  ".###./#...#/#...#/#####/#...#/#...#/#...#/.....",
	'B':  "####./#...#/#...#/####./#...#/#...#/####./.....",
	'C':  ".###./#...#/#..../#..../#..../#...#/.###./.....",
	'D':  "###../#..#./#...#/#...#/#...#/#..#./###../.....",
	'E':  "#####/#..../#..../####./#..../#..../#####/.....",
	'F':  "#####/#..../#..../####./#..../#..../#..../.....",
	'G':  ".###./#...#/#..../#.###/#...#/#...#/.###./.....",
	'H':  "#...#/#...#/#...#/#####/#...#/#...#/#...#/.....",
	'I':  ".###./..#../..#../..#../..#../..#../.###./.....",
	'J':  "..###/...#./...#./...#./...#./#..#./.##../.....",
	'K':  "#...#/#..#./#.#../##.../#.#../#..#./#...#/.....",
	'L':  "#..../#..../#..../#..../#..../#..../#####/.....",
	'M':  "#...#/##.##/#.#.#/#.#.#/#...#/#...#/#...#/.....",
	'N':  "#...#/#...#/##..#/#.#.#/#..##/#...#/#...#/.....",
	'O':  ".###./#...#/#...#/#...#/#...#/#...#/.###./.....",
	'P':  "####./#...#/#...#/####./#..../#..../#..../.....",
	'Q':  ".###./#...#/#...#/#...#/#.#.#/#..#./.##.#/.....",
	'R':  "####./#...#/#...#/####./#.#../#..#./#...#/.....",
	'S':  ".###./#...#/#..../.###./....#/#...#/.###./.....",
	'T':  "#####/..#../..#../..#../..#../..#../..#../.....",
	'U':  "#...#/#...#/#...#/#...#/#...#/#...#/.###./.....",
	'V':  "#...#/#...#/#...#/#...#/#...#/.#.#./..#../.....",
	'W':  "#...#/#...#/#...#/#.#.#/#.#.#/##.##/#...#/.....",
	'X':  "#...#/#...#/.#.#./..#../.#.#./#...#/#...#/.....",
	'Y':  "#...#/#...#/.#.#./..#../..#../..#../..#../.....",
	'Z':  "#####/....#/...#./..#../.#.../#..../#####/.....",
	'[':  ".###./.#.../.#.../.#.../.#.../.#.../.###./.....",
	'\\': "#..../#..../.#.../..#../...#./....#/....#/.....",
	']':  ".###./...#./...#./...#./...#./...#./.###./.....",
	'^':  "..#../.#.#./#...#/...../...../...../...../.....",
	'_':  "...../...../...../...../...../...../...../#####",
	'`':  ".#.../..#../...../...../...../...../...../.....",
	'a':  "...../...../.###./....#/.####/#...#/.####/.....",
	'b':  "#..../#..../####./#...#/#...#/#...#/####./.....",
	'c':  "...../...../.###./#..../#..../#..../.###./.....",
	'd':  "....#/....#/.####/#...#/#...#/#...#/.####/.....",
	'e':  "...../...../.###./#...#/#####/#..../.###./.....",
	'f':  "..##./.#..#/.#.../####./.#.../.#.../.#.../.....",
	'g':  "...../...../.####/#...#/#...#/.####/....#/.###.",
	'h':  "#..../#..../####./#...#/#...#/#...#/#...#/.....",
	'i':  "..#../...../.##../..#../..#../..#../.###./.....",
	'j':  "...#./...../..##./...#./...#./...#./...#./.##..",
	'k':  "#..../#..../#..#./#.#../##.../#.#../#..#./.....",
	'l':  ".##../..#../..#../..#../..#../..#../.###./.....",
	'm':  "...../...../##.#./#.#.#/#.#.#/#...#/#...#/.....",
	'n':  "...../...../####./#...#/#...#/#...#/#...#/.....",
	'o':  "...../...../.###./#...#/#...#/#...#/.###./.....",
	'p':  "...../...../####./#...#/#...#/####./#..../#....",
	'q':  "...../...../.####/#...#/#...#/.####/....#/....#",
	'r':  "...../...../#.##./##..#/#..../#..../#..../.....",
	's':  "...../...../.###./#..../.###./....#/####./.....",
	't':  ".#.../.#.../####./.#.../.#.../.#..#/..##./.....",
	'u':  "...../...../#...#/#...#/#...#/#...#/.####/.....",
	'v':  "...../...../#...#/#...#/#...#/.#.#./..#../.....",
	'w':  "...../...../#...#/#...#/#.#.#/#.#.#/.#.#./.....",
	'x':  "...../...../#...#/.#.#./..#../.#.#./#...#/.....",
	'y':  "...../...../#...#/#...#/#...#/.####/....#/.###.",
	'z':  "...../...../#####/...#./..#../.#.../#####/.....",
	'{':  "...#./..#../..#../.#.../..#../..#../...#./.....",
	'|':  "..#../..#../..#../..#../..#../..#../..#../.....",
	'}':  ".#.../..#../..#../...#./..#../..#../.#.../.....",
	'~':  "...../...../.#..#/#.#.#/#..#./...../...../.....",
}

// fallbackScale is how many device pixels one bitmap cell pixel becomes.
// Always a whole number, so the bitmap stays crisp instead of turning to mush
// at a fractional scale. The divisor is the cell height, so the line height
// that comes out lands near the size that was asked for.
func fallbackScale(px float32) int {
	s := int(px/fallbackRows + 0.5)
	if s < 1 {
		s = 1
	}
	return s
}

// fallbackMetrics is the ascent, descent and line height of the bitmap face,
// matching the cell geometry with one blank row between lines.
func fallbackMetrics(px float32) (ascent, descent, lineHeight float32) {
	s := float32(fallbackScale(px))
	return fallbackBaseline * s, (fallbackRows - fallbackBaseline) * s, (fallbackRows + 1) * s
}

// fallbackMask rasterises one bitmap glyph, trimmed to its ink so the packer is
// not asked to store a mostly-blank cell for every glyph.
func fallbackMask(r rune, px float32) (image.Image, image.Rectangle, float32, bool) {
	pat, ok := fallbackGlyphs[r]
	if !ok {
		return nil, image.Rectangle{}, 0, false
	}
	s := fallbackScale(px)
	advance := float32(fallbackAdvance * s)

	rows := splitRows(pat)
	top, bottom, left, right := inkBounds(rows)
	if top > bottom {
		// No ink at all — a space. A real glyph, with an advance and no pixels.
		return nil, image.Rectangle{}, advance, true
	}

	w, h := (right-left+1)*s, (bottom-top+1)*s
	x0, y0 := left*s, (top-fallbackBaseline)*s
	dr := image.Rect(x0, y0, x0+w, y0+h)

	m := image.NewAlpha(dr)
	for row := top; row <= bottom; row++ {
		for col := left; col <= right; col++ {
			if rows[row][col] != '#' {
				continue
			}
			for dy := 0; dy < s; dy++ {
				for dx := 0; dx < s; dx++ {
					m.SetAlpha(x0+(col-left)*s+dx, y0+(row-top)*s+dy, opaque)
				}
			}
		}
	}
	return m, dr, advance, true
}

// notdefMask is the substitute box: a hollow rectangle sitting on the baseline,
// sized off the face rather than off the bitmap cell so that it matches
// whatever it is standing in for.
func notdefMask(px float32) (image.Image, image.Rectangle, float32, bool) {
	w, h := int(px*0.5+0.5), int(px*0.7+0.5)
	if w < 3 {
		w = 3
	}
	if h < 3 {
		h = 3
	}
	t := int(px/16 + 0.5)
	if t < 1 || t*2 >= w || t*2 >= h {
		t = 1
	}

	dr := image.Rect(0, -h, w, 0)
	m := image.NewAlpha(dr)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < t || x >= w-t || y < t || y >= h-t {
				m.SetAlpha(x, -h+y, opaque)
			}
		}
	}

	gap := int(px/8 + 0.5)
	if gap < 1 {
		gap = 1
	}
	return m, dr, float32(w + gap), true
}

// splitRows cuts a slash-separated pattern into its rows.
func splitRows(pat string) [fallbackRows]string {
	var out [fallbackRows]string
	start, n := 0, 0
	for i := 0; i <= len(pat); i++ {
		if i == len(pat) || pat[i] == '/' {
			if n < fallbackRows {
				out[n] = pat[start:i]
				n++
			}
			start = i + 1
		}
	}
	return out
}

// inkBounds is the tight box around the set pixels. top > bottom means none.
func inkBounds(rows [fallbackRows]string) (top, bottom, left, right int) {
	top, left = fallbackRows, fallbackCols
	bottom, right = -1, -1
	for y := 0; y < fallbackRows; y++ {
		for x := 0; x < fallbackCols && x < len(rows[y]); x++ {
			if rows[y][x] != '#' {
				continue
			}
			if y < top {
				top = y
			}
			if y > bottom {
				bottom = y
			}
			if x < left {
				left = x
			}
			if x > right {
				right = x
			}
		}
	}
	return top, bottom, left, right
}
