package edit

import (
	"strings"
	"testing"

	"foundry/internal/score"
)

func loc(bar, ev int) Loc { return Loc{Bar: bar, Event: ev} }

func TestMarksTakeTheirGroupsPlace(t *testing.T) {
	s := one(t, "4/4", 0, "qC5 qD5 qE5 qr")
	if on, err := ToggleArticulation(s, loc(1, 0), "stacc", ""); !on || err != nil {
		t.Fatal(on, err)
	}
	// A staccatissimo replaces the staccato; a tenuto sits beside it.
	ToggleArticulation(s, loc(1, 0), "staccmo", "")
	ToggleArticulation(s, loc(1, 0), "ten", "")
	if got := barText(s, 0, 1); got != "qC5(staccmo,ten) qD5 qE5 qr" {
		t.Errorf("got %q", got)
	}
	// Again: off.
	if on, _ := ToggleArticulation(s, loc(1, 0), "ten", ""); on {
		t.Error("a second tenuto did not take it off")
	}
	// An accent and a marcato, one ornament, one tremolo.
	ToggleArticulation(s, loc(1, 1), "acc", "")
	ToggleArticulation(s, loc(1, 1), "marc", "")
	ToggleArticulation(s, loc(1, 1), "tr", "#")
	ToggleArticulation(s, loc(1, 1), "turn", "")
	ToggleArticulation(s, loc(1, 1), "trem", "3")
	ToggleArticulation(s, loc(1, 1), "trem2", "2")
	if got := barText(s, 0, 1); got != "qC5(staccmo) qD5(marc,turn,trem2=2) qE5 qr" {
		t.Errorf("got %q", got)
	}
	// Articulations go on notes; a fermata on a rest is fine.
	if _, err := ToggleArticulation(s, loc(1, 3), "stacc", ""); err == nil {
		t.Error("a staccato on a rest")
	}
	if _, err := ToggleMark(s, loc(1, 3), "ferm", "long"); err != nil {
		t.Error(err)
	}
	if got := barText(s, 0, 1); !strings.HasSuffix(got, "qr(ferm=long)") {
		t.Errorf("fermata on the rest: %q", got)
	}
	// The same mark with another value replaces it.
	ToggleMark(s, loc(1, 3), "ferm", "short")
	if got := barText(s, 0, 1); !strings.HasSuffix(got, "qr(ferm=short)") {
		t.Errorf("fermata changed: %q", got)
	}
	if _, err := ToggleMark(s, loc(1, 9), "ferm", ""); err != ErrNoEvent {
		t.Error("a mark on nothing")
	}
}

func TestDynamicsAndText(t *testing.T) {
	s := one(t, "4/4", 0, "qC5(p,stacc) qD5 hE5")
	if err := SetDynamic(s, loc(1, 0), "mf"); err != nil {
		t.Fatal(err)
	}
	if err := SetDynamic(s, loc(1, 0), "mezzo"); err == nil {
		t.Error("a word set as a dynamic")
	}
	SetText(s, loc(1, 1), "tech", "pizz.")
	SetText(s, loc(1, 1), "expr", " dolce ")
	SetText(s, loc(1, 2), "text", "solo")
	if got := barText(s, 0, 1); got != `qC5(stacc,mf) qD5(tech=pizz.,expr=dolce) hE5("solo")` {
		t.Errorf("got %q", got)
	}
	if Dynamic(Event(s, loc(1, 0))) != "mf" {
		t.Error("Dynamic")
	}
	SetText(s, loc(1, 1), "tech", "")
	SetDynamic(s, loc(1, 0), "")
	if got := barText(s, 0, 1); got != `qC5(stacc) qD5(expr=dolce) hE5("solo")` {
		t.Errorf("after clearing: %q", got)
	}
	if err := SetText(s, loc(1, 0), "lyric", "la"); err == nil {
		t.Error("an unknown text kind")
	}
}

func TestClassifyExpression(t *testing.T) {
	for in, want := range map[string]struct {
		k Expression
		t string
	}{
		"mf": {ExprDynamic, "mf"}, " SFZ ": {ExprDynamic, "sfz"}, "pizz.": {ExprTechnique, "pizz."},
		"pizz": {ExprTechnique, "pizz."}, "con sord.": {ExprTechnique, "con sord."}, "arco": {ExprTechnique, "arco"},
		"cresc.": {ExprCresc, "cresc."}, "decresc.": {ExprDim, "dim."}, "dim": {ExprDim, "dim."},
		"rit.": {ExprTempo, "rit."}, "a tempo": {ExprTempo, "a tempo"}, "poco accel.": {ExprTempo, "poco accel."},
		"dolce": {ExprText, "dolce"}, "espress.": {ExprText, "espress."},
	} {
		k, txt := ClassifyExpression(in)
		if k != want.k || txt != want.t {
			t.Errorf("%q: %d %q, want %d %q", in, k, txt, want.k, want.t)
		}
	}
}

func TestSpansFromNoteToNote(t *testing.T) {
	s := one(t, "4/4", 0, "qC5 qD5 qE5 qF5 | qG5 qA5 hB5")
	if err := AddSpan(s, loc(1, 1), loc(2, 0), SpanSlur, ""); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1) + " | " + barText(s, 0, 2); got != "qC5 qD5(slur) qE5 qF5 | qG5(slur_end) qA5 hB5" {
		t.Errorf("slur: %q", got)
	}
	// Found from any note under it, and taken out from there.
	if a, z, ok := SpanAt(s, loc(1, 3), SpanSlur); !ok || a != loc(1, 1) || z != loc(2, 0) {
		t.Errorf("SpanAt: %v %v %v", a, z, ok)
	}
	if _, _, ok := SpanAt(s, loc(2, 1), SpanSlur); ok {
		t.Error("a note after the slur is under it")
	}
	// A slur over part of it replaces it.
	if err := AddSpan(s, loc(1, 0), loc(1, 2), SpanSlur, ""); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1) + " | " + barText(s, 0, 2); got != "qC5(slur) qD5 qE5(slur_end) qF5 | qG5 qA5 hB5" {
		t.Errorf("replaced slur: %q", got)
	}
	if !RemoveSpan(s, loc(1, 1), SpanSlur) || RemoveSpan(s, loc(1, 1), SpanSlur) {
		t.Error("RemoveSpan")
	}
	if got := barText(s, 0, 1); got != "qC5 qD5 qE5 qF5" {
		t.Errorf("after removing: %q", got)
	}
	// Hairpins: cresc, a text dim, an ottava and a pedal on one note.
	AddSpan(s, loc(1, 0), loc(1, 1), SpanHairpin, "cresc")
	AddSpan(s, loc(1, 2), loc(2, 1), SpanHairpin, "dim.")
	if err := AddSpan(s, loc(2, 2), loc(2, 2), SpanOttava, "8vb"); err != nil {
		t.Fatal(err)
	}
	AddSpan(s, loc(1, 0), loc(2, 2), SpanPedal, "")
	if got := barText(s, 0, 1) + " | " + barText(s, 0, 2); got != "qC5(cresc,ped) qD5(hairpin_end) qE5(dim=text) qF5 | qG5 qA5(hairpin_end) hB5(8vb,ottava_end,ped_end)" {
		t.Errorf("hairpins: %q", got)
	}
	// A cresc over both replaces both.
	AddSpan(s, loc(1, 1), loc(1, 3), SpanHairpin, "cresc")
	if got := barText(s, 0, 1); got != "qC5(ped) qD5(cresc) qE5 qF5(hairpin_end)" {
		t.Errorf("hairpin over two: %q", got)
	}
	// Refused: across voices, one note for a slur, a slur ending on a rest,
	// a bad hairpin kind.
	s2 := one(t, "4/4", 0, "qC5 qr hE5", "hC4 hD4")
	if err := AddSpan(s2, loc(1, 0), Loc{Voice: 1, Bar: 1, Event: 1}, SpanSlur, ""); err == nil {
		t.Error("a slur across voices")
	}
	if err := AddSpan(s2, loc(1, 0), loc(1, 0), SpanSlur, ""); err == nil {
		t.Error("a slur on one note")
	}
	if err := AddSpan(s2, loc(1, 0), loc(1, 1), SpanSlur, ""); err == nil {
		t.Error("a slur ending on a rest")
	}
	if err := AddSpan(s2, loc(1, 0), loc(1, 2), SpanHairpin, "louder"); err == nil {
		t.Error("a hairpin of no kind")
	}
	// The written spans read back as the engraver and interpreter read
	// them: the score still validates.
	if is := s.Validate(nil); score.HasErrors(is) {
		t.Errorf("issues: %v", is)
	}
	if n, ok := NextNote(s2, loc(1, 0)); !ok || n != loc(1, 2) {
		t.Errorf("NextNote skips the rest: %v", n)
	}
}

func TestGraceNoteEntry(t *testing.T) {
	s := one(t, "4/4", 0, "qC5 qr hE5")
	g := note("D5", "e")
	g.Grace = score.Acciaccatura
	if _, err := InsertGrace(s, loc(1, 1), g); err != nil {
		t.Fatal(err)
	}
	g2 := note("C5", "s")
	g2.Grace = score.Appoggiatura
	// After the one there: the next main event has moved to index 2.
	if _, err := InsertGrace(s, loc(1, 2), g2); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5 /eD5 ^sC5 qr hE5" {
		t.Fatalf("graces: %q", got)
	}
	// The note written over the rest keeps them.
	if _, err := Write(s, 0, 0, 0, 1, score.F(1, 4), note("B4", "q")); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5 /eD5 ^sC5 qB4 hE5" {
		t.Errorf("written over the rest: %q", got)
	}
	// Changing a grace's kind; refusals.
	if err := SetGrace(s, loc(1, 1), score.Appoggiatura); err != nil {
		t.Fatal(err)
	}
	if err := SetGrace(s, loc(1, 0), score.Appoggiatura); err == nil {
		t.Error("a main note made a grace kind")
	}
	if _, err := InsertGrace(s, loc(1, 1), g); err == nil {
		t.Error("a grace before a grace")
	}
	if _, err := InsertGrace(s, loc(1, 0), note("D5", "e")); err == nil {
		t.Error("a main note inserted as a grace")
	}
	if got := barText(s, 0, 1); got != "qC5 ^eD5 ^sC5 qB4 hE5" {
		t.Errorf("after: %q", got)
	}
	if is := s.Validate(nil); score.HasErrors(is) {
		t.Errorf("issues: %v", is)
	}
}

func TestMoveToVoice(t *testing.T) {
	s := one(t, "4/4", 0, "qC5(p) /eE5 qD5(stacc) hE5")
	to, err := MoveToVoice(s, loc(1, 2), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5(p) qr hE5" {
		t.Errorf("voice 1 left: %q", got)
	}
	// A voice written into for the first time shows its rests.
	if got := barText(s, 1, 1); got != "qr /eE5 qD5(stacc) hr" {
		t.Errorf("voice 2: %q", got)
	}
	if to.Voice != 1 || Event(s, to).Notes[0].Pitch.String() != "D5" {
		t.Errorf("moved to %v", to)
	}
	// Back: voice 2's place prints nothing.
	if _, err := MoveToVoice(s, to, 0); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5(p) /eE5 qD5(stacc) hE5" {
		t.Errorf("moved back: %q", got)
	}
	if got := barText(s, 1, 1); got != "qr q_ hr" {
		t.Errorf("voice 2 after: %q", got)
	}
	if _, err := MoveToVoice(s, loc(1, 0), 0); err == nil {
		t.Error("a move into its own voice")
	}
	tup := one(t, "4/4", 0, "3{eC5 eD5 eE5} q.F5 eG5 qA5")
	if _, err := MoveToVoice(tup, loc(1, 1), 1); err == nil {
		t.Error("one note of a tuplet moved")
	}
}

func TestFormMarks(t *testing.T) {
	s := one(t, "4/4", 0, "wC5 | wC5 | wC5 | wC5 | wC5 | wC5 | wC5 | wC5")
	s.Bars = 8
	if err := SetRepeat(s, 2, 4, 0); err != nil {
		t.Fatal(err)
	}
	if err := SetVolta(s, 4, 4, []int{1}); err != nil {
		t.Fatal(err)
	}
	if err := SetVolta(s, 5, 5, []int{2}); err != nil {
		t.Fatal(err)
	}
	if err := SetVolta(s, 6, 6, []int{3}); err == nil {
		t.Error("a third ending of a repeat played twice")
	}
	if len(s.Voltas) != 2 {
		t.Errorf("the refused ending stayed: %v", s.Voltas)
	}
	// A D.S. needs its segno; al Coda its To Coda and coda.
	if err := SetJump(s, score.Jump{Kind: score.JumpDS, Bar: 8}); err == nil {
		t.Error("a D.S. with no segno")
	}
	if err := SetJump(s, score.Jump{Kind: score.JumpSegno, Bar: 2}); err != nil {
		t.Fatal(err)
	}
	if err := SetJump(s, score.Jump{Kind: score.JumpDS, Bar: 8, Al: score.JumpCoda}); err == nil {
		t.Error("a D.S. al Coda with no To Coda")
	}
	SetJump(s, score.Jump{Kind: score.JumpCoda, Bar: 7})
	SetJump(s, score.Jump{Kind: score.JumpToCoda, Bar: 3})
	if err := SetJump(s, score.Jump{Kind: score.JumpDS, Bar: 6, Al: score.JumpCoda}); err != nil {
		t.Fatal(err)
	}
	if len(s.Jumps) != 4 || s.Jumps[0].Kind != score.JumpSegno || s.Jumps[3].Kind != score.JumpCoda {
		t.Errorf("jumps in order: %v", s.Jumps)
	}
	if RemoveJumps(s, 6, 0) != 1 {
		t.Error("RemoveJumps")
	}
	// A repeat over the old one's bars replaces it and takes its endings.
	if err := SetRepeat(s, 3, 5, 3); err != nil {
		t.Fatal(err)
	}
	if len(s.Repeats) != 1 || s.Repeats[0].Plays() != 3 {
		t.Errorf("repeats %v", s.Repeats)
	}
	if err := SetRepeat(s, 6, 12, 0); err == nil {
		t.Error("a repeat past the end")
	}
	// Tempo marks and text.
	if err := SetTempoMark(s, score.TempoMark{At: score.Pos{Bar: 5}, Text: "rit.", To: score.Pos{Bar: 6, Offset: score.F(1, 2)}, HasTo: true}); err != nil {
		t.Fatal(err)
	}
	if err := SetTempoMark(s, score.TempoMark{At: score.Pos{Bar: 7}, BPM: 80, Text: "a tempo"}); err != nil {
		t.Fatal(err)
	}
	if err := SetTempoMark(s, score.TempoMark{At: score.Pos{Bar: 3}}); err == nil {
		t.Error("a tempo mark of nothing")
	}
	if len(s.Tempo) != 3 || TempoMarkAt(s, score.Pos{Bar: 5}).Text != "rit." || !RemoveTempoMark(s, score.Pos{Bar: 7}) {
		t.Errorf("tempo %v", s.Tempo)
	}
	// Rehearsal marks: named, unique.
	if err := SetSection(s, 3, "verse"); err != nil {
		t.Fatal(err)
	}
	if err := SetSection(s, 5, "verse"); err == nil {
		t.Error("two rehearsal marks of one name")
	}
	if err := SetSection(s, 5, " "); err == nil {
		t.Error("a nameless rehearsal mark")
	}
	SetSection(s, 1, "intro")
	if s.Sections[0].Name != "intro" || !RemoveSection(s, 3) || len(s.Sections) != 1 {
		t.Errorf("sections %v", s.Sections)
	}
	if ps, err := ParsePasses("1, 3-4"); err != nil || len(ps) != 3 || ps[2] != 4 {
		t.Errorf("passes %v %v", ps, err)
	}
	if _, err := ParsePasses("x"); err == nil {
		t.Error("bad passes read")
	}
	if is := s.Validate(nil); score.HasErrors(is) {
		t.Errorf("issues: %v", is)
	}
}

func TestClefChanges(t *testing.T) {
	s := one(t, "4/4", 0, "wC5 | wC5")
	at := score.Pos{Bar: 2, Offset: score.F(1, 2)}
	if err := SetClefChange(s, 0, 0, at, "bass"); err != nil {
		t.Fatal(err)
	}
	if err := SetClefChange(s, 0, 0, at, "alto"); err != nil {
		t.Fatal(err)
	}
	st := Staff(s, 0, 0)
	if len(st.Clefs) != 1 || st.ClefAt(at) != "alto" {
		t.Errorf("clefs %v", st.Clefs)
	}
	if err := SetClefChange(s, 0, 0, score.Pos{Bar: 1}, "tenor"); err != nil || st.Clef != "tenor" {
		t.Errorf("a change at the start is the staff's clef: %v %s", err, st.Clef)
	}
	if err := SetClefChange(s, 0, 0, score.Pos{Bar: 2, Offset: score.Whole(1)}, "bass"); err == nil {
		t.Error("a change past the bar")
	}
	if err := SetClefChange(s, 0, 0, at, "kazoo"); err == nil {
		t.Error("no such clef")
	}
	if !RemoveClefChange(s, 0, 0, at) || len(st.Clefs) != 0 {
		t.Error("RemoveClefChange")
	}
}
