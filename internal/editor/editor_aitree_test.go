package editor

// editor_aitree_test.go — the AI editor's logic, without a window.
//
// Everything tested here is a pure function of the document and the view state,
// which is why those were kept out of the draw calls: the canvas's transforms,
// the hit test, the layout, the selection operations and the shortcut map can
// all be exercised with no GL context and no ImGui frame.

import (
	"testing"

	"foundry/internal/aitree"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// withAIDoc installs a document as the one being edited, and puts everything
// back afterwards — the editor's state is package-level, as every editor's is.
func withAIDoc(t *testing.T, d *aitree.Doc) {
	t.Helper()
	prevDoc, prevIdx, prevSel := aiDoc, aiTreeIndex, aiSelection
	prevPan, prevZoom := aiPan, aiZoom
	prevProg, prevRev := aiProg, aiProgRev
	t.Cleanup(func() {
		aiDoc, aiTreeIndex, aiSelection = prevDoc, prevIdx, prevSel
		aiPan, aiZoom = prevPan, prevZoom
		aiProg, aiProgRev = prevProg, prevRev
		AIClearUndoHistory()
	})
	aiDoc, aiTreeIndex, aiSelection = d, 0, map[string]bool{}
	aiPan, aiZoom = imgui.Vec2{}, 1
	aiProg, aiProgRev = nil, 0
	AIClearUndoHistory()
	aiRefreshProgram()
}

func aiTestDoc() *aitree.Doc {
	return &aitree.Doc{ID: "t", Trees: []*aitree.Tree{{
		Event: "damage", Args: []string{"src", "amount"}, Root: "n1",
		Nodes: []*aitree.Node{
			{ID: "n1", Kind: aitree.NodeCondition, X: 0, Y: 0, Then: "n2", Else: "n3",
				Check: aitree.Call{Verb: "has_target"}},
			{ID: "n2", Kind: aitree.NodeAction, X: 300, Y: 200,
				Do: aitree.Call{Verb: "stop"}},
			{ID: "n3", Kind: aitree.NodeAction, X: 600, Y: 200,
				Do: aitree.Call{Verb: "set", Fields: map[string]string{"var": "x", "value": "1"}}},
		},
	}}}
}

// ---------- the canvas's transforms ----------

func TestScreenAndDocumentAreInversesAtAnyZoom(t *testing.T) {
	origin := imgui.Vec2{X: 230, Y: 54}
	for _, z := range []float32{0.3, 1, 1.7, 2.5} {
		aiZoom, aiPan = z, imgui.Vec2{X: 12, Y: -40}
		p := imgui.Vec2{X: 123.5, Y: -87.25}
		back := aiToDoc(origin, aiToScreen(origin, p))
		if aiAbsF(back.X-p.X) > 0.01 || aiAbsF(back.Y-p.Y) > 0.01 {
			t.Fatalf("at zoom %v: %v came back as %v", z, p, back)
		}
	}
	aiZoom, aiPan = 1, imgui.Vec2{}
}

func TestHitTestPrefersAPortOverTheNodeUnderIt(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	tr := aiCurrentTree()
	n := tr.Node("n1")

	// The middle of the box is the box.
	node, port := aiHitTest(tr, imgui.Vec2{X: n.X + aiNodeW/2, Y: n.Y + 10})
	if node != "n1" || port != "" {
		t.Fatalf("middle of the box gave %q/%q", node, port)
	}
	// The `then` socket on its bottom edge is the port, although it is also
	// within a few pixels of the box.
	p := aiOutPort(n, "then")
	node, port = aiHitTest(tr, p)
	if node != "n1" || port != "then" {
		t.Fatalf("on the then socket gave %q/%q", node, port)
	}
}

func TestHitTestFindsTheNodeDrawnOnTop(t *testing.T) {
	d := aiTestDoc()
	// Two nodes exactly on top of each other: the later one is drawn over the
	// earlier, so a click has to reach it.
	d.Trees[0].Nodes[1].X, d.Trees[0].Nodes[1].Y = 0, 0
	withAIDoc(t, d)
	node, _ := aiHitTest(aiCurrentTree(), imgui.Vec2{X: aiNodeW / 2, Y: 10})
	if node != "n2" {
		t.Fatalf("got %q; the node drawn last is the one on top", node)
	}
}

// ---------- selection and editing ----------

func TestDeletingANodeUnhooksEveryWireIntoIt(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	aiSelectOnly("n2")
	aiDeleteSelection()

	tr := aiCurrentTree()
	if tr.Node("n2") != nil {
		t.Fatal("still there")
	}
	if tr.Node("n1").Then != "" {
		t.Fatalf("n1.then still points at %q", tr.Node("n1").Then)
	}
	if len(aiSelection) != 0 {
		t.Fatal("the selection should be empty after a delete")
	}
}

func TestDeletingTheRootPromotesAnotherNodeRatherThanLeavingNone(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	aiSelectOnly("n1")
	aiDeleteSelection()
	if aiCurrentTree().Root == "" {
		t.Fatal("a tree with nodes left in it should still have a root")
	}
}

func TestDuplicateKeepsLinksInsideTheSelectionAndDropsTheOnesOut(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	aiSelection = map[string]bool{"n1": true, "n2": true}
	aiDuplicateSelection()

	tr := aiCurrentTree()
	if len(tr.Nodes) != 5 {
		t.Fatalf("expected two more nodes, have %d", len(tr.Nodes))
	}
	if len(aiSelection) != 2 {
		t.Fatalf("the copies should be what is selected, got %v", aiSelection)
	}
	for id := range aiSelection {
		n := tr.Node(id)
		if n.Kind != aitree.NodeCondition {
			continue
		}
		// then pointed at n2, which was copied too: it must point at the copy.
		if n.Then == "" || n.Then == "n2" {
			t.Fatalf("the copy's then is %q; it should point at the other copy", n.Then)
		}
		if !aiSelection[n.Then] {
			t.Fatalf("the copy's then (%q) leaves the selection", n.Then)
		}
		// else pointed at n3, which was not: it is dropped.
		if n.Else != "" {
			t.Fatalf("the copy's else is %q; a link out of the selection is not copied", n.Else)
		}
	}
}

func TestAddingANodeWiresItIntoTheOneFreePortOfTheSelectedNode(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	tr := aiCurrentTree()
	tr.Node("n1").Then = ""
	aiSelectOnly("n1")
	aiMenuAt = imgui.Vec2{X: 40, Y: 400}
	aiAddNode(tr, aitree.NodeAction, "stop")

	if tr.Node("n1").Then == "" {
		t.Fatal("the new node should have been wired into the free port")
	}
	if !aiSelection[tr.Node("n1").Then] {
		t.Fatal("the new node should be what is selected")
	}
}

func TestAddingANodeToAnEmptyTreeMakesItTheRoot(t *testing.T) {
	d := &aitree.Doc{ID: "t", Trees: []*aitree.Tree{{Event: "spawn"}}}
	withAIDoc(t, d)
	tr := aiCurrentTree()
	aiAddNode(tr, aitree.NodeAction, "stop")
	if tr.Root == "" {
		t.Fatal("the first node on a tree is its root")
	}
}

func TestANoteIsNeverMadeTheRoot(t *testing.T) {
	d := &aitree.Doc{ID: "t", Trees: []*aitree.Tree{{Event: "spawn"}}}
	withAIDoc(t, d)
	tr := aiCurrentTree()
	aiAddNode(tr, aitree.NodeNote, "")
	if tr.Root != "" {
		t.Fatalf("root is %q; a note is not run, so it cannot be where a tree starts", tr.Root)
	}
}

// ---------- layout ----------

func TestTidyPutsEveryNodeBelowTheOneItComesFrom(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	// Scramble, so tidy has something to do.
	for _, n := range aiCurrentTree().Nodes {
		n.X, n.Y = 999, 999
	}
	aiTidyTree()

	tr := aiCurrentTree()
	root, then, els := tr.Node("n1"), tr.Node("n2"), tr.Node("n3")
	if !(root.Y < then.Y && root.Y < els.Y) {
		t.Fatalf("children should sit below their parent: %v %v %v", root.Y, then.Y, els.Y)
	}
	if then.X >= els.X {
		t.Fatalf("`then` goes on the left of `else`: %v vs %v", then.X, els.X)
	}
}

func TestTidyNeverChangesWhatTheTreeDoes(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	before := aitree.ToRaw(aiDoc)
	aiTidyTree()
	after := aitree.ToRaw(aiDoc)

	for i := range before.Trees {
		for j := range before.Trees[i].Nodes {
			b, a := before.Trees[i].Nodes[j], after.Trees[i].Nodes[j]
			if b.ID != a.ID || b.Then != a.Then || b.Else != a.Else || b.Next != a.Next {
				t.Fatalf("tidy changed a link: %#v -> %#v", b, a)
			}
		}
		if before.Trees[i].Root != after.Trees[i].Root {
			t.Fatal("tidy changed the root")
		}
	}
}

func TestTidyParksWhatTheRootCannotReachBelowEverythingElse(t *testing.T) {
	d := aiTestDoc()
	d.Trees[0].Nodes = append(d.Trees[0].Nodes, &aitree.Node{
		ID: "orphan", Kind: aitree.NodeAction, Do: aitree.Call{Verb: "stop"}})
	withAIDoc(t, d)
	aiTidyTree()

	tr := aiCurrentTree()
	orphan := tr.Node("orphan")
	for _, n := range tr.Nodes {
		if n.ID != "orphan" && n.Y >= orphan.Y {
			t.Fatalf("%s (y=%v) is not above the orphan (y=%v)", n.ID, n.Y, orphan.Y)
		}
	}
}

// ---------- undo ----------

func TestAIUndoSnapshotIsDeep(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	AIPushUndo()
	// Mutate every kind of shared structure a shallow clone would have kept.
	aiCurrentTree().Node("n3").Do.Set("value", "999")
	aiCurrentTree().Node("n1").Then = "changed"
	aiCurrentTree().Nodes[0].X = 12345
	aiDoc.Trees[0].Args[0] = "clobbered"
	AIUndo()

	tr := aiCurrentTree()
	if got := tr.Node("n3").Do.Field("value"); got != "1" {
		t.Fatalf("a field map was shared with the snapshot: %q", got)
	}
	if tr.Node("n1").Then != "n2" {
		t.Fatalf("a link was not restored: %q", tr.Node("n1").Then)
	}
	if tr.Nodes[0].X != 0 {
		t.Fatalf("a position was not restored: %v", tr.Nodes[0].X)
	}
	if tr.Args[0] != "src" {
		t.Fatalf("an argument list was shared with the snapshot: %q", tr.Args[0])
	}
}

func TestRedoPutsBackWhatUndoTookAway(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	AIPushUndo()
	aiCurrentTree().Node("n1").Then = "n3"
	AIUndo()
	if aiCurrentTree().Node("n1").Then != "n2" {
		t.Fatal("undo")
	}
	AIRedo()
	if aiCurrentTree().Node("n1").Then != "n3" {
		t.Fatal("redo")
	}
}

func TestUndoDropsASelectionOfNodesThatAreNoLongerThere(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	aiSelectOnly("n2")
	aiDeleteSelection()
	AIUndo()
	// n2 is back, and the snapshot's selection named it, so it is reselected.
	if !aiSelection["n2"] {
		t.Fatal("the node the undo brought back should be selected again")
	}

	AIPushUndo()
	aiSelectOnly("n2")
	aiCurrentTree().RemoveNode("n2")
	aiDoc.Touch()
	AIUndo()
	for id := range aiSelection {
		if aiCurrentTree().Node(id) == nil {
			t.Fatalf("%q is selected but is not in the tree", id)
		}
	}
}

func TestUndoNeverPutsTheRevisionCounterBack(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	AIPushUndo()
	aiDoc.Touch()
	rev := aiDoc.Rev
	AIUndo()
	if aiDoc.Rev <= rev {
		t.Fatalf("Rev went from %d to %d; it is the change signal the recompile and the save chip are keyed on, and it only goes up", rev, aiDoc.Rev)
	}
}

// ---------- the compile the canvas draws from ----------

func TestTheProgramIsRebuiltExactlyWhenTheDocumentChanges(t *testing.T) {
	d := aiTestDoc()
	withAIDoc(t, d)
	first := aiProg
	aiRefreshProgram()
	if aiProg != first {
		t.Fatal("nothing changed, so nothing should have been recompiled")
	}
	aiDoc.Touch()
	aiRefreshProgram()
	if aiProg == first {
		t.Fatal("the document changed and was not recompiled")
	}
}

// ---------- shortcuts ----------

func TestAIShortcutMap(t *testing.T) {
	cases := []struct {
		sc          sdl.Scancode
		ctrl, shift bool
		want        aiShortcut
	}{
		{sdl.SCANCODE_S, true, false, aiShortcutSave},
		{sdl.SCANCODE_Z, true, false, aiShortcutUndo},
		{sdl.SCANCODE_Z, true, true, aiShortcutRedo},
		{sdl.SCANCODE_Y, true, false, aiShortcutRedo},
		{sdl.SCANCODE_D, true, false, aiShortcutDuplicate},
		{sdl.SCANCODE_A, true, false, aiShortcutSelectAll},
		{sdl.SCANCODE_DELETE, false, false, aiShortcutDelete},
		{sdl.SCANCODE_F, false, false, aiShortcutFrameAll},
		// A bare letter that is a shortcut with Ctrl must not be one without:
		// typing in the canvas would delete the tree.
		{sdl.SCANCODE_D, false, false, aiShortcutNone},
		{sdl.SCANCODE_S, false, false, aiShortcutNone},
	}
	for _, c := range cases {
		if got := aiShortcutFor(c.sc, c.ctrl, c.shift); got != c.want {
			t.Errorf("%v ctrl=%v shift=%v: got %v, want %v", c.sc, c.ctrl, c.shift, got, c.want)
		}
	}
}

// ---------- what a box says ----------

func TestANodeShowsItsFilledInFieldsAndHidesTheDefaultSelf(t *testing.T) {
	n := &aitree.Node{Kind: aitree.NodeAction, Do: aitree.Call{
		Verb:   "send",
		Fields: map[string]string{"of": "@self", "to": "@self", "event": "grenade_check", "after": "470"},
		Args:   []string{"target"},
	}}
	rows := aiNodeRows(n)
	joined := ""
	for _, r := range rows {
		joined += r + "|"
	}
	for _, want := range []string{"event: grenade_check", "after: 470", "(target)"} {
		if !aiContains(joined, want) {
			t.Fatalf("a box should say %q; it says %q", want, joined)
		}
	}
	if aiContains(joined, "of: @self") {
		t.Fatalf("`of: @self` is the default on nearly every verb and should not be drawn: %q", joined)
	}
}

func TestATallerNodeIsDrawnTaller(t *testing.T) {
	small := &aitree.Node{Kind: aitree.NodeAction, Do: aitree.Call{Verb: "stop"}}
	big := &aitree.Node{Kind: aitree.NodeAction, Do: aitree.Call{
		Verb: "send", Fields: map[string]string{"tag": "enemy", "event": "alert", "within": "30", "where": "it != @self", "after": "100"}}}
	if aiNodeHeight(big) <= aiNodeHeight(small) {
		t.Fatalf("a node with more to say should be taller: %v vs %v", aiNodeHeight(big), aiNodeHeight(small))
	}
}

func aiContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
