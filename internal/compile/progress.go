package compile

// progress.go — telling whoever asked how far a compile has got.
//
// A compile of a big level is minutes of light bake, and the editor runs it
// off the main thread behind a progress bar (editor_compile.go). What it needs
// from here is the name of the step being taken and, for the steps that loop
// over faces, how far through them it is.

import (
	"sync/atomic"
)

// Progress hears a compile's steps: stage names the step, done is how far
// through it, 0 to 1. A new stage is first reported at 0.
//
// Called from the compile's own goroutines — the per-face steps report from
// whichever worker finished the face — so it must be safe to call
// concurrently.
type Progress func(stage string, done float32)

// report is p, or nothing for a nil p.
func (p Progress) report(stage string, done float32) {
	if p != nil {
		p(stage, done)
	}
}

// progressSteps is how many times a per-face stage reports along the way: often
// enough for the bar to move, rarely enough that a level of a million faces is
// not a million calls.
const progressSteps = 200

// parallelFor is parallelFor, reporting stage as the items are finished.
func (p Progress) parallelFor(stage string, n int, fn func(i int)) {
	if p == nil {
		parallelFor(n, fn)
		return
	}
	p(stage, 0)
	every := int64(max(n/progressSteps, 1))
	var finished atomic.Int64
	parallelFor(n, func(i int) {
		fn(i)
		if c := finished.Add(1); c%every == 0 {
			p(stage, float32(c)/float32(n))
		}
	})
	p(stage, 1)
}
