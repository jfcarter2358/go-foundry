package notate

// key.go — guessing a key from the notes.

import "math"

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
