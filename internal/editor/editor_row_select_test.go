package editor

// editor_row_select_test.go — the selection model behind the log window and the
// problems pane. The drawing needs a GL context; the model does not, and the
// model is where the rules that are easy to get wrong live: what a shift-click
// resolves to, and what happens to a selection whose rows have gone away.

import (
	"strings"
	"testing"

	"foundry/internal/flang"
	"foundry/internal/script"
)

// rowFrame plays one frame's worth of rows through the selection without
// drawing them, standing in for the row() calls the real lists make.
func rowFrame(s *rowSelection, keys ...string) {
	s.begin()
	for _, k := range keys {
		s.keys = append(s.keys, k)
		s.text[k] = "text " + k
	}
	s.end()
}

// rowClickIn is a click delivered during a frame, the way row() reports one:
// recorded mid-list and applied once the whole list is known.
func rowClickIn(s *rowSelection, c rowClick, keys ...string) {
	s.begin()
	for _, k := range keys {
		s.keys = append(s.keys, k)
		s.text[k] = "text " + k
	}
	s.pending = &rowClick{key: c.key, ctrl: c.ctrl, shift: c.shift}
	s.end()
}

func selectedKeys(s *rowSelection) []string {
	var out []string
	for _, k := range s.keys {
		if s.sel[k] {
			out = append(out, k)
		}
	}
	return out
}

func TestRowSelectionPlainClickReplaces(t *testing.T) {
	var s rowSelection
	rowClickIn(&s, rowClick{key: "b"}, "a", "b", "c")
	if got := selectedKeys(&s); len(got) != 1 || got[0] != "b" {
		t.Fatalf("after clicking b: %v", got)
	}
	rowClickIn(&s, rowClick{key: "c"}, "a", "b", "c")
	if got := selectedKeys(&s); len(got) != 1 || got[0] != "c" {
		t.Fatalf("a plain click should replace, not add: %v", got)
	}
}

func TestRowSelectionCtrlClickToggles(t *testing.T) {
	var s rowSelection
	rowClickIn(&s, rowClick{key: "a"}, "a", "b", "c")
	rowClickIn(&s, rowClick{key: "c", ctrl: true}, "a", "b", "c")
	if got := strings.Join(selectedKeys(&s), ","); got != "a,c" {
		t.Fatalf("ctrl-click should add: %q", got)
	}
	rowClickIn(&s, rowClick{key: "a", ctrl: true}, "a", "b", "c")
	if got := strings.Join(selectedKeys(&s), ","); got != "c" {
		t.Fatalf("ctrl-click on a selected row should remove it: %q", got)
	}
}

// A shift-click has to reach rows that had not been drawn yet when the click
// happened, which is the whole reason the click is deferred to the end of the
// list.
func TestRowSelectionShiftClickExtendsForwards(t *testing.T) {
	var s rowSelection
	rowClickIn(&s, rowClick{key: "b"}, "a", "b", "c", "d", "e")
	rowClickIn(&s, rowClick{key: "d", shift: true}, "a", "b", "c", "d", "e")
	if got := strings.Join(selectedKeys(&s), ","); got != "b,c,d" {
		t.Fatalf("shift-click range: %q", got)
	}
}

func TestRowSelectionShiftClickExtendsBackwards(t *testing.T) {
	var s rowSelection
	rowClickIn(&s, rowClick{key: "d"}, "a", "b", "c", "d", "e")
	rowClickIn(&s, rowClick{key: "b", shift: true}, "a", "b", "c", "d", "e")
	if got := strings.Join(selectedKeys(&s), ","); got != "b,c,d" {
		t.Fatalf("shift-click upwards: %q", got)
	}
}

// The anchor stays put so a second shift-click re-cuts the range from the same
// end rather than from wherever the last one stopped.
func TestRowSelectionShiftClickRecutsFromAnchor(t *testing.T) {
	var s rowSelection
	rowClickIn(&s, rowClick{key: "b"}, "a", "b", "c", "d", "e")
	rowClickIn(&s, rowClick{key: "e", shift: true}, "a", "b", "c", "d", "e")
	rowClickIn(&s, rowClick{key: "c", shift: true}, "a", "b", "c", "d", "e")
	if got := strings.Join(selectedKeys(&s), ","); got != "b,c" {
		t.Fatalf("second shift-click should re-cut from the anchor: %q", got)
	}
}

// The log ring evicts and the filters hide; either way a selected row can stop
// being listed, and a selection pointing at text nobody can see would copy it.
func TestRowSelectionForgetsRowsThatLeaveTheList(t *testing.T) {
	var s rowSelection
	rowClickIn(&s, rowClick{key: "a"}, "a", "b", "c")
	rowClickIn(&s, rowClick{key: "c", ctrl: true}, "a", "b", "c")
	rowFrame(&s, "b", "c")
	if got := strings.Join(selectedKeys(&s), ","); got != "c" {
		t.Fatalf("evicted rows should drop out of the selection: %q", got)
	}
	if s.count() != 1 {
		t.Fatalf("count should follow the pruning, got %d", s.count())
	}
}

func TestRowSelectionCopyIsInListOrder(t *testing.T) {
	var s rowSelection
	rowClickIn(&s, rowClick{key: "c"}, "a", "b", "c")
	rowClickIn(&s, rowClick{key: "a", ctrl: true}, "a", "b", "c")
	if got := s.selectedText(); got != "text a\ntext c" {
		t.Fatalf("copy should follow the list, not the clicking: %q", got)
	}
	if got := s.allText(); got != "text a\ntext b\ntext c" {
		t.Fatalf("copy all: %q", got)
	}
}

func TestRowSelectionSelectAllAndClear(t *testing.T) {
	var s rowSelection
	rowFrame(&s, "a", "b", "c")
	s.selectAll()
	if s.count() != 3 {
		t.Fatalf("select all: %d", s.count())
	}
	s.clearSel()
	if s.count() != 0 || s.anchor != "" {
		t.Fatalf("clear should drop the anchor too: %d %q", s.count(), s.anchor)
	}
}

// An empty selection must not put an empty line on the clipboard: a paste that
// silently replaced what was there would be worse than one that did nothing.
func TestRowSelectionCopyNothingIsNoop(t *testing.T) {
	var s rowSelection
	rowFrame(&s, "a", "b")
	if got := s.selectedText(); got != "" {
		t.Fatalf("nothing selected should copy nothing, got %q", got)
	}
}

// The window drops the level because colour says it and shortens the source
// because the column repeats it. A pasted line has neither and needs both.
func TestLogEntryCopyTextCarriesWhatTheColumnsDrop(t *testing.T) {
	e := script.LogEntry{
		Time:   1500,
		Level:  script.LogError,
		Source: "scripts/doors/airlock.f",
		Text:   "undefined function `oepn`",
		Pos:    flang.Pos{File: "scripts/doors/airlock.f", Line: 12, Col: 5},
	}
	got := logEntryCopyText(e)
	for _, want := range []string{"1.50", "error", "scripts/doors/airlock.f:12:5:", "undefined function `oepn`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("copy text %q is missing %q", got, want)
		}
	}
}

func TestLogEntryCopyTextWithoutSourceOrPosition(t *testing.T) {
	e := script.LogEntry{Time: 250, Level: script.LogInfo, Text: "level loaded"}
	got := logEntryCopyText(e)
	if !strings.Contains(got, "info") || !strings.Contains(got, "level loaded") {
		t.Fatalf("engine line: %q", got)
	}
	if strings.Contains(got, ":0:") {
		t.Fatalf("an unpositioned line should carry no position: %q", got)
	}
}

func TestPlural(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, "0 lines"}, {1, "1 line"}, {2, "2 lines"},
	} {
		if got := plural(tc.n, "line"); got != tc.want {
			t.Fatalf("plural(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
