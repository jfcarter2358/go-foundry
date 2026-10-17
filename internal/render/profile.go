package render

// profile.go — where a frame's time goes, pass by pass, on both processors.
//
// The FPS number says a frame is slow and nothing about why. A pass is timed
// twice: on the CPU (how long the Go code that issues it took) and on the GPU
// (how long the GPU took to execute what it issued). The two answer different
// questions. A pass that is cheap on the GPU but slow on the CPU is spending its
// time in the engine or the driver — too many draw calls, too much per-object
// work — and a faster GPU would not help it at all; the reverse is fill rate or
// geometry, and no amount of CPU work will help that.
//
// GPU times come from timestamp queries (glQueryCounter). A result is not ready
// until the GPU has got that far, which is usually a frame or two after the
// CPU issued it, and asking for it sooner stalls the CPU until it is — which
// would slow down the very frame being measured. So each section keeps a ring
// of query pairs, one slot per frame in flight, and a slot is only read back
// when the ring comes round to it again.
//
// Sections may nest (a timestamp is a point, not an interval, so nesting costs
// nothing) and are identified by name; the overlay lists them in the order they
// were first seen, indented by depth. A section may run any number of times in
// a frame — once per shadow cube face, say — and reports the frame's total.
// Off, every call is a bool test.

import (
	"github.com/go-gl/gl/v4.3-core/gl"
	"log"
)

// ProfileEnabled turns the profiler on. The F3 overlay and the benchmark turn
// it on; nothing else pays for it.
var ProfileEnabled bool

// ProfileSlowMs, when above 0, logs every section whose CPU or GPU time in
// one frame passes it (the bench's slow=; docs/adventure/PHASE_F.md): which
// pass a hitch was, frame by frame, where the report's maxima only say that
// one happened.
var ProfileSlowMs float64

// profileRing is how many frames' queries are kept in flight. Drivers queue up
// to three frames, so four means a slot is always finished by the time it is
// reused.
const profileRing = 4

// profileSmoothing is the weight a new frame gets in the overlay's averages —
// a quarter-second or so of history at 60 fps, which is steady enough to read
// without hiding a change as it happens.
const profileSmoothing = 0.06

type profSection struct {
	name  string
	depth int

	cpuStart float64 // profNow, ms
	// cpuFrame is this frame's CPU total so far; it is folded into the
	// averages when the next frame begins.
	cpuFrame float64

	// queries holds a begin/end timestamp pair per run of the section, per
	// frame in flight; used is how many of a slot's pairs this frame issued.
	queries [profileRing][][2]uint32
	used    [profileRing]int
	// seen is the frame the section last ran in. A section that did not run
	// this frame (a pass that has nothing to do) still shows, at zero.
	seen uint64

	cpuMs, gpuMs float64 // smoothed

	// The benchmark's totals, since the last ProfileResetTotals.
	sumCPU, sumGPU float64
	maxCPU, maxGPU float64
	nCPU, nGPU     int
}

// profKey names a section by its parent as well as its own name, so the one
// shadow walk timed under both the sun and the point lights is two rows.
type profKey struct {
	parent int
	name   string
}

var prof struct {
	sections []*profSection
	byName   map[profKey]int
	stack    []int
	frame    uint64
	slot     int
}

// ProfileSample is one section's numbers, as the overlay and the benchmark
// read them.
type ProfileSample struct {
	Name  string
	Depth int
	// The overlay's smoothed milliseconds.
	CPUMs, GPUMs float64
	// The benchmark's averages and worst frames since ProfileResetTotals.
	AvgCPU, AvgGPU float64
	MaxCPU, MaxGPU float64
}

// ProfileFrameBegin starts a frame: it reads back the GPU times for the ring
// slot about to be reused, then hands that slot to this frame's sections.
// Called once, at the top of the frame, before any ProfileBegin.
func ProfileFrameBegin() {
	if !ProfileEnabled {
		return
	}
	// Close out the frame that just ended on the CPU side.
	for _, s := range prof.sections {
		if s.seen != prof.frame || prof.frame == 0 {
			continue
		}
		ms := s.cpuFrame
		s.cpuFrame = 0
		s.cpuMs += (ms - s.cpuMs) * profileSmoothing
		s.sumCPU += ms
		s.nCPU++
		if ProfileSlowMs > 0 && ms > ProfileSlowMs {
			log.Printf("profile: frame %d %q (depth %d) %.1f ms CPU", prof.frame, s.name, s.depth, ms)
		}
		if ms > s.maxCPU {
			s.maxCPU = ms
		}
	}

	prof.frame++
	prof.slot = int(prof.frame % profileRing)
	prof.stack = prof.stack[:0]
	for _, s := range prof.sections {
		n := s.used[prof.slot]
		if n == 0 {
			continue
		}
		s.used[prof.slot] = 0
		var avail int32
		gl.GetQueryObjectiv(s.queries[prof.slot][n-1][1], gl.QUERY_RESULT_AVAILABLE, &avail)
		if avail == 0 {
			// Four frames behind and still not done: drop this sample rather
			// than stall for it.
			continue
		}
		var ms float64
		for _, q := range s.queries[prof.slot][:n] {
			var t0, t1 uint64
			gl.GetQueryObjectui64v(q[0], gl.QUERY_RESULT, &t0)
			gl.GetQueryObjectui64v(q[1], gl.QUERY_RESULT, &t1)
			ms += float64(t1-t0) / 1e6
		}
		s.gpuMs += (ms - s.gpuMs) * profileSmoothing
		s.sumGPU += ms
		s.nGPU++
		if ProfileSlowMs > 0 && ms > ProfileSlowMs {
			log.Printf("profile: frame %d %q (depth %d) %.1f ms GPU", prof.frame-profileRing, s.name, s.depth, ms)
		}
		if ms > s.maxGPU {
			s.maxGPU = ms
		}
	}
}

// ProfileBegin opens a section. Every ProfileBegin needs its ProfileEnd, in
// nesting order.
func ProfileBegin(name string) {
	if !ProfileEnabled {
		return
	}
	if prof.byName == nil {
		prof.byName = map[profKey]int{}
	}
	key := profKey{parent: -1, name: name}
	if n := len(prof.stack); n > 0 {
		key.parent = prof.stack[n-1]
	}
	i, ok := prof.byName[key]
	if !ok {
		i = len(prof.sections)
		prof.byName[key] = i
		prof.sections = append(prof.sections, &profSection{name: name, depth: len(prof.stack)})
	}
	s := prof.sections[i]
	s.seen = prof.frame
	prof.stack = append(prof.stack, i)
	n := s.used[prof.slot]
	if n == len(s.queries[prof.slot]) {
		var q [2]uint32
		gl.GenQueries(2, &q[0])
		s.queries[prof.slot] = append(s.queries[prof.slot], q)
	}
	gl.QueryCounter(s.queries[prof.slot][n][0], gl.TIMESTAMP)
	s.cpuStart = profNow()
}

// ProfileEnd closes the innermost open section.
func ProfileEnd() {
	if !ProfileEnabled || len(prof.stack) == 0 {
		return
	}
	i := prof.stack[len(prof.stack)-1]
	prof.stack = prof.stack[:len(prof.stack)-1]
	s := prof.sections[i]
	s.cpuFrame += profNow() - s.cpuStart
	n := s.used[prof.slot]
	gl.QueryCounter(s.queries[prof.slot][n][1], gl.TIMESTAMP)
	s.used[prof.slot] = n + 1
}

// ProfileAddCPU adds ms of CPU time to a row named name under the open
// section, with no GPU half: work that is not one contiguous span of the
// frame — the scenery collision's queries, spread through the player's move,
// the AI and the entity pass — measured where it happens and reported here
// as one total. A row that is only ever added to reads zero on the GPU.
func ProfileAddCPU(name string, ms float64) {
	if !ProfileEnabled {
		return
	}
	if prof.byName == nil {
		prof.byName = map[profKey]int{}
	}
	key := profKey{parent: -1, name: name}
	if n := len(prof.stack); n > 0 {
		key.parent = prof.stack[n-1]
	}
	i, ok := prof.byName[key]
	if !ok {
		i = len(prof.sections)
		prof.byName[key] = i
		prof.sections = append(prof.sections, &profSection{name: name, depth: len(prof.stack)})
	}
	s := prof.sections[i]
	s.seen = prof.frame
	s.cpuFrame += ms
}

// ProfileSamples is every section seen so far, in first-seen order. A section
// that has not run for a second's worth of frames reads as zero rather than as
// the last thing it measured.
func ProfileSamples() []ProfileSample {
	out := make([]ProfileSample, 0, len(prof.sections))
	for _, s := range prof.sections {
		p := ProfileSample{Name: s.name, Depth: s.depth, MaxCPU: s.maxCPU, MaxGPU: s.maxGPU}
		if prof.frame-s.seen < 60 {
			p.CPUMs, p.GPUMs = s.cpuMs, s.gpuMs
		}
		if s.nCPU > 0 {
			p.AvgCPU = s.sumCPU / float64(s.nCPU)
		}
		if s.nGPU > 0 {
			p.AvgGPU = s.sumGPU / float64(s.nGPU)
		}
		out = append(out, p)
	}
	return out
}

// ProfileResetTotals starts the benchmark's averages over.
func ProfileResetTotals() {
	for _, s := range prof.sections {
		s.sumCPU, s.sumGPU, s.maxCPU, s.maxGPU = 0, 0, 0, 0
		s.nCPU, s.nGPU = 0, 0
	}
}
