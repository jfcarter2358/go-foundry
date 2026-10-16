package sfz

// build.go — regions as a .finst and its samples.
//
// A .finst maps notes to samples with zones (a key range each, split by
// velocity into layers, each layer a sample and its round-robin turns) and
// names other sets of zones as articulations. SFZ regions may overlap in
// any way, so the keyboard is cut wherever any region starts or stops and
// each stretch becomes one zone holding the regions over it: the engine
// takes the first zone that holds a note, and with disjoint zones that is
// always the right one. Within a zone, regions over the same velocities
// are one layer's round robin (seq_position, or lorand) — or, with no
// round robin, stacked sounds, of which the engine plays one.
//
// Keyswitches (sw_last) become articulations: the regions under the
// keyswitch sw_default names (else the lowest) are the instrument's own
// zones, every other keyswitch an articulation named from its sw_label
// ("Pizz" → pizzicato), so notation marks and technique text pick it the
// way they pick a starter instrument's. A region under no keyswitch plays
// in every articulation, as in SFZ.
//
// Each distinct way a region plays a sample file — its stretch (offset,
// end), loop, root note, tune and volume — is one library sample: a WAV
// and its .fsmp, since the .finst's zones carry none of those.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"foundry/internal/audio"
	"foundry/internal/tracker"
)

// ImportedCategory is the library folder imports land in, for instruments
// (sounds/instruments/imported/) and samples (sounds/samples/imported/).
const ImportedCategory = "imported"

// Options say what an import is called and where it goes.
type Options struct {
	// Name is the instrument's library name ("imported/chamber_strings");
	// "" takes ImportedCategory + "/" + the source file's name.
	Name string
	// Title is the instrument's display name; "" is made from the name.
	Title string
	// Description is written into the .finst; "" says where it came from.
	Description string
	// SoundsDir is the sounds folder Write writes into; "" is the
	// project's (internal/audio.SoundsDir).
	SoundsDir string
	// Source is the file imported, for the description.
	Source string
}

// Built is an imported instrument before it is written: its samples are
// decoded and installed (Instrument plays them as it is), and Write puts
// both into the library.
type Built struct {
	// Name is the instrument's library name.
	Name       string
	Instrument *tracker.Instrument
	Samples    []*tracker.Sample
	// Warnings are what the import could not carry over.
	Warnings []string
}

// Import reads an .sfz and writes it into the library as a .finst and its
// samples.
func Import(path string, o Options) (*Built, error) {
	f, err := ParseFile(path)
	if err != nil {
		return nil, err
	}
	if o.Source == "" {
		o.Source = filepath.Base(path)
	}
	if o.Name == "" {
		o.Name = ImportedCategory + "/" + Slug(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	b, err := Build(f, o)
	if err != nil {
		return nil, err
	}
	return b, b.Write(o.SoundsDir)
}

// Build turns parsed regions into an instrument, decoding every sample the
// regions name.
func Build(f *File, o Options) (*Built, error) {
	name := strings.Trim(strings.TrimSuffix(filepath.ToSlash(o.Name), tracker.InstrumentExt), "/")
	if name == "" {
		return nil, fmt.Errorf("an imported instrument needs a name")
	}
	for _, part := range strings.Split(name, "/") {
		// A library name, never a path out of the library.
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `\:`) {
			return nil, fmt.Errorf("%q is not an instrument name (want category/name)", o.Name)
		}
	}
	bd := &builder{f: f, name: name, audio: map[string]*Audio{}, failed: map[string]bool{},
		variants: map[variantKey]int{}, sampleNames: map[string]bool{}, ownAudio: map[*Audio]string{}}
	bd.warnings = append(bd.warnings, f.Warnings...)
	built, err := bd.build(o)
	if err != nil {
		return nil, err
	}
	built.Warnings = append(built.Warnings, bd.summary()...)
	return built, nil
}

// builder holds an import's state.
type builder struct {
	f    *File
	name string
	// regions are the regions that play, with their audio.
	regions []Region
	auds    []*Audio
	audio   map[string]*Audio
	failed  map[string]bool
	// variants are the library samples made so far, by how they play.
	variants    map[variantKey]int
	samples     []*tracker.Sample
	sampleNames map[string]bool
	ownAudio    map[*Audio]string
	warnings    []string
	// Counted problems, said once each at the end.
	releaseTriggers, stacked, belowRange, oneShots, sustainLoops int
}

// variantKey is one way of playing one sample file.
type variantKey struct {
	src                string
	offset, end        int64
	loop               tracker.LoopMode
	loopStart, loopEnd int64
	base               uint8
	tune, volume       float64
}

func (bd *builder) warn(format string, a ...any) {
	bd.warnings = append(bd.warnings, fmt.Sprintf(format, a...))
}

func (bd *builder) build(o Options) (*Built, error) {
	for _, r := range bd.f.Regions {
		switch r.Trigger {
		case "release":
			bd.releaseTriggers++
			continue
		}
		a := r.Audio
		if a == nil {
			a = bd.load(r.Sample, r.Where)
			if a == nil {
				continue
			}
		}
		if r.LoopMode == LoopOneShot {
			bd.oneShots++
		}
		if r.LoopMode == LoopSustain {
			bd.sustainLoops++
		}
		bd.regions = append(bd.regions, r)
		bd.auds = append(bd.auds, a)
	}
	if len(bd.regions) == 0 {
		return nil, fmt.Errorf("no region has a sample that loads")
	}

	// The keyswitches, and the regions each picks.
	sets := map[int][]int{}
	var free []int
	for i, r := range bd.regions {
		if r.SwLast >= 0 {
			sets[r.SwLast] = append(sets[r.SwLast], i)
		} else {
			free = append(free, i)
		}
	}
	var switches []int
	for k := range sets {
		switches = append(switches, k)
	}
	sort.Ints(switches)
	def := -1
	if len(switches) > 0 {
		def = switches[0]
		if _, ok := sets[bd.f.SwDefault]; ok {
			def = bd.f.SwDefault
		}
	}
	defSet := append(append([]int(nil), sets[def]...), free...)
	if def < 0 {
		defSet = free
	}
	sort.Ints(defSet)

	in := tracker.NewInstrument(o.Title, "")
	if in.Name == "" {
		in.Name = Title(bd.name[strings.LastIndex(bd.name, "/")+1:])
	}
	in.Description = o.Description
	if in.Description == "" && o.Source != "" {
		in.Description = "Imported from " + o.Source
	}
	in.Polyphony = 4
	in.Zones = bd.zones(defSet)
	if len(in.Zones) == 0 {
		return nil, fmt.Errorf("no region maps to a playable key")
	}
	first := bd.regions[defSet[0]]
	in.VolEnv, in.FadeOut = envelope(first.Env)
	if bd.oneShots == len(bd.regions) {
		// Every sample plays to its end: a note-off must not cut it short.
		in.FadeOut = float32(min(bd.longest(), 10))
	}
	bd.uniform(in, defSet)

	names := map[string]bool{}
	for _, sw := range switches {
		if sw == def {
			continue
		}
		set := append(append([]int(nil), sets[sw]...), free...)
		sort.Ints(set)
		label := bd.regions[sets[sw][0]].SwLabel
		art := tracker.Articulation{}
		art.Name, art.Triggers = ArticulationName(label, sw)
		for names[art.Name] {
			art.Name += "+"
		}
		names[art.Name] = true
		art.Zones = bd.zones(set)
		if r := bd.regions[sets[sw][0]]; r.Env != first.Env {
			env, fade := envelope(r.Env)
			if env.On() {
				art.VolEnv = env
			}
			art.FadeOut = fade
		}
		in.Articulations = append(in.Articulations, art)
	}
	if def >= 0 {
		// The default keyswitch's own words still pick the default zones
		// when the instrument has no articulation by that name.
		dn, _ := ArticulationName(bd.regions[sets[def][0]].SwLabel, def)
		if dn != "sustain" && !names[dn] {
			in.Articulations = append(in.Articulations, tracker.Articulation{Name: dn, Triggers: triggersFor(bd.regions[sets[def][0]].SwLabel), Zones: in.Zones})
		}
	}

	bd.playback(in, defSet)
	for i, s := range bd.samples {
		in.Samples = append(in.Samples, s.Name)
		in.SetLoaded(i, s)
	}
	return &Built{Name: bd.name, Instrument: in, Samples: bd.samples, Warnings: bd.warnings}, nil
}

// load decodes a sample file once.
func (bd *builder) load(path, where string) *Audio {
	if a, ok := bd.audio[path]; ok {
		return a
	}
	if bd.failed[path] {
		return nil
	}
	a, err := loadAudio(path)
	if err != nil {
		bd.failed[path] = true
		reason := err.Error()
		if os.IsNotExist(err) {
			reason = "no such file"
		} else if strings.EqualFold(pathExt(path), ".flac") {
			reason = "FLAC samples are not read (convert them to WAV)"
		}
		bd.warn("%s: sample %s: %s", where, filepath.Base(path), reason)
		return nil
	}
	bd.audio[path] = a
	return a
}

// longest is the longest sample made, in seconds.
func (bd *builder) longest() float64 {
	l := 0.0
	for _, s := range bd.samples {
		if s.Rate > 0 {
			l = math.Max(l, float64(s.Frames())/float64(s.Rate))
		}
	}
	return l
}

// tkNote is a MIDI key as a tracker note (MIDI 60 is tracker.NoteC4), or
// 0 below the tracker's range.
func tkNote(midi int) uint8 {
	n := midi - 60 + int(tracker.NoteC4)
	if n < int(tracker.NoteMin) {
		return 0
	}
	return uint8(min(n, int(tracker.NoteMax)))
}

// zones builds the zones for a set of regions (indices into bd.regions).
func (bd *builder) zones(set []int) []tracker.Zone {
	// Every place a region's key range starts or stops cuts the keyboard.
	var cuts []int
	seen := map[int]bool{}
	for _, i := range set {
		r := bd.regions[i]
		lo, hi := r.LoKey, r.HiKey
		if hi < lo || hi < 0 {
			continue
		}
		for _, c := range []int{lo, hi + 1} {
			if !seen[c] {
				seen[c] = true
				cuts = append(cuts, c)
			}
		}
	}
	sort.Ints(cuts)
	type stretch struct {
		lo, hi int
		set    []int
	}
	var ss []stretch
	for k := 0; k+1 < len(cuts); k++ {
		lo, hi := cuts[k], cuts[k+1]-1
		var over []int
		for _, i := range set {
			if r := bd.regions[i]; r.LoKey <= lo && r.HiKey >= hi {
				over = append(over, i)
			}
		}
		if len(over) == 0 {
			continue
		}
		if n := len(ss); n > 0 && ss[n-1].hi == lo-1 && sameInts(ss[n-1].set, over) {
			ss[n-1].hi = hi
			continue
		}
		ss = append(ss, stretch{lo, hi, over})
	}
	var out []tracker.Zone
	for _, s := range ss {
		lo, hi := tkNote(s.lo), tkNote(s.hi)
		if hi == 0 {
			bd.belowRange++
			continue
		}
		if lo == 0 {
			lo = tracker.NoteMin
		}
		if z, ok := bd.zone(s.set); ok {
			z.Low, z.High = lo, hi
			// Neighbours that play alike are one zone: different regions
			// over them often come to the same library samples (the
			// keyboard is cut for every region, and regions that play a
			// file the same way share its sample).
			if n := len(out); n > 0 && out[n-1].High+1 == lo && sameZone(out[n-1], z) {
				out[n-1].High = hi
				continue
			}
			out = append(out, z)
		}
	}
	return out
}

// sameZone reports whether two zones play alike, whatever their keys.
func sameZone(a, b tracker.Zone) bool {
	a.Low, a.High, b.Low, b.High = 0, 0, 0, 0
	return reflect.DeepEqual(a, b)
}

// zone builds one zone from the regions over it: a layer per velocity
// range, each layer's round robin in turn order.
func (bd *builder) zone(set []int) (tracker.Zone, bool) {
	type layer struct {
		lo, hi int
		rs     []int
	}
	var ls []layer
	for _, i := range set {
		r := bd.regions[i]
		lo, hi := min(max(r.LoVel, 0), 127), min(max(r.HiVel, 0), 127)
		if hi < lo {
			continue
		}
		found := false
		for k := range ls {
			if ls[k].lo == lo && ls[k].hi == hi {
				ls[k].rs = append(ls[k].rs, i)
				found = true
				break
			}
		}
		if !found {
			ls = append(ls, layer{lo, hi, []int{i}})
		}
	}
	sort.SliceStable(ls, func(a, b int) bool {
		if ls[a].lo != ls[b].lo {
			return ls[a].lo < ls[b].lo
		}
		return ls[a].hi < ls[b].hi
	})
	var z tracker.Zone
	var layers []tracker.Layer
	for _, l := range ls {
		turns := bd.turns(l.rs)
		var smp []int
		for _, i := range turns {
			if k, ok := bd.sample(i); ok {
				smp = append(smp, k)
			}
		}
		if len(smp) == 0 {
			continue
		}
		layers = append(layers, tracker.Layer{Sample: smp[0], RoundRobin: smp[1:], VelLow: uint8(l.lo), VelHigh: uint8(l.hi)})
	}
	switch {
	case len(layers) == 0:
		return z, false
	case len(layers) == 1 && layers[0].VelLow == 0 && layers[0].VelHigh == 127:
		z.Sample, z.RoundRobin = layers[0].Sample, layers[0].RoundRobin
	default:
		z.Sample, z.Layers = layers[0].Sample, layers
	}
	if len(z.RoundRobin) == 0 {
		z.RoundRobin = nil
	}
	for k := range z.Layers {
		if len(z.Layers[k].RoundRobin) == 0 {
			z.Layers[k].RoundRobin = nil
		}
	}
	return z, true
}

// turns orders the regions of one layer as round-robin turns: by
// seq_position (or lorand), one region a turn. Regions sharing a turn are
// stacked sounds; the first is kept.
func (bd *builder) turns(rs []int) []int {
	rr := false
	for _, i := range rs {
		r := bd.regions[i]
		if r.SeqLength > 1 || r.LoRand > 0 || r.HiRand < 1 {
			rr = true
		}
	}
	if !rr {
		bd.stacked += len(rs) - 1
		return rs[:1]
	}
	sorted := append([]int(nil), rs...)
	sort.SliceStable(sorted, func(a, b int) bool {
		ra, rb := bd.regions[sorted[a]], bd.regions[sorted[b]]
		if ra.SeqPosition != rb.SeqPosition {
			return ra.SeqPosition < rb.SeqPosition
		}
		return ra.LoRand < rb.LoRand
	})
	var out []int
	for k, i := range sorted {
		if k > 0 {
			p, r := bd.regions[sorted[k-1]], bd.regions[i]
			if p.SeqPosition == r.SeqPosition && p.LoRand == r.LoRand {
				bd.stacked++
				continue
			}
		}
		out = append(out, i)
	}
	return out
}

// sample is the library sample (an index into bd.samples) for region i,
// made the first time it is asked for.
func (bd *builder) sample(i int) (int, bool) {
	r, a := bd.regions[i], bd.auds[i]
	frames := a.Frames()
	off := min(max(r.Offset, 0), frames)
	end := frames
	if r.End >= 0 {
		end = min(r.End+1, frames)
	}
	if end <= off {
		bd.warn("%s: offset %d and end %d leave nothing of %s", r.Where, r.Offset, r.End, filepath.Base(a.Name))
		return 0, false
	}
	// The loop: the region's, else the file's.
	ls, le := a.LoopStart, a.LoopEnd
	if r.LoopStart >= 0 {
		ls = r.LoopStart
	}
	if r.LoopEnd >= 0 {
		le = r.LoopEnd + 1
	}
	loop := tracker.LoopNone
	switch r.LoopMode {
	case LoopUnset:
		// SFZ: no loop, unless the file itself has one.
		if a.LoopEnd > a.LoopStart {
			loop = tracker.LoopForward
		}
	case LoopContinuous, LoopSustain:
		loop = tracker.LoopForward
		if le <= ls {
			ls, le = off, end
		}
	}
	ls, le = min(max(ls-off, 0), end-off), min(max(le-off, 0), end-off)
	if loop != tracker.LoopNone && le <= ls+1 {
		loop = tracker.LoopNone
	}
	if loop == tracker.LoopNone {
		ls, le = 0, 0
	}
	key := r.Key
	tune := r.Tune
	if key < 0 {
		key = a.Root
		tune += a.Tune
	}
	if key < 0 {
		key = 60
	}
	base := tkNote(key - r.Transpose)
	if base == 0 {
		base = tracker.NoteMin
	}
	src := a.Name
	if r.Audio != nil {
		if bd.ownAudio[a] == "" {
			bd.ownAudio[a] = fmt.Sprintf("%s#%d", a.Name, len(bd.ownAudio))
		}
		src = bd.ownAudio[a]
	}
	vk := variantKey{src: src, offset: off, end: end, loop: loop, loopStart: ls, loopEnd: le, base: base, tune: tune, volume: r.Volume}
	if k, ok := bd.variants[vk]; ok {
		return k, true
	}
	ch := a.Channels
	data := append([]float32(nil), a.Data[off*int64(ch):end*int64(ch)]...)
	s := &tracker.Sample{
		Name: bd.sampleName(a.Name), Data: data, Channels: ch, Rate: a.Rate,
		BaseNote: base, FineTune: float32(tune), Loop: loop, LoopStart: int(ls), LoopEnd: int(le),
		Volume: float32(math.Pow(10, r.Volume/20)), Tags: []string{ImportedCategory},
		Description: "from " + filepath.Base(a.Name),
	}
	bd.samples = append(bd.samples, s)
	k := len(bd.samples) - 1
	bd.variants[vk] = k
	return k, true
}

// sampleName is a new library name for a sample from src:
// imported/<instrument>/<file> (the instrument's name under imported/),
// with _2, _3… for the same file played another way.
func (bd *builder) sampleName(src string) string {
	base := Slug(strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)))
	if base == "" {
		base = "sample"
	}
	// The instrument's own folder under imported/, so two banks' "piano"
	// presets keep their samples apart.
	var parts []string
	for _, p := range strings.Split(strings.TrimPrefix(bd.name, ImportedCategory+"/"), "/") {
		if p = Slug(p); p != "" {
			parts = append(parts, p)
		}
	}
	dir := ImportedCategory + "/" + strings.Join(parts, "/")
	n := dir + "/" + base
	for k := 2; bd.sampleNames[n]; k++ {
		n = fmt.Sprintf("%s/%s_%d", dir, base, k)
	}
	bd.sampleNames[n] = true
	return n
}

// uniform carries over what every default region agrees on: a pan, a
// filter.
func (bd *builder) uniform(in *tracker.Instrument, set []int) {
	r0 := bd.regions[set[0]]
	samePan, sameFilter := true, true
	// Cutoffs a little apart (within half an octave: a SoundFont's per-key
	// envelope settings move them) are one filter at their geometric mean.
	lo, hi, logSum := math.Inf(1), 0.0, 0.0
	for _, i := range set {
		r := bd.regions[i]
		samePan = samePan && r.Pan == r0.Pan
		sameFilter = sameFilter && r.Filter == r0.Filter && math.Abs(r.Resonance-r0.Resonance) < 0.5 && (r.Cutoff > 0) == (r0.Cutoff > 0)
		if r.Cutoff > 0 {
			lo, hi = math.Min(lo, r.Cutoff), math.Max(hi, r.Cutoff)
			logSum += math.Log(r.Cutoff)
		}
	}
	if sameFilter && r0.Cutoff > 0 {
		if hi > lo*math.Sqrt2 {
			sameFilter = false
		} else if hi > lo {
			r0.Cutoff = math.Exp(logSum / float64(len(set)))
			bd.warn("regions' cutoffs run %.0f..%.0f Hz; the instrument filters at %.0f Hz", lo, hi, r0.Cutoff)
		}
	}
	if samePan {
		in.Pan = float32(clamp(r0.Pan/100, -1, 1))
	} else {
		bd.warn("regions pan differently; the instrument plays them centred")
	}
	if r0.Cutoff > 0 && sameFilter {
		switch {
		case strings.HasPrefix(r0.Filter, "hpf"):
			in.Filter = tracker.FilterHighPass
		case strings.HasPrefix(r0.Filter, "bpf"):
			in.Filter = tracker.FilterBandPass
		default:
			in.Filter = tracker.FilterLowPass
		}
		in.Cutoff = float32(clamp(r0.Cutoff, 20, 20000))
		in.Resonance = float32(clamp(r0.Resonance/24, 0, 1))
	} else if hi > 0 {
		bd.warn("regions filter differently; the instrument plays them unfiltered")
	}
}

// playback sets the playable range (keyswitches aside) and a clef for it.
func (bd *builder) playback(in *tracker.Instrument, set []int) {
	lo, hi := 128, -1
	for _, i := range set {
		r := bd.regions[i]
		lo, hi = min(lo, r.LoKey), max(hi, r.HiKey)
	}
	if bd.f.SwLoKey >= 0 && bd.f.SwHiKey >= bd.f.SwLoKey {
		// A keyswitch range at the bottom (or top) is not music.
		if lo >= bd.f.SwLoKey && lo <= bd.f.SwHiKey {
			lo = bd.f.SwHiKey + 1
		}
		if hi >= bd.f.SwLoKey && hi <= bd.f.SwHiKey {
			hi = bd.f.SwLoKey - 1
		}
	}
	if hi < lo {
		return
	}
	// A region over the whole keyboard says nothing about a range.
	if lo > 0 || hi < 127 {
		in.Playback.Low, in.Playback.High = max(tkNote(lo), tracker.NoteMin), max(tkNote(hi), tracker.NoteMin)
	}
	mid := (lo + hi) / 2
	switch {
	case hi-lo > 48 && lo < 48 && hi > 72:
		in.Playback.Clef = "grand"
	case mid < 55:
		in.Playback.Clef = "bass"
	default:
		in.Playback.Clef = "treble"
	}
}

// summary is the counted problems, in words.
func (bd *builder) summary() []string {
	var out []string
	if bd.releaseTriggers > 0 {
		out = append(out, fmt.Sprintf("%d release-trigger regions skipped (the engine has no release samples)", bd.releaseTriggers))
	}
	if bd.stacked > 0 {
		out = append(out, fmt.Sprintf("%d stacked regions dropped (a layer plays one sample a note)", bd.stacked))
	}
	if bd.belowRange > 0 {
		out = append(out, fmt.Sprintf("%d key ranges below C0 (MIDI 12) dropped", bd.belowRange))
	}
	if bd.sustainLoops > 0 {
		out = append(out, fmt.Sprintf("%d loop_sustain regions loop on through their release", bd.sustainLoops))
	}
	if n := len(bd.f.Ignored); n > 0 {
		var ks []string
		for k := range bd.f.Ignored {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		var parts []string
		for _, k := range ks {
			parts = append(parts, fmt.Sprintf("%s ×%d", k, bd.f.Ignored[k]))
		}
		out = append(out, "ignored: "+strings.Join(parts, ", "))
	}
	return out
}

// envelope turns an SFZ envelope into a volume envelope and a fade-out:
// no envelope at all when it only switches on and off (the fade-out is
// its release), otherwise breakpoints with the sustain point held.
func envelope(e Envelope) (tracker.Envelope, float32) {
	release := float32(math.Max(e.Release, 0.01))
	if !e.Shaped() {
		return tracker.EnvelopeOff(), release
	}
	env := tracker.EnvelopeOff()
	add := func(t, v float64) { env.Points = append(env.Points, tracker.EnvPoint{T: float32(t), V: float32(v)}) }
	t := 0.0
	start := 1.0
	if e.Delay > 0 || e.Attack > 0 {
		start = 0
	}
	add(0, start)
	if e.Delay > 0 {
		t = e.Delay
		add(t, 0)
	}
	if start == 0 {
		t += math.Max(e.Attack, 0.001)
		add(t, 1)
	}
	if e.Hold > 0 {
		t += e.Hold
		add(t, 1)
	}
	if e.Sustain < 0.999 {
		t += math.Max(e.Decay, 0.001)
		add(t, e.Sustain)
	}
	env.Sustain = len(env.Points) - 1
	t += float64(release)
	add(t, 0)
	return env, release
}

// Write puts the instrument and its samples into the library under
// soundsDir ("" is the project's sounds folder).
func (b *Built) Write(soundsDir string) error {
	if soundsDir == "" {
		soundsDir = audio.SoundsDir()
	}
	for _, s := range b.Samples {
		base := filepath.Join(soundsDir, tracker.SamplesDirName, filepath.FromSlash(s.Name))
		if err := tracker.SaveSampleTo(base, s); err != nil {
			return fmt.Errorf("sample %s: %w", s.Name, err)
		}
	}
	path := filepath.Join(soundsDir, tracker.InstrumentsDirName, filepath.FromSlash(b.Name)+tracker.InstrumentExt)
	if err := tracker.SaveInstrumentFile(path, b.Instrument); err != nil {
		return err
	}
	tracker.ReloadInstruments()
	tracker.ReloadSamples()
	return nil
}

// ArticulationName names a keyswitch's articulation from its label, the
// way the starter library names them, so notation finds it: "Pizz" is
// pizzicato, "Staccato" or "Spiccato" staccato, "Trem" tremolo, "Legato",
// "Con sord." muted, "Sustain" or "Arco" sustain. Any other label is its
// own name; no label is "keyswitch C0". The triggers are the label's own
// words.
func ArticulationName(label string, sw int) (string, []string) {
	l := strings.ToLower(strings.TrimSpace(label))
	trig := triggersFor(label)
	switch {
	case l == "":
		n := "C-0"
		if t := tkNote(sw); t > 0 {
			n = tracker.NoteName(t)
		}
		return "keyswitch " + strings.ReplaceAll(n, "-", ""), nil
	case strings.Contains(l, "pizz"):
		return "pizzicato", trig
	case strings.Contains(l, "trem"):
		return "tremolo", trig
	case strings.Contains(l, "stac") || strings.Contains(l, "spicc") || strings.Contains(l, "short"):
		return "staccato", trig
	case strings.Contains(l, "leg"):
		return "legato", trig
	case strings.Contains(l, "sord") || strings.Contains(l, "mute"):
		return "muted", trig
	case strings.Contains(l, "sus") || strings.Contains(l, "arco") || strings.Contains(l, "long"):
		return "sustain", trig
	case strings.Contains(l, "marc"):
		return "marcato", trig
	case strings.Contains(l, "harm"):
		return "harmonics", trig
	}
	return Slug(l), trig
}

// triggersFor is a label as trigger words: itself, lower case.
func triggersFor(label string) []string {
	l := strings.ToLower(strings.TrimSpace(label))
	if l == "" {
		return nil
	}
	return []string{l}
}

// Slug is a name fit for a library path: lower case, letters, digits and
// underscores.
func Slug(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			under = false
		case !under && b.Len() > 0:
			b.WriteByte('_')
			under = true
		}
	}
	return strings.TrimRight(b.String(), "_")
}

// Title is a slug as a display name: "chamber_strings" → "Chamber Strings".
func Title(slug string) string {
	f := strings.FieldsFunc(slug, func(r rune) bool { return r == '_' || r == '-' || r == ' ' })
	for i, w := range f {
		f[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(f, " ")
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func log10(v float64) float64 { return math.Log10(v) }
