package render

// dynres.go — the render scale and dynamic resolution (gfx/render_scale.go;
// docs/adventure/PHASE_F.md, Phase F2).
//
// Under a render scale s < 1 a TAA frame draws the world into a scene target
// s times the output's width and height, jittered over more sample points
// (taaJitterPhases), and the TAA resolve reconstructs the output-sized
// history from them (taa_resolve_frag.glsl's resolveUpscaled). The history,
// bloom, exposure and output pass stay at the output's size, and so does
// everything drawn after DrawWorld (the HUD, the UI).
//
// Dynamic resolution picks s each frame from the world's GPU time: two
// timestamp queries round the frame (sceneFrameBegin to the resolve's end),
// read three frames later — PaceFrame has by then waited out the frame they
// bracket, so the read never stalls — and a controller that steps s in
// twentieths: down at once when a frame runs over the target, up only after
// a run of frames with room to spare, so it does not hunt.
//
// FOUNDRY_RENDER_SCALE_PATH=0: every frame at the output's size, whatever
// the settings say (the old behaviour).

import (
	"math"

	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// RenderScalePath switches the render scale on at all.
var RenderScalePath = gfx.EnvFlag("FOUNDRY_RENDER_SCALE_PATH", true)

// dynResStep is the scale's quantum: a change remakes the scene target's
// attachments (and the passes' own targets sized from it), so it moves in
// steps rather than continuously.
const dynResStep = 0.05

// dynResSlots is the timestamp ring's length: a slot is read back this many
// frames after it was written.
const dynResSlots = 4

type dynResState struct {
	// scale is the dynamic scale in force; 0 before the first frame.
	scale float32
	q     [dynResSlots][2]uint32
	used  [dynResSlots]bool
	at    [dynResSlots]float32 // the scale the slot's frame was drawn at
	slot  int
	open  bool
	// since counts frames since the last change; roomy counts frames in a
	// row with room to step up.
	since, roomy int
	// lastMs is the last world GPU time read back; avgMs its moving
	// average at the scale in force (0: none yet).
	lastMs, avgMs float32
	// spikes counts frames in a row far over the target; downAt is
	// sinceDown's count of frames since the last step down.
	spikes, sinceDown int
}

var dynRes dynResState

// frameRenderScale is the scale this frame is drawn at: 1 outside a TAA
// frame the resolve runs in, in the editor's view, or with the path off.
func frameRenderScale(o WorldDraw, on bool) float32 {
	if !RenderScalePath || !on || o.Editor {
		return 1
	}
	top := gfx.ClampRenderScale(gfx.RenderScale)
	if !gfx.DynamicResolution {
		dynRes.scale = 0
		return top
	}
	lo := min(gfx.ClampRenderScale(gfx.DynamicResolutionMin), top)
	if dynRes.scale == 0 {
		dynRes.scale = top
	}
	dynRes.scale = min(max(dynRes.scale, lo), top)
	return dynRes.scale
}

// CurrentRenderScale is the scale the last frame was drawn at (1: none),
// for the bench and the F3 overlay.
func CurrentRenderScale() float32 {
	if taa.w == 0 {
		return 1
	}
	return float32(taa.rw) / float32(taa.w)
}

// LastWorldGPUMs is the world's GPU time dynamic resolution last read back
// (0 when it is off).
func LastWorldGPUMs() float32 { return dynRes.lastMs }

// scaledSize is w by h under scale s, at least a pixel each way.
func scaledSize(w, h int32, s float32) (int32, int32) {
	if s >= 1 {
		return w, h
	}
	rw := int32(math.Round(float64(w) * float64(s)))
	rh := int32(math.Round(float64(h) * float64(s)))
	return max(rw, 1), max(rh, 1)
}

// taaJitterPhases is the length of the jitter sequence for a world rw wide
// drawn into an output w wide: 8 at full size, 8/s² under a scale (UE's
// TAAU), so an output pixel sees as many samples near its centre as before.
func taaJitterPhases(rw, w int32) int {
	if rw >= w || rw <= 0 {
		return taaJitterSamples
	}
	s := float64(rw) / float64(w)
	return min(int(math.Ceil(taaJitterSamples/(s*s))), 32)
}

// upscaleSharpen is the output's sharpening under scale s: the player's,
// and more as s falls (the reconstruction's filter is wider than a pixel).
func upscaleSharpen(sharpen, s float32) float32 {
	return min(sharpen+0.6*(1-s), 1)
}

// dynResBegin opens this frame's timestamp pair. After the scene target is
// bound, before anything draws.
func dynResBegin() {
	if !gfx.DynamicResolution || !RenderScalePath || !taa.on || taa.editorDepth {
		dynRes.open = false
		return
	}
	d := &dynRes
	if d.q[0][0] == 0 {
		for i := range d.q {
			gl.GenQueries(2, &d.q[i][0])
		}
	}
	// The slot written dynResSlots frames ago: PaceFrame waited for the
	// frame before last, so it is done.
	if d.used[d.slot] {
		var t0, t1 uint64
		gl.GetQueryObjectui64v(d.q[d.slot][0], gl.QUERY_RESULT, &t0)
		gl.GetQueryObjectui64v(d.q[d.slot][1], gl.QUERY_RESULT, &t1)
		d.used[d.slot] = false
		if t1 > t0 {
			dynResUpdate(float32(float64(t1-t0)/1e6), d.at[d.slot])
		}
	}
	gl.QueryCounter(d.q[d.slot][0], gl.TIMESTAMP)
	d.open = true
}

// dynResEnd closes it, after the resolve.
func dynResEnd() {
	d := &dynRes
	if !d.open {
		return
	}
	gl.QueryCounter(d.q[d.slot][1], gl.TIMESTAMP)
	d.used[d.slot] = true
	d.at[d.slot] = CurrentRenderScale()
	d.slot = (d.slot + 1) % dynResSlots
	d.open = false
}

// dynResSettle is how many measured frames after a change are not believed:
// the first frames at a new size remake the passes' targets.
const dynResSettle = 4

// dynResFixedShare is the share of a frame's GPU time the up-step's
// prediction takes as not scaling with the pixels.
const dynResFixedShare = 0.3

// dynResUpdate steps the scale by a frame that took ms at scale at.
//
// The decisions read a moving average of the frames drawn at the scale in
// force. Down: once the average is over the target (or one frame is far
// over it), straight to the scale that would have met it, at least a step.
// Up: a step, once the average says the next step up would still come in
// under the target with a margin, for a run of frames — so it settles
// just under the target instead of hunting about it.
func dynResUpdate(ms, at float32) {
	d := &dynRes
	d.lastMs = ms
	target := gfx.DynamicResolutionTargetMs
	top := gfx.ClampRenderScale(gfx.RenderScale)
	lo := min(gfx.ClampRenderScale(gfx.DynamicResolutionMin), top)
	if d.scale == 0 {
		d.scale = top
	}
	// A frame drawn before the last change says little about the scale now.
	if math.Abs(float64(at-d.scale)) > 1e-3 {
		return
	}
	d.since++
	d.sinceDown++
	if d.since <= dynResSettle {
		return
	}
	if d.avgMs == 0 {
		d.avgMs = ms
	} else {
		d.avgMs += 0.2 * (ms - d.avgMs)
	}
	avg := d.avgMs
	if ms > target*1.5 {
		d.spikes++
	} else {
		d.spikes = 0
	}
	// Down: time taken as all per pixel, s² of the full-size frame's (part
	// of it is not, so a step down falls short and the next one follows).
	// Up: a share dynResFixedShare of the time taken as not per pixel at
	// all (the adventure level's slot: 29 ms at 1.0, 18.4 at 0.75 — about
	// a third of it fixed at the scales dynamic resolution settles at). One
	// far-over frame — a chunk switch, a page burst — is not a reason to
	// drop; two in a row are.
	switch {
	case (avg > target*1.04 || d.spikes >= 3) && d.scale > lo+1e-4:
		worst := max(avg, ms*0.5+avg*0.5)
		ideal := at * float32(math.Sqrt(float64(target/max(worst, 0.1))))
		n := float32(math.Floor(float64(ideal/dynResStep))) * dynResStep
		d.set(max(min(n, d.scale-dynResStep), lo))
		d.roomy, d.sinceDown = 0, 0
	case d.scale < top-1e-4:
		up := min(d.scale+dynResStep, top)
		pred := avg * (dynResFixedShare + (1-dynResFixedShare)*(up*up)/(at*at))
		// Every change remakes the frame's targets (a hitch of its own),
		// so up only with a margin, after a second of room, and not within
		// two seconds of a step down: it settles rather than hunts.
		if pred < target*0.92 && d.sinceDown >= 120 {
			d.roomy++
			if d.roomy >= 60 {
				d.set(up)
				d.roomy = 0
			}
		} else {
			d.roomy = 0
		}
	}
}

func (d *dynResState) set(s float32) {
	s = float32(math.Round(float64(s)/dynResStep)) * dynResStep
	if math.Abs(float64(s-d.scale)) < 1e-4 {
		return
	}
	d.scale = s
	d.since = 0
	d.avgMs = 0
}

// RenderScaleMipBias switches the upsampling frame's texture LOD bias
// (upscaleDerivScale). FOUNDRY_RENDER_SCALE_MIPBIAS=0: none.
var RenderScaleMipBias = gfx.EnvFlag("FOUNDRY_RENDER_SCALE_MIPBIAS", true)

// upscaleDerivScale is the material pass's derivative scale this frame: the
// render scale under an upsampling frame (a LOD bias of log2 s), else 1.
func upscaleDerivScale() float32 {
	if !RenderScaleMipBias || !taa.active || !taa.on || !taa.upscaling() {
		return 1
	}
	return float32(taa.rw) / float32(taa.w)
}
