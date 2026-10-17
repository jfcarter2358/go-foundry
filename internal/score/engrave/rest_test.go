package engrave

import (
	"testing"

	"foundry/internal/score"
)

// parse reads an .fscore document for a test.
func parse(t testing.TB, text string) *score.Score {
	t.Helper()
	s, err := score.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// inkOf is the bounds of a list's items of some roles in a bar, voice
// and staff (voice < 0 for any).
func inkOf(l *List, bar, staff, voice int, roles ...Role) []Rect {
	var out []Rect
	for i := range l.Items {
		it := &l.Items[i]
		if it.Ref.Bar != bar || it.Ref.Staff != staff || (voice >= 0 && it.Ref.Voice != voice) {
			continue
		}
		for _, r := range roles {
			if it.Role == r {
				out = append(out, ItemBounds(it))
			}
		}
	}
	return out
}

func TestBarRestsClearTheOtherVoice(t *testing.T) {
	// Voice 2's bar rests under a voice 1 that comes down to the G line
	// (bars 1 and 3) and to C4 (bar 2); voice 1's over a high voice 2.
	l := Engrave(one(t, "treble",
		"| qE4 qF4 qG4 qA4 | qC4 qD4 hE4 | qG4 qG4 hG4 | R",
		"| R | R | R | qA5 qB5 hC6"), Options{})
	for bar := 1; bar <= 4; bar++ {
		rv := 1
		if bar == 4 {
			rv = 0
		}
		rests := inkOf(l, bar, 0, rv, RoleRest)
		if len(rests) != 1 {
			t.Fatalf("bar %d: %d rests", bar, len(rests))
		}
		for _, o := range inkOf(l, bar, 0, 1-rv, RoleNotehead, RoleStem, RoleLedger) {
			if overlap(rests[0], o, 0.02) {
				t.Errorf("bar %d: the bar rest %v touches the other voice's %v", bar, rests[0], o)
			}
		}
	}
	// Under the G line it leaves the staff: on a ledger line of its own.
	var leger int
	for _, it := range l.Items {
		if it.Role == RoleRest && it.Glyph == "restWholeLegerLine" {
			leger++
		}
	}
	if leger == 0 {
		t.Error("no bar rest drawn with its ledger line off the staff")
	}
	// With nothing in its way it stays at its voice's place.
	l = Engrave(one(t, "treble", "| qC6 qD6 hE6", "| R"), Options{})
	if r := itemsBy(l, RoleRest, 1, 1, 0); len(r) != 1 || r[0].P.Y-l.Staves[0].Top != 3 {
		t.Errorf("voice 2's bar rest under high notes at %v, want hanging from the G line (3)", r)
	}
}

func TestBarRestsClearNotesFromTheOtherStaff(t *testing.T) {
	// The left hand rests a bar while the right hand plays on its staff.
	s := parse(t, `version: 1
bars: 1
lines:
- name: Piano
  instrument: keys/grand_piano
  staves:
  - clef: treble
    voices:
    - "| qG3(cross=down) qB3(cross=down) qD4(cross=down) qG3(cross=down)"
  - clef: bass
    voices:
    - "| R"
`)
	l := Engrave(s, Options{})
	rests := inkOf(l, 1, 1, -1, RoleRest)
	if len(rests) != 1 {
		t.Fatalf("%d rests", len(rests))
	}
	for _, o := range inkOf(l, 1, 0, -1, RoleNotehead, RoleStem) {
		if overlap(rests[0], o, 0.02) {
			t.Errorf("the bass's bar rest %v touches the crossed notes' %v", rests[0], o)
		}
	}
}

func TestRestsStandBesideCrossStaffNotes(t *testing.T) {
	// Voice 2's rest beside a voice 1 chord written down on the bass
	// staff, whose stem reaches up into the treble.
	s := parse(t, `version: 1
bars: 1
lines:
- name: Piano
  instrument: keys/grand_piano
  staves:
  - clef: treble
    voices:
    - "| hA5 qG4+B4+D5(cross=down) qE5"
    - "| hF4 qr qC4"
  - clef: bass
    voices:
    - "| wC3"
`)
	l := Engrave(s, Options{})
	rest := itemsBy(l, RoleRest, 1, 1, 1)
	if len(rest) != 1 {
		t.Fatalf("%d rests", len(rest))
	}
	rb := ItemBounds(&rest[0])
	n := 0
	for i := range l.Items {
		it := &l.Items[i]
		if it.Ref.Bar == 1 && it.Ref.Voice == 0 && it.Ref.Event == 1 && (it.Role == RoleNotehead || it.Role == RoleStem) {
			n++
			if b := ItemBounds(it); overlap(rb, b, 0.02) {
				t.Errorf("the rest %v touches the cross-staff chord's %v %v", rb, it.Role, b)
			}
		}
	}
	if n < 4 {
		t.Fatalf("cross-staff chord: %d heads and stem", n)
	}
	// Same in one voice: the bass's rest beside the notes the treble
	// writes on its staff.
	s = parse(t, `version: 1
bars: 1
lines:
- name: Piano
  instrument: keys/grand_piano
  staves:
  - clef: treble
    voices:
    - "| qC4(cross=down) qD4 hE4"
  - clef: bass
    voices:
    - "| qr qC3 hC3"
`)
	l = Engrave(s, Options{})
	rest = itemsBy(l, RoleRest, 1, 0, 0)
	if len(rest) != 1 || rest[0].Ref.Staff != 1 {
		t.Fatalf("rests %v", rest)
	}
	rb = ItemBounds(&rest[0])
	for i := range l.Items {
		it := &l.Items[i]
		if it.Ref.Staff == 0 && it.Ref.Event == 0 && (it.Role == RoleNotehead || it.Role == RoleStem) {
			if b := ItemBounds(it); overlap(rb, b, 0.02) {
				t.Errorf("the bass's rest %v touches the crossed C4's %v %v", rb, it.Role, b)
			}
		}
	}
}

func TestRestsFarOffTheStaffHaveLedgerLines(t *testing.T) {
	// Voice 2's bar rest under A3: hanging from the third ledger line, the
	// two above it drawn too.
	l := Engrave(one(t, "treble", "| qC4 qB3 hA3", "| R"), Options{})
	top := l.Staves[0].Top
	r := itemsBy(l, RoleRest, 1, 1, 0)
	if len(r) != 1 || r[0].Glyph != "restWholeLegerLine" {
		t.Fatalf("rest %v", r)
	}
	y := r[0].P.Y - top
	if y < 6 {
		t.Fatalf("the rest hangs from %g, want under the A3's ledger lines", y)
	}
	lines := map[float64]bool{}
	for _, it := range itemsBy(l, RoleLedger, 1, 1, 0) {
		lines[it.P.Y-top] = true
	}
	for L := 5.0; L < y; L++ {
		if !lines[L] {
			t.Errorf("no ledger line at %g for the rest at %g (have %v)", L, y, lines)
		}
	}
}
