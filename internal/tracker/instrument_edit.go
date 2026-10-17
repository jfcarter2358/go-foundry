package tracker

// instrument_edit.go — the edits an instrument editor makes to velocity
// layers, round robin and articulations, as plain operations on the model
// so the Tracker's editor (and its tests) share one set of rules:
//
//   - a zone split by velocity becomes two layers over 0..63 and 64..127,
//     its round robin going with its sample into the lower one; a further
//     split halves the widest layer; removing a layer gives its range to
//     the neighbour below (above for the lowest), and the last layer folds
//     back into the zone;
//   - a round-robin take is a sample slot added to a zone's (or a layer's)
//     alternates;
//   - an articulation is added under a name no other has, takes its own
//     copy of the instrument's zones when it is to play other samples, and
//     drops them to play the instrument's again.

import (
	"fmt"
	"strings"
)

// SplitByVelocity adds a velocity layer to a zone and returns its index:
// a zone with none becomes two (its sample and round robin below 64, the
// same sample above, to be pointed at the louder recording); a layered
// zone's widest layer is halved, the new half above it.
func (z *Zone) SplitByVelocity() int {
	if len(z.Layers) == 0 {
		z.Layers = []Layer{
			{Sample: z.Sample, RoundRobin: z.RoundRobin, VelLow: 0, VelHigh: 63},
			{Sample: z.Sample, VelLow: 64, VelHigh: 127},
		}
		z.RoundRobin = nil
		return 1
	}
	wide := 0
	for i, l := range z.Layers {
		if int(l.VelHigh)-int(l.VelLow) > int(z.Layers[wide].VelHigh)-int(z.Layers[wide].VelLow) {
			wide = i
		}
	}
	l := z.Layers[wide]
	if l.VelHigh == l.VelLow {
		// Too narrow to halve: a layer over the same velocity, which
		// crossfades with it.
		z.Layers = append(z.Layers, Layer{Sample: l.Sample, VelLow: l.VelLow, VelHigh: l.VelHigh})
		return len(z.Layers) - 1
	}
	mid := uint8((int(l.VelLow) + int(l.VelHigh)) / 2)
	z.Layers[wide].VelHigh = mid
	up := Layer{Sample: l.Sample, VelLow: mid + 1, VelHigh: l.VelHigh, Gain: l.Gain}
	z.Layers = append(z.Layers, Layer{})
	copy(z.Layers[wide+2:], z.Layers[wide+1:])
	z.Layers[wide+1] = up
	return wide + 1
}

// RemoveLayer drops layer i, its velocities going to the layer below it
// (the one above, for the lowest); the zone's last layer folds back into
// the zone itself, sample and round robin.
func (z *Zone) RemoveLayer(i int) {
	if i < 0 || i >= len(z.Layers) {
		return
	}
	gone := z.Layers[i]
	z.Layers = append(z.Layers[:i], z.Layers[i+1:]...)
	switch {
	case len(z.Layers) == 1:
		l := z.Layers[0]
		z.Sample, z.RoundRobin, z.Layers = l.Sample, l.RoundRobin, nil
		return
	case len(z.Layers) == 0:
		z.Sample, z.RoundRobin, z.Layers = gone.Sample, gone.RoundRobin, nil
		return
	}
	// The neighbours by velocity: the nearest range under it, else over.
	below, above := -1, -1
	for j, l := range z.Layers {
		if l.VelHigh < gone.VelLow && (below < 0 || l.VelHigh > z.Layers[below].VelHigh) {
			below = j
		}
		if l.VelLow > gone.VelHigh && (above < 0 || l.VelLow < z.Layers[above].VelLow) {
			above = j
		}
	}
	switch {
	case below >= 0:
		z.Layers[below].VelHigh = max(z.Layers[below].VelHigh, gone.VelHigh)
	case above >= 0:
		z.Layers[above].VelLow = min(z.Layers[above].VelLow, gone.VelLow)
	}
}

// SetLayerVelocity sets layer i's range, clamped to 0..127 with low ≤ high.
func (z *Zone) SetLayerVelocity(i, low, high int) {
	if i < 0 || i >= len(z.Layers) {
		return
	}
	low, high = max(0, min(127, low)), max(0, min(127, high))
	if low > high {
		low, high = high, low
	}
	z.Layers[i].VelLow, z.Layers[i].VelHigh = uint8(low), uint8(high)
}

// roundRobin is the alternates of a zone (layer < 0) or of one of its
// layers, nil when there is no such layer.
func (z *Zone) roundRobin(layer int) *[]int {
	if layer < 0 {
		return &z.RoundRobin
	}
	if layer >= len(z.Layers) {
		return nil
	}
	return &z.Layers[layer].RoundRobin
}

// AddRoundRobin adds a sample slot as another take of a zone's sample
// (layer < 0) or of a layer's, played in turn with it.
func (z *Zone) AddRoundRobin(layer, sample int) {
	if rr := z.roundRobin(layer); rr != nil {
		*rr = append(*rr, sample)
	}
}

// SetRoundRobin points take i at another sample slot.
func (z *Zone) SetRoundRobin(layer, i, sample int) {
	if rr := z.roundRobin(layer); rr != nil && i >= 0 && i < len(*rr) {
		(*rr)[i] = sample
	}
}

// RemoveRoundRobin drops take i.
func (z *Zone) RemoveRoundRobin(layer, i int) {
	if rr := z.roundRobin(layer); rr != nil && i >= 0 && i < len(*rr) {
		*rr = append((*rr)[:i], (*rr)[i+1:]...)
		if len(*rr) == 0 {
			*rr = nil
		}
	}
}

// Takes is how many recordings a zone (layer < 0) or a layer plays in
// turn: its sample and its round robin.
func (z *Zone) Takes(layer int) int {
	if rr := z.roundRobin(layer); rr != nil {
		return len(*rr) + 1
	}
	return 0
}

// CommonArticulations are the names an editor offers for a new
// articulation (the ones notation marks and technique text reach).
var CommonArticulations = []string{"sustain", "staccato", "legato", "pizzicato", "tremolo", "muted", "spiccato", "marcato", "harmonics"}

// AddArticulation adds an articulation under name (or, taken, "name 2"…)
// playing the instrument's own zones, and returns its index (the Tracker's
// @ number less one).
func (in *Instrument) AddArticulation(name string) int {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "articulation"
	}
	taken := func(n string) bool {
		for _, a := range in.Articulations {
			if strings.EqualFold(a.Name, n) {
				return true
			}
		}
		return false
	}
	base := name
	for k := 2; taken(name); k++ {
		name = fmt.Sprintf("%s %d", base, k)
	}
	in.Articulations = append(in.Articulations, Articulation{Name: name, VolEnv: EnvelopeOff()})
	return len(in.Articulations) - 1
}

// RemoveArticulation drops articulation i (the ones after it move down an
// @ number).
func (in *Instrument) RemoveArticulation(i int) {
	if i < 0 || i >= len(in.Articulations) {
		return
	}
	in.Articulations = append(in.Articulations[:i], in.Articulations[i+1:]...)
}

// MoveArticulation moves articulation i by d places (its @ number with it).
func (in *Instrument) MoveArticulation(i, d int) {
	j := i + d
	if i < 0 || i >= len(in.Articulations) || j < 0 || j >= len(in.Articulations) {
		return
	}
	in.Articulations[i], in.Articulations[j] = in.Articulations[j], in.Articulations[i]
}

// RenameArticulation names articulation i, refusing an empty name or one
// another articulation has.
func (in *Instrument) RenameArticulation(i int, name string) error {
	name = strings.TrimSpace(name)
	if i < 0 || i >= len(in.Articulations) {
		return fmt.Errorf("no articulation %d", i+1)
	}
	if name == "" {
		return fmt.Errorf("an articulation needs a name")
	}
	for j, a := range in.Articulations {
		if j != i && strings.EqualFold(a.Name, name) {
			return fmt.Errorf("%q is taken", name)
		}
	}
	in.Articulations[i].Name = name
	return nil
}

// SetTriggers sets articulation i's trigger words from a comma-separated
// list ("pizz., pizzicato"), dropping blanks and repeats.
func (in *Instrument) SetTriggers(i int, list string) {
	if i < 0 || i >= len(in.Articulations) {
		return
	}
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.Split(list, ",") {
		w = strings.TrimSpace(w)
		if w == "" || seen[strings.ToLower(w)] {
			continue
		}
		seen[strings.ToLower(w)] = true
		out = append(out, w)
	}
	in.Articulations[i].Triggers = out
}

// SetOwnZones gives articulation i a copy of the instrument's zones to
// point at other samples (own), or drops its zones so it plays the
// instrument's (an articulation that is only an envelope or a gain).
func (in *Instrument) SetOwnZones(i int, own bool) {
	if i < 0 || i >= len(in.Articulations) {
		return
	}
	a := &in.Articulations[i]
	switch {
	case !own:
		a.Zones = nil
	case len(a.Zones) == 0:
		a.Zones = cloneZones(in.Zones)
		if len(a.Zones) == 0 {
			// An instrument with no keymap plays its first sample
			// everywhere; the articulation's own starts the same.
			a.Zones = []Zone{{Sample: 0, Low: NoteMin, High: NoteMax}}
		}
	}
}
