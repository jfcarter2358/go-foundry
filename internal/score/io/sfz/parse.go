package sfz

// parse.go — an .sfz file as regions.
//
// An SFZ file is plain text: headers in angle brackets, each followed by
// opcodes written name=value. A <region> maps one sample; the headers above
// it — <global>, <master>, <group> — hold opcodes every region under them
// shares, each level able to override the one above, and <control> sets
// what the file as a whole means (default_path, octave_offset). #define
// names a value and #include pulls in another file:
//
//	<control> default_path=samples/
//	<global> ampeg_release=0.4
//	<group> lovel=0 hivel=63 seq_length=2
//	<region> sample=vln_p_g3_rr1.wav seq_position=1 lokey=55 hikey=60 pitch_keycenter=55
//	<region> sample=vln_p_g3_rr2.wav seq_position=2 lokey=55 hikey=60 pitch_keycenter=55
//
// Parse folds the headers into each region so a Region carries everything
// that applies to it. Opcodes the importer does not use are counted, not
// lost silently: the result says which were ignored and how often.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// LoopMode is how a region's sample repeats.
type LoopMode uint8

const (
	// LoopUnset takes the sample file's own loop (a WAV's smpl chunk),
	// else no loop.
	LoopUnset LoopMode = iota
	LoopNone
	// LoopOneShot plays the whole sample whatever the note's length.
	LoopOneShot
	// LoopContinuous loops for as long as the note sounds.
	LoopContinuous
	// LoopSustain loops while the key is held, then plays on to the end.
	LoopSustain
)

// Envelope is a region's amplitude envelope (ampeg_*): times in seconds,
// Sustain a level 0..1.
type Envelope struct {
	Delay, Attack, Hold, Decay, Sustain, Release float64
}

// DefaultEnvelope is SFZ's: an instant attack to full level, held, a 1 ms
// release.
func DefaultEnvelope() Envelope { return Envelope{Sustain: 1, Release: 0.001} }

// Shaped reports an envelope that does more than switch on and off: a
// delay, an attack, a hold or a decay to below full level.
func (e Envelope) Shaped() bool {
	return e.Delay > 0 || e.Attack > 0.002 || (e.Decay > 0 && e.Sustain < 0.999) || (e.Hold > 0 && e.Sustain < 0.999)
}

// Audio is decoded sample data carried by a region itself: an SF2 sample.
// Root is the note it was recorded at (MIDI, -1 unknown), Tune the cents
// to add to play it in tune, Loop its own loop (frames, End exclusive;
// End 0 for none).
type Audio struct {
	Name               string
	Data               []float32
	Channels, Rate     int
	Root               int
	Tune               float64
	LoopStart, LoopEnd int64
}

// Frames is the audio's length.
func (a *Audio) Frames() int64 {
	if a == nil || a.Channels == 0 {
		return 0
	}
	return int64(len(a.Data) / a.Channels)
}

// Region is one sample mapped over keys and velocities, with everything
// that shapes how it plays: an SFZ <region> with its headers folded in, or
// an SF2 zone (internal/score/io/sf2).
type Region struct {
	// Sample is the audio file, resolved to a path; Audio is the audio
	// itself when the region carries it (SF2). One of the two is set.
	Sample string
	Audio  *Audio

	// LoKey..HiKey and LoVel..HiVel are the MIDI keys and velocities the
	// region answers (inclusive). Key is the note at which the sample
	// plays at its own pitch; -1 takes the sample's root (60 when it has
	// none).
	LoKey, HiKey int
	LoVel, HiVel int
	Key          int

	// Transpose (semitones) and Tune (cents) move the pitch; Volume is a
	// gain in dB, Pan -100..100.
	Transpose int
	Tune      float64
	Volume    float64
	Pan       float64

	// Offset is the first frame played; End the last (inclusive), -1 the
	// sample's end.
	Offset, End int64
	// LoopMode, LoopStart and LoopEnd (frames, End inclusive; -1 unset:
	// the file's own) are the loop.
	LoopMode           LoopMode
	LoopStart, LoopEnd int64

	Env Envelope

	// SeqLength and SeqPosition are round robin: the region plays on
	// every SeqLength-th note, at its SeqPosition (1-based). LoRand and
	// HiRand are random round robin (0..1).
	SeqLength, SeqPosition int
	LoRand, HiRand         float64

	// SwLast is the keyswitch (MIDI key) that picks the region, -1 for
	// none: it plays after that key was pressed. SwLabel names it.
	SwLast  int
	SwLabel string

	// Trigger is "attack" (the default), "release", "first" or "legato".
	Trigger string

	// Filter is fil_type ("" none), Cutoff in Hz, Resonance in dB.
	Filter    string
	Cutoff    float64
	Resonance float64

	// Where is file:line, for messages.
	Where string
}

// newRegion is a region with SFZ's defaults.
func newRegion() Region {
	return Region{LoKey: 0, HiKey: 127, LoVel: 0, HiVel: 127, Key: 60, End: -1,
		LoopStart: -1, LoopEnd: -1, Env: DefaultEnvelope(), SeqLength: 1, SeqPosition: 1,
		LoRand: 0, HiRand: 1, SwLast: -1, Trigger: "attack"}
}

// File is a parsed .sfz: its regions in order, the keyswitch settings
// <control> and <global> make, and what was ignored.
type File struct {
	Regions []Region
	// SwLoKey..SwHiKey is the keyswitch range (-1 none); SwDefault the
	// keyswitch in force before any is pressed (-1 none).
	SwLoKey, SwHiKey int
	SwDefault        int
	// Ignored counts each opcode the importer does not use, by name.
	Ignored map[string]int
	// Warnings are problems that did not stop the parse.
	Warnings []string
}

// opcode is one name=value with where it was written.
type opcode struct {
	name, value string
	where       string
}

// header is one <name> and the opcodes after it.
type header struct {
	name  string
	ops   []opcode
	where string
}

// ParseFile reads an .sfz from disk, its #includes and sample paths
// resolved against its folder.
func ParseFile(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(b), path)
}

// Parse reads SFZ text. path is the file it came from: #include and sample
// paths are resolved against its folder.
func Parse(text, path string) (*File, error) {
	p := &parser{root: filepath.Dir(path), defines: map[string]string{}, file: &File{SwLoKey: -1, SwHiKey: -1, SwDefault: -1, Ignored: map[string]int{}}}
	hs, err := p.headers(text, path, 0)
	if err != nil {
		return nil, err
	}
	if err := p.fold(hs); err != nil {
		return nil, err
	}
	return p.file, nil
}

type parser struct {
	root    string
	defines map[string]string
	file    *File
	// noteOffset and octaveOffset are <control>'s note_offset and
	// octave_offset, added to every key.
	noteOffset, octaveOffset int
	defaultPath              string
	// bad is each skipped opcode already warned of.
	bad map[string]bool
}

// maxIncludeDepth stops an #include that includes itself.
const maxIncludeDepth = 16

// stringOpcodes are the opcodes whose values may hold spaces: they run to
// the next opcode or header.
func stringOpcode(name string) bool {
	switch name {
	case "sample", "default_path", "sw_label", "region_label", "group_label", "master_label", "global_label", "image":
		return true
	}
	return strings.HasPrefix(name, "label_cc")
}

// headers splits text into headers and their opcodes, expanding #define
// and #include.
func (p *parser) headers(text, path string, depth int) ([]header, error) {
	if depth > maxIncludeDepth {
		return nil, fmt.Errorf("%s: #include nested more than %d deep", path, maxIncludeDepth)
	}
	text = stripComments(text)
	var out []header
	cur := -1
	name := filepath.Base(path)
	for ln, line := range strings.Split(text, "\n") {
		where := fmt.Sprintf("%s:%d", name, ln+1)
		line = strings.TrimRight(line, "\r")
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "#define"):
			f := strings.Fields(trim)
			if len(f) < 3 || !strings.HasPrefix(f[1], "$") {
				return nil, fmt.Errorf("%s: #define wants $name value", where)
			}
			p.defines[f[1]] = strings.Join(f[2:], " ")
			continue
		}
		line = p.expand(line)
		for len(strings.TrimSpace(line)) > 0 {
			line = strings.TrimLeft(line, " \t")
			if strings.HasPrefix(line, "#include") {
				// #include "file", anywhere on a line.
				rest := strings.TrimLeft(line[len("#include"):], " \t")
				if !strings.HasPrefix(rest, `"`) {
					return nil, fmt.Errorf("%s: #include wants a quoted file name", where)
				}
				k := strings.IndexByte(rest[1:], '"')
				if k < 0 {
					return nil, fmt.Errorf("%s: #include's file name has no closing quote", where)
				}
				arg := rest[1 : k+1]
				line = rest[k+2:]
				inc := filepath.Join(filepath.Dir(path), filepath.FromSlash(arg))
				if _, err := os.Stat(inc); err != nil {
					inc = filepath.Join(p.root, filepath.FromSlash(arg))
				}
				b, err := os.ReadFile(inc)
				if err != nil {
					return nil, fmt.Errorf("%s: #include %q: %w", where, arg, err)
				}
				sub, err := p.headers(string(b), inc, depth+1)
				if err != nil {
					return nil, err
				}
				// An included file may carry on the header it was included under.
				for i, h := range sub {
					if h.name == "" {
						if cur >= 0 {
							out[cur].ops = append(out[cur].ops, h.ops...)
						}
						continue
					}
					out = append(out, sub[i])
					cur = len(out) - 1
				}
				continue
			}
			if strings.HasPrefix(line, "<") {
				k := strings.IndexByte(line, '>')
				if k < 0 {
					return nil, fmt.Errorf("%s: header %q has no '>'", where, line)
				}
				out = append(out, header{name: strings.ToLower(strings.TrimSpace(line[1:k])), where: where})
				cur = len(out) - 1
				line = line[k+1:]
				continue
			}
			op, rest, err := nextOpcode(line, where)
			if err != nil {
				return nil, err
			}
			line = rest
			if cur < 0 {
				// Opcodes before any header (an included fragment's).
				out = append(out, header{where: where})
				cur = len(out) - 1
			}
			out[cur].ops = append(out[cur].ops, op)
		}
	}
	return out, nil
}

// nextOpcode reads one name=value from the start of line.
func nextOpcode(line, where string) (opcode, string, error) {
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return opcode{}, "", fmt.Errorf("%s: %q is not an opcode (want name=value)", where, strings.TrimSpace(line))
	}
	name := strings.TrimSpace(line[:eq])
	if strings.ContainsAny(name, " \t<>") {
		return opcode{}, "", fmt.Errorf("%s: %q is not an opcode name", where, name)
	}
	rest := line[eq+1:]
	var value string
	if stringOpcode(name) {
		// To the next opcode (" word=") or header, whichever is first.
		end := len(rest)
		if k := strings.IndexByte(rest, '<'); k >= 0 {
			end = k
		}
		for i := 0; i < end; i++ {
			if rest[i] != ' ' && rest[i] != '\t' {
				continue
			}
			j := i + 1
			for j < end && (rest[j] == ' ' || rest[j] == '\t') {
				j++
			}
			k := j
			for k < end && isNameByte(rest[k]) {
				k++
			}
			if k > j && k < end && rest[k] == '=' {
				end = i
				break
			}
		}
		value, rest = strings.TrimSpace(rest[:end]), rest[end:]
	} else {
		rest = strings.TrimLeft(rest, " \t")
		end := strings.IndexAny(rest, " \t<")
		if end < 0 {
			end = len(rest)
		}
		value, rest = rest[:end], rest[end:]
	}
	return opcode{name: name, value: value, where: where}, rest, nil
}

func isNameByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// expand replaces #define'd names, longest first so $A does not eat $AB.
func (p *parser) expand(s string) string {
	if len(p.defines) == 0 || !strings.Contains(s, "$") {
		return s
	}
	names := make([]string, 0, len(p.defines))
	for n := range p.defines {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, n := range names {
		s = strings.ReplaceAll(s, n, p.defines[n])
	}
	return s
}

// stripComments drops // line comments and /* block */ comments, keeping
// the line count.
func stripComments(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				b.WriteByte('\n')
			}
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i < len(s) && !(s[i] == '*' && i+1 < len(s) && s[i+1] == '/') {
				if s[i] == '\n' {
					b.WriteByte('\n')
				}
				i++
			}
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// fold walks the headers keeping the <global>, <master> and <group>
// opcodes in force, and makes each <region> from them and its own.
func (p *parser) fold(hs []header) error {
	var global, master, group []opcode
	for _, h := range hs {
		switch h.name {
		case "control":
			for _, op := range h.ops {
				if err := p.control(op); err != nil {
					p.badOpcode(err)
				}
			}
		case "global":
			global, master, group = h.ops, nil, nil
		case "master":
			master, group = h.ops, nil
		case "group":
			group = h.ops
		case "region":
			r := newRegion()
			r.Where = h.where
			for _, layer := range [][]opcode{global, master, group, h.ops} {
				for _, op := range layer {
					// A value the opcode cannot take is skipped, as a
					// player skips it: one typo in a big library should
					// not refuse the whole instrument.
					keep, sw := r, [3]int{p.file.SwLoKey, p.file.SwHiKey, p.file.SwDefault}
					if err := p.apply(&r, op); err != nil {
						r = keep
						p.file.SwLoKey, p.file.SwHiKey, p.file.SwDefault = sw[0], sw[1], sw[2]
						p.badOpcode(err)
					}
				}
			}
			if r.Sample == "" && r.Audio == nil {
				p.warn("%s: a region with no sample is skipped", h.where)
				continue
			}
			p.file.Regions = append(p.file.Regions, r)
		case "curve", "effect", "midi", "sample":
			// Curves, effects, MIDI settings and embedded samples: nothing
			// a .finst holds.
			if len(h.ops) > 0 {
				p.file.Ignored["<"+h.name+">"]++
			}
		case "":
		default:
			p.warn("%s: unknown header <%s> skipped", h.where, h.name)
		}
	}
	return nil
}

func (p *parser) warn(format string, a ...any) {
	p.file.Warnings = append(p.file.Warnings, fmt.Sprintf(format, a...))
}

// badOpcode notes an opcode skipped for its value — once per place, since
// a group's bad opcode is folded into each of its regions.
func (p *parser) badOpcode(err error) {
	msg := err.Error() + " (skipped)"
	if p.bad == nil {
		p.bad = map[string]bool{}
	}
	if !p.bad[msg] {
		p.bad[msg] = true
		p.warn("%s", msg)
	}
}

// control applies a <control> opcode.
func (p *parser) control(op opcode) error {
	switch op.name {
	case "default_path":
		p.defaultPath = filepath.FromSlash(strings.ReplaceAll(op.value, `\`, "/"))
	case "octave_offset":
		v, err := strconv.Atoi(op.value)
		if err != nil {
			return fmt.Errorf("%s: octave_offset %q", op.where, op.value)
		}
		p.octaveOffset = v
	case "note_offset":
		v, err := strconv.Atoi(op.value)
		if err != nil {
			return fmt.Errorf("%s: note_offset %q", op.where, op.value)
		}
		p.noteOffset = v
	default:
		// set_ccN, label_ccN, hint_*: nothing a .finst holds.
		p.file.Ignored[op.name]++
	}
	return nil
}

// apply sets one opcode on a region (or, for the keyswitch range and
// default, on the file).
func (p *parser) apply(r *Region, op opcode) error {
	num := func() (float64, error) {
		v, err := strconv.ParseFloat(op.value, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e12 {
			return 0, fmt.Errorf("%s: %s=%q is not a number", op.where, op.name, op.value)
		}
		return v, nil
	}
	integer := func() (int64, error) {
		v, err := num()
		return int64(v), err
	}
	key := func() (int, error) {
		k, err := p.key(op.value)
		if err != nil {
			return 0, fmt.Errorf("%s: %s: %w", op.where, op.name, err)
		}
		return k, nil
	}
	var err error
	var v float64
	var k int
	var n int64
	switch op.name {
	case "sample":
		path := strings.ReplaceAll(op.value, `\`, "/")
		r.Sample = filepath.Join(p.root, p.defaultPath, filepath.FromSlash(path))
	case "lokey":
		r.LoKey, err = key()
	case "hikey":
		r.HiKey, err = key()
	case "key":
		if k, err = key(); err == nil {
			r.LoKey, r.HiKey, r.Key = k, k, k
		}
	case "pitch_keycenter":
		if strings.EqualFold(op.value, "sample") {
			r.Key = -1
			break
		}
		r.Key, err = key()
	case "lovel":
		n, err = integer()
		r.LoVel = int(n)
	case "hivel":
		n, err = integer()
		r.HiVel = int(n)
	case "transpose":
		n, err = integer()
		r.Transpose = int(n)
	case "tune", "pitch":
		r.Tune, err = num()
	case "volume", "gain":
		r.Volume, err = num()
	case "amplitude":
		// A percentage: as dB on top of volume.
		if v, err = num(); err == nil && v > 0 {
			r.Volume += 20 * log10(v/100)
		}
	case "pan":
		r.Pan, err = num()
	case "offset":
		r.Offset, err = integer()
	case "end":
		r.End, err = integer()
	case "loop_mode", "loopmode":
		switch op.value {
		case "no_loop":
			r.LoopMode = LoopNone
		case "one_shot":
			r.LoopMode = LoopOneShot
		case "loop_continuous":
			r.LoopMode = LoopContinuous
		case "loop_sustain":
			r.LoopMode = LoopSustain
		default:
			return fmt.Errorf("%s: loop_mode %q", op.where, op.value)
		}
	case "loop_start", "loopstart":
		r.LoopStart, err = integer()
	case "loop_end", "loopend":
		r.LoopEnd, err = integer()
	case "ampeg_delay":
		r.Env.Delay, err = num()
	case "ampeg_attack":
		r.Env.Attack, err = num()
	case "ampeg_hold":
		r.Env.Hold, err = num()
	case "ampeg_decay":
		r.Env.Decay, err = num()
	case "ampeg_sustain":
		if v, err = num(); err == nil {
			r.Env.Sustain = clamp(v/100, 0, 1)
		}
	case "ampeg_release":
		r.Env.Release, err = num()
	case "seq_length":
		n, err = integer()
		r.SeqLength = max(int(n), 1)
	case "seq_position":
		n, err = integer()
		r.SeqPosition = max(int(n), 1)
	case "lorand":
		r.LoRand, err = num()
	case "hirand":
		r.HiRand, err = num()
	case "sw_last", "sw_down":
		r.SwLast, err = key()
	case "sw_label":
		r.SwLabel = op.value
	case "sw_lokey":
		p.file.SwLoKey, err = key()
	case "sw_hikey":
		p.file.SwHiKey, err = key()
	case "sw_default":
		p.file.SwDefault, err = key()
	case "trigger":
		r.Trigger = op.value
	case "fil_type", "filtype":
		r.Filter = op.value
	case "cutoff":
		r.Cutoff, err = num()
	case "resonance":
		r.Resonance, err = num()
	case "amp_veltrack", "group", "off_by", "off_mode", "polyphony", "note_polyphony",
		"region_label", "group_label", "master_label", "global_label", "pitch_keytrack":
		// How notes choke and track: the engine has its own rules. Counted
		// only when they ask for something other than the default.
		if op.name == "pitch_keytrack" && op.value != "100" || op.name == "amp_veltrack" && op.value != "100" {
			p.file.Ignored[op.name]++
		}
	default:
		p.file.Ignored[op.name]++
	}
	return err
}

// noteSemis are the letters' semitones above C.
var noteSemis = map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}

// key reads a key as a MIDI number: a number (60) or a name (c4, c#4,
// db4; c4 is 60), with <control>'s offsets added.
func (p *parser) key(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, fmt.Errorf("no key")
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return -1, nil
		}
		return clampKey(n + p.noteOffset + 12*p.octaveOffset), nil
	}
	semi, ok := noteSemis[s[0]]
	if !ok {
		return 0, fmt.Errorf("bad key %q", s)
	}
	rest := s[1:]
	for len(rest) > 0 && (rest[0] == '#' || rest[0] == 'b') {
		if rest[0] == '#' {
			semi++
		} else {
			semi--
		}
		rest = rest[1:]
	}
	oct, err := strconv.Atoi(rest)
	if err != nil {
		return 0, fmt.Errorf("bad key %q", s)
	}
	return clampKey((oct+1)*12 + semi + p.noteOffset + 12*p.octaveOffset), nil
}

func clampKey(k int) int { return min(max(k, 0), 127) }

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }
