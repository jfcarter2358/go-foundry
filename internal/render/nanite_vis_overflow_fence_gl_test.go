package render

// nanite_vis_overflow_fence_gl_test.go — the overflow readback never asks GL
// about a fence it no longer owns (nanite_vis_overflow.go, "Fence lifetime").
//
// PRESETS_5 found TestSceneryGPUCullDrawsWhatTheCPUCullDrew failing in a long
// combined GL run on a GL_INVALID_VALUE it had not caused: glGetSynciv on a
// fence an earlier test's saved copy of this state still held after the live
// state had deleted it. These cases build that copy on purpose, and a state
// "from another context", and check the GL error queue stays empty.
//
//	FOUNDRY_GL_TEST=1 go test -run VisOverflowFence ./internal/render/

import (
	"testing"

	"foundry/internal/gltest"

	"github.com/go-gl/gl/v4.3-core/gl"
)

func TestVisOverflowFenceIsNeverQueriedOnceDead(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	prevDense := NaniteVisDense
	NaniteVisDense = true
	t.Cleanup(func() { NaniteVisDense = prevDense })
	drainGLErrors()

	// A counter holding a demand, as the dense numbering leaves it.
	var counter uint32
	gl.GenBuffers(1, &counter)
	t.Cleanup(func() { gl.DeleteBuffers(1, &counter) })
	gl.BindBuffer(gl.COPY_READ_BUFFER, counter)
	demand := []uint32{1234}
	gl.BufferData(gl.COPY_READ_BUFFER, 4, gl.Ptr(demand), gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.COPY_READ_BUFFER, 0)

	var live visOverflowState
	t.Cleanup(func() { live.release() })
	live.note(counter, true) // copies the demand and fences it
	if !live.ok || !live.ring[0].pending {
		t.Fatalf("the first note did not leave a fenced copy (ok %v)", live.ok)
	}
	saved := live // the copy the old test kept, fence and all

	// The live state reads its copy back and deletes the fence.
	gl.Finish()
	live.note(counter, false)
	if live.demand != 1234 {
		t.Fatalf("read back demand %d, want 1234", live.demand)
	}
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("reading the live state's own fence left GL error 0x%x", e)
	}

	// The saved copy's slot names that deleted fence. Its next note must
	// drop it rather than query it.
	saved.note(counter, false)
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("a note over a dead fence left GL error 0x%x (glGetSynciv on a deleted sync)", e)
	}
	if saved.ring[0].pending {
		t.Errorf("the dead fence's slot is still pending: the ring would stay full for good")
	}
	saved.forget() // its buffers are live's; live releases them
}

func TestVisOverflowStateFromAnotherContextIsForgottenNotQueried(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	prevDense := NaniteVisDense
	NaniteVisDense = true
	t.Cleanup(func() { NaniteVisDense = prevDense })
	drainGLErrors()

	var counter uint32
	gl.GenBuffers(1, &counter)
	t.Cleanup(func() { gl.DeleteBuffers(1, &counter) })
	gl.BindBuffer(gl.COPY_READ_BUFFER, counter)
	gl.BufferData(gl.COPY_READ_BUFFER, 4, nil, gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.COPY_READ_BUFFER, 0)

	// A state whose objects were made in some other context: its names mean
	// nothing here (a bogus fence would be GL_INVALID_VALUE if queried).
	other := visOverflowState{tried: true, ok: true, ctx: currentGLContext() + 1, risk: true}
	other.ring[0].pending = true
	other.ring[0].fence = 0xdead0
	other.ring[0].buf = 0xbeef
	other.note(counter, true)
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("a note in a new context touched the old context's names: GL error 0x%x", e)
	}
	if other.ctx != currentGLContext() || !other.ok {
		t.Fatalf("the state did not rebuild in the current context (ctx %x ok %v)", other.ctx, other.ok)
	}
	if other.risk {
		t.Errorf("the old context's risk survived the context change")
	}
	if other.ring[0].buf == 0xbeef {
		t.Errorf("the old context's buffer name was kept")
	}

	// release deletes everything it made and zeroes the state.
	other.release()
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("release left GL error 0x%x", e)
	}
	if other.tried || other.ok || other.ring[0].fence != 0 || other.ring[0].buf != 0 {
		t.Errorf("release left state behind: %+v", other)
	}
}

// drainGLErrors empties the error queue so a test sees only its own errors.
func drainGLErrors() {
	for i := 0; i < 32 && gl.GetError() != gl.NO_ERROR; i++ {
	}
}
