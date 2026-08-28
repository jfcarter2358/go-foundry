package editor

// editor_ui_test.go — the UI editor's headless half.
//
// Everything here runs with no window, no GL and no imgui: the shortcut table,
// the drag arithmetic, the structural edits and undo. Those are the parts where
// being wrong is invisible until an author notices their widget creeping.

import (
	"testing"

	"foundry/internal/ui"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

const uiEps = 1e-3

func uiClose(a, b float32) bool { return a-b < uiEps && b-a < uiEps }

func uiRectClose(a, b ui.Rect) bool {
	return uiClose(a.X, b.X) && uiClose(a.Y, b.Y) && uiClose(a.W, b.W) && uiClose(a.H, b.H)
}

// uiTestDoc installs a document with one pinned child and returns that child.
// The area is 1000x800, so every number below is in those units.
func uiTestDoc(t *testing.T) *ui.Node {
	t.Helper()
	prevDoc, prevSel := uiDoc, uiSel
	t.Cleanup(func() { uiDoc, uiSel = prevDoc, prevSel; UIClearUndoHistory() })

	uiDoc = ui.NewScreen("test")
	n := ui.NewNode("box", ui.KindPanel)
	n.Rect.AnchorMin, n.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	n.Rect.Offset = [4]float32{100, 50, -300, -150} // 200x100 at (100,50)
	uiDoc.Root.Children = []ui.Node{n}
	uiSel = "box"
	UIClearUndoHistory()
	uiSolve()
	return uiDoc.Find("box")
}

func uiSolve() { uiDoc.SolveInto(ui.Rect{W: 1000, H: 800}) }

// ---------- shortcuts ----------

func TestUIEditorShortcutFor(t *testing.T) {
	cases := []struct {
		sc          sdl.Scancode
		ctrl, shift bool
		want        uiShortcut
	}{
		{sdl.SCANCODE_S, true, false, uiShortcutSave},
		{sdl.SCANCODE_S, true, true, uiShortcutSaveAs},
		{sdl.SCANCODE_O, true, false, uiShortcutOpen},
		{sdl.SCANCODE_N, true, false, uiShortcutNew},
		{sdl.SCANCODE_D, true, false, uiShortcutDuplicate},
		{sdl.SCANCODE_Z, true, false, uiShortcutUndo},
		{sdl.SCANCODE_Z, true, true, uiShortcutRedo},
		{sdl.SCANCODE_Y, true, false, uiShortcutRedo},
		{sdl.SCANCODE_DELETE, false, false, uiShortcutDelete},
		{sdl.SCANCODE_X, false, false, uiShortcutDelete},
		{sdl.SCANCODE_F, false, false, uiShortcutFrame},
		{sdl.SCANCODE_B, false, false, uiShortcutToggleOutlines},
		{sdl.SCANCODE_G, false, false, uiShortcutToggleGrid},
		// Ctrl changes the meaning entirely rather than being ignored.
		{sdl.SCANCODE_F, true, false, uiShortcutNone},
		{sdl.SCANCODE_Q, false, false, uiShortcutNone},
		// An unmodified S must not save; it is a movement key everywhere else.
		{sdl.SCANCODE_S, false, false, uiShortcutNone},
	}
	for _, tc := range cases {
		if got := uiEditorShortcutFor(tc.sc, tc.ctrl, tc.shift); got != tc.want {
			t.Errorf("scancode %v ctrl=%v shift=%v gave %v, want %v",
				tc.sc, tc.ctrl, tc.shift, got, tc.want)
		}
	}
}

// ---------- drag arithmetic ----------

func TestMoveDragKeepsTheSizeOnBothAxisKinds(t *testing.T) {
	// One formula covers pinned and stretched, because the anchor rect does not
	// move either way. Asserting both is what stops someone "fixing" it into
	// two cases that disagree.
	for _, tc := range []struct {
		name     string
		min, max mgl32.Vec2
	}{
		{"pinned", mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}},
		{"stretched", mgl32.Vec2{0, 0}, mgl32.Vec2{1, 1}},
		{"mixed", mgl32.Vec2{0, 0}, mgl32.Vec2{1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := uiTestDoc(t)
			n.Rect.AnchorMin, n.Rect.AnchorMax = tc.min, tc.max
			ui.SetAnchorsPreservingRect(n, ui.Rect{W: 1000, H: 800}, tc.min, tc.max)
			uiSolve()
			was := n.Solved()

			uiMoveNode(n, 30, -20)
			uiSolve()
			got := n.Solved()

			want := ui.Rect{X: was.X + 30, Y: was.Y - 20, W: was.W, H: was.H}
			if !uiRectClose(got, want) {
				t.Errorf("moved to %+v, want %+v", got, want)
			}
		})
	}
}

func TestResizeHandleOwnsTheRightEdges(t *testing.T) {
	// Each handle moves exactly the edges it is drawn on, and no others. A sign
	// slip here makes a rectangle that grows when dragged inward.
	cases := []struct {
		handle uiHandle
		dx, dy float32
		want   ui.Rect
	}{
		{uiHandleLeft, 10, 0, ui.Rect{X: 110, Y: 50, W: 190, H: 100}},
		{uiHandleRight, 10, 0, ui.Rect{X: 100, Y: 50, W: 210, H: 100}},
		{uiHandleTop, 0, 10, ui.Rect{X: 100, Y: 60, W: 200, H: 90}},
		{uiHandleBottom, 0, 10, ui.Rect{X: 100, Y: 50, W: 200, H: 110}},
		{uiHandleTopLeft, 10, 10, ui.Rect{X: 110, Y: 60, W: 190, H: 90}},
		{uiHandleTopRight, 10, 10, ui.Rect{X: 100, Y: 60, W: 210, H: 90}},
		{uiHandleBottomLeft, 10, 10, ui.Rect{X: 110, Y: 50, W: 190, H: 110}},
		{uiHandleBottomRight, 10, 10, ui.Rect{X: 100, Y: 50, W: 210, H: 110}},
	}
	for _, tc := range cases {
		n := uiTestDoc(t)
		uiResizeNode(n, tc.handle, tc.dx, tc.dy)
		uiSolve()
		if got := n.Solved(); !uiRectClose(got, tc.want) {
			t.Errorf("handle %d gave %+v, want %+v", tc.handle, got, tc.want)
		}
	}
}

func TestAnchorDragDoesNotMoveTheWidget(t *testing.T) {
	n := uiTestDoc(t)
	was := n.Solved()

	uiDragAnchorTo(n, uiAnchorMaxMax, 300, 200)
	uiSolve()

	if got := n.Solved(); !uiRectClose(got, was) {
		t.Errorf("dragging an anchor moved the widget: %+v -> %+v", was, got)
	}
	if n.Rect.AnchorMax == (mgl32.Vec2{0, 0}) {
		t.Error("the anchor did not actually move")
	}
}

func TestAnchorDragOnTheRootIsANoOp(t *testing.T) {
	// The root is the canvas. It has no parent to be a fraction of.
	uiTestDoc(t)
	root := &uiDoc.Root
	was := root.Rect
	uiDragAnchorTo(root, uiAnchorMaxMax, 100, 100)
	if root.Rect != was {
		t.Error("the root's anchors were changed")
	}
}

func TestSnapToSiblingEdgeWithinTolerance(t *testing.T) {
	uiTestDoc(t)
	sibling := ui.NewNode("other", ui.KindPanel)
	sibling.Rect.AnchorMin, sibling.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	sibling.Rect.Offset = [4]float32{400, 300, -600, -400} // left edge at 400
	uiDoc.Root.Children = append(uiDoc.Root.Children, sibling)

	n := uiDoc.Find("box")
	// Put the box's left edge 3 units from the sibling's, inside tolerance.
	uiMoveNode(n, 297, 0)
	uiSolve()
	uiSnapNode(n, uiSnapTolerance)
	uiSolve()

	if got := n.Solved().X; !uiClose(got, 400) {
		t.Errorf("left edge is at %v, want it snapped onto the sibling's 400", got)
	}
}

func TestSnapDoesNothingBeyondTolerance(t *testing.T) {
	uiTestDoc(t)
	sibling := ui.NewNode("other", ui.KindPanel)
	sibling.Rect.AnchorMin, sibling.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	sibling.Rect.Offset = [4]float32{400, 300, -600, -400}
	uiDoc.Root.Children = append(uiDoc.Root.Children, sibling)

	n := uiDoc.Find("box")
	uiMoveNode(n, 250, 0) // left edge at 350, well clear
	uiSolve()
	was := n.Solved()
	uiSnapNode(n, uiSnapTolerance)
	uiSolve()

	if !uiRectClose(n.Solved(), was) {
		t.Errorf("a distant edge snapped anyway: %+v -> %+v", was, n.Solved())
	}
}

func TestSnapReachesTheParentsEdgesAndCentre(t *testing.T) {
	n := uiTestDoc(t)
	// Centre the box on the parent's midpoint, 4 units out.
	uiMoveNode(n, 296, 0) // centre at 200+296 = 496, parent centre is 500
	uiSolve()
	uiSnapNode(n, uiSnapTolerance)
	uiSolve()

	r := n.Solved()
	if centre := r.X + r.W/2; !uiClose(centre, 500) {
		t.Errorf("centre is at %v, want it snapped onto the parent's 500", centre)
	}
}

func TestNearestSnapPicksTheSmallestMove(t *testing.T) {
	got, ok := uiNearestSnap([]float32{100}, []float32{104, 98, 130}, 6)
	if !ok {
		t.Fatal("nothing snapped")
	}
	if !uiClose(got, -2) {
		t.Errorf("snapped by %v, want -2 (the nearest candidate)", got)
	}
}

// ---------- structural edits ----------

func TestAddNodeGivesItAVisibleSizeAndUniqueID(t *testing.T) {
	uiTestDoc(t)
	uiSel = ""
	uiAddNode(ui.KindButton)

	n := uiSelected()
	if n == nil {
		t.Fatal("nothing was selected after adding")
	}
	uiSolve()
	if r := n.Solved(); r.W <= 0 || r.H <= 0 {
		t.Errorf("a new widget solved to %+v; it would be invisible", r)
	}

	uiAddNode(ui.KindButton)
	if second := uiSelected(); second == nil || second.ID == n.ID {
		t.Error("two new widgets share an id")
	}
}

func TestDuplicateRenamesTheWholeSubtree(t *testing.T) {
	// Two nodes sharing an id make every reference to one a coin flip, and the
	// format refuses to save it at all.
	uiTestDoc(t)
	child := ui.NewNode("label", ui.KindText)
	uiDoc.Find("box").Children = []ui.Node{child}

	uiSel = "box"
	uiDuplicateSelection()

	seen := map[string]int{}
	uiDoc.Root.Walk(func(_, n *ui.Node) bool { seen[n.ID]++; return true })
	for id, n := range seen {
		if n > 1 {
			t.Errorf("id %q appears %d times after a duplicate", id, n)
		}
	}
	if len(seen) != 5 { // root, box, label, and the two copies
		t.Errorf("%d nodes after duplicating a two-node subtree, want 5", len(seen))
	}
}

func TestDuplicateInsertsBesideTheOriginal(t *testing.T) {
	uiTestDoc(t)
	uiDoc.Root.Children = append(uiDoc.Root.Children, ui.NewNode("after", ui.KindPanel))
	uiSel = "box"
	uiDuplicateSelection()

	kids := uiDoc.Root.Children
	if len(kids) != 3 || kids[0].ID != "box" || kids[2].ID != "after" {
		t.Errorf("order is %v, want the copy between box and after", uiChildIDs(kids))
	}
	if kids[1].ID != uiSel {
		t.Errorf("the copy at index 1 is %q but %q is selected", kids[1].ID, uiSel)
	}
}

func TestStripSuffix(t *testing.T) {
	for in, want := range map[string]string{
		"btn":    "btn",
		"btn_2":  "btn",
		"btn_12": "btn",
		"btn2":   "btn2",
		"_2":     "_2",
		"a_b":    "a_b",
	} {
		if got := uiStripSuffix(in); got != want {
			t.Errorf("uiStripSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeleteSelectsSomethingSensible(t *testing.T) {
	uiTestDoc(t)
	uiDoc.Root.Children = append(uiDoc.Root.Children, ui.NewNode("second", ui.KindPanel))

	uiSel = "box"
	uiDeleteSelection()
	if uiSel != "second" {
		t.Errorf("after deleting the first child the selection is %q, want the one that took its place", uiSel)
	}

	uiDeleteSelection()
	if uiSel != uiDoc.Root.ID {
		t.Errorf("after deleting the last child the selection is %q, want the parent", uiSel)
	}
}

func TestDeleteRefusesTheRoot(t *testing.T) {
	uiTestDoc(t)
	uiSel = uiDoc.Root.ID
	uiDeleteSelection()
	if uiDoc.Find(uiDoc.Root.ID) == nil {
		t.Error("the root was deleted; it is the canvas and has nowhere to go")
	}
}

func TestMoveSelectionReordersTheDraw(t *testing.T) {
	uiTestDoc(t)
	uiDoc.Root.Children = append(uiDoc.Root.Children, ui.NewNode("second", ui.KindPanel))

	uiSel = "box"
	uiMoveSelection(1)
	if got := uiChildIDs(uiDoc.Root.Children); got[0] != "second" || got[1] != "box" {
		t.Errorf("order is %v after moving down", got)
	}
	uiMoveSelection(-1)
	if got := uiChildIDs(uiDoc.Root.Children); got[0] != "box" {
		t.Errorf("order is %v after moving back up", got)
	}
	// Past the ends is a no-op rather than a wrap or a panic.
	uiMoveSelection(-1)
	if got := uiChildIDs(uiDoc.Root.Children); got[0] != "box" {
		t.Errorf("moving past the start changed the order to %v", got)
	}
}

func TestReparentRefusesToMoveANodeIntoItself(t *testing.T) {
	// This would detach the branch from the document and lose it.
	uiTestDoc(t)
	child := ui.NewNode("inner", ui.KindPanel)
	uiDoc.Find("box").Children = []ui.Node{child}

	if uiReparent("box", "inner") {
		t.Error("a node was moved into its own child")
	}
	if uiDoc.Find("box") == nil || uiDoc.Find("inner") == nil {
		t.Error("the subtree was lost")
	}
}

func TestReparentMovesTheSubtree(t *testing.T) {
	uiTestDoc(t)
	uiDoc.Root.Children = append(uiDoc.Root.Children, ui.NewNode("host", ui.KindPanel))

	if !uiReparent("box", "host") {
		t.Fatal("reparent refused")
	}
	host := uiDoc.Find("host")
	if len(host.Children) != 1 || host.Children[0].ID != "box" {
		t.Errorf("host has %v, want box", uiChildIDs(host.Children))
	}
	if len(uiDoc.Root.Children) != 1 {
		t.Errorf("the root still has %v", uiChildIDs(uiDoc.Root.Children))
	}
}

func TestWrapInPanelKeepsTheWidgetWhereItWas(t *testing.T) {
	n := uiTestDoc(t)
	was := n.Solved()

	uiWrapInPanel()
	uiSolve()

	inner := uiDoc.Find("box")
	if inner == nil {
		t.Fatal("the wrapped node went missing")
	}
	if got := inner.Solved(); !uiRectClose(got, was) {
		t.Errorf("wrapping moved the widget: %+v -> %+v", was, got)
	}
	if uiParentOf(inner) == &uiDoc.Root {
		t.Error("no panel was inserted")
	}
}

// ---------- undo ----------

func TestDragCoalescesToOneUndoEntry(t *testing.T) {
	uiTestDoc(t)
	for i := 0; i < 10; i++ {
		uiBeginEdit("canvas.move")
	}
	if n := uiUndoStack.len(); n != 1 {
		t.Errorf("one gesture pushed %d undo entries", n)
	}

	// A different control starts a new entry.
	uiBeginEdit("canvas.resize")
	if n := uiUndoStack.len(); n != 2 {
		t.Errorf("a second gesture gave %d entries, want 2", n)
	}
}

func TestUndoRestoresTheDocumentAndTheSelection(t *testing.T) {
	uiTestDoc(t)
	uiSel = "box"
	uiAddNode(ui.KindButton)
	added := uiSel

	UIUndo()
	if uiDoc.Find(added) != nil {
		t.Error("the added node survived the undo")
	}
	if uiSel != "box" {
		t.Errorf("the selection is %q after undo, want the one from before the add", uiSel)
	}

	UIRedo()
	if uiDoc.Find(added) == nil {
		t.Error("redo did not put the node back")
	}
}

func TestUndoClearsASelectionThatNoLongerExists(t *testing.T) {
	// Undoing past a node's creation leaves the panel pointing at something
	// that is gone. A dangling name is a blank panel; a dangling pointer would
	// be a crash.
	uiTestDoc(t)
	uiSel = ""
	uiAddNode(ui.KindButton)
	added := uiSel

	UIPushUndo()
	uiDoc.Root.Children = nil
	uiSel = added

	UIUndo() // back to "children cleared"? no — back to the state before the clear
	UIUndo() // and back past the add
	if uiSel != "" && uiDoc.Find(uiSel) == nil {
		t.Errorf("selection %q survived as a dangling name", uiSel)
	}
}

func TestClearUndoHistoryDropsBothStacks(t *testing.T) {
	uiTestDoc(t)
	uiAddNode(ui.KindPanel)
	UIUndo()
	if !UICanRedo() {
		t.Fatal("nothing to redo")
	}
	UIClearUndoHistory()
	if UICanUndo() || UICanRedo() {
		t.Error("history survived a document swap; it would restore one screen's nodes into another's tree")
	}
}

// ---------- sample data ----------

func TestSampleValuesFollowTheBindingsName(t *testing.T) {
	// An author checking a health bar should see a health bar, not 0.5 in every
	// widget on the screen.
	if v, _ := uiSampleValue("@player.attr.health_max"); v.AsNum() != 100 {
		t.Errorf("a max binding gave %v, want a plausible ceiling", v.AsNum())
	}
	if v, _ := uiSampleValue("@player.attr.name"); v.Kind != ui.ValueStr {
		t.Errorf("a name binding gave a %v, want a string", v.Kind)
	}
	if v, _ := uiSampleValue("@level.prop.load_progress"); v.AsNum() < 0 || v.AsNum() > 1 {
		t.Errorf("a progress binding gave %v, want 0..1", v.AsNum())
	}
	if v, _ := uiSampleValue("Health"); v.AsNum() <= 0 {
		t.Error("matching is case sensitive; it should not be")
	}
}

func TestLiveResolverDeclinesWithNoSession(t *testing.T) {
	prev := uiLiveBindings
	t.Cleanup(func() { uiLiveBindings = prev })
	uiLiveBindings = nil

	if _, ok := uiLiveResolver("anything"); ok {
		t.Error("live data was claimed with no session; every widget would show zeros that look real")
	}
}

func uiChildIDs(kids []ui.Node) []string {
	out := make([]string, len(kids))
	for i := range kids {
		out[i] = kids[i].ID
	}
	return out
}

// ---------- the canvas view transform ----------

func TestCanvasViewRoundTrips(t *testing.T) {
	// A click has to land on the widget under the cursor, which means the two
	// directions must be exact inverses at every pan and zoom.
	canvas := ui.Rect{X: 230, Y: 54, W: 900, H: 700}
	area := ui.Rect{W: 1920, H: 1080}

	for _, v := range []uiCanvasView{
		{zoom: 1},
		{zoom: 0.25},
		{zoom: 4},
		{pan: mgl32.Vec2{-120, 60}, zoom: 1.5},
	} {
		for _, p := range []mgl32.Vec2{{0, 0}, {960, 540}, {1920, 1080}, {-40, 2000}} {
			screen := v.designToScreen(p, canvas, area)
			back := v.screenToDesign(screen, canvas, area)
			if !uiClose(back[0], p[0]) || !uiClose(back[1], p[1]) {
				t.Errorf("view %+v: %v -> %v -> %v", v, p, screen, back)
			}
		}
	}
}

func TestCanvasViewCentresTheDocument(t *testing.T) {
	canvas := ui.Rect{X: 0, Y: 0, W: 1000, H: 1000}
	area := ui.Rect{W: 1000, H: 500}
	v := uiCanvasView{zoom: 1}

	tl := v.designToScreen(mgl32.Vec2{0, 0}, canvas, area)
	br := v.designToScreen(mgl32.Vec2{area.W, area.H}, canvas, area)

	if left, right := tl[0]-canvas.X, canvas.X+canvas.W-br[0]; !uiClose(left, right) {
		t.Errorf("document is not horizontally centred: %v left, %v right", left, right)
	}
	if top, bottom := tl[1]-canvas.Y, canvas.Y+canvas.H-br[1]; !uiClose(top, bottom) {
		t.Errorf("document is not vertically centred: %v top, %v bottom", top, bottom)
	}
}

func TestCanvasViewFitsTheNarrowerAxis(t *testing.T) {
	// A wide document in a tall viewport must fit by width, not overflow it.
	canvas := ui.Rect{W: 400, H: 1000}
	area := ui.Rect{W: 1920, H: 1080}
	v := uiCanvasView{zoom: 1}

	br := v.designToScreen(mgl32.Vec2{area.W, area.H}, canvas, area)
	if br[0] > canvas.W+uiEps {
		t.Errorf("document reaches x %v, past the %v-wide viewport", br[0], canvas.W)
	}
	if br[1] > canvas.H+uiEps {
		t.Errorf("document reaches y %v, past the %v-tall viewport", br[1], canvas.H)
	}
}

func TestScreenToDesignSizeIsTheInverseOfTheScale(t *testing.T) {
	canvas := ui.Rect{W: 960, H: 540}
	area := ui.Rect{W: 1920, H: 1080}
	v := uiCanvasView{zoom: 2}

	s := v.scale(canvas, area)
	if got := v.screenToDesignSize(s*37, canvas, area); !uiClose(got, 37) {
		t.Errorf("37 document units round-tripped to %v", got)
	}
}

func TestHandleAndAnchorHitTests(t *testing.T) {
	r := ui.Rect{X: 100, Y: 100, W: 200, H: 80}
	rects := uiHandleRects(r)

	for i := range rects {
		c := mgl32.Vec2{rects[i].X + rects[i].W/2, rects[i].Y + rects[i].H/2}
		got, ok := uiHandleAt(r, c)
		if !ok || got != uiHandle(i) {
			t.Errorf("the centre of handle %d hit %v (found=%v)", i, got, ok)
		}
	}
	if _, ok := uiHandleAt(r, mgl32.Vec2{200, 140}); ok {
		t.Error("the middle of the rect hit a resize handle")
	}
}
