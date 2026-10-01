package render

// frame_pace.go — where a GPU-bound frame waits.
//
// When the GPU is the slower of the two, the CPU has to wait for it somewhere.
// Left to the driver, "somewhere" is wherever its command queue happens to be
// full: a glGet (until frame_target.go took those out of the frame), or a
// draw call deep in a pass — on level1 it was the sun's cascade pass, so the
// F3 profile blamed the shadows for the time the GPU spent on everything else.
//
// PaceFrame makes the wait explicit and puts it in one place, right after the
// swap: a fence at the end of each frame, and before the next one starts, a
// wait for the frame BEFORE this one to be done. The GPU always has this
// frame's work queued while the CPU builds the next, so it never runs dry, and
// no more than that is queued, so the driver never blocks mid-frame and the
// player's input is at most one frame behind the picture.

import (
	"github.com/go-gl/gl/v4.3-core/gl"
)

// framePaceTimeout bounds one wait, in nanoseconds: a GPU reset or a hung
// driver should cost a stutter, not the game.
const framePaceTimeout = 1_000_000_000

var framePace struct {
	prev uintptr // the last frame's fence; 0 before the first
}

// PaceFrame fences the frame just submitted and waits for the one before it.
// Call once a frame, after the swap.
func PaceFrame() {
	f := gl.FenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0)
	if framePace.prev != 0 {
		gl.ClientWaitSync(framePace.prev, gl.SYNC_FLUSH_COMMANDS_BIT, framePaceTimeout)
		gl.DeleteSync(framePace.prev)
	}
	framePace.prev = f
}
