package main

// editor_model_anim_panel_test.go — headless ImGui frames over the animation
// panel.
//
// ImGui's style and item-flag stacks are asserted, not tolerated: a push without
// its pop (or a pop without its push) aborts inside imgui.h with nothing but a
// line number to go on. That is how entering pose mode first crashed — the mode
// button re-tested the flag its own click had flipped, and popped a style colour
// it never pushed.
//
// These run a real frame with no GL context, so the panel is actually laid out
// and every push/pop is actually executed. Balance is checked by imgui itself:
// an unbalanced frame aborts here rather than in front of the user.

import (
	"foundry/internal/model"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// imguiTestFrame runs body inside a complete headless ImGui frame.
func imguiTestFrame(t *testing.T, body func()) {
	t.Helper()
	ctx := imgui.CreateContext(nil)
	defer ctx.Destroy()
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 800})
	// The atlas has to be built and given an id, or drawing text asserts.
	io.Fonts().TextureDataRGBA32()
	io.Fonts().SetTextureID(1)

	imgui.NewFrame()
	imgui.Begin("test##panelframe")
	body()
	imgui.End()
	// EndFrame is where a *leftover* push is caught; a stray pop has already
	// aborted by now.
	imgui.Render()
}

// TestAnimPanelBalancesItsImguiStacks walks the states the panel can be in. Each
// one is a frame that must complete.
func TestAnimPanelBalancesItsImguiStacks(t *testing.T) {
	idx := withPoseEditor(t)

	cases := []struct {
		name  string
		setup func()
	}{
		{"pose mode, no clips", func() {
			modelPoseMode = true
			modelPoseClip = -1
		}},
		{"rest mode, no clips", func() {
			modelPoseMode = false
			modelPoseClip = -1
		}},
		{"pose mode with a clip", func() {
			modelPoseMode = true
			modelPoseClip = modelDoc.AddClip("walk")
		}},
		{"rest mode with a clip", func() {
			modelPoseMode = false
		}},
		{"a keyed and posed bone", func() {
			modelPoseMode = true
			modelSelection.Bones = []int{idx[1]}
			modelPoseSet(idx[1], model.ModelBonePose{
				Rot:   mgl32.QuatRotate(0.4, mgl32.Vec3{1, 0, 0}),
				Scale: mgl32.Vec3{1, 1, 1},
			})
			modelKeyBones([]int{idx[1]})
		}},
		{"nothing selected", func() {
			modelSelection.Bones = nil
		}},
		{"playing", func() {
			modelPosePlaying = true
		}},
	}
	for _, c := range cases {
		c.setup()
		imguiTestFrame(t, modelAnimSection)
		t.Logf("frame completed: %s", c.name)
	}
}

// TestTimelineStripBalancesItsImguiStacks covers the docked strip, which has its
// own push/pop pairs — a disabled Delete button and a latched Play toggle — plus
// a child region that must be ended on every path out.
//
// It cannot go through modelAnimTimelineWindow, which positions itself against
// modelEditorWindow and so needs a real window; the two functions it wraps are
// the ones that actually touch the stacks.
func TestTimelineStripBalancesItsImguiStacks(t *testing.T) {
	idx, c := keyedPoseEditor(t)

	cases := []struct {
		name  string
		setup func()
	}{
		{"nothing selected", func() { modelClearKeySelection() }},
		{"one key selected", func() {
			modelSelectKey([]modelKeyRef{{Bone: idx[0], Time: 0}}, false)
		}},
		{"playing", func() { modelPosePlaying = true }},
		{"paused", func() { modelPosePlaying = false }},
		{"clip with no keys", func() {
			modelClearKeySelection()
			c.Tracks = nil
		}},
	}
	for _, tc := range cases {
		tc.setup()
		imguiTestFrame(t, func() {
			modelTimelineTransportRow()
			modelTimelineCanvas()
		})
		t.Logf("frame completed: %s", tc.name)
	}
}

// TestAnimPanelBalancesWithNoArmature covers the early-return path, which skips
// most of the panel and so exercises a different set of pushes.
func TestAnimPanelBalancesWithNoArmature(t *testing.T) {
	prevDoc, prevMode := modelDoc, modelPoseMode
	t.Cleanup(func() { modelDoc, modelPoseMode = prevDoc, prevMode })

	modelDoc = model.NewCubeDoc()
	for _, mode := range []bool{false, true} {
		modelPoseMode = mode
		imguiTestFrame(t, modelAnimSection)
	}
}

// TestActiveToggleButtonBalancesInBothStates pins the helper the mode switch
// goes through. `active` is a parameter precisely so a caller cannot flip it
// between the push and the pop.
func TestActiveToggleButtonBalancesInBothStates(t *testing.T) {
	for _, active := range []bool{false, true} {
		imguiTestFrame(t, func() {
			editorActiveToggleButton("toggle##test", active)
		})
	}

	// And the shape that used to crash: a caller that flips the flag it passed.
	// Through the helper this is harmless, because the push and the pop both
	// read the same evaluated argument.
	flag := false
	imguiTestFrame(t, func() {
		for i := 0; i < 3; i++ {
			active := flag
			if editorActiveToggleButton("flip##test", active) {
				flag = !active
			}
			flag = !flag // flip underneath, as a click would
		}
	})
}
