// Package sf2 imports SoundFont 2 banks as .finst instruments.
//
// An SF2 file is a RIFF 'sfbk' with three lists: INFO (the bank's name),
// sdta (every sample's 16-bit audio, one after another, with an optional
// sm24 chunk of extra low bytes) and pdta (the "hydra": presets, which
// layer instruments, which map samples by key and velocity, each level a
// list of zones carrying generators — key and velocity ranges, tuning,
// loop offsets, the volume envelope, pan, attenuation).
//
// Each preset is read as SFZ regions (internal/score/io/sfz): one region
// per instrument zone that sounds under a preset zone, with the preset's
// generators added to the instrument's as the specification says (ranges
// intersect; everything else adds). A left/right pair of linked samples
// under the same keys becomes one stereo sample. sfz.Build then makes the
// .finst and its samples exactly as it does for an .sfz, so the two
// importers agree on zones, layers, loops and envelopes:
//
//	sounds/instruments/imported/<bank>.finst              (one preset)
//	sounds/instruments/imported/<bank>/<preset>.finst      (several)
//	sounds/samples/imported/<bank>[/<preset>]/<sample>.wav + .fsmp
//
// Modulators, the modulation envelope and LFOs have no place in a .finst;
// the import's warnings count them.
package sf2

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/score/io/sfz"
)

// Generator numbers (SoundFont 2.04, section 8.1.2) the importer reads.
const (
	genStartOffset       = 0
	genEndOffset         = 1
	genStartLoopOffset   = 2
	genEndLoopOffset     = 3
	genStartCoarseOffset = 4
	genFilterFc          = 8
	genFilterQ           = 9
	genModEnvToFilterFc  = 11
	genDecayModEnv       = 28
	genSustainModEnv     = 29
	genEndCoarseOffset   = 12
	genPan               = 17
	genDelayVolEnv       = 33
	genAttackVolEnv      = 34
	genHoldVolEnv        = 35
	genDecayVolEnv       = 36
	genSustainVolEnv     = 37
	genReleaseVolEnv     = 38
	genInstrument        = 41
	genKeyRange          = 43
	genVelRange          = 44
	genStartLoopCoarse   = 45
	genAttenuation       = 48
	genEndLoopCoarse     = 50
	genCoarseTune        = 51
	genFineTune          = 52
	genSampleID          = 53
	genSampleModes       = 54
	genScaleTuning       = 56
	genExclusiveClass    = 57
	genRootKey           = 58
)

// unused are generators with no place in a .finst, by name, for the
// warnings (the modulation envelope, the LFOs, chorus and reverb sends).
var unused = map[uint16]string{
	5: "modLfoToPitch", 6: "vibLfoToPitch", 7: "modEnvToPitch", 10: "modLfoToFilterFc", 11: "modEnvToFilterFc",
	13: "modLfoToVolume", 15: "chorusEffectsSend", 16: "reverbEffectsSend", 21: "delayModLFO", 22: "freqModLFO",
	23: "delayVibLFO", 24: "freqVibLFO", 25: "delayModEnv", 26: "attackModEnv", 27: "holdModEnv", 28: "decayModEnv",
	29: "sustainModEnv", 30: "releaseModEnv", 31: "keynumToModEnvHold", 32: "keynumToModEnvDecay",
	39: "keynumToVolEnvHold", 40: "keynumToVolEnvDecay", 46: "keynum", 47: "velocity",
}

// Sample types.
const (
	monoSample  = 1
	rightSample = 2
	leftSample  = 4
	romSample   = 0x8000
)

// File is a parsed SoundFont.
type File struct {
	Name string
	// Path is where it was read from.
	Path string

	presets []presetHdr
	pbags   []bag
	pgens   []gen
	insts   []instHdr
	ibags   []bag
	igens   []gen
	samples []sampleHdr
	pmods   int
	imods   int
	data    []int16
	data24  []byte
}

// Preset is one playable patch of the bank.
type Preset struct {
	Name          string
	Bank, Program int
	Index         int
}

type presetHdr struct {
	name         string
	preset, bank uint16
	bagIndex     uint16
}

type bag struct{ gen, mod uint16 }

type gen struct {
	oper   uint16
	amount uint16
}

func (g gen) signed() int { return int(int16(g.amount)) }

func (g gen) lo() int { return int(g.amount & 0xff) }
func (g gen) hi() int { return int(g.amount >> 8) }

type instHdr struct {
	name     string
	bagIndex uint16
}

type sampleHdr struct {
	name                           string
	start, end, startLoop, endLoop uint32
	rate                           uint32
	pitch                          uint8
	correction                     int8
	link, kind                     uint16
}

// Open reads a SoundFont from disk.
func Open(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	f.Path = path
	if f.Name == "" {
		f.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return f, nil
}

// Parse reads a SoundFont's bytes.
func Parse(b []byte) (*File, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "sfbk" {
		return nil, fmt.Errorf("not a SoundFont (no RIFF sfbk header)")
	}
	f := &File{}
	body := b[12:]
	if n := int(binary.LittleEndian.Uint32(b[4:])) - 4; n >= 0 && n < len(body) {
		body = body[:n]
	}
	err := chunks(body, func(id string, data []byte) error {
		if id != "LIST" || len(data) < 4 {
			return nil
		}
		kind, inner := string(data[:4]), data[4:]
		return chunks(inner, func(id string, d []byte) error { return f.chunk(kind, id, d) })
	})
	if err != nil {
		return nil, err
	}
	if len(f.presets) < 2 || len(f.insts) < 2 || len(f.samples) < 2 {
		return nil, fmt.Errorf("no presets, instruments or samples (the pdta list is missing or empty)")
	}
	if len(f.data) == 0 {
		return nil, fmt.Errorf("no sample data (the sdta list has no smpl chunk)")
	}
	return f, nil
}

// chunks walks RIFF chunks: a four-byte id, a little-endian size, the
// data, padded to an even length.
func chunks(b []byte, f func(id string, data []byte) error) error {
	for len(b) >= 8 {
		id, n := string(b[0:4]), int(binary.LittleEndian.Uint32(b[4:8]))
		b = b[8:]
		if n < 0 || n > len(b) {
			return fmt.Errorf("chunk %q runs past the end of the file", id)
		}
		if err := f(id, b[:n]); err != nil {
			return err
		}
		b = b[min(n+n&1, len(b)):]
	}
	return nil
}

// cstr is a fixed-length, zero-padded name.
func cstr(b []byte) string {
	if k := bytes.IndexByte(b, 0); k >= 0 {
		b = b[:k]
	}
	return strings.TrimSpace(string(b))
}

func (f *File) chunk(list, id string, d []byte) error {
	le := binary.LittleEndian
	records := func(size int) (int, error) {
		if len(d)%size != 0 {
			return 0, fmt.Errorf("%s chunk is %d bytes, not a multiple of %d", id, len(d), size)
		}
		return len(d) / size, nil
	}
	switch list + "/" + id {
	case "INFO/INAM":
		f.Name = cstr(d)
	case "sdta/smpl":
		f.data = make([]int16, len(d)/2)
		for i := range f.data {
			f.data[i] = int16(le.Uint16(d[2*i:]))
		}
	case "sdta/sm24":
		f.data24 = d
	case "pdta/phdr":
		n, err := records(38)
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			r := d[i*38:]
			f.presets = append(f.presets, presetHdr{name: cstr(r[:20]), preset: le.Uint16(r[20:]), bank: le.Uint16(r[22:]), bagIndex: le.Uint16(r[24:])})
		}
	case "pdta/pbag", "pdta/ibag":
		n, err := records(4)
		if err != nil {
			return err
		}
		bags := make([]bag, n)
		for i := range bags {
			bags[i] = bag{le.Uint16(d[i*4:]), le.Uint16(d[i*4+2:])}
		}
		if id == "pbag" {
			f.pbags = bags
		} else {
			f.ibags = bags
		}
	case "pdta/pmod", "pdta/imod":
		n, err := records(10)
		if err != nil {
			return err
		}
		if id == "pmod" {
			f.pmods = max(n-1, 0)
		} else {
			f.imods = max(n-1, 0)
		}
	case "pdta/pgen", "pdta/igen":
		n, err := records(4)
		if err != nil {
			return err
		}
		gens := make([]gen, n)
		for i := range gens {
			gens[i] = gen{le.Uint16(d[i*4:]), le.Uint16(d[i*4+2:])}
		}
		if id == "pgen" {
			f.pgens = gens
		} else {
			f.igens = gens
		}
	case "pdta/inst":
		n, err := records(22)
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			r := d[i*22:]
			f.insts = append(f.insts, instHdr{name: cstr(r[:20]), bagIndex: le.Uint16(r[20:])})
		}
	case "pdta/shdr":
		n, err := records(46)
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			r := d[i*46:]
			f.samples = append(f.samples, sampleHdr{
				name: cstr(r[:20]), start: le.Uint32(r[20:]), end: le.Uint32(r[24:]),
				startLoop: le.Uint32(r[28:]), endLoop: le.Uint32(r[32:]), rate: le.Uint32(r[36:]),
				pitch: r[40], correction: int8(r[41]), link: le.Uint16(r[42:]), kind: le.Uint16(r[44:]),
			})
		}
	}
	return nil
}

// Presets lists the bank's presets (the last header is the terminal EOP
// record and is not one), in bank and program order.
func (f *File) Presets() []Preset {
	var out []Preset
	for i := 0; i+1 < len(f.presets); i++ {
		p := f.presets[i]
		out = append(out, Preset{Name: p.name, Bank: int(p.bank), Program: int(p.preset), Index: i})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Bank != out[b].Bank {
			return out[a].Bank < out[b].Bank
		}
		return out[a].Program < out[b].Program
	})
	return out
}

// zone is one bag's generators, by number.
type zone map[uint16]gen

// zones reads a header's bags (from its bag index to the next header's)
// as zones; the first is the global zone when it does not end in its
// terminal generator (instrument or sampleID).
func zonesOf(bags []bag, gens []gen, from, to int, terminal uint16) (global zone, zs []zone, err error) {
	if from > to || to >= len(bags) {
		return nil, nil, fmt.Errorf("bag indices %d..%d outside %d bags", from, to, len(bags))
	}
	for i := from; i < to; i++ {
		a, b := int(bags[i].gen), int(bags[i+1].gen)
		if a > b || b > len(gens) {
			return nil, nil, fmt.Errorf("generator indices %d..%d outside %d generators", a, b, len(gens))
		}
		z := zone{}
		last := uint16(0xffff)
		for _, g := range gens[a:b] {
			z[g.oper] = g
			last = g.oper
		}
		if last != terminal {
			if i == from && global == nil {
				global = z
			}
			// A zone with no terminal past the first is ignored, as the
			// specification says.
			continue
		}
		zs = append(zs, z)
	}
	return global, zs, nil
}

// value is a generator's value in a zone, falling back to the global
// zone, then the default.
func value(z, global zone, oper uint16, def int) int {
	if g, ok := z[oper]; ok {
		return g.signed()
	}
	if g, ok := global[oper]; ok {
		return g.signed()
	}
	return def
}

// offset is a preset-level generator's addition (0 when unset).
func offset(z, global zone, oper uint16) int { return value(z, global, oper, 0) }

// rangeOf is a key or velocity range, intersected with the preset's.
func rangeOf(z, global zone, oper uint16) (lo, hi int) {
	lo, hi = 0, 127
	if g, ok := global[oper]; ok {
		lo, hi = g.lo(), g.hi()
	}
	if g, ok := z[oper]; ok {
		lo, hi = g.lo(), g.hi()
	}
	return lo, hi
}

// seconds is timecents as seconds; the default -12000 is about 1 ms.
func seconds(tc int) float64 {
	if tc <= -12000 {
		return 0
	}
	return math.Exp2(float64(tc) / 1200)
}

// emuAttenuation is how much of a zone's initialAttenuation is heard: the
// EMU hardware SoundFonts were made on (and FluidSynth after it) apply
// 0.4 of it, so banks are voiced for that, and the full figure plays most
// of them 5–15 dB too quiet.
const emuAttenuation = 0.4

// modEnvMean is the modulation envelope's mean level (0..1 of its peak)
// over a note's first second — attack and hold taken as instant, then
// the decay's straight fall to the sustain level (sustain in 0.1 % of
// fall, as the generator has it). A .finst's filter does not move, so a
// filter the envelope opens is set where it sits while a note is heard.
func modEnvMean(decayTC, sustain int) float64 {
	sus := 1 - math.Min(math.Max(float64(sustain), 0), 1000)/1000
	td := seconds(decayTC)
	if td >= 1 {
		return 1 - (1-sus)*0.5/td
	}
	return td*(1+sus)/2 + (1-td)*sus
}

// Regions reads one preset (an index into the preset headers) as SFZ
// regions, with what was ignored.
func (f *File) Regions(index int) ([]sfz.Region, map[string]int, error) {
	if index < 0 || index+1 >= len(f.presets) {
		return nil, nil, fmt.Errorf("no preset %d", index)
	}
	ignored := map[string]int{}
	pglobal, pzones, err := zonesOf(f.pbags, f.pgens, int(f.presets[index].bagIndex), int(f.presets[index+1].bagIndex), genInstrument)
	if err != nil {
		return nil, nil, fmt.Errorf("preset %s: %w", f.presets[index].name, err)
	}
	// The modulators this preset's zones and its instruments' carry (the
	// bag's mod index counts them, as its gen index counts generators).
	mods := func(bags []bag, from, to, total int) int {
		if from < 0 || to >= len(bags) || from > to {
			return 0
		}
		return min(max(int(bags[to].mod)-int(bags[from].mod), 0), total)
	}
	nmods := mods(f.pbags, int(f.presets[index].bagIndex), int(f.presets[index+1].bagIndex), f.pmods)
	seenInst := map[int]bool{}
	var out []sfz.Region
	audio := map[[2]int]*sfz.Audio{}
	for _, pz := range pzones {
		ii := pz[genInstrument].signed()
		if ii < 0 || ii+1 >= len(f.insts) {
			return nil, nil, fmt.Errorf("preset %s names instrument %d of %d", f.presets[index].name, ii, len(f.insts)-1)
		}
		iglobal, izones, err := zonesOf(f.ibags, f.igens, int(f.insts[ii].bagIndex), int(f.insts[ii+1].bagIndex), genSampleID)
		if err != nil {
			return nil, nil, fmt.Errorf("instrument %s: %w", f.insts[ii].name, err)
		}
		if !seenInst[ii] {
			seenInst[ii] = true
			nmods += mods(f.ibags, int(f.insts[ii].bagIndex), int(f.insts[ii+1].bagIndex), f.imods)
		}
		pklo, pkhi := rangeOf(pz, pglobal, genKeyRange)
		pvlo, pvhi := rangeOf(pz, pglobal, genVelRange)
		var rs []sfz.Region
		var links []int
		for _, iz := range izones {
			for _, z := range []zone{iz, iglobal} {
				for oper := range z {
					if n, ok := unused[oper]; ok {
						ignored[n]++
					}
				}
			}
			si := iz[genSampleID].signed()
			if si < 0 || si+1 >= len(f.samples) {
				return nil, nil, fmt.Errorf("instrument %s names sample %d of %d", f.insts[ii].name, si, len(f.samples)-1)
			}
			sh := f.samples[si]
			if sh.kind&romSample != 0 {
				ignored["ROM samples"]++
				continue
			}
			klo, khi := rangeOf(iz, iglobal, genKeyRange)
			vlo, vhi := rangeOf(iz, iglobal, genVelRange)
			klo, khi = max(klo, pklo), min(khi, pkhi)
			vlo, vhi = max(vlo, pvlo), min(vhi, pvhi)
			if klo > khi || vlo > vhi {
				continue
			}
			add := func(oper uint16, def int) int {
				return value(iz, iglobal, oper, def) + offset(pz, pglobal, oper)
			}
			r := sfz.Region{
				LoKey: klo, HiKey: khi, LoVel: vlo, HiVel: vhi,
				SeqLength: 1, SeqPosition: 1, HiRand: 1, SwLast: -1, Trigger: "attack",
				Where: fmt.Sprintf("%s / %s / %s", f.presets[index].name, f.insts[ii].name, sh.name),
			}
			// Tuning: the root key, coarse and fine tune, the sample's own
			// correction.
			r.Key = value(iz, iglobal, genRootKey, -1)
			if r.Key < 0 {
				r.Key = int(sh.pitch)
				if r.Key > 127 {
					r.Key = 60
				}
			}
			r.Transpose = add(genCoarseTune, 0)
			r.Tune = float64(add(genFineTune, 0) + int(sh.correction))
			if st := add(genScaleTuning, 100); st != 100 {
				ignored["scaleTuning"]++
			}
			// Level and pan.
			r.Volume = -float64(add(genAttenuation, 0)) / 10 * emuAttenuation
			r.Pan = float64(add(genPan, 0)) / 5
			// The volume envelope.
			r.Env = sfz.Envelope{
				Delay: seconds(add(genDelayVolEnv, -12000)), Attack: seconds(add(genAttackVolEnv, -12000)),
				Hold: seconds(add(genHoldVolEnv, -12000)), Decay: seconds(add(genDecayVolEnv, -12000)),
				Sustain: math.Pow(10, -math.Max(float64(add(genSustainVolEnv, 0)), 0)/200),
				Release: seconds(add(genReleaseVolEnv, -12000)),
			}
			fc := add(genFilterFc, 13500)
			if amt := add(genModEnvToFilterFc, 0); amt != 0 {
				fc += int(math.Round(float64(amt) * modEnvMean(add(genDecayModEnv, -12000), add(genSustainModEnv, 0))))
			}
			if fc < 13500 {
				r.Filter, r.Cutoff = "lpf_2p", 8.176*math.Exp2(float64(fc)/1200)
				r.Resonance = float64(add(genFilterQ, 0)) / 10
			}
			if _, ok := iz[genExclusiveClass]; ok {
				ignored["exclusiveClass"]++
			}
			// The stretch of sample data and the loop, as frames from the
			// sample's start.
			start := int64(sh.start) + int64(value(iz, iglobal, genStartOffset, 0)) + 32768*int64(value(iz, iglobal, genStartCoarseOffset, 0))
			end := int64(sh.end) + int64(value(iz, iglobal, genEndOffset, 0)) + 32768*int64(value(iz, iglobal, genEndCoarseOffset, 0))
			ls := int64(sh.startLoop) + int64(value(iz, iglobal, genStartLoopOffset, 0)) + 32768*int64(value(iz, iglobal, genStartLoopCoarse, 0))
			le := int64(sh.endLoop) + int64(value(iz, iglobal, genEndLoopOffset, 0)) + 32768*int64(value(iz, iglobal, genEndLoopCoarse, 0))
			a := f.audio(si, audio)
			if a == nil {
				ignored["empty samples"]++
				continue
			}
			base := int64(sh.start)
			r.Audio = a
			r.Offset, r.End = start-base, end-base-1
			r.LoopStart, r.LoopEnd = ls-base, le-base-1
			switch value(iz, iglobal, genSampleModes, 0) & 3 {
			case 1:
				r.LoopMode = sfz.LoopContinuous
			case 3:
				r.LoopMode = sfz.LoopSustain
			default:
				r.LoopMode = sfz.LoopNone
			}
			rs = append(rs, r)
			links = append(links, si)
		}
		out = append(out, f.stereo(rs, links, audio)...)
	}
	if nmods > 0 {
		ignored["modulators"] = nmods
	}
	return out, ignored, nil
}

// audio is sample si's data as mono audio, decoded once.
func (f *File) audio(si int, cache map[[2]int]*sfz.Audio) *sfz.Audio {
	k := [2]int{si, -1}
	if a, ok := cache[k]; ok {
		return a
	}
	sh := f.samples[si]
	a, b := int(sh.start), int(sh.end)
	if a < 0 || b > len(f.data) || b <= a {
		cache[k] = nil
		return nil
	}
	d := make([]float32, b-a)
	for i := range d {
		v := int32(f.data[a+i]) << 8
		if len(f.data24) >= b {
			v |= int32(f.data24[a+i])
		}
		d[i] = float32(v) / 8388608
	}
	out := &sfz.Audio{Name: sh.name, Data: d, Channels: 1, Rate: int(sh.rate), Root: int(sh.pitch)}
	if sh.rate == 0 {
		out.Rate = 44100
	}
	cache[k] = out
	return out
}

// stereo joins left and right zones of a linked sample pair over the same
// keys and velocities into one stereo region.
func (f *File) stereo(rs []sfz.Region, samples []int, cache map[[2]int]*sfz.Audio) []sfz.Region {
	used := make([]bool, len(rs))
	var out []sfz.Region
	for i := range rs {
		if used[i] {
			continue
		}
		sh := f.samples[samples[i]]
		if sh.kind&(leftSample|rightSample) != 0 {
			for j := range rs {
				if j == i || used[j] || samples[j] != int(sh.link) {
					continue
				}
				a, b := rs[i], rs[j]
				if a.LoKey != b.LoKey || a.HiKey != b.HiKey || a.LoVel != b.LoVel || a.HiVel != b.HiVel {
					continue
				}
				l, r := i, j
				if sh.kind&rightSample != 0 {
					l, r = j, i
				}
				st := rs[l]
				st.Audio = f.pair(samples[l], samples[r], cache)
				st.Pan = 0
				used[j] = true
				rs[i] = st
				break
			}
		}
		used[i] = true
		out = append(out, rs[i])
	}
	return out
}

// pair is a left and a right sample as one stereo audio.
func (f *File) pair(l, r int, cache map[[2]int]*sfz.Audio) *sfz.Audio {
	k := [2]int{l, r}
	if a, ok := cache[k]; ok {
		return a
	}
	al, ar := f.audio(l, cache), f.audio(r, cache)
	n := min(len(al.Data), len(ar.Data))
	d := make([]float32, 2*n)
	for i := 0; i < n; i++ {
		d[2*i], d[2*i+1] = al.Data[i], ar.Data[i]
	}
	name := strings.TrimSuffix(strings.TrimSuffix(al.Name, "L"), "(L)")
	out := &sfz.Audio{Name: strings.TrimRight(name, " _-"), Data: d, Channels: 2, Rate: al.Rate, Root: al.Root}
	cache[k] = out
	return out
}

// Options say what an import is called and where it goes.
type Options struct {
	// Name is the library name: the instrument's for a bank of one
	// preset, the folder of instruments for a bank of several. "" takes
	// "imported/" + the file's name.
	Name string
	// Presets are the presets to import (Preset.Index); nil is all.
	Presets []int
	// SoundsDir is the sounds folder written into; "" is the project's.
	SoundsDir string
}

// Import reads a SoundFont and writes the presets asked for into the
// library, one .finst each.
func Import(path string, o Options) ([]*sfz.Built, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	return f.Import(o)
}

// Import writes the presets asked for into the library.
func (f *File) Import(o Options) ([]*sfz.Built, error) {
	presets := f.Presets()
	if o.Presets != nil {
		var pick []Preset
		for _, p := range presets {
			for _, k := range o.Presets {
				if p.Index == k {
					pick = append(pick, p)
				}
			}
		}
		presets = pick
	}
	if len(presets) == 0 {
		return nil, fmt.Errorf("no presets to import")
	}
	base := strings.Trim(filepath.ToSlash(o.Name), "/")
	if base == "" {
		base = sfz.ImportedCategory + "/" + sfz.Slug(strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path)))
		if f.Path == "" {
			base = sfz.ImportedCategory + "/" + sfz.Slug(f.Name)
		}
	}
	taken := map[string]bool{}
	var out []*sfz.Built
	// A bank of one preset is one instrument named for the file; a bank of
	// several is a folder of them, however many are imported now, so a
	// preset imported later does not land on another.
	several := len(f.Presets()) > 1
	for _, p := range presets {
		name := base
		if several {
			slug := sfz.Slug(p.Name)
			if slug == "" {
				slug = fmt.Sprintf("preset_%d", p.Program)
			}
			name = base + "/" + slug
			if taken[name] {
				name = fmt.Sprintf("%s_%d_%d", name, p.Bank, p.Program)
			}
		}
		taken[name] = true
		rs, ignored, err := f.Regions(p.Index)
		if err != nil {
			return out, err
		}
		src := filepath.Base(f.Path)
		if src == "." || src == "" {
			src = f.Name
		}
		b, err := sfz.Build(&sfz.File{Regions: rs, SwLoKey: -1, SwHiKey: -1, SwDefault: -1, Ignored: ignored},
			sfz.Options{Name: name, Title: p.Name, Description: fmt.Sprintf("Imported from %s, preset %d:%d %s", src, p.Bank, p.Program, p.Name)})
		if err != nil {
			return out, fmt.Errorf("preset %s: %w", p.Name, err)
		}
		if err := b.Write(o.SoundsDir); err != nil {
			return out, err
		}
		out = append(out, b)
	}
	return out, nil
}
