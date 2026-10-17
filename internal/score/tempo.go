package score

// tempo.go — what tempo text means.
//
// A tempo mark is a number, a text, or both. A mark with a number sets the
// tempo ("Andante ♩ = 92"). Text without a number changes it relative to
// what is playing, and the words are the musician's:
//
//	rit. rall. allarg. riten.       slow down gradually, to the next mark
//	accel. string. affrett.         speed up gradually, to the next mark
//	a tempo                         back to the tempo before the change
//	tempo primo (tempo I)           back to the piece's first tempo
//	meno mosso / più mosso          a step slower / faster
//
// "poco" makes a change half as big, "molto" half as big again. A gradual
// change runs to its To (the end of its dashes) or the next mark; its
// target is the next mark's number when that mark is where it ends, else
// its own number, else Factor() of the tempo it started from. A mark with
// Ramp (the file's `ramp: true`) is the older spelling: it moves from its
// own number to the next mark's.

import "strings"

// TempoChange is what a tempo mark does.
type TempoChange uint8

const (
	// TempoSet sets the tempo to the mark's number (or, with no number,
	// is text only).
	TempoSet TempoChange = iota
	// TempoRit slows down gradually.
	TempoRit
	// TempoAccel speeds up gradually.
	TempoAccel
	// TempoATempo goes back to the tempo before the last change.
	TempoATempo
	// TempoPrimo goes back to the first tempo of the piece.
	TempoPrimo
	// TempoMeno is a step slower (meno mosso).
	TempoMeno
	// TempoPiu is a step faster (più mosso).
	TempoPiu
)

// Gradual reports a change that moves over time (rit., accel.).
func (c TempoChange) Gradual() bool { return c == TempoRit || c == TempoAccel }

var tempoWords = []struct {
	prefix string
	change TempoChange
}{
	{"a tempo", TempoATempo}, {"tempo primo", TempoPrimo}, {"tempo i", TempoPrimo}, {"tempo 1", TempoPrimo},
	{"meno mosso", TempoMeno}, {"meno", TempoMeno}, {"più mosso", TempoPiu}, {"piu mosso", TempoPiu}, {"più", TempoPiu}, {"piu", TempoPiu},
	{"rit", TempoRit}, {"rall", TempoRit}, {"allarg", TempoRit}, {"riten", TempoRit}, {"slentando", TempoRit}, {"calando", TempoRit},
	{"accel", TempoAccel}, {"string", TempoAccel}, {"affrett", TempoAccel}, {"stretto", TempoAccel},
}

// tempoText splits a mark's text into its strength ("poco" 0.5, "molto"
// 1.5, plain 1) and the rest, lower case.
func tempoText(text string) (string, float64) {
	t := strings.ToLower(strings.TrimSpace(text))
	k := 1.0
	for _, w := range []struct {
		word string
		k    float64
	}{{"poco a poco ", 1}, {"poco ", 0.5}, {"un poco ", 0.5}, {"molto ", 1.5}, {"sempre ", 1}} {
		if strings.HasPrefix(t, w.word) {
			t, k = strings.TrimSpace(t[len(w.word):]), w.k
			break
		}
	}
	return t, k
}

// Change classifies the mark by its text. A mark whose text names no
// change is TempoSet.
func (t TempoMark) Change() TempoChange {
	if t.Ramp {
		return TempoSet
	}
	w, _ := tempoText(t.Text)
	for _, tw := range tempoWords {
		if strings.HasPrefix(w, tw.prefix) {
			// "tempo i" must not catch "tempo di valse" or "tempo istesso".
			if tw.change == TempoPrimo && len(w) > len(tw.prefix) && w[len(tw.prefix)] != ' ' && w[len(tw.prefix)] != '.' && w[len(tw.prefix)] != 'o' {
				continue
			}
			return tw.change
		}
	}
	return TempoSet
}

// Factor is how far a relative change moves the tempo with no number of
// its own: rit. to 3/4, accel. to 5/4, meno mosso 0.85, più mosso 1.15,
// scaled by "poco" and "molto". 1 for the rest.
func (t TempoMark) Factor() float64 {
	_, k := tempoText(t.Text)
	var f float64
	switch t.Change() {
	case TempoRit:
		f = 0.75
	case TempoAccel:
		f = 1.25
	case TempoMeno:
		f = 0.85
	case TempoPiu:
		f = 1.15
	default:
		return 1
	}
	return 1 + (f-1)*k
}
