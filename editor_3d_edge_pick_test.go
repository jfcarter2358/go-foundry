package main

import (
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// setupEdgePickScene builds a chunk holding sectors over the given point set,
// aims the camera at the origin from +Z, and returns the level. Ceilings sit at
// y=3 so ceiling and vertical edges stay well away from the clicked pixels.
func setupEdgePickScene(points []mgl32.Vec2, sectors ...[]int) *Level {
	platform.WinWidth, platform.WinHeight = 800, 600
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	gfx.Cam.Position = mgl32.Vec3{0, 1, 6}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})

	ch := Chunk{ID: "c0", Points: points}
	for i, idx := range sectors {
		s := Sector{ID: string(rune('a' + i)), PointIndices: idx}
		s.Floor.Enabled = true
		s.Ceiling.Enabled = true
		s.Ceiling.Height = 3
		ch.Sectors = append(ch.Sectors, s)
	}
	return &Level{Chunks: []Chunk{ch}}
}

// pixelOf projects a world point to the pixel it lands on.
func pixelOf(t *testing.T, p mgl32.Vec3) (int32, int32) {
	t.Helper()
	screen, ok := projectToScreen(gfx.Projection.Mul4(gfx.Cam.Matrix), p)
	if !ok {
		t.Fatalf("%v did not project to the screen", p)
	}
	return int32(screen.X()), int32(screen.Y())
}

// TestEdgeClickSelectsBothEndpoints checks that clicking the middle of a floor
// edge selects the two vertices it joins, so the translate gizmo drags the edge.
func TestEdgeClickSelectsBothEndpoints(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	Editor.level = setupEdgePickScene(
		[]mgl32.Vec2{{-2, 0}, {2, 0}, {0, -3}},
		[]int{0, 1, 2},
	)
	Editor.ActiveChunkIdx = 0
	clearVertexSelection()

	// Edge 0 runs (-2,0,0) → (2,0,0); click its midpoint.
	mx, my := pixelOf(t, mgl32.Vec3{0, 0, 0})
	if !handleEdgeClick(mx, my, false) {
		t.Fatal("click on the middle of a floor edge picked nothing")
	}
	if len(Editor.SelectedVertices) != 2 {
		t.Fatalf("selected %d vertices, want the edge's 2: %+v",
			len(Editor.SelectedVertices), Editor.SelectedVertices)
	}
	for i, want := range []int{0, 1} {
		got := Editor.SelectedVertices[i]
		if got.vertIdx != want || !got.isFloor || got.sectorIdx != 0 {
			t.Fatalf("endpoint %d = %+v, want floor vertex %d of sector 0", i, got, want)
		}
	}
	if Editor.GizmoKind != gizmoKindVertices {
		t.Fatalf("GizmoKind = %q, want %q", Editor.GizmoKind, gizmoKindVertices)
	}

	// A click well off the edge must leave it to the geometry picker.
	if handleEdgeClick(mx, my-300, false) {
		t.Fatal("a click far from every edge picked an edge")
	}
}

// TestEdgePrismYieldsCornersToPoints is the rule the prism sizing exists for: at
// a vertex, the point is selectable and no edge is, so a corner click can never
// grab an edge instead of the point it meets.
func TestEdgePrismYieldsCornersToPoints(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	Editor.level = setupEdgePickScene(
		[]mgl32.Vec2{{-2, 0}, {2, 0}, {0, -3}},
		[]int{0, 1, 2},
	)
	Editor.ActiveChunkIdx = 0
	clearVertexSelection()

	// Vertex 0's floor handle — where the floor edge and the vertical edge
	// both terminate.
	mx, my := pixelOf(t, mgl32.Vec3{-2, 0, 0})
	if len(vertexHandlesInBox(mx, my)) == 0 {
		t.Fatal("the corner's own point was not selectable")
	}
	if got := edgeHandlesInPrism(mx, my); len(got) != 0 {
		t.Fatalf("a corner click landed inside %d edge prism(s): %+v", len(got), got)
	}

	// And the prism is genuinely thinner than the point box: a click offset
	// sideways by more than half an edge prism but less than half a point box
	// still finds the point.
	off := int32(vertexHandleHalfPx - 1)
	if len(vertexHandlesInBox(mx, my+off)) == 0 {
		t.Fatalf("point box did not extend %d px from the vertex", off)
	}
}

// TestEdgeHandleCycling checks that repeated clicks walk through every edge
// under the cursor, and that ctrl+click takes them all at once. Two sectors
// share the segment between points 0 and 1, so its floor edge is two edges.
func TestEdgeHandleCycling(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	Editor.level = setupEdgePickScene(
		[]mgl32.Vec2{{-2, 0}, {2, 0}, {0, -3}, {0, -8}},
		[]int{0, 1, 2},
		[]int{0, 1, 3},
	)
	Editor.ActiveChunkIdx = 0
	clearVertexSelection()

	mx, my := pixelOf(t, mgl32.Vec3{0, 0, 0})

	var seen []int
	for i := 0; i < 3; i++ {
		h, ok := pickEdgeHandle(mx, my)
		if !ok {
			t.Fatalf("click %d: no edge handle picked", i)
		}
		if h.kind != edgeKind3DFloor || h.vertIdx != 0 {
			t.Fatalf("click %d: picked kind=%q vertIdx=%d, want the shared floor edge",
				i, h.kind, h.vertIdx)
		}
		setEdgeSelection([]edgeSel{h.edgeSel})
		seen = append(seen, h.sectorIdx)
	}
	if seen[0] == seen[1] {
		t.Fatalf("clicking again did not advance to the other sector: %v", seen)
	}
	if seen[2] != seen[0] {
		t.Fatalf("cycle did not wrap around: %v", seen)
	}

	// Ctrl+click grabs both sectors' copies of the edge — four vertices, since
	// the two sectors track the shared points separately.
	clearVertexSelection()
	if !handleEdgeClick(mx, my, true) {
		t.Fatal("ctrl+click picked nothing")
	}
	if len(Editor.SelectedVertices) != 4 {
		t.Fatalf("ctrl+click selected %d vertices, want 4: %+v",
			len(Editor.SelectedVertices), Editor.SelectedVertices)
	}
	// Both copies now read as selected, which is what draws them highlighted.
	for si := 0; si < 2; si++ {
		sel := edgeSel{chunkIdx: 0, sectorIdx: si, kind: edgeKind3DFloor, vertIdx: 0}
		if !isEdgeSelected(sel) {
			t.Fatalf("sector %d's shared edge did not read as selected", si)
		}
	}
}

// TestEdgeHandleRangeLimit checks that an edge whose points are beyond
// vertexHandleMaxDist gets no prism, matching the vertex handle range rule.
func TestEdgeHandleRangeLimit(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	far := vertexHandleMaxDist * 3
	Editor.level = setupEdgePickScene(
		[]mgl32.Vec2{{-2, -far}, {2, -far}, {0, -far - 3}},
		[]int{0, 1, 2},
	)
	Editor.ActiveChunkIdx = 0
	clearVertexSelection()

	// In range: camera a few units in front of the edge.
	gfx.Cam.Position = mgl32.Vec3{0, 1, -far + 6}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, -far}, mgl32.Vec3{0, 1, 0})
	mx, my := pixelOf(t, mgl32.Vec3{0, 0, -far})
	if len(edgeHandlesInPrism(mx, my)) == 0 {
		t.Fatal("edge within range was not pickable")
	}

	// Out of range: same screen position, camera far away.
	gfx.Cam.Position = mgl32.Vec3{0, 1, 6}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	if got := edgeHandlesInPrism(platform.WinWidth/2, platform.WinHeight/2); len(got) != 0 {
		t.Fatalf("edge %.0f units away was pickable (limit %.0f)", far, vertexHandleMaxDist)
	}
}

// TestEdgePrismTrimAndThickness pins the two size rules directly: the prism is
// pulled back from both endpoints far enough to clear their handle boxes on
// screen, and it is thinner across than one of those boxes.
func TestEdgePrismTrimAndThickness(t *testing.T) {
	platform.WinWidth, platform.WinHeight = 800, 600
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	gfx.Cam.Position = mgl32.Vec3{0, 0, 6}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)

	p0 := mgl32.Vec3{-2, 0, 0}
	p1 := mgl32.Vec3{2, 0, 0}
	center, axes, half, ok := edgePrism(p0, p1)
	if !ok {
		t.Fatal("edgePrism rejected a 4-unit edge")
	}
	if d := center.Sub(mgl32.Vec3{0, 0, 0}).Len(); d > 1e-3 {
		t.Fatalf("prism centre %v is %v off the edge midpoint", center, d)
	}
	if a := float32(math.Abs(float64(axes[0].Dot(mgl32.Vec3{1, 0, 0})))); a < 0.999 {
		t.Fatalf("prism long axis %v does not run along the edge", axes[0])
	}

	// Each end must sit at least a vertex box away from its endpoint on screen,
	// which is what leaves the corner to the point.
	for _, end := range []struct {
		name     string
		endpoint mgl32.Vec3
		cap      mgl32.Vec3
	}{
		{"near -x", p0, center.Sub(axes[0].Mul(half[0]))},
		{"near +x", p1, center.Add(axes[0].Mul(half[0]))},
	} {
		a, okA := projectToScreen(projView, end.endpoint)
		b, okB := projectToScreen(projView, end.cap)
		if !okA || !okB {
			t.Fatalf("%s: endpoint or prism cap did not project", end.name)
		}
		if gap := b.Sub(a).Len(); gap < vertexHandleHalfPx-0.5 {
			t.Fatalf("%s: prism starts %.1f px from the vertex, want at least %.0f",
				end.name, gap, vertexHandleHalfPx)
		}
	}

	// And it is thinner across than a vertex box at the same distance.
	if box := worldSizeForPixels(center, vertexHandleHalfPx); half[1] >= box || half[2] >= box {
		t.Fatalf("prism cross-section %v/%v is not thinner than the vertex box half-extent %v",
			half[1], half[2], box)
	}

	// An edge shorter than the boxes capping it has no prism left to click.
	if _, _, _, ok := edgePrism(p0, p0.Add(mgl32.Vec3{0.01, 0, 0})); ok {
		t.Fatal("an edge swallowed by its endpoints' boxes still produced a prism")
	}

	// Neither does one pointing straight at the camera: it is a dot on screen,
	// covered by the handle boxes of the two points that define it.
	if _, _, _, ok := edgePrism(mgl32.Vec3{0, 0, 2}, mgl32.Vec3{0, 0, -2}); ok {
		t.Fatal("an edge along the line of sight still produced a clickable prism")
	}
}

// TestRayOBBIntersect covers the oriented-box ray test the prisms are picked
// with, including that the returned distance is the world-space one.
func TestRayOBBIntersect(t *testing.T) {
	// A thin box 5 units down -Z, long axis along Z.
	center := mgl32.Vec3{0, 0, -5}
	axes := [3]mgl32.Vec3{{0, 0, -1}, {1, 0, 0}, {0, 1, 0}}
	half := mgl32.Vec3{1, 0.1, 0.1}

	hit, dist := rayOBBIntersect(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, -1}, center, axes, half)
	if !hit || math.Abs(float64(dist-4)) > 1e-3 {
		t.Fatalf("head-on ray: hit=%v dist=%v, want a hit at ~4", hit, dist)
	}

	// Offset past the thin cross-section.
	if hit, _ := rayOBBIntersect(mgl32.Vec3{0.5, 0, 0}, mgl32.Vec3{0, 0, -1}, center, axes, half); hit {
		t.Fatal("a ray 0.5 units off a 0.1-thick box hit it")
	}

	// Behind the camera.
	if hit, _ := rayOBBIntersect(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, 1}, center, axes, half); hit {
		t.Fatal("a ray pointing away from the box hit it")
	}
}
