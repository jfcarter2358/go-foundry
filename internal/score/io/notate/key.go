package notate

// key.go — guessing a key from the notes.

import (
	"math"

	"foundry/internal/score"
)

// Krumhansl–Kessler key profiles: how strongly each pitch class above the
// tonic belongs to a major and a minor key, from listening experiments.
var (
	majorProfile = [12]float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
	minorProfile = [12]float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}
)

// DetectKey guesses the key of some notes: the major or minor key whose
// profile correlates best with how long each pitch class sounds. It
// returns the signature (sharps positive) and whether it is minor; with no
// notes it is C major.
func DetectKey(notes []Note) (fifths int, minor bool) {
	var hist [12]float64
	total := 0.0
	for _, n := range notes {
		d := n.End.Sub(n.Start).Float()
		if d <= 0 {
			continue
		}
		// Long notes count, but not so much that one pedal drowns a tune.
		w := math.Min(d, 1)
		hist[((n.MIDI%12)+12)%12] += w
		total += w
	}
	if total == 0 {
		return 0, false
	}
	best, bestR := 0, math.Inf(-1)
	bestMinor := false
	for tonic := 0; tonic < 12; tonic++ {
		for _, mi := range []bool{false, true} {
			prof := majorProfile
			if mi {
				prof = minorProfile
			}
			var rot [12]float64
			for i := 0; i < 12; i++ {
				rot[i] = prof[(i-tonic+12)%12]
			}
			if r := correlate(hist, rot); r > bestR+1e-9 {
				best, bestR, bestMinor = tonic, r, mi
			}
		}
	}
	major := best
	if bestMinor {
		major = (best + 3) % 12
	}
	return FifthsOf(major), bestMinor
}

// FifthsOf is the signature of the major key on a pitch class, the one
// with fewer accidentals (F# and Gb, six either way, are Gb).
func FifthsOf(pc int) int {
	for _, f := range []int{0, 1, -1, 2, -2, 3, -3, 4, -4, 5, -5, -6} {
		if ((f*7)%12+12)%12 == pc {
			return f
		}
	}
	return 0
}

func correlate(a, b [12]float64) float64 {
	var ma, mb float64
	for i := 0; i < 12; i++ {
		ma += a[i]
		mb += b[i]
	}
	ma /= 12
	mb /= 12
	var num, da, db float64
	for i := 0; i < 12; i++ {
		x, y := a[i]-ma, b[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

// KeyChange is a key from a bar on (1-based), as DetectKeys finds it.
type KeyChange struct {
	Bar    int
	Fifths int
	Minor  bool
}

// keyWindow is how many bars around each bar its key is heard over, and
// keyChangeCost what moving to another key costs, in correlation: a
// modulation has to be heard over a few bars to be written.
const (
	keyWindow     = 4
	keyChangeCost = 1.6
)

// DetectKeys guesses a piece's keys bar by bar: each bar's notes and its
// neighbours' (keyWindow bars) correlated with the 24 key profiles, and the
// path through the bars that matches best with a cost on every change of
// key (dynamic programming), so a passing chord is not a modulation and a
// real one is. A key that changes only its mode (C major to A minor, the
// same signature) is not a change. The first entry is bar 1's key.
func DetectKeys(notes []Note, l Layout) []KeyChange {
	n := l.Bars()
	if n == 0 {
		return []KeyChange{{Bar: 1}}
	}
	hist := make([][12]float64, n)
	for _, nt := range notes {
		if nt.Grace {
			continue
		}
		for b := l.BarAt(nt.Start); b < n && l.Starts[b].Less(nt.End); b++ {
			a := score.MaxFrac(nt.Start, l.Starts[b])
			z := score.MinFrac(nt.End, l.Starts[b+1])
			if d := z.Sub(a).Float(); d > 0 {
				hist[b][((nt.MIDI%12)+12)%12] += math.Min(d, 1)
			}
		}
	}
	type key struct {
		tonic int
		minor bool
	}
	var keys []key
	var profs [][12]float64
	for tonic := 0; tonic < 12; tonic++ {
		for _, mi := range []bool{false, true} {
			prof := majorProfile
			if mi {
				prof = minorProfile
			}
			var rot [12]float64
			for i := 0; i < 12; i++ {
				rot[i] = prof[(i-tonic+12)%12]
			}
			keys = append(keys, key{tonic, mi})
			profs = append(profs, rot)
		}
	}
	// signature is a key's fifths: a minor key's relative major's.
	signature := func(k key) int {
		major := k.tonic
		if k.minor {
			major = (k.tonic + 3) % 12
		}
		return FifthsOf(major)
	}
	K := len(keys)
	cost := make([][]float64, n)
	from := make([][]int, n)
	for b := 0; b < n; b++ {
		var h [12]float64
		total := 0.0
		for w := max(0, b-keyWindow/2); w < min(n, b+keyWindow/2+1); w++ {
			for i := range h {
				h[i] += hist[w][i]
				total += hist[w][i]
			}
		}
		cost[b], from[b] = make([]float64, K), make([]int, K)
		for k := range keys {
			c := 0.0
			if total > 0 {
				c = -correlate(h, profs[k])
			}
			if b == 0 {
				cost[b][k] = c
				continue
			}
			best, bk := math.Inf(1), 0
			for j := range keys {
				step := cost[b-1][j]
				if signature(keys[j]) != signature(keys[k]) {
					step += keyChangeCost
				}
				if step < best {
					best, bk = step, j
				}
			}
			cost[b][k], from[b][k] = best+c, bk
		}
	}
	// Back from the best last key.
	k := 0
	for j := range keys {
		if cost[n-1][j] < cost[n-1][k] {
			k = j
		}
	}
	path := make([]int, n)
	for b := n - 1; b >= 0; b-- {
		path[b] = k
		if b > 0 {
			k = from[b][k]
		}
	}
	var out []KeyChange
	for b, k := range path {
		f := signature(keys[k])
		if len(out) > 0 && out[len(out)-1].Fifths == f {
			continue
		}
		out = append(out, KeyChange{Bar: b + 1, Fifths: f, Minor: keys[k].minor})
	}
	// A key with nothing heard in it (an empty file) is C major.
	if len(out) == 0 {
		out = []KeyChange{{Bar: 1}}
	}
	return out
}
