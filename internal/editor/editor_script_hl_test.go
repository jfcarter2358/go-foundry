package editor

// editor_script_hl_test.go — the colouring, as far as it can be checked without
// a window: that the classified source is cut into lines without a byte going
// missing, and that a chunk's text and its class stay together.
//
// The classification itself is flang's, and is tested there.

import (
	"strings"
	"testing"

	"foundry/internal/flang"
)

// joinChunks reassembles a line from its chunks.
func joinChunks(chunks []scriptHLChunk) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.Text)
	}
	return b.String()
}

func TestHighlightLinesReproduceTheSource(t *testing.T) {
	srcs := []string{
		"",
		"\n",
		"x = 1",
		"x = 1\n",
		"\n\n\n",
		"def go(:spawn:) -> null\n    log(\"hi\")\nend\n",
		"-- a comment\n\tx = \"unterminated\nend",
	}
	for _, src := range srcs {
		lines := scriptHighlightLines(src)
		want := strings.Split(src, "\n")
		if len(lines) != len(want) {
			t.Fatalf("%q: got %d lines, want %d", src, len(lines), len(want))
		}
		for i := range want {
			if got := joinChunks(lines[i]); got != want[i] {
				t.Errorf("%q line %d: got %q, want %q", src, i, got, want[i])
			}
		}
	}
}

func TestHighlightLinesClassifyPerLine(t *testing.T) {
	src := "-- note\nlog(1)"
	lines := scriptHighlightLines(src)

	if len(lines[0]) != 1 || lines[0][0].Class != flang.ClassComment {
		t.Fatalf("line 0: got %v, want one comment chunk", lines[0])
	}
	// A comment stops at the newline rather than colouring the line below it.
	for _, c := range lines[1] {
		if c.Class == flang.ClassComment {
			t.Errorf("line 1: %q was coloured as a comment", c.Text)
		}
	}
	if got := lines[1][0]; got.Text != "log" || got.Class != flang.ClassBuiltin {
		t.Errorf("line 1 first chunk: got %q/%v, want \"log\"/builtin", got.Text, got.Class)
	}
}

func TestHighlightLinesIndentIsPlain(t *testing.T) {
	// Leading whitespace has to arrive as its own plain chunk, because the
	// drawing advances past it rather than drawing it.
	lines := scriptHighlightLines("    end")
	if len(lines[0]) != 2 {
		t.Fatalf("got %d chunks, want 2: %v", len(lines[0]), lines[0])
	}
	if lines[0][0].Text != "    " || lines[0][0].Class != flang.ClassPlain {
		t.Errorf("first chunk: got %q/%v, want four spaces, plain", lines[0][0].Text, lines[0][0].Class)
	}
	if lines[0][1].Class != flang.ClassKeyword {
		t.Errorf("second chunk: got %v, want keyword", lines[0][1].Class)
	}
}

func TestHighlightCacheFollowsTheText(t *testing.T) {
	d := &scriptDoc{Text: "x = 1"}
	first := d.HighlightLines()
	if same := d.HighlightLines(); &same[0] != &first[0] {
		t.Error("unchanged text was reclassified")
	}
	d.SetText("y = :on:", 1)
	lines := d.HighlightLines()
	if got := joinChunks(lines[0]); got != "y = :on:" {
		t.Fatalf("got %q after an edit, want the new text", got)
	}
	if lines[0][len(lines[0])-1].Class != flang.ClassAtom {
		t.Errorf("last chunk: got %v, want atom", lines[0][len(lines[0])-1].Class)
	}
}

func TestScriptLinePrefix(t *testing.T) {
	chunks := scriptHighlightLines("log(\"hi\")")[0]
	for n := 0; n <= len("log(\"hi\")"); n++ {
		if got, want := scriptLinePrefix(chunks, n), "log(\"hi\")"[:n]; got != want {
			t.Errorf("prefix of %d: got %q, want %q", n, got, want)
		}
	}
	// Past the end of the line — a cursor reading left over from a longer
	// buffer — is the whole line, not a panic.
	if got := scriptLinePrefix(chunks, 999); got != "log(\"hi\")" {
		t.Errorf("overlong prefix: got %q", got)
	}
}
