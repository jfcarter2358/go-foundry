package render

// nanite_vis_overflow.go — what the visibility buffer cannot number is drawn
// the forward way instead of being lost (docs/regions/PRESETS_4.md, bug 1).
//
// # The bug
//
// A visibility-buffer pixel holds an ID: (record + 1) << 12 | triangle, so a
// frame can number 2^20 commands (naniteVisMaxRecords). Dense numbering
// (NaniteVisDense, nanite_vis_number_comp.glsl) takes a record per command
// that draws, from one counter every pass of the frame shares, and a command
// past the last one is DROPPED — its instance count set to 0 — as is a
// compute-rasterised cluster past it (nanite_swraster_comp.glsl). Nothing
// draws either: the pixels they covered keep whatever the colour target held,
// which in the biome showcase was white. Its 600 m overview — 14,300 scanned
// placements, every one at least its DAG's root clusters, over 1,000 terrain
// tiles — asked for ~1.19 M records a frame (measured on the GTX 1070) and
// lost the rest: white blocks over the pines, the volcanic and tundra scans
// and the wetland, white dashes along terrain tiles' seams, all of it moving
// with the camera. Raising the LOD error did not help (the roots are the
// floor); turning the compute rasteriser off lost fewer (its clusters each
// take a record, and they are numbered after the hardware's commands).
//
// # The fix
//
// The frame's demand is read back — the counter, copied at the start of the
// next frame into a small ring and read when its fence has passed, never
// waited on — and while it is near or over the limit (overflowRisk, with
// hysteresis) each pass of a dense batch:
//
//   - lists nothing for the compute rasteriser (admitSW): a cluster it drops
//     has no command to fall back to, a hardware command does;
//   - after its ID draws, has its commands turned round
//     (nanite_vis_overflow_comp.glsl: what was dropped draws, what was
//     numbered does not) and its groups drawn again the forward way, depth
//     tested against the visibility buffer, setting the stencil to 0 where
//     they win — the rule every forward draw over the ID pass already
//     follows, so the material pass never shades those pixels twice.
//
// A frame under the limit — every view of an ordinary level — pays one
// 4-byte copy and nothing else. Over it, the overflow costs the forward
// shading of what overflowed, which is what the vis buffer existed to save,
// but the picture is whole. The first frame or two of an overflow (before
// its readback lands) still lose those commands.
//
// # Fence lifetime
//
// The state is a package global and outlives the GL context it was built in
// (a test binary shares one context across every test; an app could tear its
// context down and make another). Its ring holds fences, and a fence is only
// a name inside the context that made it: asking glGetSynciv about one that
// was deleted — or that belongs to another context — is GL_INVALID_VALUE,
// left on the error queue for whoever calls glGetError next. That is what
// TestSceneryGPUCullDrawsWhatTheCPUCullDrew picked up in a long combined GL
// run (PRESETS_5): the overflow test had saved a copy of this state with a
// fence pending, its first frames read and deleted that fence through the
// live state, and its cleanup put the copy back — the next frame's note
// queried a dead fence. So:
//
//   - the state remembers the context it was built in (ctx); a note in any
//     other context forgets everything WITHOUT a GL call (the objects are
//     not this context's to query or delete) and rebuilds lazily;
//   - a pending slot whose fence is no longer a sync object (glIsSync) is
//     dropped, never queried;
//   - release deletes the program, buffers and fences in their own context
//     and zeroes the state — what teardown (and a test's cleanup) calls,
//     instead of restoring a copy of live GPU state.

import (
	"log"

	"foundry/internal/gfx"
	"foundry/internal/glext"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// naniteVisRecordLimit is the records the dense numbering hands out a frame:
// naniteVisMaxRecords, all an ID can name. A variable only so a test can
// make a frame of a few placements overflow (nanite_vis_overflow_gl_test.go).
var naniteVisRecordLimit uint32 = naniteVisMaxRecords

// The thresholds, as shares of naniteVisRecordLimit: the risk is raised when
// a frame asks for more than visOverflowOn of the records, and dropped after
// visOverflowCalm readbacks in a row under visOverflowOff.
const (
	visOverflowOn   = 0.92
	visOverflowOff  = 0.75
	visOverflowCalm = 60
	visOverflowRing = 3
)

// visOverflowState is the demand readback and the risk it says.
type visOverflowState struct {
	tried, ok bool
	// ctx is the GL context the program, ring buffers and fences were made
	// in (currentGLContext); 0 before ready.
	ctx       uintptr
	prog      progUniforms
	ring      [visOverflowRing]readbackSlot
	next int
	// demand is the newest frame's records asked for (read back); risk
	// whether passes hand their overflow to the forward path; calm the
	// readbacks in a row under visOverflowOff.
	demand uint32
	risk   bool
	calm   int
	logged bool
}

var naniteVisOverflow visOverflowState

func (s *visOverflowState) ready() bool {
	if s.tried {
		return s.ok
	}
	s.tried = true
	s.ctx = currentGLContext()
	cs, err := buildComputeProgram(mustReadShader(biomeShaderPath("nanite_vis_overflow_comp.glsl")))
	if err != nil {
		log.Printf("nanite: visibility buffer overflow pass unavailable: %v", err)
		return false
	}
	s.prog = cs
	persistent := NanitePersistent && glext.BufferStorageAvailable()
	for i := range s.ring {
		s.ring[i].init(4, persistent)
	}
	s.ok = true
	return true
}

// buildComputeProgram builds a compute program from source.
func buildComputeProgram(src string) (progUniforms, error) {
	cs, err := gfx.CompileShader(src, gl.COMPUTE_SHADER)
	if err != nil {
		return progUniforms{}, err
	}
	p, err := linkStages(cs)
	if err != nil {
		return progUniforms{}, err
	}
	return progUniforms{prog: p, loc: map[string]int32{}}, nil
}

// note takes the frame that just ended's demand into the ring (counter is
// the dense numbering's, used: whether that frame numbered anything) and
// reads back whatever earlier copy has landed. Called at the start of a
// frame, before the counter is zeroed.
func (s *visOverflowState) note(counter uint32, used bool) {
	if !NaniteVisDense || counter == 0 {
		return
	}
	if s.tried && s.ctx != currentGLContext() {
		s.forget()
	}
	if !s.ready() {
		return
	}
	// Read what has landed, oldest first; never wait — and never ask with
	// glClientWaitSync, which blocks on NVIDIA's driver even with a zero
	// timeout (fencePassed, nanite_stream.go): asked here every frame it
	// cost the showcase's pine forest 15 ms of CPU a frame.
	for k := 0; k < visOverflowRing; k++ {
		e := &s.ring[(s.next+k)%visOverflowRing]
		if !e.pending {
			continue
		}
		if !e.alive() {
			// Its fence was deleted behind this slot's back: nothing will
			// ever signal it, and asking is an error. Free the slot.
			e.fence, e.pending = 0, false
			continue
		}
		if !e.ready() {
			break
		}
		words := e.read()
		e.done()
		e.pending = false
		if len(words) > 0 {
			s.take(words[0])
		}
	}
	if !used {
		return
	}
	e := &s.ring[s.next]
	if e.pending {
		return // the ring is full of frames in flight: skip this one
	}
	e.copyFrom(counter, 4, 0)
	s.next = (s.next + 1) % visOverflowRing
}

// forget drops every GL name the state holds without touching GL — the
// context they lived in is gone or is not the current one — so the next
// ready builds afresh in the current context. The risk is dropped too: the
// demand it was read from was another context's.
func (s *visOverflowState) forget() {
	*s = visOverflowState{}
}

// release deletes the state's program, ring buffers and fences (in the
// context they were made in — a call from any other only forgets them) and
// zeroes it, so a later frame rebuilds it lazily. What a renderer's
// teardown, or a test that swapped the state, calls.
func (s *visOverflowState) release() {
	if s.tried && s.ctx == currentGLContext() {
		for i := range s.ring {
			if s.ring[i].fence != 0 && !s.ring[i].alive() {
				s.ring[i].fence = 0 // already deleted: do not delete again
			}
			s.ring[i].release()
		}
		if s.prog.prog != 0 {
			gl.DeleteProgram(s.prog.prog)
		}
	}
	s.forget()
}

// take updates the risk from one frame's demand.
func (s *visOverflowState) take(n uint32) {
	s.demand = n
	switch {
	case float64(n) > visOverflowOn*float64(naniteVisRecordLimit):
		s.risk, s.calm = true, 0
		if !s.logged {
			s.logged = true
			log.Printf("nanite: visibility buffer: a frame asked for %d records of %d — the overflow is drawn the forward way while it lasts",
				n, naniteVisRecordLimit)
		}
	case float64(n) < visOverflowOff*float64(naniteVisRecordLimit):
		if s.risk {
			if s.calm++; s.calm >= visOverflowCalm {
				s.risk, s.calm = false, 0
			}
		}
	default:
		s.calm = 0
	}
}

// overflowRisk reports whether this frame's dense passes hand their
// overflow to the forward path.
func (s *visOverflowState) overflowRisk() bool { return NaniteVisDense && s.ok && s.risk }

// drawOverflow draws what pass b's numbering refused, the forward way: its
// commands (cmd, packed or not) turned round, then each group the
// visibility buffer drew, through its own forward draw. Called by drawBatch
// after the ID draws, with the colour target bound and the stencil rule
// writing 0.
func (s *visOverflowState) drawOverflow(b *cullBatch, groups []visGroup, cmd uint32) {
	p := &s.prog
	gl.UseProgram(p.prog)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 5, cmd)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, b.visGroupBuf)
	if b.compacted {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 4, b.countBuf)
	} else {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 4, naniteVis.dummy)
	}
	gl.Uniform1ui(p.U("slots"), uint32(b.slots))
	gl.Uniform1ui(p.U("groupCount"), uint32(len(b.groups)))
	gl.Uniform1ui(p.U("packedCounts"), uint32(boolToInt32(b.compacted)))
	gl.DispatchCompute(uint32((b.slots+63)/64), 1, 1)
	gl.MemoryBarrier(gl.COMMAND_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)
	for _, i := range []uint32{2, 4, 5} {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, i, 0)
	}
	for gi := range groups {
		if b.groupLive(gi) && frameSurface(groups[gi].mat).prepass && groups[gi].forward != nil {
			groups[gi].forward()
		}
	}
	gl.BindVertexArray(0)
}
