package textureui

import (
	"testing"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// A solid block's outline is its four sides, each merged into one segment.
func TestSelectionOutlineMergesRuns(t *testing.T) {
	const w, h = 6, 5
	mask := make([]bool, w*h)
	for y := 1; y <= 3; y++ {
		for x := 1; x <= 4; x++ {
			mask[y*w+x] = true
		}
	}
	segs := selectionOutline(mask, w, h)
	if len(segs) != 4 {
		t.Fatalf("got %d segments, want 4: %v", len(segs), segs)
	}
	var perim float32
	for _, s := range segs {
		perim += s.b.Sub(s.a).Len()
	}
	if perim != 2*(4+3) {
		t.Fatalf("perimeter %v, want 14", perim)
	}
}

// Selected texels on the canvas edge are outlined along it.
func TestSelectionOutlineCanvasEdge(t *testing.T) {
	mask := []bool{true, true, true, true}
	if segs := selectionOutline(mask, 2, 2); len(segs) != 4 {
		t.Fatalf("got %d segments, want the canvas border's 4", len(segs))
	}
}

func TestClipSpan(t *testing.T) {
	lo, hi := imgui.Vec2{X: 0, Y: 0}, imgui.Vec2{X: 10, Y: 10}
	t0, t1, ok := clipSpan(imgui.Vec2{X: -5, Y: 5}, imgui.Vec2{X: 1, Y: 0}, 100, lo, hi)
	if !ok || t0 != 5 || t1 != 15 {
		t.Fatalf("clipSpan = %v %v %v, want 5 15 true", t0, t1, ok)
	}
	if _, _, ok := clipSpan(imgui.Vec2{X: -5, Y: 20}, imgui.Vec2{X: 1, Y: 0}, 100, lo, hi); ok {
		t.Fatal("a line wholly outside the rect should not clip in")
	}
}

func TestScaleHandleEdges(t *testing.T) {
	// Handle 2 is the bottom-right corner, 5 the right edge, 0 the top-left.
	if got := scaleHandleEdges(2, 3, 1, 4, 2, false); got != [4]int{0, 0, 3, 1} {
		t.Fatalf("free corner = %v", got)
	}
	if got := scaleHandleEdges(5, 3, 7, 4, 2, true); got != [4]int{0, 0, 3, 0} {
		t.Fatalf("edge handle should ignore y and aspect: %v", got)
	}
	// 4x2 box, corner dragged 4 right but only 1 down: x wins, doubling both.
	if got := scaleHandleEdges(2, 4, 1, 4, 2, true); got != [4]int{0, 0, 4, 2} {
		t.Fatalf("aspect corner = %v", got)
	}
	// Top-left corner moves the left and top edges, in the drag's direction.
	if got := scaleHandleEdges(0, -2, -1, 4, 2, true); got != [4]int{-2, -1, 0, 0} {
		t.Fatalf("top-left aspect corner = %v", got)
	}
}
